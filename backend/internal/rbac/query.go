package rbac

import (
	"sort"
	"strings"
)

// StandardVerbs are the matrix columns always shown, in this order.
var StandardVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete", "deletecollection"}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v || x == "*" {
			return true
		}
	}
	return false
}

// resourceMatches applies Kubernetes' resource matching: "*" matches every
// resource and subresource, "pods/*" every pods subresource, "*/scale" the
// scale subresource of anything.
func resourceMatches(ruleRes []string, resource, sub string) bool {
	full := resource
	if sub != "" {
		full = resource + "/" + sub
	}
	for _, r := range ruleRes {
		if r == "*" || r == full {
			return true
		}
		if sub != "" && (r == resource+"/*" || r == "*/"+sub) {
			return true
		}
	}
	return false
}

// RuleAllows reports whether rule r grants q (verb, group, resource,
// subresource, name). q.Group "*" matches any API group; "" is the core group.
func RuleAllows(r Rule, q Query) bool {
	if len(r.Resources) == 0 || !has(r.Verbs, q.Verb) {
		return false
	}
	if q.Group != "*" && !has(r.APIGroups, q.Group) {
		return false
	}
	if !resourceMatches(r.Resources, q.Resource, q.Subresource) {
		return false
	}
	if len(r.ResourceNames) > 0 {
		// A named rule only answers a question about that name.
		return q.Name != "" && has(r.ResourceNames, q.Name)
	}
	return true
}

// WhoCan lists every subject allowed q, with the binding paths that allow it.
func (g *Graph) WhoCan(q Query) *WhoCanResult {
	q.Verb = strings.ToLower(strings.TrimSpace(q.Verb))
	if q.Subresource == "" {
		if res, sub, ok := strings.Cut(q.Resource, "/"); ok {
			q.Resource, q.Subresource = res, sub
		}
	}
	bySubject := map[string]*WhoCanSubject{}
	for i := range g.edges {
		e := &g.edges[i]
		if e.role == nil || !inScope(e.scope, q.Namespace) {
			continue
		}
		for _, r := range e.role.rules {
			if !RuleAllows(r, q) {
				continue
			}
			for _, s := range e.subjects {
				k := s.Key()
				ws := bySubject[k]
				if ws == nil {
					ws = &WhoCanSubject{Subject: s}
					bySubject[k] = ws
				}
				ws.Paths = append(ws.Paths, Path{Binding: e.binding, Role: e.roleRef, Scope: e.scope, Rule: r})
				if e.scope == "" {
					ws.ClusterWide = true
				}
			}
			break // one matching rule per binding is enough to explain it
		}
	}
	out := &WhoCanResult{Query: q, Warnings: g.warnings, Subjects: make([]WhoCanSubject, 0, len(bySubject))}
	for _, v := range bySubject {
		out.Subjects = append(out.Subjects, *v)
	}
	sort.Slice(out.Subjects, func(i, j int) bool {
		a, b := out.Subjects[i], out.Subjects[j]
		if a.ClusterWide != b.ClusterWide {
			return a.ClusterWide
		}
		return subjectLess(a.Subject, b.Subject)
	})
	return out
}

type grantSource struct {
	e   *edge
	via string
}

// SubjectPermissions builds the permission matrix of a subject. With a
// namespace, only grants that apply there (cluster-wide or in it) count.
func (g *Graph) SubjectPermissions(subject SubjectRef, namespace string) *PermissionMatrix {
	var srcs []grantSource
	for _, se := range g.subjectEdges(subject) {
		if inScope(se.e.scope, namespace) {
			srcs = append(srcs, grantSource{se.e, se.via})
		}
	}
	m := buildMatrix(srcs)
	m.Subject = &subject
	m.Namespace = namespace
	m.Warnings = g.warnings
	// Risks of grants reaching the subject only through broad implicit
	// groups belong to those groups, not to every subject in them.
	var direct []grantSource
	for _, s := range srcs {
		if s.via == "" {
			direct = append(direct, s)
		}
	}
	m.Risks = g.risksFor(direct, true)
	return m
}

