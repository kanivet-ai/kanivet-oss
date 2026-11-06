package cis

import (
	"strings"

	"github.com/kanivet/backend/internal/clusterinfo"
)

// Profile IDs.
const (
	ProfileK8s       = "cis-k8s"
	ProfileEKS       = "cis-eks"
	ProfileGKE       = "cis-gke"
	ProfileAKS       = "cis-aks"
	ProfileK3s       = "k3s-hardening"
	ProfileRKE2      = "rke2-hardening"
	ProfileOpenShift = "openshift-policies"
)

// control is one catalog entry: the CIS number, our short title and the
// check that evaluates it.
type control struct {
	id     string
	key    string
	title  string
	scored bool
	level  int
}

type profile struct {
	info     ProfileInfo
	sections map[string]string // "5" and "5.1" -> title
	controls []control
	// notes are shown above the controls.
	notes []string
}

func c(id, key, title string, scored bool) control {
	return control{id: id, key: key, title: title, scored: scored, level: 1}
}

func c2(id, key, title string, scored bool) control {
	return control{id: id, key: key, title: title, scored: scored, level: 2}
}

// policyControls are the workload-policy controls; generic and managed
// benchmarks number them under different prefixes.
func policyControls(p string, eks bool) []control {
	out := []control{
		c(p+".1.1", "rbac.cluster-admin", "cluster-admin bound only where needed", false),
		c(p+".1.2", "rbac.secrets", "Minimal access to Secrets", false),
		c(p+".1.3", "rbac.wildcard", "Minimal wildcards in Roles and ClusterRoles", false),
		c(p+".1.4", "rbac.create-pods", "Minimal access to create pods", false),
		c(p+".1.5", "rbac.default-sa", "Default service accounts not used", true),
		c(p+".1.6", "rbac.token-mount", "Service account tokens mounted only where needed", false),
	}
	if eks {
		out = append(out,
			c(p+".1.7", "eks.access", "EKS access entries used instead of aws-auth", false),
			c(p+".1.8", "rbac.escalate", "Minimal bind, impersonate and escalate rights", false),
		)
	} else {
		out = append(out,
			c(p+".1.7", "rbac.system-masters", "system:masters group not used", false),
			c(p+".1.8", "rbac.escalate", "Minimal bind, impersonate and escalate rights", false),
		)
		if p == "5" {
			out = append(out,
				c(p+".1.9", "rbac.create-pv", "Minimal access to create PersistentVolumes", false),
				c(p+".1.10", "rbac.nodes-proxy", "Minimal access to nodes/proxy", false),
				c(p+".1.11", "rbac.csr-approve", "Minimal access to approve CSRs", false),
				c(p+".1.12", "rbac.webhooks", "Minimal access to webhook configurations", false),
				c(p+".1.13", "rbac.token-create", "Minimal access to create service account tokens", false),
			)
		}
	}
	ps := []control{
		c(p+".2.1", "psa.mechanism", "A pod security admission mechanism is in place", false),
		c(p+".2.2", "psa.privileged", "Privileged containers not admitted", false),
		c(p+".2.3", "psa.host-pid", "Host PID namespace not admitted", true),
		c(p+".2.4", "psa.host-ipc", "Host IPC namespace not admitted", true),
		c(p+".2.5", "psa.host-network", "Host network not admitted", true),
		c(p+".2.6", "psa.privilege-escalation", "Privilege escalation not admitted", true),
		c2(p+".2.7", "psa.root", "Root containers not admitted", false),
		c(p+".2.8", "psa.net-raw", "NET_RAW capability not admitted", false),
		c(p+".2.9", "psa.added-caps", "Added capabilities not admitted", false),
		c2(p+".2.10", "psa.caps", "Containers drop all capabilities", false),
	}
	if p == "5" {
		ps = append(ps,
			c(p+".2.11", "psa.host-process", "Windows HostProcess containers not admitted", false),
			c(p+".2.12", "psa.host-path", "hostPath volumes not admitted", false),
			c(p+".2.13", "psa.host-ports", "Host ports not admitted", false),
		)
	}
	out = append(out, ps...)
	out = append(out,
		c(p+".3.1", "net.cni", "CNI enforces NetworkPolicy", false),
		c2(p+".3.2", "net.namespaces", "Every namespace has a NetworkPolicy", false),
		c2(p+".4.1", "secrets.env", "Secrets as files, not environment variables", false),
		c2(p+".4.2", "secrets.external", "External secret storage", false),
	)
	if p == "5" {
		out = append(out,
			c2(p+".5.1", "admission.images", "Image provenance enforced at admission", false),
			c(p+".7.1", "general.namespaces", "Namespaces separate resources", false),
			c2(p+".7.2", "general.seccomp", "Seccomp profile set on pods", false),
			c2(p+".7.3", "general.security-context", "Security context applied to pods and containers", false),
			c2(p+".7.4", "general.default-ns", "Default namespace not used", false),
		)
	} else {
		gen := p + ".5"
		if eks {
			gen = p + ".6"
			out = append(out, c2(p+".5.1", "admission.images", "Image provenance enforced at admission", false))
		}
		out = append(out,
			c(gen+".1", "general.namespaces", "Namespaces separate resources", false),
			c2(gen+".2", "general.seccomp", "Seccomp profile set on pods", false),
			c2(gen+".3", "general.security-context", "Security context applied to pods and containers", false),
			c2(gen+".4", "general.default-ns", "Default namespace not used", false),
		)
	}
	return out
}

