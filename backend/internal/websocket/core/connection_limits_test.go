package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// stalledPair returns a server-side websocket conn whose peer never reads, so
// writes to it eventually block once the socket buffers fill.
func stalledPair(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()
	serverConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn <- c
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	c := <-serverConn
	return c, func() {
		_ = client.Close()
		_ = c.Close()
		srv.Close()
	}
}

// A stalled client used to pin SendChannelSize x MaxMessageSize of memory;
// the byte cap now refuses frames with the same error as a full queue.
func TestSendQueueIsBoundedByBytes(t *testing.T) {
	raw, cleanup := stalledPair(t)
	defer cleanup()
	conn := NewConnection("bytes", raw, &ConnectionConfig{
		MaxMessageSize:  2 << 20,
		SendChannelSize: 1000,
		MaxQueuedBytes:  4 << 20,
	})
	defer conn.Close()

	frame := make([]byte, 1<<20)
	var refused error
	for i := 0; i < 200; i++ {
		if err := conn.Send(frame); err != nil {
			refused = err
			break
		}
	}
	if !errors.Is(refused, ErrRateLimitExceeded) {
		t.Fatalf("Send never hit the byte cap, last error %v", refused)
	}
	if q := conn.QueuedBytes(); q > 4<<20 {
		t.Fatalf("queued %d bytes, cap is %d", q, 4<<20)
	}
}

// Normal traffic: the accounting returns to zero once frames are written.
func TestQueuedBytesDrain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := NewConnection("drain", client, DefaultConnectionConfig())
	defer conn.Close()
	for i := 0; i < 50; i++ {
		if err := conn.Send([]byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for conn.QueuedBytes() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if q := conn.QueuedBytes(); q != 0 {
		t.Fatalf("queued bytes = %d after drain", q)
	}
}

// Close must not wait out the 10s write timeout when the writer is stuck on a
// client that stopped reading.
func TestCloseBreaksStuckWrite(t *testing.T) {
	raw, cleanup := stalledPair(t)
	defer cleanup()
	conn := NewConnection("stuck", raw, &ConnectionConfig{
		MaxMessageSize:  16 << 20,
		SendChannelSize: 64,
		WriteTimeout:    10 * time.Second,
	})

	frame := make([]byte, 8<<20)
	for i := 0; i < 8; i++ {
		_ = conn.Send(frame)
	}
	time.Sleep(300 * time.Millisecond) // let the pump block inside WriteMessage

	start := time.Now()
	_ = conn.Close()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Close took %v with a stuck writer", d)
	}
}
