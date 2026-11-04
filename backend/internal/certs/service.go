package certs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// ClientProvider yields clients for a cluster; *k8s.Client satisfies it.
type ClientProvider interface {
	GetClientForCluster(cluster string) (kubernetes.Interface, error)
	GetDynamicClient(cluster string) (dynamic.Interface, error)
}

var (
	gvrGateway       = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	gvrCertificate   = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	gvrCertRequest   = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests"}
	gvrIssuer        = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "issuers"}
	gvrClusterIssuer = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}
)

const (
	cacheTTL     = 30 * time.Second
	buildTimeout = 25 * time.Second
)

type cached struct {
	report *Report
	at     time.Time
}

// Service builds certificate reports. It is safe for concurrent use.
type Service struct {
	clients ClientProvider
	now     func() time.Time

	mu     sync.Mutex
	cache  map[string]cached
	builds map[string]*sync.Mutex
}

// NewService returns a Service that reads clusters through clients.
func NewService(clients ClientProvider) *Service {
	return &Service{clients: clients, now: time.Now, cache: map[string]cached{}, builds: map[string]*sync.Mutex{}}
}

// Certificates returns the cluster's certificate report narrowed by f.
// Issues are narrowed to those touching a returned certificate or, for
// resource-scoped filters, the resource itself.
func (s *Service) Certificates(ctx context.Context, cluster string, f Filter) (*Report, error) {
	full, err := s.report(ctx, cluster, f.Refresh)
	if err != nil {
		return nil, err
	}
	return applyFilter(full, f), nil
}

// ExpiringIssues returns the expiry, coverage and cert-manager issues of a
// cluster. It is the hook for the cluster Issues system.
func (s *Service) ExpiringIssues(ctx context.Context, cluster string) ([]Issue, error) {
	r, err := s.report(ctx, cluster, false)
	if err != nil {
		return nil, err
	}
	return append([]Issue(nil), r.Issues...), nil
}

// Forget drops a cluster's cached report.
func (s *Service) Forget(cluster string) {
	s.mu.Lock()
	delete(s.cache, cluster)
	s.mu.Unlock()
}

func (s *Service) report(ctx context.Context, cluster string, refresh bool) (*Report, error) {
	s.mu.Lock()
	lock, ok := s.builds[cluster]
	if !ok {
		lock = &sync.Mutex{}
		s.builds[cluster] = lock
	}
	s.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	c, hit := s.cache[cluster]
	s.mu.Unlock()
	if hit && !refresh && s.now().Sub(c.at) < cacheTTL {
		return c.report, nil
	}
	cs, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	dyn, err := s.clients.GetDynamicClient(cluster)
	if err != nil {
		dyn = nil
	}
	bctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	r := Build(bctx, cluster, cs, dyn, s.now())
	s.mu.Lock()
	s.cache[cluster] = cached{report: r, at: s.now()}
	s.mu.Unlock()
	return r, nil
}

type builder struct {
	ctx   context.Context
	now   time.Time
	r     *Report
	byKey map[string]*Certificate // "ns/name" of tls secrets
	order []*Certificate
}

func secretKey(ns, name string) string { return ns + "/" + name }

func (b *builder) warn(what string, err error) {
	switch {
	case apierrors.IsForbidden(err):
		b.r.Warnings = append(b.r.Warnings, fmt.Sprintf("No permission to list %s", what))
	default:
		b.r.Warnings = append(b.r.Warnings, fmt.Sprintf("Could not list %s: %v", what, err))
	}
}

func notInstalled(err error) bool {
	return apierrors.IsNotFound(err) || strings.Contains(err.Error(), "the server could not find the requested resource")
}

// Build reads every source and assembles a report. Exported for tests and
// callers that manage their own clients; dyn may be nil.
func Build(ctx context.Context, cluster string, cs kubernetes.Interface, dyn dynamic.Interface, now time.Time) *Report {
	b := &builder{ctx: ctx, now: now, r: &Report{Cluster: cluster, GeneratedAt: now}, byKey: map[string]*Certificate{}}
	b.secrets(cs)
	b.ingresses(cs)
	if dyn != nil {
		b.gateways(dyn)
		b.certManager(dyn)
	}
	b.webhooks(cs)
	b.expiryIssues()
	b.finish()
	return b.r
}

