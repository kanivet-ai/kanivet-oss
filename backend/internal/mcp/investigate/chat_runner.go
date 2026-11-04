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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ChatManager runs multi-turn chats with an agent CLI. Each turn is one
// headless process that resumes the CLI's own session (claude --resume,
// codex exec resume), so the conversation survives app restarts.
type ChatManager struct {
	endpoint Endpoint
	store    *ChatStore
	locs     *LocationStore
	ops      ClusterOps // may be nil (no cluster layer: context gathering and proposals off)

	mu   sync.Mutex
	live map[string]*liveChat
	// spawn builds the process for a turn; replaced in tests.
	spawn  func(ctx context.Context, t TurnSpec) (*exec.Cmd, error)
	detect func() []Agent
	now    func() time.Time
}

type liveChat struct {
	mu      sync.Mutex
	chat    *Chat
	version int64
	cancel  context.CancelFunc
	dirty   bool
}

// NewChatManager stores chats under stateDir (~/.kanivet/ai).
func NewChatManager(stateDir string, endpoint Endpoint, ops ClusterOps) (*ChatManager, error) {
	st, err := NewChatStore(filepath.Join(stateDir, "chats"))
	if err != nil {
		return nil, err
	}
	m := &ChatManager{endpoint: endpoint, store: st, locs: NewLocationStore(filepath.Join(stateDir, "code-locations.json")),
		ops: ops, live: map[string]*liveChat{}, detect: DetectAgents, now: time.Now}
	m.spawn = m.defaultSpawn
	return m, nil
}

// NewChatInput starts a chat.
type NewChatInput struct {
	Agent   string        `json:"agent"`
	Mode    string        `json:"mode"`
	Cluster string        `json:"cluster"`
	Title   string        `json:"title"`
	Source  string        `json:"source"`
	NoMCP   bool          `json:"noMcp"`
	Text    string        `json:"text"`
	Context []ContextChip `json:"context"`
	// Code mode
	Repo       string `json:"repo"`
	NewBranch  bool   `json:"newBranch"`
	AllowDirty bool   `json:"allowDirty"`
}

func (m *ChatManager) pickAgent(id string) (*Agent, error) {
	for _, a := range m.detect() {
		if a.Available && (id == "" || a.ID == id) {
			a := a
			return &a, nil
		}
	}
	if id != "" {
		return nil, fmt.Errorf("%s CLI not found on PATH", id)
	}
	return nil, ErrNoAgent
}

// Create makes a chat and, if Text is set, sends the first message.
func (m *ChatManager) Create(in NewChatInput) (*Chat, error) {
	agent, err := m.pickAgent(in.Agent)
	if err != nil {
		return nil, err
	}
	mode := in.Mode
	if mode != ModeCode {
		mode = ModeAsk
	}
	now := m.now()
	c := &Chat{ID: uuid.NewString(), AgentID: agent.ID, Agent: agent.Name, Mode: mode, Cluster: in.Cluster,
		Source: in.Source, NoMCP: in.NoMCP, Created: now, Updated: now, Messages: []Message{}}
	if _, _, enabled := m.endpoint(); !enabled {
		c.NoMCP, c.MCPNote = true, "Kanivet's MCP server is turned off; context is inlined instead."
	}
	if c.NoMCP {
		c.MCP = MCPOff
	}
	if mode == ModeCode {
		if in.Repo == "" {
			return nil, errors.New("pick a code location (repository folder) for a code fix")
		}
		slug := in.Title
		if slug == "" {
			slug = in.Text
		}
		w, _, err := PrepareRepo(context.Background(), in.Repo, PrepareOpts{NewBranch: in.NewBranch, AllowDirty: in.AllowDirty || in.NewBranch, Slug: slug})
		if err != nil {
			return nil, err
		}
		c.Code = w
	}
	c.Title = in.Title
	if c.Title == "" {
		c.Title = TitleFrom(in.Text)
	}
	if err := m.store.Save(c); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) != "" {
		if err := m.Send(c.ID, in.Text, in.Context); err != nil {
			return nil, err
		}
	}
	snap, _, err := m.Get(c.ID, -1)
	return snap, err
}

