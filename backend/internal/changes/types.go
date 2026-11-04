// Package changes reconstructs the change history of Kubernetes resources from
// what the cluster itself keeps: ReplicaSets and ControllerRevisions (one per
// rollout), Helm release revisions, Argo CD and Flux sync history,
// managedFields (who last wrote which fields) and recent Events. Nothing is
// recorded locally, so history is complete the first time a resource is
// opened and never has gaps from the app being closed. See README.md.
package changes

import "time"

// Source says where an entry was reconstructed from.
type Source string

const (
	SourceReplicaSet         Source = "replicaset"
	SourceControllerRevision Source = "controllerrevision"
	SourceHelm               Source = "helm"
	SourceArgoCD             Source = "argocd"
	SourceFlux               Source = "flux"
	SourceManagedFields      Source = "managedfields"
	SourceEvent              Source = "event"
	SourceCreation           Source = "creation"
)

// Category classifies what an entry changed.
type Category string

const (
	CatCreated   Category = "created"
	CatDeleted   Category = "deleted"
	CatRollout   Category = "rollout"
	CatImage     Category = "image"
	CatEnv       Category = "env"
	CatConfig    Category = "config"
	CatResources Category = "resources"
	CatReplicas  Category = "replicas"
	CatRestart   Category = "restart"
	CatSecret    Category = "secret"
	CatSync      Category = "sync"
	CatEdit      Category = "edit"
	CatEvent     Category = "event"
	CatOther     Category = "other"
)

// ResourceRef names one object. Group is empty for the core group.
type ResourceRef struct {
	Cluster   string `json:"cluster"`
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// FieldChange is one field that differs from the previous revision.
type FieldChange struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// Entry is one point in a resource's history.
type Entry struct {
	ID     string    `json:"id"`
	Source Source    `json:"source"`
	Time   time.Time `json:"time"`
	// Subject is the object the entry is about (the workload, not its
	// ReplicaSet).
	Subject    ResourceRef `json:"subject"`
	Revision   string      `json:"revision,omitempty"`
	Title      string      `json:"title"`
	Summary    string      `json:"summary,omitempty"`
	Categories []Category  `json:"categories"`
	// Actor is who made the change where the cluster records it: a field
	// manager, "helm", "argocd", a controller.
	Actor string `json:"actor,omitempty"`
	// Fields are the top-level field paths the change touched
	// (managedFields entries carry no old values, only paths).
	Fields []string      `json:"fields,omitempty"`
	Diff   []FieldChange `json:"diff,omitempty"`
	// Snapshot is a YAML rendering of the revision for side-by-side diffs.
	// Empty with HasSnapshot set means it is fetched with Snapshot.
	Snapshot    string `json:"snapshot,omitempty"`
	HasSnapshot bool   `json:"hasSnapshot"`
	// Origin is the object the entry was read from when it is not Subject
	// (a ReplicaSet, a ControllerRevision, an Argo Application).
	Origin *ResourceRef `json:"origin,omitempty"`
	// Warning marks Events of type Warning.
	Warning bool `json:"warning,omitempty"`
	Current bool `json:"current,omitempty"`
}

// History is a resource's reconstructed history, newest first.
type History struct {
	Subject ResourceRef `json:"subject"`
	Entries []Entry     `json:"entries"`
	// Sources lists the sources that were consulted.
	Sources []Source `json:"sources"`
	// Warnings are sources that could not be read (RBAC, unreachable),
	// so the caller can say the history is partial.
	Warnings []string `json:"warnings,omitempty"`
}

// Filter narrows RecentChanges. Empty fields do not filter.
type Filter struct {
	Namespaces []string
	Kinds      []string
	Categories []Category
	Sources    []Source
	Since      time.Time
	Limit      int
}

// Feed is the cluster-wide list of recent changes, newest first.
type Feed struct {
	Cluster  string    `json:"cluster"`
	Since    time.Time `json:"since"`
	Entries  []Entry   `json:"entries"`
	Warnings []string  `json:"warnings,omitempty"`
}
