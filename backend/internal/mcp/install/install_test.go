package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeExec struct {
	bins  map[string]bool
	calls [][]string
	reply func(args []string) (string, string, error)
}

func (f *fakeExec) env(home, goos string) Env {
	return Env{
		Home: home, GOOS: goos,
		LookPath: func(n string) (string, error) {
			if f.bins[n] {
				return "/fake/bin/" + n, nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(ctx context.Context, name string, args ...string) (string, string, error) {
			f.calls = append(f.calls, append([]string{filepath.Base(name)}, args...))
			if f.reply != nil {
				return f.reply(args)
			}
			return "", "", nil
		},
		Now:             func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) },
		ManagedSettings: []string{filepath.Join(home, "managed-settings.json")},
	}
}

var target = Target{
	ReadOnlyURL: "http://127.0.0.1:53727/mcp-readonly", FullURL: "http://127.0.0.1:53727/mcp", Token: "tok-1",
	BridgeCommand: "/Applications/Kanivet.app/Contents/Resources/kanivet-backend", BridgeArgs: []string{"mcp-stdio"},
}

func statusOf(t *testing.T, m *Manager, id string, tg Target) Status {
	t.Helper()
	for _, s := range m.Statuses(context.Background(), tg) {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no status for %s", id)
	return Status{}
}

func write(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func backups(t *testing.T, p string) []string {
	t.Helper()
	got, _ := filepath.Glob(p + ".bak-kanivet-*")
	return got
}

func TestNothingDetected(t *testing.T) {
	m := New((&fakeExec{}).env(t.TempDir(), "darwin"))
	for _, s := range m.Statuses(context.Background(), target) {
		if s.State != StateNotDetected {
			t.Errorf("%s: %s, want not_detected", s.ID, s.State)
		}
	}
}

func TestCursorInstallMergeBackupModeAndOutdated(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".cursor", "mcp.json")
	write(t, p, `{"mcpServers":{"other":{"command":"x"}},"theme":"dark"}`, 0o644)
	m := New((&fakeExec{}).env(home, "darwin"))
	ctx := context.Background()

	if s := statusOf(t, m, "cursor", target); s.State != StateNotInstalled || !strings.HasPrefix(s.Deeplink, "cursor://anysphere.cursor-deeplink/mcp/install?name=kanivet&config=") {
		t.Fatalf("before: %+v", s)
	}
	s, err := m.Install(ctx, "cursor", target, ModeReadOnly)
	if err != nil || s.State != StateInstalled || s.Mode != ModeReadOnly {
		t.Fatalf("install: %+v %v", s, err)
	}
	doc := readJSON(t, p)
	servers := doc["mcpServers"].(map[string]any)
	if servers["other"] == nil || doc["theme"] != "dark" {
		t.Fatalf("other entries clobbered: %v", doc)
	}
	k := servers["kanivet"].(map[string]any)
	if k["url"] != target.ReadOnlyURL || k["headers"].(map[string]any)["Authorization"] != "Bearer tok-1" {
		t.Fatalf("entry: %v", k)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode changed to %v", fi.Mode().Perm())
	}
	if len(backups(t, p)) != 1 {
		t.Fatal("no backup written")
	}

	rotated := target
	rotated.Token = "tok-2"
	if s := statusOf(t, m, "cursor", rotated); s.State != StateOutdated || !strings.Contains(s.Detail, "token") {
		t.Fatalf("after rotation: %+v", s)
	}
	moved := target
	moved.ReadOnlyURL, moved.FullURL = "http://127.0.0.1:60000/mcp-readonly", "http://127.0.0.1:60000/mcp"
	if s := statusOf(t, m, "cursor", moved); s.State != StateOutdated || !strings.Contains(s.Detail, "60000") {
		t.Fatalf("after port change: %+v", s)
	}
	sts, err := m.UpdateOutdated(ctx, rotated)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sts {
		if s.ID == "cursor" && s.State != StateInstalled {
			t.Fatalf("update all: %+v", s)
		}
	}

	if _, err := m.Install(ctx, "cursor", rotated, ModeFull); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(t, m, "cursor", rotated); s.Mode != ModeFull || s.State != StateInstalled {
		t.Fatalf("full: %+v", s)
	}
	if _, err := m.Uninstall(ctx, "cursor", rotated); err != nil {
		t.Fatal(err)
	}
	doc = readJSON(t, p)
	servers = doc["mcpServers"].(map[string]any)
	if servers["kanivet"] != nil || servers["other"] == nil {
		t.Fatalf("uninstall: %v", doc)
	}
}

func TestJSONWithCommentsRefused(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".gemini", "settings.json")
	orig := "{\n  // my settings\n  \"theme\": \"x\"\n}\n"
	write(t, p, orig, 0o600)
	m := New((&fakeExec{}).env(home, "linux"))
	if _, err := m.Install(context.Background(), "gemini", target, ModeReadOnly); err == nil {
		t.Fatal("expected refusal for a JSONC file")
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Fatal("file modified")
	}
}

