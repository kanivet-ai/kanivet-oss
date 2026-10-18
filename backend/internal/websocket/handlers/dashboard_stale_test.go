package handlers_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/podcache"
	"github.com/kanivet/backend/internal/websocket/handlers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

// frozenPods serves pods like the pod cache, with a health it is told.
type frozenPods struct {
	mu     sync.Mutex
	health podcache.Health
}

func (f *frozenPods) List(ctx context.Context, cluster string) ([]*corev1.Pod, error) {
	return []*corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}}, nil
}

func (f *frozenPods) Health(cluster string) podcache.Health {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health
}

func (f *frozenPods) set(h podcache.Health) {
	f.mu.Lock()
	f.health = h
	f.mu.Unlock()
}

func staleSections(data map[string]interface{}) map[string]bool {
	out := map[string]bool{}
	list, _ := data["staleSections"].([]interface{})
	for _, s := range list {
		out[s.(string)] = true
	}
	return out
}

func sectionAt(data map[string]interface{}, section string) int64 {
	at, _ := data["sectionUpdatedAt"].(map[string]interface{})
	n, _ := at[section].(float64)
	return int64(n)
}

// The pod cache keeps serving its pods while its watch fails; the dashboard
// used to broadcast them as a fresh update.
func TestDashboardMarksPodSectionsStaleWhileThePodsWatchFails(t *testing.T) {
	cs := seedFakeClientset()
	pods := &frozenPods{}
	_, conn := startDashboard(t, &failingCountClient{MockClient: &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}}, func(h *handlers.DashboardHandler) {
		h.SetPodLister(pods)
	})

	first := requestDashboard(t, conn)
	if first["stale"] != false || first["staleSections"] != nil {
		t.Fatalf("a current dashboard is flagged stale: stale=%v sections=%v", first["stale"], first["staleSections"])
	}
	if sectionAt(first, "podStatus") == 0 {
		t.Fatalf("no freshness time for the pod status: %v", first["sectionUpdatedAt"])
	}

	failingSince := time.Now().Add(-2 * time.Minute).Truncate(time.Millisecond)
	pods.set(podcache.Health{Synced: true, FailingSince: failingSince, LastError: "connection refused"})

	second := requestDashboard(t, conn)
	if second["stale"] != true {
		t.Fatalf("stale = %v while the pods watch fails", second["stale"])
	}
	got := staleSections(second)
	for _, s := range []string{"podStatus", "criticalAlerts", "resourceCapacity", "resourceCounts"} {
		if !got[s] {
			t.Errorf("%s not flagged stale: %v", s, second["staleSections"])
		}
	}
	if got["nodeStatus"] || got["workloadStatus"] || got["events"] {
		t.Errorf("sections read fresh flagged stale: %v", second["staleSections"])
	}
	if since := int64(second["staleSince"].(float64)); since != failingSince.UnixMilli() {
		t.Errorf("staleSince = %d, want when the failures began %d", since, failingSince.UnixMilli())
	}
	if at := sectionAt(second, "podStatus"); at != failingSince.UnixMilli() {
		t.Errorf("pod status stamped %d, want %d", at, failingSince.UnixMilli())
	}
	if reason, _ := second["staleReason"].(string); !strings.Contains(reason, "connection refused") {
		t.Errorf("staleReason = %q", reason)
	}

	pods.set(podcache.Health{Synced: true, LastSyncAt: time.Now()})
	third := requestDashboard(t, conn)
	if third["stale"] != false || third["staleSections"] != nil {
		t.Errorf("still stale after the watch recovered: %v", third["staleSections"])
	}
}

// Pods the cache serves while its watch fails were not read either: with the
// nodes failing too, the cluster is down and the clients are told.
func TestDashboardReportsUnreachableWhenNodesFailAndPodsAreFrozen(t *testing.T) {
	cs := seedFakeClientset()
	pods := &frozenPods{}
	pods.set(podcache.Health{Synced: true, FailingSince: time.Now(), LastError: "connection refused"})
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	_, conn := startDashboard(t, &failingCountClient{MockClient: &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}}, func(h *handlers.DashboardHandler) {
		h.SetPodLister(pods)
	})
	if err := conn.WriteJSON(startMsg()); err != nil {
		t.Fatal(err)
	}
	msg := readJSON(t, conn, 5*time.Second)
	if errText, _ := msg["error"].(string); !strings.Contains(errText, "unreachable") {
		t.Fatalf("error = %q, want the cluster reported unreachable", errText)
	}
	if msg["data"] != nil {
		t.Fatalf("an unreachable cluster must not carry data: %v", msg["data"])
	}
}

// A section carried forward from the previous update keeps the time it was
// last read and is listed as stale, rather than shown as current.
func TestDashboardCarriedForwardSectionsKeepTheirTime(t *testing.T) {
	cs := seedFakeClientset()
	_, conn := startDashboard(t, &failingCountClient{MockClient: &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}}, nil)

	first := requestDashboard(t, conn)
	workloadsAt := sectionAt(first, "workloadStatus")
	if workloadsAt == 0 {
		t.Fatalf("no freshness time for workloads: %v", first["sectionUpdatedAt"])
	}

	var fail atomic.Bool
	fail.Store(true)
	cs.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, errors.New("deployments unavailable")
		}
		return false, nil, nil
	})
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, errors.New("nodes unavailable")
		}
		return false, nil, nil
	})
	time.Sleep(5 * time.Millisecond)

	second := requestDashboard(t, conn)
	if second["stale"] != true {
		t.Fatalf("stale = %v with carried-forward sections", second["stale"])
	}
	got := staleSections(second)
	for _, s := range []string{"workloadStatus", "nodeStatus", "resourceCounts", "resourceCapacity"} {
		if !got[s] {
			t.Errorf("%s not flagged stale: %v", s, second["staleSections"])
		}
	}
	if got["podStatus"] {
		t.Errorf("pod status read fresh but flagged stale")
	}
	if at := sectionAt(second, "workloadStatus"); at != workloadsAt {
		t.Errorf("carried workloads stamped %d, want the first read's %d", at, workloadsAt)
	}
	if since := int64(second["staleSince"].(float64)); since > workloadsAt {
		t.Errorf("staleSince %d is newer than the carried data (%d)", since, workloadsAt)
	}
	reason, _ := second["staleReason"].(string)
	if !strings.Contains(reason, "nodes unavailable") || !strings.Contains(reason, "deployments unavailable") {
		t.Errorf("staleReason = %q", reason)
	}
}
