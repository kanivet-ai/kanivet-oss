# gitops

Flux CD reads and actions, a unified Argo CD + Flux view, and GitOps ownership
of arbitrary resources. Every function takes a `dynamic.Interface` and returns
plain structs, so the MCP layer can wrap them directly.

## Service API

| Function | Purpose |
| --- | --- |
| `DetectFlux(ctx, dyn) (*FluxDetection, error)` | Probes each Flux CRD (one GET each) and resolves its served version. Cache the result. |
| `GitOpsStatus(ctx, dyn, argoInstalled, fluxDet) (*Status, error)` | **MCP: GitOpsStatus.** Argo Applications and Flux objects as one list with a unified health (`Healthy`, `Failing`, `Progressing`, `Suspended`, `Unknown`), failing first. Partial failures land in `Status.Errors`. |
| `ListFlux(ctx, dyn, det) ([]FluxObject, error)` | Every GitRepository, OCIRepository, HelmRepository, Bucket, Kustomization and HelmRelease. |
| `GetFluxObject(ctx, dyn, det, kind, ns, name) (*FluxObjectDetail, error)` | One object with conditions, inventory (Kustomization) and Helm history (HelmRelease). |
| `FluxReconcile(ctx, dyn, det, kind, ns, name, ReconcileOptions) (*ReconcileResult, error)` | **MCP: FluxReconcile.** Sets `reconcile.fluxcd.io/requestedAt`; `WithSource` also annotates the source first. Refuses suspended objects. Write. |
| `FluxSetSuspended(ctx, dyn, det, kind, ns, name, suspend) error` | Patches `spec.suspend`; resume also requests a reconcile. Write. |
| `OwnerOf(labels, annotations, argoNamespace, trustInstanceLabel) *Owner` | Which Flux Kustomization/HelmRelease or Argo Application manages a resource. |

Status derivation (`FluxObject.Status`): suspended wins; `Reconciling=True` is
Progressing; `Ready=True` is Ready; `Ready=False` is Failed unless the reason is
`Progressing`/`DependencyNotReady`; anything else is Progressing or Unknown.

HTTP routes (see `internal/api/integrations_handlers.go`):
`GET /cluster/integrations/detect`, `GET /cluster/gitops/status`,
`GET /cluster/gitops/flux`, `GET /cluster/gitops/flux/:kind/:ns/:name`,
`POST .../reconcile[?withSource=true]`, `POST .../suspend`, `POST .../resume`.
