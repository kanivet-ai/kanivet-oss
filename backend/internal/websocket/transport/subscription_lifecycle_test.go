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

func dialN(t *testing.T, n int) (*core.Hub, *DefaultSubscriptionManager, []*core.Connection, func()) {
	t.Helper()
	sm := NewSubscriptionManager()
	hub := core.NewHub(&core.HubConfig{
		MaxConnections:    100,
		SendChannelSize:   64,
		MaxMessageSize:    1 << 20,
		ConnectionTimeout: time.Minute,
		HeartbeatInterval: time.Minute,
	}, NewRouter(), sm)
	server := NewServer(hub, nil)
	ts := httptest.NewServer(server)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	clients := make([]*websocket.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			ts.Close()
			t.Fatalf("dial %d: %v", i, err)
		}
		clients = append(clients, c)
	}

	var conns []*core.Connection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conns = conns[:0]
		hub.RangeConnections(func(c *core.Connection) bool {
			conns = append(conns, c)
			return true
		})
		if len(conns) == n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(conns) != n {
		ts.Close()
		t.Fatalf("expected %d registered connections, got %d", n, len(conns))
	}
	cleanup := func() {
		for _, c := range clients {
			c.Close()
		}
		ts.Close()
	}
	return hub, sm, conns, cleanup
}

func TestSubscribeReportsNewPair(t *testing.T) {
	_, sm, conns, cleanup := dialN(t, 1)
	defer cleanup()

	added, err := sm.Subscribe("t", conns[0])
	if err != nil || !added {
		t.Fatalf("first subscribe must report added=true, got added=%v err=%v", added, err)
	}
	added, err = sm.Subscribe("t", conns[0])
	if err != nil || added {
		t.Fatalf("duplicate subscribe must report added=false, got added=%v err=%v", added, err)
	}
}

func TestUnsubscribeReportsRemovalOnce(t *testing.T) {
	_, sm, conns, cleanup := dialN(t, 1)
	defer cleanup()

	if _, err := sm.Subscribe("t", conns[0]); err != nil {
		t.Fatal(err)
	}
	removed, err := sm.Unsubscribe("t", conns[0])
	if err != nil || !removed {
		t.Fatalf("first unsubscribe must report removed=true, got removed=%v err=%v", removed, err)
	}
	removed, err = sm.Unsubscribe("t", conns[0])
	if err != nil || removed {
		t.Fatalf("second unsubscribe must report removed=false, got removed=%v err=%v", removed, err)
	}
}

func TestSubscriptionRemovedCallbackFiresPerPair(t *testing.T) {
	_, sm, conns, cleanup := dialN(t, 2)
	defer cleanup()

	var mu sync.Mutex
	var fired []string
	sm.SetOnSubscriptionRemoved(func(topic string, _ *core.Connection) {
		mu.Lock()
		fired = append(fired, topic)
		mu.Unlock()
	})

	sm.Subscribe("t", conns[0])
	sm.Subscribe("t", conns[1])

	sm.Unsubscribe("t", conns[0])
	mu.Lock()
	count := len(fired)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("removing one of two subscribers must fire removal callback exactly once, got %d", count)
	}

	sm.Unsubscribe("t", conns[0])
	mu.Lock()
	count = len(fired)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("repeated unsubscribe must not re-fire callback, got %d", count)
	}

	sm.Unsubscribe("t", conns[1])
	mu.Lock()
	count = len(fired)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("second subscriber removal must fire callback, got %d total", count)
	}
}

func TestUnsubscribeAllFiresRemovalPerTopic(t *testing.T) {
	_, sm, conns, cleanup := dialN(t, 2)
	defer cleanup()

	var mu sync.Mutex
	fired := map[string]int{}
	sm.SetOnSubscriptionRemoved(func(topic string, _ *core.Connection) {
		mu.Lock()
		fired[topic]++
		mu.Unlock()
	})

	sm.Subscribe("t1", conns[0])
	sm.Subscribe("t2", conns[0])
	sm.Subscribe("t1", conns[1])

	if err := sm.UnsubscribeAll(conns[0]); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if fired["t1"] != 1 || fired["t2"] != 1 {
		t.Fatalf("UnsubscribeAll must fire removal once per (conn,topic) pair, got %v", fired)
	}
}