func (m *ChatManager) liveFor(id string) (*liveChat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lc := m.live[id]; lc != nil {
		return lc, nil
	}
	c, err := m.store.Load(id)
	if err != nil {
		return nil, err
	}
	// A chat that was running when the app quit is not running any more.
	if c.Running {
		c.Running = false
		for i := range c.Messages {
			if c.Messages[i].Status == "running" {
				c.Messages[i].Status, c.Messages[i].Error = "cancelled", "interrupted (Kanivet restarted)"
			}
		}
	}
	lc := &liveChat{chat: c, version: 1}
	m.live[id] = lc
	return lc, nil
}

// Get returns a copy of the chat, or (nil, version, nil) when it has not
// changed since `since`.
func (m *ChatManager) Get(id string, since int64) (*Chat, int64, error) {
	lc, err := m.liveFor(id)
	if err != nil {
		return nil, 0, err
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if since >= 0 && since == lc.version {
		return nil, lc.version, nil
	}
	b, _ := json.Marshal(lc.chat)
	var cp Chat
	_ = json.Unmarshal(b, &cp)
	return &cp, lc.version, nil
}

// List returns saved chats.
func (m *ChatManager) List() []ChatSummary {
	out := m.store.List()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range out {
		if lc := m.live[out[i].ID]; lc != nil {
			lc.mu.Lock()
			out[i].Running = lc.chat.Running
			out[i].Title = lc.chat.Title
			lc.mu.Unlock()
		}
	}
	return out
}

// Rename sets a chat's title.
func (m *ChatManager) Rename(id, title string) error {
	return m.mutate(id, func(c *Chat) error {
		if t := strings.TrimSpace(title); t != "" {
			c.Title = TitleFrom(t)
		}
		return nil
	})
}

// Delete removes a chat (cancelling a running turn).
func (m *ChatManager) Delete(id string) error {
	m.mu.Lock()
	lc := m.live[id]
	delete(m.live, id)
	m.mu.Unlock()
	if lc != nil && lc.cancel != nil {
		lc.cancel()
	}
	return m.store.Delete(id)
}

// Cancel stops the running turn.
func (m *ChatManager) Cancel(id string) error {
	lc, err := m.liveFor(id)
	if err != nil {
		return err
	}
	lc.mu.Lock()
	cancel := lc.cancel
	lc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *ChatManager) mutate(id string, fn func(c *Chat) error) error {
	lc, err := m.liveFor(id)
	if err != nil {
		return err
	}
	lc.mu.Lock()
	err = fn(lc.chat)
	if err == nil {
		lc.chat.Updated = m.now()
		lc.version++
	}
	cp := *lc.chat
	lc.mu.Unlock()
	if err != nil {
		return err
	}
	return m.store.Save(&cp)
}

// ErrBusy is returned when a turn is already running.
var ErrBusy = errors.New("the agent is still answering; wait or stop it first")

// Send adds a user message and runs the agent's reply in the background.
func (m *ChatManager) Send(id, text string, chips []ContextChip) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("message is empty")
	}
	lc, err := m.liveFor(id)
	if err != nil {
		return err
	}
	lc.mu.Lock()
	if lc.chat.Running {
		lc.mu.Unlock()
		return ErrBusy
	}
	c := lc.chat
	agent, err := m.pickAgent(c.AgentID)
	if err != nil {
		lc.mu.Unlock()
		return err
	}
	first := c.SessionID == ""
	if first {
		if c.AgentID == "claude" {
			c.SessionID = uuid.NewString()
		}
	}
	scrub := m.scrubber()
	chips = m.enrichChips(c.Cluster, chips, scrub)
	now := m.now()
	c.Messages = append(c.Messages,
		Message{ID: uuid.NewString(), Role: "user", Text: text, Context: chips, Time: now},
		Message{ID: uuid.NewString(), Role: "assistant", Status: "running", Time: now, Events: []Event{}},
	)
	c.Running, c.Updated = true, now
	url, token, enabled := m.endpoint()
	useMCP := enabled && !c.NoMCP && c.MCP != MCPBlocked
	repo := ""
	if c.Code != nil {
		repo = c.Code.Repo
	}
	prompt := BuildChatPrompt(text, chips, PromptOpts{Mode: c.Mode, Cluster: c.Cluster, UseMCP: useMCP, MCPState: c.MCP, First: first, Repo: repo}, scrub)
	spec := TurnSpec{Agent: *agent, Mode: c.Mode, Prompt: prompt, SessionID: c.SessionID, Resume: !first,
		MCPURL: url, MCPToken: token, UseMCP: useMCP}
	if c.Mode == ModeCode && c.Code != nil {
		spec.Workdir = c.Code.Repo
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	lc.cancel = cancel
	lc.version++
	cp := *c
	lc.mu.Unlock()
	_ = m.store.Save(&cp)
	go m.runTurn(ctx, lc, spec)
	return nil
}

