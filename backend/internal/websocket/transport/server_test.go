package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

func newTestHub() *core.Hub {
	router := &mockRouter{handlers: make(map[core.MessageType]core.MessageHandler)}
	subManager := &mockSubscriptionManager{}
	config := &core.HubConfig{
		MaxConnections:    100,
		MaxMessageSize:    1024,
		HeartbeatInterval: 30 * time.Second,
		SendChannelSize:   10,
		EnableCompression: true,
	}
	return core.NewHub(config, router, subManager)
}

func TestServerWebSocketUpgrade(t *testing.T) {
	hub := newTestHub()
	server := NewServer(hub, DefaultServerConfig())

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	// Test WebSocket upgrade
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected status %d, got %d", http.StatusSwitchingProtocols, resp.StatusCode)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.CountConnections() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("Expected 1 connection registered, got %d", hub.CountConnections())
}

func TestServerConfigDefaults(t *testing.T) {
	config := DefaultServerConfig()

	if config.ReadBufferSize <= 0 {
		t.Error("ReadBufferSize should have default value")
	}
	if config.WriteBufferSize <= 0 {
		t.Error("WriteBufferSize should have default value")
	}
	if config.HandshakeTimeout <= 0 {
		t.Error("HandshakeTimeout should have default value")
	}
	if config.CheckOrigin == nil {
		t.Error("CheckOrigin should have default function")
	}
	if !config.EnableCompression {
		t.Error("Compression should be enabled by default")
	}
	for _, origin := range []string{"", "file://", "null", "http://localhost:5173"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if !config.CheckOrigin(req) {
			t.Errorf("expected default CheckOrigin to allow %q", origin)
		}
	}
}

func TestServerCustomConfig(t *testing.T) {
	customConfig := &ServerConfig{
		ReadBufferSize:    2048,
		WriteBufferSize:   2048,
		HandshakeTimeout:  5 * time.Second,
		EnableCompression: false,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == "http://localhost"
		},
	}

	hub := newTestHub()
	server := NewServer(hub, customConfig)

	// Verify custom config is used
	if server.upgrader.ReadBufferSize != 2048 {
		t.Errorf("Expected ReadBufferSize 2048, got %d", server.upgrader.ReadBufferSize)
	}
	if server.upgrader.WriteBufferSize != 2048 {
		t.Errorf("Expected WriteBufferSize 2048, got %d", server.upgrader.WriteBufferSize)
	}
	if server.upgrader.HandshakeTimeout != 5*time.Second {
		t.Errorf("Expected HandshakeTimeout 5s, got %v", server.upgrader.HandshakeTimeout)
	}
	if server.upgrader.EnableCompression {
		t.Error("Compression should be disabled")
	}
}

func TestServerOriginCheck(t *testing.T) {
	customConfig := &ServerConfig{
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == "http://allowed.com"
		},
	}

	hub := newTestHub()
	server := NewServer(hub, customConfig)

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")

	// Test with disallowed origin
	dialer := &websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	headers := http.Header{}
	headers.Set("Origin", "http://malicious.com")

	conn, resp, err := dialer.Dial(wsURL, headers)
	if err == nil {
		_ = conn.Close()
		t.Error("Should have rejected connection with bad origin")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 status for bad origin, got %d", resp.StatusCode)
	}

	// Test with allowed origin
	headers.Set("Origin", "http://allowed.com")
	conn, resp, err = dialer.Dial(wsURL, headers)
	if err != nil {
		t.Errorf("Should have allowed connection with good origin: %v", err)
	}
	if conn != nil {
		_ = conn.Close()
	}
	if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Expected 101 status for good origin, got %d", resp.StatusCode)
	}
}

