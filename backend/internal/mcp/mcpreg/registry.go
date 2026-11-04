// Package mcpreg is the tool registry behind Kanivet's built-in MCP server.
//
// It is a leaf package on purpose: any feature package (helm, topology,
// rbac, certs, ...) can import it and register its own tools with one call
// without creating an import cycle with internal/mcp, which wires the core
// tools and the HTTP endpoints.
//
//	type certArgs struct {
//		mcpreg.ClusterArg
//		Namespace string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
//	}
//
//	mcpreg.Register(mcpreg.Spec{
//		Name:        "list_expiring_certs",
//		Description: "Certificates that expire within 30 days.",
//	}, func(ctx context.Context, call *mcpreg.Call, in certArgs) (any, error) {
//		return certs.Expiring(ctx, call.Cluster, in.Namespace)
//	})
//
// Handlers return any JSON-marshalable value (sent compact) or a string (sent
// verbatim). Returning an error yields an MCP tool error the agent can read.
// Tools registered after the servers are built are added to them live.
package mcpreg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ClusterArg must be embedded in every tool's input struct so every tool
// accepts the optional `cluster` parameter. Register panics otherwise.
type ClusterArg struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"kube context name; defaults to the cluster open in Kanivet (see list_clusters)"`
}

// Spec describes a tool.
type Spec struct {
	// Name is snake_case and unique.
	Name string
	// Title is a short human label (optional).
	Title string
	// Description tells the agent when to use the tool and what it returns.
	Description string
	// Write marks a tool that changes cluster state. Write tools are served
	// only on the full /mcp endpoint, never on /mcp-readonly.
	Write bool
	// Destructive marks a write tool whose effect may not be reversible
	// (delete, drain, rollback, apply). Implies Write.
	Destructive bool
	// Idempotent marks a write that can be repeated safely.
	Idempotent bool
	// Feature groups tools in the UI ("core", "helm", "topology", ...).
	Feature string
	// ClusterOptional lets the tool run with an empty Call.Cluster when no
	// cluster was given and no default could be resolved (list_clusters).
	ClusterOptional bool
}

// Call is what a handler learns about the invocation besides its arguments.
type Call struct {
	// Cluster is the resolved target cluster (never empty when a resolver
	// is configured and succeeded).
	Cluster string
	// Client is the MCP client name from initialize (e.g. "claude-code").
	Client string
	// Endpoint is "readonly" or "full".
	Endpoint string
}

// Handler implements a tool.
type Handler[In any] func(ctx context.Context, call *Call, in In) (any, error)

// Record is one tool invocation, for the live call log.
type Record struct {
	Time       time.Time `json:"time"`
	Client     string    `json:"client"`
	Endpoint   string    `json:"endpoint"`
	Tool       string    `json:"tool"`
	Cluster    string    `json:"cluster,omitempty"`
	Args       string    `json:"args,omitempty"`
	DurationMs int64     `json:"durationMs"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Bytes      int       `json:"bytes"`
	EstTokens  int       `json:"estTokens"`
	Write      bool      `json:"write,omitempty"`
}

type entry struct {
	spec    Spec
	install func(s *mcpsdk.Server, r *Registry, endpoint string)
}

type builtServer struct {
	server   *mcpsdk.Server
	writes   bool
	endpoint string
}

// Registry holds tool definitions and the servers built from them.
type Registry struct {
	mu       sync.RWMutex
	entries  []entry
	byName   map[string]bool
	servers  []builtServer
	resolve  func(ctx context.Context, requested string) (string, error)
	observer func(Record)
}

// New returns an empty registry.
func New() *Registry { return &Registry{byName: map[string]bool{}} }

// Default is the process-wide registry the backend serves.
var Default = New()

// Register adds a tool to the Default registry.
func Register[In any](spec Spec, h Handler[In]) { RegisterOn(Default, spec, h) }

var toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// RegisterOn adds a tool to r. It panics on programmer errors (bad name,
// duplicate, input struct without ClusterArg) so they surface in tests.
func RegisterOn[In any](r *Registry, spec Spec, h Handler[In]) {
	if !toolNameRe.MatchString(spec.Name) {
		panic(fmt.Sprintf("mcpreg: invalid tool name %q", spec.Name))
	}
	if spec.Description == "" {
		panic(fmt.Sprintf("mcpreg: tool %q needs a description", spec.Name))
	}
	if !hasClusterField(reflect.TypeFor[In]()) {
		panic(fmt.Sprintf("mcpreg: tool %q input must embed mcpreg.ClusterArg", spec.Name))
	}
	if spec.Destructive {
		spec.Write = true
	}
	if spec.Feature == "" {
		spec.Feature = "core"
	}
	e := entry{spec: spec}
	e.install = func(s *mcpsdk.Server, reg *Registry, endpoint string) {
		mcpsdk.AddTool(s, toolDef(spec), func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, any, error) {
			return reg.invoke(ctx, spec, endpoint, req, func(ctx context.Context, call *Call) (any, error) {
				return h(ctx, call, in)
			}), nil, nil
		})
	}

	r.mu.Lock()
	if r.byName[spec.Name] {
		r.mu.Unlock()
		panic(fmt.Sprintf("mcpreg: duplicate tool %q", spec.Name))
	}
	r.byName[spec.Name] = true
	r.entries = append(r.entries, e)
	servers := append([]builtServer(nil), r.servers...)
	r.mu.Unlock()

	for _, b := range servers {
		if !spec.Write || b.writes {
			e.install(b.server, r, b.endpoint)
		}
	}
}

