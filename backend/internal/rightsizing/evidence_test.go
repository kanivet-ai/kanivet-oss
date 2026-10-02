package rightsizing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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
	var result []metrics.HistorySeries
	for _, wk := range []string{"api", "other"} {
		value := float32(0.25)
		if strings.Contains(query, "memory") || strings.Contains(query, `resource="memory"`) {
			value = 128 << 20
		} else if strings.Contains(query, "changes(") || strings.Contains(query, "throttled") {
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
			for key, count := range h.calls {
				if !reportQueries[key] {
					t.Errorf("evidence issued a query the report did not cache: %s", key)
				} else if count != 2 {
					t.Errorf("report query not reused by evidence (calls=%d): %s", count, key)
				}
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

type cachedEvidenceSource struct {
	metricsSource
	fixture *evidenceHistory
	t       *testing.T
}

func (s cachedEvidenceSource) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if !metrics.CacheOnly(ctx) {
		s.t.Error("cached evidence attempted upstream history")
	}
	return s.fixture.QueryRange(ctx, cluster, query, start, end, step)
}
func (s cachedEvidenceSource) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	if !metrics.CacheOnly(ctx) {
		s.t.Error("cached evidence attempted upstream probe")
	}
	return s.fixture.QueryInstant(ctx, cluster, query, at)
}

func TestEvidenceCachedSnapshotThenFreshGrid(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	h := &evidenceHistory{calls: map[string]int{}}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, nil)
	s.now = func() time.Time { return now }
	cluster := t.Name()
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	rep := s.compute(ctx, cluster, ProfileBalanced, defaultWindow, nil, func(Progress) {})
	rep.ComputedAt = now
	s.runs[runKey(cluster, ProfileBalanced, defaultWindow, "mimir")] = &run{ready: rep, loaded: true}
	// Cached evidence must not touch pod listing, discovery, rates or Kubernetes.
	s.metrics = cachedEvidenceSource{fixture: h, t: t}
	s.k8s, s.rates, s.pods = nil, nil, nil
	now = now.Add(10 * time.Minute)
	q := WorkloadQuery{Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api", Profile: ProfileBalanced, Window: defaultWindow, CacheOnly: true}
	cached, err := s.GetEvidence(ctx, q)
	if err != nil || !cached.AsOf.Equal(rep.AsOf) {
		t.Fatalf("cached=%v err=%v", cached, err)
	}
	if cached.Workload.Containers[0].CPU.P95 != 0.25 {
		t.Fatal("incorrect cached workload")
	}
	s.metrics, s.k8s, s.rates, s.pods = h, &k8s.MockClient{}, evidenceFixtures{}, evidenceFixtures{}
	q.CacheOnly, q.Refresh = false, true
	fresh, err := s.GetEvidence(ctx, q)
	if err != nil || !fresh.AsOf.After(cached.AsOf) || !fresh.AsOf.Equal(now) {
		t.Fatalf("fresh=%v err=%v", fresh, err)
	}
	q.CacheOnly, q.Refresh, q.Provider = true, false, "prometheus"
	if _, err := s.GetEvidence(ctx, q); err != metrics.ErrCacheMiss {
		t.Fatalf("provider switch reused snapshot: %v", err)
	}
}
