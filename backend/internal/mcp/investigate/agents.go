// Package investigate runs an installed coding-agent CLI (Claude Code or
// Codex) headless against Kanivet's read-only MCP endpoint and streams what it
// does into the UI. No API keys are involved: the agent uses whatever login
// the user already has for that CLI.
package investigate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Agent is a detected agent CLI.
type Agent struct {
	ID        string `json:"id"`   // "claude" | "codex"
	Name      string `json:"name"` // display name
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Available bool   `json:"available"`
	Install   string `json:"install"` // how to install when missing
}

var knownAgents = []Agent{
	{ID: "claude", Name: "Claude Code", Install: "npm install -g @anthropic-ai/claude-code"},
	{ID: "codex", Name: "Codex", Install: "npm install -g @openai/codex"},
}

// searchPath is PATH plus the places these CLIs install to. A GUI-launched
// app on macOS inherits a minimal PATH that misses most of them.
func searchPath() string {
	home, _ := os.UserHomeDir()
	extra := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".claude", "local"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, "bin"),
	}
	if runtime.GOOS == "windows" {
		if ad := os.Getenv("APPDATA"); ad != "" {
			extra = append(extra, filepath.Join(ad, "npm"))
		}
	} else {
		extra = append(extra, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	}
	// nvm: newest installed node's bin.
	if entries, err := os.ReadDir(filepath.Join(home, ".nvm", "versions", "node")); err == nil && len(entries) > 0 {
		extra = append(extra, filepath.Join(home, ".nvm", "versions", "node", entries[len(entries)-1].Name(), "bin"))
	}
	parts := filepath.SplitList(os.Getenv("PATH"))
	seen := map[string]bool{}
	var out []string
	for _, p := range append(parts, extra...) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func lookPath(name string) string {
	for _, dir := range filepath.SplitList(searchPath()) {
		cands := []string{filepath.Join(dir, name)}
		if runtime.GOOS == "windows" {
			cands = []string{filepath.Join(dir, name+".exe"), filepath.Join(dir, name+".cmd"), filepath.Join(dir, name+".bat")}
		}
		for _, c := range cands {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0) {
				return c
			}
		}
	}
	return ""
}

var (
	detectMu  sync.Mutex
	detectAt  time.Time
	detectRes []Agent
)

// DetectAgents reports which agent CLIs are installed (cached for 30s).
func DetectAgents() []Agent {
	detectMu.Lock()
	defer detectMu.Unlock()
	if time.Since(detectAt) < 30*time.Second && detectRes != nil {
		return append([]Agent(nil), detectRes...)
	}
	out := make([]Agent, 0, len(knownAgents))
	for _, a := range knownAgents {
		a.Path = lookPath(a.ID)
		if a.Path != "" {
			a.Available = true
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, a.Path, "--version")
			cmd.Env = childEnv(nil)
			if b, err := cmd.Output(); err == nil {
				a.Version = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
			}
			cancel()
		}
		out = append(out, a)
	}
	detectRes, detectAt = out, time.Now()
	return append([]Agent(nil), out...)
}

func childEnv(extra map[string]string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		// Never leak the UI session secret to a child process.
		if strings.EqualFold(k, "PATH") || k == "KANIVET_SESSION_SECRET" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PATH="+searchPath())
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