func TestWindsurfCreatesFilePrivately(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codeium", "windsurf"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New((&fakeExec{}).env(home, "linux"))
	if _, err := m.Install(context.Background(), "windsurf", target, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	if fi, _ := os.Stat(p); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("new file mode: %v", fi)
	}
	k := readJSON(t, p)["mcpServers"].(map[string]any)["kanivet"].(map[string]any)
	if k["serverUrl"] != target.ReadOnlyURL {
		t.Fatalf("%v", k)
	}
}

const codexOrig = `# my codex config
model = "o3"

[mcp_servers.other]
command = "npx"
args = ["-y", "other"] # inline comment

[profiles.work]
model = "gpt-5"
`

func TestCodexTOMLEditPreservesEverythingElse(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".codex", "config.toml")
	write(t, p, codexOrig, 0o600)
	m := New((&fakeExec{bins: map[string]bool{"codex": true}}).env(home, "darwin"))
	ctx := context.Background()

	if _, err := m.Install(ctx, "codex", target, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if !strings.HasPrefix(got, strings.TrimRight(codexOrig, "\n")) {
		t.Fatalf("original content not kept verbatim:\n%s", got)
	}
	if !strings.Contains(got, "# my codex config") || !strings.Contains(got, "# inline comment") {
		t.Fatal("comments lost")
	}
	if s := statusOf(t, m, "codex", target); s.State != StateInstalled || s.Mode != ModeReadOnly {
		t.Fatalf("status: %+v\n%s", s, got)
	}
	// Re-install with the full endpoint replaces the table instead of adding one.
	if _, err := m.Install(ctx, "codex", target, ModeFull); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if n := strings.Count(string(b), "[mcp_servers.kanivet]"); n != 1 {
		t.Fatalf("%d kanivet tables:\n%s", n, b)
	}
	if s := statusOf(t, m, "codex", target); s.Mode != ModeFull {
		t.Fatalf("mode: %+v", s)
	}
	if len(backups(t, p)) == 0 {
		t.Fatal("no backup")
	}
	if _, err := m.Uninstall(ctx, "codex", target); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "kanivet") || !strings.Contains(string(b), "[profiles.work]") || !strings.Contains(string(b), "[mcp_servers.other]") {
		t.Fatalf("uninstall:\n%s", b)
	}
}

