package transport

import (
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

// A connection refused at the limit is closed through the Connection wrapper:
// the client gets a normal close frame and the write pump does not linger.
func TestRejectedConnectionIsClosedCleanly(t *testing.T) {
	hub := core.NewHub(&core.HubConfig{
		MaxConnections:    1,
		SendChannelSize:   8,
		MaxMessageSize:    1 << 20,
		ConnectionTimeout: time.Minute,
		HeartbeatInterval: time.Minute,
	}, NewRouter(), NewSubscriptionManager())
	ts := httptest.NewServer(NewServer(hub, nil))
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for hub.CountConnections() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	before := runtime.NumGoroutine()
	second, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = second.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("rejected client saw %v, want a normal close frame", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := runtime.NumGoroutine(); n > before+3 {
		t.Fatalf("goroutines grew %d -> %d after a rejected connection", before, n)
	}
}
