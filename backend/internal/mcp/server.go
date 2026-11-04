// Package mcp serves Kanivet's built-in MCP server: streamable-HTTP endpoints
// on the backend's loopback port, guarded by a per-install bearer token.
//
//	/mcp-readonly  read tools only
//	/mcp           read tools + write tools (marked destructive where they are)
//
// Tools live in the mcpreg registry; see README.md for how a feature adds its own.
package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/helm"
	"github.com/kanivet/backend/internal/incidents"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/watcher"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/metrics"
	"github.com/kanivet/backend/internal/nodeload"
	"github.com/kanivet/backend/internal/rightsizing"
	"github.com/kanivet/backend/internal/search"
)

// Deps are the backend services the core tools read from. Nil members turn
// the matching tools into clear "not available" errors.
type Deps struct {
	K8s         *k8s.Client
	Watcher     *watcher.Service
	Search      *search.Service
	Incidents   *incidents.Service
	Helm        *helm.Service
	FinOps      *finops.Service
	Rightsizing *rightsizing.Service
	Metrics     *metrics.Service
	NodeLoad    *nodeload.Service
	// Router is the backend's own HTTP handler, used in-process for actions
	// that only exist as handlers (Argo sync goes through Argo's API proxy).
	Router http.Handler
	// Version is reported in the MCP initialize result.
	Version string
}

// Service is the MCP subsystem.
type Service struct {
	deps  Deps
	store *Store
	reg   *mcpreg.Registry
	port  atomic.Int64

	ro, full http.Handler
}

var registerOnce sync.Once

const instructions = `Kanivet is a Kubernetes desktop app; these tools read the user's clusters through Kanivet's caches.
Start with list_clusters if unsure which cluster to use; every tool takes an optional "cluster" (defaults to the one open in Kanivet).
Prefer diagnose for "why is X broken" (one call: health, owners, related, events, logs). Use get_incidents for a cluster-wide view of what is failing, search to find resources by name/label, list_resources for filtered inventories, get_resource for full (minified, secret-redacted) YAML.
Responses are compact JSON. Secret values are always redacted. Write tools exist only on /mcp, are marked destructive, and must be confirmed with the user first.`

// New builds the service, registers the core tools on mcpreg.Default and
// loads (or creates) the token in stateDir (normally ~/.kanivet).
func New(stateDir string, deps Deps) (*Service, error) {
	store, err := OpenStore(stateDir)
	if err != nil {
		return nil, err
	}
	s := &Service{deps: deps, store: store, reg: mcpreg.Default}
	registerOnce.Do(func() { registerCoreTools(s.reg, &s.deps) })
	s.syncExecTool()
	s.reg.SetObserver(store.Observe)
	s.reg.SetClusterResolver(s.resolveCluster)

	impl := &mcpsdk.Implementation{Name: "kanivet", Title: "Kanivet", Version: orDefault(deps.Version, "dev")}
	opts := &mcpsdk.ServerOptions{Instructions: instructions}
	roSrv := s.reg.NewServer(impl, opts, false)
	fullSrv := s.reg.NewServer(impl, opts, true)
	hopts := &mcpsdk.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute}
	s.ro = mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return roSrv }, hopts)
	s.full = mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return fullSrv }, hopts)
	return s, nil
}

// DefaultStateDir is ~/.kanivet.
func DefaultStateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kanivet")
}

// Store exposes settings, call log and active cluster.
func (s *Service) Store() *Store { return s.store }

// SetPort records the port the backend listens on, for endpoint URLs.
func (s *Service) SetPort(p int) {
	s.port.Store(int64(p))
	if s.store.Settings().Port != p {
		_, _ = s.store.Update(func(st *Settings) { st.Port = p })
	}
}

// BaseURL is http://127.0.0.1:<port>.
func (s *Service) BaseURL() string {
	p := s.port.Load()
	if p == 0 {
		p = 53727
	}
	return fmt.Sprintf("http://127.0.0.1:%d", p)
}

// ReadOnlyURL / FullURL are the endpoint URLs agents connect to.
func (s *Service) ReadOnlyURL() string { return s.BaseURL() + "/mcp-readonly" }
func (s *Service) FullURL() string     { return s.BaseURL() + "/mcp" }

// Token is the current bearer token.
func (s *Service) Token() string { return s.store.Settings().Token }

// Mount registers /mcp and /mcp-readonly on router. Call it before the
// session-secret middleware: agents authenticate with the bearer token.
func (s *Service) Mount(router gin.IRoutes) {
	router.Any("/mcp", gin.WrapH(s.guard(s.full, true)))
	router.Any("/mcp-readonly", gin.WrapH(s.guard(s.ro, false)))
}