func (b *builder) secrets(cs kubernetes.Interface) {
	list, err := cs.CoreV1().Secrets("").List(b.ctx, metav1.ListOptions{FieldSelector: "type=kubernetes.io/tls"})
	if err != nil {
		b.warn("TLS Secrets", err)
		return
	}
	for i := range list.Items {
		sec := &list.Items[i]
		if string(sec.Type) != "kubernetes.io/tls" {
			continue
		}
		c := &Certificate{
			ID:     "secret/" + sec.Namespace + "/" + sec.Name,
			Source: "secret",
			Owner:  Ref{APIVersion: "v1", Kind: "Secret", Namespace: sec.Namespace, Name: sec.Name},
			UsedBy: []Ref{},
			SANs:   []string{},
			Chain:  []ChainEntry{},
		}
		// Only the public certificate is read; tls.key is never touched.
		if bundle, err := ParsePEM(sec.Data["tls.crt"]); err != nil {
			c.Status, c.ParseError = StatusInvalid, err.Error()
		} else {
			fillFromBundle(c, bundle, b.now)
		}
		b.byKey[secretKey(sec.Namespace, sec.Name)] = c
		b.order = append(b.order, c)
	}
}

func (b *builder) addIssue(i Issue) { b.r.Issues = append(b.r.Issues, i) }

// useSecret links obj to the TLS secret ns/name, checking hosts against SANs.
func (b *builder) useSecret(obj Ref, ns, name string, hosts []string) {
	c, ok := b.byKey[secretKey(ns, name)]
	if !ok {
		b.addIssue(Issue{
			Type: IssueMissingSecret, Severity: "critical", Resource: obj,
			Message: fmt.Sprintf("%s %s references TLS Secret %s/%s, which does not exist or is not of type kubernetes.io/tls", obj.Kind, obj.Name, ns, name),
		})
		return
	}
	obj.Hosts = hosts
	c.UsedBy = append(c.UsedBy, obj)
	if c.ParseError != "" {
		return
	}
	for _, h := range hosts {
		if !HostCovered(h, c.SANs) {
			b.addIssue(Issue{
				Type: IssueHostMismatch, Severity: "warning", Resource: obj, CertificateID: c.ID, Host: h,
				Message: fmt.Sprintf("Host %s is not covered by the SANs of Secret %s/%s", h, ns, name),
			})
		}
	}
}

func (b *builder) ingresses(cs kubernetes.Interface) {
	list, err := cs.NetworkingV1().Ingresses("").List(b.ctx, metav1.ListOptions{})
	if err != nil {
		b.warn("Ingresses", err)
		return
	}
	for _, ing := range list.Items {
		ref := Ref{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Namespace: ing.Namespace, Name: ing.Name}
		for _, t := range ing.Spec.TLS {
			if t.SecretName == "" {
				continue // served by the controller's default certificate
			}
			b.useSecret(ref, ing.Namespace, t.SecretName, t.Hosts)
		}
	}
}

func (b *builder) gateways(dyn dynamic.Interface) {
	list, err := dyn.Resource(gvrGateway).Namespace("").List(b.ctx, metav1.ListOptions{})
	if err != nil {
		if !notInstalled(err) {
			b.warn("Gateways", err)
		}
		return
	}
	b.r.GatewayAPIDetected = true
	for _, gw := range list.Items {
		ref := Ref{APIVersion: gw.GetAPIVersion(), Kind: "Gateway", Namespace: gw.GetNamespace(), Name: gw.GetName()}
		listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		for _, l := range listeners {
			lm, ok := l.(map[string]any)
			if !ok {
				continue
			}
			host, _, _ := unstructured.NestedString(lm, "hostname")
			refs, _, _ := unstructured.NestedSlice(lm, "tls", "certificateRefs")
			for _, cr := range refs {
				crm, ok := cr.(map[string]any)
				if !ok {
					continue
				}
				kind, _, _ := unstructured.NestedString(crm, "kind")
				if kind != "" && kind != "Secret" {
					continue
				}
				name, _, _ := unstructured.NestedString(crm, "name")
				ns, _, _ := unstructured.NestedString(crm, "namespace")
				if ns == "" {
					ns = gw.GetNamespace()
				}
				var hosts []string
				if host != "" {
					hosts = []string{host}
				}
				b.useSecret(ref, ns, name, hosts)
			}
		}
	}
}

func conditions(obj map[string]any) []Condition {
	raw, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	out := make([]Condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		c := Condition{}
		c.Type, _, _ = unstructured.NestedString(m, "type")
		c.Status, _, _ = unstructured.NestedString(m, "status")
		c.Reason, _, _ = unstructured.NestedString(m, "reason")
		c.Message, _, _ = unstructured.NestedString(m, "message")
		if ts, _, _ := unstructured.NestedString(m, "lastTransitionTime"); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				c.LastTransitionTime = &t
			}
		}
		out = append(out, c)
	}
	return out
}

