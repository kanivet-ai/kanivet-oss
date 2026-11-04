// Package compare diffs two Kubernetes objects: any two resources, the same
// resource across clusters or namespaces, or a live object against what was
// declared for it (its kubectl last-applied-configuration or its Helm release
// manifest). Objects are normalized before diffing, Secret values are reduced
// to keyed hashes, and the result leads with a semantic summary.
package compare

// Source says which version of an object a Ref names.
type Source string

const (
	// SourceLive is the object as the API server returns it now.
	SourceLive Source = "live"
	// SourceLastApplied is the object's kubectl.kubernetes.io/last-applied-configuration.
	SourceLastApplied Source = "last-applied"
	// SourceHelm is the object as rendered in its Helm release's manifest.
	SourceHelm Source = "helm"
)

// Ref names one side of a comparison.
type Ref struct {
	Cluster   string `json:"cluster"`
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// Source defaults to live.
	Source Source `json:"source,omitempty"`
}

func (r Ref) source() Source {
	if r.Source == "" {
		return SourceLive
	}
	return r.Source
}

// Options control normalization.
type Options struct {
	// IncludeStatus keeps .status (stripped by default).
	IncludeStatus bool `json:"includeStatus,omitempty"`
	// IncludeMetadata keeps server-managed metadata (uid, resourceVersion,
	// creationTimestamp, generation, ownerReferences, managedFields).
	IncludeMetadata bool `json:"includeMetadata,omitempty"`
	// KeepDefaults keeps fields that equal a well-known server default and
	// cluster-assigned values such as a Service's clusterIP.
	KeepDefaults bool `json:"keepDefaults,omitempty"`
	// IncludeUndeclared keeps live fields the declared side never set when
	// one side is a last-applied or Helm baseline. By default they are
	// dropped, so drift shows what changed rather than what the server added.
	IncludeUndeclared bool `json:"includeUndeclared,omitempty"`
}

// ChangeType classifies a field difference.
type ChangeType string

const (
	OnlyInA ChangeType = "removed" // present in A, missing in B
	OnlyInB ChangeType = "added"   // missing in A, present in B
	Changed ChangeType = "changed"
)

// FieldChange is one leaf (or subtree) that differs.
type FieldChange struct {
	// Path is dotted; named list items use [name], others [index].
	Path string     `json:"path"`
	Type ChangeType `json:"type"`
	A    string     `json:"a,omitempty"`
	B    string     `json:"b,omitempty"`
}

// Insight is one line of the semantic summary.
type Insight struct {
	// Category: kind, identity, image, replicas, env, resources, ports,
	// probes, volumes, scheduling, labels, annotations, data, spec, status, other.
	Category string `json:"category"`
	Title    string `json:"title"`
	A        string `json:"a,omitempty"`
	B        string `json:"b,omitempty"`
	// Paths are the FieldChange paths this insight explains.
	Paths []string `json:"paths,omitempty"`
}

// Side is one normalized object.
type Side struct {
	Ref   Ref    `json:"ref"`
	Label string `json:"label"`
	YAML  string `json:"yaml"`
	// Error is set when this side could not be read; the diff is then empty.
	Error string `json:"error,omitempty"`
	// Status is an HTTP-style status for Error: 404, 403 or 500.
	Status int `json:"status,omitempty"`
}

// Stats counts changes.
type Stats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

// Result is a comparison.
type Result struct {
	A         Side          `json:"a"`
	B         Side          `json:"b"`
	Identical bool          `json:"identical"`
	Summary   []Insight     `json:"summary"`
	Changes   []FieldChange `json:"changes"`
	Stats     Stats         `json:"stats"`
	// Notes explain normalization the user should know about.
	Notes []string `json:"notes,omitempty"`
}

// Baselines says which declared versions exist for a live object.
type Baselines struct {
	LastApplied   bool   `json:"lastApplied"`
	HelmRelease   string `json:"helmRelease,omitempty"`
	HelmNamespace string `json:"helmNamespace,omitempty"`
}
