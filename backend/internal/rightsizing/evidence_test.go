package rightsizing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/metrics"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type evidenceHistory struct {
	mu    sync.Mutex
	calls map[string]int
}

func (h *evidenceHistory) HistorySource(context.Context, string) (*metrics.ProviderInfo, bool) {
	return &metrics.ProviderInfo{Type: "mimir", Found: true}, false
}

func (h *evidenceHistory) QueryInstant(_ context.Context, _, query string, at time.Time) ([]metrics.HistorySeries, error) {
	if strings.Contains(query, "max_over_time(kube_pod_start_time") {
		return nil, nil
	}
	return []metrics.HistorySeries{{Times: []int64{at.Unix()}, Values: []float32{1}}}, nil
}

func (h *evidenceHistory) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	key := fmt.Sprintf("%s|%s|%s|%d|%d|%s", cluster, metrics.HistoryProvider(ctx), query, start.Unix(), end.Unix(), step)
	h.mu.Lock()
	h.calls[key]++
	h.mu.Unlock()
	if strings.Contains(query, "restarts_total") {
		return nil, nil // no restarts: "> 0" keeps no step
	}
	var result []metrics.HistorySeries
	for _, wk := range []string{"api", "other"} {
		value := float32(0.25)
		if strings.Contains(query, "memory") || strings.Contains(query, `resource="memory"`) {
			value = 128 << 20
		} else if strings.Contains(query, "throttled") {
			value = 0
		}
		if wk == "other" {
			value *= 100
		}
		s := metrics.HistorySeries{Labels: map[string]string{
			"namespace": "apps", "pod": wk + "-bcdfgh-bcdfg", "wk": wk, "container": "main",
		}}
		for at := start; !at.After(end); at = at.Add(step) {
			s.Times = append(s.Times, at.Unix())
			s.Values = append(s.Values, value)
		}
		result = append(result, s)
	}
	return result, nil
}

type evidenceFixtures struct{}

func (evidenceFixtures) List(context.Context, string) ([]*v1.Pod, error) {
	var pods []*v1.Pod
	controller := true
	for _, name := range []string{"api", "other"} {
		pods = append(pods, &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "apps", Name: name + "-bcdfgh-bcdfg",
				Labels:          map[string]string{"pod-template-hash": "bcdfgh"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: name + "-bcdfgh", Controller: &controller}},
			},
			Spec:   v1.PodSpec{Containers: []v1.Container{{Name: "main"}}},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
	}
	return pods, nil
}

func (evidenceFixtures) NodeRates(context.Context, string) (map[string]finops.Rates, error) {
	return nil, nil
}

func TestEvidenceReusesReportQueriesAndFiltersWorkload(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprintf("reportReady=%t", ready), func(t *testing.T) {
			now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
			h := &evidenceHistory{calls: map[string]int{}}
			s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, nil)
			s.now = func() time.Time { return now }
			cluster := t.Name()
			ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
			rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
			if rep.Status != StatusReady || len(rep.Workloads) != 2 {
				t.Fatalf("report status=%s error=%s workloads=%d", rep.Status, rep.Error, len(rep.Workloads))
			}
			if ready {
				rep.ComputedAt = now
				s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, readyAt: now, loaded: true}
				// The drawer must use the completed report's grid even as time advances.
				now = now.Add(10 * time.Minute)
			} else {
				s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{computing: true, loaded: true}
			}
			reportQueries := make(map[string]bool, len(h.calls))
			for key := range h.calls {
				reportQueries[key] = true
			}
			ev, err := s.GetEvidence(ctx, WorkloadQuery{
				Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api",
				Profile: ProfileBalanced, Window: defaultWindow,
			})
			if err != nil {
				t.Fatal(err)
			}
			// Finished days are the report's own queries. The day in progress,
			// which nothing caches, is asked for the workload's pods alone.
			const narrow = `,pod=~"(api).*"`
			today := now.UTC().Truncate(day).Unix()
			narrowed := 0
			for key, count := range h.calls {
				parts := strings.Split(key, "|")
				start, _ := strconv.ParseInt(parts[len(parts)-3], 10, 64)
				live := start >= today
				switch {
				case strings.Contains(key, narrow):
					narrowed++
					if !live {
						t.Errorf("a finished day was narrowed, so it can't come from the report's days: %s", key)
					}
					if !reportQueries[strings.ReplaceAll(key, narrow, "")] {
						t.Errorf("narrowed query is not one the report ran: %s", key)
					}
				case !reportQueries[key]:
					t.Errorf("evidence issued a query the report did not run: %s", key)
				case live && count != 1:
					t.Errorf("evidence asked for the whole namespace's day in progress: %s", key)
				case !live && count != 2:
					t.Errorf("report query not reused by evidence (calls=%d): %s", count, key)
				}
			}
			if narrowed == 0 {
				t.Error("evidence did not ask for its day in progress")
			}
			if len(ev.Workload.Containers) != 1 {
				t.Fatalf("containers=%d", len(ev.Workload.Containers))
			}
			c := ev.Workload.Containers[0]
			if c.CPU.P95 != 0.25 || c.Memory.Peak != 128<<20 {
				t.Fatalf("other workload contaminated evidence: CPU=%v memory=%v", c.CPU.P95, c.Memory.Peak)
			}
			if !ev.AsOf.Equal(rep.AsOf) {
				t.Fatalf("evidence grid=%v report grid=%v", ev.AsOf, rep.AsOf)
			}
		})
	}
}

