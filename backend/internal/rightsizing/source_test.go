package rightsizing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/metrics"
)

// restartCounter is kube-state-metrics' restart counter for one container,
// scraped every 30 seconds from first to last (zero: still running). It
// answers the restart query in any shape it could take: changes() within
// each step's window, the rise since the previous step (an offset), or that
// rise with changes() wherever there is no earlier count to rise from. CPU
// and memory queries get no series.
type restartCounter struct {
	first, last time.Time // first and last scrape
	restarts    []time.Time
}

var (
	changesRange = regexp.MustCompile(`changes\(kube_pod_container_status_restarts_total\{[^}]*\}\[(\d+)s\]\)`)
	offsetRange  = regexp.MustCompile(`restarts_total\{[^}]*\} offset (\d+)s`)
)

// scrapes returns the counter as scraped in (from, to].
func (c restartCounter) scrapes(from, to time.Time) []float64 {
	var out []float64
	for t := c.first; !t.After(to) && (c.last.IsZero() || !t.After(c.last)); t = t.Add(30 * time.Second) {
		if !t.After(from) {
			continue
		}
		n := 0
		for _, r := range c.restarts {
			if !r.After(t) {
				n++
			}
		}
		out = append(out, float64(n))
	}
	return out
}

// at is the counter an instant selector sees at t, if any: the last scrape
// in the 5 minutes before, unless the series went stale at the scrape after
// its last.
func (c restartCounter) at(t time.Time) (float64, bool) {
	if !c.last.IsZero() && !t.Before(c.last.Add(30*time.Second)) {
		return 0, false
	}
	v := c.scrapes(t.Add(-5*time.Minute), t)
	if len(v) == 0 {
		return 0, false
	}
	return v[len(v)-1], true
}

func (c restartCounter) QueryRange(_ context.Context, _, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if !strings.Contains(query, "restarts_total") || strings.Contains(query, "OOMKilled") {
		return nil, nil
	}
	changes, offset := changesRange.FindStringSubmatch(query), offsetRange.FindStringSubmatch(query)
	s := metrics.HistorySeries{Labels: map[string]string{"namespace": "n", "vns": "", "wk": "db", "container": "c"}}
	for t := start; !t.After(end); t = t.Add(step) {
		var v float64
		rose := false
		if offset != nil {
			secs, _ := strconv.Atoi(offset[1])
			now, ok := c.at(t)
			before, okBefore := c.at(t.Add(-time.Duration(secs) * time.Second))
			if rose = ok && okBefore; rose {
				v = now - before
			}
		}
		if !rose && changes != nil {
			secs, _ := strconv.Atoi(changes[1])
			w := c.scrapes(t.Add(-time.Duration(secs)*time.Second), t)
			for i := 1; i < len(w); i++ {
				if w[i] != w[i-1] {
					v++
				}
			}
		}
		if v > 0 {
			s.Times = append(s.Times, t.Unix())
			s.Values = append(s.Values, float32(v))
		}
	}
	return []metrics.HistorySeries{s}, nil
}

func (restartCounter) QueryInstant(context.Context, string, string, time.Time) ([]metrics.HistorySeries, error) {
	return nil, nil
}

// A restart that lands between the last scrape of one step and the first of
// the next is still a restart, and so is each of two between two scrapes.
func TestRestartsCountedAcrossStepBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	g := newGrid(now, 2*time.Hour, 5*time.Minute)
	boundary := g.start.Add(time.Hour)
	store := restartCounter{
		first: g.start.Add(-time.Hour + 10*time.Second),
		restarts: []time.Time{
			boundary.Add(-10 * time.Second),               // after a step's last scrape, before the next step's first
			boundary.Add(22 * time.Minute),                // between two scrapes of one step
			boundary.Add(40*time.Minute + 15*time.Second), // two between the same pair of scrapes
			boundary.Add(40*time.Minute + 20*time.Second),
		},
	}
	c := newChunker(store, nil)
	c.now = func() time.Time { return now }
	sc := scope{namespace: "n"}
	h, err := fetchHistory(context.Background(), c, "c", sc, g, probe{ksm: true, memMetric: memWorkingSet, throttle: throttlePeriods}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(h.restarts[seriesKey{"n", "", "db", "c"}]); got != len(store.restarts) {
		t.Fatalf("%d restarts seen, %d happened", got, len(store.restarts))
	}
}

