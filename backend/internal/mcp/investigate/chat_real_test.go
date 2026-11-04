package investigate

import (
	"os"
	"strings"
	"testing"
)

// TestRealCLIChat drives an installed agent CLI end to end (two turns, the
// second resuming the first). Opt-in: it costs tokens and needs a login.
//
//	KANIVET_REAL_AGENT=claude KANIVET_REAL_MCP_URL=http://127.0.0.1:PORT/mcp-readonly \
//	KANIVET_REAL_MCP_TOKEN=kmcp_... go test ./internal/mcp/investigate -run TestRealCLIChat -v
func TestRealCLIChat(t *testing.T) {
	agent := os.Getenv("KANIVET_REAL_AGENT")
	if agent == "" {
		t.Skip("set KANIVET_REAL_AGENT=claude|codex to run")
	}
	url, tok := os.Getenv("KANIVET_REAL_MCP_URL"), os.Getenv("KANIVET_REAL_MCP_TOKEN")
	m, err := NewChatManager(t.TempDir(), func() (string, string, bool) { return url, tok, url != "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := NewChatInput{Agent: agent, Cluster: "none", Text: "Call the kanivet list_clusters tool if you have it, then answer in one sentence: how many clusters are there? If you have no tools, say so."}
	if repo := os.Getenv("KANIVET_REAL_REPO"); repo != "" {
		in.Mode, in.Repo, in.NewBranch = ModeCode, repo, true
		in.Text = "In values.yaml set the memory limit to 256Mi. Then list the files you changed."
	}
	c, err := m.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	waitIdleLong(t, m, c.ID)
	if err := m.Send(c.ID, "What did I ask you in my previous message? One short sentence.", nil); err != nil {
		t.Fatal(err)
	}
	waitIdleLong(t, m, c.ID)
	got, _, _ := m.Get(c.ID, -1)
	t.Logf("agent=%s mcp=%q note=%q session=%s", got.Agent, got.MCP, got.MCPNote, got.SessionID)
	for _, msg := range got.Messages {
		t.Logf("[%s %s %s] %s", msg.Role, msg.Status, msg.Error, strings.ReplaceAll(msg.Text, "\n", " "))
		for _, e := range msg.Events {
			if e.Type != "text" {
				t.Logf("    %s %s %s", e.Type, e.Tool, e.Input)
			}
		}
	}
	if got.Code != nil {
		files, err := RepoDiff(t.Context(), got.Code)
		t.Logf("branch=%s diff files=%d err=%v", got.Code.Branch, len(files), err)
		for _, f := range files {
			t.Logf("%s %s\n%s", f.Status, f.Path, f.Patch)
		}
	}
}

func waitIdleLong(t *testing.T, m *ChatManager, id string) {
	t.Helper()
	for i := 0; i < 900; i++ {
		c, _, _ := m.Get(id, -1)
		if !c.Running {
			return
		}
		sleepMs(200)
	}
	t.Fatal("turn did not finish")
}
