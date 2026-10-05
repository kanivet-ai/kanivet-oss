package websocket_test

import (
	"encoding/base64"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/terminal/domain"
	"github.com/kanivet/backend/internal/terminal/service"
	terminalws "github.com/kanivet/backend/internal/terminal/websocket"
	ws "github.com/kanivet/backend/internal/websocket"
)

// scriptedTerminal returns the given reads in order, then blocks until closed.
type scriptedTerminal struct {
	reads  chan []byte
	closed chan struct{}
	once   sync.Once
}

func (s *scriptedTerminal) Start() error                     { return nil }
func (s *scriptedTerminal) Resize(domain.TerminalSize) error { return nil }
func (s *scriptedTerminal) Write(p []byte) (int, error)      { return len(p), nil }
func (s *scriptedTerminal) Wait() error                      { <-s.closed; return nil }
func (s *scriptedTerminal) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
func (s *scriptedTerminal) Read() ([]byte, error) {
	select {
	case b := <-s.reads:
		return b, nil
	case <-s.closed:
		return nil, io.EOF
	}
}

type scriptedFactory struct{ term *scriptedTerminal }

func (f scriptedFactory) CreateTerminal(string, domain.TerminalSize) (domain.Terminal, error) {
	return f.term, nil
}

func outputOf(t *testing.T, reads [][]byte) []map[string]interface{} {
	t.Helper()
	term := &scriptedTerminal{reads: make(chan []byte, len(reads)), closed: make(chan struct{})}
	for _, r := range reads {
		term.reads <- r
	}
	server := ws.NewServer()
	h := terminalws.NewTerminalHandler(service.NewTerminalService(scriptedFactory{term}))
	server.RegisterHandler("terminal", h)
	ts := httptest.NewServer(server)
	defer ts.Close()
	defer h.Cleanup()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]interface{}{"type": "terminal", "payload": map[string]interface{}{"action": "create"}}); err != nil {
		t.Fatal(err)
	}

	total := 0
	for _, r := range reads {
		total += len(r)
	}
	var outputs []map[string]interface{}
	got := 0
	for got < total {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v (received %d of %d bytes)", err, got, total)
		}
		payload, _ := msg["payload"].(map[string]interface{})
		if payload == nil {
			payload, _ = msg["data"].(map[string]interface{})
		}
		if payload == nil || payload["type"] != "output" {
			continue
		}
		outputs = append(outputs, payload)
		data := payload["data"].(string)
		if payload["encoding"] == "base64" {
			b, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				t.Fatal(err)
			}
			got += len(b)
		} else {
			got += len(data)
		}
	}
	return outputs
}

// The PTY is read in fixed-size chunks with no regard for character
// boundaries. A box-drawing character cut in two made both chunks invalid
// UTF-8, so both went out as base64, which the terminal rendered as Latin-1.
func TestTerminalOutputKeepsMultibyteCharactersWhole(t *testing.T) {
	text := strings.Repeat("x", 4095) + strings.Repeat("─┬─ ok ", 600)
	b := []byte(text)
	reads := [][]byte{b[:4096], b[4096:8192], b[8192:]}

	var sent strings.Builder
	for _, out := range outputOf(t, reads) {
		if out["encoding"] != "utf8" {
			t.Fatalf("valid UTF-8 output was sent as %v", out["encoding"])
		}
		sent.WriteString(out["data"].(string))
	}
	if sent.String() != text {
		t.Fatal("terminal output arrived altered")
	}
}

// Output that is not UTF-8 at all still arrives byte for byte.
func TestTerminalOutputPassesInvalidBytesThrough(t *testing.T) {
	reads := [][]byte{[]byte("ok \xff\xfe "), []byte("\xe2\x94"), []byte("x")}
	var sent []byte
	for _, out := range outputOf(t, reads) {
		data := out["data"].(string)
		if out["encoding"] == "base64" {
			b, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				t.Fatal(err)
			}
			sent = append(sent, b...)
		} else {
			sent = append(sent, data...)
		}
	}
	if want := "ok \xff\xfe \xe2\x94x"; string(sent) != want {
		t.Fatalf("sent %q, want %q", sent, want)
	}
}
