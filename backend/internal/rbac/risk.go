package rbac

import (
	"fmt"
	"sort"
	"strings"
)

// isSystemBinding reports whether a binding ships with Kubernetes or its
// installer; their broad grants are intended.
func isSystemBinding(name string) bool {
	return strings.HasPrefix(name, "system:") || strings.HasPrefix(name, "kubeadm:")
}

func scopeLabel(scope string) string {
	if scope == "" {
		return "cluster-wide"
	}
	return "in namespace " + scope
}

type riskCheck struct {
	category string
	title    string
	// severity returns "" when the rule does not trip the check.
	match func(r Rule, clusterWide bool) Severity
}

func any3(r Rule, verbs []string, groups []string, resources []string) bool {
	for _, v := range verbs {
		for _, g := range groups {
			for _, res := range resources {
				sub := ""
				base := res
				if b, s, ok := strings.Cut(res, "/"); ok {
					base, sub = b, s
				}
				if RuleAllows(r, Query{Verb: v, Group: g, Resource: base, Subresource: sub}) {
					return true
				}
			}
		}
	}
	return false
}

func pick(clusterWide bool, cw, ns Severity) Severity {
	if clusterWide {
		return cw
	}
	return ns
}

var riskChecks = []riskCheck{
	{"wildcard", "Full wildcard access", func(r Rule, cw bool) Severity {
		if has(r.Verbs, "*") && has(r.APIGroups, "*") && has(r.Resources, "*") && len(r.ResourceNames) == 0 {
			return pick(cw, SeverityCritical, SeverityHigh)
		}
		return ""
	}},
	{"wildcard", "Wildcard verbs or resources", func(r Rule, cw bool) Severity {
		full := has(r.Verbs, "*") && has(r.APIGroups, "*") && has(r.Resources, "*")
		if full || len(r.Resources) == 0 || len(r.ResourceNames) > 0 {
			return ""
		}
		if contains(r.Verbs, "*") || contains(r.Resources, "*") {
			return pick(cw, SeverityHigh, SeverityMedium)
		}
		return ""
	}},
	{"secrets", "Can read Secrets", func(r Rule, cw bool) Severity {
		if len(r.ResourceNames) == 0 && any3(r, []string{"get", "list", "watch"}, []string{""}, []string{"secrets"}) {
			return pick(cw, SeverityHigh, SeverityMedium)
		}
		return ""
	}},
	{"escalation", "Can escalate or bind roles", func(r Rule, cw bool) Severity {
		if any3(r, []string{"escalate", "bind"}, []string{"rbac.authorization.k8s.io"}, []string{"roles", "clusterroles"}) {
			return SeverityCritical
		}
		if any3(r, []string{"create", "update", "patch"}, []string{"rbac.authorization.k8s.io"}, []string{"rolebindings", "clusterrolebindings"}) {
			return pick(cw, SeverityHigh, SeverityMedium)
		}
		return ""
	}},
	{"impersonation", "Can impersonate", func(r Rule, cw bool) Severity {
		if any3(r, []string{"impersonate"}, []string{"", "authentication.k8s.io"}, []string{"users", "groups", "serviceaccounts", "userextras/*", "uids"}) {
			return SeverityCritical
		}
		return ""
	}},
	{"exec", "Can exec into or attach to pods", func(r Rule, cw bool) Severity {
		if any3(r, []string{"create", "get"}, []string{""}, []string{"pods/exec", "pods/attach"}) {
			return pick(cw, SeverityHigh, SeverityMedium)
		}
		return ""
	}},
	{"nodes", "Can proxy to nodes", func(r Rule, cw bool) Severity {
		if any3(r, []string{"get", "create"}, []string{""}, []string{"nodes/proxy"}) {
			return SeverityHigh
		}
		return ""
	}},
	{"tokens", "Can mint service account tokens", func(r Rule, cw bool) Severity {
		if any3(r, []string{"create"}, []string{""}, []string{"serviceaccounts/token"}) {
			return pick(cw, SeverityHigh, SeverityMedium)
		}
		return ""
	}},
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// risksFor evaluates the bindings in srcs.
func (g *Graph) risksFor(srcs []grantSource, includeSystem bool) []Risk {
	var out []Risk
	for _, s := range srcs {
		out = append(out, edgeRisks(s.e, includeSystem)...)
	}
	sortRisks(out)
	return out
}

func edgeRisks(e *edge, includeSystem bool) []Risk {
	system := isSystemBinding(e.binding.Name)
	if system && !includeSystem {
		return nil
	}
	var out []Risk
	add := func(sev Severity, cat, title, detail string) {
		out = append(out, Risk{
			ID:       fmt.Sprintf("%s:%s/%s/%s#%d", cat, e.binding.Kind, e.binding.Namespace, e.binding.Name, len(out)),
			Severity: sev, Category: cat, Title: title, Detail: detail,
			Binding: e.binding, Role: e.roleRef, Scope: e.scope, Subjects: e.subjects, System: system,
		})
	}
	cw := e.scope == ""
	if e.roleRef.Kind == "ClusterRole" && e.roleRef.Name == "cluster-admin" && e.binding.Kind != "" {
		add(pick(cw, SeverityCritical, SeverityHigh), "cluster-admin", "Binds cluster-admin",
			"Grants full control "+scopeLabel(e.scope)+".")
	} else if e.role != nil {
		seen := map[string]bool{}
		for _, r := range e.role.rules {
			for _, c := range riskChecks {
				if seen[c.title] {
					continue
				}
				if sev := c.match(r, cw); sev != "" {
					seen[c.title] = true
					if c.category == "wildcard" {
						seen["Wildcard verbs or resources"] = true
						seen["Full wildcard access"] = true
					}
					add(sev, c.category, c.title, ruleText(r)+" "+scopeLabel(e.scope)+".")
				}
			}
		}
	}
	if e.binding.Kind != "" {
		for _, s := range e.subjects {
			if s.Kind == KindServiceAccount && s.Name == "default" {
				add(SeverityMedium, "default-sa", "Grants permissions to a default service account",
					"Every pod in "+s.Namespace+" without its own service account inherits "+e.roleRef.Name+".")
			}
			if s.Kind == KindGroup && (s.Name == "system:unauthenticated" || s.Name == "system:anonymous") ||
				s.Kind == KindUser && s.Name == "system:anonymous" {
				add(SeverityCritical, "anonymous", "Grants access to unauthenticated users",
					s.Name+" receives "+e.roleRef.Name+" "+scopeLabel(e.scope)+".")
			}
		}
	}
	return out
}

// ruleText renders a rule compactly: "get, list secrets".
func ruleText(r Rule) string {
	res := r.Resources
	if len(res) == 0 {
		res = r.NonResourceURLs
	}
	s := strings.Join(r.Verbs, ", ") + " " + strings.Join(res, ", ")
	if len(r.ResourceNames) > 0 {
		s += " (" + strings.Join(r.ResourceNames, ", ") + ")"
	}
	return s
}

var severityRank = map[Severity]int{SeverityCritical: 0, SeverityHigh: 1, SeverityMedium: 2}

func sortRisks(rs []Risk) {
	sort.SliceStable(rs, func(i, j int) bool {
		if severityRank[rs[i].Severity] != severityRank[rs[j].Severity] {
			return severityRank[rs[i].Severity] < severityRank[rs[j].Severity]
		}
		if rs[i].Category != rs[j].Category {
			return rs[i].Category < rs[j].Category
		}
		return rs[i].ID < rs[j].ID
	})
}

// RiskyGrants lists every risky grant in the cluster.
func (g *Graph) RiskyGrants(opts RiskOptions) []Risk {
	out := []Risk{}
	for i := range g.edges {
		e := &g.edges[i]
		if opts.Namespace != "" && e.scope != "" && e.scope != opts.Namespace {
			continue
		}
		out = append(out, edgeRisks(e, opts.IncludeSystem)...)
	}
	sortRisks(out)
	return out
}
