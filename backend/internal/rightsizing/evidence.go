package rightsizing

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/metrics"
)

// probeTTL is how long a cluster's optional-signal probe is reused.
const probeTTL = 10 * time.Minute

type probeEntry struct {
	p  probe
	at time.Time
}

var probeCache sync.Map // history cluster -> probeEntry

// probeEpoch moves on whenever ForgetCluster clears probes, so a probe that
// was already running against the old metrics settings isn't cached after.
var probeEpoch atomic.Uint64

func storeProbe(key string, e probeEntry, epoch uint64) {
	if probeEpoch.Load() == epoch {
		probeCache.Store(key, e)
	}
}

func (s *Service) cachedProbe(ctx context.Context, cluster string, at time.Time) probe {
	key := probeKey(ctx, cluster)
	if e, ok := probeCache.Load(key); ok && s.now().Sub(e.(probeEntry).at) < probeTTL {
		return e.(probeEntry).p
	}
	epoch := probeEpoch.Load()
	p := probeSignals(ctx, newControlled(s.metrics), cluster, at)
	storeProbe(key, probeEntry{p, s.now()}, epoch)
	return p
}

// probeKey keeps each history store's signals apart.
func probeKey(ctx context.Context, cluster string) string {
	return cluster + "|source=" + historySource(ctx)
}

// withSourceOf records in ctx which store answers the cluster's history, so
// cached days are keyed by it.
func (s *Service) withSourceOf(ctx context.Context, cluster string) context.Context {
	if k, ok := s.metrics.(interface {
		HistorySourceKey(ctx context.Context, cluster string) string
	}); ok {
		return withHistorySource(ctx, k.HistorySourceKey(ctx, cluster))
	}
	return ctx
}

// WorkloadQuery names one workload as reports do.
type WorkloadQuery struct {
	Cluster, Namespace, VClusterNamespace, Kind, Name string
	Provider                                          string
	Profile                                           Profile
	Window                                            time.Duration
	CacheOnly, Refresh                                bool
}

// evidenceInputs is what a report knew besides history, kept with it so the
// evidence drawer can show its numbers straight from the day-chunk cache
// without asking Kubernetes or the metrics store anything.
type evidenceInputs struct {
	workloads []*liveWorkload
	probe     probe
	hpas      map[string]*hpaTarget
	rates     map[string]finops.Rates
	source    string // history store the report's chunks are keyed by

	mu     sync.Mutex
	starts map[string]map[string]int64 // pod start times per host namespace
}

func (e *evidenceInputs) setStarts(namespace string, starts map[string]int64) {
	if starts == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.starts == nil {
		e.starts = map[string]map[string]int64{}
	}
	e.starts[namespace] = starts
}

func (e *evidenceInputs) startsIn(namespace string) map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.starts[namespace]
}