func TestServerRegistrationFailure(t *testing.T) {
	// Create hub with max connections = 0 to force registration failure
	config := &core.HubConfig{
		MaxConnections:    0, // This will cause registration failures
		MaxMessageSize:    1024,
		HeartbeatInterval: 30 * time.Second,
		SendChannelSize:   10,
		EnableCompression: true,
	}
	router := &mockRouter{handlers: make(map[core.MessageType]core.MessageHandler)}
	subManager := &mockSubscriptionManager{}
	hub := core.NewHub(config, router, subManager)

	server := NewServer(hub, DefaultServerConfig())
	testServer := httptest.NewServer(server)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	// The WebSocket upgrade succeeds but the connection should be closed immediately
	if err != nil {
		// This is expected - connection closed after upgrade
		if resp == nil || resp.StatusCode != http.StatusSwitchingProtocols {
			t.Error("Expected WebSocket upgrade to succeed before connection closure")
		}
	} else {
		// If no error, the connection should still be closed soon
		defer func() { _ = conn.Close() }()

		// Try to read - should get an error as connection was closed server-side
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, _, readErr := conn.ReadMessage()
		if readErr == nil {
			t.Error("Expected read error due to server-side connection closure")
		}
	}
}

func TestServerHTTPFallback(t *testing.T) {
	hub := newTestHub()
	server := NewServer(hub, DefaultServerConfig())

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	// Test regular HTTP request (should fail upgrade)
	resp, err := http.Get(testServer.URL)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 for non-WebSocket request, got %d", resp.StatusCode)
	}
}

func TestServerConnectionConfiguration(t *testing.T) {
	hubConfig := &core.HubConfig{
		MaxMessageSize:    512,
		HeartbeatInterval: 15 * time.Second,
		SendChannelSize:   128,
		EnableCompression: false,
	}

	// We need a more complete hub for this test
	router := &mockRouter{handlers: make(map[core.MessageType]core.MessageHandler)}
	subManager := &mockSubscriptionManager{}
	hub := core.NewHub(hubConfig, router, subManager)

	server := NewServer(hub, DefaultServerConfig())

	// Verify connection config is derived from hub config
	if server.connConfig.MaxMessageSize != hubConfig.MaxMessageSize {
		t.Errorf("Expected MaxMessageSize %d, got %d",
			hubConfig.MaxMessageSize, server.connConfig.MaxMessageSize)
	}
	if server.connConfig.PingInterval != hubConfig.HeartbeatInterval {
		t.Errorf("Expected PingInterval %v, got %v",
			hubConfig.HeartbeatInterval, server.connConfig.PingInterval)
	}
	if server.connConfig.SendChannelSize != hubConfig.SendChannelSize {
		t.Errorf("Expected SendChannelSize %d, got %d",
			hubConfig.SendChannelSize, server.connConfig.SendChannelSize)
	}
	if server.connConfig.EnableCompression != hubConfig.EnableCompression {
		t.Errorf("Expected EnableCompression %v, got %v",
			hubConfig.EnableCompression, server.connConfig.EnableCompression)
	}
}

type mockRouter struct {
	handlers map[core.MessageType]core.MessageHandler
}

func (r *mockRouter) Register(msgType core.MessageType, handler core.MessageHandler) error {
	r.handlers[msgType] = handler
	return nil
}

func (r *mockRouter) Unregister(msgType core.MessageType) error {
	delete(r.handlers, msgType)
	return nil
}

func (r *mockRouter) Route(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	return nil
}

type mockSubscriptionManager struct{}

func (sm *mockSubscriptionManager) Subscribe(topic string, conn *core.Connection) (bool, error) {
	return true, nil
}

func (sm *mockSubscriptionManager) Unsubscribe(topic string, conn *core.Connection) (bool, error) {
	return true, nil
}

func (sm *mockSubscriptionManager) UnsubscribeAll(conn *core.Connection) error {
	return nil
}

func (sm *mockSubscriptionManager) Broadcast(topic string, msg core.Message) error {
	return nil
}

func (sm *mockSubscriptionManager) GetSubscribers(topic string) []*core.Connection {
	return nil
}

func (sm *mockSubscriptionManager) GetTopics(conn *core.Connection) []string {
	return nil
}

func BenchmarkServerWebSocketUpgrade(b *testing.B) {
	hub := newTestHub()
	server := NewServer(hub, DefaultServerConfig())

	testServer := httptest.NewServer(server)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				b.Error(err)
				continue
			}
			_ = conn.Close()
		}
	})
}