// enrichChips fetches live YAML (redacted) for resource chips that ask for
// it and redacts every text payload before it is stored.
func (m *ChatManager) enrichChips(cluster string, chips []ContextChip, scrub Scrubber) []ContextChip {
	out := make([]ContextChip, 0, len(chips))
	for _, ch := range chips {
		if ch.Cluster == "" {
			ch.Cluster = cluster
		}
		if ch.IncludeYAML && ch.Type == "resource" && m.ops != nil && ch.Name != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			obj, err := m.ops.FetchRedacted(ctx, ch.Cluster, ch.Kind, ch.Namespace, ch.Name)
			cancel()
			if err == nil {
				ch.Text = toYAML(obj)
			} else {
				ch.Text = "(could not read the live object: " + err.Error() + ")"
			}
			ch.IncludeYAML = false
		}
		ch.Text = RedactText(ch.Text, scrub)
		out = append(out, ch)
	}
	return out
}

func (m *ChatManager) scrubber() Scrubber {
	if m.ops == nil {
		return nil
	}
	return m.ops.Scrub
}

func (m *ChatManager) defaultSpawn(ctx context.Context, t TurnSpec) (*exec.Cmd, error) {
	switch t.Agent.ID {
	case "claude":
		cfg := ""
		if t.UseMCP {
			p, err := writeMCPConfig(t.ScratchDir, t.MCPURL, t.MCPToken)
			if err != nil {
				return nil, err
			}
			cfg = p
		}
		cmd := exec.CommandContext(ctx, t.Agent.Path, ClaudeArgs(t, cfg)...)
		cmd.Dir = t.Workdir
		cmd.Env = childEnv(nil)
		cmd.Stdin = strings.NewReader("")
		return cmd, nil
	case "codex":
		cmd := exec.CommandContext(ctx, t.Agent.Path, CodexArgs(t)...)
		cmd.Dir = t.Workdir
		cmd.Stdin = strings.NewReader("")
		cmd.Env = childEnv(map[string]string{"KANIVET_MCP_TOKEN": t.MCPToken})
		return cmd, nil
	}
	return nil, fmt.Errorf("unsupported agent %q", t.Agent.ID)
}

// turnState accumulates one assistant message from the stream.
type turnState struct {
	lc       *liveChat
	sawDelta atomic.Bool
}

func (m *ChatManager) appendEvent(lc *liveChat, e Event) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	msg := &lc.chat.Messages[len(lc.chat.Messages)-1]
	if e.Type == "delta" || e.Type == "text" {
		// Merge consecutive text into one block.
		if n := len(msg.Events); n > 0 && msg.Events[n-1].Type == "text" {
			if e.Type == "text" {
				msg.Events[n-1].Text += "\n\n"
			}
			msg.Events[n-1].Text += e.Text
		} else {
			msg.Events = append(msg.Events, Event{Seq: len(msg.Events) + 1, Time: m.now().UnixMilli(), Type: "text", Text: e.Text})
		}
		if msg.Text != "" && e.Type == "text" {
			msg.Text += "\n\n"
		}
		msg.Text += e.Text
	} else {
		e.Seq = len(msg.Events) + 1
		e.Time = m.now().UnixMilli()
		msg.Events = append(msg.Events, e)
	}
	lc.version++
	lc.dirty = true
}

