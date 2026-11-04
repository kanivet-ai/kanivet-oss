// Package topology builds a relationship graph between Kubernetes resources
// in one namespace: ownership, Service selection, routing, config and storage
// references, scaling and disruption targets, scheduling and network
// policies. See README.md for the service API.
package topology

// Relation names the kind of link an Edge represents. Edges point from the
// "upstream" resource (the one that owns, selects, routes to, mounts, scales,
// ...) to the one it acts on.
type Relation string

const (
	RelOwns      Relation = "owns"       // ownerReferences: owner -> owned
	RelSelects   Relation = "selects"    // Service -> Pod (label selector)
	RelExposes   Relation = "exposes"    // Service -> workload whose pods it selects
	RelRoutes    Relation = "routes"     // Ingress/HTTPRoute -> Service
	RelAttaches  Relation = "attaches"   // Gateway -> HTTPRoute (parentRefs)
	RelMounts    Relation = "mounts"     // workload/Pod -> ConfigMap/Secret/PVC volume
	RelEnv       Relation = "env"        // workload/Pod -> ConfigMap/Secret via env/envFrom
	RelRunsAs    Relation = "runs-as"    // workload/Pod -> ServiceAccount
	RelTLS       Relation = "tls"        // Ingress -> Secret
	RelBinds     Relation = "binds"      // PVC -> PV
	RelClass     Relation = "class"      // PVC/PV -> StorageClass
	RelScales    Relation = "scales"     // HPA -> target
	RelProtects  Relation = "protects"   // PDB -> Pod
	RelRunsOn    Relation = "runs-on"    // Pod -> Node
	RelPolicy    Relation = "applies-to" // NetworkPolicy -> Pod
	RelImagePull Relation = "pulls-with" // workload/Pod -> image pull Secret
)

// Health is a coarse traffic-light state. The UI maps it to its status tokens.
type Health string

const (
	HealthHealthy Health = "healthy"
	HealthWarning Health = "warning"
	HealthError   Health = "error"
	HealthUnknown Health = "unknown"
)

// Ref identifies a resource. Namespace is empty for cluster-scoped kinds.
type Ref struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// ID is the stable node identifier used in Graph.Nodes and Edge endpoints.
func (r Ref) ID() string { return r.Kind + "/" + r.Namespace + "/" + r.Name }

// Node is one resource in the graph.
type Node struct {
	ID string `json:"id"`
	Ref
	UID    string `json:"uid,omitempty"`
	Health Health `json:"health"`
	// Status is a short human summary ("Running", "3/3 ready", "Bound").
	Status string `json:"status,omitempty"`
	// Owner is the ID of this node's controller owner, when it has one in the
	// graph. The UI uses it to group pods under their ReplicaSet.
	Owner string `json:"owner,omitempty"`
	// Missing marks a node that is referenced but was not found (a Secret a
	// pod mounts that does not exist, or one we may not read).
	Missing bool `json:"missing,omitempty"`
	// App names the application the resource belongs to, from its labels
	// (app.kubernetes.io/name, instance, part-of, app, k8s-app) or its Helm
	// release. Empty when nothing says so; the UI then falls back to owners.
	App string `json:"app,omitempty"`
}

// Edge is a directed, labelled relationship.
type Edge struct {
	ID       string   `json:"id"`
	Source   string   `json:"source"`
	Target   string   `json:"target"`
	Relation Relation `json:"relation"`
}

// Graph is the result of every query.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Focus is the ID of the resource a Neighbors query started from.
	Focus string `json:"focus,omitempty"`
	// Truncated is set when the graph was cut at MaxNodes.
	Truncated bool `json:"truncated,omitempty"`
	// Warnings lists kinds that could not be read (RBAC and other errors;
	// an API that isn't installed is skipped silently), so the UI can say the
	// graph is partial.
	Warnings []string `json:"warnings,omitempty"`
}

// Filters narrows a NamespaceGraph query.
type Filters struct {
	// Kinds, when non-empty, keeps only nodes of these kinds (edges between
	// kept nodes survive).
	Kinds []string `json:"kinds,omitempty"`
	// IncludeIdle keeps ReplicaSets scaled to zero (old rollout revisions).
	IncludeIdle bool `json:"includeIdle,omitempty"`
	// MaxNodes caps the graph size; 0 means DefaultMaxNodes.
	MaxNodes int `json:"maxNodes,omitempty"`
}

// DefaultMaxNodes bounds a graph so a huge namespace can't freeze the UI.
const DefaultMaxNodes = 1500
