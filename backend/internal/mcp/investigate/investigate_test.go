package investigate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const claudeTranscript = `{"type":"system","subtype":"init","mcp_servers":[{"name":"kanivet","status":"connected"}]}
{"type":"assistant","message":{"content":[{"type":"text","text":"Let me diagnose."},{"type":"tool_use","name":"mcp__kanivet__diagnose","input":{"kind":"Deployment","name":"web"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"{\"health\":\"failing\"}"}],"is_error":false}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.12,"result":"## Summary\nweb is crashing.\n` + "```json" + `\n{\"summary\":\"web crashes on start\",\"severity\":\"critical\",\"findings\":[{\"title\":\"OOM\",\"resources\":[{\"kind\":\"Pod\",\"apiVersion\":\"v1\",\"namespace\":\"prod\",\"name\":\"web-1\"}]}],\"resources\":[{\"kind\":\"Deployment\",\"apiVersion\":\"apps/v1\",\"namespace\":\"prod\",\"name\":\"web\"}]}\n` + "```" + `"}
`

func TestHelperProcess(t *testing.T) {
	if os.Getenv("KANIVET_HELPER") != "1" {
		return
	}
	fmt.Print(claudeTranscript)
	os.Exit(0)
}

func TestParseClaudeAndFindings(t *testing.T) {
	var events []Event
	var final string
	for _, l := range strings.Split(strings.TrimSpace(claudeTranscript), "\n") {
		p := parseClaudeLine([]byte(l))
		events = append(events, p.events...)
		if p.final != "" {
			final = p.final
		}
	}
	if len(events) != 4 || events[2].Tool != "diagnose" || events[3].OK == nil || !*events[3].OK {
		t.Fatalf("unexpected events %+v", events)
	}
	f, report := ExtractFindings(final)
	if f == nil || f.Summary != "web crashes on start" || len(f.Findings) != 1 || f.Findings[0].Resources[0].Name != "web-1" {
		t.Fatalf("findings: %+v", f)
	}
	if strings.Contains(report, "```") || !strings.Contains(report, "web is crashing") {
		t.Fatalf("report: %q", report)
	}
}

func TestParseCodex(t *testing.T) {
	lines := []string{
		`{"type":"thread.started","thread_id":"x"}`,
		`{"type":"item.started","item":{"id":"1","type":"mcp_tool_call","server":"kanivet","tool":"get_incidents","arguments":{},"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"1","type":"mcp_tool_call","server":"kanivet","tool":"get_incidents","status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"2","type":"agent_message","text":"done"}}`,
	}
	var n int
	var final string
	for _, l := range lines {
		p := parseCodexLine([]byte(l))
		n += len(p.events)
		if p.final != "" {
			final = p.final
		}
	}
	if n != 4 || final != "done" {
		t.Fatalf("n=%d final=%q", n, final)
	}
}

func TestManagerRunsAgentAndCollectsFindings(t *testing.T) {
	m := NewManager(func() (string, string, bool) { return "http://127.0.0.1:1/mcp-readonly", "tok", true })
	m.detect = func() []Agent { return []Agent{{ID: "claude", Name: "Claude Code", Available: true, Path: os.Args[0]}} }
	var gotPrompt string
	m.command = func(ctx context.Context, a Agent, prompt, url, token, dir string) (*exec.Cmd, func([]byte) parsed, error) {
		gotPrompt = prompt
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "KANIVET_HELPER=1")
		return cmd, parseClaudeLine, nil
	}
	inv, err := m.Start("", Target{Cluster: "kind-dev", Kind: "Deployment", Namespace: "prod", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var snap *Investigation
	for time.Now().Before(deadline) {
		snap, _ = m.Snapshot(inv.ID, 0)
		if snap.Status != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snap.Status != "done" || snap.Findings == nil || snap.ToolCalls != 1 || snap.CostUSD != 0.12 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if snap.Findings.Resources[0].Cluster != "kind-dev" {
		t.Fatal("cluster not filled into resource refs")
	}
	if !strings.Contains(gotPrompt, `cluster="kind-dev"`) || !strings.Contains(gotPrompt, "Deployment in namespace prod named web") {
		t.Fatalf("prompt: %s", gotPrompt)
	}
	// Incremental fetch.
	tail, _ := m.Snapshot(inv.ID, len(snap.Events)-1)
	if len(tail.Events) != 1 {
		t.Fatalf("after-filter returned %d events", len(tail.Events))
	}
}

func TestStartWithoutAgentOrDisabled(t *testing.T) {
	m := NewManager(func() (string, string, bool) { return "", "", true })
	m.detect = func() []Agent { return []Agent{{ID: "claude"}} }
	if _, err := m.Start("", Target{Cluster: "c"}); err != ErrNoAgent {
		t.Fatalf("expected ErrNoAgent, got %v", err)
	}
	m2 := NewManager(func() (string, string, bool) { return "", "", false })
	if _, err := m2.Start("", Target{Cluster: "c"}); err == nil {
		t.Fatal("expected error when MCP disabled")
	}
}

func TestClaudeCommandIsRestricted(t *testing.T) {
	dir := t.TempDir()
	cmd, _, err := buildCommand(context.Background(), Agent{ID: "claude", Path: "claude"}, "p", "http://x/mcp-readonly", "tok", dir)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--strict-mcp-config", "--allowedTools mcp__kanivet__*", "Bash", "--output-format stream-json"} {
		if !strings.Contains(args, want) {
			t.Errorf("claude args lack %q: %s", want, args)
		}
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "KANIVET_SESSION_SECRET=") {
			t.Error("session secret leaked to agent env")
		}
	}
}
