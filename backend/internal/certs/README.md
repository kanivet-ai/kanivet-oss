# certs

TLS certificate inventory for a cluster. Sources:

- `kubernetes.io/tls` Secrets (listed with a `type=` field selector). Only
  `tls.crt` is parsed; `tls.key` and other data are never copied into results.
- Ingress `spec.tls` and Gateway API listener `tls.certificateRefs`, which become
  `usedBy` links plus `host-not-covered` / `missing-secret` issues.
- cert-manager `Certificate`, `CertificateRequest`, `Issuer`, `ClusterIssuer`
  (when installed): Ready state, conditions, renewal time, failed requests.
- `caBundle` of Mutating/ValidatingWebhookConfigurations.

Reports are cached per cluster for 30s. Missing APIs are skipped; RBAC denials
are reported in `Report.Warnings` rather than failing the whole report.

## Service API (wrap these as MCP tools)

```go
svc := certs.NewService(k8sClient) // any ClientProvider

// list_certificates
report, err := svc.Certificates(ctx, cluster, certs.Filter{
    Namespace: "web", Statuses: []certs.Status{certs.StatusExpired, certs.StatusCritical},
    ExpiringWithinDays: 30, Search: "example.com", Source: "secret",
    Resource: &certs.Ref{Kind: "Ingress", Namespace: "web", Name: "site"},
})

// Issues-system hook: expiry, coverage, missing-secret and cert-manager issues.
issues, err := svc.ExpiringIssues(ctx, cluster)
```

`certs.Build(ctx, cluster, clientset, dynamicClient, now)` builds a report from
explicit clients (used by the tests).

HTTP: `GET /api/v1/certs?cluster=&namespace=&status=expired,critical&expiringWithin=30&search=&source=&kind=&resourceNamespace=&name=&refresh=1`.
