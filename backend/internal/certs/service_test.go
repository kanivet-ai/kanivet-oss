package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	admv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func mkCert(t *testing.T, cn string, dns []string, notAfter time.Time, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Org " + cn}},
		DNSNames:              dns,
		NotBefore:             notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:              notAfter,
		IsCA:                  parent == nil,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	signer, signerKey := tpl, key
	if parent != nil {
		signer, signerKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func tlsSecret(ns, name string, crt []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": crt, "tls.key": []byte("-----BEGIN PRIVATE KEY-----\nSECRETKEYMATERIAL\n-----END PRIVATE KEY-----\n")},
	}
}

func TestHostCovered(t *testing.T) {
	cases := []struct {
		host string
		sans []string
		want bool
	}{
		{"a.example.com", []string{"a.example.com"}, true},
		{"A.Example.com", []string{"a.example.com"}, true},
		{"a.example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"*.example.com"}, false},
		{"a.b.example.com", []string{"*.example.com"}, false},
		{"*.example.com", []string{"*.example.com"}, true},
		{"*.example.com", []string{"a.example.com"}, false},
		{"other.com", []string{"a.example.com"}, false},
		{"10.0.0.1", []string{"10.0.0.1"}, true},
	}
	for _, c := range cases {
		if got := HostCovered(c.host, c.sans); got != c.want {
			t.Errorf("HostCovered(%q, %v) = %v, want %v", c.host, c.sans, got, c.want)
		}
	}
}

func TestBucket(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want Status
	}{
		{-time.Hour, StatusExpired},
		{5 * 24 * time.Hour, StatusCritical},
		{20 * 24 * time.Hour, StatusWarning},
		{60 * 24 * time.Hour, StatusOK},
	} {
		if got, _ := bucket(now.Add(c.in), now); got != c.want {
			t.Errorf("bucket(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func gvrMap() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		gvrGateway: "GatewayList", gvrCertificate: "CertificateList", gvrCertRequest: "CertificateRequestList",
		gvrIssuer: "IssuerList", gvrClusterIssuer: "ClusterIssuerList",
	}
}

func u(obj map[string]any) *unstructured.Unstructured { return &unstructured.Unstructured{Object: obj} }

func TestBuildReport(t *testing.T) {
	ca, caKey, caPEM := mkCert(t, "Test CA", nil, now.Add(400*24*time.Hour), nil, nil)
	_, _, leafPEM := mkCert(t, "web.example.com", []string{"web.example.com", "*.apps.example.com"}, now.Add(10*24*time.Hour), ca, caKey)
	_, _, expiredPEM := mkCert(t, "old.example.com", []string{"old.example.com"}, now.Add(-2*24*time.Hour), ca, caKey)
	_, _, okPEM := mkCert(t, "api.example.com", []string{"api.example.com"}, now.Add(80*24*time.Hour), ca, caKey)

	cs := fake.NewSimpleClientset(
		tlsSecret("web", "web-tls", append(append([]byte{}, leafPEM...), caPEM...)),
		tlsSecret("web", "old-tls", expiredPEM),
		tlsSecret("api", "api-tls", okPEM),
		tlsSecret("web", "broken-tls", []byte("not a cert")),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "web", Name: "opaque"}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"tls.crt": okPEM}},
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Namespace: "web", Name: "site"},
			Spec: netv1.IngressSpec{TLS: []netv1.IngressTLS{
				{Hosts: []string{"web.example.com", "x.apps.example.com", "nope.example.org"}, SecretName: "web-tls"},
				{Hosts: []string{"gone.example.com"}, SecretName: "missing-tls"},
			}},
		},
		&admv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "policy"},
			Webhooks: []admv1.ValidatingWebhook{
				{Name: "a.policy", ClientConfig: admv1.WebhookClientConfig{CABundle: expiredPEM}},
				{Name: "b.policy", ClientConfig: admv1.WebhookClientConfig{CABundle: expiredPEM}},
			},
		},
	)

	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrMap(),
		u(map[string]any{
			"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
			"metadata": map[string]any{"namespace": "api", "name": "api"},
			"spec":     map[string]any{"secretName": "api-tls", "issuerRef": map[string]any{"name": "le", "kind": "ClusterIssuer"}, "dnsNames": []any{"api.example.com"}},
			"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "renewalTime": now.Add(50 * 24 * time.Hour).Format(time.RFC3339)},
		}),
		u(map[string]any{
			"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
			"metadata": map[string]any{"namespace": "api", "name": "pending"},
			"spec":     map[string]any{"secretName": "pending-tls", "issuerRef": map[string]any{"name": "le", "kind": "ClusterIssuer"}},
			"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False", "reason": "Issuing", "message": "challenge pending"}}},
		}),
		u(map[string]any{
			"apiVersion": "cert-manager.io/v1", "kind": "CertificateRequest",
			"metadata": map[string]any{"namespace": "api", "name": "pending-1", "annotations": map[string]any{"cert-manager.io/certificate-name": "pending"}},
			"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False", "reason": "Failed"}}},
		}),
		u(map[string]any{
			"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
			"metadata": map[string]any{"name": "le"},
			"spec":     map[string]any{"acme": map[string]any{"server": "https://acme"}},
			"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
		}),
	)

	// The fake tracker would guess "gatewaies" from the kind; create it at
	// the real resource instead.
	if _, err := dyn.Resource(gvrGateway).Namespace("edge").Create(context.Background(), u(map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
		"metadata": map[string]any{"namespace": "edge", "name": "gw"},
		"spec": map[string]any{"listeners": []any{map[string]any{
			"name": "https", "hostname": "api.example.com",
			"tls": map[string]any{"certificateRefs": []any{map[string]any{"name": "api-tls", "namespace": "api"}}},
		}}},
	}), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	r := Build(context.Background(), "c1", cs, dyn, now)

	if !r.CertManagerDetected || !r.GatewayAPIDetected {
		t.Fatalf("detection: cm=%v gw=%v", r.CertManagerDetected, r.GatewayAPIDetected)
	}
	byID := map[string]Certificate{}
	for _, c := range r.Certificates {
		byID[c.ID] = c
	}
	// 4 tls secrets + 1 pending cert-manager cert + 1 deduped webhook bundle.
	if len(r.Certificates) != 6 {
		t.Fatalf("got %d certs: %v", len(r.Certificates), keys(byID))
	}
	if r.Certificates[0].Status != StatusExpired {
		t.Errorf("expired certs should sort first, got %s", r.Certificates[0].Status)
	}

	web := byID["secret/web/web-tls"]
	if web.Status != StatusCritical || web.DaysLeft != 10 {
		t.Errorf("web-tls status=%s days=%d", web.Status, web.DaysLeft)
	}
	if len(web.Chain) != 2 || web.Chain[1].Subject != "Test CA" || !web.Chain[1].SelfSigned {
		t.Errorf("chain = %+v", web.Chain)
	}
	if web.KeyAlgorithm != "ECDSA" || web.KeySize != 256 || web.SelfSigned {
		t.Errorf("key=%s/%d self=%v", web.KeyAlgorithm, web.KeySize, web.SelfSigned)
	}
	if len(web.UsedBy) != 1 || web.UsedBy[0].Kind != "Ingress" {
		t.Errorf("usedBy = %+v", web.UsedBy)
	}

	api := byID["secret/api/api-tls"]
	if api.CertManager == nil || !api.CertManager.Ready || api.CertManager.IssuerRef.Name != "le" {
		t.Errorf("cert-manager link = %+v", api.CertManager)
	}
	if len(api.UsedBy) != 1 || api.UsedBy[0].Kind != "Gateway" {
		t.Errorf("gateway usedBy = %+v", api.UsedBy)
	}
	pending := byID["cert-manager/api/pending"]
	if pending.CertManager == nil || !pending.CertManager.SecretMissing || pending.CertManager.FailedRequests != 1 {
		t.Errorf("pending = %+v", pending.CertManager)
	}
	if byID["secret/web/broken-tls"].Status != StatusInvalid {
		t.Errorf("broken should be invalid")
	}
	if len(r.Issuers) != 1 || r.Issuers[0].Type != "acme" {
		t.Errorf("issuers = %+v", r.Issuers)
	}

	types := map[IssueType]int{}
	for _, is := range r.Issues {
		types[is.Type]++
	}
	want := map[IssueType]int{
		IssueHostMismatch: 1, IssueMissingSecret: 1, IssueExpired: 1, IssueExpiring: 1,
		IssueParseError: 1, IssueNotReady: 1, IssueWebhookExpiring: 1,
	}
	for k, v := range want {
		if types[k] != v {
			t.Errorf("issue %s: got %d want %d (all: %v)", k, types[k], v, types)
		}
	}

	// Never leak private key material.
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "SECRETKEYMATERIAL") || strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("report contains private key material")
	}

	// Filters.
	f := applyFilter(r, Filter{Resource: &Ref{Kind: "Ingress", Namespace: "web", Name: "site"}})
	if len(f.Certificates) != 1 || f.Certificates[0].ID != "secret/web/web-tls" {
		t.Errorf("resource filter certs = %+v", f.Certificates)
	}
	// Host mismatch and missing secret on the Ingress, plus the expiring cert it uses.
	if len(f.Issues) != 3 {
		t.Errorf("resource filter issues = %+v", f.Issues)
	}
	f = applyFilter(r, Filter{ExpiringWithinDays: 14})
	if f.Summary.Total != 3 { // expired leaf, web-tls, expired webhook bundle
		t.Errorf("expiring filter total = %d", f.Summary.Total)
	}
	f = applyFilter(r, Filter{Search: "API.example"})
	if f.Summary.Total != 1 {
		t.Errorf("search total = %d", f.Summary.Total)
	}
}

func keys(m map[string]Certificate) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParseFilter(t *testing.T) {
	q := map[string]string{"status": "expired, critical", "expiringWithin": "30", "kind": "Secret", "name": "x", "resourceNamespace": "ns", "refresh": "1"}
	f := ParseFilter(func(k string) string { return q[k] })
	if len(f.Statuses) != 2 || f.ExpiringWithinDays != 30 || f.Resource == nil || f.Resource.Namespace != "ns" || !f.Refresh {
		t.Errorf("ParseFilter = %+v", f)
	}
}