func (m *ChatManager) runTurn(ctx context.Context, lc *liveChat, spec TurnSpec) {
	defer func() {
		lc.mu.Lock()
		if lc.cancel != nil {
			lc.cancel()
		}
		lc.cancel = nil
		lc.mu.Unlock()
	}()
	scratch, err := os.MkdirTemp("", "kanivet-chat-")
	if err != nil {
		m.finishTurn(lc, "failed", err.Error(), "", 0)
		return
	}
	defer os.RemoveAll(scratch)
	spec.ScratchDir = scratch
	if spec.Workdir == "" {
		spec.Workdir = scratch
	}
	cmd, err := m.spawn(ctx, spec)
	if err != nil {
		m.finishTurn(lc, "failed", err.Error(), "", 0)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.finishTurn(lc, "failed", err.Error(), "", 0)
		return
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		m.finishTurn(lc, "failed", err.Error(), "", 0)
		return
	}
	if err := cmd.Start(); err != nil {
		m.finishTurn(lc, "failed", "could not start "+spec.Agent.Name+": "+err.Error(), "", 0)
		return
	}
	var stderr strings.Builder
	var blocked atomic.Bool
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
			}
		}
	}()
	// Persist streaming progress about once a second.
	stopSaver := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopSaver:
				return
			case <-t.C:
				m.saveIfDirty(lc)
			}
		}
	}()

	var final, agentErr string
	var cost float64
	ts := &turnState{lc: lc}
	sessionSeen := false
	lastReconnect := ""
	defer func() {
		// The CLI never started its session (e.g. not logged in): the next
		// turn must start a fresh one instead of resuming a missing id.
		if !sessionSeen && !spec.Resume {
			lc.mu.Lock()
			lc.chat.SessionID = ""
			lc.mu.Unlock()
		}
	}()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 256<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var p parsed
		var extra chatExtras
		if spec.Agent.ID == "codex" {
			p = parseCodexLine(line)
			extra = codexExtras(line)
		} else {
			p, extra = parseClaudeChatLine(line, ts.sawDelta.Load())
			if extra.delta != "" {
				ts.sawDelta.Store(true)
			}
		}
		if extra.sessionID != "" {
			sessionSeen = true
			lc.mu.Lock()
			if lc.chat.SessionID == "" || spec.Agent.ID == "codex" {
				lc.chat.SessionID = extra.sessionID
			}
			lc.mu.Unlock()
		}
		if extra.mcpStatus != "" && spec.UseMCP {
			m.setMCP(lc, extra.mcpStatus)
		}
		if extra.delta != "" {
			m.appendEvent(lc, Event{Type: "delta", Text: extra.delta})
		}
		for _, e := range p.events {
			if e.Type == "status" && strings.HasPrefix(e.Text, "Agent started") {
				continue
			}
			if e.Type == "error" && strings.Contains(e.Text, "Kanivet MCP") {
				continue // shown in the header instead
			}
			if e.Type == "error" && strings.HasPrefix(e.Text, "Reconnecting") {
				// codex retries; show one status line, not five errors.
				if lastReconnect != "" {
					continue
				}
				lastReconnect = e.Text
				e.Type, e.Text = "status", spec.Agent.Name+" is having trouble reaching its service and is retrying ("+e.Text+")"
			}
			m.appendEvent(lc, e)
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
	close(stopSaver)

	if blocked.Load() && spec.UseMCP {
		m.setMCP(lc, MCPBlocked)
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		m.finishTurn(lc, "cancelled", "", final, cost)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		m.finishTurn(lc, "failed", "timed out after 20 minutes", final, cost)
	case final == "" && (waitErr != nil || agentErr != ""):
		msg := agentErr
		if msg == "" {
			msg = strings.TrimSpace(stderr.String())
		}
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		m.finishTurn(lc, "failed", hintFor(spec.Agent, msg), final, cost)
	default:
		m.finishTurn(lc, "done", "", final, cost)
	}
}

