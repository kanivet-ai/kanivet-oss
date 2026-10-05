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

// cachedProbe is the store's probe, asked again once probeTTL has passed. If
// asking fails it falls back to the last good probe or rep's signals.
func (s *Service) cachedProbe(ctx context.Context, cluster string, at time.Time, rep *Report) (probe, error) {
	key := probeKey(ctx, cluster)
	if e, ok := probeCache.Load(key); ok && s.now().Sub(e.(probeEntry).at) < probeTTL {
		return e.(probeEntry).p, nil
	}
	epoch := probeEpoch.Load()
	p := probeSignals(ctx, newControlled(safeQuerier{s.metrics}), cluster, at)
	if p.err != nil {
		if last, ok := lastProbe(key, rep); ok {
			return last, nil
		}
		return probe{}, fmt.Errorf("could not check which signals the metrics store has: %w", p.err)
	}
	storeProbe(key, probeEntry{p, s.now()}, epoch)
	return p, nil
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
	// workloads is nil once restored with a persisted report: the local pod
	// cache has the pods then.
	workloads []*liveWorkload
	probe     probe
	hpas      map[string]*hpaTarget
	rates     map[string]finops.Rates
	source    string // history store the report's chunks are keyed by
	// now is the report's clock, which decided the days it fetched whole and
	// the day in progress it stored as a tail.
	now time.Time

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

// savedInputs is evidenceInputs as persisted next to its report, so cached
// evidence works after a restart too. Workloads are left out: they're most of
// the size, and the local pod cache has them again.
type savedInputs struct {
	AsOf   time.Time                   `json:"asOf"` // the report's, to match them up
	Now    time.Time                   `json:"now"`
	Source string                      `json:"source"`
	Probe  savedProbe                  `json:"probe"`
	HPAs   map[string]savedHPA         `json:"hpas,omitempty"`
	Rates  map[string]finops.Rates     `json:"rates,omitempty"`
	Starts map[string]map[string]int64 `json:"starts,omitempty"`
}

type savedProbe struct {
	KSM       bool   `json:"ksm"`
	StartTime bool   `json:"startTime"`
	Requests  bool   `json:"requests"`
	InPlace   bool   `json:"inPlace"`
	MemMetric string `json:"memMetric"`
	Throttle  string `json:"throttle,omitempty"`
}

type savedHPA struct {
	Resource    string `json:"resource"`
	Utilization int32  `json:"utilization"`
	Name        string `json:"name"`
}

// inputsKey is where a report's evidence inputs are persisted, next to it.
func inputsKey(key string) string { return key + "|evidence-inputs" }

func (e *evidenceInputs) saved(asOf time.Time) savedInputs {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.probe
	out := savedInputs{
		AsOf: asOf, Now: e.now, Source: e.source, Rates: e.rates, Starts: e.starts,
		Probe: savedProbe{KSM: p.ksm, StartTime: p.startTime, Requests: p.requests, InPlace: p.inPlace, MemMetric: p.memMetric, Throttle: p.throttle},
	}
	if len(e.hpas) > 0 {
		out.HPAs = make(map[string]savedHPA, len(e.hpas))
		for k, h := range e.hpas {
			out.HPAs[k] = savedHPA{Resource: h.resource, Utilization: h.utilization, Name: h.name}
		}
	}
	return out
}

// loadInputs reads back the evidence inputs persisted with the report of
// grid end asOf; nil if there are none, or they belong to another report.
func (s *Service) loadInputs(key string, asOf time.Time) *evidenceInputs {
	data, err := s.store.GetRightsizingReport(inputsKey(key))
	if err != nil || len(data) == 0 {
		return nil
	}
	var in savedInputs
	if unpackJSON(data, &in) != nil || !in.AsOf.Equal(asOf) {
		return nil
	}
	p := in.Probe
	e := &evidenceInputs{
		probe:  probe{cpuSeries: 1, ksm: p.KSM, startTime: p.StartTime, requests: p.Requests, inPlace: p.InPlace, memMetric: p.MemMetric, throttle: p.Throttle},
		rates:  in.Rates,
		source: in.Source,
		now:    in.Now,
		starts: in.Starts,
	}
	if len(in.HPAs) > 0 {
		e.hpas = make(map[string]*hpaTarget, len(in.HPAs))
		for k, h := range in.HPAs {
			e.hpas[k] = &hpaTarget{resource: h.Resource, utilization: h.Utilization, name: h.Name}
		}
	}
	return e
}

// podCacheWait bounds how long cached evidence waits for the local pod cache,
// which may still be loading just after a restart.
const podCacheWait = 2 * time.Second

// cachedWorkloads lists the workloads for cached evidence whose inputs came
// back without them. Only the local pod cache may answer: cached evidence
// must not wait on the API server.
func (s *Service) cachedWorkloads(ctx context.Context, t target) ([]*liveWorkload, error) {
	if s.pods == nil {
		return nil, errNotCached
	}
	ctx, cancel := context.WithTimeout(ctx, podCacheWait)
	defer cancel()
	pods, err := s.listPods(ctx, t)
	if err != nil {
		return nil, errNotCached
	}
	return liveWorkloads(pods, t.vcluster != ""), nil
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
	// Someone is waiting on this one: its queries, the probe's included, go
	// ahead of background reports.
	ctx = interactive(metrics.WithHistoryProvider(ctx, q.Provider))
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
		if workloads == nil {
			var err error
			if workloads, err = s.cachedWorkloads(ctx, t); err != nil {
				return nil, err
			}
		}
	} else {
		// While the breaker holds queries back, say so at once: the queries
		// below would fail with ErrStoreBusy anyway, and the cluster probe,
		// which runs at background priority, would wait out the pause first.
		if limiterFor(t.history).state().PausedFor > interactiveWait {
			return nil, ErrStoreBusy
		}
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
		if pr, err = s.cachedProbe(ctx, t.history, g.end, rep); err != nil {
			return nil, err
		}
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
	sc := scope{namespace: w.hostNamespace, wks: wks, want: want, jobs: jobs}
	if inputs != nil {
		sc.starts = inputs.startsIn(w.hostNamespace)
	}
	ch := newChunker(newControlled(safeQuerier{s.metrics}), s.chunks())
	ch.now = s.now
	if inputs != nil {
		// Read the report's days as the report classified them: once its last
		// day is past midnight that day is final, but what it stored is the
		// day's tail, under its run.
		ch.now = func() time.Time { return inputs.now }
		ch.run = runKey(q.Cluster, q.Profile, q.Window, q.Provider)
	}
	// HPAs, the server version and node prices don't depend on history: they
	// are asked for while it loads, not after. Their goroutines are outside
	// the request handler's recovery, so a panic in one fails the request
	// here rather than the backend.
	var (
		side    sync.WaitGroup
		sideMu  sync.Mutex
		sideErr error
	)
	sideCtx, cancelSide := context.WithCancel(ctx)
	defer cancelSide()
	alongside := func(f func()) {
		side.Add(1)
		go func() {
			defer side.Done()
			defer func() {
				if r := recover(); r != nil {
					err := panicked("loading evidence", r)
					sideMu.Lock()
					sideErr = err
					sideMu.Unlock()
				}
			}()
			f()
		}()
	}
	inPlace := pr.inPlace
	if !q.CacheOnly {
		if cs, err := s.k8s.GetClientForCluster(q.Cluster); err == nil {
			alongside(func() { hpas = hpaTargets(listHPAs(sideCtx, cs)) })
			alongside(func() { inPlace = supportsInPlaceResize(cs) })
		}
		alongside(func() { rates, _ = s.rates.NodeRates(sideCtx, q.Cluster) })
	}
	h, err := fetchHistory(ctx, ch, t.history, sc, g, pr, q.Window)
	if err != nil {
		cancelSide()
	}
	side.Wait()
	if err == nil {
		err = sideErr
	}
	if err != nil {
		return nil, err
	}
	pr.inPlace = inPlace

	if q.CacheOnly && (len(h.cpu) == 0 || len(h.mem) == 0) {
		return nil, errNotCached
	}
	if !q.CacheOnly {
		// A pause that began while we fetched cut the optional series short,
		// and restarts or OOM kills that failed to load would read as none:
		// say the store is busy rather than show evidence without them.
		if limiterFor(t.history).state().PausedFor > interactiveWait {
			return nil, ErrStoreBusy
		}
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

		// Never nil: a container without events is an empty list, not null.
		evs := []Event{}
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
