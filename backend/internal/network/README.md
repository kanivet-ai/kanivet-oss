# network

Network path analysis, NetworkPolicy coverage, a live connection probe, and
observed traffic (`traffic/`).

## Data

`LiveSource` builds a `Snapshot` per call:

- Pods come from the shared pod cache (`podcache`, fed by the watcher's pods
  watch). No pod LIST. The cache keeps `podIP`, `hostNetwork` and container
  ports for this package.
- Namespaces, Services, EndpointSlices and NetworkPolicies are listed with a
  10s per-cluster cache. If RBAC forbids a kind, it is treated as empty and
  the result's `warnings` name it.
- The CNI that enforces policies (Cilium, Calico, Antrea, ...) is detected
  from pod names. If none is found while policies exist, results warn that
  the policies may have no effect.

All analysis is pure functions on `*Snapshot` (`CheckPath`, `Coverage`,
`ResourceNetwork`, `PolicyDetail`, `Targets`), so it is tested without a
cluster.

## Service API (wrap these for MCP)

```go
svc.CheckPath(ctx, cluster, PathRequest{Source, Destination Ref; Port int32; Protocol string}) (*PathResult, error)
svc.PolicyCoverage(ctx, cluster, namespace string) (*CoverageReport, error)   // "" = all namespaces
svc.ResourceNetwork(ctx, cluster, Ref) (*ResourceNetwork, error)              // Pod, workload or Service
svc.PolicyDetail(ctx, cluster, namespace, name string) (*PolicyDetail, error)
svc.Targets(ctx, cluster) (*Targets, error)
svc.Flows(ctx, cluster, namespace string, window time.Duration) *traffic.Response
svc.Probe(ctx, cluster, ProbeRequest{..., Confirm: true}) (*ProbeResult, error) // WRITE: creates a pod
```

`Ref.Kind` accepts `Pod`, `Deployment`, `StatefulSet`, `DaemonSet`,
`ReplicaSet`, `Job`, `Service`, `IP` (set `IP`) and the usual short names
(`deploy`, `svc`, ...).

### Path semantics

Hops: `source → egress → service → endpoints → ingress → port → destination`.

- A pod is isolated in a direction when any policy in its namespace selects
  it with that policy type (`policyTypes` defaults to Ingress, plus Egress
  when egress rules exist). With no rules, that policy is a default-deny.
- Peers: `podSelector` (policy namespace), `namespaceSelector`, both
  (intersection), `ipBlock` with `except`. Ports: number, `endPort` ranges,
  names (resolved on the destination pod), protocol defaulting to TCP.
- For a Service, policies are evaluated against each ready backend on its
  resolved targetPort (post-DNAT), as CNIs do.
- An unresolved named targetPort fails. A numeric port no container declares
  is only a warning, because declaring a port is informational.
- Destination `IP`: a pod IP is treated as that pod. Any other address is
  external, and only the source's egress policies apply.
- Verdict: `blocked` if any hop fails, `partial` if any warns, else `allowed`.

### Probe

The probe creates `kanivet-probe-<id>` in the source's namespace and runs
`nc -z -w 3 host port` with busybox. The pod runs as non-root with all
capabilities dropped, a read-only root filesystem, a 60s active deadline and
no service account token. It is always deleted. Before creating anything, the
probe runs a `SelfSubjectAccessReview`. If the review denies it, the result is
`forbidden` with a kubectl command to run by hand.

With `mimicLabels`, the probe gets the source's labels, so label-based
policies apply to it. To avoid side effects, the probe is then owned by the
source pod as its controller, so no ReplicaSet or Job adopts it. Its
readiness probe always fails, so no Service routes to it.

## traffic

`traffic.Source` is the integration interface (`Detect`, `Flows`).
`traffic.Manager` uses the first source that is available. The two built-in
sources read the cluster's Prometheus through the metrics service:

- `Istio`: `istio_requests_total` (rate, 5xx rate) and
  `istio_request_duration_milliseconds_bucket` (p95).
- `Hubble`: `hubble_flows_processed_total` with workload context labels, and
  `verdict="DROPPED"` as errors.

Detection tells three states apart: not installed, installed but metrics
missing, and available. Each state comes with setup hints.
