package handlers_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
	ws "github.com/kanivet/backend/internal/websocket"
	"github.com/kanivet/backend/internal/websocket/handlers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// countingClient records which resources the dashboard counts separately.
type countingClient struct {
	*k8s.MockClient
	mu      sync.Mutex
	counted []string
}

func (c *countingClient) GetResourceCount(ctx context.Context, cluster string, gvr schema.GroupVersionResource) (int, error) {
	c.mu.Lock()
	c.counted = append(c.counted, gvr.Resource)
	c.mu.Unlock()
	return 0, nil
}

func startDashboard(t *testing.T, client k8s.Interface, configure func(*handlers.DashboardHandler)) (*handlers.DashboardHandler, *websocket.Conn) {
	t.Helper()
	server := ws.NewServer()
	h := handlers.NewDashboardHandler(client, server.Hub())
	if configure != nil {
		configure(h)
	}
	server.RegisterHandler("dashboard", h)
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	conn := dialWS(t, ts.URL)
	t.Cleanup(func() { conn.Close() })
	t.Cleanup(h.Shutdown)
	return h, conn
}

func requestDashboard(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	if err := conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "start", "cluster": "test-cluster"},
	}); err != nil {
		t.Fatal(err)
	}
	data, ok := readJSON(t, conn, 5*time.Second)["data"].(map[string]interface{})
	if !ok {
		t.Fatal("dashboard message without data")
	}
	return data
}

// emulateKeyOrderedLimit makes a limited events LIST return the first events
// in storage-key order (namespace/name), as the apiserver does; the fake
// clientset otherwise ignores Limit.
func emulateKeyOrderedLimit(cs *fake.Clientset) {
	cs.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		limit := action.(k8stesting.ListActionImpl).ListOptions.Limit
		if limit == 0 {
			return false, nil, nil
		}
		obj, err := cs.Tracker().List(corev1.SchemeGroupVersion.WithResource("events"), corev1.SchemeGroupVersion.WithKind("Event"), "")
		if err != nil {
			return true, nil, err
		}
		list := obj.(*corev1.EventList)
		sort.Slice(list.Items, func(i, j int) bool {
			a, b := list.Items[i], list.Items[j]
			return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
		})
		if int(limit) < len(list.Items) {
			list.Items = list.Items[:limit]
		}
		return true, list, nil
	})
}

// A limited events LIST pages through the key space, so on a cluster with more
// than a page of events the panel showed old events of the first namespaces.
// The event store, kept current by a watch, has the newest ones.
func TestDashboardRecentEventsComeFromTheEventStore(t *testing.T) {
	cs := seedFakeClientset()
	for i := 0; i < 150; i++ {
		ev := &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: fmt.Sprintf("old-%03d", i), Namespace: "aaa"},
			Reason:         "Old",
			LastTimestamp:  metav1.NewTime(time.Now().Add(-time.Hour)),
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "p", Namespace: "aaa"},
		}
		if err := cs.Tracker().Add(ev); err != nil {
			t.Fatal(err)
		}
	}
	emulateKeyOrderedLimit(cs)
	now := time.Now()
	store := func(cluster string, n int) ([]db.K8sEvent, bool) {
		if cluster != "test-cluster" || n < 2 {
			t.Errorf("store asked for %d events of %q", n, cluster)
		}
		return []db.K8sEvent{
			{Reason: "Scheduled", Type: "Normal", EventTime: now.Add(-10 * time.Second), InvolvedObjectKind: "Pod", InvolvedObjectName: "api-1", InvolvedObjectNamespace: "zz-prod"},
			{Reason: "BackOff", Type: "Warning", LastTimestamp: now.Add(-20 * time.Second), EventTime: now.Add(-20 * time.Second), InvolvedObjectKind: "Pod", InvolvedObjectName: "api-2", InvolvedObjectNamespace: "zz-prod"},
		}, true
	}
	_, conn := startDashboard(t, &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}, func(h *handlers.DashboardHandler) {
		h.SetRecentEvents(store)
	})

	events, _ := requestDashboard(t, conn)["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("got %d events, want the 2 from the store", len(events))
	}
	first := events[0].(map[string]interface{})
	if first["reason"] != "Scheduled" || first["namespace"] != "zz-prod" || first["object"] != "Pod/api-1" {
		t.Fatalf("newest event = %v", first)
	}
}

// Until the store has synced, the panel keeps using the API.
func TestDashboardRecentEventsFallBackToTheAPI(t *testing.T) {
	_, conn := startDashboard(t, &k8s.MockClient{TypedClient: seedFakeClientset(), ClusterName: "test-cluster"}, func(h *handlers.DashboardHandler) {
		h.SetRecentEvents(func(string, int) ([]db.K8sEvent, bool) { return nil, false })
	})
	events, _ := requestDashboard(t, conn)["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("got %d events, want the 2 seeded in the cluster", len(events))
	}
}

// Each update listed workloads and services in full and then counted them again
// with separate requests, and re-ran the metrics-server discovery probe before
// fetching anything else.
func TestDashboardUpdatesReuseListsAndCacheTheMetricsProbe(t *testing.T) {
	cs := seedFakeClientset()
	client := &countingClient{MockClient: &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}}
	_, conn := startDashboard(t, client, nil)

	first := requestDashboard(t, conn)
	requestDashboard(t, conn) // a remount restarts the stream

	counts := first["resourceCounts"].(map[string]interface{})
	for key, want := range map[string]float64{"apps:deployments": 1, "apps:statefulsets": 1, "apps:daemonsets": 1, ":services": 0, ":pods": 3, ":nodes": 2} {
		if got, ok := counts[key].(float64); !ok || got != want {
			t.Errorf("resourceCounts[%q] = %v, want %v", key, counts[key], want)
		}
	}
	client.mu.Lock()
	counted := append([]string(nil), client.counted...)
	client.mu.Unlock()
	for _, r := range counted {
		switch r {
		case "deployments", "statefulsets", "daemonsets", "services":
			t.Errorf("%s counted separately although the update lists them in full", r)
		}
	}

	probes := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "get" && a.GetResource().Resource == "group" {
			probes++
		}
		if l, ok := a.(k8stesting.ListActionImpl); ok && l.ListOptions.Limit == 0 && l.GetResource().Resource != "events" {
			if l.ListOptions.ResourceVersion != "0" {
				t.Errorf("full %s list without resourceVersion=0 reads etcd instead of the watch cache", l.GetResource().Resource)
			}
		}
	}
	if probes != 1 {
		t.Errorf("metrics-server discovery probed %d times over two updates, want 1", probes)
	}
}
