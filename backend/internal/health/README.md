# health: live issues and best-practice audit

Two views over one cluster snapshot:

- **Issues**: what is broken now. A pluggable rule engine (`RegisterRule`)
  over pods, workloads, Services, Ingresses, PVCs, Nodes, HPAs, Jobs and
  CronJobs, plus the incident timeline's classifier (`incidents.BuildTimeline`)
  for recent Warning events that no state rule covers (probe output, mount
  failures). Signals are deduplicated per object and grouped by owner: ten
  crash-looping pods of a Deployment are one issue on the Deployment. Issues
  rank by severity, then number of affected objects, then age.
- **Audit**: security and reliability checks per workload template
  (`RegisterCheck`), each with an id, severity, explanation and YAML fix.
  Scores are the share of severity-weighted checks passed (critical 10,
  warning 3, info 1), per namespace and for the cluster. System namespaces
  are skipped unless asked for. Ignores (per resource, namespace or cluster)
  persist in `~/.kanivet/health/audit-ignores.json`.

## Data sources

Pods come from the shared pod cache (`podcache`), which the Pods list's watch
feeds. The watcher slims workload templates out of its objects, so the other
kinds are listed through the typed client and cached for 20 s per cluster;
concurrent callers share one listing. A kind that cannot be listed (RBAC,
not served) is named in `unavailable` and the rules that read it are skipped,
so a forbidden list never reads as healthy. Secrets are never read.

## Service API (for MCP)

```go
svc.Issues(ctx, health.IssueFilter{Cluster, Namespaces, Severities, Kinds, Resource, Search}) (health.IssuesReport, error)  // get_issues
svc.Audit(ctx, health.AuditScope{Cluster, Namespaces, Resource, IncludeSystem}) (health.AuditReport, error)                 // audit
svc.Resource(ctx, cluster, health.ResourceRef{Kind, Namespace, Name}) (health.ResourceHealth, error)                       // one object
svc.Checks() []health.Check
svc.AddIgnore(health.Ignore{...}) / svc.RemoveIgnore(...) / svc.Ignores(cluster)
svc.Refresh(cluster)
```

`EvaluateIssues(*Snapshot)` and `EvaluateAudit(*Snapshot, scope, ignored)` are
pure and testable without a cluster.

## HTTP

Under `/api/v1/cluster-health`: `GET issues`, `GET audit`, `GET resource`,
`GET checks`, `GET|POST|DELETE ignores`. Query: `cluster`, `namespaces`,
`severities`, `kinds`, `search`, `kind`/`namespace`/`name` for one object,
`refresh=1` to bypass the 20 s listing cache.
