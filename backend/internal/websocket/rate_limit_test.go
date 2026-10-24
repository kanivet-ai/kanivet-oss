package websocket

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

func dialServer(t *testing.T, server *Server) *websocket.Conn {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// A reconnect restarts every open stream at once. Their start messages used
// to share a 10-message bucket with everything else, and the ones past it
// were dropped without a word.
func TestStreamControlMessagesAreNotRateLimited(t *testing.T) {
	server := NewServer()
	defer server.Shutdown(context.Background())
	var handled atomic.Int64
	server.RegisterHandler("logs", core.MessageHandlerFunc(func(context.Context, *core.Connection, *core.IncomingMessage) error {
		handled.Add(1)
		return nil
	}))
	conn := dialServer(t, server)

	const n = 200
	for i := range n {
		if err := conn.WriteJSON(map[string]any{"type": "logs", "payload": map[string]any{"action": "start", "key": i}}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for handled.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d stream starts handled", handled.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A message the limiter refuses is answered, so the client can retry it.
func TestRateLimitedMessageIsAnswered(t *testing.T) {
	server := NewServer()
	defer server.Shutdown(context.Background())
	var handled atomic.Int64
	server.RegisterHandler("other", core.MessageHandlerFunc(func(context.Context, *core.Connection, *core.IncomingMessage) error {
		handled.Add(1)
		return nil
	}))
	conn := dialServer(t, server)

	const n = 3 * rateLimitBurst
	for range n {
		if err := conn.WriteJSON(map[string]any{"type": "other", "id": "req-1", "payload": map[string]any{"key": "k1"}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no rate_limited reply (%d of %d handled): %v", handled.Load(), n, err)
		}
		var reply map[string]any
		if json.Unmarshal(data, &reply) != nil || reply["type"] != "error" {
			continue
		}
		if reply["code"] != "rate_limited" || reply["messageType"] != "other" || reply["key"] != "k1" || reply["requestId"] != "req-1" {
			t.Fatalf("reply %v", reply)
		}
		return
	}
}