func readyOf(conds []Condition) (bool, string, string) {
	for _, c := range conds {
		if c.Type == "Ready" {
			return c.Status == "True", c.Reason, c.Message
		}
	}
	return false, "Unknown", "No Ready condition reported"
}

func timeField(obj map[string]any, fields ...string) *time.Time {
	s, _, _ := unstructured.NestedString(obj, fields...)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func (b *builder) certManager(dyn dynamic.Interface) {
	certs, err := dyn.Resource(gvrCertificate).Namespace("").List(b.ctx, metav1.ListOptions{})
	if err != nil {
		if !notInstalled(err) {
			b.warn("cert-manager Certificates", err)
		}
		return
	}
	b.r.CertManagerDetected = true

	// CertificateRequests, counted per owning Certificate.
	type reqCount struct{ failed, pending int }
	reqs := map[string]*reqCount{}
	if list, err := dyn.Resource(gvrCertRequest).Namespace("").List(b.ctx, metav1.ListOptions{}); err == nil {
		for _, cr := range list.Items {
			owner := cr.GetAnnotations()["cert-manager.io/certificate-name"]
			if owner == "" {
				continue
			}
			k := secretKey(cr.GetNamespace(), owner)
			if reqs[k] == nil {
				reqs[k] = &reqCount{}
			}
			ready, reason, _ := readyOf(conditions(cr.Object))
			switch {
			case ready:
			case reason == "Failed" || reason == "Denied" || reason == "InvalidRequest":
				reqs[k].failed++
			default:
				reqs[k].pending++
			}
		}
	} else if !notInstalled(err) {
		b.warn("cert-manager CertificateRequests", err)
	}

	for _, cert := range certs.Items {
		ns := cert.GetNamespace()
		secretName, _, _ := unstructured.NestedString(cert.Object, "spec", "secretName")
		issuerName, _, _ := unstructured.NestedString(cert.Object, "spec", "issuerRef", "name")
		issuerKind, _, _ := unstructured.NestedString(cert.Object, "spec", "issuerRef", "kind")
		if issuerKind == "" {
			issuerKind = "Issuer"
		}
		dnsNames, _, _ := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
		conds := conditions(cert.Object)
		ready, reason, msg := readyOf(conds)
		ref := Ref{APIVersion: "cert-manager.io/v1", Kind: "Certificate", Namespace: ns, Name: cert.GetName()}
		issuerRef := Ref{APIVersion: "cert-manager.io/v1", Kind: issuerKind, Name: issuerName}
		if issuerKind == "Issuer" {
			issuerRef.Namespace = ns
		}
		info := &CertManagerInfo{
			Certificate: ref, IssuerRef: issuerRef, Ready: ready, Reason: reason, Message: msg,
			RenewalTime: timeField(cert.Object, "status", "renewalTime"),
			NotAfter:    timeField(cert.Object, "status", "notAfter"),
			DNSNames:    dnsNames, Conditions: conds,
		}
		if rc := reqs[secretKey(ns, cert.GetName())]; rc != nil {
			info.FailedRequests, info.PendingRequests = rc.failed, rc.pending
		}
		c, ok := b.byKey[secretKey(ns, secretName)]
		if !ok {
			info.SecretMissing = true
			c = &Certificate{
				ID: "cert-manager/" + ns + "/" + cert.GetName(), Source: "cert-manager", Owner: ref,
				Status: StatusInvalid, ParseError: "Secret " + secretName + " has not been issued yet",
				SANs: append([]string{}, dnsNames...), Chain: []ChainEntry{}, UsedBy: []Ref{},
			}
			b.byKey[secretKey(ns, secretName)] = c
			b.order = append(b.order, c)
		}
		c.CertManager = info
		if !ready {
			sev := "warning"
			if info.SecretMissing || info.FailedRequests > 0 {
				sev = "critical"
			}
			text := fmt.Sprintf("cert-manager Certificate %s/%s is not ready", ns, cert.GetName())
			if msg != "" {
				text += ": " + msg
			}
			b.addIssue(Issue{Type: IssueNotReady, Severity: sev, Resource: ref, CertificateID: c.ID, Message: text})
		}
	}

	b.issuers(dyn, gvrIssuer, "Issuer")
	b.issuers(dyn, gvrClusterIssuer, "ClusterIssuer")
}

func issuerType(obj map[string]any) (string, string) {
	spec, _, _ := unstructured.NestedMap(obj, "spec")
	for _, t := range []string{"acme", "ca", "selfSigned", "vault", "venafi"} {
		if _, ok := spec[t]; ok {
			server := ""
			if t == "acme" {
				server, _, _ = unstructured.NestedString(spec, "acme", "server")
			}
			return t, server
		}
	}
	return "external", ""
}

func (b *builder) issuers(dyn dynamic.Interface, gvr schema.GroupVersionResource, kind string) {
	list, err := dyn.Resource(gvr).Namespace("").List(b.ctx, metav1.ListOptions{})
	if err != nil {
		if !notInstalled(err) {
			b.warn("cert-manager "+kind+"s", err)
		}
		return
	}
	for _, it := range list.Items {
		ready, reason, msg := readyOf(conditions(it.Object))
		t, server := issuerType(it.Object)
		ref := Ref{APIVersion: "cert-manager.io/v1", Kind: kind, Namespace: it.GetNamespace(), Name: it.GetName()}
		b.r.Issuers = append(b.r.Issuers, Issuer{Ref: ref, Type: t, Ready: ready, Reason: reason, Message: msg, Server: server})
		if !ready {
			b.addIssue(Issue{Type: IssueIssuerNotReady, Severity: "warning", Resource: ref,
				Message: fmt.Sprintf("%s %s is not ready: %s", kind, it.GetName(), msg)})
		}
	}
}

func (b *builder) webhooks(cs kubernetes.Interface) {
	add := func(kind, name string, idx int, bundle []byte) {
		if len(bundle) == 0 {
			return
		}
		ref := Ref{APIVersion: "admissionregistration.k8s.io/v1", Kind: kind, Name: name}
		c := &Certificate{ID: fmt.Sprintf("webhook/%s/%s#%d", kind, name, idx), Source: "webhook", Owner: ref, SANs: []string{}, Chain: []ChainEntry{}, UsedBy: []Ref{}}
		if p, err := ParsePEM(bundle); err != nil {
			c.Status, c.ParseError = StatusInvalid, err.Error()
		} else {
			fillFromBundle(c, p, b.now)
		}
		for _, o := range b.order {
			if o.Source == "webhook" && o.Owner.Name == name && o.Owner.Kind == kind && o.FingerprintSHA256 == c.FingerprintSHA256 {
				return // webhooks of one configuration usually share a bundle
			}
		}
		b.order = append(b.order, c)
		if c.Status == StatusExpired || c.Status == StatusCritical || c.Status == StatusWarning {
			sev := "warning"
			if c.Status != StatusWarning {
				sev = "critical"
			}
			verb := fmt.Sprintf("expires in %d days", c.DaysLeft)
			if c.Status == StatusExpired {
				verb = "has expired"
			}
			b.addIssue(Issue{Type: IssueWebhookExpiring, Severity: sev, Resource: ref, CertificateID: c.ID,
				Message: fmt.Sprintf("The CA bundle of %s %s %s; API calls through this webhook will fail", kind, name, verb)})
		}
	}
	if list, err := cs.AdmissionregistrationV1().MutatingWebhookConfigurations().List(b.ctx, metav1.ListOptions{}); err == nil {
		for _, w := range list.Items {
			for i, h := range w.Webhooks {
				add("MutatingWebhookConfiguration", w.Name, i, h.ClientConfig.CABundle)
			}
		}
	} else {
		b.warn("MutatingWebhookConfigurations", err)
	}
	if list, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(b.ctx, metav1.ListOptions{}); err == nil {
		for _, w := range list.Items {
			for i, h := range w.Webhooks {
				add("ValidatingWebhookConfiguration", w.Name, i, h.ClientConfig.CABundle)
			}
		}
	} else {
		b.warn("ValidatingWebhookConfigurations", err)
	}
}

func (b *builder) expiryIssues() {
	for _, c := range b.order {
		if c.Source != "secret" {
			continue
		}
		if c.ParseError != "" {
			b.addIssue(Issue{Type: IssueParseError, Severity: "warning", Resource: c.Owner, CertificateID: c.ID,
				Message: fmt.Sprintf("Secret %s/%s holds no valid certificate: %s", c.Owner.Namespace, c.Owner.Name, c.ParseError)})
			continue
		}
		managed := c.CertManager != nil && c.CertManager.Ready
		switch c.Status {
		case StatusExpired:
			b.addIssue(Issue{Type: IssueExpired, Severity: "critical", Resource: c.Owner, CertificateID: c.ID,
				Message: fmt.Sprintf("Certificate %s in Secret %s/%s expired %d days ago", nameOf(c), c.Owner.Namespace, c.Owner.Name, -c.DaysLeft)})
		case StatusCritical:
			b.addIssue(Issue{Type: IssueExpiring, Severity: "critical", Resource: c.Owner, CertificateID: c.ID,
				Message: fmt.Sprintf("Certificate %s in Secret %s/%s expires in %d days", nameOf(c), c.Owner.Namespace, c.Owner.Name, c.DaysLeft)})
		case StatusWarning:
			if managed {
				continue // cert-manager renews it before expiry
			}
			b.addIssue(Issue{Type: IssueExpiring, Severity: "warning", Resource: c.Owner, CertificateID: c.ID,
				Message: fmt.Sprintf("Certificate %s in Secret %s/%s expires in %d days", nameOf(c), c.Owner.Namespace, c.Owner.Name, c.DaysLeft)})
		}
	}
}

func nameOf(c *Certificate) string {
	if c.CommonName != "" {
		return c.CommonName
	}
	if len(c.SANs) > 0 {
		return c.SANs[0]
	}
	return c.Owner.Name
}

var statusRank = map[Status]int{StatusExpired: 0, StatusCritical: 1, StatusWarning: 2, StatusOK: 3, StatusInvalid: 4}

func (b *builder) finish() {
	out := make([]Certificate, 0, len(b.order))
	for _, c := range b.order {
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if statusRank[a.Status] != statusRank[c.Status] {
			return statusRank[a.Status] < statusRank[c.Status]
		}
		if a.DaysLeft != c.DaysLeft {
			return a.DaysLeft < c.DaysLeft
		}
		return a.ID < c.ID
	})
	b.r.Certificates = out
	if b.r.Issues == nil {
		b.r.Issues = []Issue{}
	}
	if b.r.Issuers == nil {
		b.r.Issuers = []Issuer{}
	}
	sort.SliceStable(b.r.Issues, func(i, j int) bool {
		return b.r.Issues[i].Severity == "critical" && b.r.Issues[j].Severity != "critical"
	})
	b.r.Summary = summarize(out, len(b.r.Issues))
}

