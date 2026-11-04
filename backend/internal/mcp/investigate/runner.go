package investigate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Target is what to investigate. Kind/Name may be empty for a cluster-wide
// question (Question then says what to look at).
type Target struct {
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	// Context is extra text the UI adds, e.g. the incident being looked at.
	Context string `json:"context,omitempty"`
	// Question overrides the default "why is this unhealthy" question.
	Question string `json:"question,omitempty"`
}

// Label is a short human description of the target.
func (t Target) Label() string {
	if t.Name == "" {
		return "cluster " + t.Cluster
	}
	if t.Namespace != "" {
		return t.Kind + " " + t.Namespace + "/" + t.Name
	}
	return t.Kind + " " + t.Name
}

// Investigation is one agent run.
type Investigation struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`
	Target    Target     `json:"target"`
	Status    string     `json:"status"` // running | done | failed | cancelled
	Started   time.Time  `json:"started"`
	Ended     *time.Time `json:"ended,omitempty"`
	Error     string     `json:"error,omitempty"`
	Report    string     `json:"report,omitempty"`
	Findings  *Findings  `json:"findings,omitempty"`
	CostUSD   float64    `json:"costUsd,omitempty"`
	Events    []Event    `json:"events"`
	ToolCalls int        `json:"toolCalls"`

	mu     sync.Mutex
	cancel context.CancelFunc
}

// Endpoint supplies the read-only MCP URL and token at run time.
type Endpoint func() (url, token string, enabled bool)

// Manager runs and keeps investigations (the last 20).
type Manager struct {
	endpoint Endpoint
	mu       sync.Mutex
	runs     map[string]*Investigation
	order    []string
	// command builds the process; replaced in tests.
	command func(ctx context.Context, agent Agent, prompt, mcpURL, token, workdir string) (*exec.Cmd, func([]byte) parsed, error)
	detect  func() []Agent
}

// NewManager returns a manager using endpoint for MCP access.
func NewManager(endpoint Endpoint) *Manager {
	return &Manager{endpoint: endpoint, runs: map[string]*Investigation{}, command: buildCommand, detect: DetectAgents}
}

const maxRuns = 20

// ErrNoAgent is returned when no supported CLI is installed.
var ErrNoAgent = errors.New("no supported agent CLI found (install Claude Code or Codex)")

// Start launches an investigation with the given agent ("" picks the first
// available).
func (m *Manager) Start(agentID string, t Target) (*Investigation, error) {
	url, token, enabled := m.endpoint()
	if !enabled {
		return nil, errors.New("the Kanivet MCP server is turned off; enable it in AI & MCP settings")
	}
	if t.Cluster == "" {
		return nil, errors.New("cluster is required")
	}
	var agent *Agent
	for _, a := range m.detect() {
		if a.Available && (agentID == "" || a.ID == agentID) {
			a := a
			agent = &a
			break
		}
	}
	if agent == nil {
		if agentID != "" {
			return nil, fmt.Errorf("%s CLI not found on PATH", agentID)
		}
		return nil, ErrNoAgent
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	inv := &Investigation{ID: uuid.NewString(), Agent: agent.Name, Target: t, Status: "running", Started: time.Now(), Events: []Event{}, cancel: cancel}
	m.mu.Lock()
	m.runs[inv.ID] = inv
	m.order = append(m.order, inv.ID)
	for len(m.order) > maxRuns {
		old := m.order[0]
		m.order = m.order[1:]
		if r := m.runs[old]; r != nil && r.Status == "running" && r.cancel != nil {
			r.cancel()
		}
		delete(m.runs, old)
	}
	m.mu.Unlock()

	inv.add(Event{Type: "status", Text: fmt.Sprintf("Starting %s on %s", agent.Name, t.Label())})
	go m.run(ctx, inv, *agent, url, token)
	return inv, nil
}

func (inv *Investigation) add(e Event) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	e.Seq = len(inv.Events) + 1
	e.Time = time.Now().UnixMilli()
	if e.Type == "tool" {
		inv.ToolCalls++
	}
	inv.Events = append(inv.Events, e)
}

func (inv *Investigation) finish(status, errMsg string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if inv.Status != "running" {
		return
	}
	now := time.Now()
	inv.Status, inv.Ended, inv.Error = status, &now, errMsg
}

func (m *Manager) run(ctx context.Context, inv *Investigation, agent Agent, url, token string) {
	defer inv.cancel()
	workdir, err := os.MkdirTemp("", "kanivet-investigation-")
	if err != nil {
		inv.finish("failed", err.Error())
		return
	}
	defer os.RemoveAll(workdir)

	cmd, parse, err := m.command(ctx, agent, BuildPrompt(inv.Target), url, token, workdir)
	if err != nil {
		inv.finish("failed", err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		inv.finish("failed", err.Error())
		return
	}
	var stderr strings.Builder
	var blocked atomic.Bool
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		inv.finish("failed", err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		inv.finish("failed", "could not start "+agent.Name+": "+err.Error())
		return
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		lw := &limitedWriter{w: &stderr, n: 8 << 10}
		sc := bufio.NewScanner(errPipe)
		for sc.Scan() {
			line := sc.Text()
			_, _ = lw.Write([]byte(line + "\n"))
			if isMCPBlocked(line) {
				blocked.Store(true)
				inv.add(Event{Type: "error", Text: strings.TrimSpace(line)})
			}
		}
	}()

	var final, agentErr string
	var cost float64
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 256<<10), 16<<20)
	for sc.Scan() {
		p := parse(sc.Bytes())
		for _, e := range p.events {
			inv.add(e)
		}
		if p.final != "" {
			final = p.final
		}
		if p.cost > 0 {
			cost = p.cost
		}
		if p.err != "" {
			agentErr = p.err
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	<-stderrDone
	waitErr := cmd.Wait()

	findings, report := ExtractFindings(final)
	inv.mu.Lock()
	inv.Report, inv.Findings, inv.CostUSD = report, findings, cost
	if findings != nil {
		for i := range findings.Resources {
			if findings.Resources[i].Cluster == "" {
				findings.Resources[i].Cluster = inv.Target.Cluster
			}
		}
		for i := range findings.Findings {
			for j := range findings.Findings[i].Resources {
				if findings.Findings[i].Resources[j].Cluster == "" {
					findings.Findings[i].Resources[j].Cluster = inv.Target.Cluster
				}
			}
		}
	}
	inv.mu.Unlock()

	inv.mu.Lock()
	toolCalls := inv.ToolCalls
	inv.mu.Unlock()
	switch {
	case blocked.Load() && toolCalls == 0:
		inv.finish("failed", agent.Name+" did not load Kanivet's MCP server: its managed or enterprise MCP policy blocks it. Allow the \"kanivet\" server in that policy, or use another agent.")
	case errors.Is(ctx.Err(), context.Canceled):
		inv.finish("cancelled", "")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		inv.finish("failed", "timed out after 15 minutes")
	case final == "" && (waitErr != nil || agentErr != ""):
		msg := agentErr
		if msg == "" {
			msg = strings.TrimSpace(stderr.String())
		}
		if msg == "" {
			msg = waitErr.Error()
		}
		inv.finish("failed", hintFor(agent, msg))
	default:
		inv.finish("done", "")
	}
	inv.add(Event{Type: "status", Text: "Finished: " + inv.Status})
}

func hintFor(agent Agent, msg string) string {
	low := strings.ToLower(msg)
	if strings.Contains(low, "login") || strings.Contains(low, "auth") || strings.Contains(low, "api key") {
		return msg + fmt.Sprintf(" (sign in by running `%s` once in a terminal)", agent.ID)
	}
	if len(msg) > 800 {
		msg = msg[len(msg)-800:]
	}
	return msg
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

// disallowed built-ins: the agent may only use Kanivet's MCP tools.
var claudeDisallowed = []string{"Bash", "Edit", "Write", "MultiEdit", "Read", "Glob", "Grep", "LS", "WebFetch", "WebSearch", "NotebookEdit", "NotebookRead", "Task", "TodoWrite", "KillShell", "BashOutput"}

func buildCommand(ctx context.Context, agent Agent, prompt, mcpURL, token, workdir string) (*exec.Cmd, func([]byte) parsed, error) {
	switch agent.ID {
	case "claude":
		cfg := map[string]any{"mcpServers": map[string]any{"kanivet": map[string]any{
			"type": "http", "url": mcpURL, "headers": map[string]string{"Authorization": "Bearer " + token},
		}}}
		b, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(workdir, "mcp.json")
		if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
			return nil, nil, err
		}
		args := []string{
			"-p", prompt,
			"--mcp-config", cfgPath,
			"--strict-mcp-config",
			"--allowedTools", "mcp__kanivet__*",
			"--disallowedTools", strings.Join(claudeDisallowed, ","),
			"--output-format", "stream-json",
			"--verbose",
		}
		cmd := exec.CommandContext(ctx, agent.Path, args...)
		cmd.Dir = workdir
		cmd.Env = childEnv(nil)
		return cmd, parseClaudeLine, nil
	case "codex":
		args := []string{
			"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only",
			"-c", fmt.Sprintf("mcp_servers.kanivet.url=%q", mcpURL),
			"-c", `mcp_servers.kanivet.bearer_token_env_var="KANIVET_MCP_TOKEN"`,
			prompt,
		}
		cmd := exec.CommandContext(ctx, agent.Path, args...)
		cmd.Dir = workdir
		cmd.Stdin = strings.NewReader("")
		cmd.Env = childEnv(map[string]string{"KANIVET_MCP_TOKEN": token})
		return cmd, parseCodexLine, nil
	}
	return nil, nil, fmt.Errorf("unsupported agent %q", agent.ID)
}

// BuildPrompt is the instruction given to the agent.
func BuildPrompt(t Target) string {
	var b strings.Builder
	b.WriteString("You are a Kubernetes SRE investigating a problem through Kanivet's MCP tools (server \"kanivet\"). ")
	b.WriteString("Use only those tools; they are read-only. Do not suggest you ran commands you did not run.\n\n")
	fmt.Fprintf(&b, "Cluster: %s (pass cluster=%q to every tool).\n", t.Cluster, t.Cluster)
	if t.Name != "" {
		fmt.Fprintf(&b, "Target: %s", t.Kind)
		if t.APIVersion != "" {
			fmt.Fprintf(&b, " (%s)", t.APIVersion)
		}
		if t.Namespace != "" {
			fmt.Fprintf(&b, " in namespace %s", t.Namespace)
		}
		fmt.Fprintf(&b, " named %s.\n", t.Name)
	}
	if t.Context != "" {
		fmt.Fprintf(&b, "Context from the user's screen:\n%s\n", truncate(t.Context, 2000))
	}
	q := t.Question
	if q == "" {
		if t.Name != "" {
			q = "Find out whether this resource is healthy and, if not, the root cause and how to fix it."
		} else {
			q = "Find what is currently failing in this cluster, the most likely root causes, and how to fix them."
		}
	}
	fmt.Fprintf(&b, "Question: %s\n\n", q)
	b.WriteString(`Method: start with diagnose on the target (or get_incidents for a cluster-wide question), then follow the evidence with get_events, get_logs, get_resource, list_resources and search. Check owners and dependencies (config, secrets by name only, services, nodes, Helm/Argo if they manage it). Stop when the cause is supported by evidence; keep tool calls focused.