// GetEvidence recomputes one workload's analysis from its namespace's history
// and returns everything the evidence drawer shows. It aligns to the cached
// report's time so its numbers match the row the user clicked.
//
// With CacheOnly it reads nothing but the report's stored days, including the
// day in progress the report stored for its own grid, and fails with
// errNotCached when they're missing; the drawer shows that at once and then
// asks again without it for fresh numbers.
func (s *Service) GetEvidence(ctx context.Context, q WorkloadQuery) (*Evidence, error) {
	ctx = metrics.WithHistoryProvider(ctx, q.Provider)
	t := resolveTarget(q.Cluster)
	rep := s.Cached(q.Cluster, q.Profile, q.Window, q.Provider)
	var workloads []*liveWorkload
	var pr probe
	var hpas map[string]*hpaTarget
	var rates map[string]finops.Rates
	var inputs *evidenceInputs
	asOf := s.now()
	if !q.Refresh && rep != nil && rep.Status == StatusReady && (q.CacheOnly || s.now().Sub(rep.ComputedAt) < reportTTL) {
		asOf = rep.AsOf
	}
	g := newGrid(asOf, q.Window, stepFor(q.Window))
	if q.CacheOnly {
		if rep == nil || rep.Status != StatusReady || rep.evidenceInputs == nil {
			return nil, errNotCached
		}
		inputs = rep.evidenceInputs
		ctx = withChunksOnly(withHistorySource(ctx, inputs.source))
		workloads, pr, hpas, rates = inputs.workloads, inputs.probe, inputs.hpas, inputs.rates
	} else {
		info, _ := s.metrics.HistorySource(ctx, t.history)
		if info == nil || !info.Found {
			return nil, fmt.Errorf("no Prometheus-compatible metrics store in this cluster")
		}
		ctx = s.withSourceOf(ctx, t.history)
		pods, err := s.listPods(ctx, t)
		if err != nil {
			return nil, err
		}
		workloads = liveWorkloads(pods, t.vcluster != "")
		pr = s.cachedProbe(ctx, t.history, g.end)
	}
	var w *liveWorkload
	for _, lw := range workloads {
		if lw.ref.Namespace == q.Namespace && lw.ref.VClusterNamespace == q.VClusterNamespace && lw.ref.Kind == q.Kind && lw.ref.Name == q.Name {
			w = lw
			break
		}
	}
	if w == nil {
		return nil, fmt.Errorf("workload %s/%s %s not found among running pods", q.Namespace, q.Name, q.Kind)
	}
	step := g.step

	wkSet := map[string]struct{}{}
	want := map[seriesKey]struct{}{}
	for _, keys := range w.keys {
		for k := range keys {
			wkSet[k.wk] = struct{}{}
			want[k] = struct{}{}
		}
	}
	wks := slices.Sorted(maps.Keys(wkSet))
	var jobs map[seriesKey]struct{}
	if w.isJob {
		jobs = want
	}
	// Someone is waiting on this one: it goes ahead of background reports.
	ctx = interactive(ctx)
	sc := scope{namespace: w.hostNamespace, wks: wks, want: want, jobs: jobs}
	if inputs != nil {
		sc.starts = inputs.startsIn(w.hostNamespace)
	}
	h, err := fetchHistory(ctx, newChunker(newControlled(s.metrics), s.chunks()), t.history, sc, g, pr, q.Window)
	if err != nil {
		return nil, err
	}

	if q.CacheOnly && (len(h.cpu) == 0 || len(h.mem) == 0) {
		return nil, errNotCached
	}
	if !q.CacheOnly {
		if cs, err := s.k8s.GetClientForCluster(q.Cluster); err == nil {
			hpas = hpaTargets(listHPAs(ctx, cs))
			pr.inPlace = supportsInPlaceResize(cs)
		}
		rates, _ = s.rates.NodeRates(ctx, q.Cluster)
	}
	var prev map[recKey]prevRec
	if rep != nil {
		prev = previousRecs(rep)
	}

	wr := s.analyzeWorkload(q.Cluster, w, h, g, pr, q.Profile, hpas, rates, prev)
	if wr == nil {
		return nil, fmt.Errorf("no containers")
	}
	applyDismissals(wr, s.dismissalIndex(q.Cluster))

	ev := &Evidence{
		Workload: *wr,
		Step:     fmtStep(step),
		Series:   map[string]Hourly{},
		Dist:     map[string]Dist{},
		Events:   map[string][]Event{},
		Profiles: map[string]Snapshots{},
		AsOf:     g.end,
	}
	for _, c := range w.containers {
		hs := containerHistory(h, w.keys[c.name], g.n)
		oom, restarts := hs.oom, hs.restarts
		ev.Series[c.name] = hourly(g, hs.cpu, hs.mem, hs.cpuReq, hs.memReq, h.requestsGrid)
		ev.Dist[c.name] = distribution(hs.cpu, hs.mem, step, q.Profile)

		snaps := Snapshots{}
		for _, p := range []Profile{ProfileConservative, ProfileBalanced, ProfileAggressive} {
			in := containerInput{
				container: c.name, start: g.start, step: g.step,
				cpu: hs.cpu, podMeans: hs.podMeans, mem: hs.mem, throttle: hs.throttle, cpuBurst: hs.burst, throttleKind: pr.throttle,
				hpa:      hpaFor(hpas, w),
				oomTimes: oom, restartTimes: restarts, startupCPUPeak: math.NaN(), memIsUsage: pr.memMetric == memUsage,
				current: c.res, isJob: w.isJob, profile: p, seed: seedFor(q.Cluster, w.id(), c.name),
				jvm: c.jvm, versionSince: versionSince(w.newestHash, hs.podFirst), inPlaceResize: pr.inPlace,
			}
			r := analyze(in)
			snaps[p] = ProfileRec{CPU: r.CPU.Recommended, Memory: r.Memory.Recommended}
		}
		ev.Profiles[c.name] = snaps

		var evs []Event
		oomAt := map[int64]bool{}
		for _, t := range oom {
			oomAt[t.Unix()] = true
			evs = append(evs, Event{At: t, Kind: "oom", Text: "OOM-killed"})
		}
		for _, t := range restarts {
			if !oomAt[t.Unix()] {
				evs = append(evs, Event{At: t, Kind: "restart", Text: "Restarted"})
			}
		}
		for _, cr := range wr.Containers {
			if cr.Container != c.name {
				continue
			}
			if cr.CPU.ShiftAt != nil {
				evs = append(evs, Event{At: *cr.CPU.ShiftAt, Kind: "shift-cpu", Text: fmt.Sprintf("CPU changed %.1f×", cr.CPU.ShiftRatio)})
			}
			if cr.Memory.ShiftAt != nil {
				evs = append(evs, Event{At: *cr.Memory.ShiftAt, Kind: "shift-memory", Text: fmt.Sprintf("Memory changed %.1f×", cr.Memory.ShiftRatio)})
			}
		}
		for _, rp := range ev.Series[c.name].Requests[min(1, len(ev.Series[c.name].Requests)):] {
			evs = append(evs, Event{At: rp.At, Kind: "request-change", Text: fmt.Sprintf("Requests set to %s / %s", fmtCores(rp.CPU), fmtBytes(rp.Mem))})
		}
		slices.SortFunc(evs, func(a, b Event) int { return a.At.Compare(b.At) })
		ev.Events[c.name] = evs
	}
	return ev, nil
}