var policySections = func(p string) map[string]string {
	return map[string]string{
		p: "Policies", p + ".1": "RBAC and service accounts", p + ".2": "Pod Security",
		p + ".3": "Network policies and CNI", p + ".4": "Secrets management", p + ".5": "Extensible admission control",
		p + ".6": "General policies", p + ".7": "General policies",
	}
}

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// kubeletControls are the kubelet configuration controls under prefix.
func kubeletControls(p string, full bool) []control {
	out := []control{
		c(p+".1", "kubelet.anonymous-auth", "Kubelet anonymous auth disabled", true),
		c(p+".2", "kubelet.authz-mode", "Kubelet authorization mode is not AlwaysAllow", true),
		c(p+".3", "kubelet.client-ca", "Kubelet client CA set", true),
		c(p+".4", "kubelet.read-only-port", "Kubelet read-only port disabled", true),
		c(p+".5", "kubelet.streaming-timeout", "Streaming connection idle timeout set", true),
	}
	if full {
		out = append(out,
			c(p+".6", "kubelet.protect-kernel-defaults", "Kubelet protects kernel defaults", true),
			c(p+".7", "kubelet.iptables-chains", "Kubelet manages iptables util chains", true),
			c(p+".8", "kubelet.hostname-override", "Kubelet hostname not overridden", false),
			c2(p+".9", "kubelet.event-qps", "Event record QPS allows event capture", false),
			c(p+".10", "kubelet.tls-cert", "Kubelet TLS certificate and key set", false),
			c(p+".11", "kubelet.rotate-certs", "Kubelet client certificate rotation on", true),
			c(p+".12", "kubelet.rotate-server-cert", "Kubelet serving certificate rotation on", true),
			c(p+".13", "kubelet.tls-ciphers", "Kubelet uses strong TLS ciphers", false),
		)
		return out
	}
	return append(out,
		c(p+".6", "kubelet.iptables-chains", "Kubelet manages iptables util chains", true),
		c2(p+".7", "kubelet.event-qps", "Event record QPS allows event capture", false),
		c(p+".8", "kubelet.rotate-certs", "Kubelet client certificate rotation on", true),
		c(p+".9", "kubelet.rotate-server-cert", "Kubelet serving certificate rotation on", true),
	)
}

