package rightsizing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/podcache"
	"github.com/kanivet/backend/internal/metrics"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// carryPods lists the Deployment api in apps (other was deleted) and front
// in web, and reports its health as health.
type carryPods struct{ health podcache.Health }

func (carryPods) List(context.Context, string) ([]*v1.Pod, error) {
	controller := true
	pod := func(ns, name string) *v1.Pod {
		return &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns, Name: name + "-bcdfgh-bcdfg",
				Labels:          map[string]string{"pod-template-hash": "bcdfgh"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: name + "-bcdfgh", Controller: &controller}},
			},
			Spec:   v1.PodSpec{Containers: []v1.Container{{Name: "main"}}},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		}
	}
	return []*v1.Pod{pod("apps", "api"), pod("web", "front")}, nil
}

func (p carryPods) Health(string) podcache.Health { return p.health }

// appsDown fails every history query about the apps namespace.
type appsDown struct{ *evidenceHistory }

func (h appsDown) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if strings.Contains(query, `"apps"`) {
		return nil, errors.New("query timed out")
	}
	return h.evidenceHistory.QueryRange(ctx, cluster, query, start, end, step)
}

// A namespace whose history query keeps failing used to carry all of its
// previous workloads over, deleted ones included, report after report.
func TestFailedNamespaceCarriesOnlyLiveWorkloads(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	s := NewService(&k8s.MockClient{}, appsDown{&evidenceHistory{calls: map[string]int{}}}, evidenceFixtures{}, carryPods{}, nil)
	s.now = func() time.Time { return now }

	firstComputed := now.Add(-3 * time.Hour)
	prev := &Report{Status: StatusReady, ComputedAt: firstComputed, Workloads: []WorkloadReport{
		{Namespace: "apps", Kind: "Deployment", Name: "api", MonthlySavings: 10},
		{Namespace: "apps", Kind: "Deployment", Name: "other", MonthlySavings: 500},
	}}
	rep := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, prev, func(Progress) {})
	if rep.Status != StatusReady {
		t.Fatalf("status=%s error=%q", rep.Status, rep.Error)
	}
	byName := map[string]WorkloadReport{}
	for _, w := range rep.Workloads {
		byName[w.Namespace+"/"+w.Name] = w
	}
	if _, ok := byName["apps/other"]; ok {
		t.Fatal("a deleted workload was carried over")
	}
	api, ok := byName["apps/api"]
	if !ok {
		t.Fatalf("the live workload of the failed namespace was not carried: %v", byName)
	}
	if !api.Carried || api.ComputedAt == nil || !api.ComputedAt.Equal(firstComputed) {
		t.Fatalf("carried workload not stamped: carried=%t computedAt=%v", api.Carried, api.ComputedAt)
	}
	if front := byName["web/front"]; front.Carried {
		t.Fatal("a freshly computed workload is marked carried")
	}

	// Carried again, it keeps the time it was actually computed.
	rep.ComputedAt = now
	again := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, rep, func(Progress) {})
	for _, w := range again.Workloads {
		if w.Name == "api" && (w.ComputedAt == nil || !w.ComputedAt.Equal(firstComputed)) {
			t.Fatalf("re-carried workload computedAt=%v, want %v", w.ComputedAt, firstComputed)
		}
	}
}

// A report computed while the pod cache's watch is failing is computed from
// frozen pods, and says so.
func TestReportFlagsPodsFromAFailingWatch(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	failing := now.Add(-20 * time.Minute)
	for _, tc := range []struct {
		name   string
		health podcache.Health
		stale  bool
	}{
		{"healthy", podcache.Health{Synced: true, LastSyncAt: now}, false},
		{"failing", podcache.Health{Synced: true, LastSyncAt: failing.Add(-time.Hour), FailingSince: failing, LastError: "unauthorized"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(&k8s.MockClient{}, &evidenceHistory{calls: map[string]int{}}, evidenceFixtures{}, carryPods{health: tc.health}, nil)
			s.now = func() time.Time { return now }
			rep := s.compute(ctx, t.Name(), ProfileBalanced, defaultWindow, nil, func(Progress) {})
			if rep.PodsStale != tc.stale {
				t.Fatalf("podsStale=%t, want %t", rep.PodsStale, tc.stale)
			}
			if tc.stale && (rep.PodsStaleSince == nil || !rep.PodsStaleSince.Equal(failing)) {
				t.Fatalf("podsStaleSince=%v, want %v", rep.PodsStaleSince, failing)
			}
		})
	}
}
