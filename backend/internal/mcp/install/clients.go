package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Claude Code (CLI: claude mcp add/remove; status from ~/.claude.json)

type claudeCode struct{}

func (claudeCode) id() string    { return "claude-code" }
func (claudeCode) label() string { return "Claude Code" }

// policyBlocks remembers clients whose CLI refused the server because of an
// enterprise policy, so the row keeps saying so after the failed attempt.
var policyBlocks sync.Map // id -> message

var policyMarkers = []string{"enterprise policy", "blocked by policy", "managed policy", "not allowed by your organization"}

func policyMessage(out string) (string, bool) {
	low := strings.ToLower(out)
	for _, m := range policyMarkers {
		if strings.Contains(low, m) {
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(strings.ToLower(line), m) {
					return strings.TrimSpace(line), true
				}
			}
			return strings.TrimSpace(out), true
		}
	}
	return "", false
}

// managedPolicyBlock reads Claude Code's managed settings: a denylist naming
// kanivet, or an allowlist that does not, blocks the server.
func managedPolicyBlock(e Env) (string, bool) {
	paths := e.ManagedSettings
	for _, p := range paths {
		m, _, err := readJSONObject(p)
		if err != nil || len(m) == 0 {
			continue
		}
		names := func(key string) ([]string, bool) {
			arr, ok := m[key].([]any)
			if !ok {
				return nil, false
			}
			var out []string
			for _, a := range arr {
				if o, ok := a.(map[string]any); ok {
					if n, _ := o["serverName"].(string); n != "" {
						out = append(out, n)
					}
				}
			}
			return out, true
		}
		if denied, ok := names("deniedMcpServers"); ok && contains(denied, ServerName) {
			return "MCP server blocked by enterprise policy (deniedMcpServers in " + p + ")", true
		}
		if allowed, ok := names("allowedMcpServers"); ok && !contains(allowed, ServerName) {
			return "MCP server blocked by enterprise policy (not in allowedMcpServers in " + p + ")", true
		}
	}
	return "", false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func claudeJSON(e Env) string { return filepath.Join(e.Home, ".claude.json") }

func (c claudeCode) status(ctx context.Context, e Env, t Target) Status {
	s := Status{Where: "claude mcp add --scope user (" + claudeJSON(e) + ")", CanInstall: true, RestartHint: true}
	if _, err := e.LookPath("claude"); err != nil {
		s.State, s.CanInstall = StateNotDetected, false
		return s
	}
	if msg, ok := managedPolicyBlock(e); ok {
		s.State, s.Detail = StateBlocked, msg
		return s
	}
	if v, ok := policyBlocks.Load(c.id()); ok {
		s.State, s.Detail = StateBlocked, v.(string)
		return s
	}
	entry, err := jsonEntry(claudeJSON(e), "mcpServers")
	if err != nil {
		s.State, s.Detail = StateUnknown, err.Error()
		return s
	}
	return fillFromURLHeaders(s, entry, "url", t)
}

func (claudeCode) plan(e Env, t Target, m Mode, action string) Plan {
	if action == "uninstall" {
		return Plan{Commands: []string{"claude mcp remove --scope user " + ServerName}}
	}
	return Plan{Commands: []string{
		"claude mcp remove --scope user " + ServerName + "   (if present)",
		fmt.Sprintf("claude mcp add --transport http --scope user %s %s --header \"Authorization: Bearer ••••\"", ServerName, t.URL(m)),
	}, Note: "Restart Claude Code sessions to load the server."}
}

func (c claudeCode) install(ctx context.Context, e Env, t Target, m Mode) error {
	bin, err := e.LookPath("claude")
	if err != nil {
		return errors.New("claude CLI not found")
	}
	_, _, _ = e.Run(ctx, bin, "mcp", "remove", "--scope", "user", ServerName)
	out, errOut, err := e.Run(ctx, bin, "mcp", "add", "--transport", "http", "--scope", "user", ServerName, t.URL(m), "--header", "Authorization: "+bearer(t.Token))
	if msg, ok := policyMessage(out + "\n" + errOut); ok {
		policyBlocks.Store(c.id(), msg)
		return fmt.Errorf("blocked by policy: %s", msg)
	}
	if err != nil {
		return fmt.Errorf("claude mcp add: %v: %s", err, strings.TrimSpace(errOut+out))
	}
	policyBlocks.Delete(c.id())
	return nil
}

func (claudeCode) uninstall(ctx context.Context, e Env) error {
	bin, err := e.LookPath("claude")
	if err != nil {
		return errors.New("claude CLI not found")
	}
	out, errOut, err := e.Run(ctx, bin, "mcp", "remove", "--scope", "user", ServerName)
	if err != nil && !strings.Contains(strings.ToLower(out+errOut), "no mcp server") {
		return fmt.Errorf("claude mcp remove: %v: %s", err, strings.TrimSpace(errOut+out))
	}
	return nil
}

// fillFromURLHeaders sets state/mode from an entry shaped {<urlKey>, headers}.
func fillFromURLHeaders(s Status, entry map[string]any, urlKey string, t Target) Status {
	if entry == nil {
		s.State = StateNotInstalled
		return s
	}
	u, _ := entry[urlKey].(string)
	h, _ := entry["headers"].(map[string]any)
	auth, _ := h["Authorization"].(string)
	s.Mode = modeOf(u)
	if current(t, u, strings.TrimPrefix(auth, "Bearer ")) {
		s.State = StateInstalled
	} else {
		s.State = StateOutdated
		s.Detail = outdatedDetail(t, u, strings.TrimPrefix(auth, "Bearer "))
	}
	return s
}

func outdatedDetail(t Target, u, token string) string {
	switch {
	case u != t.URL(modeOf(u)):
		return "points at " + u + " (Kanivet now listens at " + t.URL(modeOf(u)) + ")"
	case token != t.Token:
		return "uses an old token"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Codex (~/.codex/config.toml, [mcp_servers.kanivet])

type codex struct{}

func (codex) id() string    { return "codex" }
func (codex) label() string { return "Codex" }

func codexPath(e Env) string { return filepath.Join(e.Home, ".codex", "config.toml") }

func (codex) status(ctx context.Context, e Env, t Target) Status {
	p := codexPath(e)
	s := Status{Where: p, CanInstall: true, RestartHint: true}
	if _, err := e.LookPath("codex"); err != nil && !dirExists(filepath.Dir(p)) {
		s.State, s.CanInstall = StateNotDetected, false
		return s
	}
	entry, err := readCodexEntry(p)
	if err != nil {
		s.State, s.Detail = StateUnknown, err.Error()
		return s
	}
	if entry == nil {
		s.State = StateNotInstalled
		return s
	}
	u, _ := entry["url"].(string)
	h, _ := entry["http_headers"].(map[string]any)
	auth, _ := h["Authorization"].(string)
	s.Mode = modeOf(u)
	token := strings.TrimPrefix(auth, "Bearer ")
	if current(t, u, token) {
		s.State = StateInstalled
	} else {
		s.State, s.Detail = StateOutdated, outdatedDetail(t, u, token)
	}
	return s
}

func (codex) plan(e Env, t Target, m Mode, action string) Plan {
	if action == "uninstall" {
		return Plan{Files: []string{codexPath(e)}, Backup: true, Note: "Removes the [mcp_servers.kanivet] table; the rest of the file is kept as is."}
	}
	return Plan{Files: []string{codexPath(e)}, Backup: true,
		Note: "Writes [mcp_servers.kanivet] with url " + t.URL(m) + " and the bearer token header; the rest of the file is kept as is. Restart Codex to load it."}
}

func (codex) install(ctx context.Context, e Env, t Target, m Mode) error {
	return editCodex(codexPath(e), &codexServer{URL: t.URL(m), Headers: map[string]string{"Authorization": bearer(t.Token)}}, e.Now())
}

func (codex) uninstall(ctx context.Context, e Env) error {
	return editCodex(codexPath(e), nil, e.Now())
}

// ---------------------------------------------------------------------------
// JSON-file clients: Cursor, Gemini CLI, Windsurf

type jsonClient struct {
	cid, name string
	path      func(Env) string
	entry     entryShape
	cli       string
	restart   bool
	deeplink  func(t Target, m Mode) string
}

type entryShape struct {
	urlKey string
}

func urlHeadersEntry(urlKey string) entryShape { return entryShape{urlKey: urlKey} }

func (j jsonClient) id() string    { return j.cid }
func (j jsonClient) label() string { return j.name }

func (j jsonClient) detected(e Env) bool {
	if dirExists(filepath.Dir(j.path(e))) {
		return true
	}
	if j.cli != "" {
		if _, err := e.LookPath(j.cli); err == nil {
			return true
		}
	}
	return false
}

func (j jsonClient) status(ctx context.Context, e Env, t Target) Status {
	p := j.path(e)
	s := Status{Where: p, CanInstall: true, RestartHint: j.restart}
	if j.deeplink != nil {
		s.Deeplink = j.deeplink(t, ModeReadOnly)
	}
	if !j.detected(e) {
		s.State = StateNotDetected
		return s
	}
	entry, err := jsonEntry(p, "mcpServers")
	if err != nil {
		s.State, s.Detail = StateUnknown, err.Error()
		return s
	}
	return fillFromURLHeaders(s, entry, j.entry.urlKey, t)
}

func (j jsonClient) plan(e Env, t Target, m Mode, action string) Plan {
	p := Plan{Files: []string{j.path(e)}, Backup: fileExists(j.path(e))}
	if action == "uninstall" {
		p.Note = "Removes mcpServers.kanivet; other servers are kept."
	} else {
		p.Note = "Sets mcpServers.kanivet to " + t.URL(m) + " with the bearer token; other servers are kept."
	}
	if j.restart {
		p.Note += " Restart " + j.name + " to apply."
	}
	return p
}

func (j jsonClient) install(ctx context.Context, e Env, t Target, m Mode) error {
	entry := map[string]any{j.entry.urlKey: t.URL(m), "headers": map[string]any{"Authorization": bearer(t.Token)}}
	return editJSONEntry(j.path(e), "mcpServers", entry, e.Now())
}

func (j jsonClient) uninstall(ctx context.Context, e Env) error {
	return editJSONEntry(j.path(e), "mcpServers", nil, e.Now())
}

func cursorPath(e Env) string { return filepath.Join(e.Home, ".cursor", "mcp.json") }
func geminiPath(e Env) string { return filepath.Join(e.Home, ".gemini", "settings.json") }
func windsurfPath(e Env) string {
	return filepath.Join(e.Home, ".codeium", "windsurf", "mcp_config.json")
}

func cursorDeeplink(t Target, m Mode) string {
	cfg, _ := json.Marshal(map[string]any{"url": t.URL(m), "headers": map[string]string{"Authorization": bearer(t.Token)}})
	return "cursor://anysphere.cursor-deeplink/mcp/install?name=" + ServerName + "&config=" + base64.StdEncoding.EncodeToString(cfg)
}

// ---------------------------------------------------------------------------
// VS Code / VS Code Insiders (code --add-mcp, status from the user mcp.json)

type vscode struct{ insiders bool }

func (v vscode) id() string {
	if v.insiders {
		return "vscode-insiders"
	}
	return "vscode"
}

func (v vscode) label() string {
	if v.insiders {
		return "VS Code Insiders"
	}
	return "VS Code"
}

func (v vscode) cli() string {
	if v.insiders {
		return "code-insiders"
	}
	return "code"
}

func (v vscode) userDir(e Env) string {
	app := "Code"
	if v.insiders {
		app = "Code - Insiders"
	}
	switch e.GOOS {
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", app, "User")
	case "windows":
		base := e.AppData
		if base == "" {
			base = filepath.Join(e.Home, "AppData", "Roaming")
		}
		return filepath.Join(base, app, "User")
	}
	return filepath.Join(e.Home, ".config", app, "User")
}

func (v vscode) mcpFile(e Env) string { return filepath.Join(v.userDir(e), "mcp.json") }

func vscodeServer(t Target, m Mode) map[string]any {
	return map[string]any{"type": "http", "url": t.URL(m), "headers": map[string]any{"Authorization": bearer(t.Token)}}
}

func (v vscode) link(t Target, m Mode) string {
	cfg := vscodeServer(t, m)
	cfg["name"] = ServerName
	b, _ := json.Marshal(cfg)
	scheme := "vscode"
	if v.insiders {
		scheme = "vscode-insiders"
	}
	return scheme + ":mcp/install?" + url.QueryEscape(string(b))
}

func (v vscode) status(ctx context.Context, e Env, t Target) Status {
	s := Status{Where: v.cli() + " --add-mcp (" + v.mcpFile(e) + ")", Deeplink: v.link(t, ModeReadOnly), RestartHint: false}
	_, cliErr := e.LookPath(v.cli())
	s.CanInstall = cliErr == nil
	if cliErr != nil && !dirExists(v.userDir(e)) {
		s.State = StateNotDetected
		return s
	}
	entry, err := jsonEntry(v.mcpFile(e), "servers")
	if err != nil {
		s.State, s.Detail = StateUnknown, err.Error()
		return s
	}
	return fillFromURLHeaders(s, entry, "url", t)
}

func (v vscode) plan(e Env, t Target, m Mode, action string) Plan {
	if action == "uninstall" {
		return Plan{Files: []string{v.mcpFile(e)}, Backup: true, Note: "Removes servers.kanivet; other servers are kept."}
	}
	return Plan{Commands: []string{v.cli() + ` --add-mcp '{"name":"kanivet","type":"http","url":"` + t.URL(m) + `","headers":{"Authorization":"Bearer ••••"}}'`}}
}

func (v vscode) install(ctx context.Context, e Env, t Target, m Mode) error {
	bin, err := e.LookPath(v.cli())
	if err != nil {
		return fmt.Errorf("%s CLI not found; use the install link instead", v.cli())
	}
	// --add-mcp does not replace an existing entry: drop ours first.
	if err := editJSONEntry(v.mcpFile(e), "servers", nil, e.Now()); err != nil {
		return err
	}
	cfg := vscodeServer(t, m)
	cfg["name"] = ServerName
	b, _ := json.Marshal(cfg)
	out, errOut, err := e.Run(ctx, bin, "--add-mcp", string(b))
	if err != nil {
		return fmt.Errorf("%s --add-mcp: %v: %s", v.cli(), err, strings.TrimSpace(errOut+out))
	}
	return nil
}

func (v vscode) uninstall(ctx context.Context, e Env) error {
	return editJSONEntry(v.mcpFile(e), "servers", nil, e.Now())
}

// ---------------------------------------------------------------------------
// Claude Desktop (stdio bridge: <backend> mcp-stdio)

type claudeDesktop struct{}

func (claudeDesktop) id() string    { return "claude-desktop" }
func (claudeDesktop) label() string { return "Claude Desktop" }

func claudeDesktopPath(e Env) string {
	switch e.GOOS {
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		base := e.AppData
		if base == "" {
			base = filepath.Join(e.Home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Claude", "claude_desktop_config.json")
	}
	return filepath.Join(e.Home, ".config", "Claude", "claude_desktop_config.json")
}

func (claudeDesktop) status(ctx context.Context, e Env, t Target) Status {
	p := claudeDesktopPath(e)
	s := Status{Where: p, CanInstall: t.BridgeCommand != "", RestartHint: true}
	if !dirExists(filepath.Dir(p)) {
		s.State = StateNotDetected
		return s
	}
	entry, err := jsonEntry(p, "mcpServers")
	if err != nil {
		s.State, s.Detail = StateUnknown, err.Error()
		return s
	}
	if entry == nil {
		s.State = StateNotInstalled
		return s
	}
	cmd, _ := entry["command"].(string)
	args := toStrings(entry["args"])
	s.Mode = ModeReadOnly
	if contains(args, "--full") {
		s.Mode = ModeFull
	}
	if cmd == t.BridgeCommand && len(args) > 0 && args[0] == t.BridgeArgs[0] {
		s.State = StateInstalled
	} else {
		s.State, s.Detail = StateOutdated, "points at "+cmd+" (Kanivet moved or was updated)"
	}
	return s
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (claudeDesktop) plan(e Env, t Target, m Mode, action string) Plan {
	p := Plan{Files: []string{claudeDesktopPath(e)}, Backup: fileExists(claudeDesktopPath(e))}
	if action == "uninstall" {
		p.Note = "Removes mcpServers.kanivet; other servers are kept. Restart Claude Desktop to apply."
		return p
	}
	p.Note = "Adds a stdio server that runs " + t.BridgeCommand + " " + strings.Join(bridgeArgs(t, m), " ") +
		"; it reads the token and port from ~/.kanivet/mcp.json each time, so token rotation does not break it. Restart Claude Desktop to apply."
	return p
}

func bridgeArgs(t Target, m Mode) []string {
	args := append([]string(nil), t.BridgeArgs...)
	if m == ModeFull {
		args = append(args, "--full")
	}
	return args
}

func (claudeDesktop) install(ctx context.Context, e Env, t Target, m Mode) error {
	if t.BridgeCommand == "" {
		return errors.New("the stdio bridge is not available in this build")
	}
	return editJSONEntry(claudeDesktopPath(e), "mcpServers", map[string]any{"command": t.BridgeCommand, "args": bridgeArgs(t, m)}, e.Now())
}

func (claudeDesktop) uninstall(ctx context.Context, e Env) error {
	return editJSONEntry(claudeDesktopPath(e), "mcpServers", nil, e.Now())
}