// A pod has no count a step before its first step, and a Job's pod may not
// live a step at all; a restart (an OOM kill) in that time still counts.
func TestRestartsCountedInAPodsFirstStep(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	g := newGrid(now, 2*time.Hour, 5*time.Minute)
	at := func(d time.Duration) time.Time { return g.start.Add(time.Hour + d) }
	for name, store := range map[string]restartCounter{
		"a new pod":                  {first: at(20 * time.Second), restarts: []time.Time{at(3 * time.Minute)}},
		"a Job pod living 3 minutes": {first: at(40 * time.Second), last: at(3*time.Minute + 40*time.Second), restarts: []time.Time{at(2 * time.Minute)}},
	} {
		c := newChunker(store, nil)
		c.now = func() time.Time { return now }
		h, err := fetchHistory(context.Background(), c, "c", scope{namespace: "n"}, g, probe{ksm: true, memMetric: memWorkingSet, throttle: throttlePeriods}, 2*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(h.restarts[seriesKey{"n", "", "db", "c"}]); got != 1 {
			t.Errorf("%s: %d restarts seen, 1 happened", name, got)
		}
	}
}

// duplicateScrapes is a store fed by two Prometheus replicas (or a kubelet
// and a cAdvisor job) that both scrape every container: each cAdvisor series
// arrives twice, told apart only by the replica label. The pod's container
// uses 0.5 cores on one CPU and 0.25 on another, with per-CPU series on. It
// answers the CPU query by its aggregation: an outer sum by, over an
// optional max by.
type duplicateScrapes struct{ countingStore }

var outerSum = regexp.MustCompile(`sum by \(([^)]*)\) \((?:max by \(([^)]*)\) \()?rate\(container_cpu_usage_seconds_total`)

func (s *duplicateScrapes) QueryRange(_ context.Context, _, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	m := outerSum.FindStringSubmatch(query)
	if m == nil {
		return nil, nil
	}
	type raw struct {
		labels map[string]string
		rate   float64
	}
	var series []raw
	for _, replica := range []string{"a", "b"} {
		for cpu, rate := range map[string]float64{"cpu00": 0.5, "cpu01": 0.25} {
			series = append(series, raw{map[string]string{
				"namespace": "n", "pod": "db-0", "container": "c", "id": "/kubepods/pod1/c1", "cpu": cpu, "replica": replica,
			}, rate})
		}
	}
	group := func(rs []raw, by string, combine func(a, b float64) float64) []raw {
		names := strings.Split(by, ", ")
		out := map[string]*raw{}
		var order []string
		for _, r := range rs {
			l := map[string]string{}
			var key []string
			for _, n := range names {
				l[n] = r.labels[n]
				key = append(key, r.labels[n])
			}
			k := strings.Join(key, "\x00")
			if g, ok := out[k]; ok {
				g.rate = combine(g.rate, r.rate)
				continue
			}
			out[k] = &raw{l, r.rate}
			order = append(order, k)
		}
		var res []raw
		for _, k := range order {
			res = append(res, *out[k])
		}
		return res
	}
	if m[2] != "" {
		series = group(series, m[2], math.Max)
	}
	series = group(series, m[1], func(a, b float64) float64 { return a + b })
	var out []metrics.HistorySeries
	for _, r := range series {
		r.labels["wk"] = "db"
		s := metrics.HistorySeries{Labels: r.labels}
		for t := start; !t.After(end); t = t.Add(step) {
			s.Times = append(s.Times, t.Unix())
			s.Values = append(s.Values, float32(r.rate))
		}
		out = append(out, s)
	}
	return out, nil
}

// Copies of one container's series, from two replicas or two scrape jobs,
// must not add up; its per-CPU series must.
func TestCPUCountsEachCgroupOnce(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	c := newChunker(&duplicateScrapes{}, nil)
	c.now = func() time.Time { return now }
	g := newGrid(now, time.Hour, 5*time.Minute)
	h, err := fetchHistory(context.Background(), c, "c", scope{namespace: "n"}, g, probe{memMetric: memWorkingSet, throttle: throttlePeriods}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p := h.cpu[seriesKey{"n", "", "db", "c"}]
	if p == nil || len(p.at(0)) != 1 || math.Abs(p.at(0)[0]-0.75) > 1e-6 {
		t.Fatalf("CPU at the first step: %v, want one replica at 0.75 cores", p.at(0))
	}
}

// gateStore holds every query until want of them wait together, then lets
// them all through: a caller that waits for one answer before asking the
// next never gets there, and the gate gives up after a while.
type gateStore struct {
	want    int
	mu      sync.Mutex
	waiting int
	open    chan struct{}
	opened  bool // all want queries were waiting at once
}

func newGateStore(want int) *gateStore { return &gateStore{want: want, open: make(chan struct{})} }

func (s *gateStore) wait() {
	s.mu.Lock()
	s.waiting++
	if s.waiting == s.want {
		s.opened = true
		close(s.open)
	}
	s.mu.Unlock()
	select {
	case <-s.open:
	case <-time.After(2 * time.Second):
		s.mu.Lock()
		if !s.opened && s.waiting < s.want {
			s.waiting = s.want // give up: let the rest through
			close(s.open)
		}
		s.mu.Unlock()
	}
}

func (s *gateStore) QueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	s.wait()
	return nil, nil
}

func (s *gateStore) QueryInstant(context.Context, string, string, time.Time) ([]metrics.HistorySeries, error) {
	s.wait()
	return nil, nil
}

// Nothing fetchHistory asks for depends on another answer, so it asks for
// everything in one round trip: pod starts, usage, throttling, restarts, OOM
// kills and request history.
func TestFetchHistoryAsksEverythingAtOnce(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	store := newGateStore(8)
	c := newChunker(store, nil)
	c.now = func() time.Time { return now }
	pr := probe{cpuSeries: 1, ksm: true, startTime: true, requests: true, memMetric: memWorkingSet, throttle: throttlePeriods}
	if _, err := fetchHistory(context.Background(), c, "c", scope{namespace: "n"}, newGrid(now, 2*time.Hour, 5*time.Minute), pr, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if !store.opened {
		t.Fatal("history was fetched in several round trips")
	}
}

// The probe's nine counts are independent too.
func TestProbeAsksEverythingAtOnce(t *testing.T) {
	store := newGateStore(9)
	if p := probeSignals(context.Background(), store, "c", time.Now()); p.err != nil {
		t.Fatal(p.err)
	}
	if !store.opened {
		t.Fatal("the probe asked one count at a time")
	}
}

// priorityStore records the priority of every query it is asked.
type priorityStore struct {
	*evidenceHistory
	mu         sync.Mutex
	background []string
}

func (s *priorityStore) note(ctx context.Context, query string) {
	if priorityOf(ctx) != priorityInteractive {
		s.mu.Lock()
		s.background = append(s.background, query)
		s.mu.Unlock()
	}
}

func (s *priorityStore) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	s.note(ctx, query)
	return s.evidenceHistory.QueryRange(ctx, cluster, query, start, end, step)
}

func (s *priorityStore) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	s.note(ctx, query)
	return s.evidenceHistory.QueryInstant(ctx, cluster, query, at)
}

