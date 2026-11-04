package network

// Ref names one end of a path or the subject of a view. Kind is a pod,
// a workload (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job), a
// Service, or "IP" for an address outside the cluster (IP is then set).
type Ref struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	IP        string `json:"ip,omitempty"`
}

// ObjectRef points at a Kubernetes object the UI can open.
type ObjectRef struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

// PodRef is a pod taking part in a path.
type PodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	IP        string `json:"ip,omitempty"`
	Ready     bool   `json:"ready"`
	// Workload is the pod's controller, when it has one.
	Workload *ObjectRef `json:"workload,omitempty"`
}

// RuleRef is one rule of one policy, numbered from 1 as people count them.
type RuleRef struct {
	Policy    ObjectRef `json:"policy"`
	Direction string    `json:"direction"` // "ingress" | "egress"
	Index     int       `json:"index"`
}

// Hop statuses.
const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusWarn = "warn"
	StatusSkip = "skip"
)

// Verdicts.
const (
	VerdictAllowed = "allowed"
	VerdictBlocked = "blocked"
	VerdictPartial = "partial"
)

// Hop is one step of a path: who sends, the egress check, the Service, its
// endpoints, the ingress check, the destination port.
type Hop struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // source | egress | service | endpoints | ingress | port | destination
	Title  string `json:"title"`
	Status string `json:"status"`
	// Detail is the one-line explanation shown under the hop.
	Detail string `json:"detail"`
	// Reasons explain the decision rule by rule.
	Reasons   []string    `json:"reasons,omitempty"`
	AllowedBy []RuleRef   `json:"allowedBy,omitempty"`
	Policies  []ObjectRef `json:"policies,omitempty"`
	Objects   []ObjectRef `json:"objects,omitempty"`
}

// PathRequest asks whether Source can open a connection to Destination on
// Port. For a Service destination Port is the Service port; for a pod or a
// workload it is the container port. Protocol defaults to TCP.
type PathRequest struct {
	Source      Ref    `json:"source"`
	Destination Ref    `json:"destination"`
	Port        int32  `json:"port"`
	Protocol    string `json:"protocol,omitempty"`
}

// PathResult is the answer, hop by hop.
type PathResult struct {
	Verdict string `json:"verdict"`
	Summary string `json:"summary"`
	Hops    []Hop  `json:"hops"`
	// Port and Protocol are what was evaluated, after defaults.
	Port       int32    `json:"port"`
	Protocol   string   `json:"protocol"`
	SourcePods []PodRef `json:"sourcePods"`
	DestPods   []PodRef `json:"destPods"`
	// Pairs counts the source/destination pod pairs evaluated and how many
	// policy allows end to end.
	Pairs        int      `json:"pairs"`
	PairsAllowed int      `json:"pairsAllowed"`
	Warnings     []string `json:"warnings,omitempty"`
	// Enforcer names the CNI detected to enforce NetworkPolicies, "" if none.
	Enforcer string `json:"enforcer,omitempty"`
}

// WorkloadCoverage is one workload's isolation in a namespace.
type WorkloadCoverage struct {
	Workload        ObjectRef   `json:"workload"`
	Pods            int         `json:"pods"`
	IngressIsolated bool        `json:"ingressIsolated"`
	EgressIsolated  bool        `json:"egressIsolated"`
	IngressPolicies []ObjectRef `json:"ingressPolicies"`
	EgressPolicies  []ObjectRef `json:"egressPolicies"`
	// Status is "isolated" (both directions), "partial" (one) or "open".
	Status string `json:"status"`
}

// PolicySummary is a NetworkPolicy as the coverage view lists it.
type PolicySummary struct {
	Policy       ObjectRef `json:"policy"`
	PolicyTypes  []string  `json:"policyTypes"`
	SelectedPods int       `json:"selectedPods"`
	IngressRules int       `json:"ingressRules"`
	EgressRules  int       `json:"egressRules"`
	// DefaultDeny marks a policy that selects every pod and allows nothing
	// in at least one direction.
	DefaultDenyIngress bool   `json:"defaultDenyIngress"`
	DefaultDenyEgress  bool   `json:"defaultDenyEgress"`
	Selector           string `json:"selector"`
}

