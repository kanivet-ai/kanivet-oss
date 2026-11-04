package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(t.TempDir(), Deps{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHTTPHandshakeAndAuth(t *testing.T) {
	s := newTestService(t)
	srv := httptest.NewServer(s.Handler(false))
	defer srv.Close()

	// No token: 401.
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearer{s.Token(), http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("readonly endpoint serves non-read tool %s", tl.Name)
		}
	}
	for _, want := range []string{"list_clusters", "list_resources", "get_resource", "get_events", "get_logs", "search", "get_incidents", "diagnose", "list_helm_releases", "list_argo_apps", "get_metrics", "get_costs"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if names["delete_pod"] || names["apply_yaml"] {
		t.Error("write tools on readonly endpoint")
	}
	// A call without a k8s client reports a tool error, not a protocol error.
	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "list_clusters", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("expected tool error, got %v %v", res, err)
	}
	if calls := s.Store().Calls(10); len(calls) != 1 || calls[0].Client != "e2e 1" || calls[0].Tool != "list_clusters" {
		t.Fatalf("call log: %+v", calls)
	}
}

func TestFullEndpointRespectsAllowWrites(t *testing.T) {
	s := newTestService(t)
	srv := httptest.NewServer(s.Handler(true))
	defer srv.Close()
	ctx := context.Background()
	connect := func() (*mcpsdk.ClientSession, error) {
		return mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e"}, nil).Connect(ctx, &mcpsdk.StreamableClientTransport{
			Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer{s.Token(), http.DefaultTransport}}, MaxRetries: -1,
		}, nil)
	}
	cs, err := connect()
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := cs.ListTools(ctx, nil)
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == "delete_pod" {
			found = tl.Annotations.DestructiveHint != nil && *tl.Annotations.DestructiveHint
		}
	}
	cs.Close()
	if !found {
		t.Fatal("full endpoint lacks destructive delete_pod")
	}
	if _, err := s.Store().Update(func(st *Settings) { st.AllowWrites = false }); err != nil {
		t.Fatal(err)
	}
	if _, err := connect(); err == nil {
		t.Fatal("full endpoint accepted a session with writes disabled")
	}
}

func TestTokenPersistedPrivately(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Settings().Token == "" || a.Settings().Token != b.Settings().Token {
		t.Fatal("token not persisted")
	}
	if fi, err := os.Stat(filepath.Join(dir, "mcp.json")); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("token file permissions: %v %v", fi.Mode(), err)
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:5": true, "[::1]:80": true, "10.0.0.2:1": false, "192.168.1.1:9": false} {
		if isLoopback(addr) != want {
			t.Errorf("isLoopback(%s) != %v", addr, want)
		}
	}
}

func TestSetupRecipes(t *testing.T) {
	rs := SetupRecipes("http://127.0.0.1:1/mcp-readonly", "http://127.0.0.1:1/mcp", "kmcp_x")
	labels := map[string]bool{}
	for _, r := range rs {
		labels[r.Label] = true
		if !strings.Contains(r.Snippet, "kmcp_x") {
			t.Errorf("%s snippet lacks token", r.ID)
		}
	}
	for _, l := range []string{"Claude Code", "Codex", "Cursor", "VS Code", "Claude Desktop"} {
		if !labels[l] {
			t.Errorf("missing recipe %s", l)
		}
	}
}
