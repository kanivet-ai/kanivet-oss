package network

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Snapshot is the cluster state the analysis reads. Everything in it is
// shared and must not be modified.
type Snapshot struct {
	Namespaces map[string]*corev1.Namespace
	Pods       []*corev1.Pod
	Services   []*corev1.Service
	Slices     []*discoveryv1.EndpointSlice
	Policies   []*netv1.NetworkPolicy
	// Enforcer is the CNI found to enforce NetworkPolicies ("" if none was
	// recognised).
	Enforcer string
	// Warnings say what could not be read (RBAC, errors); the analysis then
	// treats the missing kind as empty and says so.
	Warnings []string

	byNS map[string][]*netv1.NetworkPolicy
}

// NewSnapshot indexes the objects. Pods that finished (Succeeded/Failed) are
// dropped: they take no traffic.
func NewSnapshot(namespaces []*corev1.Namespace, pods []*corev1.Pod, services []*corev1.Service, slices []*discoveryv1.EndpointSlice, policies []*netv1.NetworkPolicy) *Snapshot {
	s := &Snapshot{Namespaces: map[string]*corev1.Namespace{}, Services: services, Slices: slices, Policies: append([]*netv1.NetworkPolicy(nil), policies...), byNS: map[string][]*netv1.NetworkPolicy{}}
	for _, ns := range namespaces {
		s.Namespaces[ns.Name] = ns
	}
	for _, p := range pods {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		s.Pods = append(s.Pods, p)
	}
	sort.Slice(s.Pods, func(i, j int) bool {
		if s.Pods[i].Namespace != s.Pods[j].Namespace {
			return s.Pods[i].Namespace < s.Pods[j].Namespace
		}
		return s.Pods[i].Name < s.Pods[j].Name
	})
	sort.Slice(s.Policies, func(i, j int) bool {
		if s.Policies[i].Namespace != s.Policies[j].Namespace {
			return s.Policies[i].Namespace < s.Policies[j].Namespace
		}
		return s.Policies[i].Name < s.Policies[j].Name
	})
	for _, np := range s.Policies {
		s.byNS[np.Namespace] = append(s.byNS[np.Namespace], np)
	}
	s.Enforcer = detectEnforcer(s.Pods)
	return s
}

// enforcers are the pod name prefixes of CNIs (or add-ons) that enforce
// NetworkPolicy.
var enforcers = []struct{ prefix, name string }{
	{"cilium", "Cilium"},
	{"calico-node", "Calico"},
	{"calico-typha", "Calico"},
	{"antrea-agent", "Antrea"},
	{"kube-router", "kube-router"},
	{"weave-net", "Weave Net"},
	{"canal", "Canal"},
	{"kube-network-policies", "kube-network-policies"},
	{"aws-node", "Amazon VPC CNI"},
	{"azure-npm", "Azure NPM"},
	{"netpol", "Network policy agent"},
}

func detectEnforcer(pods []*corev1.Pod) string {
	for _, p := range pods {
		for _, e := range enforcers {
			if strings.HasPrefix(p.Name, e.prefix) {
				// The VPC CNI only enforces with its network policy agent,
				// which runs as a container of aws-node.
				if e.prefix == "aws-node" && !hasContainer(p, "aws-network-policy-agent") {
					continue
				}
				return e.name
			}
		}
		// GKE Dataplane V2 runs anetd (Cilium).
		if strings.HasPrefix(p.Name, "anetd") {
			return "GKE Dataplane V2"
		}
	}
	return ""
}

