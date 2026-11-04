// Package certs inventories the TLS certificates a cluster serves and
// depends on: kubernetes.io/tls Secrets, cert-manager resources, and admission
// webhook CA bundles. Only public certificate material is parsed; private keys
// and other Secret data are never copied into a result.
package certs

import "time"

// Status buckets a certificate by how close it is to expiry.
type Status string

const (
	StatusExpired  Status = "expired"  // notAfter is in the past
	StatusCritical Status = "critical" // expires within CriticalDays
	StatusWarning  Status = "warning"  // expires within WarningDays
	StatusOK       Status = "ok"
	StatusInvalid  Status = "invalid" // the PEM could not be parsed / not issued yet
)

const (
	CriticalDays = 14
	WarningDays  = 30
)

// Ref points at a Kubernetes object so the UI can open it in the inspector.
type Ref struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Namespace  string   `json:"namespace,omitempty"`
	Name       string   `json:"name"`
	Hosts      []string `json:"hosts,omitempty"` // hosts this object serves with the cert
}

// ChainEntry describes one certificate of a PEM bundle, leaf first.
type ChainEntry struct {
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	NotBefore  time.Time `json:"notBefore"`
	NotAfter   time.Time `json:"notAfter"`
	IsCA       bool      `json:"isCA"`
	SelfSigned bool      `json:"selfSigned"`
}

// Condition mirrors a cert-manager status condition.
type Condition struct {
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	Message            string     `json:"message,omitempty"`
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
}

// CertManagerInfo is the cert-manager Certificate that owns a Secret.
type CertManagerInfo struct {
	Certificate     Ref         `json:"certificate"`
	IssuerRef       Ref         `json:"issuerRef"`
	Ready           bool        `json:"ready"`
	Reason          string      `json:"reason,omitempty"`
	Message         string      `json:"message,omitempty"`
	RenewalTime     *time.Time  `json:"renewalTime,omitempty"`
	NotAfter        *time.Time  `json:"notAfter,omitempty"`
	DNSNames        []string    `json:"dnsNames,omitempty"`
	Conditions      []Condition `json:"conditions,omitempty"`
	FailedRequests  int         `json:"failedRequests"`
	PendingRequests int         `json:"pendingRequests"`
	SecretMissing   bool        `json:"secretMissing,omitempty"`
}

// Certificate is one parsed certificate (the leaf of a bundle) and its context.
type Certificate struct {
	ID string `json:"id"` // "<source>/<namespace>/<name>[#<index>]"
	// Source: "secret" (kubernetes.io/tls), "webhook" (caBundle) or
	// "cert-manager" (a Certificate whose Secret does not exist yet).
	Source string `json:"source"`
	Owner  Ref    `json:"owner"` // the object holding the cert

	Subject            string       `json:"subject"`
	CommonName         string       `json:"commonName,omitempty"`
	SANs               []string     `json:"sans"`
	Issuer             string       `json:"issuer"`
	IssuerCommonName   string       `json:"issuerCommonName,omitempty"`
	IssuerOrganization string       `json:"issuerOrganization,omitempty"`
	Chain              []ChainEntry `json:"chain"`
	NotBefore          *time.Time   `json:"notBefore,omitempty"`
	NotAfter           *time.Time   `json:"notAfter,omitempty"`
	DaysLeft           int          `json:"daysLeft"`
	Status             Status       `json:"status"`
	KeyAlgorithm       string       `json:"keyAlgorithm,omitempty"`
	KeySize            int          `json:"keySize,omitempty"`
	SignatureAlgorithm string       `json:"signatureAlgorithm,omitempty"`
	SelfSigned         bool         `json:"selfSigned"`
	IsCA               bool         `json:"isCA"`
	SerialNumber       string       `json:"serialNumber,omitempty"`
	FingerprintSHA256  string       `json:"fingerprintSha256,omitempty"`
	ParseError         string       `json:"parseError,omitempty"`

	UsedBy      []Ref            `json:"usedBy"`
	CertManager *CertManagerInfo `json:"certManager,omitempty"`
}

// IssueType classifies a finding.
type IssueType string

const (
	IssueExpired         IssueType = "expired"
	IssueExpiring        IssueType = "expiring"
	IssueHostMismatch    IssueType = "host-not-covered"
	IssueMissingSecret   IssueType = "missing-secret"
	IssueNotReady        IssueType = "cert-manager-not-ready"
	IssueIssuerNotReady  IssueType = "issuer-not-ready"
	IssueParseError      IssueType = "invalid-certificate"
	IssueWebhookExpiring IssueType = "webhook-ca-expiring"
)

// Issue is a problem worth surfacing (expired cert, uncovered host, ...).
type Issue struct {
	Type          IssueType `json:"type"`
	Severity      string    `json:"severity"` // "critical" | "warning"
	Message       string    `json:"message"`
	Resource      Ref       `json:"resource"`
	CertificateID string    `json:"certificateId,omitempty"`
	Host          string    `json:"host,omitempty"`
}

// Issuer is a cert-manager Issuer or ClusterIssuer.
type Issuer struct {
	Ref     Ref    `json:"ref"`
	Type    string `json:"type"` // acme | ca | selfSigned | vault | venafi | unknown
	Ready   bool   `json:"ready"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Server  string `json:"server,omitempty"` // ACME directory
}

// Summary counts certificates by status.
type Summary struct {
	Total    int `json:"total"`
	Expired  int `json:"expired"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	OK       int `json:"ok"`
	Invalid  int `json:"invalid"`
	Issues   int `json:"issues"`
}

// Report is the result of Certificates.
type Report struct {
	Cluster             string        `json:"cluster"`
	GeneratedAt         time.Time     `json:"generatedAt"`
	Certificates        []Certificate `json:"certificates"`
	Issues              []Issue       `json:"issues"`
	Issuers             []Issuer      `json:"issuers"`
	CertManagerDetected bool          `json:"certManagerDetected"`
	GatewayAPIDetected  bool          `json:"gatewayApiDetected"`
	Summary             Summary       `json:"summary"`
	// Warnings lists sources that could not be read (RBAC, API missing).
	Warnings []string `json:"warnings,omitempty"`
}

// Filter narrows Certificates. Zero values match everything.
type Filter struct {
	Namespace string
	// Statuses keeps only these buckets (e.g. expired, critical).
	Statuses []Status
	// ExpiringWithinDays keeps certs with DaysLeft <= N (includes expired). 0 = off.
	ExpiringWithinDays int
	// Search matches subject, SANs, issuer, or owner name (case-insensitive).
	Search string
	// Source keeps only "secret", "webhook" or "cert-manager".
	Source string
	// Resource keeps certs owned or used by this object (Kind + Namespace + Name).
	Resource *Ref
	// Refresh bypasses the short-lived report cache.
	Refresh bool
}