// quietHistory is evidenceHistory without restarts, so without OOM kills.
type quietHistory struct{ *evidenceHistory }

func (h quietHistory) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if strings.Contains(query, "restarts_total") {
		return nil, nil
	}
	return h.evidenceHistory.QueryRange(ctx, cluster, query, start, end, step)
}

// A container without events has an empty list, not null: the drawer would
// make a new array for null on every render and redraw its charts each time.
func TestEvidenceWithoutEventsIsAnEmptyList(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	s := NewService(&k8s.MockClient{}, quietHistory{&evidenceHistory{calls: map[string]int{}}}, evidenceFixtures{}, evidenceFixtures{}, nil)
	s.now = func() time.Time { return now }
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	ev, err := s.GetEvidence(ctx, WorkloadQuery{
		Cluster: t.Name(), Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api",
		Profile: ProfileBalanced, Window: defaultWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(ev.Events); string(b) != `{"main":[]}` {
		t.Fatalf("events=%s", b)
	}
}

// evidenceStore persists day chunks in memory and nothing else.
type evidenceStore struct{ *memChunks }

func (evidenceStore) SaveRightsizingReport(string, string, []byte) error { return nil }
func (evidenceStore) GetRightsizingReport(string) ([]byte, error)        { return nil, errNoStore }
func (evidenceStore) ListRightsizingDismissals(string) ([]db.RightsizingDismissal, error) {
	return nil, nil
}
func (evidenceStore) SaveRightsizingDismissal(*db.RightsizingDismissal) error { return nil }
func (evidenceStore) ForgetRightsizingClusters(func(string) bool) error       { return nil }
func (evidenceStore) DeleteRightsizingDismissal(string, string, string, string, string, string) error {
	return nil
}

// offlineSource fails the test on any query: cached evidence must be served
// from stored chunks alone.
type offlineSource struct {
	metricsSource
	t *testing.T
}

func (s offlineSource) QueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	s.t.Error("cached evidence attempted upstream history")
	return nil, nil
}
func (s offlineSource) QueryInstant(context.Context, string, string, time.Time) ([]metrics.HistorySeries, error) {
	s.t.Error("cached evidence attempted upstream probe")
	return nil, nil
}

func TestEvidenceCachedSnapshotThenFreshGrid(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	h := &evidenceHistory{calls: map[string]int{}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, evidenceStore{&memChunks{data: map[string][]byte{}}})
	s.now = func() time.Time { return now }
	cluster := t.Name()
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
	rep.ComputedAt = now
	s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, loaded: true}
	// Cached evidence must not touch pod listing, discovery, rates or Kubernetes.
	s.metrics = offlineSource{t: t}
	s.k8s, s.rates, s.pods = nil, nil, nil
	now = now.Add(10 * time.Minute)
	q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, CacheOnly: true}
	cached, err := s.GetEvidence(ctx, q)
	if err != nil || !cached.AsOf.Equal(rep.AsOf) {
		t.Fatalf("cached=%v err=%v", cached, err)
	}
	// Stored days are quantised to 0.1%.
	if p95 := cached.Workload.Containers[0].CPU.P95; math.Abs(p95-0.25) > 0.001 {
		t.Fatalf("incorrect cached workload: CPU p95 %v", p95)
	}
	// The day in progress came from the report's stored tail, so the drawer
	// counts the same samples as the row the user clicked.
	for _, w := range rep.Workloads {
		if w.Name == "api" && w.Containers[0].Data.Samples != cached.Workload.Containers[0].Data.Samples {
			t.Fatalf("cached evidence has %d samples, its report row %d", cached.Workload.Containers[0].Data.Samples, w.Containers[0].Data.Samples)
		}
	}
	s.metrics, s.k8s, s.rates, s.pods = h, &k8s.MockClient{}, evidenceFixtures{}, evidenceFixtures{}
	q.CacheOnly, q.Refresh = false, true
	fresh, err := s.GetEvidence(ctx, q)
	if err != nil || !fresh.AsOf.After(cached.AsOf) || !fresh.AsOf.Equal(now) {
		t.Fatalf("fresh=%v err=%v", fresh, err)
	}
	q.CacheOnly, q.Refresh, q.Provider = true, false, "prometheus"
	if _, err := s.GetEvidence(ctx, q); err != errNotCached {
		t.Fatalf("provider switch reused snapshot: %v", err)
	}
}