// RolePermissions is the matrix of a single Role or ClusterRole, plus the
// bindings that reference it.
func (g *Graph) RolePermissions(kind, namespace, name string) (*PermissionMatrix, bool) {
	if kind == "ClusterRole" {
		namespace = ""
	}
	r := g.roles[roleKey(kind, namespace, name)]
	if r == nil {
		return nil, false
	}
	pseudo := &edge{binding: ObjectRef{}, roleRef: r.ref, role: r, scope: namespace}
	m := buildMatrix([]grantSource{{e: pseudo}})
	m.Bindings = nil
	for i := range g.edges {
		e := &g.edges[i]
		if e.roleRef == r.ref {
			m.Bindings = append(m.Bindings, BindingUse{Binding: e.binding, Role: e.roleRef, Scope: e.scope})
		}
	}
	if m.Bindings == nil {
		m.Bindings = []BindingUse{}
	}
	m.Warnings = g.warnings
	return m, true
}

// BindingPermissions is the matrix a binding grants and the subjects it names.
func (g *Graph) BindingPermissions(kind, namespace, name string) (*PermissionMatrix, []SubjectRef, bool) {
	if kind == "ClusterRoleBinding" {
		namespace = ""
	}
	for i := range g.edges {
		e := &g.edges[i]
		if e.binding.Kind == kind && e.binding.Namespace == namespace && e.binding.Name == name {
			m := buildMatrix([]grantSource{{e: e}})
			m.Risks = g.risksFor([]grantSource{{e: e}}, true)
			m.Warnings = g.warnings
			return m, e.subjects, true
		}
	}
	return nil, nil, false
}

func buildMatrix(srcs []grantSource) *PermissionMatrix {
	m := &PermissionMatrix{Bindings: []BindingUse{}, Rows: []MatrixRow{}}
	rows := map[string]*MatrixRow{}
	nonRes := map[string]*NonResRow{}
	extraVerbs := map[string]bool{}
	for _, s := range srcs {
		m.Bindings = append(m.Bindings, BindingUse{Binding: s.e.binding, Role: s.e.roleRef, Scope: s.e.scope, Via: s.via, RoleMissing: s.e.role == nil})
		if s.e.role == nil {
			continue
		}
		for _, r := range s.e.role.rules {
			p := Path{Binding: s.e.binding, Role: s.e.roleRef, Scope: s.e.scope, Rule: r, Via: s.via}
			for _, u := range r.NonResourceURLs {
				nr := nonRes[u]
				if nr == nil {
					nr = &NonResRow{URL: u}
					nonRes[u] = nr
				}
				nr.Verbs = appendUnique(nr.Verbs, r.Verbs...)
				nr.Paths = append(nr.Paths, p)
			}
			groups := r.APIGroups
			if len(groups) == 0 && len(r.Resources) > 0 {
				groups = []string{""}
			}
			for _, grp := range groups {
				for _, res := range r.Resources {
					k := grp + "|" + res
					row := rows[k]
					if row == nil {
						row = &MatrixRow{Group: grp, Resource: res, Cells: map[string]*Cell{}}
						rows[k] = row
					}
					verbs := r.Verbs
					if has(r.Verbs, "*") {
						verbs = append(append([]string{}, StandardVerbs...), "*")
					}
					for _, v := range verbs {
						if !isStandard(v) && v != "*" {
							extraVerbs[v] = true
						}
						c := row.Cells[v]
						if c == nil {
							c = &Cell{Limited: true}
							row.Cells[v] = c
						}
						c.Allowed = true
						if len(r.ResourceNames) == 0 {
							c.Limited = false
						}
						c.Paths = append(c.Paths, p)
					}
				}
			}
		}
	}
	m.Verbs = append([]string{}, StandardVerbs...)
	var extras []string
	for v := range extraVerbs {
		extras = append(extras, v)
	}
	sort.Strings(extras)
	m.Verbs = append(m.Verbs, extras...)
	for _, r := range rows {
		m.Rows = append(m.Rows, *r)
	}
	sort.Slice(m.Rows, func(i, j int) bool {
		a, b := m.Rows[i], m.Rows[j]
		if (a.Resource == "*") != (b.Resource == "*") {
			return a.Resource == "*"
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Resource < b.Resource
	})
	for _, n := range nonRes {
		m.NonResourceURLs = append(m.NonResourceURLs, *n)
	}
	sort.Slice(m.NonResourceURLs, func(i, j int) bool { return m.NonResourceURLs[i].URL < m.NonResourceURLs[j].URL })
	return m
}

func isStandard(v string) bool {
	for _, s := range StandardVerbs {
		if s == v {
			return true
		}
	}
	return false
}

func appendUnique(list []string, vs ...string) []string {
	for _, v := range vs {
		found := false
		for _, x := range list {
			if x == v {
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	return list
}
