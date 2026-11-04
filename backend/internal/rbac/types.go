// Package rbac builds an access graph of a cluster's RBAC objects and answers
// questions about it: what a subject may do, who may do something, which
// grants are risky, and what the current user may do. See README.md.
package rbac

import "time"

// Subject kinds, as they appear in a binding's subjects.
const (
	KindUser           = "User"
	KindGroup          = "Group"
	KindServiceAccount = "ServiceAccount"
)

// SubjectRef names a user, group or service account. Namespace is set only
// for service accounts.
type SubjectRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Key is a stable identifier: "ServiceAccount:ns/name", "User:alice".
func (s SubjectRef) Key() string {
	if s.Kind == KindServiceAccount {
		return s.Kind + ":" + s.Namespace + "/" + s.Name
	}
	return s.Kind + ":" + s.Name
}

// ObjectRef names a Role, ClusterRole, RoleBinding or ClusterRoleBinding.
type ObjectRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Rule is one policy rule. Source names the ClusterRole that defined it when
// the rule reached its role through aggregation.
type Rule struct {
	Verbs           []string `json:"verbs"`
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// Path explains why a subject holds a permission: the binding, the role it
// references and the rule that matched, plus the scope it applies in.
type Path struct {
	Binding ObjectRef `json:"binding"`
	Role    ObjectRef `json:"role"`
	// Scope is the namespace the grant applies in; "" means cluster-wide.
	Scope string `json:"scope"`
	Rule  Rule   `json:"rule"`
	// Via names the binding subject that matched when it is not the subject
	// itself, e.g. the group "system:serviceaccounts:default".
	Via         string `json:"via,omitempty"`
	RoleMissing bool   `json:"roleMissing,omitempty"`
}

// Query asks who may perform Verb on Resource (optionally a Subresource and
// a single object Name) in Namespace. Group "*" matches any API group and
// "" is the core group;
// an empty Namespace means "anywhere" and each path reports its scope.
type Query struct {
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

// WhoCanSubject is one subject allowed by a WhoCan query, with every path.
type WhoCanSubject struct {
	Subject     SubjectRef `json:"subject"`
	ClusterWide bool       `json:"clusterWide"`
	Paths       []Path     `json:"paths"`
}

// WhoCanResult answers a Query.
type WhoCanResult struct {
	Query    Query           `json:"query"`
	Subjects []WhoCanSubject `json:"subjects"`
	Warnings []string        `json:"warnings,omitempty"`
}

// Cell is one (resource, verb) entry of a permission matrix.
type Cell struct {
	Allowed bool `json:"allowed"`
	// Limited is true when every granting rule names specific objects.
	Limited bool   `json:"limited,omitempty"`
	Paths   []Path `json:"paths,omitempty"`
}

// MatrixRow is one resource of a permission matrix. Resource "*" and
// Group "*" stand for wildcards.
type MatrixRow struct {
	Group    string           `json:"group"`
	Resource string           `json:"resource"`
	Cells    map[string]*Cell `json:"cells"`
}

// PermissionMatrix is what a subject (or role, or the current user) may do,
// resources by verbs.
type PermissionMatrix struct {
	Subject         *SubjectRef  `json:"subject,omitempty"`
	Namespace       string       `json:"namespace,omitempty"`
	Verbs           []string     `json:"verbs"`
	Rows            []MatrixRow  `json:"rows"`
	NonResourceURLs []NonResRow  `json:"nonResourceURLs,omitempty"`
	Bindings        []BindingUse `json:"bindings"`
	Risks           []Risk       `json:"risks,omitempty"`
	Warnings        []string     `json:"warnings,omitempty"`
}

// NonResRow is a non-resource URL grant such as "/metrics".
type NonResRow struct {
	URL   string   `json:"url"`
	Verbs []string `json:"verbs"`
	Paths []Path   `json:"paths"`
}

// BindingUse is a binding that grants the subject something.
type BindingUse struct {
	Binding     ObjectRef `json:"binding"`
	Role        ObjectRef `json:"role"`
	Scope       string    `json:"scope"`
	Via         string    `json:"via,omitempty"`
	RoleMissing bool      `json:"roleMissing,omitempty"`
}

// Severity of a risky grant.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
)

// Risk is a grant worth a second look.
type Risk struct {
	ID       string       `json:"id"`
	Severity Severity     `json:"severity"`
	Category string       `json:"category"`
	Title    string       `json:"title"`
	Detail   string       `json:"detail"`
	Binding  ObjectRef    `json:"binding"`
	Role     ObjectRef    `json:"role"`
	Scope    string       `json:"scope"`
	Subjects []SubjectRef `json:"subjects"`
	System   bool         `json:"system,omitempty"`
}

// RiskOptions filters RiskyGrants.
type RiskOptions struct {
	// IncludeSystem keeps grants from Kubernetes' own bindings (system:*,
	// kubeadm:*), which are risky by design and usually noise.
	IncludeSystem bool
	Namespace     string
}

// SubjectSummary lists a subject for pickers.
type SubjectSummary struct {
	Subject  SubjectRef `json:"subject"`
	Bindings int        `json:"bindings"`
	// Exists is false for a service account named by a binding but absent.
	Exists bool `json:"exists"`
}

// SelfAccess is the current user's own access, from the API server.
type SelfAccess struct {
	Namespace  string            `json:"namespace"`
	Incomplete bool              `json:"incomplete"`
	Matrix     *PermissionMatrix `json:"matrix"`
	Evaluation string            `json:"evaluationError,omitempty"`
}

// CanIResult answers a SelfSubjectAccessReview.
type CanIResult struct {
	Allowed bool   `json:"allowed"`
	Denied  bool   `json:"denied,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// GraphInfo describes the loaded graph.
type GraphInfo struct {
	Roles               int       `json:"roles"`
	ClusterRoles        int       `json:"clusterRoles"`
	RoleBindings        int       `json:"roleBindings"`
	ClusterRoleBindings int       `json:"clusterRoleBindings"`
	LoadedAt            time.Time `json:"loadedAt"`
	Warnings            []string  `json:"warnings,omitempty"`
}