func summarize(certs []Certificate, issues int) Summary {
	s := Summary{Total: len(certs), Issues: issues}
	for _, c := range certs {
		switch c.Status {
		case StatusExpired:
			s.Expired++
		case StatusCritical:
			s.Critical++
		case StatusWarning:
			s.Warning++
		case StatusOK:
			s.OK++
		default:
			s.Invalid++
		}
	}
	return s
}

func refMatches(r Ref, want *Ref) bool {
	return strings.EqualFold(r.Kind, want.Kind) && r.Name == want.Name && (want.Namespace == "" || r.Namespace == want.Namespace)
}

func certMatchesResource(c *Certificate, want *Ref) bool {
	if refMatches(c.Owner, want) {
		return true
	}
	for _, u := range c.UsedBy {
		if refMatches(u, want) {
			return true
		}
	}
	return c.CertManager != nil && refMatches(c.CertManager.Certificate, want)
}

func applyFilter(full *Report, f Filter) *Report {
	if f.Namespace == "" && len(f.Statuses) == 0 && f.ExpiringWithinDays == 0 && f.Search == "" && f.Source == "" && f.Resource == nil {
		return full
	}
	out := *full
	out.Certificates = []Certificate{}
	keep := map[string]bool{}
	q := strings.ToLower(strings.TrimSpace(f.Search))
	for i := range full.Certificates {
		c := &full.Certificates[i]
		if f.Namespace != "" && c.Owner.Namespace != f.Namespace {
			continue
		}
		if len(f.Statuses) > 0 && !containsStatus(f.Statuses, c.Status) {
			continue
		}
		if f.ExpiringWithinDays > 0 && (c.NotAfter == nil || c.DaysLeft > f.ExpiringWithinDays) {
			continue
		}
		if f.Source != "" && c.Source != f.Source {
			continue
		}
		if f.Resource != nil && !certMatchesResource(c, f.Resource) {
			continue
		}
		if q != "" && !certMatchesSearch(c, q) {
			continue
		}
		out.Certificates = append(out.Certificates, *c)
		keep[c.ID] = true
	}
	out.Issues = []Issue{}
	for _, is := range full.Issues {
		switch {
		case is.CertificateID != "" && keep[is.CertificateID]:
		case f.Resource != nil && refMatches(is.Resource, f.Resource):
		case is.CertificateID == "" && f.Resource == nil && (f.Namespace == "" || is.Resource.Namespace == f.Namespace):
		default:
			continue
		}
		out.Issues = append(out.Issues, is)
	}
	out.Summary = summarize(out.Certificates, len(out.Issues))
	return &out
}

func containsStatus(ss []Status, s Status) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func certMatchesSearch(c *Certificate, q string) bool {
	fields := []string{c.Subject, c.Issuer, c.Owner.Name, c.Owner.Namespace}
	fields = append(fields, c.SANs...)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}
