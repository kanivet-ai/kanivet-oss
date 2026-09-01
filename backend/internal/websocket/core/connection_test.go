package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectionLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("Failed to close connection in handler: %v", err)
			}
		}()

		// Keep connection alive for testing
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultConnectionConfig()
	wsConn := NewConnection("test-1", conn, config)

	// Test initial state
	if wsConn.State() != StateConnected {
		t.Errorf("Expected StateConnected, got %v", wsConn.State())
	}

	if wsConn.ID() != "test-1" {
		t.Errorf("Expected ID 'test-1', got %s", wsConn.ID())
	}

	// Test metadata
	wsConn.SetMetadata("key", "value")
	val, ok := wsConn.GetMetadata("key")
	if !ok || val != "value" {
		t.Errorf("Expected metadata value 'value', got %v", val)
	}

	// Test activity tracking
	before := wsConn.LastActivity()
	time.Sleep(10 * time.Millisecond)
	wsConn.UpdateActivity()
	after := wsConn.LastActivity()

	if !after.After(before) {
		t.Error("Activity timestamp should update")
	}

	// Test close
	if err := wsConn.Close(); err != nil {
		t.Error(err)
	}

	if wsConn.State() != StateClosed {
		t.Errorf("Expected StateClosed after close, got %v", wsConn.State())
	}

	// Test send after close
	err = wsConn.Send([]byte("test"))
	if err != ErrConnectionClosed {
		t.Errorf("Expected ErrConnectionClosed, got %v", err)
	}
}

func TestConnectionConfigValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Test with invalid config
	invalidConfig := &ConnectionConfig{
		MaxMessageSize:  -1,
		WriteTimeout:    -1,
		PingInterval:    -1,
		PongWait:        -1,
		SendChannelSize: -1,
	}

	wsConn := NewConnection("test", conn, invalidConfig)

	// Should use defaults for invalid values
	if wsConn.config.MaxMessageSize <= 0 {
		t.Error("Should use default for invalid MaxMessageSize")
	}
	if wsConn.config.WriteTimeout <= 0 {
		t.Error("Should use default for invalid WriteTimeout")
	}
}

func TestConnectionSendBackpressure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("Failed to close connection in handler: %v", err)
			}
		}()

		// Don't read messages to fill up the channel
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	config := &ConnectionConfig{
		MaxMessageSize:  1024,
		WriteTimeout:    time.Second,
		SendChannelSize: 2, // Very small buffer
		PingInterval:    time.Minute,
		PongWait:        2 * time.Minute,
	}

	wsConn := NewConnection("test", conn, config)

	// Fill the channel
	if err := wsConn.Send([]byte("msg1")); err != nil {
		t.Logf("Failed to send msg1: %v", err)
	}
	if err := wsConn.Send([]byte("msg2")); err != nil {
		t.Logf("Failed to send msg2: %v", err)
	}

	// This should trigger backpressure
	err = wsConn.Send([]byte("msg3"))
	if err != ErrRateLimitExceeded {
		t.Errorf("Expected ErrRateLimitExceeded, got %v", err)
	}

	if err := wsConn.Close(); err != nil {
		t.Logf("Failed to close connection: %v", err)
	}
}

func TestConnectionMessageSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	config := &ConnectionConfig{
		MaxMessageSize:  100,
		WriteTimeout:    time.Second,
		SendChannelSize: 10,
		PingInterval:    time.Minute,
		PongWait:        2 * time.Minute,
	}

	wsConn := NewConnection("test", conn, config)

	// Test message too large
	largeMsg := make([]byte, 200)
	err = wsConn.Send(largeMsg)
	if err != ErrInvalidMessage {
		t.Errorf("Expected ErrInvalidMessage for large message, got %v", err)
	}

	err = wsConn.SendBinary(largeMsg)
	if err != ErrInvalidMessage {
		t.Errorf("Expected ErrInvalidMessage for large binary message, got %v", err)
	}

	if err := wsConn.Close(); err != nil {
		t.Logf("Failed to close connection: %v", err)
	}
}

func TestConnectionConcurrency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("Failed to close connection in handler: %v", err)
			}
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	wsConn := NewConnection("test", conn, DefaultConnectionConfig())

	// Test concurrent sends
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := wsConn.Send([]byte("message " + string(rune(i))))
			if err != nil && err != ErrConnectionClosed && err != ErrRateLimitExceeded {
				errors <- err
			}
		}(i)
	}

	// Close connection while sends are happening
	time.Sleep(10 * time.Millisecond)
	if err := wsConn.Close(); err != nil {
		t.Logf("Failed to close connection: %v", err)
	}

	wg.Wait()
	close(errors)

	// Should not have any unexpected errors
	for err := range errors {
		t.Errorf("Unexpected error: %v", err)
	}
}

func BenchmarkConnectionSend(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				b.Logf("Failed to close connection in handler: %v", err)
			}
		}()

		// Read and discard messages
		go func() {
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					break
				}
			}
		}()

		time.Sleep(time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		b.Fatal(err)
	}

	wsConn := NewConnection("bench", conn, DefaultConnectionConfig())
	message := []byte("benchmark message")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := wsConn.Send(message); err != nil {
				b.Logf("Failed to send message: %v", err)
			}
		}
	})

	if err := wsConn.Close(); err != nil {
		b.Logf("Failed to close connection: %v", err)
	}
}
