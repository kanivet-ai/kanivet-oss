package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/cloud"
	ws "github.com/kanivet/backend/internal/websocket"
)

// fakeDiscoverer's first run ignores cancellation until released, like a
// provider call that is slow to notice; later runs end with their context.
type fakeDiscoverer struct {
	mu      sync.Mutex
	runs    int
	release chan struct{}
}

func (f *fakeDiscoverer) DiscoverClustersStreaming(ctx context.Context, req cloud.DiscoverRequest, ch chan<- cloud.DiscoveryEvent) {
	defer close(ch)
	f.mu.Lock()
	f.runs++
	n := f.runs
	f.mu.Unlock()
	if n == 1 {
		ch <- cloud.DiscoveryEvent{Type: cloud.DiscoveryEventProgress, Progress: &cloud.DiscoveryProgress{Status: "first"}}
		<-f.release
		// Late output of the superseded run.
		select {
		case ch <- cloud.DiscoveryEvent{Type: cloud.DiscoveryEventError, Error: "stale"}:
		case <-ctx.Done():
		}
		return
	}
	ch <- cloud.DiscoveryEvent{Type: cloud.DiscoveryEventProgress, Progress: &cloud.DiscoveryProgress{Status: "second"}}
	<-ctx.Done()
}

func discoverMsg(action string) map[string]any {
	return map[string]any{"type": "cloud.discover", "payload": map[string]any{"action": action, "key": "k", "provider": "aws"}}
}

func readStatus(t *testing.T, c *websocket.Conn, d time.Duration) (status, errText string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(d))
	var m struct {
		Payload struct {
			Progress struct {
				Status string `json:"status"`
			} `json:"progress"`
			Error string `json:"error"`
		} `json:"payload"`
	}
	if err := c.ReadJSON(&m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m.Payload.Progress.Status, m.Payload.Error
}

// A replayed start used to wait for the old discovery on the connection's
// read loop, stalling every other message on the socket; and the old run's
// deferred delete could remove the new run's entry.
func TestCloudDiscoveryRestartDoesNotBlockReadLoopOrLeakStaleEvents(t *testing.T) {
	d := &fakeDiscoverer{release: make(chan struct{})}
	server := ws.NewServer()
	h := newCloudDiscoveryHandler(d)
	server.RegisterHandler("cloud.discover", h)
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.WriteJSON(discoverMsg("start")); err != nil {
		t.Fatal(err)
	}
	if status, _ := readStatus(t, c, 3*time.Second); status != "first" {
		t.Fatalf("first event = %q", status)
	}

	// The old run is stuck; the new one must still start at once.
	if err := c.WriteJSON(discoverMsg("start")); err != nil {
		t.Fatal(err)
	}
	if status, _ := readStatus(t, c, time.Second); status != "second" {
		t.Fatalf("restarted discovery's first event = %q", status)
	}

	// The old run wakes up: nothing of it may reach the client now.
	close(d.release)
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var m map[string]any
	if err := c.ReadJSON(&m); err == nil {
		t.Fatalf("stale event from the superseded discovery: %v", m)
	}

	// The new run's entry survived the old one finishing.
	found := false
	h.activeStreams.Range(func(_, _ any) bool { found = true; return false })
	if !found {
		t.Fatal("the running discovery was dropped from the active streams")
	}
}
