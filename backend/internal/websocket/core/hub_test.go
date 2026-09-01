package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type mockRouter struct {
	handlers map[MessageType]MessageHandler
}

func newMockRouter() *mockRouter {
	return &mockRouter{
		handlers: make(map[MessageType]MessageHandler),
	}
}

func (r *mockRouter) Register(msgType MessageType, handler MessageHandler) error {
	r.handlers[msgType] = handler
	return nil
}

func (r *mockRouter) Unregister(msgType MessageType) error {
	delete(r.handlers, msgType)
	return nil
}

func (r *mockRouter) Route(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
	if handler, ok := r.handlers[msg.Type()]; ok {
		return handler.HandleMessage(ctx, conn, msg)
	}
	return ErrHandlerNotFound
}

type mockSubscriptionManager struct {
	subscriptions sync.Map
}

func newMockSubscriptionManager() *mockSubscriptionManager {
	return &mockSubscriptionManager{}
}

func (sm *mockSubscriptionManager) Subscribe(topic string, conn *Connection) (bool, error) {
	key := topic + ":" + string(conn.ID())
	_, existed := sm.subscriptions.Load(key)
	sm.subscriptions.Store(key, conn)
	return !existed, nil
}

func (sm *mockSubscriptionManager) Unsubscribe(topic string, conn *Connection) (bool, error) {
	key := topic + ":" + string(conn.ID())
	_, existed := sm.subscriptions.Load(key)
	sm.subscriptions.Delete(key)
	return existed, nil
}

func (sm *mockSubscriptionManager) UnsubscribeAll(conn *Connection) error {
	toDelete := []string{}
	sm.subscriptions.Range(func(key, value interface{}) bool {
		if strings.HasSuffix(key.(string), ":"+string(conn.ID())) {
			toDelete = append(toDelete, key.(string))
		}
		return true
	})

	for _, key := range toDelete {
		sm.subscriptions.Delete(key)
	}
	return nil
}

func (sm *mockSubscriptionManager) Broadcast(topic string, msg Message) error {
	sm.subscriptions.Range(func(key, value interface{}) bool {
		if strings.HasPrefix(key.(string), topic+":") {
			conn := value.(*Connection)
			if data, err := msg.Marshal(); err == nil {
				_ = conn.Send(data)
			}
		}
		return true
	})
	return nil
}

func (sm *mockSubscriptionManager) GetSubscribers(topic string) []*Connection {
	var subscribers []*Connection
	sm.subscriptions.Range(func(key, value interface{}) bool {
		if strings.HasPrefix(key.(string), topic+":") {
			subscribers = append(subscribers, value.(*Connection))
		}
		return true
	})
	return subscribers
}

func (sm *mockSubscriptionManager) GetTopics(conn *Connection) []string {
	var topics []string
	connSuffix := ":" + string(conn.ID())
	sm.subscriptions.Range(func(key, value interface{}) bool {
		if strings.HasSuffix(key.(string), connSuffix) {
			keyStr := key.(string)
			topic := keyStr[:len(keyStr)-len(connSuffix)]
			topics = append(topics, topic)
		}
		return true
	})
	return topics
}

type mockMetrics struct {
	connectionsAdded   int64
	connectionsRemoved int64
	messagesReceived   int64
	messagesSent       int64
	messageErrors      int64
}

func (m *mockMetrics) ConnectionAdded()                         { atomic.AddInt64(&m.connectionsAdded, 1) }
func (m *mockMetrics) ConnectionRemoved()                       { atomic.AddInt64(&m.connectionsRemoved, 1) }
func (m *mockMetrics) MessageReceived(msgType MessageType)      { atomic.AddInt64(&m.messagesReceived, 1) }
func (m *mockMetrics) MessageSent(msgType MessageType)          { atomic.AddInt64(&m.messagesSent, 1) }
func (m *mockMetrics) MessageError(operation string, err error) { atomic.AddInt64(&m.messageErrors, 1) }
func (m *mockMetrics) SubscriptionAdded(topic string)           {}
func (m *mockMetrics) SubscriptionRemoved(topic string)         {}

