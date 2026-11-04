package health

import (
	"context"
	"errors"
	"strings"
)

// Service is the health feature's API: what the HTTP handler serves and what
// the MCP layer wraps (get_issues, audit).
type Service struct {
	loader  *Loader
	ignores *IgnoreStore
}

func NewService(loader *Loader, ignores *IgnoreStore) *Service {
	if ignores == nil {
		ignores = NewIgnoreStore("")
	}
	return &Service{loader: loader, ignores: ignores}
}

var errNoCluster = errors.New("cluster is required")

// Issues returns the cluster's live issues, ranked, deduplicated and grouped
// by owner, narrowed by the filter.
func (s *Service) Issues(ctx context.Context, f IssueFilter) (IssuesReport, error) {
	if f.Cluster == "" {
		return IssuesReport{}, errNoCluster
	}
	snap, err := s.loader.Load(ctx, f.Cluster)
	if err != nil {
		return IssuesReport{}, err
	}
	rep := EvaluateIssues(snap)
	rep.Issues = FilterIssues(rep.Issues, f)
	rep.Summary = summarize(rep.Issues)
	return rep, nil
}

func summarize(xs []Issue) IssueSummary {
	var sum IssueSummary
	for _, is := range xs {
		sum.Total++
		switch is.Severity {
		case SeverityCritical:
			sum.Critical++
		case SeverityWarning:
			sum.Warning++
		default:
			sum.Info++
		}
	}
	return sum
}

// FilterIssues applies an IssueFilter to evaluated issues.
func FilterIssues(xs []Issue, f IssueFilter) []Issue {
	ns := toSet(f.Namespaces)
	kinds := toSet(f.Kinds)
	sev := map[Severity]bool{}
	for _, s := range f.Severities {
		sev[s] = true
	}
	q := strings.ToLower(f.Search)
	out := []Issue{}
	for _, is := range xs {
		if len(ns) > 0 && !ns[is.Resource.Namespace] {
			continue
		}
		if len(kinds) > 0 && !kinds[is.Resource.Kind] {
			continue
		}
		if len(sev) > 0 && !sev[is.Severity] {
			continue
		}
		if f.Resource != nil && !issueTouches(is, *f.Resource) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(is.Title+" "+is.Reason+" "+is.Message+" "+is.Resource.Name+" "+is.Resource.Namespace), q) {
			continue
		}
		out = append(out, is)
	}
	return out
}

func sameRef(a, b ResourceRef) bool {
	return a.Kind == b.Kind && a.Name == b.Name && a.Namespace == b.Namespace
}

func issueTouches(is Issue, r ResourceRef) bool {
	if sameRef(is.Resource, r) {
		return true
	}
	for _, a := range is.Affected {
		if sameRef(a, r) {
			return true
		}
	}
	return false
}

func toSet(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		if x != "" {
			m[x] = true
		}
	}
	return m
}

// Audit runs the best-practice checks over the scope and scores them.
func (s *Service) Audit(ctx context.Context, scope AuditScope) (AuditReport, error) {
	if scope.Cluster == "" {
		return AuditReport{}, errNoCluster
	}
	snap, err := s.loader.Load(ctx, scope.Cluster)
	if err != nil {
		return AuditReport{}, err
	}
	return EvaluateAudit(snap, scope, s.ignores.Matcher(scope.Cluster)), nil
}

// Resource returns one object's issues and audit findings, for the
// inspector. A pod shows the findings of the workload that owns it, since
// that is where the fix goes.
func (s *Service) Resource(ctx context.Context, cluster string, ref ResourceRef) (ResourceHealth, error) {
	if cluster == "" {
		return ResourceHealth{}, errNoCluster
	}
	snap, err := s.loader.Load(ctx, cluster)
	if err != nil {
		return ResourceHealth{}, err
	}
	out := ResourceHealth{Issues: FilterIssues(EvaluateIssues(snap).Issues, IssueFilter{Resource: &ref}), Findings: []Finding{}, Checks: []Check{}}
	target := ref
	if ref.Kind == "Pod" {
		for _, p := range snap.Pods {
			if p.Namespace == ref.Namespace && p.Name == ref.Name {
				target = newOwners(snap).ownerOfPod(p)
			}
		}
	}
	rep := EvaluateAudit(snap, AuditScope{Cluster: cluster, Resource: &target, IncludeSystem: true}, s.ignores.Matcher(cluster))
	out.Findings = rep.Findings
	used := map[string]bool{}
	for _, f := range rep.Findings {
		if !used[f.CheckID] {
			used[f.CheckID] = true
			if c, ok := CheckByID(f.CheckID); ok {
				out.Checks = append(out.Checks, c)
			}
		}
	}
	return out, nil
}

// Checks lists every audit check with its explanation and fix.
func (s *Service) Checks() []Check { return Checks() }

func (s *Service) Ignores(cluster string) []Ignore { return s.ignores.List(cluster) }
func (s *Service) AddIgnore(ig Ignore) error       { return s.ignores.Add(ig) }
func (s *Service) RemoveIgnore(ig Ignore) error    { return s.ignores.Remove(ig) }

// Refresh drops the cached listing so the next call reads the cluster again.
func (s *Service) Refresh(cluster string) { s.loader.Forget(cluster) }
