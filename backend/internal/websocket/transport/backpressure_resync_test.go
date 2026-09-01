package transport

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

// dialOne starts a hub-backed server with the given send-channel size, dials a
// single client, and returns the server-side Connection plus a cleanup func.
func dialOne(t *testing.T, sendChanSize int) (*core.Hub, *core.Connection, func()) {
	t.Helper()
	hubConfig := &core.HubConfig{
		MaxConnections:    100,
		SendChannelSize:   sendChanSize,
		MaxMessageSize:    1 << 20,
		ConnectionTimeout: time.Minute,
		HeartbeatInterval: time.Minute,
	}
	sm := NewSubscriptionManager()
	router := NewRouter()
	hub := core.NewHub(hubConfig, router, sm)
	server := NewServer(hub, nil)
	ts := httptest.NewServer(server)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("dial: %v", err)
	}

	var serverConn *core.Connection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.RangeConnections(func(c *core.Connection) bool {
			serverConn = c
			return false
		})
		if serverConn != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if serverConn == nil {
		client.Close()
		ts.Close()
		t.Fatal("server connection never registered")
	}
	cleanup := func() {
		client.Close()
		ts.Close()
	}
	return hub, serverConn, cleanup
}

func TestBackpressureTriggersResyncCallback(t *testing.T) {
	sm := NewSubscriptionManager()

	var mu sync.Mutex
	var gotTopic string
	var gotConn core.ConnectionID
	calls := 0
	sm.SetOnBackpressureDrop(func(topic string, conn *core.Connection) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotTopic = topic
		gotConn = conn.ID()
	})

	// Build a real connection with a 1-slot send channel via a hub server.
	hub, conn, cleanup := dialOne(t, 1)
	defer cleanup()
	_ = hub

	if _, err := sm.Subscribe("pods", conn); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Saturate the send channel so the broadcast Send returns ErrRateLimitExceeded.
	// The client never reads, so once the write pump is blocked on the socket the
	// buffered channel fills and stays full. Pump until Send reports saturation.
	big := make([]byte, 4096)
	saturated := false
	for i := 0; i < 100000 && !saturated; i++ {
		if err := conn.Send(big); err != nil {
			saturated = true
		}
	}
	if !saturated {
		t.Fatal("could not saturate send channel to provoke backpressure")
	}

	// Keep the channel saturated through the broadcast: retry until the broadcast
	// itself observes the rate-limit drop (the write pump may transiently free a
	// slot between our fill loop and the broadcast's single Send).
	msg := core.NewOutgoingMessage("resource_update", map[string]any{"topic": "pods", "hello": "world"})
	var bErr error
	for i := 0; i < 100000; i++ {
		_ = conn.Send(big) // keep pressure on
		bErr = sm.Broadcast("pods", msg)
		mu.Lock()
		c := calls
		mu.Unlock()
		if c > 0 {
			break
		}
	}
	_ = bErr

	mu.Lock()
	defer mu.Unlock()
	if calls == 0 {
		t.Fatal("expected resync callback to fire on backpressure drop, got none")
	}
	if gotTopic != "pods" {
		t.Fatalf("expected topic 'pods', got %q", gotTopic)
	}
	if gotConn != conn.ID() {
		t.Fatalf("expected resync for conn %s, got %s", conn.ID(), gotConn)
	}
}

func TestNoCallbackWhenNoBackpressure(t *testing.T) {
	sm := NewSubscriptionManager()
	calls := 0
	sm.SetOnBackpressureDrop(func(string, *core.Connection) { calls++ })

	hub, conn, cleanup := dialOne(t, 256)
	defer cleanup()
	_ = hub

	if _, err := sm.Subscribe("pods", conn); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	msg := core.NewOutgoingMessage("resource_update", map[string]any{"topic": "pods", "hello": "world"})
	if err := sm.Broadcast("pods", msg); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no resync callback under normal flow, got %d", calls)
	}
}
