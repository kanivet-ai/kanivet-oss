package rightsizing

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

// historyQuerier is the slice of metrics.Service rightsizing reads through.
type historyQuerier interface {
	QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error)
	QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error)
}

// grid is the time axis every series is aligned to: n samples, step apart,
// the last one at end.
type grid struct {
	start, end time.Time
	step       time.Duration
	n          int
}

func newGrid(asOf time.Time, window, step time.Duration) grid {
	end := asOf.Truncate(step)
	n := int(window / step)
	return grid{start: end.Add(-time.Duration(n-1) * step), end: end, step: step, n: n}
}

func (g grid) index(unix int64) int {
	d := time.Unix(unix, 0).Sub(g.start)
	if d < 0 {
		return -1
	}
	i := int((d + g.step/2) / g.step)
	if i >= g.n {
		return -1
	}
	return i
}

// stepFor keeps a window at no more than ~4,100 points per series while never
// going below 5 minutes: 7 and 14 days at 5 minutes (sharing cached days),
// 28 days at 10. A finer step for 28 days would double what is fetched,
// held and analysed for no gain the statistics can see.
func stepFor(window time.Duration) time.Duration {
	step := 5 * time.Minute
	for window/step > 4100 {
		step *= 2
	}
	return step
}

// Memory metrics, best first. Working set is what the kubelet evicts on and
// roughly what the OOM killer sees; total usage also counts page cache the
// kernel would reclaim, so it overstates the need. Collectors that forward a
// subset of cAdvisor sometimes keep only the latter.
const (
	memWorkingSet = "container_memory_working_set_bytes"
	memUsage      = "container_memory_usage_bytes"
)

// Throttling signals: CFS period counters give the share of scheduling
// periods throttled, which can drive a verdict; throttled seconds alone are
// only a hint.
const (
	throttleNone    = ""
	throttlePeriods = "periods"
	throttleSeconds = "seconds"
)

// probe records which series a cluster has, probed once per report.
type probe struct {
	// cpuSeries counts pod-level CPU series: zero means the store answers but
	// holds nothing for this cluster (often the wrong Mimir tenant).
	cpuSeries                float64
	ksm, startTime, requests bool
	memMetric                string
	throttle                 string
	// inPlace: the cluster resizes running pods (Kubernetes 1.33+).
	inPlace bool
}

func probeSignals(ctx context.Context, q historyQuerier, cluster string, at time.Time) probe {
	// Count at the level the queries aggregate at: some collectors export a
	// metric name for a few app jobs without pod labels, which must not count.
	count := func(metric string) float64 {
		query := fmt.Sprintf(`count(max by (namespace, pod, container) (%s{namespace!="",pod!="",container!="",container!="POD"}))`, metric)
		res, err := q.QueryInstant(ctx, cluster, query, at)
		if err != nil {
			return -1 // unknown, not zero: a failed probe must not read as "no data"
		}
		if len(res) == 0 || len(res[0].Values) == 0 {
			return 0
		}
		return float64(res[0].Values[0])
	}
	has := func(metric string) bool {
		res, err := q.QueryInstant(ctx, cluster, fmt.Sprintf("count(%s)", metric), at)
		return err == nil && len(res) > 0 && len(res[0].Values) > 0 && res[0].Values[0] > 0
	}
	var p probe
	p.cpuSeries = count("container_cpu_usage_seconds_total")
	p.ksm = has("kube_pod_container_status_restarts_total")
	p.startTime = has("kube_pod_start_time")
	p.requests = has("kube_pod_container_resource_requests")
	ws, usage := count(memWorkingSet), count(memUsage)
	switch {
	case ws > 0 && ws >= 0.5*usage:
		p.memMetric = memWorkingSet
	case usage > 0:
		p.memMetric = memUsage
	default:
		p.memMetric = memWorkingSet
	}
	switch {
	case count("container_cpu_cfs_periods_total") > 0 && count("container_cpu_cfs_throttled_periods_total") > 0:
		p.throttle = throttlePeriods
	case count("container_cpu_cfs_throttled_seconds_total") > 0:
		p.throttle = throttleSeconds
	}
	return p
}

// scope narrows queries to one namespace and, for a single workload's
// evidence, to pods whose names start with its key.
type scope struct {
	namespace string
	wks       []string
	// want, if set, is every history key someone will look at; series of
	// workloads that no longer run are dropped as they are read.
	want map[seriesKey]struct{}
	// jobs are the keys of Jobs and CronJobs. Their pods get no startup
	// exclusion: a run often lasts minutes, so its start is the work.
	jobs map[seriesKey]struct{}
}