// samplesOf is the sample count of a workload's first container in a report.
func samplesOf(rep *Report, name string) int {
	for _, w := range rep.Workloads {
		if w.Name == name {
			return w.Containers[0].Data.Samples
		}
	}
	return -1
}

// A report computed shortly before midnight stored its last day as a tail.
// Cached evidence opened once that day is final still reads that tail.
func TestCachedEvidenceAcrossMidnight(t *testing.T) {
	for _, later := range []time.Duration{20 * time.Minute, 55 * time.Minute} {
		now := time.Date(2026, 10, 2, 23, 50, 0, 0, time.UTC)
		h := &evidenceHistory{calls: map[string]int{}}
		s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, evidenceStore{&memChunks{data: map[string][]byte{}}})
		s.now = func() time.Time { return now }
		cluster := t.Name()
		ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
		rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
		rep.ComputedAt = now
		s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, readyAt: now, loaded: true}
		now = now.Add(later)
		q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, CacheOnly: true}
		cached, err := s.GetEvidence(ctx, q)
		if err != nil {
			t.Fatalf("+%s: %v", later, err)
		}
		if got, row := cached.Workload.Containers[0].Data.Samples, samplesOf(rep, "api"); got != row {
			t.Errorf("opened %s after the report: cached evidence has %d samples, its report row %d", later, got, row)
		}
	}
}

// Reports for other profiles run the same queries at the same step. Each
// keeps its own day in progress, so cached evidence still matches its row.
func TestCachedEvidenceKeepsItsOwnTail(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	h := &evidenceHistory{calls: map[string]int{}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, evidenceStore{&memChunks{data: map[string][]byte{}}})
	s.now = func() time.Time { return now }
	cluster := t.Name()
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
	rep.ComputedAt = now
	s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, readyAt: now, loaded: true}

	now = now.Add(10 * time.Minute)
	if other := s.compute(ctx, cluster, ProfileConservative, defaultWindow, nil, func(Progress) {}); other.Status != StatusReady {
		t.Fatal(other.Status)
	}
	now = now.Add(time.Minute)
	q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, CacheOnly: true}
	cached, err := s.GetEvidence(ctx, q)
	if err != nil {
		t.Fatalf("cached evidence: %v", err)
	}
	if got, row := cached.Workload.Containers[0].Data.Samples, samplesOf(rep, "api"); got != row {
		t.Fatalf("cached evidence lost the day in progress to another profile's report: %d samples, its row %d", got, row)
	}
}

// persistingStore keeps reports too, the way the local database does across
// restarts.
type persistingStore struct {
	evidenceStore
	reports map[string][]byte
}

func (s persistingStore) SaveRightsizingReport(key, _ string, data []byte) error {
	s.reports[key] = data
	return nil
}

