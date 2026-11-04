package investigate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- persistence ----------------------------------------------------------

func TestChatStoreRoundTripListRenameDelete(t *testing.T) {
	st, err := NewChatStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := &Chat{ID: "a1", Title: "old", Updated: time.Now().Add(-time.Hour), Messages: []Message{{Role: "user", Text: "hi"}}}
	nw := &Chat{ID: "b2", Title: "new", Updated: time.Now()}
	for _, c := range []*Chat{old, nw} {
		if err := st.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Load("a1")
	if err != nil || got.Title != "old" || got.Messages[0].Text != "hi" {
		t.Fatalf("load: %+v %v", got, err)
	}
	l := st.List()
	if len(l) != 2 || l[0].ID != "b2" || l[1].Turns != 1 {
		t.Fatalf("list: %+v", l)
	}
	if _, err := st.Load("../etc/passwd"); err != ErrChatNotFound {
		t.Fatal("path traversal id accepted")
	}
	if err := st.Delete("a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load("a1"); err != ErrChatNotFound {
		t.Fatal("deleted chat still loads")
	}
}

// ---- prompt and redaction --------------------------------------------------

func TestRedactText(t *testing.T) {
	in := `apiVersion: v1
kind: Secret
metadata:
  name: db
data:
  password: c2VjcmV0
  user: YWRtaW4=
type: Opaque
---
env:
- name: DB_PASSWORD
  value: hunter2
- name: LOG_LEVEL
  value: debug
- name: API_TOKEN
  valueFrom:
    secretKeyRef:
      name: tok
token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijk
`
	out := RedactText(in, nil)
	for _, leak := range []string{"c2VjcmV0", "YWRtaW4=", "hunter2", "eyJhbGci"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"LOG_LEVEL", "value: debug", "name: tok", "type: Opaque"} {
		if !strings.Contains(out, keep) {
			t.Errorf("over-redacted %q:\n%s", keep, out)
		}
	}
}

func TestBuildChatPrompt(t *testing.T) {
	chips := []ContextChip{
		{Type: "resource", Kind: "Deployment", APIVersion: "apps/v1", Namespace: "prod", Name: "web"},
		{Type: "finding", Label: "Audit finding: no memory limit", Text: "container app has no limits\npassword: sup3r"},
	}
	p := BuildChatPrompt("Fix it", chips, PromptOpts{Mode: ModeCode, Cluster: "kind-dev", UseMCP: true, First: true, Repo: "/r"}, nil)
	for _, want := range []string{"source repository", `cluster="kind-dev"`, "Deployment (apps/v1) prod/web", "Audit finding", "kanivet-patch", "Repository: /r", "User: Fix it"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "sup3r") {
		t.Error("prompt leaks a password from a chip")
	}
	// Without MCP the agent is told to rely on the inlined context.
	p2 := BuildChatPrompt("why", nil, PromptOpts{Mode: ModeAsk, Cluster: "c", UseMCP: false, First: true}, nil)
	if !strings.Contains(p2, "no cluster tools") || strings.Contains(p2, "pass cluster=") {
		t.Errorf("fallback prompt: %s", p2)
	}
	// Follow-ups carry only the new message.
	p3 := BuildChatPrompt("and now?", nil, PromptOpts{Mode: ModeAsk, UseMCP: true}, nil)
	if p3 != "User: and now?" {
		t.Errorf("follow-up prompt: %q", p3)
	}
}

func TestExtractProposals(t *testing.T) {
	txt := "Raise it.\n```kanivet-patch\n{\"summary\":\"mem\",\"kind\":\"Deployment\",\"namespace\":\"p\",\"name\":\"w\",\"patchType\":\"strategic\",\"patch\":{\"spec\":{}}}\n```\nbad:\n```kanivet-patch\n{\"kind\":\"\"}\n```"
	ps := ExtractProposals(txt)
	if len(ps) != 1 || ps[0].Name != "w" || ps[0].Status != "proposed" || string(ps[0].Patch) != `{"spec":{}}` {
		t.Fatalf("%+v", ps)
	}
}

type fakeOps struct {
	patched []bool // dryRun flags seen
	fail    bool
}

func (f *fakeOps) FetchRedacted(_ context.Context, _, kind, ns, name string) (map[string]any, error) {
	return map[string]any{"kind": kind, "metadata": map[string]any{"name": name, "namespace": ns,
		"labels": map[string]any{"argocd.argoproj.io/instance": "shop"}}}, nil
}
func (f *fakeOps) PatchLive(_ context.Context, _, kind, ns, name, _ string, _ []byte, dry bool) (map[string]any, map[string]any, error) {
	f.patched = append(f.patched, dry)
	if f.fail {
		return map[string]any{"kind": kind}, nil, fmt.Errorf("admission denied")
	}
	return map[string]any{"kind": kind, "spec": map[string]any{"replicas": 1}}, map[string]any{"kind": kind, "spec": map[string]any{"replicas": 3}}, nil
}
func (f *fakeOps) Scrub(s string) string { return s }

func TestProposalNeedsDryRunBeforeApply(t *testing.T) {
	ops := &fakeOps{}
	p := &Proposal{Kind: "Deployment", Name: "w", Status: "proposed", Patch: []byte(`{}`)}
	if err := Apply(context.Background(), ops, "c", p); err != ErrNeedsDryRun || len(ops.patched) != 0 {
		t.Fatal("apply without dry-run must be refused")
	}
	DryRun(context.Background(), ops, "c", p)
	if p.Status != "dry-run-ok" || !strings.Contains(p.After, "replicas: 3") || !ops.patched[0] {
		t.Fatalf("dry-run: %+v %v", p, ops.patched)
	}
	if err := Apply(context.Background(), ops, "c", p); err != nil || p.Status != "applied" || ops.patched[1] {
		t.Fatalf("apply: %+v %v", p, ops.patched)
	}
	ops2 := &fakeOps{fail: true}
	p2 := &Proposal{Kind: "Deployment", Name: "w", Patch: []byte(`{}`)}
	DryRun(context.Background(), ops2, "c", p2)
	if p2.Status != "dry-run-failed" || p2.Error == "" {
		t.Fatalf("failed dry-run: %+v", p2)
	}
}

// ---- CLI arguments: the restricted tool sets ------------------------------

func TestClaudeArgsRestricted(t *testing.T) {
	ask := strings.Join(ClaudeArgs(TurnSpec{Mode: ModeAsk, Prompt: "p", SessionID: "s1", UseMCP: true}, "/tmp/mcp.json"), " ")
	for _, want := range []string{"--restricted", "--permission-prompts none", "--strict-mcp-config", "--session-id s1", "--tools  ", "--allowedTools mcp__kanivet__*", "--mcp-config /tmp/mcp.json", "--include-partial-messages", "--permission-mode dontAsk"} {
		if !strings.Contains(ask, want) {
			t.Errorf("ask args lack %q: %s", want, ask)
		}
	}
	if strings.Contains(ask, "--allowedTools Read") || strings.Contains(ask, "bypassPermissions") {
		t.Errorf("ask mode allows edits: %s", ask)
	}
	code := ClaudeArgs(TurnSpec{Mode: ModeCode, Prompt: "p", SessionID: "s1", Resume: true, UseMCP: true}, "/tmp/mcp.json")
	joined := strings.Join(code, " ")
	for _, want := range []string{"--restricted", "--resume s1", "--tools Read,Edit,Write,Glob,Grep", "--allowedTools Read,Edit,Write,Glob,Grep,mcp__kanivet__*", "--permission-mode acceptEdits"} {
		if !strings.Contains(joined, want) {
			t.Errorf("code args lack %q: %s", want, joined)
		}
	}
	for i, a := range code {
		if a == "--disallowedTools" {
			dis := code[i+1]
			if !strings.Contains(dis, "Bash") || !strings.Contains(dis, "WebFetch") || strings.Contains(","+dis+",", ",Edit,") || strings.Contains(","+dis+",", ",Read,") {
				t.Errorf("code disallowed list wrong: %s", dis)
			}
		}
		if a == "--allowedTools" && strings.Contains(code[i+1], "Bash") {
			t.Error("Bash allowed in code mode")
		}
	}
	noMCP := strings.Join(ClaudeArgs(TurnSpec{Mode: ModeCode, Prompt: "p", UseMCP: false}, ""), " ")
	if strings.Contains(noMCP, "--mcp-config") || strings.Contains(noMCP, "mcp__kanivet") {
		t.Errorf("MCP configured although off: %s", noMCP)
	}
}

func TestCodexArgsSandbox(t *testing.T) {
	ask := strings.Join(CodexArgs(TurnSpec{Mode: ModeAsk, Prompt: "p", Workdir: "/w", UseMCP: true, MCPURL: "http://x"}), " ")
	if !strings.Contains(ask, `sandbox_mode="read-only"`) || !strings.Contains(ask, "-C /w") || !strings.Contains(ask, `approval_policy="never"`) {
		t.Errorf("codex ask: %s", ask)
	}
	code := CodexArgs(TurnSpec{Mode: ModeCode, Prompt: "p", Workdir: "/repo", SessionID: "th1", Resume: true})
	j := strings.Join(code, " ")
	if !strings.HasPrefix(j, "exec resume --json") || !strings.Contains(j, `sandbox_mode="workspace-write"`) || !strings.Contains(j, "network_access=false") || code[len(code)-2] != "th1" || strings.Contains(j, "danger") {
		t.Errorf("codex code resume: %s", j)
	}
}

// ---- repo mapping ----------------------------------------------------------

func TestNormalizeRepoURL(t *testing.T) {
	want := "github.com/acme/shop"
	for _, u := range []string{"git@github.com:acme/shop.git", "https://github.com/acme/shop", "ssh://git@github.com/acme/shop.git", "https://GitHub.com/acme/shop/"} {
		if got := NormalizeRepoURL(u); got != want {
			t.Errorf("%s -> %s", u, got)
		}
	}
	if got := NormalizeRepoURL("ssh://git@gitlab.local:2222/a/b.git"); got != "gitlab.local/a/b" {
		t.Errorf("port: %s", got)
	}
}

func TestSuggestRanking(t *testing.T) {
	locs := []CodeLocation{
		{Path: "/src/shop-api", Name: "shop-api"},
		{Path: "/src/charts", Name: "charts", Charts: []string{"redis"}},
		{Path: "/src/gitops", Name: "gitops", Remotes: []string{"git@github.com:acme/deploy.git"}},
	}
	h := SourceHints{RepoURLs: []string{"https://github.com/acme/deploy"}, Paths: []string{"apps/shop"}, Charts: []string{"redis"}, Names: []string{"shop"}, Manager: "argocd"}
	s := Suggest(locs, h)
	if len(s) != 3 || s[0].Location.Name != "gitops" || s[0].SubPath != "apps/shop" || s[1].Location.Name != "charts" || s[2].Location.Name != "shop-api" {
		t.Fatalf("%+v", s)
	}
}

func TestHintsFromObjectAndArgo(t *testing.T) {
	obj := map[string]any{"metadata": map[string]any{"name": "web-1", "labels": map[string]any{
		"helm.sh/chart": "redis-17.3.2", "app.kubernetes.io/instance": "cache",
	}, "annotations": map[string]any{"argocd.argoproj.io/tracking-id": "argocd_shop:apps/Deployment:prod/web"}}}
	h, argo, _, _ := HintsFromObject(obj)
	if argo != [2]string{"argocd", "shop"} || h.Manager != "argocd" || h.Charts[0] != "redis" || !containsFold(h.Names, "cache") {
		t.Fatalf("%+v %v", h, argo)
	}
	HintsFromArgoApp(&h, map[string]any{"spec": map[string]any{"sources": []any{map[string]any{"repoURL": "https://x/y", "path": "k8s"}}}})
	if h.RepoURLs[0] != "https://x/y" || h.Paths[0] != "k8s" {
		t.Fatalf("%+v", h)
	}
}

func TestLocationStoreAddInspects(t *testing.T) {
	repo := newRepo(t)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/deploy.git")
	os.MkdirAll(filepath.Join(repo, "charts", "web"), 0o755)
	os.WriteFile(filepath.Join(repo, "charts", "web", "Chart.yaml"), []byte("apiVersion: v2\nname: web\nversion: 1.0.0\n"), 0o644)
	s := NewLocationStore(filepath.Join(t.TempDir(), "locs.json"))
	loc, err := s.Add(repo)
	if err != nil || len(loc.Remotes) != 1 || len(loc.Charts) != 1 || loc.Charts[0] != "web" {
		t.Fatalf("%+v %v", loc, err)
	}
	if _, err := s.Add(repo); err != nil || len(s.List()) != 1 {
		t.Fatal("re-adding duplicated the entry")
	}
	_ = s.Remove(repo)
	if len(s.List()) != 0 {
		t.Fatal("remove failed")
	}
}

// ---- git diff / discard / commit -----------------------------------------

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@x")
	runGit(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte("replicas: 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("memory: 128Mi\n"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestPrepareRefusesDirtyUnlessConfirmedOrBranch(t *testing.T) {
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "deploy.yaml"), []byte("replicas: 2\n"), 0o644)
	if _, _, err := PrepareRepo(context.Background(), repo, PrepareOpts{}); err != ErrDirty {
		t.Fatalf("want ErrDirty, got %v", err)
	}
	w, _, err := PrepareRepo(context.Background(), repo, PrepareOpts{NewBranch: true, AllowDirty: true, Slug: "Fix memory limit!"})
	if err != nil || w.Branch != "kanivet/fix-fix-memory-limit" || !w.CreatedBranch || len(w.DirtyAtStart) != 1 {
		t.Fatalf("%+v %v", w, err)
	}
	if b := strings.TrimSpace(runGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); b != w.Branch {
		t.Fatalf("on %s", b)
	}
	if _, _, err := PrepareRepo(context.Background(), t.TempDir(), PrepareOpts{}); err != ErrNotGitRepo {
		t.Fatalf("non-repo: %v", err)
	}
}

