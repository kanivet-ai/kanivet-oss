# compare

Diffs two Kubernetes objects and explains the difference.

- Any two resources, of the same kind or not.
- The same resource across clusters or namespaces (staging vs prod).
- Drift: a live object against what was declared for it, either its
  `kubectl.kubernetes.io/last-applied-configuration` or the document for it
  in its Helm release manifest (found through `meta.helm.sh/release-*`).

## Service API (wrap these as MCP tools)

```go
svc := compare.NewService(k8sClient, helmService)

// compare_resources
res, err := svc.Compare(ctx, compare.Ref{...}, compare.Ref{...}, compare.Options{})

// drift: A is the declared version, B is live
res, err := svc.Drift(ctx, ref, compare.SourceLastApplied /* or SourceHelm */, compare.Options{})

// which drift baselines exist for a live object
b, err := svc.Baselines(ctx, ref)

// two objects already in hand (no cluster access)
res, err := compare.CompareObjects(objA, objB, compare.Options{})
```

`Ref` is `{cluster, group, version, kind, namespace, name, source}` with
`source` one of `live` (default), `last-applied`, `helm`.

`Result` holds both normalized sides as YAML (`a.yaml`, `b.yaml`), a semantic
`summary` (`[]Insight`: category, title, A/B values, the paths it explains),
the full `changes` list (path, added/removed/changed, A/B values), `stats`,
`identical`, and `notes` about what normalization hid. A side that cannot be
read (not found, forbidden) is reported in `a.error`/`a.status` rather than
failing the call.

## Normalization

Always: Secret `data`/`stringData` values become
`<redacted hash:…, N bytes>`, an HMAC keyed per process, so key sets and
equality are visible but values never leave the backend; the last-applied
annotation is removed (it can embed Secret data).

By default, toggleable through `Options`:

- `IncludeMetadata=false`: drops managedFields, resourceVersion, uid,
  creationTimestamp, generation, ownerReferences, the revision annotations
  and `pod-template-hash`.
- `IncludeStatus=false`: drops `.status`.
- `KeepDefaults=false`: drops values equal to well-known server defaults
  (dnsPolicy, terminationMessagePath, probe timings, Deployment strategy,
  Service sessionAffinity, …) and cluster-assigned fields (Service clusterIP).
- `IncludeUndeclared=false`: when one side is a declared baseline, live map
  keys the baseline never set are dropped (`PruneUndeclared`); extra list
  items, such as an injected sidecar, are kept since they are real drift.

## HTTP

- `POST /api/v1/compare` body `{a: Ref, b: Ref, options: Options}` → `Result`
- `GET /api/v1/compare/baselines?cluster&group&version&kind&namespace&name` → `Baselines`

Objects are read with a single GET each through the interactive dynamic
client (the same path the inspector uses), so RBAC applies per side.