func TestClaudeCodeUsesCLIAndReportsPolicyBlock(t *testing.T) {
	home := t.TempDir()
	policyBlocks.Delete("claude-code")
	defer policyBlocks.Delete("claude-code")
	fe := &fakeExec{bins: map[string]bool{"claude": true}}
	fe.reply = func(args []string) (string, string, error) {
		if len(args) > 1 && args[1] == "add" {
			// What the CLI writes on success.
			write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"kanivet":{"type":"http","url":"`+target.ReadOnlyURL+`","headers":{"Authorization":"Bearer tok-1"}}}}`, 0o600)
		}
		return "", "", nil
	}
	m := New(fe.env(home, "darwin"))
	ctx := context.Background()
	if s := statusOf(t, m, "claude-code", target); s.State != StateNotInstalled {
		t.Fatalf("before: %+v", s)
	}
	s, err := m.Install(ctx, "claude-code", target, ModeReadOnly)
	if err != nil || s.State != StateInstalled {
		t.Fatalf("install: %+v %v", s, err)
	}
	want := []string{"claude", "mcp", "add", "--transport", "http", "--scope", "user", "kanivet", target.ReadOnlyURL, "--header", "Authorization: Bearer tok-1"}
	if len(fe.calls) != 2 || strings.Join(fe.calls[0][:4], " ") != "claude mcp remove --scope" || strings.Join(fe.calls[1], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("calls: %q", fe.calls)
	}

	// Enterprise policy refusal: blocked, explained, not retried around.
	fe.reply = func(args []string) (string, string, error) {
		if len(args) > 1 && args[1] == "add" {
			return "", "Error: MCP server blocked by enterprise policy\n", errors.New("exit status 1")
		}
		return "", "", nil
	}
	s, err = m.Install(ctx, "claude-code", target, ModeReadOnly)
	if err == nil || s.State != StateBlocked || !strings.Contains(s.Detail, "enterprise policy") {
		t.Fatalf("policy: %+v %v", s, err)
	}
}

func TestClaudeCodeManagedPolicyFile(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "managed-settings.json"), `{"allowedMcpServers":[{"serverName":"github"}]}`, 0o644)
	m := New((&fakeExec{bins: map[string]bool{"claude": true}}).env(home, "darwin"))
	if s := statusOf(t, m, "claude-code", target); s.State != StateBlocked || !strings.Contains(s.Detail, "allowedMcpServers") {
		t.Fatalf("%+v", s)
	}
}

func TestVSCodeCLIAndLink(t *testing.T) {
	home := t.TempDir()
	fe := &fakeExec{bins: map[string]bool{"code": true}}
	m := New(fe.env(home, "linux"))
	if _, err := m.Install(context.Background(), "vscode", target, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || fe.calls[0][0] != "code" || fe.calls[0][1] != "--add-mcp" {
		t.Fatalf("calls: %q", fe.calls)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(fe.calls[0][2]), &cfg); err != nil || cfg["name"] != "kanivet" || cfg["url"] != target.ReadOnlyURL || cfg["type"] != "http" {
		t.Fatalf("payload %v %v", cfg, err)
	}
	// Insiders: no CLI but the user dir exists -> link only.
	if err := os.MkdirAll(filepath.Join(home, ".config", "Code - Insiders", "User"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := statusOf(t, m, "vscode-insiders", target)
	if s.State != StateNotInstalled || s.CanInstall || !strings.HasPrefix(s.Deeplink, "vscode-insiders:mcp/install?") {
		t.Fatalf("insiders: %+v", s)
	}
	// Status reads the user mcp.json.
	write(t, filepath.Join(home, ".config", "Code", "User", "mcp.json"), `{"servers":{"kanivet":{"type":"http","url":"`+target.FullURL+`","headers":{"Authorization":"Bearer tok-1"}}}}`, 0o600)
	if s := statusOf(t, m, "vscode", target); s.State != StateInstalled || s.Mode != ModeFull {
		t.Fatalf("vscode: %+v", s)
	}
}

func TestClaudeDesktopStdioBridgeWindowsPath(t *testing.T) {
	home := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	if err := os.MkdirAll(filepath.Join(appData, "Claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := (&fakeExec{}).env(home, "windows")
	env.AppData = appData
	m := New(env)
	if _, err := m.Install(context.Background(), "claude-desktop", target, ModeReadOnly); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(appData, "Claude", "claude_desktop_config.json")
	k := readJSON(t, p)["mcpServers"].(map[string]any)["kanivet"].(map[string]any)
	if k["command"] != target.BridgeCommand || k["args"].([]any)[0] != "mcp-stdio" {
		t.Fatalf("%v", k)
	}
	// The bridge reads the token at runtime: rotation does not make it stale.
	rotated := target
	rotated.Token = "tok-9"
	if s := statusOf(t, m, "claude-desktop", rotated); s.State != StateInstalled {
		t.Fatalf("after rotation: %+v", s)
	}
	moved := target
	moved.BridgeCommand = "/elsewhere/kanivet-backend"
	if s := statusOf(t, m, "claude-desktop", moved); s.State != StateOutdated {
		t.Fatalf("moved: %+v", s)
	}
}

func TestPlanNamesFilesAndHidesToken(t *testing.T) {
	m := New((&fakeExec{}).env(t.TempDir(), "darwin"))
	for _, id := range []string{"claude-code", "codex", "cursor", "vscode", "claude-desktop", "gemini", "windsurf"} {
		p, err := m.Plan(id, target, ModeReadOnly, "install")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Files)+len(p.Commands) == 0 {
			t.Errorf("%s: empty plan", id)
		}
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), target.Token) {
			t.Errorf("%s: plan leaks the token", id)
		}
	}
}
