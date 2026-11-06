package cis

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/kanivet/backend/internal/clusterinfo"
	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/rbac"
)

// Inputs is everything the checks read. It is plain data so the whole
// benchmark is evaluated in tests without a cluster.
type Inputs struct {
	Provider clusterinfo.Info
	Snap     *health.Snapshot

	Namespaces      []corev1.Namespace
	NamespacesErr   string
	ServiceAccounts []corev1.ServiceAccount
	// RBAC is nil when roles and bindings could not be read.
	RBAC    *rbac.Graph
	RBACErr string

	// AdmissionWebhooks are validating and mutating webhook configuration
	// names; PolicyCount counts ValidatingAdmissionPolicies.
	AdmissionWebhooks []string
	PolicyCount       int
	AdmissionErr      string

	ControlPlane ControlPlane
	Kubelet      KubeletState

	// AWSAuth is kube-system/aws-auth's data when it exists (EKS only).
	AWSAuth      map[string]string
	AWSAuthFound bool
	AWSAuthErr   string

	Scanners []ScannerReport
}

// Outcome is what a check returns before the engine dresses it as a
// ControlResult.
type Outcome struct {
	Status      Status
	Message     string
	ManualCheck string
	Resources   []health.ResourceRef
	Values      []Value
	NodeGroups  []NodeGroup
}

func pass(msg string) Outcome { return Outcome{Status: StatusPass, Message: msg} }
func fail(msg string) Outcome { return Outcome{Status: StatusFail, Message: msg} }
func manual(msg, what string) Outcome {
	return Outcome{Status: StatusManual, Message: msg, ManualCheck: what}
}
func notVisible(msg string) Outcome { return Outcome{Status: StatusNotVisible, Message: msg} }
func errored(msg string) Outcome    { return Outcome{Status: StatusError, Message: msg} }

// check is one reusable piece of logic a profile control points at.
type check struct {
	desc        string
	remediation string
	snippet     string
	// manual is the human check, used when eval is nil or returns manual.
	manual string
	// plane marks control-plane checks: provider-responsibility on managed
	// clusters.
	plane bool
	eval  func(in *Inputs) Outcome
}

var checks = map[string]check{}

func reg(key string, c check) { checks[key] = c }

// userNamespaces are the namespaces the policy checks look at.
func userNamespaces(in *Inputs) []corev1.Namespace {
	var out []corev1.Namespace
	for _, ns := range in.Namespaces {
		if !health.IsSystemNamespace(ns.Name) {
			out = append(out, ns)
		}
	}
	return out
}

func userWorkloads(in *Inputs) []health.Workload {
	if in.Snap == nil {
		return nil
	}
	var out []health.Workload
	for _, w := range health.Workloads(in.Snap) {
		if !health.IsSystemNamespace(w.Ref.Namespace) {
			out = append(out, w)
		}
	}
	return out
}

func nsRef(name string) health.ResourceRef {
	return health.ResourceRef{APIVersion: "v1", Kind: "Namespace", Name: name}
}