func workerFiles(p string, n int) []control {
	titles := []string{
		"Kubelet service file permissions", "Kubelet service file ownership",
		"kube-proxy kubeconfig permissions", "kube-proxy kubeconfig ownership",
		"Kubelet kubeconfig permissions", "Kubelet kubeconfig ownership",
		"Client CA file permissions", "Client CA file ownership",
		"Kubelet config file permissions", "Kubelet config file ownership",
	}
	var out []control
	for i := 0; i < n && i < len(titles); i++ {
		out = append(out, c(p+"."+itoa(i+1), "files.worker", titles[i], true))
	}
	return out
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func genericProfile() profile {
	cpFiles := []string{
		"API server manifest permissions", "API server manifest ownership",
		"Controller manager manifest permissions", "Controller manager manifest ownership",
		"Scheduler manifest permissions", "Scheduler manifest ownership",
		"etcd manifest permissions", "etcd manifest ownership",
		"CNI file permissions", "CNI file ownership",
		"etcd data directory permissions", "etcd data directory ownership",
		"admin.conf permissions", "admin.conf ownership",
		"scheduler.conf permissions", "scheduler.conf ownership",
		"controller-manager.conf permissions", "controller-manager.conf ownership",
		"PKI directory ownership", "PKI certificate permissions", "PKI key permissions",
	}
	var cs []control
	for i, t := range cpFiles {
		cs = append(cs, c("1.1."+itoa(i+1), "files.control-plane", t, true))
	}
	cs = append(cs,
		c("1.2.1", "api.anonymous-auth", "API server anonymous auth disabled", false),
		c("1.2.2", "api.token-auth-file", "No static token file", true),
		c("1.2.3", "api.deny-external-ips", "DenyServiceExternalIPs enabled", false),
		c("1.2.4", "api.kubelet-client-cert", "Kubelet client certificate and key set", true),
		c("1.2.5", "api.kubelet-ca", "Kubelet certificate authority set", true),
		c("1.2.6", "api.authz-not-always-allow", "Authorization mode is not AlwaysAllow", true),
		c("1.2.7", "api.authz-node", "Authorization mode includes Node", true),
		c("1.2.8", "api.authz-rbac", "Authorization mode includes RBAC", true),
		c("1.2.9", "api.event-rate-limit", "EventRateLimit admission plugin set", false),
		c("1.2.10", "api.always-admit", "AlwaysAdmit admission plugin not set", true),
		c("1.2.11", "api.always-pull", "AlwaysPullImages admission plugin set", false),
		c("1.2.12", "api.service-account", "ServiceAccount admission plugin set", true),
		c("1.2.13", "api.namespace-lifecycle", "NamespaceLifecycle admission plugin set", true),
		c("1.2.14", "api.node-restriction", "NodeRestriction admission plugin set", true),
		c("1.2.15", "api.profiling", "API server profiling disabled", true),
		c("1.2.16", "api.audit-log-path", "Audit log path set", true),
		c("1.2.17", "api.audit-log-maxage", "Audit log max age at least 30", true),
		c("1.2.18", "api.audit-log-maxbackup", "Audit log max backup at least 10", true),
		c("1.2.19", "api.audit-log-maxsize", "Audit log max size at least 100", true),
		c("1.2.20", "api.request-timeout", "Request timeout appropriate", false),
		c("1.2.21", "api.sa-lookup", "Service account lookup on", true),
		c("1.2.22", "api.sa-key-file", "Service account key file set", true),
		c("1.2.23", "api.etcd-cert", "etcd client certificate and key set", true),
		c("1.2.24", "api.tls-cert", "API server TLS certificate and key set", true),
		c("1.2.25", "api.client-ca", "Client CA file set", true),
		c("1.2.26", "api.etcd-ca", "etcd CA file set", true),
		c("1.2.27", "api.encryption-config", "Encryption provider config set", false),
		c("1.2.28", "api.encryption-providers", "Encryption providers appropriate", false),
		c("1.2.29", "api.tls-ciphers", "API server uses strong TLS ciphers", false),
		c("1.3.1", "cm.terminated-pod-gc", "Terminated pod GC threshold set", false),
		c("1.3.2", "cm.profiling", "Controller manager profiling disabled", true),
		c("1.3.3", "cm.sa-credentials", "Per-controller service account credentials", true),
		c("1.3.4", "cm.sa-private-key", "Service account private key file set", true),
		c("1.3.5", "cm.root-ca", "Root CA file set", true),
		c2("1.3.6", "cm.rotate-server-cert", "RotateKubeletServerCertificate not disabled", true),
		c("1.3.7", "cm.bind-address", "Controller manager bound to localhost", true),
		c("1.4.1", "sch.profiling", "Scheduler profiling disabled", true),
		c("1.4.2", "sch.bind-address", "Scheduler bound to localhost", true),
		c("2.1", "etcd.cert", "etcd TLS certificate and key set", true),
		c("2.2", "etcd.client-cert-auth", "etcd client certificate auth on", true),
		c("2.3", "etcd.auto-tls", "etcd auto TLS off", true),
		c("2.4", "etcd.peer-cert", "etcd peer TLS certificate and key set", true),
		c("2.5", "etcd.peer-client-cert-auth", "etcd peer client certificate auth on", true),
		c("2.6", "etcd.peer-auto-tls", "etcd peer auto TLS off", true),
		c2("2.7", "etcd.unique-ca", "etcd uses a unique CA", false),
		c("3.1.1", "authn.client-certs", "No client certificate auth for users", false),
		c("3.1.2", "authn.sa-tokens", "No service account tokens for users", false),
		c("3.1.3", "authn.bootstrap", "No bootstrap tokens for users", false),
		c("3.2.1", "audit.policy", "Minimal audit policy created", false),
		c2("3.2.2", "audit.coverage", "Audit policy covers key concerns", false),
	)
	cs = append(cs, workerFiles("4.1", 10)...)
	cs = append(cs, kubeletControls("4.2", true)...)
	cs = append(cs, policyControls("5", false)...)
	return profile{
		info: ProfileInfo{ID: ProfileK8s, Title: "CIS Kubernetes", Version: "1.9 numbering",
			Description: "Self-managed clusters (kubeadm and similar): control plane, etcd, worker nodes and policies."},
		sections: merge(map[string]string{
			"1": "Control plane components", "1.1": "Control plane node files", "1.2": "API server", "1.3": "Controller manager", "1.4": "Scheduler",
			"2": "etcd", "3": "Control plane configuration", "3.1": "Authentication and authorization", "3.2": "Logging",
			"4": "Worker nodes", "4.1": "Worker node files", "4.2": "Kubelet",
		}, policySections("5")),
		controls: cs,
	}
}

// managedPlane lists the generic control-plane controls a managed provider
// owns, so the report still shows them (as provider-responsibility).
func managedPlane(id string) []control {
	return []control{
		c(id+".1", "api.anonymous-auth", "API server configuration managed by the provider", false),
		c(id+".2", "etcd.cert", "etcd managed by the provider", false),
		c(id+".3", "cm.profiling", "Controller manager and scheduler managed by the provider", false),
	}
}

func eksProfile() profile {
	cs := managedPlane("1.1")
	cs = append(cs,
		c("2.1.1", "eks.audit-logs", "Control plane audit logs enabled", false),
	)
	cs = append(cs, workerFiles("3.1", 4)...)
	cs = append(cs, kubeletControls("3.2", false)...)
	cs = append(cs, policyControls("4", true)...)
	cs = append(cs,
		c("5.1.1", "eks.ecr-scan", "ECR image scanning on push", false),
		c("5.1.2", "eks.ecr-access", "Minimal user access to ECR", false),
		c("5.1.3", "eks.ecr-readonly", "Cluster has read-only ECR access", false),
		c("5.3.1", "eks.kms", "Secrets encrypted with a KMS key", false),
		c("5.4.1", "eks.endpoint", "Restricted access to the control plane endpoint", false),
		c("5.4.2", "eks.private-endpoint", "Private endpoint, public access off", false),
		c("5.4.3", "eks.private-nodes", "Nodes in private subnets", false),
		c("5.4.5", "eks.lb-tls", "TLS on load balancers", false),
		c("5.5.1", "eks.iam-users", "Users managed through IAM or an identity provider", false),
	)
	return profile{
		info: ProfileInfo{ID: ProfileEKS, Title: "CIS Amazon EKS", Version: "1.5 numbering",
			Description: "EKS: AWS runs the control plane; worker nodes, policies and the EKS-specific configuration are yours."},
		sections: merge(map[string]string{
			"1": "Control plane (managed)", "1.1": "Control plane components", "2": "Control plane configuration", "2.1": "Logging",
			"3": "Worker nodes", "3.1": "Worker node files", "3.2": "Kubelet",
			"5": "Managed services", "5.1": "Image registry and scanning", "5.3": "Key management", "5.4": "Cluster networking", "5.5": "Authentication",
		}, policySections("4")),
		controls: cs,
	}
}

func gkeProfile() profile {
	cs := managedPlane("1.1")
	cs = append(cs, c("2.1.1", "gke.audit-logs", "Cloud Audit Logs enabled for the cluster", false))
	cs = append(cs, workerFiles("3.1", 4)...)
	cs = append(cs, kubeletControls("3.2", false)...)
	cs = append(cs, policyControls("4", false)...)
	cs = append(cs,
		c("5.1.1", "gke.image-scanning", "Image vulnerability scanning on", false),
		c("5.2.1", "gke.node-sa", "Nodes do not use the default Compute Engine service account", false),
		c("5.2.2", "gke.workload-identity", "Workload Identity in use", false),
		c("5.3.1", "gke.kms", "Secrets encrypted with Cloud KMS", false),
		c("5.5.1", "gke.cos", "Container-Optimized OS on nodes", false),
		c("5.5.7", "gke.shielded-nodes", "Shielded GKE nodes", false),
		c("5.6.2", "gke.vpc-native", "VPC-native (alias IP) networking", false),
		c("5.6.4", "gke.private-endpoint", "Private endpoint, public access restricted", false),
		c("5.6.7", "gke.network-policy", "Network policy enforcement on", false),
		c("5.8.1", "gke.basic-auth", "No basic or client-certificate auth", false),
		c("5.10.1", "gke.binauthz", "Binary Authorization on", false),
	)
	return profile{
		info: ProfileInfo{ID: ProfileGKE, Title: "CIS Google GKE", Version: "1.5 numbering",
			Description: "GKE: Google runs the control plane; nodes, policies and GKE configuration are yours."},
		sections: merge(map[string]string{
			"1": "Control plane (managed)", "1.1": "Control plane components", "2": "Control plane configuration", "2.1": "Logging",
			"3": "Worker nodes", "3.1": "Worker node files", "3.2": "Kubelet",
			"5": "Managed services", "5.1": "Image registry and scanning", "5.2": "IAM", "5.3": "Key management", "5.5": "Node metadata and OS",
			"5.6": "Cluster networking", "5.8": "Authentication", "5.10": "Other cluster configuration",
		}, policySections("4")),
		controls: cs,
	}
}

func aksProfile() profile {
	cs := managedPlane("1.1")
	cs = append(cs, c("2.1.1", "aks.audit-logs", "Control plane audit logs enabled", false))
	cs = append(cs, workerFiles("3.1", 4)...)
	cs = append(cs, kubeletControls("3.2", false)...)
	cs = append(cs, policyControls("4", false)...)
	cs = append(cs,
		c("5.1.1", "aks.image-scanning", "Image vulnerability scanning on", false),
		c("5.3.1", "aks.kms", "Secrets encrypted with Key Vault (KMS)", false),
		c("5.4.1", "aks.endpoint", "Restricted access to the API server endpoint", false),
		c("5.4.2", "aks.private-cluster", "Private cluster", false),
		c("5.5.1", "aks.entra", "Microsoft Entra ID integration for users", false),
	)
	return profile{
		info: ProfileInfo{ID: ProfileAKS, Title: "CIS Azure AKS", Version: "1.5 numbering",
			Description: "AKS: Microsoft runs the control plane; nodes, policies and AKS configuration are yours."},
		sections: merge(map[string]string{
			"1": "Control plane (managed)", "1.1": "Control plane components", "2": "Control plane configuration", "2.1": "Logging",
			"3": "Worker nodes", "3.1": "Worker node files", "3.2": "Kubelet",
			"5": "Managed services", "5.1": "Image registry and scanning", "5.3": "Key management", "5.4": "Cluster networking", "5.5": "Authentication",
		}, policySections("4")),
		controls: cs,
	}
}

func distroProfile(id, title, desc, note string) profile {
	p := genericProfile()
	p.info = ProfileInfo{ID: id, Title: title, Version: "CIS Kubernetes mapping", Description: desc}
	p.notes = []string{note}
	return p
}

func openshiftProfile() profile {
	return profile{
		info: ProfileInfo{ID: ProfileOpenShift, Title: "OpenShift (policies only)", Version: "CIS Kubernetes section 5",
			Description: "OpenShift has its own CIS benchmark; Kanivet evaluates the portable policy controls."},
		sections: policySections("5"),
		controls: policyControls("5", false),
		notes:    []string{"For the full CIS Red Hat OpenShift benchmark (control plane, nodes and SCCs), use the OpenShift Compliance Operator; its results are authoritative there."},
	}
}

var profiles = map[string]func() profile{
	ProfileK8s: genericProfile, ProfileEKS: eksProfile, ProfileGKE: gkeProfile, ProfileAKS: aksProfile,
	ProfileK3s: func() profile {
		return distroProfile(ProfileK3s, "k3s hardening (CIS)", "k3s follows the CIS Kubernetes numbering in its hardening guide.",
			"k3s runs the API server, scheduler and controller manager inside the k3s process, not as static pods, so control-plane flags are checked only when a scanner reports them. See the k3s CIS hardening guide for the k3s-specific flag names.")
	},
	ProfileRKE2: func() profile {
		return distroProfile(ProfileRKE2, "RKE2 hardening (CIS)", "RKE2 follows the CIS Kubernetes numbering and runs control-plane static pods.",
			"RKE2 passes most control-plane controls by default when started with profile: cis. Its static pods live in kube-system and are read like kubeadm's.")
	},
	ProfileOpenShift: openshiftProfile,
}

// ProfileOrder is the switcher order.
var ProfileOrder = []string{ProfileK8s, ProfileEKS, ProfileGKE, ProfileAKS, ProfileK3s, ProfileRKE2, ProfileOpenShift}

// Profiles lists every profile.
func Profiles() []ProfileInfo {
	out := make([]ProfileInfo, 0, len(ProfileOrder))
	for _, id := range ProfileOrder {
		out = append(out, profiles[id]().info)
	}
	return out
}

// ProfileFor picks the benchmark that fits a provider.
func ProfileFor(provider string) string {
	switch provider {
	case clusterinfo.EKS:
		return ProfileEKS
	case clusterinfo.GKE:
		return ProfileGKE
	case clusterinfo.AKS:
		return ProfileAKS
	case clusterinfo.K3s:
		return ProfileK3s
	case clusterinfo.RKE2:
		return ProfileRKE2
	case clusterinfo.OpenShift:
		return ProfileOpenShift
	}
	return ProfileK8s
}

// sectionOf splits "5.1.3" into "5" and "5.1"; "2.1" into "2" and "2".
func sectionOf(id string) (string, string) {
	parts := strings.Split(id, ".")
	if len(parts) <= 2 {
		return parts[0], parts[0]
	}
	return parts[0], parts[0] + "." + parts[1]
}
