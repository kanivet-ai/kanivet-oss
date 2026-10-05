package transport

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

func BenchmarkSubscriptionBroadcast(b *testing.B) {
	hubConfig := &core.HubConfig{
		MaxConnections:    100,
		SendChannelSize:   1 << 14,
		MaxMessageSize:    1 << 20,
		ConnectionTimeout: time.Minute,
		HeartbeatInterval: time.Minute,
	}
	sm := NewSubscriptionManager()
	hub := core.NewHub(hubConfig, NewRouter(), sm)
	ts := httptest.NewServer(NewServer(hub, nil))
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	for i := 0; i < 8; i++ {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			b.Fatal(err)
		}
		defer c.Close()
		go func() {
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for hub.CountConnections() < 8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hub.RangeConnections(func(c *core.Connection) bool {
		_, _ = sm.Subscribe("t", c)
		return true
	})
	msg := core.NewOutgoingMessage("event", map[string]any{"hello": strings.Repeat("x", 512)})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sm.Broadcast("t", msg)
	}
}
