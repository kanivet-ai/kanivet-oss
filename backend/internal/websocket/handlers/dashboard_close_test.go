package handlers_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	ws "github.com/kanivet/backend/internal/websocket"
	"github.com/kanivet/backend/internal/websocket/handlers"
)

func TestDashboardStreamStopsWhenLastSubscriberDisconnects(t *testing.T) {
	mockK8s := &k8s.MockClient{
		TypedClient:    seedFakeClientset(),
		ClusterName:    "test-cluster",
		ClusterVersion: "1.30",
	}

	server := ws.NewServer()
	dashHandler := handlers.NewDashboardHandler(mockK8s, server.Hub())
	server.RegisterHandler("dashboard", dashHandler)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	conn := dialWS(t, testServer.URL)
	conn.WriteJSON(map[string]interface{}{
		"type":    "dashboard",
		"payload": map[string]interface{}{"action": "start", "cluster": "test-cluster"},
	})
	readJSON(t, conn, 5*time.Second)
	if !dashHandler.HasActiveStream("test-cluster") {
		t.Fatal("expected active stream after start")
	}

	conn.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !dashHandler.HasActiveStream("test-cluster") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("dashboard stream kept running after its only subscriber disconnected")
}
