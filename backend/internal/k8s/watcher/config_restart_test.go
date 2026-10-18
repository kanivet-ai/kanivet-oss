package watcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// widgetServer is an apiserver serving one cluster-scoped custom resource,
// with a single object named after the server.
func widgetServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
		switch r.URL.Path {
		case "/api":
			writeJSON(map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
		case "/apis":
			writeJSON(map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": []any{map[string]any{
				"name":             "example.com",
				"versions":         []any{map[string]any{"groupVersion": "example.com/v1", "version": "v1"}},
				"preferredVersion": map[string]any{"groupVersion": "example.com/v1", "version": "v1"},
			}}})
		case "/apis/example.com/v1":
			writeJSON(map[string]any{"kind": "APIResourceList", "groupVersion": "example.com/v1", "resources": []any{
				map[string]any{"name": "widgets", "singularName": "widget", "namespaced": false, "kind": "Widget", "verbs": []string{"get", "list", "watch"}},
			}})
		case "/apis/example.com/v1/widgets":
			if r.URL.Query().Get("watch") != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			writeJSON(map[string]any{
				"apiVersion": "example.com/v1", "kind": "WidgetList",
				"metadata": map[string]any{"resourceVersion": "5"},
				"items": []any{map[string]any{
					"apiVersion": "example.com/v1", "kind": "Widget",
					"metadata": map[string]any{"name": name, "uid": "uid-" + name, "resourceVersion": "5", "creationTimestamp": "2026-10-01T00:00:00Z"},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cachedNames(s *Service, topic string) map[string]bool {
	names := map[string]bool{}
	for _, item := range s.cache.GetAll(topic) {
		names[item["name"].(string)] = true
	}
	return names
}

// A running watch kept the client it was built with: after a kubeconfig
// reload pointed the context at another cluster, the list went on showing the
// old cluster while detail views read the new one. A cache refresh that
// changes the context's config has its watches rebuild their client and
// relist, the stale diff dropping the old cluster's rows.
func TestWatchFollowsAContextPointedElsewhere(t *testing.T) {
	a, b := widgetServer(t, "from-a"), widgetServer(t, "from-b")
	client := useKubeconfig(t, map[string]string{"c": a.URL})
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := NewService(client, hub)
	defer s.Shutdown()
	topic := "items:c:example.com:v1:widgets:"

	if err := s.StartWatch("c", "example.com", "v1", "widgets", "", "age", "desc"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first cluster's list", func() bool { return len(hub.syncCompletes()) == 1 })
	if !cachedNames(s, topic)["from-a"] {
		t.Fatal("the first list did not hold the first cluster's row")
	}

	// The same refresh with nothing changed leaves the watch alone.
	client.RefreshClusterCache("c")
	time.Sleep(100 * time.Millisecond) // the comparison runs in the background
	syncs := len(hub.syncCompletes())
	if syncs != 1 {
		t.Fatalf("a refresh that changed nothing relisted (%d syncs)", syncs)
	}

	cfg, err := os.ReadFile(os.Getenv("KUBECONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	cfg = []byte(strings.ReplaceAll(string(cfg), a.URL, b.URL))
	if err := os.WriteFile(os.Getenv("KUBECONFIG"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	client.RefreshClusterCache("c")

	waitFor(t, "the relist from the second cluster", func() bool {
		names := cachedNames(s, topic)
		return names["from-b"] && !names["from-a"] && len(hub.syncCompletes()) > syncs
	})
	time.Sleep(50 * time.Millisecond) // no second relist follows
	got := hub.syncCompletes()
	if len(got) != syncs+1 || got[len(got)-1].Epoch == 0 {
		t.Fatalf("syncs %+v, want one more, authoritative, after the config change (and none for the no-op refresh)", got)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	var deleted bool
	for _, m := range hub.batched {
		if ev, ok := m.(*ResourceEventMessage); ok && ev.Action == "deleted" && ev.Item["name"] == "from-a" {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("the old cluster's row was not deleted on the client")
	}
}
