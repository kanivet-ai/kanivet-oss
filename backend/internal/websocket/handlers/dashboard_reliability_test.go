package handlers_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// failingCountClient counts successfully until told to fail.
type failingCountClient struct {
	*k8s.MockClient
	fail atomic.Bool
}

func (c *failingCountClient) GetResourceCount(ctx context.Context, cluster string, gvr schema.GroupVersionResource) (int, error) {
	if c.fail.Load() {
		return 0, errors.New("apiserver unavailable")
	}
	return 7, nil
}

func startMsg() map[string]interface{} {
	return map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "start", "cluster": "test-cluster"},
	}
}

// A failed fetch used to overwrite the counts with zero and the failed
// sections with empty ones, and the clients showed that as the truth.
func TestDashboardKeepsPreviousValuesForSectionsThatFail(t *testing.T) {
	cs := seedFakeClientset()
	client := &failingCountClient{MockClient: &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}}
	_, conn := startDashboard(t, client, nil)

	first := requestDashboard(t, conn)
	firstCounts := first["resourceCounts"].(map[string]interface{})
	if firstCounts[":namespaces"] != float64(7) {
		t.Fatalf("setup: namespaces count = %v", firstCounts[":namespaces"])
	}

	client.fail.Store(true)
	var failList atomic.Bool
	failList.Store(true)
	cs.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failList.Load() {
			return true, nil, errors.New("deployments unavailable")
		}
		return false, nil, nil
	})

	if err := conn.WriteJSON(startMsg()); err != nil { // a remount refreshes
		t.Fatal(err)
	}
	second := readJSON(t, conn, 5*time.Second)["data"].(map[string]interface{})

	counts := second["resourceCounts"].(map[string]interface{})
	if counts[":namespaces"] != float64(7) || counts["storage.k8s.io:storageclasses"] != float64(7) {
		t.Errorf("counts zeroed by a failed count: %v", counts)
	}
	if got := counts["apps:deployments"]; got != float64(1) {
		t.Errorf("deployments count = %v after a failed list, want the previous 1", got)
	}
	workloads := second["workloadStatus"].(map[string]interface{})
	if d := workloads["deployments"].(map[string]interface{}); d["total"] != float64(1) {
		t.Errorf("deployment health reset by a failed list: %v", d)
	}
}

// When neither pods nor nodes can be read the cluster is down; the clients
// are told instead of being sent an all-zero dashboard.
func TestDashboardReportsAnUnreachableCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	boom := func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	}
	cs.PrependReactor("list", "pods", boom)
	cs.PrependReactor("list", "nodes", boom)
	_, conn := startDashboard(t, &k8s.MockClient{TypedClient: cs, ClusterName: "test-cluster"}, nil)

	if err := conn.WriteJSON(startMsg()); err != nil {
		t.Fatal(err)
	}
	msg := readJSON(t, conn, 5*time.Second)
	if msg["type"] != "dashboard" {
		t.Fatalf("type = %v", msg["type"])
	}
	if errText, _ := msg["error"].(string); errText == "" {
		t.Fatalf("no error in the message: %v", msg)
	}
	if msg["data"] != nil {
		t.Fatalf("an unreachable cluster must not carry data: %v", msg["data"])
	}
}

func subscribe(t *testing.T, conn *websocket.Conn, action string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": action, "cluster": "test-cluster"},
	}); err != nil {
		t.Fatal(err)
	}
}

// One subscriber leaving must not stop a stream another one watches, and the
// last one leaving must; a duplicate start never doubles a stream.
func TestDashboardStreamLifecycleWithTwoSubscribers(t *testing.T) {
	h, a := startDashboard(t, &k8s.MockClient{TypedClient: seedFakeClientset(), ClusterName: "test-cluster"}, nil)
	b := dialWS(t, "http://"+a.RemoteAddr().String())
	t.Cleanup(func() { b.Close() })

	subscribe(t, a, "start")
	readJSON(t, a, 5*time.Second)
	subscribe(t, b, "start")
	subscribe(t, b, "start") // replayed after a reconnect
	readJSON(t, b, 5*time.Second)
	if !h.HasActiveStream("test-cluster") {
		t.Fatal("no stream")
	}

	subscribe(t, a, "stop")
	time.Sleep(300 * time.Millisecond)
	if !h.HasActiveStream("test-cluster") {
		t.Fatal("stream stopped while a subscriber remained")
	}
	subscribe(t, b, "stop")
	deadline := time.Now().Add(3 * time.Second)
	for h.HasActiveStream("test-cluster") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.HasActiveStream("test-cluster") {
		t.Fatal("stream kept running after the last subscriber stopped")
	}
}
