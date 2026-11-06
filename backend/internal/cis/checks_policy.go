package cis

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/rbac"
)

const maxResources = 50

func rbacRef(o rbac.ObjectRef) health.ResourceRef {
	return health.ResourceRef{APIVersion: "rbac.authorization.k8s.io/v1", Kind: o.Kind, Namespace: o.Namespace, Name: o.Name}
}

func dedupeRefs(xs []health.ResourceRef) []health.ResourceRef {
	seen := map[string]bool{}
	var out []health.ResourceRef
	for _, x := range xs {
		if !seen[x.Key()] {
			seen[x.Key()] = true
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func noRBAC(in *Inputs) Outcome {
	msg := "Roles and bindings could not be read"
	if in.RBACErr != "" {
		msg += ": " + in.RBACErr
	}
	return notVisible(msg + ". Grant list on roles, clusterroles, rolebindings and clusterrolebindings.")
}

// riskCheck fails when the rbac package's risky-grant engine reports a
// non-system grant in one of the categories.
func riskCheck(what string, categories ...string) func(in *Inputs) Outcome {
	cats := map[string]bool{}
	for _, c := range categories {
		cats[c] = true
	}
	return func(in *Inputs) Outcome {
		if in.RBAC == nil {
			return noRBAC(in)
		}
		var refs []health.ResourceRef
		subjects := map[string]bool{}
		for _, r := range in.RBAC.RiskyGrants(rbac.RiskOptions{}) {
			if !cats[r.Category] || r.System {
				continue
			}
			refs = append(refs, rbacRef(r.Binding))
			for _, s := range r.Subjects {
				subjects[s.Key()] = true
			}
		}
		refs = dedupeRefs(refs)
		if len(refs) == 0 {
			return pass("No binding outside Kubernetes' own grants " + what + ".")
		}
		return Outcome{Status: StatusFail, Resources: refs,
			Message: fmt.Sprintf("%d binding(s) grant %d subject(s) %s. Review whether each one needs it.", len(refs), len(subjects), what)}
	}
}

// isSystemSubject is a Kubernetes component identity, whose grants come
// with the platform.
func isSystemSubject(s rbac.SubjectRef) bool {
	if s.Kind == rbac.KindServiceAccount {
		return s.Namespace == "kube-system" || strings.HasPrefix(s.Namespace, "kube-") || strings.HasPrefix(s.Namespace, "gke-") || s.Namespace == "openshift" || strings.HasPrefix(s.Namespace, "openshift-")
	}
	return strings.HasPrefix(s.Name, "system:") || strings.HasPrefix(s.Name, "eks:") || strings.HasPrefix(s.Name, "kubeadm:")
}

// whoCanCheck fails when a non-system subject may do any of the queries.
func whoCanCheck(what string, qs ...rbac.Query) func(in *Inputs) Outcome {
	return func(in *Inputs) Outcome {
		if in.RBAC == nil {
			return noRBAC(in)
		}
		var refs []health.ResourceRef
		subjects := map[string]bool{}
		for _, q := range qs {
			res := in.RBAC.WhoCan(q)
			for _, s := range res.Subjects {
				if isSystemSubject(s.Subject) {
					continue
				}
				for _, p := range s.Paths {
					if strings.HasPrefix(p.Binding.Name, "system:") || strings.HasPrefix(p.Binding.Name, "kubeadm:") || strings.HasPrefix(p.Binding.Name, "eks:") {
						continue
					}
					subjects[s.Subject.Key()] = true
					if p.Binding.Kind != "" {
						refs = append(refs, rbacRef(p.Binding))
					}
				}
			}
		}
		refs = dedupeRefs(refs)
		if len(subjects) == 0 {
			return pass("Only Kubernetes' own identities " + what + ".")
		}
		names := make([]string, 0, len(subjects))
		for k := range subjects {
			names = append(names, k)
		}
		sort.Strings(names)
		if len(names) > 8 {
			names = append(names[:8], fmt.Sprintf("and %d more", len(subjects)-8))
		}
		return Outcome{Status: StatusFail, Resources: refs,
			Message: fmt.Sprintf("%d subject(s) %s: %s.", len(subjects), what, strings.Join(names, ", "))}
	}
}

func boolFalse(b *bool) bool { return b != nil && !*b }

func defaultSAs(in *Inputs) Outcome {
	if in.ServiceAccounts == nil {
		return notVisible("Service accounts could not be read.")
	}
	var refs []health.ResourceRef
	for _, sa := range in.ServiceAccounts {
		if sa.Name != "default" || health.IsSystemNamespace(sa.Namespace) {
			continue
		}
		if !boolFalse(sa.AutomountServiceAccountToken) {
			refs = append(refs, health.ResourceRef{APIVersion: "v1", Kind: "ServiceAccount", Namespace: sa.Namespace, Name: sa.Name})
		}
	}
	bound := 0
	if in.RBAC != nil {
		for _, r := range in.RBAC.RiskyGrants(rbac.RiskOptions{}) {
			if r.Category == "default-sa" {
				bound++
				refs = append(refs, rbacRef(r.Binding))
			}
		}
	}
	refs = dedupeRefs(refs)
	if len(refs) == 0 {
		return pass("Every default service account has token automount off and no extra grants.")
	}
	msg := fmt.Sprintf("%d default service account(s) still auto-mount a token", len(refs)-bound)
	if bound > 0 {
		msg += fmt.Sprintf(", %d binding(s) grant permissions to default service accounts", bound)
	}
	return Outcome{Status: StatusFail, Resources: refs, Message: msg + "."}
}

func tokenMount(in *Inputs) Outcome {
	if in.Snap == nil {
		return notVisible("Workloads could not be read.")
	}
	saOff := map[string]bool{}
	for _, sa := range in.ServiceAccounts {
		if boolFalse(sa.AutomountServiceAccountToken) {
			saOff[sa.Namespace+"/"+sa.Name] = true
		}
	}
	var refs []health.ResourceRef
	total := 0
	for _, w := range userWorkloads(in) {
		total++
		if boolFalse(w.Spec.AutomountServiceAccountToken) {
			continue
		}
		sa := w.Spec.ServiceAccountName
		if sa == "" {
			sa = "default"
		}
		if w.Spec.AutomountServiceAccountToken == nil && saOff[w.Ref.Namespace+"/"+sa] {
			continue
		}
		refs = append(refs, w.Ref)
	}
	if len(refs) == 0 {
		return pass(fmt.Sprintf("None of %d workloads mounts a service account token implicitly.", total))
	}
	return Outcome{Status: StatusFail, Resources: refs,
		Message: fmt.Sprintf("%d of %d workloads mount a service account token. Turn it off where the workload never calls the API.", len(refs), total)}
}

// --- Pod Security admission -------------------------------------------------

var psaRank = map[string]int{"privileged": 0, "baseline": 1, "restricted": 2}

// policyEngines are admission webhooks that usually enforce pod security.
var policyEngines = []string{"kyverno", "gatekeeper", "kubewarden", "jspolicy", "polaris", "datree"}

func policyEngine(in *Inputs) string {
	for _, w := range in.AdmissionWebhooks {
		lw := strings.ToLower(w)
		for _, e := range policyEngines {
			if strings.Contains(lw, e) {
				return e
			}
		}
	}
	return ""
}

// enforceLevel is the namespace's Pod Security enforce level, "" when unset.
func enforceLevel(ns corev1.Namespace) string {
	return ns.Labels["pod-security.kubernetes.io/enforce"]
}

// PSACoverage splits namespaces by whether their enforce level is at least
// want ("baseline" or "restricted").
func PSACoverage(nss []corev1.Namespace, want string) (covered, uncovered []string) {
	for _, ns := range nss {
		if lvl, ok := psaRank[enforceLevel(ns)]; ok && lvl >= psaRank[want] {
			covered = append(covered, ns.Name)
		} else {
			uncovered = append(uncovered, ns.Name)
		}
	}
	sort.Strings(covered)
	sort.Strings(uncovered)
	return
}

// podSecurity builds the 5.2.x checks: admission must block the setting in
// every user namespace. bad finds workloads that use it today, as evidence.
func podSecurity(what, level string, bad func(*corev1.PodSpec) bool) func(in *Inputs) Outcome {
	return func(in *Inputs) Outcome {
		if in.Namespaces == nil {
			return notVisible("Namespaces could not be read" + suffix(in.NamespacesErr) + ".")
		}
		_, uncovered := PSACoverage(userNamespaces(in), level)
		var refs []health.ResourceRef
		for _, w := range userWorkloads(in) {
			if bad(w.Spec) {
				refs = append(refs, w.Ref)
			}
		}
		running := ""
		if len(refs) > 0 {
			running = fmt.Sprintf(" %d workload(s) %s today.", len(refs), what)
		}
		if len(uncovered) == 0 {
			o := pass(fmt.Sprintf("Every user namespace enforces Pod Security %q or stricter.%s", level, running))
			o.Resources = refs
			return o
		}
		nsRefs := make([]health.ResourceRef, 0, len(uncovered))
		for _, n := range uncovered {
			nsRefs = append(nsRefs, nsRef(n))
		}
		if e := policyEngine(in); e != "" {
			o := manual(fmt.Sprintf("%d namespace(s) lack a Pod Security %q label, but %s is installed.%s", len(uncovered), level, e, running),
				fmt.Sprintf("Confirm the %s policies reject pods that %s in: %s.", e, what, listNames(uncovered)))
			o.Resources = append(nsRefs, refs...)
			return o
		}
		return Outcome{Status: StatusFail, Resources: append(nsRefs, refs...),
			Message: fmt.Sprintf("%d user namespace(s) do not enforce Pod Security %q, so pods that %s are admitted.%s", len(uncovered), level, what, running)}
	}
}

func listNames(xs []string) string {
	if len(xs) > 10 {
		return strings.Join(xs[:10], ", ") + fmt.Sprintf(" and %d more", len(xs)-10)
	}
	return strings.Join(xs, ", ")
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func allContainers(s *corev1.PodSpec) []corev1.Container {
	out := append([]corev1.Container{}, s.InitContainers...)
	return append(out, s.Containers...)
}

func anyContainer(s *corev1.PodSpec, f func(c corev1.Container) bool) bool {
	for _, c := range allContainers(s) {
		if f(c) {
			return true
		}
	}
	return false
}

func sc(c corev1.Container) *corev1.SecurityContext {
	if c.SecurityContext == nil {
		return &corev1.SecurityContext{}
	}
	return c.SecurityContext
}

func runsAsRoot(s *corev1.PodSpec) bool {
	podNonRoot := s.SecurityContext != nil && s.SecurityContext.RunAsNonRoot != nil && *s.SecurityContext.RunAsNonRoot
	podUID := s.SecurityContext != nil && s.SecurityContext.RunAsUser != nil && *s.SecurityContext.RunAsUser > 0
	return anyContainer(s, func(c corev1.Container) bool {
		x := sc(c)
		if x.RunAsUser != nil {
			return *x.RunAsUser == 0
		}
		if x.RunAsNonRoot != nil {
			return !*x.RunAsNonRoot
		}
		return !podNonRoot && !podUID
	})
}

func dropsAll(c corev1.Container) bool {
	if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil {
		return false
	}
	for _, d := range c.SecurityContext.Capabilities.Drop {
		if strings.EqualFold(string(d), "ALL") {
			return true
		}
	}
	return false
}

func hasCap(c corev1.Container, name string) bool {
	if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil {
		return false
	}
	for _, a := range c.SecurityContext.Capabilities.Add {
		if strings.EqualFold(strings.TrimPrefix(string(a), "CAP_"), name) {
			return true
		}
	}
	return false
}

func secretEnv(s *corev1.PodSpec) bool {
	return anyContainer(s, func(c corev1.Container) bool {
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				return true
			}
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef != nil {
				return true
			}
		}
		return false
	})
}

func seccompSet(s *corev1.PodSpec) bool {
	podOK := s.SecurityContext != nil && s.SecurityContext.SeccompProfile != nil && s.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeUnconfined
	return !anyContainer(s, func(c corev1.Container) bool {
		if c.SecurityContext != nil && c.SecurityContext.SeccompProfile != nil {
			return c.SecurityContext.SeccompProfile.Type == corev1.SeccompProfileTypeUnconfined
		}
		return !podOK
	})
}

func workloadCheck(what, okMsg string, bad func(*corev1.PodSpec) bool) func(in *Inputs) Outcome {
	return func(in *Inputs) Outcome {
		if in.Snap == nil {
			return notVisible("Workloads could not be read.")
		}
		ws := userWorkloads(in)
		var refs []health.ResourceRef
		for _, w := range ws {
			if bad(w.Spec) {
				refs = append(refs, w.Ref)
			}
		}
		if len(refs) == 0 {
			return pass(fmt.Sprintf("%s (%d workloads checked).", okMsg, len(ws)))
		}
		return Outcome{Status: StatusFail, Resources: refs, Message: fmt.Sprintf("%d of %d workloads %s.", len(refs), len(ws), what)}
	}
}

// --- Networking -------------------------------------------------------------

// cniSupport names the CNI and whether it enforces NetworkPolicy: yes,
// no or "" when unknown.
func cniSupport(in *Inputs) (name string, supports string) {
	if in.Snap == nil {
		return "", ""
	}
	type cni struct {
		match, name, supports string
	}
	known := []cni{
		{"cilium", "Cilium", "yes"}, {"calico", "Calico", "yes"}, {"canal", "Canal", "yes"},
		{"antrea", "Antrea", "yes"}, {"weave", "Weave Net", "yes"}, {"kube-router", "kube-router", "yes"},
		{"anetd", "GKE Dataplane V2", "yes"}, {"azure-npm", "Azure NPM", "yes"},
		{"aws-network-policy-agent", "VPC CNI network policy agent", "yes"},
		{"kube-network-policies", "kube-network-policies", "yes"},
		{"flannel", "Flannel", "no"}, {"kindnet", "kindnet", ""}, {"aws-node", "Amazon VPC CNI", ""},
	}
	best := cni{}
	consider := func(s string) {
		s = strings.ToLower(s)
		for _, k := range known {
			if strings.Contains(s, k.match) && (best.name == "" || (best.supports != "yes" && k.supports == "yes")) {
				best = k
			}
		}
	}
	for _, d := range in.Snap.DaemonSets {
		if d.Namespace != "kube-system" && !strings.Contains(d.Namespace, "calico") && !strings.Contains(d.Namespace, "cilium") && !strings.Contains(d.Namespace, "tigera") {
			continue
		}
		consider(d.Name)
		for _, c := range d.Spec.Template.Spec.Containers {
			consider(c.Name)
			if c.Name == "aws-node" {
				for _, e := range c.Env {
					if e.Name == "NETWORK_POLICY_ENFORCING_MODE" || (e.Name == "ENABLE_NETWORK_POLICY" && e.Value == "true") {
						consider("aws-network-policy-agent")
					}
				}
			}
		}
	}
	return best.name, best.supports
}

func cniCheck(in *Inputs) Outcome {
	name, ok := cniSupport(in)
	switch ok {
	case "yes":
		return pass(name + " enforces NetworkPolicy.")
	case "no":
		return fail(name + " does not enforce NetworkPolicy: policies are accepted but have no effect.")
	}
	if name != "" {
		return manual(name+" was found; whether it enforces NetworkPolicy depends on its version and settings.",
			"Confirm the CNI enforces NetworkPolicy (for the VPC CNI, enable its network policy agent; for kindnet, use kind 0.24 or later).")
	}
	return manual("The CNI could not be identified from kube-system DaemonSets.", "Confirm the cluster's CNI enforces NetworkPolicy.")
}

func nsNetpol(in *Inputs) Outcome {
	if in.Namespaces == nil {
		return notVisible("Namespaces could not be read.")
	}
	if in.Snap == nil || contains(in.Snap.Unavailable, "NetworkPolicy") {
		return notVisible("NetworkPolicies could not be read.")
	}
	has := map[string]bool{}
	for _, p := range in.Snap.NetworkPolicies {
		has[p.Namespace] = true
	}
	var refs []health.ResourceRef
	nss := userNamespaces(in)
	for _, ns := range nss {
		if !has[ns.Name] {
			refs = append(refs, nsRef(ns.Name))
		}
	}
	if len(refs) == 0 {
		return pass(fmt.Sprintf("All %d user namespaces have a NetworkPolicy.", len(nss)))
	}
	return Outcome{Status: StatusFail, Resources: refs, Message: fmt.Sprintf("%d of %d user namespaces have no NetworkPolicy.", len(refs), len(nss))}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func policyMechanism(in *Inputs) Outcome {
	if in.Namespaces == nil {
		return notVisible("Namespaces could not be read.")
	}
	covered, _ := PSACoverage(userNamespaces(in), "baseline")
	var parts []string
	if len(covered) > 0 {
		parts = append(parts, fmt.Sprintf("%d namespace(s) enforce Pod Security", len(covered)))
	}
	if e := policyEngine(in); e != "" {
		parts = append(parts, e+" admission webhook installed")
	}
	if in.PolicyCount > 0 {
		parts = append(parts, fmt.Sprintf("%d ValidatingAdmissionPolicies", in.PolicyCount))
	}
	if len(parts) > 0 {
		return pass(strings.Join(parts, "; ") + ".")
	}
	return Outcome{Status: StatusFail, Message: "No namespace enforces Pod Security, and no policy engine or ValidatingAdmissionPolicy was found. A cluster-wide PodSecurity admission configuration on the API server is not visible from the API.",
		ManualCheck: "If the API server has a PodSecurity AdmissionConfiguration with cluster-wide defaults, this control is met."}
}

func defaultNamespace(in *Inputs) Outcome {
	if in.Snap == nil {
		return notVisible("Workloads could not be read.")
	}
	var refs []health.ResourceRef
	for _, w := range health.Workloads(in.Snap) {
		if w.Ref.Namespace == "default" {
			refs = append(refs, w.Ref)
		}
	}
	for _, s := range in.Snap.Services {
		if s.Namespace == "default" && s.Name != "kubernetes" {
			refs = append(refs, health.ResourceRef{APIVersion: "v1", Kind: "Service", Namespace: s.Namespace, Name: s.Name})
		}
	}
	if len(refs) == 0 {
		return pass("Nothing but the kubernetes Service runs in the default namespace.")
	}
	return Outcome{Status: StatusFail, Resources: refs, Message: fmt.Sprintf("%d object(s) live in the default namespace.", len(refs))}
}

func init() {
	reg("rbac.cluster-admin", check{
		desc:        "The cluster-admin role is bound only where it is really needed.",
		remediation: "Replace cluster-admin bindings with narrower roles, or bind cluster-admin through a RoleBinding to limit it to one namespace.",
		snippet:     "kubectl get clusterrolebindings -o wide | grep cluster-admin",
		eval:        riskCheck("cluster-admin", "cluster-admin"),
	})
	reg("rbac.secrets", check{
		desc:        "Few identities can get, list or watch Secrets.",
		remediation: "Drop get/list/watch on secrets from roles that do not need it; prefer resourceNames for the specific Secrets a workload reads.",
		snippet:     "rules:\n- apiGroups: [\"\"]\n  resources: [\"secrets\"]\n  resourceNames: [\"my-app-config\"]\n  verbs: [\"get\"]",
		eval:        riskCheck("read access to Secrets", "secrets"),
	})
	reg("rbac.wildcard", check{
		desc:        "Roles avoid \"*\" in verbs, resources and API groups.",
		remediation: "Spell out the verbs and resources each role needs instead of wildcards.",
		snippet:     "rules:\n- apiGroups: [\"apps\"]\n  resources: [\"deployments\"]\n  verbs: [\"get\", \"list\", \"watch\"]",
		eval:        riskCheck("wildcard permissions", "wildcard"),
	})
	reg("rbac.create-pods", check{
		desc:        "Few identities can create pods, since that implies the rights of any service account in the namespace.",
		remediation: "Let controllers (Deployments, Jobs) create pods; remove pods/create from human and CI roles where possible.",
		eval:        whoCanCheck("can create pods", rbac.Query{Verb: "create", Group: "", Resource: "pods"}),
	})
	reg("rbac.default-sa", check{
		desc:        "Default service accounts are not used: they carry no grants and do not auto-mount a token.",
		remediation: "Set automountServiceAccountToken: false on each namespace's default service account and give workloads their own accounts.",
		snippet:     "apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: default\nautomountServiceAccountToken: false",
		eval:        defaultSAs,
	})
	reg("rbac.token-mount", check{
		desc:        "Service account tokens are mounted only into pods that call the API.",
		remediation: "Set automountServiceAccountToken: false in the pod spec (or on the service account) for workloads that do not talk to the API server.",
		snippet:     "spec:\n  template:\n    spec:\n      automountServiceAccountToken: false",
		eval:        tokenMount,
	})
	reg("rbac.system-masters", check{
		desc:   "The system:masters group is not used for day-to-day access.",
		manual: "Membership of system:masters lives in client certificates and the authenticator, not in RBAC objects. Check who holds certificates with O=system:masters (and, on EKS, aws-auth or access-entry mappings to it).",
	})
	reg("rbac.escalate", check{
		desc:        "Few identities can bind, escalate or impersonate.",
		remediation: "Remove bind, escalate and impersonate verbs from roles outside cluster administration.",
		eval:        riskCheck("bind, escalate or impersonate rights", "escalation", "impersonation"),
	})
	reg("rbac.create-pv", check{
		desc:        "Few identities can create PersistentVolumes, which can expose host paths.",
		remediation: "Grant persistentvolumes/create only to storage administrators and provisioners.",
		eval:        whoCanCheck("can create PersistentVolumes", rbac.Query{Verb: "create", Group: "", Resource: "persistentvolumes"}),
	})
	reg("rbac.nodes-proxy", check{
		desc:        "Few identities can use the nodes/proxy subresource, which reaches the kubelet API directly.",
		remediation: "Remove nodes/proxy from roles that do not need direct kubelet access (monitoring agents usually only need nodes/metrics).",
		eval:        riskCheck("nodes/proxy", "nodes"),
	})
	reg("rbac.csr-approve", check{
		desc:        "Few identities can approve certificate signing requests.",
		remediation: "Limit update on certificatesigningrequests/approval to cluster administrators and the approving controller.",
		eval:        whoCanCheck("can approve certificate requests", rbac.Query{Verb: "update", Group: "certificates.k8s.io", Resource: "certificatesigningrequests", Subresource: "approval"}),
	})
	reg("rbac.webhooks", check{
		desc:        "Few identities can change admission webhook configurations.",
		remediation: "Limit create, update and delete on validating and mutating webhook configurations to cluster administrators.",
		eval: whoCanCheck("can change admission webhooks",
			rbac.Query{Verb: "update", Group: "admissionregistration.k8s.io", Resource: "validatingwebhookconfigurations"},
			rbac.Query{Verb: "update", Group: "admissionregistration.k8s.io", Resource: "mutatingwebhookconfigurations"}),
	})
	reg("rbac.token-create", check{
		desc:        "Few identities can mint service account tokens.",
		remediation: "Remove serviceaccounts/token create from roles that do not issue tokens.",
		eval:        riskCheck("token creation rights", "tokens"),
	})

	reg("psa.mechanism", check{
		desc:        "At least one admission mechanism enforces pod security (Pod Security admission, a policy engine or ValidatingAdmissionPolicy).",
		remediation: "Label namespaces with a Pod Security enforce level, or install a policy engine.",
		snippet:     "kubectl label namespace my-app pod-security.kubernetes.io/enforce=baseline",
		eval:        policyMechanism,
	})
	psa := func(key, what, level, desc string, bad func(*corev1.PodSpec) bool) {
		reg(key, check{
			desc:        desc,
			remediation: fmt.Sprintf("Enforce Pod Security %q (or stricter) on every user namespace, or add an equivalent policy-engine rule.", level),
			snippet:     fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: my-app\n  labels:\n    pod-security.kubernetes.io/enforce: %s\n    pod-security.kubernetes.io/enforce-version: latest", level),
			eval:        podSecurity(what, level, bad),
		})
	}
	psa("psa.privileged", "run privileged containers", "baseline", "Admission rejects privileged containers.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool { return sc(c).Privileged != nil && *sc(c).Privileged })
	})
	psa("psa.host-pid", "share the host PID namespace", "baseline", "Admission rejects pods that share the host PID namespace.", func(s *corev1.PodSpec) bool { return s.HostPID })
	psa("psa.host-ipc", "share the host IPC namespace", "baseline", "Admission rejects pods that share the host IPC namespace.", func(s *corev1.PodSpec) bool { return s.HostIPC })
	psa("psa.host-network", "use the host network", "baseline", "Admission rejects pods on the host network.", func(s *corev1.PodSpec) bool { return s.HostNetwork })
	psa("psa.privilege-escalation", "allow privilege escalation", "restricted", "Admission rejects containers that allow privilege escalation.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool {
			x := sc(c)
			return x.AllowPrivilegeEscalation == nil || *x.AllowPrivilegeEscalation
		})
	})
	psa("psa.root", "may run as root", "restricted", "Admission rejects containers that may run as root.", runsAsRoot)
	psa("psa.net-raw", "keep NET_RAW", "restricted", "Admission rejects containers that keep the NET_RAW capability.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool { return !dropsAll(c) || hasCap(c, "NET_RAW") })
	})
	psa("psa.added-caps", "add capabilities", "baseline", "Admission rejects containers that add capabilities beyond the runtime default.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool {
			if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil {
				return false
			}
			for _, a := range c.SecurityContext.Capabilities.Add {
				if !strings.EqualFold(string(a), "NET_BIND_SERVICE") {
					return true
				}
			}
			return false
		})
	})
	psa("psa.caps", "do not drop all capabilities", "restricted", "Admission requires containers to drop all capabilities.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool { return !dropsAll(c) })
	})
	psa("psa.host-process", "run as Windows HostProcess", "baseline", "Admission rejects Windows HostProcess containers.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool {
			x := sc(c)
			return x.WindowsOptions != nil && x.WindowsOptions.HostProcess != nil && *x.WindowsOptions.HostProcess
		})
	})
	psa("psa.host-path", "mount hostPath volumes", "baseline", "Admission rejects hostPath volumes.", func(s *corev1.PodSpec) bool {
		for _, v := range s.Volumes {
			if v.HostPath != nil {
				return true
			}
		}
		return false
	})
	psa("psa.host-ports", "bind host ports", "baseline", "Admission rejects containers that bind host ports.", func(s *corev1.PodSpec) bool {
		return anyContainer(s, func(c corev1.Container) bool {
			for _, p := range c.Ports {
				if p.HostPort != 0 {
					return true
				}
			}
			return false
		})
	})

	reg("net.cni", check{
		desc:        "The CNI plugin enforces NetworkPolicy.",
		remediation: "Use a CNI that enforces NetworkPolicy (Cilium, Calico, Antrea), or enable policy support in the one you run.",
		eval:        cniCheck,
	})
	reg("net.namespaces", check{
		desc:        "Every user namespace has at least one NetworkPolicy.",
		remediation: "Start each namespace with a default-deny ingress policy and allow what is needed on top.",
		snippet:     "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: default-deny-ingress\nspec:\n  podSelector: {}\n  policyTypes: [Ingress]",
		eval:        nsNetpol,
	})
	reg("secrets.env", check{
		desc:        "Secrets reach containers as mounted files, not environment variables.",
		remediation: "Mount the Secret as a volume and read the file; environment variables leak into logs, crash dumps and child processes.",
		snippet:     "volumes:\n- name: creds\n  secret:\n    secretName: my-secret\ncontainers:\n- name: app\n  volumeMounts:\n  - name: creds\n    mountPath: /var/run/secrets/app\n    readOnly: true",
		eval:        workloadCheck("read Secrets through environment variables", "No workload reads Secrets through environment variables", secretEnv),
	})
	reg("secrets.external", check{
		desc:   "Secrets are stored in an external secret manager.",
		manual: "Check whether secrets come from an external store (Vault, AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) through the Secrets Store CSI driver or External Secrets Operator.",
	})
	reg("admission.images", check{
		desc:   "Image provenance is enforced at admission.",
		manual: "Check for an admission control that only admits signed or allow-listed images (Kyverno verifyImages, Sigstore policy-controller, Gatekeeper, ImagePolicyWebhook).",
	})
	reg("general.namespaces", check{
		desc:   "Namespaces separate teams and applications.",
		manual: "Review that unrelated applications and teams run in separate namespaces with their own RBAC and quotas.",
	})
	reg("general.seccomp", check{
		desc:        "Pods run with the RuntimeDefault (or a custom) seccomp profile.",
		remediation: "Set seccompProfile.type: RuntimeDefault in the pod security context, or enable the kubelet's seccompDefault.",
		snippet:     "spec:\n  securityContext:\n    seccompProfile:\n      type: RuntimeDefault",
		eval:        workloadCheck("run without a seccomp profile", "Every workload sets a seccomp profile", func(s *corev1.PodSpec) bool { return !seccompSet(s) }),
	})
	reg("general.security-context", check{
		desc:   "Pods and containers set a security context.",
		manual: "Review that workloads set runAsNonRoot, readOnlyRootFilesystem, dropped capabilities and a seccomp profile. The Audit view's security checks list the gaps per workload.",
	})
	reg("general.default-ns", check{
		desc:        "The default namespace is not used for workloads.",
		remediation: "Move workloads into their own namespaces.",
		eval:        defaultNamespace,
	})
}
