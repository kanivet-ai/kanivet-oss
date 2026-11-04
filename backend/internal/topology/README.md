# topology

Relationship graph between the Kubernetes resources of one namespace.

## Service API (wrap these for MCP)

```go
svc := topology.NewService(sourceFor, topology.DefaultTTL) // api.Handler.Topology() returns the shared one

// get_related: everything within `depth` hops of a resource, either direction.
// `contextNS` is only used for cluster-scoped refs (Node, PV, StorageClass).
g, err := svc.Neighbors(ctx, cluster, topology.Ref{Kind: "Deployment", Namespace: "shop", Name: "web"}, contextNS, depth, includeIdle)

// get_topology: the whole namespace, optionally narrowed.
g, err := svc.NamespaceGraph(ctx, cluster, "shop", topology.Filters{Kinds: []string{"Service", "Deployment"}})
```

// get_cluster_overview: per-namespace pod and workload health rollups,
// with the application names found (5 cluster-wide LISTs, cached ~10s).
ov, err := svc.ClusterOverview(ctx, cluster) // *topology.Overview{Namespaces, Warnings}
```

The graph queries return `*topology.Graph{Nodes, Edges, Focus, Truncated, Warnings}`.
Each node also carries `app`, the application it belongs to (labels
`app.kubernetes.io/name`, `instance`, `part-of`, `app`, `k8s-app`, then the
Helm release), which the UI's Application view groups by.
HTTP for the overview: `GET /api/v1/cluster/topology/overview?cluster=`.
Node IDs are `Kind/namespace/name` (`Kind//name` for cluster-scoped).
Each node carries `health` (healthy | warning | error | unknown), a short
`status`, its controller `owner` ID, and `missing` for dangling references.

HTTP: `GET /api/v1/cluster/topology/neighbors/:group/:version/:kind/:namespace/:name?cluster=&depth=&context=&includeIdle=`
and `GET /api/v1/cluster/topology/namespace/:namespace?cluster=&kinds=&includeIdle=&max=`
(`_` stands for an empty group or namespace).

## Relations

| relation | from -> to |
|---|---|
| owns | any owner -> owned (ownerReferences) |
| selects | Service -> Pod |
| exposes | Service -> top workload of the pods it selects |
| routes | Ingress / HTTPRoute -> Service |
| attaches | Gateway -> HTTPRoute (parentRefs) |
| tls | Ingress -> Secret |
| mounts / env / runs-as / pulls-with | workload (top of its owner chain) or bare Pod -> ConfigMap, Secret, PVC, ServiceAccount |
| binds, class | PVC -> PV -> StorageClass |
| scales | HPA -> scale target |
| protects | PDB -> Pod |
| applies-to | NetworkPolicy -> Pod |
| runs-on | Pod -> Node |

## Data source and safety

The watcher cache holds simplified list rows (no selectors, volumes or env),
so it can't answer these questions. The engine lists the namespace's kinds
through the dynamic client instead: 18 LISTs at 8-way concurrency, cached
per (cluster, namespace) for `DefaultTTL` and shared by every caller.
Cluster-scoped Nodes, PVs and StorageClasses are fetched by name only when
something references them.

- Secrets, ConfigMaps and ServiceAccounts are read through the **metadata**
  API, so Secret `.data` never reaches the backend.
- A kind that is forbidden becomes a `Warnings` entry ("Secret: access
  denied"). An API that isn't installed (Gateway API) is skipped silently.
  Refs to an unreadable kind are never marked `missing`.
- Graphs are capped at `DefaultMaxNodes`, dropping pods and config before
  workloads and entry points.

The Argo CD application topology (`internal/argo/topology.go`) stays separate:
it is scoped to an Application's managed resources and their owned children.
This engine covers the general case.
