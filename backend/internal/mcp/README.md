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

Read: `list_clusters`, `list_namespaces`, `list_resources`, `get_resource`,
`get_events`, `get_logs`, `get_workload_logs`, `search`, `get_incidents`,
`diagnose`, `get_dashboard`, `list_packages`, `list_helm_releases`,
`get_helm_release`, `list_argo_apps`, `get_argo_app`, `get_metrics`,
`top_resources`, `get_node_load`, `query_prometheus`, `discover_metrics`,
`get_prometheus_rules`, `get_costs`, `get_rightsizing`, `crossplane_trace`,
`list_vclusters`.

Write (`/mcp` only): `restart_workload`, `scale_workload`, `delete_pod`,
`apply_yaml`, `patch_resource`, `helm_rollback`, `argo_sync`,
`manage_argo_rollout`, `manage_cronjob`, `cordon_node`, `drain_node`.

`exec_pod` (destructive, `/mcp` only) is registered only while "Allow agents
to run commands in pods" (`allowExec` in `mcp.json`) is on; it is off by
default and its output is not scrubbed. Turning the setting off removes the
tool from live sessions (`Registry.Unregister`).

## Feature tools (`featuretools/`)

`featuretools.Register` (called from `cmd/main.go` with the feature
services) adds: `get_issues`, `audit` (health), `get_changes`,
`get_change_snapshot` (changes), `get_topology`, `get_related` (topology),
`list_certificates` (certs), `who_can`, `subject_permissions`,
`risky_grants` (rbac), `upgrade_readiness`, `check_network_path`,
`network_policy_coverage`, `network_probe` (write, destructive, needs
`confirm`), `compare_resources`, `get_drift`, `gitops_status`,
`cnpg_cluster_status`, `flux_reconcile` (write) and `inspect_image`. It
also registers `diagnose` enrichers (health issues, recent changes under
`context`) and `get_dashboard` sections (issue counts, audit score).

## Why a result is empty: `reasons`

Read tools never answer a failure with only an error string. They return
`{"items": [], "reasons": [...]}`, and partial results carry `reasons` too.
Each reason is `{code, kind?, namespace?, detail}` (`reasons.go`):

| Code | Meaning for the agent |
| --- | --- |
| `user_forbidden` | RBAC denies the user; do not retry |
| `kind_unknown` | no such kind; check the spelling |
| `kind_not_installed` | the CRD/API is not on this cluster |
| `namespace_not_found` | the namespace does not exist |
| `object_not_found` | definitive: the object does not exist |
| `cache_warming` | retry in a few seconds |
| `cluster_disconnected` | the cluster is unreachable |
| `credentials_rejected` | re-authenticate |
| `truncated` | more results exist; narrow the query |
| `read_error` | anything else; see `detail` |

Register read tools with `registerRead` (core) or wrap the handler with
`mcp.Explained`; feature tools use `respond`/`explain` in `featuretools`,
which also map a feature's `Warnings` to reasons. `mcp.ReasonsDoc` is
appended to every read tool's description (and stripped from the UI list).

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
