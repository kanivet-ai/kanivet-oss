# Built-in MCP server

Kanivet serves the [Model Context Protocol](https://modelcontextprotocol.io)
over streamable HTTP on the backend's loopback port (default `53727`):

| Endpoint | Tools |
| --- | --- |
| `http://127.0.0.1:<port>/mcp-readonly` | read tools only |
| `http://127.0.0.1:<port>/mcp` | read tools + write tools (annotated `destructiveHint` where irreversible) |

Both require `Authorization: Bearer <token>`. The token is generated on first
run and stored in `~/.kanivet/mcp.json` (mode 0600) together with the
on/off and "allow writes" switches. Requests from non-loopback addresses are
refused; the SDK's localhost DNS-rebinding protection is on. The endpoints are
mounted before the UI session-secret middleware, since agents do not have that
secret. The UI (AI & MCP settings) shows the URLs, the token and copyable setup
snippets for Claude Code, Codex, Cursor, VS Code and Claude Desktop, plus a
live log of tool calls (`GET /api/v1/mcp/calls`).

Tools called without `cluster` target the cluster open in Kanivet (the UI
reports it via `POST /api/v1/mcp/context`), then the kubeconfig current
context.

## Packages

- `mcpreg/` — the tool registry. Leaf package: any feature can import it.
- `.` (mcp) — core tools, minify/redact, settings/token store, HTTP guard, UI API.
- `investigate/` — runs Claude Code or Codex headless against `/mcp-readonly`
  and streams progress to the UI (`/api/v1/ai/*`).
- `mcpcheck/` — CLI that performs a real handshake against a running backend:
  `go run ./internal/mcp/mcpcheck -tokenfile ~/.kanivet/mcp.json -call list_clusters`.

## Adding tools from a feature (the extension point)

One call, from your own package, with its own service captured in the closure:

```go
import "github.com/kanivet/backend/internal/mcp/mcpreg"

type certArgs struct {
	mcpreg.ClusterArg                  // required: every tool takes optional `cluster`
	Namespace string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
	Days      int    `json:"days,omitempty" jsonschema:"expiry window, default 30"`
}

func RegisterMCPTools(svc *Service) {
	mcpreg.Register(mcpreg.Spec{
		Name:        "list_expiring_certs",
		Feature:     "certs",
		Description: "Certificates expiring within N days, soonest first.",
	}, func(ctx context.Context, call *mcpreg.Call, in certArgs) (any, error) {
		return svc.Expiring(ctx, call.Cluster, in.Namespace, in.Days)
	})
}
```

Call `RegisterMCPTools` from `cmd/main.go` where the service is built (before
or after `mcp.Setup`, both work: tools registered later are added to the live
servers). Rules:

- Return compact, already-summarised data; the result is JSON-encoded without
  indentation (a `string` is sent verbatim). Trim lists and say how many were
  dropped. Run objects through `mcp.Minify` / `mcp.Redact` and free text
  through `mcp.ScrubText` if they can contain secrets. Never return Secret data.
- Set `Write: true` for anything that changes cluster state (served only on
  `/mcp`), `Destructive: true` if it cannot be undone.
- Return errors; they become tool errors the agent can read. Panics are
  recovered into tool errors.
- Input structs get their JSON schema from field tags (`json`, `jsonschema`);
  fields without `omitempty` are required.

## Core tools

Read: `list_clusters`, `list_resources`, `get_resource`, `get_events`,
`get_logs`, `search`, `get_incidents`, `diagnose`, `list_helm_releases`,
`get_helm_release`, `list_argo_apps`, `get_argo_app`, `get_metrics`,
`get_costs`, `get_rightsizing`.

Write (`/mcp` only): `restart_workload`, `scale_workload`, `delete_pod`,
`apply_yaml`, `helm_rollback`, `argo_sync`, `cordon_node`, `drain_node`.

## Token efficiency

- `Minify` drops managedFields, last-applied annotations, uid/resourceVersion,
  defaulted pod fields, default tolerations, probe timestamps, node image lists
  and every empty value.
- `list_resources` returns one-line rows (`Summarize`) instead of objects and
  reads the watcher cache when a watch is warm.
- `get_events` folds events that differ only in ids/counters (`DedupeEvents`).
- `get_logs` collapses repeated lines, filters by regex or error-likeness,
  scrubs secrets and samples the unhealthiest pods of a workload first.
- `diagnose` replaces the typical 5–8 call sequence with one.
