package websocket_test

import (
	"io"
	"net"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/terminal/domain"
	"github.com/kanivet/backend/internal/terminal/service"
	terminalws "github.com/kanivet/backend/internal/terminal/websocket"
	ws "github.com/kanivet/backend/internal/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
	"github.com/kanivet/backend/internal/websocket/transport"
)

// gatedTerminal blocks Write until released, like a PTY whose child does not
// read its stdin.
type gatedTerminal struct {
	scriptedTerminal
	release chan struct{}
	mu      sync.Mutex
	written []byte
}

func (g *gatedTerminal) Write(p []byte) (int, error) {
	select {
	case <-g.release:
	case <-g.closed:
		return 0, io.ErrClosedPipe
	}
	g.mu.Lock()
	g.written = append(g.written, p...)
	g.mu.Unlock()
	return len(p), nil
}

func (g *gatedTerminal) Close() error {
	g.once.Do(func() { close(g.closed) })
	return nil
}

type terminalEnv struct {
	conn    *websocket.Conn
	handler *terminalws.TerminalHandler
	session string
}

// startSmallBufferTerminal is startTerminal with a tiny send channel and a
// client socket with a tiny receive buffer, so a client that does not read
// makes the send channel fill up quickly.
func startSmallBufferTerminal(t *testing.T, term domain.Terminal) *terminalEnv {
	t.Helper()
	cfg := core.DefaultHubConfig()
	cfg.SendChannelSize = 4
	hub := core.NewHub(cfg, transport.NewRouter(), transport.NewSubscriptionManager())
	h := terminalws.NewTerminalHandler(service.NewTerminalService(termFactory{term}))
	_ = hub.Router().Register("terminal", h)
	ts := httptest.NewServer(transport.NewServer(hub, transport.DefaultServerConfig()))
	t.Cleanup(ts.Close)
	t.Cleanup(h.Cleanup)
	dialer := websocket.Dialer{NetDial: func(network, addr string) (net.Conn, error) {
		d := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				setRecvBuf(fd, 2048)
			})
		}}
		return d.Dial(network, addr)
	}}
	return connectTerminal(t, dialer, ts.URL, h)
}

func startTerminal(t *testing.T, term domain.Terminal) *terminalEnv {
	t.Helper()
	server := ws.NewServer()
	h := terminalws.NewTerminalHandler(service.NewTerminalService(termFactory{term}))
	server.RegisterHandler("terminal", h)
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	t.Cleanup(h.Cleanup)
	return connectTerminal(t, *websocket.DefaultDialer, ts.URL, h)
}

func connectTerminal(t *testing.T, dialer websocket.Dialer, url string, h *terminalws.TerminalHandler) *terminalEnv {
	t.Helper()
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{"action": "create"}}); err != nil {
		t.Fatal(err)
	}
	env := &terminalEnv{conn: conn, handler: h}
	env.session = env.waitFor(t, 5*time.Second, func(p map[string]interface{}) (string, bool) {
		if p["type"] == "created" {
			s, _ := p["sessionId"].(string)
			return s, true
		}
		return "", false
	})
	return env
}

type termFactory struct{ term domain.Terminal }

func (f termFactory) CreateTerminal(string, domain.TerminalSize) (domain.Terminal, error) {
	return f.term, nil
}

func (e *terminalEnv) waitFor(t *testing.T, timeout time.Duration, match func(map[string]interface{}) (string, bool)) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		e.conn.SetReadDeadline(deadline)
		var msg map[string]interface{}
		if err := e.conn.ReadJSON(&msg); err != nil {
			t.Fatalf("waiting for message: %v", err)
		}
		payload, _ := msg["payload"].(map[string]interface{})
		if payload == nil {
			payload, _ = msg["data"].(map[string]interface{})
		}
		if payload == nil {
			continue
		}
		if v, ok := match(payload); ok {
			return v
		}
	}
}

// A client that is slow to read fills the connection's send buffer. That is
// backpressure, not a reason to kill the shell and lose the output.
func TestTerminalOutputSurvivesFullSendBuffer(t *testing.T) {
	const chunks = 150
	const chunkSize = 60 * 1024
	term := &scriptedTerminal{reads: make(chan []byte, chunks), closed: make(chan struct{})}
	chunk := []byte(strings.Repeat("a", chunkSize))
	for i := 0; i < chunks; i++ {
		term.reads <- chunk
	}
	env := startSmallBufferTerminal(t, term)

	// Let the buffers fill while nobody reads.
	time.Sleep(1500 * time.Millisecond)

	total := 0
	want := chunks * chunkSize
	for total < want {
		env.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var msg map[string]interface{}
		if err := env.conn.ReadJSON(&msg); err != nil {
			t.Fatalf("output was lost after %d of %d bytes: %v", total, want, err)
		}
		payload, _ := msg["payload"].(map[string]interface{})
		if payload == nil {
			payload, _ = msg["data"].(map[string]interface{})
		}
		if payload == nil {
			continue
		}
		if payload["type"] == "error" {
			t.Fatalf("terminal was torn down: %v", payload["error"])
		}
		if payload["type"] == "output" {
			total += len(payload["data"].(string))
		}
	}
}

// A shell that does not read its stdin must not stall the connection's read
// loop, which also serves every other subscription on the shared socket.
func TestTerminalInputDoesNotStallConnection(t *testing.T) {
	term := &gatedTerminal{
		scriptedTerminal: scriptedTerminal{reads: make(chan []byte, 1), closed: make(chan struct{})},
		release:          make(chan struct{}),
	}
	env := startTerminal(t, term)

	if err := env.conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{
		"action": "input", "sessionId": env.session, "data": "ls\r"}}); err != nil {
		t.Fatal(err)
	}
	// Any later message must still be answered while the write is blocked.
	if err := env.conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{
		"action": "resize", "sessionId": "nope"}}); err != nil {
		t.Fatal(err)
	}
	env.waitFor(t, 2*time.Second, func(p map[string]interface{}) (string, bool) {
		return "", p["type"] == "error"
	})

	// Order is preserved once the child reads again.
	if err := env.conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{
		"action": "input", "sessionId": env.session, "data": "pwd\r"}}); err != nil {
		t.Fatal(err)
	}
	close(term.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		term.mu.Lock()
		got := string(term.written)
		term.mu.Unlock()
		if got == "ls\rpwd\r" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	t.Fatalf("input arrived as %q", term.written)
}

// Closed sessions must not leave a goroutine each behind until the
// connection closes.
func TestTerminalClosedSessionsDoNotLeakGoroutines(t *testing.T) {
	server := ws.NewServer()
	h := terminalws.NewTerminalHandler(service.NewTerminalService(newTermFactory()))
	server.RegisterHandler("terminal", h)
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	env := &terminalEnv{conn: conn, handler: h}

	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 8; i++ {
		time.Sleep(250 * time.Millisecond) // the hub rate limits bursts
		if err := conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{"action": "create"}}); err != nil {
			t.Fatal(err)
		}
		id := env.waitFor(t, 5*time.Second, func(p map[string]interface{}) (string, bool) {
			s, _ := p["sessionId"].(string)
			return s, p["type"] == "created"
		})
		if err := conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{"action": "close", "sessionId": id}}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+4 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutines grew from %d to %d after closing 8 sessions", before, runtime.NumGoroutine())
}

type multiTermFactory struct{}

func newTermFactory() multiTermFactory { return multiTermFactory{} }

func (multiTermFactory) CreateTerminal(string, domain.TerminalSize) (domain.Terminal, error) {
	return &scriptedTerminal{reads: make(chan []byte), closed: make(chan struct{})}, nil
}
