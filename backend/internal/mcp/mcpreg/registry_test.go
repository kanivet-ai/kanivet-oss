package mcpreg

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	ClusterArg
	Name string `json:"name" jsonschema:"what to echo"`
}

func connect(t *testing.T, s *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-agent", Version: "1.0"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcpsdk.ClientSession) map[string]*mcpsdk.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcpsdk.Tool{}
	for _, tl := range res.Tools {
		out[tl.Name] = tl
	}
	return out
}

func TestRegistryReadonlyExcludesWrites(t *testing.T) {
	r := New()
	RegisterOn(r, Spec{Name: "read_thing", Description: "reads"}, func(ctx context.Context, c *Call, in echoArgs) (any, error) {
		return map[string]string{"name": in.Name, "cluster": c.Cluster}, nil
	})
	RegisterOn(r, Spec{Name: "delete_thing", Description: "deletes", Destructive: true}, func(ctx context.Context, c *Call, in echoArgs) (any, error) {
		return "deleted", nil
	})

	ro := toolNames(t, connect(t, r.NewServer(&mcpsdk.Implementation{Name: "k"}, nil, false)))
	if _, ok := ro["delete_thing"]; ok {
		t.Fatal("readonly server exposes a write tool")
	}
	if tl := ro["read_thing"]; tl == nil || !tl.Annotations.ReadOnlyHint {
		t.Fatal("read tool missing or not marked read-only")
	}

	full := toolNames(t, connect(t, r.NewServer(&mcpsdk.Implementation{Name: "k"}, nil, true)))
	d := full["delete_thing"]
	if d == nil || d.Annotations.DestructiveHint == nil || !*d.Annotations.DestructiveHint {
		t.Fatal("destructive tool missing or not marked destructive")
	}
	if !strings.HasPrefix(d.Description, "DESTRUCTIVE") {
		t.Fatalf("description not flagged: %q", d.Description)
	}
}

func TestRegistryCallResolvesClusterAndObserves(t *testing.T) {
	r := New()
	r.SetClusterResolver(func(ctx context.Context, req string) (string, error) {
		if req == "" {
			return "default-ctx", nil
		}
		if req == "bad" {
			return "", errors.New("unknown cluster")
		}
		return req, nil
	})
	var mu sync.Mutex
	var recs []Record
	r.SetObserver(func(rec Record) { mu.Lock(); recs = append(recs, rec); mu.Unlock() })
	RegisterOn(r, Spec{Name: "read_thing", Description: "reads"}, func(ctx context.Context, c *Call, in echoArgs) (any, error) {
		return map[string]string{"name": in.Name, "cluster": c.Cluster}, nil
	})
	cs := connect(t, r.NewServer(&mcpsdk.Implementation{Name: "k"}, nil, false))

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "read_thing", Arguments: map[string]any{"name": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Content[0].(*mcpsdk.TextContent).Text
	if got != `{"cluster":"default-ctx","name":"x"}` {
		t.Fatalf("unexpected result %s", got)
	}
	res, _ = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "read_thing", Arguments: map[string]any{"name": "x", "cluster": "bad"}})
	if !res.IsError {
		t.Fatal("expected tool error for bad cluster")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recs) != 2 || recs[0].Client != "test-agent 1.0" || !recs[0].OK || recs[1].OK {
		t.Fatalf("unexpected records %+v", recs)
	}
}

func TestRegistryLateRegistrationReachesBuiltServers(t *testing.T) {
	r := New()
	s := r.NewServer(&mcpsdk.Implementation{Name: "k"}, nil, false)
	RegisterOn(r, Spec{Name: "late_tool", Description: "late", Feature: "topology"}, func(ctx context.Context, c *Call, in echoArgs) (any, error) {
		return "hi", nil
	})
	if _, ok := toolNames(t, connect(t, s))["late_tool"]; !ok {
		t.Fatal("late registration not visible")
	}
}

func TestRegistryPanicsOnBadSpecs(t *testing.T) {
	type noCluster struct {
		Name string `json:"name"`
	}
	cases := map[string]func(){
		"no cluster": func() {
			RegisterOn(New(), Spec{Name: "x_tool", Description: "d"}, func(ctx context.Context, c *Call, in noCluster) (any, error) { return nil, nil })
		},
		"bad name": func() {
			RegisterOn(New(), Spec{Name: "Bad-Name", Description: "d"}, func(ctx context.Context, c *Call, in echoArgs) (any, error) { return nil, nil })
		},
		"duplicate": func() {
			r := New()
			h := func(ctx context.Context, c *Call, in echoArgs) (any, error) { return nil, nil }
			RegisterOn(r, Spec{Name: "dup", Description: "d"}, h)
			RegisterOn(r, Spec{Name: "dup", Description: "d"}, h)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn()
		})
	}
}

func TestHandlerPanicBecomesToolError(t *testing.T) {
	r := New()
	RegisterOn(r, Spec{Name: "boom", Description: "d"}, func(ctx context.Context, c *Call, in echoArgs) (any, error) { panic("kaboom") })
	cs := connect(t, r.NewServer(&mcpsdk.Implementation{Name: "k"}, nil, false))
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "boom", Arguments: map[string]any{"name": "x"}})
	if err != nil || !res.IsError {
		t.Fatalf("expected tool error, got %v %v", res, err)
	}
}