func (s persistingStore) GetRightsizingReport(key string) ([]byte, error) {
	if d, ok := s.reports[key]; ok {
		return d, nil
	}
	return nil, errNoStore
}

// After a restart the persisted report is fresh, so nothing recomputes it;
// cached evidence still shows its numbers at once, from the stored days and
// the local pod cache, without asking the metrics store or the API server.
func TestCachedEvidenceAfterRestart(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	store := persistingStore{evidenceStore{&memChunks{data: map[string][]byte{}}}, map[string][]byte{}}
	cluster := t.Name()
	key := runKey(cluster, ProfileBalanced, defaultWindow, "mimir")
	before := NewService(&k8s.MockClient{}, &evidenceHistory{calls: map[string]int{}}, evidenceFixtures{}, evidenceFixtures{}, store)
	before.now = func() time.Time { return now }
	before.runs[key] = &run{cluster: cluster, computing: true, loaded: true}
	before.computeAndStore(key, cluster, ProfileBalanced, defaultWindow, "mimir")
	rep := before.Cached(cluster, ProfileBalanced, defaultWindow, "mimir")
	if rep == nil || rep.Status != StatusReady {
		t.Fatalf("report: %+v", rep)
	}

	now = now.Add(10 * time.Minute)
	after := NewService(nil, offlineSource{t: t}, nil, evidenceFixtures{}, store)
	after.now = func() time.Time { return now }
	if got := after.GetReport(cluster, ProfileBalanced, defaultWindow, false, "", "mimir"); got.Status != StatusReady || got.Stale {
		t.Fatalf("persisted report: status=%s stale=%t", got.Status, got.Stale)
	}
	q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, CacheOnly: true}
	cached, err := after.GetEvidence(context.Background(), q)
	if err != nil {
		t.Fatalf("cached evidence after a restart: %v", err)
	}
	if got, row := cached.Workload.Containers[0].Data.Samples, samplesOf(rep, "api"); got != row || !cached.AsOf.Equal(rep.AsOf) {
		t.Fatalf("cached evidence has %d samples at %v, its report row %d at %v", got, cached.AsOf, row, rep.AsOf)
	}
}

// With a chunk store, evidence reads the report's finished days from it and
// asks the store only for the day in progress.
func TestEvidenceReadsReportChunks(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	h := &evidenceHistory{calls: map[string]int{}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, evidenceStore{&memChunks{data: map[string][]byte{}}})
	s.now = func() time.Time { return now }
	cluster := t.Name()
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
	rep.ComputedAt = now
	s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, readyAt: now, loaded: true}
	before := map[string]int{}
	for k, v := range h.calls {
		before[k] = v
	}
	if _, err := s.GetEvidence(ctx, WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow}); err != nil {
		t.Fatal(err)
	}
	today := now.Truncate(day).Unix()
	for key, n := range h.calls {
		if n == before[key] {
			continue
		}
		parts := strings.Split(key, "|")
		start, _ := strconv.ParseInt(parts[len(parts)-3], 10, 64)
		if start < today {
			t.Errorf("evidence re-fetched a finished day the report cached: %s", key)
		}
	}
}

// A second report reads finished days from the chunk store and asks the
// metrics store only for the day in progress.
func TestReportReadsCachedDays(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	h := &evidenceHistory{calls: map[string]int{}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, evidenceStore{&memChunks{data: map[string][]byte{}}})
	s.now = func() time.Time { return now }
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	if rep := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, nil, func(Progress) {}); rep.Status != StatusReady {
		t.Fatalf("first report: %s %s", rep.Status, rep.Error)
	}
	before := map[string]int{}
	for k, v := range h.calls {
		before[k] = v
	}
	now = now.Add(time.Hour)
	if rep := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, nil, func(Progress) {}); rep.Status != StatusReady {
		t.Fatalf("second report: %s %s", rep.Status, rep.Error)
	}
	today := now.Truncate(day).Unix()
	for key, n := range h.calls {
		if n == before[key] {
			continue
		}
		parts := strings.Split(key, "|")
		if start, _ := strconv.ParseInt(parts[len(parts)-3], 10, 64); start < today {
			t.Errorf("second report re-fetched a cached day: %s", key)
		}
	}
}