func (s scope) keep() func(map[string]string) bool {
	if s.want == nil {
		return nil
	}
	return func(l map[string]string) bool {
		_, ok := s.want[keyOf(l)]
		return ok
	}
}

func (s scope) matchers() string {
	m := fmt.Sprintf(`namespace="%s"`, promString(s.namespace))
	if len(s.wks) > 0 {
		m += "," + podSelector(s.wks)
	}
	return m
}

// history is everything fetched for one scope, keyed by workload container.
type history struct {
	cpu           map[seriesKey]*pooled
	podMeans      map[seriesKey][]float64 // lifetime mean CPU per pod
	mem, throttle map[seriesKey][]float64
	// burst is the busiest replica's highest 2-minute CPU rate in each step,
	// fetched only where CFS throttling counters are missing.
	burst map[seriesKey][]float64
	// podFirst is when each pod (by name) first reported CPU, per key.
	podFirst      map[seriesKey]map[string]int64
	oom, restarts map[seriesKey][]time.Time
	startupPeak   map[seriesKey]float64
	// requests is hourly; requestsGrid is its axis.
	cpuReq, memReq map[seriesKey][]float64
	requestsGrid   grid
}

// fetchHistory runs every query for a scope. Required series fail the whole
// fetch; optional ones (throttling, startup, request history) degrade to
// absent.
func fetchHistory(ctx context.Context, c *chunker, cluster string, sc scope, g grid, pr probe, window time.Duration) (*history, error) {
	q := c.q
	sel := sc.matchers()
	keep := sc.keep()
	stepS := fmt.Sprintf("%ds", int(g.step.Seconds()))
	h := &history{}

	cpuPod := fmt.Sprintf(`sum by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{%s,container!="",container!="POD"}[%s]))`, sel, stepS)
	// Startup is split from steady state in Go with each pod's start time,
	// one cheap instant query, instead of joining kube_pod_start_time into
	// every step of the CPU query, which is far heavier on the store.
	var starts map[string]int64
	if pr.startTime {
		starts = podStarts(ctx, q, cluster, sel, g.end, window)
	}
	memPod := fmt.Sprintf(`max by (namespace, pod, container) (max_over_time(%s{%s,container!="",container!="POD"}[%s]))`, pr.memMetric, sel, stepS)

	type job struct {
		dst      *map[seriesKey][]float64
		query    string
		required bool
		pooled   bool
	}
	jobs := []job{
		// Per pod, relabelled but not aggregated: CPU is pooled across
		// replicas in Go, and the busiest, total and count come from it.
		{query: relabel(cpuPod), required: true, pooled: true},
		{dst: &h.mem, query: fmt.Sprintf(`max by (%s) (%s)`, keyLabels, relabel(memPod)), required: true},
	}
	switch pr.throttle {
	case throttlePeriods:
		thr := fmt.Sprintf(`sum by (namespace, pod, container) (rate(container_cpu_cfs_throttled_periods_total{%[1]s,container!="",container!="POD"}[%[2]s])) / sum by (namespace, pod, container) (rate(container_cpu_cfs_periods_total{%[1]s,container!="",container!="POD"}[%[2]s]))`, sel, stepS)
		jobs = append(jobs, job{dst: &h.throttle, query: fmt.Sprintf(`max by (%s) (%s)`, keyLabels, relabel(thr))})
	case throttleSeconds:
		// Seconds of throttling per second of wall time: a rough share of time
		// spent throttled.
		thr := fmt.Sprintf(`sum by (namespace, pod, container) (rate(container_cpu_cfs_throttled_seconds_total{%s,container!="",container!="POD"}[%s]))`, sel, stepS)
		jobs = append(jobs, job{dst: &h.throttle, query: fmt.Sprintf(`max by (%s) (%s)`, keyLabels, relabel(thr))})
	}
	if pr.throttle != throttlePeriods {
		// A CPU limit throttles within 100ms periods, which a step-long
		// average hides. Without CFS period counters, the highest 2-minute
		// rate in each step is the next best evidence of bursts against the
		// limit. Rates every minute over 2-minute windows read about the same
		// raw samples as the step-long rate above, so the store does similar
		// work, not five times more.
		burst := fmt.Sprintf(`max by (namespace, pod, container) (max_over_time(rate(container_cpu_usage_seconds_total{%s,container!="",container!="POD"}[2m])[%s:1m]))`, sel, stepS)
		jobs = append(jobs, job{dst: &h.burst, query: fmt.Sprintf(`max by (%s) (%s)`, keyLabels, relabel(burst))})
	}

	errs := make([]error, len(jobs))
	done := make(chan struct{}, len(jobs))
	for i, j := range jobs {
		go func() {
			defer func() { done <- struct{}{} }()
			res, err := c.rangeQuery(ctx, cluster, j.query, g, keep)
			if err != nil {
				if j.required {
					errs[i] = err
				}
				return
			}
			if j.pooled {
				h.cpu, h.podMeans, h.startupPeak, h.podFirst = alignPooled(res, g, starts, keep, sc.jobs)
			} else {
				*j.dst = align(res, g, keep)
			}
		}()
	}
	for range jobs {
		<-done
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	if pr.ksm {
		restarts := fmt.Sprintf(`changes(kube_pod_container_status_restarts_total{%s}[%s]) > 0`, sel, stepS)
		oom := fmt.Sprintf(`(%s) and on (namespace, pod, container) (max by (namespace, pod, container) (kube_pod_container_status_last_terminated_reason{%s,reason="OOMKilled"}) == 1)`, restarts, sel)
		h.restarts = events(ctx, c, cluster, fmt.Sprintf(`sum by (%s) (%s)`, keyLabels, relabel(restarts)), g, keep)
		h.oom = events(ctx, c, cluster, fmt.Sprintf(`sum by (%s) (%s)`, keyLabels, relabel(oom)), g, keep)
	}
	if pr.requests {
		h.requestsGrid = newGrid(g.end, window, time.Hour)
		req := func(resource string) string {
			return fmt.Sprintf(`max by (%s) (%s)`, keyLabels, relabel(fmt.Sprintf(`max by (namespace, pod, container) (kube_pod_container_resource_requests{%s,resource="%s"})`, sel, resource)))
		}
		if res, err := c.rangeQuery(ctx, cluster, req("cpu"), h.requestsGrid, keep); err == nil {
			h.cpuReq = align(res, h.requestsGrid, keep)
		}
		if res, err := c.rangeQuery(ctx, cluster, req("memory"), h.requestsGrid, keep); err == nil {
			h.memReq = align(res, h.requestsGrid, keep)
		}
	}
	return h, nil
}

// align places each series on the grid, NaN where a sample is missing. keep,
// if set, skips series of workloads nobody asked about (days fetched fresh
// carry every series, to be cached whole).
func align(res seriesSet, g grid, keep func(map[string]string) bool) map[seriesKey][]float64 {
	out := map[seriesKey][]float64{}
	res.each(func(s *metrics.HistorySeries) {
		if keep != nil && !keep(s.Labels) {
			return
		}
		k := keyOf(s.Labels)
		vals := out[k]
		if vals == nil {
			vals = make([]float64, g.n)
			for i := range vals {
				vals[i] = math.NaN()
			}
			out[k] = vals
		}
		for i, t := range s.Times {
			if j := g.index(t); j >= 0 {
				vals[j] = float64(s.Values[i])
			}
		}
	})
	return out
}

// minPodLifeForMean is how much history a pod needs before its mean CPU
// counts towards replica imbalance: a day, so daily cycles average out.
const minPodLifeForMean = 24 * time.Hour

// podStarts returns each pod's start time (unix seconds) keyed by
// namespace/pod, for every pod seen in the window.
func podStarts(ctx context.Context, q historyQuerier, cluster, sel string, at time.Time, window time.Duration) map[string]int64 {
	query := fmt.Sprintf(`max by (namespace, pod) (max_over_time(kube_pod_start_time{%s}[%ds]))`, sel, int(window.Seconds()))
	res, err := q.QueryInstant(ctx, cluster, query, at)
	if err != nil {
		return nil
	}
	out := make(map[string]int64, len(res))
	for _, s := range res {
		if len(s.Values) > 0 {
			out[s.Labels["namespace"]+"/"+s.Labels["pod"]] = int64(s.Values[0])
		}
	}
	return out
}

// alignPooled groups per-pod series by workload key and lays every replica's
// samples out on the grid. Samples from a pod's first minutes are kept out of
// steady state and only feed the startup peak. It also keeps each long-lived
// pod's mean CPU, the one place pod identity matters: a replica that is hot
// all the time is a sharding problem, one that is hot by chance is not.
func alignPooled(res seriesSet, g grid, starts map[string]int64, keep func(map[string]string) bool, jobs map[seriesKey]struct{}) (map[seriesKey]*pooled, map[seriesKey][]float64, map[seriesKey]float64, map[seriesKey]map[string]int64) {
	var startup map[seriesKey]float64
	if starts != nil {
		startup = map[seriesKey]float64{}
	}
	startupSec := int64(startupWindow.Seconds())
	// Only the minutes right after the latest start are startup. A pod name
	// reused across restarts (StatefulSets) has earlier samples from before
	// that start, which are steady state.
	isStartup := func(t, start int64, hasStart bool) bool {
		return hasStart && t >= start && t-start < startupSec
	}
	type podSum struct {
		k     seriesKey
		sum   float64
		n     int
		first int64
	}
	type item struct {
		s        *metrics.HistorySeries
		k        seriesKey
		start    int64
		hasStart bool
	}
	// Two passes over the samples, so each key's layout is sized exactly:
	// count per grid step, then fill. A pod's samples arrive split across day
	// parts, so its mean is summed per pod.
	var items []item
	counts := map[seriesKey][]int32{}
	pods := map[[3]string]*podSum{}
	var podOrder [][3]string
	res.each(func(s *metrics.HistorySeries) {
		if keep != nil && !keep(s.Labels) {
			return
		}
		k := keyOf(s.Labels)
		id := [3]string{s.Labels["namespace"], s.Labels["pod"], s.Labels["container"]}
		ps := pods[id]
		if ps == nil {
			ps = &podSum{k: k, first: math.MaxInt64}
			pods[id] = ps
			podOrder = append(podOrder, id)
		}
		if len(s.Times) > 0 {
			ps.first = min(ps.first, s.Times[0])
		}
		start, hasStart := starts[id[0]+"/"+id[1]]
		if _, isJob := jobs[k]; isJob {
			hasStart = false
		}
		c := counts[k]
		if c == nil {
			c = make([]int32, g.n+1)
			counts[k] = c
		}
		for i, t := range s.Times {
			v := float64(s.Values[i])
			if isStartup(t, start, hasStart) {
				startup[k] = max(startup[k], v)
				continue
			}
			if j := g.index(t); j >= 0 {
				c[j+1]++
				ps.sum += v
				ps.n++
			}
		}
		items = append(items, item{s, k, start, hasStart})
	})
	out := make(map[seriesKey]*pooled, len(counts))
	next := map[seriesKey][]int32{}
	for k, c := range counts {
		for i := 1; i <= g.n; i++ {
			c[i] += c[i-1]
		}
		out[k] = &pooled{off: c, vals: make([]float64, c[g.n])}
		next[k] = slices.Clone(c[:g.n])
	}
	for _, it := range items {
		p, nx := out[it.k], next[it.k]
		for i, t := range it.s.Times {
			if isStartup(t, it.start, it.hasStart) {
				continue
			}
			if j := g.index(t); j >= 0 {
				p.vals[nx[j]] = float64(it.s.Values[i])
				nx[j]++
			}
		}
	}
	minSamples := int(minPodLifeForMean / g.step)
	means := map[seriesKey][]float64{}
	first := map[seriesKey]map[string]int64{}
	for _, id := range podOrder {
		ps := pods[id]
		if ps.n >= minSamples {
			means[ps.k] = append(means[ps.k], ps.sum/float64(ps.n))
		}
		if ps.first != math.MaxInt64 {
			if first[ps.k] == nil {
				first[ps.k] = map[string]int64{}
			}
			if t, ok := first[ps.k][id[1]]; !ok || ps.first < t {
				first[ps.k][id[1]] = ps.first
			}
		}
	}
	return out, means, startup, first
}

// events turns a sparse count series into one timestamp per event.
func events(ctx context.Context, c *chunker, cluster, query string, g grid, keep func(map[string]string) bool) map[seriesKey][]time.Time {
	res, err := c.rangeQuery(ctx, cluster, query, g, keep)
	if err != nil {
		return nil
	}
	out := map[seriesKey][]time.Time{}
	res.each(func(s *metrics.HistorySeries) {
		if keep != nil && !keep(s.Labels) {
			return
		}
		k := keyOf(s.Labels)
		for i, t := range s.Times {
			for range max(int(math.Round(float64(s.Values[i]))), 1) {
				out[k] = append(out[k], time.Unix(t, 0).UTC())
			}
		}
	})
	return out
}

// mergeSeries combines several workload keys that resolve to the same live
// workload (rare: a rename, or a pod name the rules could not strip): the
// busiest replica is the max across them, replicas and totals add up.
func mergeSeries(dst []float64, src []float64, combine func(a, b float64) float64) []float64 {
	if dst == nil {
		return append([]float64(nil), src...)
	}
	for i := range dst {
		switch {
		case math.IsNaN(dst[i]):
			dst[i] = src[i]
		case !math.IsNaN(src[i]):
			dst[i] = combine(dst[i], src[i])
		}
	}
	return dst
}

func maxF(a, b float64) float64 { return math.Max(a, b) }
func sumF(a, b float64) float64 { return a + b }
