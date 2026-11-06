// Package cis evaluates a cluster against the CIS Kubernetes Benchmark
// profile that fits its provider (generic/kubeadm, EKS, GKE, AKS, with
// k3s/RKE2 and OpenShift mappings). Every check is read-only. Controls are
// identified by their CIS number; titles, descriptions and remediation notes
// are Kanivet's own words, never the benchmark text. See README.md.
package cis

import (
	"time"

	"github.com/kanivet/backend/internal/clusterinfo"
	"github.com/kanivet/backend/internal/health"
)

// Status of one control.
type Status string

const (
	StatusPass Status = "automated-pass"
	StatusFail Status = "automated-fail"
	// StatusManual needs a human: ManualCheck says what to look at.
	StatusManual Status = "manual"
	// StatusProvider is the managed provider's job (control plane on EKS,
	// GKE, AKS).
	StatusProvider Status = "provider-responsibility"
	// StatusNotVisible needs access Kanivet does not have (node files, a
	// denied nodes/proxy). It never reads as a pass.
	StatusNotVisible Status = "not-visible"
	StatusError      Status = "error"
)

// AllStatuses in display order.
var AllStatuses = []Status{StatusFail, StatusPass, StatusManual, StatusNotVisible, StatusProvider, StatusError}

// Automated reports whether s is a measured pass or fail.
func (s Status) Automated() bool { return s == StatusPass || s == StatusFail }

// Value is one observed setting (a flag, a kubelet field).
type Value struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"` // pod or node it came from
	OK     bool   `json:"ok"`
}

// NodeGroup is the kubelet result for nodes that share a pool and an
// identical configuration.
type NodeGroup struct {
	Pool       string   `json:"pool"`
	Nodes      []string `json:"nodes"`      // sampled nodes read
	TotalNodes int      `json:"totalNodes"` // nodes in the pool
	Value      string   `json:"value"`
	OK         bool     `json:"ok"`
	Error      string   `json:"error,omitempty"`
}

// ScannerFinding is a scanner's verdict on one control.
type ScannerFinding struct {
	Source    string    `json:"source"` // trivy-operator, kubescape, kube-bench
	Report    string    `json:"report,omitempty"`
	Status    Status    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	ScannedAt time.Time `json:"scannedAt,omitempty"`
}

// ControlResult is one evaluated control.
type ControlResult struct {
	ID           string `json:"id"`
	Section      string `json:"section"`
	SectionTitle string `json:"sectionTitle"`
	// Subsection is the two-level group ("5.1").
	Subsection      string `json:"subsection"`
	SubsectionTitle string `json:"subsectionTitle"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	// Scored is CIS's "Automated" assessment flag (older versions: Scored).
	Scored bool   `json:"scored"`
	Level  int    `json:"level"`
	Status Status `json:"status"`
	// Source is "built-in" or the scanner that decided the status.
	Source  string `json:"source"`
	Message string `json:"message,omitempty"`
	// ManualCheck says what a human should verify.
	ManualCheck string               `json:"manualCheck,omitempty"`
	Resources   []health.ResourceRef `json:"resources,omitempty"`
	// ResourcesTotal counts failing resources when Resources is capped.
	ResourcesTotal int              `json:"resourcesTotal,omitempty"`
	Values         []Value          `json:"values,omitempty"`
	NodeGroups     []NodeGroup      `json:"nodeGroups,omitempty"`
	Remediation    string           `json:"remediation,omitempty"`
	Snippet        string           `json:"snippet,omitempty"`
	Scanners       []ScannerFinding `json:"scanners,omitempty"`
	// Disagreement is set when a scanner's verdict differs from ours.
	Disagreement bool `json:"disagreement,omitempty"`
}

// SectionSummary scores one benchmark section.
type SectionSummary struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Score  *int           `json:"score"` // nil when nothing in it is automated
	Counts map[Status]int `json:"counts"`
}

// ProfileInfo names a benchmark profile.
type ProfileInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// ScannerSource is a scanner report that was read.
type ScannerSource struct {
	Source    string    `json:"source"`
	Report    string    `json:"report"`
	Profile   string    `json:"profile,omitempty"`
	Controls  int       `json:"controls"`
	ScannedAt time.Time `json:"scannedAt,omitempty"`
	// Used is false when the report is for another profile.
	Used bool `json:"used"`
}

// Report is a full benchmark run.
type Report struct {
	Cluster     string           `json:"cluster"`
	Provider    clusterinfo.Info `json:"provider"`
	Profile     ProfileInfo      `json:"profile"`
	Profiles    []ProfileInfo    `json:"profiles"`
	AutoProfile string           `json:"autoProfile"`
	GeneratedAt time.Time        `json:"generatedAt"`
	// Score is passes over automated (pass+fail) controls, 0..100; nil when
	// none are automated.
	Score    *int             `json:"score"`
	Counts   map[Status]int   `json:"counts"`
	Sections []SectionSummary `json:"sections"`
	Controls []ControlResult  `json:"controls"`
	Scanners []ScannerSource  `json:"scanners"`
	// Notes explain degraded checks ("grant nodes/proxy to see kubelet checks").
	Notes []Note `json:"notes"`
}

// Note is one degradation or caveat.
type Note struct {
	Kind string `json:"kind"` // rbac, provider, scanner, info
	Text string `json:"text"`
}
