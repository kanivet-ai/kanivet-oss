package investigate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TurnSpec is everything needed to build one agent CLI invocation.
type TurnSpec struct {
	Agent     Agent
	Mode      string // ModeAsk | ModeCode
	Prompt    string
	SessionID string // claude: --session-id on the first turn, --resume after
	Resume    bool
	// Workdir is the process cwd: the repo in code mode, a scratch dir
	// otherwise.
	Workdir string
	// ScratchDir holds the generated MCP config (never the repo).
	ScratchDir string
	MCPURL     string
	MCPToken   string
	UseMCP     bool
}

// Built-in tools allowed in code mode. No Bash, no web, no notebook, no
// sub-agents: read and edit files only, and the CLI confines those to the
// working directory.
var claudeCodeTools = []string{"Read", "Edit", "Write", "Glob", "Grep"}

// ClaudeArgs builds the claude CLI arguments for a chat turn.
//
// Safety: --restricted removes every built-in tool that runs commands or
// code (Bash, PowerShell, ...) and WebFetch, confines the file tools to the
// working directories and refuses bypassPermissions. --tools then picks the
// exact built-ins (none in ask mode). --permission-prompts none denies
// anything that would need approval. --strict-mcp-config ignores the user's
// other MCP servers.
func ClaudeArgs(t TurnSpec, mcpConfigPath string) []string {
	args := []string{
		"-p", t.Prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--restricted",
		"--permission-prompts", "none",
		"--strict-mcp-config",
	}
	if t.Resume {
		args = append(args, "--resume", t.SessionID)
	} else if t.SessionID != "" {
		args = append(args, "--session-id", t.SessionID)
	}
	allowed := []string{}
	if t.Mode == ModeCode {
		args = append(args, "--tools", strings.Join(claudeCodeTools, ","), "--permission-mode", "acceptEdits")
		allowed = append(allowed, claudeCodeTools...)
	} else {
		args = append(args, "--tools", "", "--permission-mode", "dontAsk")
	}
	if t.UseMCP && mcpConfigPath != "" {
		args = append(args, "--mcp-config", mcpConfigPath)
		allowed = append(allowed, "mcp__kanivet__*")
	}
	if len(allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowed, ","))
	}
	args = append(args, "--disallowedTools", strings.Join(claudeDisallowedFor(t.Mode), ","))
	return args
}

func claudeDisallowedFor(mode string) []string {
	if mode != ModeCode {
		return claudeDisallowed
	}
	keep := map[string]bool{}
	for _, t := range claudeCodeTools {
		keep[t] = true
	}
	var out []string
	for _, t := range claudeDisallowed {
		if !keep[t] {
			out = append(out, t)
		}
	}
	return out
}

// CodexArgs builds the codex CLI arguments for a chat turn. The sandbox is
// read-only in ask mode and workspace-write (cwd = the repo, no network) in
// code mode; approvals are "never" so nothing escalates silently.
func CodexArgs(t TurnSpec) []string {
	sandbox := "read-only"
	if t.Mode == ModeCode {
		sandbox = "workspace-write"
	}
	var args []string
	if t.Resume {
		args = []string{"exec", "resume", "--json", "--skip-git-repo-check"}
	} else {
		args = []string{"exec", "--json", "--skip-git-repo-check", "-C", t.Workdir}
	}
	args = append(args,
		"-c", fmt.Sprintf("sandbox_mode=%q", sandbox),
		"-c", `approval_policy="never"`,
	)
	if t.Mode == ModeCode {
		args = append(args, "-c", "sandbox_workspace_write.network_access=false")
	}
	if t.UseMCP {
		args = append(args,
			"-c", fmt.Sprintf("mcp_servers.kanivet.url=%q", t.MCPURL),
			"-c", `mcp_servers.kanivet.bearer_token_env_var="KANIVET_MCP_TOKEN"`,
		)
	}
	if t.Resume {
		args = append(args, t.SessionID)
	}
	return append(args, t.Prompt)
}

// writeMCPConfig writes the claude --mcp-config file into scratch.
func writeMCPConfig(scratch, url, token string) (string, error) {
	cfg := map[string]any{"mcpServers": map[string]any{"kanivet": map[string]any{
		"type": "http", "url": url, "headers": map[string]string{"Authorization": "Bearer " + token},
	}}}
	b, _ := json.Marshal(cfg)
	p := filepath.Join(scratch, "mcp.json")
	return p, os.WriteFile(p, b, 0o600)
}