// Someone waits on the evidence drawer: none of its queries, the signal
// probe's included, may queue behind background reports.
func TestEvidenceQueriesAreInteractive(t *testing.T) {
	h := &priorityStore{evidenceHistory: &evidenceHistory{calls: map[string]int{}}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, nil)
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	q := WorkloadQuery{Cluster: t.Name(), Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, Refresh: true}
	if _, err := s.GetEvidence(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(h.background) > 0 {
		t.Fatalf("%d evidence queries at background priority, e.g. %s", len(h.background), h.background[0])
	}
}

// pricesFirst holds history until node prices have been asked for, or gives
// up after a while.
type pricesFirst struct {
	*evidenceHistory
	once  sync.Once
	asked chan struct{}
}

func (p *pricesFirst) NodeRates(context.Context, string) (map[string]finops.Rates, error) {
	p.once.Do(func() { close(p.asked) })
	return nil, nil
}

func (p *pricesFirst) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	select {
	case <-p.asked:
	case <-time.After(2 * time.Second):
		p.once.Do(func() { close(p.asked) })
	}
	return p.evidenceHistory.QueryRange(ctx, cluster, query, start, end, step)
}

// HPAs, the server version and node prices are asked for while history
// loads, not after it.
func TestEvidenceAsksPricesWhileHistoryLoads(t *testing.T) {
	h := &pricesFirst{evidenceHistory: &evidenceHistory{calls: map[string]int{}}, asked: make(chan struct{})}
	s := NewService(&k8s.MockClient{}, h, h, evidenceFixtures{}, nil)
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	q := WorkloadQuery{Cluster: t.Name(), Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, Refresh: true}
	start := time.Now()
	if _, err := s.GetEvidence(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("evidence waited for history before asking for prices: %s", time.Since(start))
	}
}

// Only a few namespaces are read at once, so the largest start first: one
// left for last would keep the whole report waiting on it alone.
func TestLargestNamespacesFirst(t *testing.T) {
	want := map[string]map[seriesKey]struct{}{}
	for ns, n := range map[string]int{"a": 1, "b": 2, "c": 3, "d": 10, "e": 2} {
		want[ns] = map[seriesKey]struct{}{}
		for i := range n {
			want[ns][seriesKey{ns, "", fmt.Sprint(i), "main"}] = struct{}{}
		}
	}
	if got := largestFirst(want); !slices.Equal(got, []string{"d", "c", "b", "e", "a"}) {
		t.Fatalf("namespaces in order %v", got)
	}
}

// panickyHistory panics answering history, as a bug in decoding or analysing
// some odd answer would.
type panickyHistory struct{ *evidenceHistory }

func (panickyHistory) QueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	panic("index out of range")
}

