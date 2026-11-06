# cis: CIS Kubernetes Benchmark compliance (read-only)

The "CIS" view of Audit. It works out which provider runs the cluster
(`internal/clusterinfo`), picks the matching benchmark profile and evaluates
each control with read-only API calls. Results from scanners that already run
in the cluster are merged in.

## Licensing

The CIS Benchmarks are copyrighted by the Center for Internet Security. This
package uses only the control **numbers** (`5.1.1`, EKS `4.1.1`, ...) so
results line up with other tools. Every title, description, manual-check
hint and remediation note is Kanivet's own short wording; no CIS rationale,
audit or remediation text is reproduced. Numbering follows CIS Kubernetes 1.9
and the EKS/GKE/AKS 1.5 benchmarks as closely as we know them; check the
official PDF before relying on a mapping. Kanivet's results are observations,
not a certified CIS assessment.

## Profiles

| id | used for | notes |
| --- | --- | --- |
| `cis-k8s` | kubeadm, kind, minikube, Docker Desktop, unknown | full control plane, etcd, node and policy sections |
| `cis-eks` | EKS | control plane is `provider-responsibility`; aws-auth / access entries checked |
| `cis-gke` | GKE | managed-service controls are mostly manual |
| `cis-aks` | AKS | managed-service controls are mostly manual |
| `k3s-hardening` | k3s | CIS numbering; control plane runs inside k3s so its flags are not visible |
| `rke2-hardening` | RKE2 | CIS numbering; static pods read like kubeadm's |
| `openshift-policies` | OpenShift | section 5 only, with a pointer to the Compliance Operator |

The profile follows detection; the user can switch it and override the
provider. Both choices persist per cluster in
`~/.kanivet/health/cis-overrides.json`.

## Statuses

`automated-pass`, `automated-fail`, `manual` (with what to check),
`provider-responsibility` (managed control plane), `not-visible` (needs node
file access or a permission we lack), `error`. Nothing we cannot see is ever
reported as a pass. The score is passes over automated (pass + fail) controls;
the other statuses are counted separately.

## What is read

- **Policies** (RBAC, Pod Security, NetworkPolicy, Secrets, general): the
  health snapshot (shared with Issues/Audit), namespaces, service accounts,
  the `rbac` package's risky-grant engine and `WhoCan`, admission webhooks and
  ValidatingAdmissionPolicies. Pod Security controls ask whether admission
  would block the setting (namespace PSA enforce level, or a policy engine,
  which makes them `manual`), and list workloads that use it today.
- **Kubelet**: `GET /api/v1/nodes/<n>/proxy/configz` after a
  SelfSubjectAccessReview for `nodes/proxy get`; denied means `not-visible`
  with a note. Up to 3 nodes per pool and 30 in total are read, 6 at a time;
  nodes with identical settings are grouped per pool. Fargate nodes are
  reported as not exposing a kubelet.
- **Control plane**: only when `kube-apiserver-<node>` static pods are
  visible. kube-system pods are listed in full (the shared pod cache drops
  command lines) and their flags parsed. On EKS/GKE/AKS these controls are
  `provider-responsibility`.
- **EKS**: `kube-system/aws-auth` (system:masters mappings; absence means
  access entries). Control-plane logging, KMS and endpoint settings need the
  EKS API and are `manual` with the exact `aws eks describe-cluster` query;
  Kanivet makes no AWS calls here.
- **Scanners** (tier 2): Trivy Operator `ClusterComplianceReport` (summary and
  detail formats; `k8s-cis`, `eks-cis`, ... matched to the profile), Kubescape
  `WorkloadConfigurationScan` controls whose id is `CIS-x.y.z`, and the logs of
  the newest completed `kube-bench` Job. A scanner verdict fills a control we
  could not decide (marked with its source and age); where both decided and
  disagree, ours stays and the control is flagged `disagreement`.

## Service API (for MCP)

```go
svc.Benchmark(ctx, cis.Options{Cluster, Profile, Refresh}) (cis.Report, error)   // cis_benchmark
svc.DetectProvider(ctx, cluster, refresh) (clusterinfo.Info, error)            // detect_provider
svc.Override(cluster) / svc.SetOverride(cluster, cis.Override{Provider, Profile})
cis.Evaluate(*cis.Inputs, profile, now) cis.Report                              // pure, for tests
clusterinfo.Detect(clusterinfo.Signals) clusterinfo.Info                        // pure
```

Inputs are cached for 90 s per cluster (kubelet reads are not free).

## HTTP

Under `/api/v1/cluster-health/cis`: `GET ` (query `cluster`, `profile`,
`refresh=1`), `GET provider`, `GET|PUT override`.

## Gaps

- Node file permissions (1.1.x, 4.1.x) need node access: only scanners fill them.
- Trivy `ClusterInfraAssessmentReport`/`InfraAssessmentReport` checks carry
  Trivy ids (KCV...), not CIS numbers, and are not mapped yet.
- Kubescape results without `CIS-` control ids are ignored.
