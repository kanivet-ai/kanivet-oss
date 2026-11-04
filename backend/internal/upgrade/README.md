# upgrade: Kubernetes upgrade readiness

Answers "what breaks if this cluster moves from its current minor to a target
minor?".

## Evidence

The version an object is stored and served in can differ from the version
its author requested, so the live object alone hides most problems. The
service reads several sources, and each one degrades on its own (RBAC denial
or failure becomes a `Check` with status `unavailable` or `partial`, never an
error):

| Source | How | Severity when removed by target |
| --- | --- | --- |
| `live` | `apiserver_requested_deprecated_apis` from the API server's `/metrics` (a client requested the API since the server started); objects of a kind with no replacement API (PodSecurityPolicy) | blocker |
| `helm` | every release's stored manifest (through `internal/helm`); `helm upgrade` fails on kinds it cannot map | blocker |
| `last-applied` | the `kubectl.kubernetes.io/last-applied-configuration` annotation, via a metadata-only LIST of each catalog kind | warning (the stored object survives; its source manifest will not apply) |

APIs deprecated but not yet removed by the target are warnings; deprecations
with no scheduled removal (core `v1` Endpoints) are info.

Also checked: kubelet version skew against the target (max 3 minors from
1.28, 2 before; newer-than-control-plane kubelets are flagged), CRDs whose
`status.storedVersions` list unserved versions or whose storage version is
deprecated, and the one-minor-at-a-time control-plane path.

The table is `catalog.json` (embedded), from the Kubernetes deprecation guide.
Bump `reviewed` and `latestRelease` when adding releases.

Collection is per cluster and does not depend on the target, so it is cached
for two minutes and every target is re-scored from the same evidence.
Annotation and CRD scans use LIST calls rather than the watcher cache: the
watcher only holds kinds a user is viewing.

## Service API (for MCP `upgrade_readiness`)

```go
svc := upgrade.NewService(k8sClient, helmService)
rep, err := svc.UpgradeReport(ctx, cluster, "1.31", false) // "" = next minor; true = re-collect
cat := svc.Catalog()                                       // the deprecation table
rep, err = upgrade.Analyze(cat, cluster, inputs, "1.32")   // pure scoring, for tests
```

`Report` carries `currentVersion`, `targetVersion`, `availableTargets`,
`path`, `summary` (blockers, warnings, infos, ok, verdict), `apis` (one
`APIFinding` per deprecated API with replacement, hint and affected
resources with their source), `nodes`, `nodeSummary`, `crds`, `notes` and
`checks`.

## HTTP

- `GET /api/v1/upgrade/report?cluster=&target=1.31&refresh=1`
- `GET /api/v1/upgrade/catalog`
