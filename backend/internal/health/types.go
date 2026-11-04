// Package health answers two questions about a cluster: what is broken right
// now (Issues) and what is configured against best practice (Audit). Both
// read one Snapshot of the cluster, built from the shared pod cache, a short
// lived cache of the other kinds and the event store the incident timeline
// classifies. See README.md for the service API.
package health

import (
	"time"

	"github.com/kanivet/backend/internal/incidents"
)

// Severity is the incident timeline's: a live issue and a timeline entry for
// the same object rank the same way.
type Severity = incidents.Severity

const (
	SeverityCritical = incidents.SeverityCritical
	SeverityWarning  = incidents.SeverityWarning
	SeverityInfo     = incidents.SeverityInfo
)

// Rank orders severities, higher is worse.
func Rank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// ResourceRef names one object.
type ResourceRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func (r ResourceRef) Key() string { return r.Kind + "/" + r.Namespace + "/" + r.Name }

// Issue is one live problem. Problems of the same rule on objects with the
// same owner collapse into one issue: ten crash-looping pods of a Deployment
// are one issue on the Deployment with Count 10.
type Issue struct {
	ID       string   `json:"id"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	// Reason is the short machine reason (CrashLoopBackOff, Unschedulable).
	Reason  string `json:"reason"`
	Message string `json:"message"`
	// Resource is the object the issue is about: the owner when grouped.
	Resource ResourceRef `json:"resource"`
	// Affected are the objects that show the problem (pods of an owner).
	Affected []ResourceRef `json:"affected,omitempty"`
	Count    int           `json:"count"`
	// Since is when the problem started, as well as the cluster says.
	Since time.Time `json:"since"`
	// Source is "state" for problems read from object status and "event"
	// for warnings the incident classifier raised that no state rule covers.
	Source string `json:"source"`
	// Hint is a one-line next step.
	Hint string `json:"hint,omitempty"`
}

// IssueFilter narrows Issues.
type IssueFilter struct {
	Cluster    string
	Namespaces []string
	Severities []Severity
	Kinds      []string
	// Resource, when set, returns only issues about that object or listing it
	// as affected.
	Resource *ResourceRef
	Search   string
}

type IssueSummary struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
}

type IssuesReport struct {
	Issues      []Issue      `json:"issues"`
	Summary     IssueSummary `json:"summary"`
	GeneratedAt time.Time    `json:"generatedAt"`
	// Unavailable lists kinds that could not be read (RBAC, API missing): the
	// rules over them did not run.
	Unavailable []string `json:"unavailable,omitempty"`
}

// Category groups audit checks.
type Category string

const (
	CategorySecurity    Category = "security"
	CategoryReliability Category = "reliability"
	CategoryEfficiency  Category = "efficiency"
)

// Check is one best-practice rule.
type Check struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Category    Category `json:"category"`
	Severity    Severity `json:"severity"`
	Explanation string   `json:"explanation"`
	// Fix is a YAML snippet showing the corrected fields.
	Fix string `json:"fix"`
	// FixPath says where Fix goes (spec.template.spec.containers[*]).
	FixPath string `json:"fixPath,omitempty"`
}

// Finding is one check failing on one resource.
type Finding struct {
	CheckID   string      `json:"checkId"`
	Severity  Severity    `json:"severity"`
	Category  Category    `json:"category"`
	Resource  ResourceRef `json:"resource"`
	Container string      `json:"container,omitempty"`
	Message   string      `json:"message"`
	Ignored   bool        `json:"ignored,omitempty"`
}

// AuditScope narrows Audit.
type AuditScope struct {
	Cluster    string
	Namespaces []string
	Resource   *ResourceRef
	// IncludeSystem audits kube-system and other control-plane namespaces,
	// which are skipped by default.
	IncludeSystem bool
}

type CheckResult struct {
	Check
	Failed  int `json:"failed"`
	Passed  int `json:"passed"`
	Ignored int `json:"ignored"`
}

type NamespaceScore struct {
	Namespace string `json:"namespace"`
	Score     int    `json:"score"`
	Resources int    `json:"resources"`
	Critical  int    `json:"critical"`
	Warning   int    `json:"warning"`
	Info      int    `json:"info"`
}

type AuditReport struct {
	Score       int              `json:"score"`
	Resources   int              `json:"resources"`
	Namespaces  []NamespaceScore `json:"namespaces"`
	Checks      []CheckResult    `json:"checks"`
	Findings    []Finding        `json:"findings"`
	Categories  map[Category]int `json:"categories"`
	GeneratedAt time.Time        `json:"generatedAt"`
	Unavailable []string         `json:"unavailable,omitempty"`
}

// Ignore suppresses a check. With Name set it applies to one resource; with
// only Namespace, to the namespace; with neither, to the cluster.
type Ignore struct {
	Cluster   string    `json:"cluster"`
	CheckID   string    `json:"checkId"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ResourceHealth is what the inspector shows for one object.
type ResourceHealth struct {
	Issues   []Issue   `json:"issues"`
	Findings []Finding `json:"findings"`
	Checks   []Check   `json:"checks"`
}
