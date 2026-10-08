package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// workerHarness serves a hub whose connections use a short PongWait, and
// returns a client that answers pings (it keeps reading).
type workerHarness struct {
	hub    *Hub
	router *mockRouter
	srv    *httptest.Server
}

func newWorkerHarness(t *testing.T, pongWait time.Duration) *workerHarness {
	t.Helper()
	router := newMockRouter()
	hub := NewHub(&HubConfig{
		MaxConnections:    100,
		HeartbeatInterval: time.Minute,
		ConnectionTimeout: time.Minute,
		MaxMessageSize:    1 << 20,
		SendChannelSize:   16,
	}, router, newMockSubscriptionManager())
	h := &workerHarness{hub: hub, router: router}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		cfg := DefaultConnectionConfig()
		cfg.PongWait = pongWait
		cfg.PingInterval = pongWait / 4
		_ = hub.RegisterConnection(NewConnection("", c, cfg))
	}))
	t.Cleanup(func() {
		h.srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	})
	return h
}

func (h *workerHarness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func send(t *testing.T, c *websocket.Conn, typ string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":%q,"payload":{}}`, typ))); err != nil {
		t.Fatal(err)
	}
}

func waitCount(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for h.CountConnections() != want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.CountConnections(); got != want {
		t.Fatalf("connections = %d, want %d", got, want)
	}
}

// A handler that outlives PongWait must not get the client disconnected: the
// read loop keeps running, so pongs keep extending the deadline.
func TestSlowHandlerDoesNotStarveReadLoop(t *testing.T) {
	h := newWorkerHarness(t, 400*time.Millisecond)
	release := make(chan struct{})
	var handled atomic.Int32
	_ = h.router.Register("slow", MessageHandlerFunc(func(context.Context, *Connection, *IncomingMessage) error {
		<-release
		return nil
	}))
	_ = h.router.Register("fast", MessageHandlerFunc(func(context.Context, *Connection, *IncomingMessage) error {
		handled.Add(1)
		return nil
	}))

	c := h.dial(t)
	waitCount(t, h.hub, 1)
	send(t, c, "slow")
	time.Sleep(1300 * time.Millisecond) // > 3x PongWait
	close(release)
	send(t, c, "fast")

	deadline := time.Now().Add(2 * time.Second)
	for handled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if handled.Load() != 1 {
		t.Fatal("connection was dropped while its handler was running")
	}
	if h.hub.CountConnections() != 1 {
		t.Fatal("connection no longer registered")
	}
}

// Messages from one connection are handled strictly in order.
func TestHandlersRunInOrder(t *testing.T) {
	h := newWorkerHarness(t, time.Minute)
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	for _, typ := range []string{"a", "b", "c", "d"} {
		typ := typ
		_ = h.router.Register(MessageType(typ), MessageHandlerFunc(func(context.Context, *Connection, *IncomingMessage) error {
			if typ == "a" {
				time.Sleep(100 * time.Millisecond)
			}
			mu.Lock()
			got = append(got, typ)
			n := len(got)
			mu.Unlock()
			if n == 4 {
				close(done)
			}
			return nil
		}))
	}
	c := h.dial(t)
	waitCount(t, h.hub, 1)
	for _, typ := range []string{"a", "b", "c", "d"} {
		send(t, c, typ)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handlers did not all run")
	}
	if strings.Join(got, "") != "abcd" {
		t.Fatalf("order = %v", got)
	}
}

// A handler panic ends the connection (as it did when handlers ran on the
// read loop) instead of killing the process or leaking the worker.
func TestHandlerPanicClosesConnection(t *testing.T) {
	h := newWorkerHarness(t, time.Minute)
	_ = h.router.Register("boom", MessageHandlerFunc(func(context.Context, *Connection, *IncomingMessage) error {
		panic("boom")
	}))
	c := h.dial(t)
	waitCount(t, h.hub, 1)
	send(t, c, "boom")
	waitCount(t, h.hub, 0)
}

// Disconnecting must not leave the worker goroutine behind, even with queued
// messages and a blocked handler.
func TestWorkerDoesNotLeakOnDisconnect(t *testing.T) {
	h := newWorkerHarness(t, time.Minute)
	release := make(chan struct{})
	_ = h.router.Register("slow", MessageHandlerFunc(func(ctx context.Context, _ *Connection, _ *IncomingMessage) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}))
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		c := h.dial(t)
		send(t, c, "slow")
		send(t, c, "slow")
		_ = c.Close()
	}
	close(release)
	waitCount(t, h.hub, 0)
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Fatalf("goroutines %d -> %d after disconnects", before, n)
	}
}

func TestMaxConnectionsIsExactUnderConcurrency(t *testing.T) {
	router := newMockRouter()
	hub := NewHub(&HubConfig{MaxConnections: 10, HeartbeatInterval: time.Minute, ConnectionTimeout: time.Minute, MaxMessageSize: 1024, SendChannelSize: 4}, router, newMockSubscriptionManager())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if err := hub.RegisterConnection(NewConnection("", c, DefaultConnectionConfig())); err != nil {
			_ = c.Close()
		}
	}))
	defer srv.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	}()

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err == nil {
				defer c.Close()
				time.Sleep(300 * time.Millisecond)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	if n := hub.CountConnections(); n != 10 {
		t.Fatalf("CountConnections = %d, want exactly 10", n)
	}
	wg.Wait()
	waitCount(t, hub, 0)
}