// panickyRates panics pricing nodes, outside any namespace's goroutine.
type panickyRates struct{ evidenceFixtures }

func (panickyRates) NodeRates(context.Context, string) (map[string]finops.Rates, error) {
	panic("nil map")
}

// A panic while computing a report fails the report (or one namespace of
// it), not the whole backend.
func TestPanicFailsTheReportNotTheBackend(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")

	s := NewService(&k8s.MockClient{}, panickyHistory{&evidenceHistory{calls: map[string]int{}}}, evidenceFixtures{}, evidenceFixtures{}, nil)
	s.now = func() time.Time { return now }
	rep := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, nil, func(Progress) {})
	if rep.Status != StatusError || !strings.Contains(rep.Error, "apps: internal error") {
		t.Fatalf("history panic: status=%s error=%q", rep.Status, rep.Error)
	}

	cluster := t.Name() + "/rates"
	key := runKey(cluster, ProfileBalanced, defaultWindow, "mimir")
	s = NewService(&k8s.MockClient{}, &evidenceHistory{calls: map[string]int{}}, panickyRates{}, evidenceFixtures{}, nil)
	s.now = func() time.Time { return now }
	good := &Report{Status: StatusReady, ComputedAt: now.Add(-2 * time.Hour), Workloads: []WorkloadReport{}}
	s.runs[key] = &run{cluster: cluster, ready: good, computing: true, loaded: true}
	s.computeAndStore(key, cluster, ProfileBalanced, defaultWindow, "mimir")
	r := s.runs[key]
	if r.computing || r.ready != good || !strings.Contains(r.lastError, "internal error") {
		t.Fatalf("report panic: computing=%t kept=%t lastError=%q", r.computing, r.ready == good, r.lastError)
	}

	// The evidence drawer prices nodes alongside history, off the request's
	// goroutine.
	q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, Refresh: true}
	if _, err := s.GetEvidence(context.Background(), q); err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("evidence panic: %v", err)
	}
}

// flakyProbe drops the probe for kube-state-metrics, the way a dropped
// port-forward or a timed-out query does.
type flakyProbe struct {
	*evidenceHistory
}

func (f flakyProbe) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	if strings.Contains(query, "count(kube_pod_container_status_restarts_total)") {
		return nil, errors.New("failed to query mimir: port-forward dropped: EOF")
	}
	return f.evidenceHistory.QueryInstant(ctx, cluster, query, at)
}

// A probe query that fails says nothing about the signal it probes. The
// report uses the last thing known about the store, or fails, rather than
// coming out ready with OOM detection quietly off.
func TestProbeFailureDoesNotTurnSignalsOff(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	compute := func(cluster string, prev *Report) (*Report, int) {
		h := flakyProbe{&evidenceHistory{calls: map[string]int{}}}
		s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, nil)
		s.now = func() time.Time { return now }
		rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, prev, func(Progress) {})
		oomQueries := 0
		for k := range h.calls {
			if strings.Contains(k, "OOMKilled") {
				oomQueries++
			}
		}
		return rep, oomQueries
	}

	// Nothing known: the report fails, and a previous good one stays shown.
	rep, _ := compute(t.Name()+"/first", nil)
	if rep.Status != StatusError || !strings.Contains(rep.Error, "could not check which signals") {
		t.Fatalf("first report: status=%s error=%q signals=%+v", rep.Status, rep.Error, rep.Signals)
	}

	// The previous report's signals stand in.
	prev := &Report{Status: StatusReady, Signals: Signals{OOMKills: true, StartupExclusion: true, RequestHistory: true, MemoryMetric: "working-set"}}
	rep, oomQueries := compute(t.Name()+"/refresh", prev)
	if rep.Status != StatusReady || !rep.Signals.OOMKills || oomQueries == 0 {
		t.Fatalf("refresh: status=%s error=%q oomKills=%t, %d OOM queries", rep.Status, rep.Error, rep.Signals.OOMKills, oomQueries)
	}

	// So does the store's last good probe, however old.
	cluster := t.Name() + "/probed"
	key := probeKey(ctx, cluster)
	storeProbe(key, probeEntry{probe{cpuSeries: 3, ksm: true, memMetric: memWorkingSet, throttle: throttlePeriods}, now.Add(-day)}, probeEpoch.Load())
	rep, oomQueries = compute(cluster, nil)
	if rep.Status != StatusReady || !rep.Signals.OOMKills || rep.Signals.ThrottleKind != throttlePeriods || oomQueries == 0 {
		t.Fatalf("stale probe: status=%s error=%q signals=%+v, %d OOM queries", rep.Status, rep.Error, rep.Signals, oomQueries)
	}
	if e, _ := probeCache.Load(key); e.(probeEntry).at != now.Add(-day) {
		t.Fatal("a failed probe replaced the last good one")
	}
}
