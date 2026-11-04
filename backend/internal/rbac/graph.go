package rbac

import (
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Snapshot is the raw RBAC state a Graph is built from.
type Snapshot struct {
	Roles               []rbacv1.Role
	ClusterRoles        []rbacv1.ClusterRole
	RoleBindings        []rbacv1.RoleBinding
	ClusterRoleBindings []rbacv1.ClusterRoleBinding
	ServiceAccounts     []corev1.ServiceAccount
	// Warnings name the kinds that could not be read (usually forbidden).
	Warnings []string
}

type role struct {
	ref   ObjectRef
	rules []Rule
}

// edge is one binding: who it names, the role it grants and where.
type edge struct {
	binding  ObjectRef
	roleRef  ObjectRef
	role     *role // nil when the referenced role does not exist
	scope    string
	subjects []SubjectRef
}

// Graph is an immutable, queryable view of a Snapshot.
type Graph struct {
	roles    map[string]*role
	edges    []edge
	sas      map[string]bool // "ns/name"
	info     GraphInfo
	warnings []string
}

func roleKey(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

// BuildGraph resolves roles (including aggregated ClusterRoles) and bindings.
func BuildGraph(s *Snapshot) *Graph {
	g := &Graph{roles: map[string]*role{}, sas: map[string]bool{}, warnings: s.Warnings}
	for i := range s.Roles {
		r := &s.Roles[i]
		g.roles[roleKey("Role", r.Namespace, r.Name)] = &role{
			ref:   ObjectRef{Kind: "Role", Name: r.Name, Namespace: r.Namespace},
			rules: convertRules(r.Rules, ""),
		}
	}
	for i := range s.ClusterRoles {
		cr := &s.ClusterRoles[i]
		g.roles[roleKey("ClusterRole", "", cr.Name)] = &role{
			ref:   ObjectRef{Kind: "ClusterRole", Name: cr.Name},
			rules: aggregatedRules(cr, s.ClusterRoles),
		}
	}
	for i := range s.RoleBindings {
		b := &s.RoleBindings[i]
		rr := ObjectRef{Kind: b.RoleRef.Kind, Name: b.RoleRef.Name}
		if rr.Kind == "Role" {
			rr.Namespace = b.Namespace
		}
		g.edges = append(g.edges, edge{
			binding:  ObjectRef{Kind: "RoleBinding", Name: b.Name, Namespace: b.Namespace},
			roleRef:  rr,
			role:     g.roles[roleKey(rr.Kind, rr.Namespace, rr.Name)],
			scope:    b.Namespace,
			subjects: convertSubjects(b.Subjects, b.Namespace),
		})
	}
	for i := range s.ClusterRoleBindings {
		b := &s.ClusterRoleBindings[i]
		rr := ObjectRef{Kind: "ClusterRole", Name: b.RoleRef.Name}
		g.edges = append(g.edges, edge{
			binding:  ObjectRef{Kind: "ClusterRoleBinding", Name: b.Name},
			roleRef:  rr,
			role:     g.roles[roleKey("ClusterRole", "", rr.Name)],
			subjects: convertSubjects(b.Subjects, ""),
		})
	}
	for i := range s.ServiceAccounts {
		g.sas[s.ServiceAccounts[i].Namespace+"/"+s.ServiceAccounts[i].Name] = true
	}
	g.info = GraphInfo{
		Roles: len(s.Roles), ClusterRoles: len(s.ClusterRoles),
		RoleBindings: len(s.RoleBindings), ClusterRoleBindings: len(s.ClusterRoleBindings),
		LoadedAt: time.Now(), Warnings: s.Warnings,
	}
	return g
}

// Info summarises the graph.
func (g *Graph) Info() GraphInfo { return g.info }

func convertRules(in []rbacv1.PolicyRule, source string) []Rule {
	out := make([]Rule, 0, len(in))
	for _, r := range in {
		out = append(out, Rule{
			Verbs: r.Verbs, APIGroups: r.APIGroups, Resources: r.Resources,
			ResourceNames: r.ResourceNames, NonResourceURLs: r.NonResourceURLs, Source: source,
		})
	}
	return out
}

func convertSubjects(in []rbacv1.Subject, bindingNS string) []SubjectRef {
	out := make([]SubjectRef, 0, len(in))
	for _, s := range in {
		ref := SubjectRef{Kind: s.Kind, Name: s.Name}
		if s.Kind == KindServiceAccount {
			ref.Namespace = s.Namespace
			if ref.Namespace == "" {
				ref.Namespace = bindingNS
			}
		}
		out = append(out, ref)
	}
	return out
}

func ruleSig(r Rule) string {
	j := func(s []string) string { return strings.Join(s, ",") }
	return j(r.Verbs) + "|" + j(r.APIGroups) + "|" + j(r.Resources) + "|" + j(r.ResourceNames) + "|" + j(r.NonResourceURLs)
}

// aggregatedRules returns a ClusterRole's rules. For an aggregated role the
// API server already copies the members' rules in; matching them back to
// the member ClusterRoles records where each one came from, and picks up
// members the controller has not reconciled yet.
func aggregatedRules(cr *rbacv1.ClusterRole, all []rbacv1.ClusterRole) []Rule {
	if cr.AggregationRule == nil || len(cr.AggregationRule.ClusterRoleSelectors) == 0 {
		return convertRules(cr.Rules, "")
	}
	source := map[string]string{}
	var extra []Rule
	for _, sel := range cr.AggregationRule.ClusterRoleSelectors {
		selector, err := metav1.LabelSelectorAsSelector(&sel)
		if err != nil {
			continue
		}
		for i := range all {
			m := &all[i]
			if m.Name == cr.Name || !selector.Matches(labels.Set(m.Labels)) {
				continue
			}
			for _, r := range convertRules(m.Rules, m.Name) {
				sig := ruleSig(r)
				if _, ok := source[sig]; !ok {
					source[sig] = m.Name
					extra = append(extra, r)
				}
			}
		}
	}
	out := make([]Rule, 0, len(cr.Rules)+len(extra))
	seen := map[string]bool{}
	for _, r := range convertRules(cr.Rules, "") {
		sig := ruleSig(r)
		r.Source = source[sig]
		seen[sig] = true
		out = append(out, r)
	}
	for _, r := range extra {
		if !seen[ruleSig(r)] {
			out = append(out, r)
		}
	}
	return out
}

// matchSubject reports whether binding subject b covers target, and through
// which identity when that is not the target itself (a group a service
// account belongs to, or its "system:serviceaccount:" user name).
func matchSubject(b, target SubjectRef) (ok bool, via string) {
	switch target.Kind {
	case KindServiceAccount:
		switch b.Kind {
		case KindServiceAccount:
			return b.Name == target.Name && b.Namespace == target.Namespace, ""
		case KindUser:
			u := "system:serviceaccount:" + target.Namespace + ":" + target.Name
			if b.Name == u {
				return true, "User " + u
			}
		case KindGroup:
			switch b.Name {
			case "system:serviceaccounts", "system:serviceaccounts:" + target.Namespace, "system:authenticated":
				return true, "Group " + b.Name
			}
		}
	case KindUser:
		if b.Kind == KindUser && b.Name == target.Name {
			return true, ""
		}
		if b.Kind == KindGroup && b.Name == "system:authenticated" && target.Name != "system:anonymous" {
			return true, "Group system:authenticated"
		}
		if b.Kind == KindGroup && b.Name == "system:unauthenticated" && target.Name == "system:anonymous" {
			return true, "Group system:unauthenticated"
		}
	case KindGroup:
		return b.Kind == KindGroup && b.Name == target.Name, ""
	}
	return false, ""
}

// subjectEdges returns the bindings that apply to target, with the matching
// identity.
func (g *Graph) subjectEdges(target SubjectRef) []struct {
	e   *edge
	via string
} {
	var out []struct {
		e   *edge
		via string
	}
	for i := range g.edges {
		e := &g.edges[i]
		best, matched := "", false
		for _, s := range e.subjects {
			if ok, via := matchSubject(s, target); ok {
				if !matched || via == "" {
					best = via
				}
				matched = true
			}
		}
		if matched {
			out = append(out, struct {
				e   *edge
				via string
			}{e, best})
		}
	}
	return out
}

func inScope(edgeScope, ns string) bool { return ns == "" || edgeScope == "" || edgeScope == ns }

// Subjects lists every subject named by a binding plus every service account.
func (g *Graph) Subjects() []SubjectSummary {
	m := map[string]*SubjectSummary{}
	for i := range g.edges {
		for _, s := range g.edges[i].subjects {
			k := s.Key()
			if m[k] == nil {
				m[k] = &SubjectSummary{Subject: s, Exists: s.Kind != KindServiceAccount || g.sas[s.Namespace+"/"+s.Name]}
			}
			m[k].Bindings++
		}
	}
	for key := range g.sas {
		ns, name, _ := strings.Cut(key, "/")
		s := SubjectRef{Kind: KindServiceAccount, Name: name, Namespace: ns}
		if m[s.Key()] == nil {
			m[s.Key()] = &SubjectSummary{Subject: s, Exists: true}
		}
	}
	out := make([]SubjectSummary, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return subjectLess(out[i].Subject, out[j].Subject) })
	return out
}

func subjectLess(a, b SubjectRef) bool {
	order := map[string]int{KindServiceAccount: 0, KindUser: 1, KindGroup: 2}
	if order[a.Kind] != order[b.Kind] {
		return order[a.Kind] < order[b.Kind]
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	return a.Name < b.Name
}