// CoverageReport is how much of a namespace (all, when Namespace is "")
// NetworkPolicies isolate.
type CoverageReport struct {
	Namespace       string             `json:"namespace"`
	Pods            int                `json:"pods"`
	IngressIsolated int                `json:"ingressIsolated"`
	EgressIsolated  int                `json:"egressIsolated"`
	Workloads       []WorkloadCoverage `json:"workloads"`
	Policies        []PolicySummary    `json:"policies"`
	Enforcer        string             `json:"enforcer,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
}

// RuleView is a policy rule in words.
type RuleView struct {
	Rule  RuleRef  `json:"rule"`
	Peers []string `json:"peers"`
	Ports []string `json:"ports"`
	// Matches counts the workloads (or, for a NetworkPolicy view, pods) its
	// peers select, -1 when it allows any source or destination.
	Matches int `json:"matches"`
}

// Peer is a workload allowed to connect, with the ports it may use.
type Peer struct {
	Workload ObjectRef `json:"workload"`
	Ports    []string  `json:"ports"`
}

// DirectionView is one direction of a pod's reachability.
type DirectionView struct {
	Isolated bool        `json:"isolated"`
	Policies []ObjectRef `json:"policies"`
	Rules    []RuleView  `json:"rules"`
	Summary  string      `json:"summary"`
	// Peers are the in-cluster workloads the rules allow, when isolated.
	Peers          []Peer   `json:"peers"`
	PeersTruncated bool     `json:"peersTruncated,omitempty"`
	ExternalCIDRs  []string `json:"externalCidrs,omitempty"`
}

// PortMapping is one Service port, followed to the containers.
type PortMapping struct {
	Name           string `json:"name,omitempty"`
	Port           int32  `json:"port"`
	Protocol       string `json:"protocol"`
	TargetPort     string `json:"targetPort"`
	ResolvedPort   int32  `json:"resolvedPort,omitempty"`
	Exposed        bool   `json:"exposed"`
	Detail         string `json:"detail"`
	ReadyEndpoints int    `json:"readyEndpoints"`
	TotalEndpoints int    `json:"totalEndpoints"`
}

// ServiceView is a Service exposing the subject's pods, or the subject itself.
type ServiceView struct {
	Service ObjectRef     `json:"service"`
	Type    string        `json:"type"`
	Ports   []PortMapping `json:"ports"`
}

// ResourceNetwork is the inspector's Network section for a pod, a workload
// or a Service: what can reach it and what it can reach.
type ResourceNetwork struct {
	Subject  Ref           `json:"subject"`
	Pods     []PodRef      `json:"pods"`
	Ingress  DirectionView `json:"ingress"`
	Egress   DirectionView `json:"egress"`
	Services []ServiceView `json:"services"`
	Enforcer string        `json:"enforcer,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// PolicyDetail is the inspector's Network section for a NetworkPolicy.
type PolicyDetail struct {
	Policy            ObjectRef   `json:"policy"`
	PolicyTypes       []string    `json:"policyTypes"`
	Selector          string      `json:"selector"`
	SelectedPods      []PodRef    `json:"selectedPods"`
	SelectedWorkloads []ObjectRef `json:"selectedWorkloads"`
	Ingress           []RuleView  `json:"ingress"`
	Egress            []RuleView  `json:"egress"`
	Summary           []string    `json:"summary"`
	Enforcer          string      `json:"enforcer,omitempty"`
	Warnings          []string    `json:"warnings,omitempty"`
}

// Target is something the path checker can pick.
type Target struct {
	Ref   Ref     `json:"ref"`
	Pods  int     `json:"pods"`
	Ports []int32 `json:"ports,omitempty"`
	// PortNames pairs with Ports for Services ("http").
	PortNames []string `json:"portNames,omitempty"`
}

// Targets lists the pickable namespaces, workloads, pods and Services.
type Targets struct {
	Namespaces []string `json:"namespaces"`
	Targets    []Target `json:"targets"`
	Truncated  bool     `json:"truncated,omitempty"`
}