func (m *ChatManager) setMCP(lc *liveChat, status string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	switch status {
	case "connected":
		lc.chat.MCP = MCPConnected
	case MCPBlocked, "failed", "needs-auth", "not loaded", "pending":
		if lc.chat.MCP != MCPConnected {
			lc.chat.MCP = MCPBlocked
			lc.chat.MCPNote = lc.chat.Agent + " did not load Kanivet's MCP server (status: " + status + "), usually because a managed or enterprise MCP policy blocks it. The chat continues without cluster tools: Kanivet inlines the context it gathered, so explanations and code fixes still work. Allow the \"kanivet\" server in that policy to give the agent live cluster access."
		}
	}
	lc.version++
	lc.dirty = true
}

func (m *ChatManager) saveIfDirty(lc *liveChat) {
	lc.mu.Lock()
	if !lc.dirty {
		lc.mu.Unlock()
		return
	}
	lc.dirty = false
	b, _ := json.Marshal(lc.chat)
	lc.mu.Unlock()
	var cp Chat
	_ = json.Unmarshal(b, &cp)
	_ = m.store.Save(&cp)
}

func (m *ChatManager) finishTurn(lc *liveChat, status, errMsg, final string, cost float64) {
	lc.mu.Lock()
	c := lc.chat
	msg := &c.Messages[len(c.Messages)-1]
	if strings.TrimSpace(msg.Text) == "" && final != "" {
		msg.Text = final
		msg.Events = append(msg.Events, Event{Seq: len(msg.Events) + 1, Time: m.now().UnixMilli(), Type: "text", Text: final})
	}
	full := msg.Text
	if final != "" && len(final) > len(full) {
		full = final
	}
	if f, _ := ExtractFindings(full); f != nil {
		fillCluster(f, c.Cluster)
		msg.Findings = f
	}
	msg.Proposals = ExtractProposals(full)
	msg.Status, msg.Error, msg.CostUSD = status, errMsg, cost
	c.Running = false
	c.Updated = m.now()
	lc.version++
	lc.dirty = false
	b, _ := json.Marshal(c)
	lc.mu.Unlock()
	var cp Chat
	_ = json.Unmarshal(b, &cp)
	_ = m.store.Save(&cp)
}

func fillCluster(f *Findings, cluster string) {
	for i := range f.Resources {
		if f.Resources[i].Cluster == "" {
			f.Resources[i].Cluster = cluster
		}
	}
	for i := range f.Findings {
		for j := range f.Findings[i].Resources {
			if f.Findings[i].Resources[j].Cluster == "" {
				f.Findings[i].Resources[j].Cluster = cluster
			}
		}
	}
}

// ---- proposals and code review -------------------------------------------

func (m *ChatManager) withProposal(chatID, msgID, pid string, fn func(c *Chat, p *Proposal) error) (*Proposal, error) {
	var out Proposal
	err := m.mutate(chatID, func(c *Chat) error {
		for i := range c.Messages {
			if c.Messages[i].ID != msgID {
				continue
			}
			for j := range c.Messages[i].Proposals {
				p := &c.Messages[i].Proposals[j]
				if p.ID == pid {
					err := fn(c, p)
					out = *p
					return err
				}
			}
		}
		return errors.New("proposal not found")
	})
	return &out, err
}

// DryRunProposal runs the proposal with dryRun=All and stores the diff.
func (m *ChatManager) DryRunProposal(chatID, msgID, pid string) (*Proposal, error) {
	if m.ops == nil {
		return nil, errors.New("cluster access is not available")
	}
	return m.withProposal(chatID, msgID, pid, func(c *Chat, p *Proposal) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		DryRun(ctx, m.ops, c.Cluster, p)
		return nil
	})
}

// ApplyProposal applies a proposal that passed its dry-run. Only the
// approval button in the UI calls this.
func (m *ChatManager) ApplyProposal(chatID, msgID, pid string) (*Proposal, error) {
	if m.ops == nil {
		return nil, errors.New("cluster access is not available")
	}
	p, err := m.withProposal(chatID, msgID, pid, func(c *Chat, p *Proposal) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = Apply(ctx, m.ops, c.Cluster, p)
		return nil
	})
	if err == nil && p.Status != "applied" {
		return p, errors.New(p.Error)
	}
	return p, err
}

