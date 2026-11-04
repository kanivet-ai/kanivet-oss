# rbac

Access graph and RBAC questions for one cluster: subjects (Users, Groups,
ServiceAccounts) → RoleBindings/ClusterRoleBindings → Roles/ClusterRoles →
rules. Aggregated ClusterRoles are resolved against their label selectors so
each rule records the member role it came from (`Rule.Source`), including
members the aggregation controller has not copied in yet.

Service accounts also match the implicit identities Kubernetes gives them
(`system:serviceaccount:<ns>:<name>`, `system:serviceaccounts`,
`system:serviceaccounts:<ns>`, `system:authenticated`); the matching
identity is reported as `Via`.

## Data source

The graph is loaded with four LISTs (ClusterRoles, ClusterRoleBindings,
Roles, RoleBindings) plus ServiceAccounts, and cached per cluster
stale-while-revalidate (30 s fresh, 10 min stale). A kind the user may not
list becomes a `Warnings` entry instead of an error; only when all four are
forbidden does a call fail with `ErrNoAccess`. The current user's own access
uses SelfSubjectRulesReview / SelfSubjectAccessReview, which work without
any RBAC read permission. Secrets are never read.

## Service API (for MCP tools)

```go
svc := rbac.NewService(k8sClient, cache)

svc.WhoCan(ctx, cluster, rbac.Query{Verb: "get", Group: "", Resource: "secrets", Namespace: "prod"}) // *WhoCanResult
svc.SubjectPermissions(ctx, cluster, rbac.SubjectRef{Kind: "ServiceAccount", Name: "ci", Namespace: "build"}, "build") // *PermissionMatrix
svc.RiskyGrants(ctx, cluster, rbac.RiskOptions{IncludeSystem: false, Namespace: ""}) // []Risk
svc.RolePermissions(ctx, cluster, "ClusterRole", "", "view")
svc.BindingPermissions(ctx, cluster, "RoleBinding", "prod", "ci-deployer")
svc.Subjects(ctx, cluster)                 // picker list + GraphInfo
svc.SelfPermissions(ctx, cluster, "prod")  // SelfSubjectRulesReview as a matrix
svc.SelfCanI(ctx, cluster, rbac.Query{...})
svc.Invalidate(cluster)
```

`Query.Group`: `"*"` matches any API group, `""` is the core group.
`Query.Resource` may carry a subresource (`pods/exec`). An empty
`Query.Namespace` asks "anywhere"; each `Path.Scope` then says where the
grant applies (`""` = cluster-wide). Rules with `resourceNames` only match a
query that names the object.

The pure functions (`BuildGraph`, `Graph.WhoCan`, `Graph.SubjectPermissions`,
`Graph.RiskyGrants`, `RuleAllows`) work on a `Snapshot` without a cluster.

## Risk checks

Full wildcard, wildcard verbs/resources, Secret reads, escalate/bind and
binding writes, impersonation, pods/exec and attach, nodes/proxy,
serviceaccounts/token, cluster-admin bindings, grants to a `default`
service account, and grants to unauthenticated users. Cluster-wide grants
rank higher than namespaced ones. Bindings named `system:*`/`kubeadm:*` are
skipped unless `IncludeSystem` is set.

## HTTP

`/api/v1/rbac/{summary,who-can,subject,role,binding,risks,self,can-i}` (GET)
and `/refresh` (POST); see `handler.go`. `group=core` means the core group;
an omitted `group` matches any.