Answer format: a short markdown report (## Summary, ## Root cause, ## Evidence, ## Fix) then, last, one fenced json block exactly like:
` + "```json" + `
{"summary":"one sentence","rootCause":"...","severity":"critical|warning|info|healthy","confidence":"high|medium|low",
 "findings":[{"title":"...","detail":"...","severity":"critical|warning|info","resources":[{"kind":"Pod","apiVersion":"v1","namespace":"ns","name":"x"}]}],
 "nextSteps":["..."],
 "resources":[{"kind":"Deployment","apiVersion":"apps/v1","namespace":"ns","name":"x"}]}
` + "```" + `
List every resource you cite in "resources" with exact kind, apiVersion, namespace and name so the user can open it.`)
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Snapshot is a copy safe to serialize; events after `after` only.
func (m *Manager) Snapshot(id string, after int) (*Investigation, bool) {
	m.mu.Lock()
	inv := m.runs[id]
	m.mu.Unlock()
	if inv == nil {
		return nil, false
	}
	inv.mu.Lock()
	defer inv.mu.Unlock()
	cp := &Investigation{ID: inv.ID, Agent: inv.Agent, Target: inv.Target, Status: inv.Status, Started: inv.Started, Ended: inv.Ended,
		Error: inv.Error, Report: inv.Report, Findings: inv.Findings, CostUSD: inv.CostUSD, ToolCalls: inv.ToolCalls}
	if after < 0 {
		after = 0
	}
	if after < len(inv.Events) {
		cp.Events = append([]Event(nil), inv.Events[after:]...)
	} else {
		cp.Events = []Event{}
	}
	return cp, true
}

// List returns run summaries, newest first (no events).
func (m *Manager) List() []*Investigation {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	var out []*Investigation
	for _, id := range ids {
		if s, ok := m.Snapshot(id, 1<<30); ok {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Cancel stops a running investigation.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	inv := m.runs[id]
	m.mu.Unlock()
	if inv == nil {
		return false
	}
	inv.cancel()
	return true
}

// isMCPBlocked recognises an agent CLI declining to load the server.
func isMCPBlocked(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "mcp") && strings.Contains(l, "kanivet") && strings.Contains(l, "polic")
}
