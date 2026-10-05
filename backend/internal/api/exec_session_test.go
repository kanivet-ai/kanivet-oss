package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
)

type fakeExecutor struct {
	run func(ctx context.Context, opts remotecommand.StreamOptions) error
}

func (f *fakeExecutor) Stream(opts remotecommand.StreamOptions) error {
	return f.run(context.Background(), opts)
}

func (f *fakeExecutor) StreamWithContext(ctx context.Context, opts remotecommand.StreamOptions) error {
	return f.run(ctx, opts)
}

// execTestServer runs runExecSession against a fake executor and reports when
// the handler returned.
func execTestServer(t *testing.T, exec remotecommand.Executor) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	returned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ec := &execConn{conn: conn}
		ctx, cancel, msgs := startExecReader(conn)
		defer cancel()
		runExecSession(ctx, cancel, ec, msgs, exec)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, returned
}

func waitReturned(t *testing.T, returned <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatalf("handler did not return: %s", what)
	}
}

func TestExecSessionReturnsWhenExecutorFailsImmediately(t *testing.T) {
	c, returned := execTestServer(t, &fakeExecutor{run: func(ctx context.Context, opts remotecommand.StreamOptions) error {
		return errors.New("exec refused")
	}})
	waitReturned(t, returned, "executor failed")

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var sawError bool
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if strings.Contains(string(data), "exec refused") {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("client never saw the exec error")
	}
}

func TestExecSessionConcurrentWritersDoNotPanic(t *testing.T) {
	// Output pump writes binary frames while the stream goroutine reports
	// its error as a text frame: gorilla panics if these overlap.
	c, returned := execTestServer(t, &fakeExecutor{run: func(ctx context.Context, opts remotecommand.StreamOptions) error {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					_, _ = opts.Stdout.Write([]byte("output"))
				}
			}()
		}
		wg.Wait()
		return errors.New("done with error")
	}})
	go func() {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	waitReturned(t, returned, "concurrent writers")
}

func TestExecSessionClientLeavingCancelsExecutor(t *testing.T) {
	cancelled := make(chan struct{})
	c, returned := execTestServer(t, &fakeExecutor{run: func(ctx context.Context, opts remotecommand.StreamOptions) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}})
	time.Sleep(100 * time.Millisecond)
	c.Close()
	waitReturned(t, returned, "client left")
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("executor context was not cancelled")
	}
}

func TestExecSessionShellExitDoesNotBlockOnStdin(t *testing.T) {
	// Shell exits right away; the client then keeps typing. Writes to the
	// stdin pipe must not wedge the handler.
	c, returned := execTestServer(t, &fakeExecutor{run: func(ctx context.Context, opts remotecommand.StreamOptions) error {
		return nil
	}})
	for i := 0; i < 3; i++ {
		_ = c.WriteMessage(websocket.BinaryMessage, []byte("ls\r"))
	}
	waitReturned(t, returned, "stdin after exit")
}

func TestExecCheckOrigin(t *testing.T) {
	cases := map[string]bool{
		"":                          true,
		"null":                      true,
		"file://":                   true,
		"http://localhost:5173":     true,
		"http://localhost:3000":     true,
		"http://evil.example.com":   false,
		"https://localhost:5173":    false,
		"http://localhost.evil.com": false,
	}
	for origin, want := range cases {
		r := httptest.NewRequest("GET", "/api/v1/ws/exec", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if got := execCheckOrigin(r); got != want {
			t.Errorf("origin %q: got %v want %v", origin, got, want)
		}
	}
}