// RejectProposal marks a proposal rejected.
func (m *ChatManager) RejectProposal(chatID, msgID, pid string) (*Proposal, error) {
	return m.withProposal(chatID, msgID, pid, func(_ *Chat, p *Proposal) error {
		p.Status = "rejected"
		return nil
	})
}

func (m *ChatManager) codeWork(id string) (*CodeWork, error) {
	c, _, err := m.Get(id, -1)
	if err != nil {
		return nil, err
	}
	if c.Code == nil {
		return nil, errors.New("this chat is not a code fix")
	}
	return c.Code, nil
}

// Diff returns the review diff of a code chat.
func (m *ChatManager) Diff(id string) ([]FileDiff, *CodeWork, error) {
	w, err := m.codeWork(id)
	if err != nil {
		return nil, nil, err
	}
	files, err := RepoDiff(context.Background(), w)
	return files, w, err
}

// Discard restores the repo (all or some files) to where the agent started.
func (m *ChatManager) Discard(id string, paths []string) ([]string, error) {
	w, err := m.codeWork(id)
	if err != nil {
		return nil, err
	}
	return DiscardChanges(context.Background(), w, paths, false)
}

// Commit commits the agent's changes on an explicit click.
func (m *ChatManager) Commit(id, branch, message string) (string, error) {
	w, err := m.codeWork(id)
	if err != nil {
		return "", err
	}
	sha, err := CommitChanges(context.Background(), w, branch, message)
	if err != nil {
		return "", err
	}
	_ = m.mutate(id, func(c *Chat) error { c.Code = w; return nil })
	return sha, nil
}

// Locations is the code-location store.
func (m *ChatManager) Locations() *LocationStore { return m.locs }

// SuggestFor ranks code locations for a resource, using its GitOps/Helm
// metadata from the cluster.
func (m *ChatManager) SuggestFor(cluster, kind, namespace, name string) (SourceHints, []Suggestion) {
	h := SourceHints{Names: dedupe([]string{name})}
	if m.ops != nil && name != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if obj, err := m.ops.FetchRedacted(ctx, cluster, kind, namespace, name); err == nil {
			var argo, ks, hr [2]string
			h, argo, ks, hr = HintsFromObject(obj)
			if argo[1] != "" {
				for _, ns := range dedupe([]string{argo[0], "argocd", "openshift-gitops", namespace}) {
					if app, err := m.ops.FetchRedacted(ctx, cluster, "applications.argoproj.io", ns, argo[1]); err == nil {
						HintsFromArgoApp(&h, app)
						break
					}
				}
			}
			if ks[1] != "" {
				if k, err := m.ops.FetchRedacted(ctx, cluster, "kustomizations.kustomize.toolkit.fluxcd.io", ks[0], ks[1]); err == nil {
					spec, _ := k["spec"].(map[string]any)
					if p, _ := spec["path"].(string); p != "" {
						h.Paths = append(h.Paths, strings.TrimPrefix(p, "./"))
					}
					ref, _ := spec["sourceRef"].(map[string]any)
					rn, _ := ref["name"].(string)
					rns, _ := ref["namespace"].(string)
					if rns == "" {
						rns = ks[0]
					}
					if rn != "" {
						if g, err := m.ops.FetchRedacted(ctx, cluster, "gitrepositories.source.toolkit.fluxcd.io", rns, rn); err == nil {
							gs, _ := g["spec"].(map[string]any)
							if u, _ := gs["url"].(string); u != "" {
								h.RepoURLs = append(h.RepoURLs, u)
							}
						}
					}
				}
			}
			if hr[1] != "" {
				if r, err := m.ops.FetchRedacted(ctx, cluster, "helmreleases.helm.toolkit.fluxcd.io", hr[0], hr[1]); err == nil {
					spec, _ := r["spec"].(map[string]any)
					ch, _ := spec["chart"].(map[string]any)
					cs, _ := ch["spec"].(map[string]any)
					if c, _ := cs["chart"].(string); c != "" {
						h.Charts = append(h.Charts, c)
					}
				}
			}
		}
	}
	return h, Suggest(m.locs.List(), h)
}
