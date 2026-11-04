# cnpg

CloudNativePG (`postgresql.cnpg.io`) clusters: status, instances, backups,
scheduled backups, poolers, and the operator actions people reach for. Plain
functions over `dynamic.Interface`, ready for the MCP layer.

## Service API

| Function | Purpose |
| --- | --- |
| `Detect(ctx, dyn) (*Detection, error)` | Probes the Cluster, Backup, ScheduledBackup and Pooler CRDs. The Cluster CRD decides `Installed`. |
| `ListClusters(ctx, dyn, det) ([]ClusterSummary, error)` | Every Cluster with health, ready/total instances, primary, timeline, backup method and last successful backup. |
| `GetClusterStatus(ctx, dyn, det, ns, name, Deps) (*ClusterStatus, error)` | **MCP: CNPGClusterStatus.** Summary, instances (role, status bucket, pod readiness, node, restarts, replica lag), backups newest first, scheduled backups, poolers and conditions. `Deps.Pods` and `Deps.Lag` are optional enrichers. |
| `BackupNow(ctx, dyn, det, ns, cluster, now) (*BackupResult, error)` | Creates a `Backup` with the cluster's declared method (barmanObjectStore, volumeSnapshot or WAL-archiver plugin). Refuses clusters without backup config. Write. |
| `Promote(ctx, dyn, det, ns, cluster, instance, now) error` | Switchover via the status subresource (`targetPrimary`), as `kubectl cnpg promote`. `CheckPromote` refuses the current primary, unknown instances and in-flight switchovers. Write. |
| `Restart(ctx, dyn, det, ns, cluster, now) error` | Rolling restart via `kubectl.kubernetes.io/restartedAt`. Write. |
| `ParseReplicationLag(metrics) (float64, bool)` | Reads `cnpg_pg_replication_lag` from an instance's `:9187/metrics`. |

Replica lag is best-effort: the API handler reads it through the pod proxy
(`pods/proxy` RBAC); without it the field is simply omitted.

HTTP routes: `GET /cluster/cnpg/clusters`, `GET /cluster/cnpg/clusters/:ns/:name`,
`POST .../backup`, `POST .../promote` (`{"instance": "pg-2"}`), `POST .../restart`.