func toolDef(spec Spec) *mcpsdk.Tool {
	ann := &mcpsdk.ToolAnnotations{Title: spec.Title}
	closed := false
	ann.OpenWorldHint = &closed
	if spec.Write {
		d := spec.Destructive
		ann.DestructiveHint = &d
		ann.IdempotentHint = spec.Idempotent
	} else {
		ann.ReadOnlyHint = true
	}
	desc := spec.Description
	if spec.Destructive {
		desc = "DESTRUCTIVE: " + desc + " Confirm with the user before calling."
	}
	return &mcpsdk.Tool{Name: spec.Name, Title: spec.Title, Description: desc, Annotations: ann}
}

func hasClusterField(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && (f.Type == reflect.TypeFor[ClusterArg]() || hasClusterField(f.Type)) {
			return true
		}
		if strings.Split(f.Tag.Get("json"), ",")[0] == "cluster" {
			return true
		}
	}
	return false
}

// SetClusterResolver configures how an empty or given `cluster` argument
// becomes the cluster a handler sees.
func (r *Registry) SetClusterResolver(fn func(ctx context.Context, requested string) (string, error)) {
	r.mu.Lock()
	r.resolve = fn
	r.mu.Unlock()
}

// SetObserver receives a Record after every call.
func (r *Registry) SetObserver(fn func(Record)) {
	r.mu.Lock()
	r.observer = fn
	r.mu.Unlock()
}

// Specs lists registered tools sorted by name.
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NewServer builds an MCP server holding every registered tool, or only the
// read-only ones when writes is false. Tools registered later are added too.
func (r *Registry) NewServer(impl *mcpsdk.Implementation, opts *mcpsdk.ServerOptions, writes bool) *mcpsdk.Server {
	s := mcpsdk.NewServer(impl, opts)
	endpoint := "readonly"
	if writes {
		endpoint = "full"
	}
	r.mu.Lock()
	entries := append([]entry(nil), r.entries...)
	r.servers = append(r.servers, builtServer{server: s, writes: writes, endpoint: endpoint})
	r.mu.Unlock()
	for _, e := range entries {
		if !e.spec.Write || writes {
			e.install(s, r, endpoint)
		}
	}
	return s
}

const maxArgsLog = 240

func (r *Registry) invoke(ctx context.Context, spec Spec, endpoint string, req *mcpsdk.CallToolRequest, run func(context.Context, *Call) (any, error)) *mcpsdk.CallToolResult {
	start := time.Now()
	r.mu.RLock()
	resolve, observer := r.resolve, r.observer
	r.mu.RUnlock()

	call := &Call{Endpoint: endpoint, Client: clientName(req)}
	var raw json.RawMessage
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}
	var ca ClusterArg
	_ = json.Unmarshal(raw, &ca)

	var (
		out any
		err error
	)
	call.Cluster = ca.Cluster
	if resolve != nil {
		call.Cluster, err = resolve(ctx, ca.Cluster)
		if err != nil && ca.Cluster == "" && spec.ClusterOptional {
			call.Cluster, err = "", nil
		}
	}
	if err == nil {
		out, err = safeRun(ctx, call, run)
	}

	res := &mcpsdk.CallToolResult{}
	var text string
	if err != nil {
		res.IsError = true
		text = "error: " + err.Error()
	} else {
		text, err = Render(out)
		if err != nil {
			res.IsError = true
			text = "error: " + err.Error()
		}
	}
	res.Content = []mcpsdk.Content{&mcpsdk.TextContent{Text: text}}

	if observer != nil {
		rec := Record{
			Time:       start,
			Client:     call.Client,
			Endpoint:   endpoint,
			Tool:       spec.Name,
			Cluster:    call.Cluster,
			Args:       sanitizeArgs(raw),
			DurationMs: time.Since(start).Milliseconds(),
			OK:         !res.IsError,
			Bytes:      len(text),
			EstTokens:  len(text) / 4,
			Write:      spec.Write,
		}
		if res.IsError {
			rec.Error = truncate(strings.TrimPrefix(text, "error: "), 300)
		}
		observer(rec)
	}
	return res
}

func safeRun(ctx context.Context, call *Call, run func(context.Context, *Call) (any, error)) (out any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	return run(ctx, call)
}

// Render turns a handler result into the text sent to the agent.
func Render(out any) (string, error) {
	switch v := out.(type) {
	case nil:
		return "ok", nil
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return "", errors.New("could not encode result: " + err.Error())
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func clientName(req *mcpsdk.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return "unknown"
	}
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil || p.ClientInfo.Name == "" {
		return "unknown"
	}
	name := p.ClientInfo.Name
	if p.ClientInfo.Version != "" {
		name += " " + p.ClientInfo.Version
	}
	return SanitizeLog(truncate(name, 60))
}

func sanitizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	// Never echo manifests or values bodies into the log.
	for k, v := range m {
		if s, ok := v.(string); ok && len(s) > 80 {
			m[k] = fmt.Sprintf("<%d chars>", len(s))
		}
	}
	b, _ := json.Marshal(m)
	return SanitizeLog(truncate(string(b), maxArgsLog))
}

// SanitizeLog strips control characters so untrusted values cannot forge
// lines in logs or the UI call log.
func SanitizeLog(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
