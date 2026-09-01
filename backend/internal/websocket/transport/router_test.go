package transport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

func TestRouterBasicRouting(t *testing.T) {
	router := NewRouter()

	handlerCalled := false
	testHandler := core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		handlerCalled = true
		return nil
	})

	// Register handler
	if err := router.Register("test", testHandler); err != nil {
		t.Fatalf("Failed to register handler: %v", err)
	}

	// Create test message
	payload := json.RawMessage(`{"data": "test"}`)
	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "test",
			Timestamp:   time.Now(),
		},
		Payload: payload,
	}

	// Route message
	err := router.Route(context.Background(), nil, msg)
	if err != nil {
		t.Errorf("Routing should succeed: %v", err)
	}

	if !handlerCalled {
		t.Error("Handler should have been called")
	}
}

func TestRouterHandlerNotFound(t *testing.T) {
	router := NewRouter()

	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "nonexistent",
			Timestamp:   time.Now(),
		},
		Payload: json.RawMessage(`{}`),
	}

	err := router.Route(context.Background(), nil, msg)
	if err != core.ErrHandlerNotFound {
		t.Errorf("Expected ErrHandlerNotFound, got %v", err)
	}
}

func TestRouterMultipleHandlers(t *testing.T) {
	router := NewRouter()

	var handler1Called, handler2Called bool

	_ = router.Register("type1", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		handler1Called = true
		return nil
	}))

	_ = router.Register("type2", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		handler2Called = true
		return nil
	}))

	// Test first handler
	msg1 := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "type1", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}
	_ = router.Route(context.Background(), nil, msg1)

	// Test second handler
	msg2 := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "type2", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}
	_ = router.Route(context.Background(), nil, msg2)

	if !handler1Called {
		t.Error("Handler 1 should have been called")
	}
	if !handler2Called {
		t.Error("Handler 2 should have been called")
	}
}

func TestRouterHandlerError(t *testing.T) {
	router := NewRouter()

	expectedErr := core.ErrInvalidMessage
	_ = router.Register("error", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		return expectedErr
	}))

	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "error", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}

	err := router.Route(context.Background(), nil, msg)
	if err != expectedErr {
		t.Errorf("Expected %v, got %v", expectedErr, err)
	}
}

func TestRouterOverwriteHandler(t *testing.T) {
	router := NewRouter()

	var firstCalled, secondCalled bool

	// Register first handler
	_ = router.Register("test", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		firstCalled = true
		return nil
	}))

	// Overwrite with second handler
	_ = router.Register("test", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		secondCalled = true
		return nil
	}))

	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "test", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}

	_ = router.Route(context.Background(), nil, msg)

	if firstCalled {
		t.Error("First handler should not have been called after overwrite")
	}
	if !secondCalled {
		t.Error("Second handler should have been called")
	}
}

func TestRouterContextCancellation(t *testing.T) {
	router := NewRouter()

	handlerCalled := false
	_ = router.Register("test", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			handlerCalled = true
			return nil
		}
	}))

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "test", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}

	err := router.Route(ctx, nil, msg)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	if handlerCalled {
		t.Error("Handler should not have been called with cancelled context")
	}
}

func TestRouterConcurrentAccess(t *testing.T) {
	router := NewRouter()

	// Register multiple handlers concurrently
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			msgType := core.MessageType("type" + string(rune('0'+id)))
			_ = router.Register(msgType, core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
				return nil
			}))
			done <- true
		}(i)
	}

	// Wait for all registrations
	for i := 0; i < 10; i++ {
		<-done
	}

	// Test concurrent routing
	for i := 0; i < 10; i++ {
		go func(id int) {
			msgType := core.MessageType("type" + string(rune('0'+id)))
			msg := &core.IncomingMessage{
				BaseMessage: core.BaseMessage{MessageType: msgType, Timestamp: time.Now()},
				Payload:     json.RawMessage(`{}`),
			}
			_ = router.Route(context.Background(), nil, msg)
			done <- true
		}(i)
	}

	// Wait for all routes
	for i := 0; i < 10; i++ {
		<-done
	}
}

type customHandler struct {
	called bool
}

func (h *customHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	h.called = true
	return nil
}

func (h *customHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"custom"}
}

func TestRouterCustomHandler(t *testing.T) {
	router := NewRouter()

	handler := &customHandler{}
	_ = router.Register("custom", handler)

	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "custom", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}

	err := router.Route(context.Background(), nil, msg)
	if err != nil {
		t.Errorf("Routing should succeed: %v", err)
	}

	if !handler.called {
		t.Error("Custom handler should have been called")
	}
}

func BenchmarkRouterRoute(b *testing.B) {
	router := NewRouter()

	// Register 100 handlers
	for i := 0; i < 100; i++ {
		msgType := core.MessageType("type" + string(rune(i)))
		_ = router.Register(msgType, core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
			return nil
		}))
	}

	// Benchmark routing to a middle handler
	msg := &core.IncomingMessage{
		BaseMessage: core.BaseMessage{MessageType: "type50", Timestamp: time.Now()},
		Payload:     json.RawMessage(`{}`),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = router.Route(context.Background(), nil, msg)
		}
	})
}

func BenchmarkRouterRegister(b *testing.B) {
	router := NewRouter()

	handler := core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		return nil
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			msgType := core.MessageType("bench" + string(rune(i)))
			_ = router.Register(msgType, handler)
			i++
		}
	})
}
