package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioBridgeRelaysAndFollowsTokenRotation(t *testing.T) {
	s := newTestService(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Handler(r.URL.Path == "/mcp").ServeHTTP(w, r)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	s.SetPort(ln.Addr().(*net.TCPAddr).Port)

	// Client <-> bridge over in-process pipes standing in for stdio.
	cr, bw := io.Pipe()
	br, cw := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runBridge(ctx, s.store.path, false, &mcpsdk.IOTransport{Reader: br, Writer: bw}) }()

	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "desktop-test"}, nil).Connect(ctx, &mcpsdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list := func() int {
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Tools)
	}
	if n := list(); n == 0 {
		t.Fatal("no tools through the bridge")
	}
	if _, err := s.store.Update(func(st *Settings) { st.Token = NewToken() }); err != nil {
		t.Fatal(err)
	}
	if n := list(); n == 0 {
		t.Fatal("bridge broke after token rotation")
	}
	cs.Close()
	cancel()
	<-done
}