// Handler returns the guarded handler for one endpoint (tests).
func (s *Service) Handler(full bool) http.Handler {
	if full {
		return s.guard(s.full, true)
	}
	return s.guard(s.ro, false)
}

func (s *Service) guard(next http.Handler, full bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "kanivet mcp: loopback only", http.StatusForbidden)
			return
		}
		set := s.store.Settings()
		if !set.Enabled {
			http.Error(w, "kanivet mcp: disabled in Kanivet settings (AI & MCP)", http.StatusServiceUnavailable)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(set.Token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kanivet"`)
			http.Error(w, "kanivet mcp: missing or invalid bearer token (see Kanivet > AI & MCP)", http.StatusUnauthorized)
			return
		}
		if full && !set.AllowWrites {
			http.Error(w, "kanivet mcp: write tools are turned off; use /mcp-readonly or enable writes in Kanivet", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Service) resolveCluster(_ context.Context, requested string) (string, error) {
	if s.deps.K8s == nil {
		return requested, nil
	}
	clusters, err := s.deps.K8s.ListClusters()
	if err != nil {
		return "", err
	}
	known := map[string]bool{}
	for _, c := range clusters {
		known[c.Name] = true
	}
	if requested != "" {
		if known[requested] {
			return requested, nil
		}
		// Accept a unique suffix match (EKS ARNs are long).
		var match []string
		for _, c := range clusters {
			if strings.HasSuffix(c.Name, "/"+requested) || strings.HasSuffix(c.Name, ":"+requested) {
				match = append(match, c.Name)
			}
		}
		if len(match) == 1 {
			return match[0], nil
		}
		return "", fmt.Errorf("unknown cluster %q; call list_clusters for valid names", requested)
	}
	if a := s.store.ActiveCluster(); a != "" && known[a] {
		return a, nil
	}
	if cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load(); err == nil && known[cfg.CurrentContext] {
		return cfg.CurrentContext, nil
	}
	if len(clusters) == 1 {
		return clusters[0].Name, nil
	}
	return "", fmt.Errorf("no cluster given and none open in Kanivet; pass cluster (see list_clusters)")
}

// ---------------------------------------------------------------------------
// REST API for the UI (behind the session secret)

// Status is GET /api/v1/mcp/status.
type Status struct {
	Enabled       bool          `json:"enabled"`
	AllowWrites   bool          `json:"allowWrites"`
	AllowExec     bool          `json:"allowExec"`
	ReadOnlyURL   string        `json:"readOnlyUrl"`
	FullURL       string        `json:"fullUrl"`
	Token         string        `json:"token"`
	ActiveCluster string        `json:"activeCluster,omitempty"`
	LastCallAt    *time.Time    `json:"lastCallAt,omitempty"`
	ActiveClients []string      `json:"activeClients"`
	Tools         []ToolInfo    `json:"tools"`
	Setup         []SetupRecipe `json:"setup"`
	TokenFile     string        `json:"tokenFile"`
}

// ToolInfo describes a tool for the UI.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Write       bool   `json:"write"`
	Destructive bool   `json:"destructive"`
	Feature     string `json:"feature"`
}

// SetupRecipe is one copyable setup snippet.
type SetupRecipe struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // "command" | "json" | "toml"
	Where    string `json:"where,omitempty"`
	Snippet  string `json:"snippet"`
	ReadOnly bool   `json:"readOnly"`
}

// GetStatus builds the status payload.
func (s *Service) GetStatus() Status {
	set := s.store.Settings()
	last, clients := s.store.Activity(2 * time.Minute)
	st := Status{
		Enabled:       set.Enabled,
		AllowWrites:   set.AllowWrites,
		AllowExec:     set.AllowExec,
		ReadOnlyURL:   s.ReadOnlyURL(),
		FullURL:       s.FullURL(),
		Token:         set.Token,
		ActiveCluster: s.store.ActiveCluster(),
		ActiveClients: clients,
		TokenFile:     s.store.path,
	}
	if st.ActiveClients == nil {
		st.ActiveClients = []string{}
	}
	sort.Strings(st.ActiveClients)
	if !last.IsZero() {
		st.LastCallAt = &last
	}
	for _, sp := range s.reg.Specs() {
		st.Tools = append(st.Tools, ToolInfo{Name: sp.Name, Description: strings.TrimSuffix(sp.Description, ReasonsDoc), Write: sp.Write, Destructive: sp.Destructive, Feature: sp.Feature})
	}
	st.Setup = SetupRecipes(s.ReadOnlyURL(), s.FullURL(), set.Token)
	return st
}