func TestRepoDiffDiscardAndCommit(t *testing.T) {
	repo := newRepo(t)
	// The user's own pending edit, confirmed before starting.
	os.WriteFile(filepath.Join(repo, "deploy.yaml"), []byte("replicas: 5\n"), 0o644)
	w, _, err := PrepareRepo(context.Background(), repo, PrepareOpts{AllowDirty: true})
	if err != nil {
		t.Fatal(err)
	}
	// The agent's edits.
	os.WriteFile(filepath.Join(repo, "values.yaml"), []byte("memory: 512Mi\n"), 0o644)
	os.MkdirAll(filepath.Join(repo, "k8s"), 0o755)
	os.WriteFile(filepath.Join(repo, "k8s", "pdb.yaml"), []byte("kind: PodDisruptionBudget\n"), 0o644)

	files, err := RepoDiff(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileDiff{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	v := byPath["values.yaml"]
	if v.Status != "modified" || v.Original != "memory: 128Mi\n" || v.Modified != "memory: 512Mi\n" || !strings.Contains(v.Patch, "+memory: 512Mi") {
		t.Fatalf("values diff: %+v", v)
	}
	if byPath["k8s/pdb.yaml"].Status != "added" || !byPath["deploy.yaml"].Preexisting {
		t.Fatalf("files: %+v", files)
	}

	// Discard one file, then the rest: the user's own edit survives.
	if done, err := DiscardChanges(context.Background(), w, []string{"values.yaml"}, false); err != nil || len(done) != 1 {
		t.Fatalf("%v %v", done, err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "values.yaml")); string(b) != "memory: 128Mi\n" {
		t.Fatalf("values not restored: %s", b)
	}
	os.WriteFile(filepath.Join(repo, "values.yaml"), []byte("memory: 256Mi\n"), 0o644)
	sha, err := CommitChanges(context.Background(), w, "kanivet/fix-mem", "fix: memory")
	if err != nil || sha == "" || w.Branch != "kanivet/fix-mem" {
		t.Fatalf("commit: %s %v", sha, err)
	}
	show := runGit(t, repo, "show", "--name-only", "--format=%s", "HEAD")
	if !strings.Contains(show, "values.yaml") || !strings.Contains(show, "k8s/pdb.yaml") || strings.Contains(show, "deploy.yaml") {
		t.Fatalf("commit content: %s", show)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "deploy.yaml")); string(b) != "replicas: 5\n" {
		t.Fatal("user's pending edit was touched")
	}

	// Discard everything after a new edit + new file.
	w2, _, _ := PrepareRepo(context.Background(), repo, PrepareOpts{AllowDirty: true})
	os.WriteFile(filepath.Join(repo, "values.yaml"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("n\n"), 0o644)
	os.Remove(filepath.Join(repo, "k8s", "pdb.yaml"))
	if _, err := DiscardChanges(context.Background(), w2, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new file not removed")
	}
	if _, err := os.Stat(filepath.Join(repo, "k8s", "pdb.yaml")); err != nil {
		t.Fatal("deleted file not restored")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "deploy.yaml")); string(b) != "replicas: 5\n" {
		t.Fatal("discard touched the user's pending edit")
	}
}

func TestInsideRepo(t *testing.T) {
	if insideRepo("/r", "../x") || insideRepo("/r", "a/../../x") || !insideRepo("/r", "a/b.yaml") {
		t.Fatal("insideRepo")
	}
}

// ---- manager: multi-turn with a stub CLI ----------------------------------

const stubTurn1 = `{"type":"system","subtype":"init","session_id":"S","mcp_servers":[{"name":"kanivet","status":"failed"}]}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Looking "}}}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"at web."}}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at web."},{"type":"tool_use","name":"Read","input":{"file_path":"values.yaml"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"memory: 128Mi","is_error":false}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.01,"result":"Looking at web.\n` + "```kanivet-patch" + `\n{\"kind\":\"Deployment\",\"namespace\":\"prod\",\"name\":\"web\",\"patch\":{\"spec\":{\"replicas\":3}}}\n` + "```" + `"}
`

func TestHelperChatProcess(t *testing.T) {
	if os.Getenv("KANIVET_CHAT_HELPER") != "1" {
		return
	}
	fmt.Print(stubTurn1)
	os.Exit(0)
}

func TestChatManagerMultiTurnResumeAndFallback(t *testing.T) {
	m, err := NewChatManager(t.TempDir(), func() (string, string, bool) { return "http://127.0.0.1:1/mcp-readonly", "tok", true }, &fakeOps{})
	if err != nil {
		t.Fatal(err)
	}
	m.detect = func() []Agent { return []Agent{{ID: "claude", Name: "Claude Code", Available: true, Path: os.Args[0]}} }
	var specs []TurnSpec
	m.spawn = func(ctx context.Context, ts TurnSpec) (*exec.Cmd, error) {
		specs = append(specs, ts)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperChatProcess")
		cmd.Env = append(os.Environ(), "KANIVET_CHAT_HELPER=1")
		return cmd, nil
	}
	c, err := m.Create(NewChatInput{Cluster: "kind-dev", Text: "Why is web failing?", Source: "audit",
		Context: []ContextChip{{Type: "resource", Kind: "Deployment", Namespace: "prod", Name: "web", IncludeYAML: true}}})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, m, c.ID)
	got, _, _ := m.Get(c.ID, -1)
	a := got.Messages[1]
	if a.Status != "done" || a.Text != "Looking at web." || len(a.Proposals) != 1 || got.MCP != MCPBlocked || got.MCPNote == "" {
		t.Fatalf("turn 1: %+v mcp=%s", a, got.MCP)
	}
	if !strings.Contains(got.Messages[0].Context[0].Text, "argocd.argoproj.io/instance") {
		t.Fatal("resource YAML not gathered into the context chip")
	}
	if len(a.Events) != 3 || a.Events[0].Type != "text" || a.Events[1].Tool != "Read" {
		t.Fatalf("events: %+v", a.Events)
	}
	// Follow-up resumes the same session and drops MCP after the block.
	if err := m.Send(c.ID, "and the fix?", nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, m, c.ID)
	if len(specs) != 2 || specs[0].Resume || !specs[1].Resume || specs[1].SessionID != specs[0].SessionID || specs[1].UseMCP {
		t.Fatalf("specs: %+v", specs)
	}
	if !specs[0].UseMCP || !strings.Contains(specs[0].Prompt, "User: Why is web failing?") {
		t.Fatalf("first prompt: %s", specs[0].Prompt)
	}
	// Persisted: a fresh manager on the same dir sees both turns.
	m2, _ := NewChatManager(filepath.Dir(m.store.dir), m.endpoint, nil)
	got2, _, err := m2.Get(c.ID, -1)
	if err != nil || len(got2.Messages) != 4 || got2.Running {
		t.Fatalf("reload: %+v %v", got2, err)
	}
	// Proposal flow through the manager.
	p, err := m.DryRunProposal(c.ID, a.ID, "p1")
	if err != nil || p.Status != "dry-run-ok" {
		t.Fatalf("dry-run %+v %v", p, err)
	}
	if err := m.Rename(c.ID, "Web memory"); err != nil || m.List()[0].Title != "Web memory" {
		t.Fatal("rename")
	}
	if err := m.Delete(c.ID); err != nil || len(m.List()) != 0 {
		t.Fatal("delete")
	}
}

func waitIdle(t *testing.T, m *ChatManager, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, _, _ := m.Get(id, -1)
		if !c.Running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("turn did not finish")
}
