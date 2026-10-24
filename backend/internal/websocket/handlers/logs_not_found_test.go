package handlers_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	ws "github.com/kanivet/backend/internal/websocket"
	"github.com/kanivet/backend/internal/websocket/handlers"
	"k8s.io/client-go/kubernetes/fake"
)

// Logs of a pod or workload that does not exist used to get a "connected"
// and nothing else, which the client showed as a live, empty stream.
func TestLogsOfAMissingResourceEndWithNotFound(t *testing.T) {
	for _, tc := range []struct {
		resourceType, want string
	}{
		{"", "pod default/gone no longer exists"},
		{"deployment", "deployment default/gone no longer exists"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			server := ws.NewServer()
			client := &k8s.MockClient{TypedClient: fake.NewSimpleClientset(), ClusterName: "test-cluster"}
			server.RegisterHandler("logs", handlers.NewLogsHandler(client))
			ts := httptest.NewServer(server)
			t.Cleanup(ts.Close)
			conn := dialWS(t, ts.URL)
			t.Cleanup(func() { conn.Close() })

			if err := conn.WriteJSON(map[string]any{"type": "logs", "payload": map[string]any{
				"action": "start", "key": "k1", "cluster": "test-cluster", "namespace": "default",
				"name": "gone", "resourceType": tc.resourceType, "follow": true,
			}}); err != nil {
				t.Fatal(err)
			}

			var types []string
			for {
				msg := readJSON(t, conn, 5*time.Second)
				payload, _ := msg["payload"].(map[string]any)
				typ, _ := payload["type"].(string)
				types = append(types, typ)
				if typ == "error" {
					t.Fatalf("a missing resource is reported as an error: %v", payload)
				}
				if typ != "end" {
					continue
				}
				if payload["key"] != "k1" || payload["reason"] != "not_found" || payload["message"] != tc.want {
					t.Fatalf("end = %v, want reason not_found and message %q", payload, tc.want)
				}
				break
			}
			if types[0] != "connected" {
				t.Errorf("messages = %v, want connected first", types)
			}
		})
	}
}
