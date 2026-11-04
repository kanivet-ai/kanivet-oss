# changes

Resource change history rebuilt on demand from what the cluster already
stores. Nothing is recorded locally: Kanivet is not always running, so a
local log would have gaps. History is complete the first time a resource is
opened, as far back as the cluster keeps it.

| Source | What it gives | Old values? |
| --- | --- | --- |
| `replicaset` | Deployment rollouts (one ReplicaSet per pod-template revision, `deployment.kubernetes.io/revision`, `kubernetes.io/change-cause`) | yes, full template diff |
| `controllerrevision` | StatefulSet / DaemonSet rollouts | yes |
| `helm` | release revisions (owning release found via `meta.helm.sh/release-*`); values + manifest per revision, fetched lazily | yes |
| `argocd` | Application `status.history` (revision, deployedAt, source); owning app via `argocd.argoproj.io/instance` or tracking-id | revision/source |
| `flux` | HelmRelease v2 `status.history`, else `lastAppliedRevision` + Ready time | partial |
| `managedfields` | who (field manager) last wrote which fields and when | no, paths only |
| `event` | recent Events of the object (short-lived) | n/a |
| `creation` | `metadata.creationTimestamp` | n/a |

Secrets: object data is never returned. Secret history comes from
managedFields (key names only, "owns keys [a, b]"); Helm manifests have every
`data:`/`stringData:` value in Secret documents replaced with `<redacted>`.

## Service API (for MCP `get_changes`)

```go
svc := changes.NewService(k8sClient, helmService)

// One resource's history, newest first. Entries carry Diff (field changes vs
// the previous revision), Categories (rollout, image, env, resources, config,
// replicas, restart, secret, sync, edit, event, created), Source and Actor.
svc.ChangesFor(ctx, changes.ResourceRef{Cluster, Group, Version, Kind, Namespace, Name}) (*History, error)

// Cluster-wide feed: rollouts, workload creations, Helm upgrades, Argo/Flux
// syncs since f.Since (default 24h, window 14d). Cached 30s per cluster.
svc.RecentChanges(ctx, cluster, changes.Filter{Namespaces, Kinds, Categories, Sources, Since, Limit}) (*Feed, error)

// YAML of an entry whose snapshot is not inline (Helm: values + manifest).
svc.Snapshot(ctx, cluster, entryID) (string, error)

// Adapter feeding "what changed before it broke" into the incident timeline.
svc.IncidentSource() incidents.ChangeSource
```

HTTP: `GET /api/v1/changes/resource?cluster&group&version&kind&namespace&name`,
`GET /api/v1/changes/feed?cluster&namespaces&kinds&categories&sources&since&limit`,
`GET /api/v1/changes/snapshot?cluster&id`.

Sources that are forbidden or unreachable are listed in `warnings` so the UI
can say the history is partial; CRDs that are not installed are silent.