// SetupRecipes returns copyable setup snippets for common agents.
func SetupRecipes(roURL, fullURL, token string) []SetupRecipe {
	var out []SetupRecipe
	for _, v := range []struct {
		ro  bool
		url string
		sfx string
	}{{false, fullURL, ""}, {true, roURL, "-readonly"}} {
		name := "kanivet" + v.sfx
		auth := "Bearer " + token
		out = append(out,
			SetupRecipe{ID: "claude-code" + v.sfx, Label: "Claude Code", Kind: "command", ReadOnly: v.ro,
				Snippet: fmt.Sprintf(`claude mcp add --transport http --scope user %s %s --header "Authorization: %s"`, name, v.url, auth)},
			SetupRecipe{ID: "codex" + v.sfx, Label: "Codex", Kind: "toml", ReadOnly: v.ro, Where: "~/.codex/config.toml",
				Snippet: fmt.Sprintf("[mcp_servers.%s]\nurl = %q\nhttp_headers = { \"Authorization\" = %q }\n", strings.ReplaceAll(name, "-", "_"), v.url, auth)},
			SetupRecipe{ID: "cursor" + v.sfx, Label: "Cursor", Kind: "json", ReadOnly: v.ro, Where: "~/.cursor/mcp.json",
				Snippet: mustJSON(map[string]any{"mcpServers": map[string]any{name: map[string]any{"url": v.url, "headers": map[string]string{"Authorization": auth}}}})},
			SetupRecipe{ID: "vscode" + v.sfx, Label: "VS Code", Kind: "command", ReadOnly: v.ro,
				Snippet: "code --add-mcp '" + compactJSON(map[string]any{"name": name, "type": "http", "url": v.url, "headers": map[string]string{"Authorization": auth}}) + "'"},
			SetupRecipe{ID: "claude-desktop" + v.sfx, Label: "Claude Desktop", Kind: "json", ReadOnly: v.ro, Where: "claude_desktop_config.json (Settings > Developer > Edit Config)",
				Snippet: mustJSON(map[string]any{"mcpServers": map[string]any{name: map[string]any{
					"command": "npx",
					"args":    []string{"-y", "mcp-remote", v.url, "--header", "Authorization:${KANIVET_AUTH}"},
					"env":     map[string]string{"KANIVET_AUTH": auth},
				}}})},
		)
	}
	return out
}

// CursorDeeplink is a cursor:// install link for the read-only endpoint.
func CursorDeeplink(url, token string) string {
	cfg := compactJSON(map[string]any{"url": url, "headers": map[string]string{"Authorization": "Bearer " + token}})
	return "cursor://anysphere.cursor-deeplink/mcp/install?name=kanivet&config=" + base64.StdEncoding.EncodeToString([]byte(cfg))
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// RegisterAPI adds the UI-facing routes under /api/v1/mcp.
func (s *Service) RegisterAPI(v1 *gin.RouterGroup) {
	g := v1.Group("/mcp")
	s.registerInstallAPI(g)
	g.GET("/status", func(c *gin.Context) { c.JSON(http.StatusOK, s.GetStatus()) })
	g.GET("/calls", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"calls": s.store.Calls(200)})
	})
	g.PUT("/settings", func(c *gin.Context) {
		var body struct {
			Enabled     *bool `json:"enabled"`
			AllowWrites *bool `json:"allowWrites"`
			AllowExec   *bool `json:"allowExec"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := s.store.Update(func(st *Settings) {
			if body.Enabled != nil {
				st.Enabled = *body.Enabled
			}
			if body.AllowWrites != nil {
				st.AllowWrites = *body.AllowWrites
			}
			if body.AllowExec != nil {
				st.AllowExec = *body.AllowExec
			}
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		s.syncExecTool()
		c.JSON(http.StatusOK, s.GetStatus())
	})
	g.POST("/token/rotate", func(c *gin.Context) {
		if _, err := s.store.Update(func(st *Settings) { st.Token = NewToken() }); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, s.GetStatus())
	})
	g.POST("/context", func(c *gin.Context) {
		var body struct {
			Cluster string `json:"cluster"`
		}
		_ = c.ShouldBindJSON(&body)
		s.store.SetActiveCluster(body.Cluster)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

// syncExecTool registers exec_pod when the setting is on and removes it
// (from the live servers too) when it is off.
func (s *Service) syncExecTool() {
	on := s.store.Settings().AllowExec
	switch {
	case on && !s.reg.Has(execToolName):
		registerExecTool(s.reg, &s.deps)
	case !on && s.reg.Has(execToolName):
		s.reg.Unregister(execToolName)
	}
}
