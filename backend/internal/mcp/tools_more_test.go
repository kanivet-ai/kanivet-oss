package mcp

import (
	"context"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func serverTools(t *testing.T, s *mcpsdk.Server) map[string]*mcpsdk.Tool {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcpsdk.Tool{}
	for _, tl := range res.Tools {
		out[tl.Name] = tl
	}
	return out
}

func TestMoreToolsRegistered(t *testing.T) {
	s := newTestService(t)
	impl := &mcpsdk.Implementation{Name: "k"}
	ro := serverTools(t, s.reg.NewServer(impl, nil, false))
	full := serverTools(t, s.reg.NewServer(impl, nil, true))
	for _, n := range []string{"get_workload_logs", "query_prometheus", "discover_metrics", "get_prometheus_rules", "get_dashboard", "top_resources", "list_namespaces", "list_packages", "crossplane_trace", "list_vclusters", "get_node_load"} {
		if ro[n] == nil || full[n] == nil {
			t.Errorf("read tool %s missing", n)
		}
	}
	for _, n := range []string{"patch_resource", "manage_cronjob", "manage_argo_rollout"} {
		if ro[n] != nil {
			t.Errorf("write tool %s on readonly", n)
		}
		if full[n] == nil {
			t.Errorf("write tool %s missing on full", n)
		}
	}
}

func TestExecToolFollowsSetting(t *testing.T) {
	s := newTestService(t)
	impl := &mcpsdk.Implementation{Name: "k"}
	ro := s.reg.NewServer(impl, nil, false)
	full := s.reg.NewServer(impl, nil, true)
	if serverTools(t, full)[execToolName] != nil {
		t.Fatal("exec_pod registered while the setting is off")
	}
	if _, err := s.store.Update(func(st *Settings) { st.AllowExec = true }); err != nil {
		t.Fatal(err)
	}
	s.syncExecTool()
	defer func() {
		_, _ = s.store.Update(func(st *Settings) { st.AllowExec = false })
		s.syncExecTool()
	}()
	tl := serverTools(t, full)[execToolName]
	if tl == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
		t.Fatal("exec_pod missing on /mcp or not destructive")
	}
	if serverTools(t, ro)[execToolName] != nil {
		t.Fatal("exec_pod served on /mcp-readonly")
	}
	_, _ = s.store.Update(func(st *Settings) { st.AllowExec = false })
	s.syncExecTool()
	if serverTools(t, full)[execToolName] != nil {
		t.Fatal("exec_pod still served after turning the setting off")
	}
}

func TestKeepPrioritisedPrefersErrors(t *testing.T) {
	now := time.Now()
	ls := []logLine{{t: now, text: "ok 1"}, {t: now.Add(time.Second), text: "ERROR boom", err: true}, {t: now.Add(2 * time.Second), text: "ok 2"}}
	kept := keepPrioritised(ls, 2)
	if len(kept) != 2 || !kept[0].err || kept[1].text != "ok 2" {
		t.Fatalf("%+v", kept)
	}
}

func TestParseTimestampedScrubsAndFilters(t *testing.T) {
	raw := []byte("2026-11-04T10:00:00.000000001Z starting\n2026-11-04T10:00:01Z token=abcdef0123456789abcdef error\n")
	ls := parseTimestamped(raw, "pod-a", nil)
	if len(ls) != 2 || ls[0].t.IsZero() || ls[0].text != "starting" || !ls[1].err {
		t.Fatalf("%+v", ls)
	}
	if ls[1].text == "token=abcdef0123456789abcdef error" {
		t.Fatal("secret not scrubbed")
	}
}