func TestHubConnectionManagement(t *testing.T) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()
	metrics := &mockMetrics{}

	config := &HubConfig{
		MaxConnections:    5,
		HeartbeatInterval: time.Minute,
		ConnectionTimeout: time.Minute,
		MaxMessageSize:    1024,
		SendChannelSize:   10,
	}

	hub := NewHub(config, router, subManager)
	hub.metrics = metrics

	// Create test connections
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer func() { _ = conn.Close() }()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	connections := make([]*Connection, 0)

	// Test registering connections
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatal(err)
		}

		wsConn := NewConnection(ConnectionID("test-"+fmt.Sprintf("%d", i)), conn, DefaultConnectionConfig())
		err = hub.RegisterConnection(wsConn)
		if err != nil {
			t.Errorf("Failed to register connection: %v", err)
		}

		connections = append(connections, wsConn)
	}

	// Test connection count
	if hub.CountConnections() != 3 {
		t.Errorf("Expected 3 connections, got %d", hub.CountConnections())
	}

	// Test metrics
	if atomic.LoadInt64(&metrics.connectionsAdded) != 3 {
		t.Errorf("Expected 3 connections added in metrics, got %d", metrics.connectionsAdded)
	}

	// Test getting connection
	conn, ok := hub.GetConnection("test-0")
	if !ok {
		t.Error("Should find connection by ID")
	}
	if conn != nil && conn.ID() != "test-0" {
		t.Errorf("Expected connection ID 'test-0', got %s", conn.ID())
	}

	// Test max connections limit
	hitLimit := false
	overflowConnections := 0
	for i := 0; i < 10; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			continue
		}
		wsConn := NewConnection(ConnectionID("overflow-"+fmt.Sprintf("%d", i)), conn, DefaultConnectionConfig())
		err = hub.RegisterConnection(wsConn)
		if err == ErrRateLimitExceeded {
			hitLimit = true
			break // Expected
		} else if err == nil {
			overflowConnections++
			connections = append(connections, wsConn)
		}
	}
	if !hitLimit {
		t.Error("Should have hit connection limit")
	}

	// We started with 3, max is 5, so 2 overflow connections should have succeeded
	expectedCount := 5
	if hub.CountConnections() != expectedCount {
		t.Errorf("Expected %d connections at max, got %d", expectedCount, hub.CountConnections())
	}

	// Test unregistering connections
	hub.UnregisterConnection("test-0")
	if hub.CountConnections() != 4 {
		t.Errorf("Expected 4 connections after unregister, got %d", hub.CountConnections())
	}

	// Clean up
	for _, c := range connections[1:] {
		_ = c.Close()
	}

	// Test shutdown
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := hub.Shutdown(ctx); err != nil {
		t.Errorf("Hub shutdown failed: %v", err)
	}
}

func TestHubConfigValidation(t *testing.T) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()

	// Test with nil config
	hub := NewHub(nil, router, subManager)
	if hub.Config.MaxConnections <= 0 {
		t.Error("Should use default config when nil provided")
	}

	// Test with invalid config values
	invalidConfig := &HubConfig{
		MaxConnections:    -1,
		HeartbeatInterval: -1,
		ConnectionTimeout: -1,
		MaxMessageSize:    -1,
		SendChannelSize:   -1,
	}

	hub = NewHub(invalidConfig, router, subManager)

	// Should use defaults for invalid values
	if hub.Config.MaxConnections <= 0 {
		t.Error("Should use default for invalid MaxConnections")
	}
	if hub.Config.HeartbeatInterval <= 0 {
		t.Error("Should use default for invalid HeartbeatInterval")
	}
}

func TestHubMessageRouting(t *testing.T) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()

	hub := NewHub(DefaultHubConfig(), router, subManager)

	// Register a test handler with atomic access
	var handlerCalled int32
	_ = router.Register("test", MessageHandlerFunc(func(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
		atomic.StoreInt32(&handlerCalled, 1)
		return nil
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer func() { _ = conn.Close() }()

		// Send a test message
		testMsg := map[string]interface{}{
			"type":    "test",
			"payload": "test data",
		}
		_ = conn.WriteJSON(testMsg)

		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	wsConn := NewConnection("test", conn, DefaultConnectionConfig())
	_ = hub.RegisterConnection(wsConn)

	// Wait for message to be processed
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&handlerCalled) == 0 {
		t.Error("Message handler should have been called")
	}

	hub.UnregisterConnection("test")
}

func TestHubBroadcast(t *testing.T) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()

	hub := NewHub(DefaultHubConfig(), router, subManager)

	// Create test message
	msg := NewOutgoingMessage("broadcast", "test data")

	// Test broadcast (should not error even with no connections)
	err := hub.Broadcast("test-topic", msg)
	if err != nil {
		t.Errorf("Broadcast should not error: %v", err)
	}
}

func TestHubMiddleware(t *testing.T) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()

	hub := NewHub(DefaultHubConfig(), router, subManager)

	var middlewareExecuted int32

	// Add test middleware
	hub.AddMiddleware(MiddlewareFunc(func(next MessageHandlerFunc) MessageHandlerFunc {
		return func(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
			atomic.StoreInt32(&middlewareExecuted, 1)
			return next(ctx, conn, msg)
		}
	}))

	// Register handler
	_ = router.Register("test", MessageHandlerFunc(func(ctx context.Context, conn *Connection, msg *IncomingMessage) error {
		return nil
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer func() { _ = conn.Close() }()

		testMsg := map[string]interface{}{
			"type": "test",
		}
		_ = conn.WriteJSON(testMsg)
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	wsConn := NewConnection("test", conn, DefaultConnectionConfig())
	_ = hub.RegisterConnection(wsConn)

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&middlewareExecuted) == 0 {
		t.Error("Middleware should have been executed")
	}

	hub.UnregisterConnection("test")
}

func BenchmarkHubConnectionRegistration(b *testing.B) {
	router := newMockRouter()
	subManager := newMockSubscriptionManager()

	hub := NewHub(&HubConfig{
		MaxConnections:    100000,
		HeartbeatInterval: time.Minute,
		ConnectionTimeout: time.Minute,
		MaxMessageSize:    1024,
		SendChannelSize:   10,
	}, router, subManager)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		_ = conn.Close()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				continue
			}

			wsConn := NewConnection(ConnectionID("bench-"+fmt.Sprintf("%d", i)), conn, DefaultConnectionConfig())
			_ = hub.RegisterConnection(wsConn)
			hub.UnregisterConnection(wsConn.ID())
			i++
		}
	})
}