// hpaFor is the HPA a workload's report would use, as analyzeWorkload picks it.
func hpaFor(hpas map[string]*hpaTarget, w *liveWorkload) *hpaTarget {
	if w.ref.VClusterNamespace != "" {
		return nil
	}
	return hpas[w.ref.Namespace+"/"+w.ref.Kind+"/"+w.ref.Name]
}

// hourly downsamples to one point per hour: the median and P95 of every
// replica's CPU samples in the hour, the busiest replica's CPU, the peak
// memory and the mean replica count.
func hourly(g grid, cpu *pooled, mem, cpuReq, memReq []float64, rg grid) Hourly {
	busiest, _, replicas := cpu.derive()
	per := max(int(time.Hour/g.step), 1)
	n := (g.n + per - 1) / per
	out := Hourly{
		Start:    g.start,
		CPUP50:   make(Floats, n),
		CPUP95:   make(Floats, n),
		CPUMax:   make(Floats, n),
		MemMax:   make(Floats, n),
		Replicas: make(Floats, n),
	}
	for b := range n {
		lo, hi := b*per, min((b+1)*per, g.n)
		cs := sortedCopy(cpu.span(lo, hi))
		var ms, rs, bs []float64
		for i := lo; i < hi; i++ {
			if !math.IsNaN(mem[i]) {
				ms = append(ms, mem[i])
			}
			if !math.IsNaN(replicas[i]) {
				rs = append(rs, replicas[i])
				bs = append(bs, busiest[i])
			}
		}
		out.CPUP50[b], out.CPUP95[b], out.CPUMax[b], out.MemMax[b], out.Replicas[b] = math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()
		if len(cs) > 0 {
			out.CPUP50[b] = quantileSorted(cs, 0.5)
			out.CPUP95[b] = quantileSorted(cs, 0.95)
			out.CPUMax[b] = maxOf(bs)
			out.Replicas[b] = mean(rs)
		}
		if len(ms) > 0 {
			out.MemMax[b] = maxOf(ms)
		}
	}
	// Request changes, as steps.
	lastC, lastM := math.NaN(), math.NaN()
	for i := range max(len(cpuReq), len(memReq)) {
		c, m := at(cpuReq, i), at(memReq, i)
		if math.IsNaN(c) && math.IsNaN(m) {
			continue
		}
		c, m = nanOr(c, lastC), nanOr(m, lastM)
		if !near(c, lastC) || !near(m, lastM) {
			out.Requests = append(out.Requests, ReqPoint{At: rg.start.Add(time.Duration(i) * rg.step), CPU: nanTo(c, 0), Mem: nanTo(m, 0)})
		}
		lastC, lastM = c, m
	}
	return out
}

func at(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return math.NaN()
}

func nanOr(v, def float64) float64 {
	if math.IsNaN(v) {
		return def
	}
	return v
}

func near(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) == math.IsNaN(b)
	}
	return math.Abs(a-b) <= 0.01*math.Max(math.Abs(b), 1e-9)
}

// distQ is the quantile grid the slider reads: every half percent, plus the
// tail in tenths of a percent where requests are actually decided.
var distQ = func() []float64 {
	var q []float64
	for i := 0; i <= 198; i++ {
		q = append(q, float64(i)*0.005)
	}
	for i := 0; i <= 10; i++ {
		q = append(q, 0.99+float64(i)*0.001)
	}
	return q
}()

// distribution summarises the data the recommendation used (after any regime
// change) as a quantile grid, plus the daily series behind shifts and trends.
func distribution(cpu *pooled, mem []float64, step time.Duration, profile Profile) Dist {
	cb, perDay := pooledBlocks(cpu, step)
	mb, _ := seriesBlocks(mem, step)
	cb, mb = sortBlocks(cb), sortBlocks(mb)
	ce := estimateCPU(cb, perDay, profile.params().cpuQuantile)
	me := estimateMem(mb, perDay)
	d := Dist{Q: distQ, CPU: make(Floats, len(distQ)), Mem: make(Floats, len(distQ))}
	for i, q := range distQ {
		d.CPU[i] = quantileSorted(ce.sorted, q)
		d.Mem[i] = quantileSorted(me.sorted, q)
	}
	daily, _ := dailyStat(cb[ce.from:], perDay, func(s []float64) float64 { return quantileSorted(s, 0.95) })
	d.DailyCPU = daily
	d.DailyPeaks = me.dailyPeaks
	return d
}
