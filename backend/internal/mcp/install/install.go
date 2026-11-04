// Package install adds Kanivet's MCP server to agent clients (Claude Code,
// Codex, Cursor, VS Code, Claude Desktop, Gemini CLI, Windsurf) with one
// click, and reports whether each client has it, with which endpoint, and
// whether the entry is still current (port, token).
//
// File-based clients are edited in place: the file is backed up first
// (<file>.bak-kanivet-<ts>), only the "kanivet" entry is touched, the write
// is atomic and the file keeps its mode. CLI-based clients go through their
// own CLI. A client whose policy refuses the server (Claude Code's
// enterprise policy) is reported as blocked; Kanivet never works around it.
package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ServerName is the entry name used in every client.
const ServerName = "kanivet"

// State of a client.
type State string

const (
	StateNotDetected  State = "not_detected"  // client not found on this machine
	StateNotInstalled State = "not_installed" // client found, no kanivet entry
	StateInstalled    State = "installed"     // entry present and current
	StateOutdated     State = "outdated"      // entry present, wrong port/token/path
	StateBlocked      State = "blocked"       // client policy refuses the server
	StateUnknown      State = "unknown"       // could not read the client's config
)

// Mode is which endpoint an entry points at.
type Mode string

const (
	ModeReadOnly Mode = "readonly"
	ModeFull     Mode = "full"
)

// Target is what an entry should contain.
type Target struct {
	ReadOnlyURL string
	FullURL     string
	Token       string
	// BridgeCommand/BridgeArgs launch the stdio bridge (Claude Desktop).
	BridgeCommand string
	BridgeArgs    []string
}

// URL returns the endpoint for mode.
func (t Target) URL(m Mode) string {
	if m == ModeFull {
		return t.FullURL
	}
	return t.ReadOnlyURL
}

// Status is one client's row in the UI.
type Status struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  State  `json:"state"`
	Mode   Mode   `json:"mode,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Where is the file or command the install changes.
	Where string `json:"where"`
	// RestartHint is set when the client must be restarted to pick up a change.
	RestartHint bool `json:"restartHint,omitempty"`
	// Deeplink installs through the client itself (Cursor, VS Code) when
	// the CLI/file route is not available.
	Deeplink string `json:"deeplink,omitempty"`
	// CanInstall is false when only the deeplink (or nothing) can be used.
	CanInstall bool `json:"canInstall"`
}

// Plan is what an install or removal will change, for the confirm popover.
type Plan struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"` // install | uninstall
	Files    []string `json:"files,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Backup   bool     `json:"backup,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Env abstracts the machine so tests can use a temp HOME and a fake exec.
type Env struct {
	Home    string
	GOOS    string
	AppData string // %APPDATA% on Windows
	// LookPath finds a CLI; Run executes one and returns stdout, stderr.
	LookPath func(name string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
	Now      func() time.Time
	// ManagedSettings are Claude Code's managed-settings files (policy).
	ManagedSettings []string
}

// DefaultEnv is the real machine.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:            home,
		GOOS:            runtime.GOOS,
		AppData:         os.Getenv("APPDATA"),
		LookPath:        lookPath,
		Run:             runCommand,
		Now:             time.Now,
		ManagedSettings: managedSettingsPaths(runtime.GOOS),
	}
}

func managedSettingsPaths(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"/Library/Application Support/ClaudeCode/managed-settings.json"}
	case "windows":
		return []string{`C:\ProgramData\ClaudeCode\managed-settings.json`}
	}
	return []string{"/etc/claude-code/managed-settings.json"}
}

func runCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// lookPath also checks the usual user-local bin dirs, which a GUI app's
// PATH often lacks.
func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat"}
	}
	for _, dir := range []string{filepath.Join(home, ".local", "bin"), "/usr/local/bin", "/opt/homebrew/bin", filepath.Join(home, ".npm-global", "bin")} {
		for _, ext := range exts {
			p := filepath.Join(dir, name+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

// client is one supported agent.
type client interface {
	id() string
	label() string
	status(ctx context.Context, e Env, t Target) Status
	plan(e Env, t Target, m Mode, action string) Plan
	install(ctx context.Context, e Env, t Target, m Mode) error
	uninstall(ctx context.Context, e Env) error
}

// Manager installs and inspects clients.
type Manager struct {
	env     Env
	clients []client
}

// New returns a manager for env.
func New(env Env) *Manager {
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.GOOS == "" {
		env.GOOS = runtime.GOOS
	}
	return &Manager{env: env, clients: []client{
		claudeCode{},
		codex{},
		jsonClient{cid: "cursor", name: "Cursor", path: cursorPath, entry: urlHeadersEntry("url"), cli: "cursor", restart: true, deeplink: cursorDeeplink},
		vscode{insiders: false},
		vscode{insiders: true},
		claudeDesktop{},
		jsonClient{cid: "gemini", name: "Gemini CLI", path: geminiPath, entry: urlHeadersEntry("httpUrl"), cli: "gemini", restart: true},
		jsonClient{cid: "windsurf", name: "Windsurf", path: windsurfPath, entry: urlHeadersEntry("serverUrl"), restart: true},
	}}
}

func (m *Manager) find(id string) (client, error) {
	for _, c := range m.clients {
		if c.id() == id {
			return c, nil
		}
	}
	return nil, fmt.Errorf("unknown client %q", id)
}

// Statuses reports every client.
func (m *Manager) Statuses(ctx context.Context, t Target) []Status {
	out := make([]Status, 0, len(m.clients))
	for _, c := range m.clients {
		s := c.status(ctx, m.env, t)
		s.ID, s.Label = c.id(), c.label()
		out = append(out, s)
	}
	return out
}

// Plan describes what Install/Uninstall would change.
func (m *Manager) Plan(id string, t Target, mode Mode, action string) (Plan, error) {
	c, err := m.find(id)
	if err != nil {
		return Plan{}, err
	}
	p := c.plan(m.env, t, normMode(mode), action)
	p.ID, p.Action = id, action
	return p, nil
}

// Install adds or replaces the client's kanivet entry.
func (m *Manager) Install(ctx context.Context, id string, t Target, mode Mode) (Status, error) {
	c, err := m.find(id)
	if err != nil {
		return Status{}, err
	}
	if err := c.install(ctx, m.env, t, normMode(mode)); err != nil {
		s := c.status(ctx, m.env, t)
		s.ID, s.Label = c.id(), c.label()
		return s, err
	}
	s := c.status(ctx, m.env, t)
	s.ID, s.Label = c.id(), c.label()
	return s, nil
}

// Uninstall removes the client's kanivet entry.
func (m *Manager) Uninstall(ctx context.Context, id string, t Target) (Status, error) {
	c, err := m.find(id)
	if err != nil {
		return Status{}, err
	}
	err = c.uninstall(ctx, m.env)
	s := c.status(ctx, m.env, t)
	s.ID, s.Label = c.id(), c.label()
	return s, err
}

// UpdateOutdated re-installs every outdated entry with its current mode.
func (m *Manager) UpdateOutdated(ctx context.Context, t Target) ([]Status, error) {
	var errs []error
	for _, s := range m.Statuses(ctx, t) {
		if s.State != StateOutdated {
			continue
		}
		if _, err := m.Install(ctx, s.ID, t, s.Mode); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Label, err))
		}
	}
	return m.Statuses(ctx, t), errors.Join(errs...)
}

func normMode(m Mode) Mode {
	if m == ModeFull {
		return ModeFull
	}
	return ModeReadOnly
}

// modeOf infers the mode from a URL.
func modeOf(url string) Mode {
	if strings.HasSuffix(strings.TrimRight(url, "/"), "/mcp") {
		return ModeFull
	}
	return ModeReadOnly
}

// current reports whether url/token match the target for the URL's mode.
func current(t Target, url, token string) bool {
	return url == t.URL(modeOf(url)) && token == t.Token
}

func bearer(token string) string { return "Bearer " + token }

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