func hasContainer(p *corev1.Pod, name string) bool {
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (s *Snapshot) policiesIn(ns string) []*netv1.NetworkPolicy { return s.byNS[ns] }

// namespaceLabels returns a namespace's labels, falling back to the label
// the API server stamps on every namespace when the namespace is unknown.
func (s *Snapshot) namespaceLabels(ns string) labels.Set {
	if n := s.Namespaces[ns]; n != nil && n.Labels != nil {
		return n.Labels
	}
	return labels.Set{"kubernetes.io/metadata.name": ns}
}

func (s *Snapshot) pod(ns, name string) *corev1.Pod {
	for _, p := range s.Pods {
		if p.Namespace == ns && p.Name == name {
			return p
		}
	}
	return nil
}

func (s *Snapshot) service(ns, name string) *corev1.Service {
	for _, svc := range s.Services {
		if svc.Namespace == ns && svc.Name == name {
			return svc
		}
	}
	return nil
}

// workloadOf names the pod's controller: a Deployment for a ReplicaSet that
// carries the pod-template-hash suffix, the direct owner otherwise.
func workloadOf(p *corev1.Pod) *ObjectRef {
	ref := metav1.GetControllerOf(p)
	if ref == nil {
		return nil
	}
	if ref.Kind == "ReplicaSet" {
		if h := p.Labels["pod-template-hash"]; h != "" && strings.HasSuffix(ref.Name, "-"+h) {
			return &ObjectRef{Kind: "Deployment", APIVersion: "apps/v1", Namespace: p.Namespace, Name: strings.TrimSuffix(ref.Name, "-"+h)}
		}
	}
	return &ObjectRef{Kind: ref.Kind, APIVersion: ref.APIVersion, Namespace: p.Namespace, Name: ref.Name}
}

// workloadKey groups pods: the controller, or the pod itself.
func workloadKey(p *corev1.Pod) ObjectRef {
	if w := workloadOf(p); w != nil {
		return *w
	}
	return ObjectRef{Kind: "Pod", APIVersion: "v1", Namespace: p.Namespace, Name: p.Name}
}

func isReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podRef(p *corev1.Pod) PodRef {
	return PodRef{Namespace: p.Namespace, Name: p.Name, IP: p.Status.PodIP, Ready: isReady(p), Workload: workloadOf(p)}
}

// podsOf resolves a pod or a workload to its pods.
func (s *Snapshot) podsOf(r Ref) []*corev1.Pod {
	var out []*corev1.Pod
	kind := normalizeKind(r.Kind)
	for _, p := range s.Pods {
		if p.Namespace != r.Namespace {
			continue
		}
		if kind == "Pod" {
			if p.Name == r.Name {
				out = append(out, p)
			}
			continue
		}
		if w := workloadOf(p); w != nil && w.Kind == kind && w.Name == r.Name {
			out = append(out, p)
		}
	}
	return out
}

// normalizeKind accepts the spellings the UI, the palette and MCP use.
func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "pod", "pods", "po":
		return "Pod"
	case "deployment", "deployments", "deploy":
		return "Deployment"
	case "statefulset", "statefulsets", "sts":
		return "StatefulSet"
	case "daemonset", "daemonsets", "ds":
		return "DaemonSet"
	case "replicaset", "replicasets", "rs":
		return "ReplicaSet"
	case "job", "jobs":
		return "Job"
	case "service", "services", "svc":
		return "Service"
	case "ip", "cidr", "external":
		return "IP"
	case "networkpolicy", "networkpolicies", "netpol":
		return "NetworkPolicy"
	}
	return k
}

func policyRef(np *netv1.NetworkPolicy) ObjectRef {
	return ObjectRef{Kind: "NetworkPolicy", APIVersion: "networking.k8s.io/v1", Namespace: np.Namespace, Name: np.Name}
}

func serviceRef(svc *corev1.Service) ObjectRef {
	return ObjectRef{Kind: "Service", APIVersion: "v1", Namespace: svc.Namespace, Name: svc.Name}
}

// servicesSelecting returns the Services whose selector picks the pod.
func (s *Snapshot) servicesSelecting(p *corev1.Pod) []*corev1.Service {
	var out []*corev1.Service
	for _, svc := range s.Services {
		if svc.Namespace != p.Namespace || len(svc.Spec.Selector) == 0 {
			continue
		}
		if labels.SelectorFromSet(svc.Spec.Selector).Matches(labels.Set(p.Labels)) {
			out = append(out, svc)
		}
	}
	return out
}
