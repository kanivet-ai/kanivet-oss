package investigate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Chat modes. "ask" is read-only toward the cluster and the disk; "code"
// lets the agent read and edit files inside one repo folder.
const (
	ModeAsk  = "ask"
	ModeCode = "code"
)

// MCP states shown in the chat header.
const (
	MCPConnected = "connected" // the agent loaded Kanivet's read-only MCP server
	MCPBlocked   = "blocked"   // a managed/enterprise policy refused it; context is inlined
	MCPOff       = "off"       // not requested (user choice)
	MCPUnknown   = ""
)

// ContextChip is one piece of context attached to a message (removable in
// the UI before sending).
type ContextChip struct {
	Type       string `json:"type"` // cluster | namespace | resource | finding | yaml | text
	Label      string `json:"label"`
	Cluster    string `json:"cluster,omitempty"`
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	// Text is the gathered content (finding detail, snippet, YAML). It is
	// redacted before it is stored or sent.
	Text string `json:"text,omitempty"`
	// IncludeYAML asks the backend to fetch the live object (redacted).
	IncludeYAML bool `json:"includeYaml,omitempty"`
}

// Message is one chat turn.
type Message struct {
	ID       string        `json:"id"`
	Role     string        `json:"role"` // user | assistant
	Text     string        `json:"text"`
	Context  []ContextChip `json:"context,omitempty"`
	Events   []Event       `json:"events,omitempty"`
	Findings *Findings     `json:"findings,omitempty"`
	Status   string        `json:"status,omitempty"` // assistant: running | done | failed | cancelled
	Error    string        `json:"error,omitempty"`
	CostUSD  float64       `json:"costUsd,omitempty"`
	Time     time.Time     `json:"time"`
	// Proposals are live-cluster patches the agent proposed in this turn.
	Proposals []Proposal `json:"proposals,omitempty"`
}

// CodeWork describes a "fix in code" chat: the repo, branch and the
// starting point the review diff is computed against.
type CodeWork struct {
	Repo             string   `json:"repo"`
	Branch           string   `json:"branch,omitempty"`
	BaseBranch       string   `json:"baseBranch,omitempty"`
	BaseSHA          string   `json:"baseSha"`
	CreatedBranch    bool     `json:"createdBranch,omitempty"`
	DirtyAtStart     []string `json:"dirtyAtStart,omitempty"`
	UntrackedAtStart []string `json:"untrackedAtStart,omitempty"`
	Committed        string   `json:"committed,omitempty"`
}

// Chat is a persisted conversation with an agent CLI.
type Chat struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	AgentID   string    `json:"agentId"`
	Agent     string    `json:"agent"`
	Mode      string    `json:"mode"`
	Cluster   string    `json:"cluster,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	MCP       string    `json:"mcp"`
	MCPNote   string    `json:"mcpNote,omitempty"`
	NoMCP     bool      `json:"noMcp,omitempty"`
	Code      *CodeWork `json:"code,omitempty"`
	Source    string    `json:"source,omitempty"` // entry point, e.g. "audit"
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Messages  []Message `json:"messages"`
	Running   bool      `json:"running"`
}

// Permissions is the human summary of what the agent may do, for the header.
func (c *Chat) Permissions() []string {
	p := []string{"cluster: read-only"}
	switch {
	case c.NoMCP:
		p = append(p, "Kanivet MCP: off (context inlined)")
	case c.MCP == MCPBlocked:
		p = append(p, "Kanivet MCP: blocked by policy (context inlined)")
	default:
		p = append(p, "Kanivet MCP: read-only tools")
	}
	if c.Mode == ModeCode && c.Code != nil {
		p = append(p, "files: read/edit in "+c.Code.Repo+" only", "no shell")
	} else {
		p = append(p, "files: none", "no shell")
	}
	return p
}

// ChatSummary is a list row.
type ChatSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Agent   string    `json:"agent"`
	Mode    string    `json:"mode"`
	Cluster string    `json:"cluster,omitempty"`
	Source  string    `json:"source,omitempty"`
	Updated time.Time `json:"updated"`
	Running bool      `json:"running"`
	Turns   int       `json:"turns"`
}

// ChatStore persists chats as one JSON file each under dir
// (~/.kanivet/ai/chats).
type ChatStore struct {
	dir string
	mu  sync.Mutex
}

// NewChatStore creates dir if needed.
func NewChatStore(dir string) (*ChatStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &ChatStore{dir: dir}, nil
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ErrChatNotFound is returned for unknown chat ids.
var ErrChatNotFound = errors.New("chat not found")

func (s *ChatStore) path(id string) (string, error) {
	if !safeID.MatchString(id) {
		return "", ErrChatNotFound
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// Save writes a chat atomically.
func (s *ChatStore) Save(c *Chat) error {
	p, err := s.path(c.ID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads one chat.
func (s *ChatStore) Load(id string) (*Chat, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrChatNotFound
	}
	if err != nil {
		return nil, err
	}
	var c Chat
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Delete removes a chat.
func (s *ChatStore) Delete(id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, os.ErrNotExist) {
		return ErrChatNotFound
	} else {
		return err
	}
}

// List returns summaries, most recently updated first.
func (s *ChatStore) List() []ChatSummary {
	entries, _ := os.ReadDir(s.dir)
	out := []ChatSummary{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		c, err := s.Load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		turns := 0
		for _, m := range c.Messages {
			if m.Role == "user" {
				turns++
			}
		}
		out = append(out, ChatSummary{ID: c.ID, Title: c.Title, Agent: c.Agent, Mode: c.Mode, Cluster: c.Cluster, Source: c.Source, Updated: c.Updated, Running: c.Running, Turns: turns})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
