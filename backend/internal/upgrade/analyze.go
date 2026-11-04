package upgrade

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Severity of a finding against the chosen target.
type Severity string

const (
	SeverityBlocker Severity = "blocker"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

func (s Severity) rank() int {
	switch s {
	case SeverityBlocker:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Source says where a deprecated apiVersion was seen. The version an object
// is stored and served in can differ from the one its author requested, so
// the live object alone never shows the problem.
type Source string

const (
	// SourceLive: the API server itself (request metrics, or objects of a kind
	// whose API has no replacement).
	SourceLive Source = "live"
	// SourceLastApplied: the kubectl.kubernetes.io/last-applied-configuration
	// annotation, i.e. the manifest someone applies.
	SourceLastApplied Source = "last-applied"
	// SourceHelm: the manifest stored in a Helm release.
	SourceHelm Source = "helm"
)

// ObservedObject is one object seen using an apiVersion.
type ObservedObject struct {
	// APIVersion is the version the source uses (possibly deprecated).
	APIVersion string `json:"apiVersion"`
	// LiveAPIVersion is the served version to open the object with; empty
	// when the object is not (or no longer) in the cluster.
	LiveAPIVersion string `json:"liveApiVersion,omitempty"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Namespace      string `json:"namespace,omitempty"`
	Source         Source `json:"source"`
	HelmRelease    string `json:"helmRelease,omitempty"`
	HelmNamespace  string `json:"helmNamespace,omitempty"`
}

// DeprecatedRequest is one series of apiserver_requested_deprecated_apis:
// the API server has served this deprecated API since it started.
type DeprecatedRequest struct {
	Group          string `json:"group"`
	Version        string `json:"version"`
	Resource       string `json:"resource"`
	Subresource    string `json:"subresource,omitempty"`
	RemovedRelease string `json:"removedRelease,omitempty"`
}

// NodeInfo is the version data of one node.
type NodeInfo struct {
	Name           string `json:"name"`
	KubeletVersion string `json:"kubeletVersion"`
}

// CRDVersion is one version of a CustomResourceDefinition.
type CRDVersion struct {
	Name               string `json:"name"`
	Served             bool   `json:"served"`
	Storage            bool   `json:"storage"`
	Deprecated         bool   `json:"deprecated"`
	DeprecationWarning string `json:"deprecationWarning,omitempty"`
}

// CRDInfo is what the readiness check needs from a CRD.
type CRDInfo struct {
	Name           string       `json:"name"`
	Group          string       `json:"group"`
	Kind           string       `json:"kind"`
	Versions       []CRDVersion `json:"versions"`
	StoredVersions []string     `json:"storedVersions"`
}

// CheckStatus of one evidence source.
type CheckStatus string

const (
	CheckOK          CheckStatus = "ok"
	CheckPartial     CheckStatus = "partial"
	CheckUnavailable CheckStatus = "unavailable"
)

// Check records whether an evidence source could be read, so a clean report
// is never mistaken for a complete one.
type Check struct {
	ID     string      `json:"id"`
	Label  string      `json:"label"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// Inputs is everything gathered from the cluster. It does not depend on the
// target, so one collection serves every target the user picks.
type Inputs struct {
	ServerVersion string              `json:"serverVersion"`
	Served        map[string]bool     `json:"-"` // "group/version/resource"
	Requests      []DeprecatedRequest `json:"requests"`
	Objects       []ObservedObject    `json:"objects"`
	Nodes         []NodeInfo          `json:"nodes"`
	CRDs          []CRDInfo           `json:"crds"`
	Checks        []Check             `json:"checks"`
	CollectedAt   time.Time           `json:"collectedAt"`
}

// AffectedResource is one object in a finding, with its own severity (a
// Helm manifest blocks an upgrade, a stale last-applied annotation does not).
type AffectedResource struct {
	ObservedObject
	Severity Severity `json:"severity"`
}

// APIFinding groups every use of one deprecated API.
type APIFinding struct {
	Severity     Severity `json:"severity"`
	APIVersion   string   `json:"apiVersion"`
	Kind         string   `json:"kind"`
	Resource     string   `json:"resource"`
	DeprecatedIn string   `json:"deprecatedIn"`
	RemovedIn    string   `json:"removedIn,omitempty"`
	Replacement  string   `json:"replacement,omitempty"`
	Hint         string   `json:"hint"`
	// RemovedByTarget: the API is gone in the target release.
	RemovedByTarget bool `json:"removedByTarget"`
	// AlreadyRemoved: the API is gone in the current release already.
	AlreadyRemoved bool `json:"alreadyRemoved"`
	// Served: the current API server still serves this version.
	Served bool `json:"served"`
	// LiveRequests: a client requested this API since the API server started.
	LiveRequests bool               `json:"liveRequests"`
	Message      string             `json:"message"`
	Resources    []AffectedResource `json:"resources"`
}

// NodeFinding is a kubelet that would be out of the supported skew.
type NodeFinding struct {
	Severity       Severity `json:"severity"`
	Node           string   `json:"node"`
	KubeletVersion string   `json:"kubeletVersion"`
	Message        string   `json:"message"`
}

// CRDFinding is a hint about a CustomResourceDefinition's versions.
type CRDFinding struct {
	Severity Severity `json:"severity"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Version  string   `json:"version"`
	Message  string   `json:"message"`
}

// Summary counts findings by severity. OK is the number of deprecated APIs
// in the target window that nothing uses.
type Summary struct {
	Blockers int    `json:"blockers"`
	Warnings int    `json:"warnings"`
	Infos    int    `json:"infos"`
	OK       int    `json:"ok"`
	Verdict  string `json:"verdict"` // "blocked", "warnings", "ready"
}

// NodeSummary describes kubelet versions across the cluster.
type NodeSummary struct {
	Total      int    `json:"total"`
	MinKubelet string `json:"minKubelet,omitempty"`
	MaxKubelet string `json:"maxKubelet,omitempty"`
	MaxSkew    int    `json:"maxSkew"`
}

// Report is the upgrade readiness of a cluster for one target release.
type Report struct {
	Cluster          string        `json:"cluster"`
	CurrentVersion   string        `json:"currentVersion"`
	ServerGitVersion string        `json:"serverGitVersion"`
	TargetVersion    string        `json:"targetVersion"`
	AvailableTargets []string      `json:"availableTargets"`
	Path             []string      `json:"path"`
	Summary          Summary       `json:"summary"`
	APIs             []APIFinding  `json:"apis"`
	Nodes            []NodeFinding `json:"nodes"`
	NodeSummary      NodeSummary   `json:"nodeSummary"`
	CRDs             []CRDFinding  `json:"crds"`
	Notes            []string      `json:"notes"`
	Checks           []Check       `json:"checks"`
	CatalogReviewed  string        `json:"catalogReviewed"`
	CatalogSource    string        `json:"catalogSource"`
	CollectedAt      time.Time     `json:"collectedAt"`
}

// MaxKubeletSkew is how many minors a kubelet may trail the API server
// (Kubernetes version skew policy since 1.28; 2 before).
func MaxKubeletSkew(apiserver Version) int {
	if apiserver.AtLeast(Version{1, 28}) {
		return 3
	}
	return 2
}

// Analyze turns collected inputs into a report for the target release. It is
// pure, so the same Inputs can be re-scored for every target.
func Analyze(cat *Catalog, cluster string, in *Inputs, targetStr string) (*Report, error) {
	current, err := ParseVersion(in.ServerVersion)
	if err != nil {
		return nil, err
	}
	targets := cat.TargetsFrom(current)
	var target Version
	if targetStr == "" {
		if len(targets) > 0 {
			target = mustVersion(targets[0])
		} else {
			target = current
		}
	} else {
		target, err = ParseVersion(targetStr)
		if err != nil {
			return nil, err
		}
		if target.Less(current) {
			return nil, fmt.Errorf("target %s is older than the cluster (%s)", target, current)
		}
	}

	rep := &Report{
		Cluster:          cluster,
		CurrentVersion:   current.String(),
		ServerGitVersion: in.ServerVersion,
		TargetVersion:    target.String(),
		AvailableTargets: targets,
		Checks:           in.Checks,
		CatalogReviewed:  cat.Reviewed,
		CatalogSource:    cat.Source,
		CollectedAt:      in.CollectedAt,
		APIs:             []APIFinding{},
		Nodes:            []NodeFinding{},
		CRDs:             []CRDFinding{},
		Notes:            []string{},
	}
	for m := current.Minor + 1; m <= target.Minor; m++ {
		rep.Path = append(rep.Path, Version{current.Major, m}.String())
	}
	if len(rep.Path) > 1 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("The control plane upgrades one minor at a time: %s → %s.", current, strings.Join(rep.Path, " → ")))
	}
	if latest := mustVersion(cat.LatestRelease); latest.Less(target) {
		rep.Notes = append(rep.Notes, fmt.Sprintf("The deprecation table covers releases up to %s; removals announced after that are not checked.", latest))
	}

	rep.APIs = analyzeAPIs(cat, in, current, target)
	rep.Nodes, rep.NodeSummary = analyzeNodes(in.Nodes, current, target)
	rep.CRDs = analyzeCRDs(in.CRDs)

	used := map[string]bool{}
	for _, f := range rep.APIs {
		used[f.APIVersion+"|"+f.Kind] = true
		countSeverity(&rep.Summary, f.Severity)
	}
	for _, n := range rep.Nodes {
		countSeverity(&rep.Summary, n.Severity)
	}
	for _, c := range rep.CRDs {
		countSeverity(&rep.Summary, c.Severity)
	}
	// Deprecated APIs in the window up to the target that nothing uses.
	for _, d := range cat.APIs {
		if !inWindow(d, current, target) || used[d.APIVersion()+"|"+d.Kind] {
			continue
		}
		rep.Summary.OK++
	}
	switch {
	case rep.Summary.Blockers > 0:
		rep.Summary.Verdict = "blocked"
	case rep.Summary.Warnings > 0:
		rep.Summary.Verdict = "warnings"
	default:
		rep.Summary.Verdict = "ready"
	}
	return rep, nil
}

func countSeverity(s *Summary, sev Severity) {
	switch sev {
	case SeverityBlocker:
		s.Blockers++
	case SeverityWarning:
		s.Warnings++
	case SeverityInfo:
		s.Infos++
	}
}

// inWindow: the API is removed after the current release and by the target,
// or newly deprecated by the target.
func inWindow(d Deprecation, current, target Version) bool {
	if d.RemovedIn != "" {
		r := mustVersion(d.RemovedIn)
		return current.Less(r) && target.AtLeast(r)
	}
	return target.AtLeast(mustVersion(d.DeprecatedIn))
}

// severityFor scores one use of a deprecated API from a given source.
func severityFor(d Deprecation, src Source, current, target Version) (Severity, bool) {
	deprecated := target.AtLeast(mustVersion(d.DeprecatedIn))
	if d.RemovedIn == "" {
		if deprecated {
			return SeverityInfo, true
		}
		return "", false
	}
	removed := mustVersion(d.RemovedIn)
	switch {
	case target.AtLeast(removed):
		switch src {
		case SourceHelm, SourceLive:
			// helm upgrade diffs against the stored manifest and fails on
			// a kind it can no longer map; a live client stops working.
			return SeverityBlocker, true
		default:
			// The stored object survives (the API server converts it); the
			// manifest that created it will fail to apply.
			return SeverityWarning, true
		}
	case deprecated:
		return SeverityWarning, true
	}
	return "", false
}

func analyzeAPIs(cat *Catalog, in *Inputs, current, target Version) []APIFinding {
	byKey := map[string]*APIFinding{}
	order := []string{}
	get := func(d Deprecation) *APIFinding {
		key := d.APIVersion() + "|" + d.Kind
		if f, ok := byKey[key]; ok {
			return f
		}
		f := &APIFinding{
			APIVersion:      d.APIVersion(),
			Kind:            d.Kind,
			Resource:        d.Resource,
			DeprecatedIn:    d.DeprecatedIn,
			RemovedIn:       d.RemovedIn,
			Replacement:     d.Replacement,
			Hint:            d.Hint,
			Served:          in.Served[d.Group+"/"+d.Version+"/"+d.Resource],
			Resources:       []AffectedResource{},
			RemovedByTarget: d.RemovedIn != "" && target.AtLeast(mustVersion(d.RemovedIn)),
			AlreadyRemoved:  d.RemovedIn != "" && current.AtLeast(mustVersion(d.RemovedIn)),
		}
		byKey[key] = f
		order = append(order, key)
		return f
	}
	raise := func(f *APIFinding, s Severity) {
		if s.rank() > f.Severity.rank() {
			f.Severity = s
		}
	}

	for _, r := range in.Requests {
		if r.Subresource != "" {
			continue
		}
		d, ok := cat.LookupResource(r.Group, r.Version, r.Resource)
		if !ok {
			continue
		}
		sev, relevant := severityFor(d, SourceLive, current, target)
		if !relevant {
			continue
		}
		f := get(d)
		f.LiveRequests = true
		raise(f, sev)
	}

	seen := map[string]bool{}
	for _, o := range in.Objects {
		d, ok := cat.Lookup(o.APIVersion, o.Kind)
		if !ok {
			continue
		}
		sev, relevant := severityFor(d, o.Source, current, target)
		if !relevant {
			continue
		}
		id := strings.Join([]string{o.APIVersion, o.Kind, o.Namespace, o.Name, string(o.Source), o.HelmNamespace, o.HelmRelease}, "|")
		if seen[id] {
			continue
		}
		seen[id] = true
		f := get(d)
		f.Resources = append(f.Resources, AffectedResource{ObservedObject: o, Severity: sev})
		raise(f, sev)
	}

	out := make([]APIFinding, 0, len(order))
	for _, k := range order {
		f := byKey[k]
		sort.SliceStable(f.Resources, func(i, j int) bool {
			a, b := f.Resources[i], f.Resources[j]
			if a.Severity.rank() != b.Severity.rank() {
				return a.Severity.rank() > b.Severity.rank()
			}
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			return a.Name < b.Name
		})
		f.Message = apiMessage(f, target)
		out = append(out, *f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity.rank() != out[j].Severity.rank() {
			return out[i].Severity.rank() > out[j].Severity.rank()
		}
		if out[i].RemovedIn != out[j].RemovedIn {
			return out[i].RemovedIn < out[j].RemovedIn
		}
		return out[i].APIVersion+out[i].Kind < out[j].APIVersion+out[j].Kind
	})
	return out
}

func apiMessage(f *APIFinding, target Version) string {
	what := f.Kind + " " + f.APIVersion
	switch {
	case f.AlreadyRemoved:
		return fmt.Sprintf("%s was removed in %s, yet manifests still use it.", what, f.RemovedIn)
	case f.RemovedByTarget:
		return fmt.Sprintf("%s is removed in %s.", what, f.RemovedIn)
	case f.RemovedIn != "":
		return fmt.Sprintf("%s is deprecated and will be removed in %s.", what, f.RemovedIn)
	default:
		return fmt.Sprintf("%s is deprecated since %s; no removal is scheduled.", what, f.DeprecatedIn)
	}
}

func analyzeNodes(nodes []NodeInfo, current, target Version) ([]NodeFinding, NodeSummary) {
	sum := NodeSummary{Total: len(nodes)}
	out := []NodeFinding{}
	limit := MaxKubeletSkew(target)
	var minV, maxV Version
	for i, n := range nodes {
		v, err := ParseVersion(n.KubeletVersion)
		if err != nil {
			continue
		}
		if i == 0 || v.Less(minV) {
			minV = v
		}
		if i == 0 || maxV.Less(v) {
			maxV = v
		}
		skew := target.Minor - v.Minor
		if skew > sum.MaxSkew {
			sum.MaxSkew = skew
		}
		switch {
		case current.Less(v):
			out = append(out, NodeFinding{Severity: SeverityWarning, Node: n.Name, KubeletVersion: n.KubeletVersion,
				Message: fmt.Sprintf("Kubelet %s is newer than the control plane (%s), which the skew policy does not support.", v, current)})
		case skew > limit:
			out = append(out, NodeFinding{Severity: SeverityBlocker, Node: n.Name, KubeletVersion: n.KubeletVersion,
				Message: fmt.Sprintf("Kubelet %s would trail a %s control plane by %d minors (max %d). Upgrade this node first.", v, target, skew, limit)})
		case skew == limit && skew > 0:
			out = append(out, NodeFinding{Severity: SeverityWarning, Node: n.Name, KubeletVersion: n.KubeletVersion,
				Message: fmt.Sprintf("Kubelet %s would be at the skew limit (%d minors) under %s. Plan the node upgrade right after the control plane.", v, limit, target)})
		}
	}
	if len(nodes) > 0 && !minV.IsZero() {
		sum.MinKubelet = minV.String()
		sum.MaxKubelet = maxV.String()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity.rank() != out[j].Severity.rank() {
			return out[i].Severity.rank() > out[j].Severity.rank()
		}
		return out[i].Node < out[j].Node
	})
	return out, sum
}

func analyzeCRDs(crds []CRDInfo) []CRDFinding {
	out := []CRDFinding{}
	for _, c := range crds {
		served := map[string]CRDVersion{}
		for _, v := range c.Versions {
			served[v.Name] = v
		}
		for _, sv := range c.StoredVersions {
			v, ok := served[sv]
			switch {
			case !ok || !v.Served:
				out = append(out, CRDFinding{Severity: SeverityWarning, Name: c.Name, Kind: c.Kind, Version: sv,
					Message: fmt.Sprintf("status.storedVersions still lists %s, which is no longer served. Run a storage version migration, then drop it from storedVersions before the chart removes it.", sv)})
			case v.Deprecated && !v.Storage:
				out = append(out, CRDFinding{Severity: SeverityWarning, Name: c.Name, Kind: c.Kind, Version: sv,
					Message: fmt.Sprintf("Objects may still be stored as deprecated %s. Migrate them before the operator drops this version.", sv)})
			}
		}
		for _, v := range c.Versions {
			if v.Deprecated && v.Storage {
				msg := fmt.Sprintf("The storage version %s is marked deprecated.", v.Name)
				if v.DeprecationWarning != "" {
					msg += " " + v.DeprecationWarning
				}
				out = append(out, CRDFinding{Severity: SeverityInfo, Name: c.Name, Kind: c.Kind, Version: v.Name, Message: msg})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity.rank() != out[j].Severity.rank() {
			return out[i].Severity.rank() > out[j].Severity.rank()
		}
		return out[i].Name < out[j].Name
	})
	return out
}
