package featuretools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/certs"
	"github.com/kanivet/backend/internal/changes"
	"github.com/kanivet/backend/internal/compare"
	"github.com/kanivet/backend/internal/health"
	"github.com/kanivet/backend/internal/images"
	"github.com/kanivet/backend/internal/mcp"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/network"
	"github.com/kanivet/backend/internal/rbac"
	"github.com/kanivet/backend/internal/topology"
	"github.com/kanivet/backend/internal/upgrade"
)

const listCap = 100

// ---------------------------------------------------------------------------
// health

type issuesArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
	Severity  string `json:"severity,omitempty" jsonschema:"critical or warning"`
	Kind      string `json:"kind,omitempty" jsonschema:"limit to issues about this kind"`
	Search    string `json:"search,omitempty" jsonschema:"substring over title, reason, message and name"`
}

type auditArgs struct {
	mcpreg.ClusterArg
	Namespace     string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
	IncludeSystem bool   `json:"includeSystem,omitempty" jsonschema:"also audit kube-system and other control-plane namespaces"`
}

func registerHealth(r *mcpreg.Registry, svc *health.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_issues", Title: "Get issues", Feature: "health",
		Description: "Live, ranked issues from Kanivet's health engine (crash loops, unschedulable pods, failing rollouts, expiring certificates, ...), grouped by owning workload, each with a one-line hint." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in issuesArgs) (any, error) {
		f := health.IssueFilter{Cluster: call.Cluster, Search: in.Search}
		if in.Namespace != "" {
			f.Namespaces = []string{in.Namespace}
		}
		if in.Kind != "" {
			f.Kinds = []string{in.Kind}
		}
		if in.Severity != "" {
			f.Severities = []health.Severity{health.Severity(in.Severity)}
		}
		rep, err := svc.Issues(ctx, f)
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(rep, listCap, unavailable(rep.Unavailable)), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "audit", Title: "Best-practice audit", Feature: "health",
		Description: "Best-practice audit (probes, limits, security context, PDBs, ...): score, per-namespace scores, failing checks and findings with the fix." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in auditArgs) (any, error) {
		sc := health.AuditScope{Cluster: call.Cluster, IncludeSystem: in.IncludeSystem}
		if in.Namespace != "" {
			sc.Namespaces = []string{in.Namespace}
		}
		rep, err := svc.Audit(ctx, sc)
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(rep, listCap, unavailable(rep.Unavailable)), nil
	})
}

func unavailable(kinds []string) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k+": could not be read")
	}
	return out
}

// ---------------------------------------------------------------------------
// changes

type changesArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
	Kind      string `json:"kind,omitempty" jsonschema:"limit to one kind"`
	Name      string `json:"name,omitempty" jsonschema:"with kind: the history of one object instead of the cluster feed"`
	Since     string `json:"since,omitempty" jsonschema:"Go duration, default 24h"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max entries, default 50"`
}

type snapshotArgs struct {
	mcpreg.ClusterArg
	ID string `json:"id" jsonschema:"entry id from get_changes (helm:... entries have snapshots)"`
}

func registerChanges(r *mcpreg.Registry, svc *changes.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_changes", Title: "Get changes", Feature: "changes",
		Description: "What changed recently: rollouts, image/env/config/replica changes, Helm/Argo/Flux revisions, with actor and touched fields, newest first. Give kind+name for one object's history." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in changesArgs) (any, error) {
		limit := in.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		if in.Kind != "" && in.Name != "" {
			g, v := groupVersion(in.Kind, "")
			h, err := svc.ChangesFor(ctx, changes.ResourceRef{Cluster: call.Cluster, Group: g, Version: v, Kind: in.Kind, Namespace: in.Namespace, Name: in.Name})
			if err != nil {
				return explain(err, in.Kind, in.Namespace)
			}
			return respond(h, limit, h.Warnings), nil
		}
		since := 24 * time.Hour
		if in.Since != "" {
			d, err := time.ParseDuration(in.Since)
			if err != nil {
				return nil, fmt.Errorf("since: %w", err)
			}
			since = d
		}
		f := changes.Filter{Since: time.Now().Add(-since), Limit: limit}
		if in.Namespace != "" {
			f.Namespaces = []string{in.Namespace}
		}
		if in.Kind != "" {
			f.Kinds = []string{in.Kind}
		}
		feed, err := svc.RecentChanges(ctx, call.Cluster, f)
		if err != nil {
			return explain(err, in.Kind, in.Namespace)
		}
		return respond(feed, limit, feed.Warnings), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_change_snapshot", Title: "Get change snapshot", Feature: "changes",
		Description: "The manifest as it was at one change entry (Helm revisions), secrets scrubbed." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in snapshotArgs) (any, error) {
		s, err := svc.Snapshot(ctx, call.Cluster, in.ID)
		if err != nil {
			return explain(err, "", "")
		}
		return mcp.RedactManifest(s), nil
	})
}

// ---------------------------------------------------------------------------
// topology

type topologyArgs struct {
	mcpreg.ClusterArg
	Namespace string   `json:"namespace" jsonschema:"namespace to graph"`
	Kinds     []string `json:"kinds,omitempty" jsonschema:"keep only these kinds"`
	MaxNodes  int      `json:"maxNodes,omitempty" jsonschema:"cap, default 150"`
}

type relatedArgs struct {
	objectArgs
	Depth int `json:"depth,omitempty" jsonschema:"hops, default 1, max 3"`
}

func registerTopology(r *mcpreg.Registry, svc *topology.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_topology", Title: "Namespace topology", Feature: "topology",
		Description: "The namespace's resource graph: nodes (kind, name, health) and edges (owns, selects, mounts, routes, binds)." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in topologyArgs) (any, error) {
		max := in.MaxNodes
		if max <= 0 || max > 500 {
			max = 150
		}
		g, err := svc.NamespaceGraph(ctx, call.Cluster, in.Namespace, topology.Filters{Kinds: in.Kinds, MaxNodes: max})
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		var extra []mcp.Reason
		if g.Truncated {
			extra = append(extra, mcp.Truncated("", len(g.Nodes), len(g.Nodes)+1))
		}
		return respond(g, 0, g.Warnings, extra...), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_related", Title: "Related resources", Feature: "topology",
		Description: "Objects related to one resource (owners, pods, services, ingresses, config, volumes, bindings) within depth hops, as a graph." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in relatedArgs) (any, error) {
		depth := in.Depth
		if depth <= 0 {
			depth = 1
		}
		if depth > 3 {
			depth = 3
		}
		g, v := groupVersion(in.Kind, in.APIVersion)
		gr, err := svc.Neighbors(ctx, call.Cluster, topology.Ref{Group: g, Version: v, Kind: in.Kind, Namespace: in.Namespace, Name: in.Name}, in.Namespace, depth, false)
		if err != nil {
			return explain(err, in.Kind, in.Namespace)
		}
		return respond(gr, 0, gr.Warnings), nil
	})
}

// ---------------------------------------------------------------------------
// certs

type certsArgs struct {
	mcpreg.ClusterArg
	Namespace    string `json:"namespace,omitempty" jsonschema:"limit to one namespace"`
	ExpiringDays int    `json:"expiringDays,omitempty" jsonschema:"only certificates expiring within N days (includes expired)"`
	Search       string `json:"search,omitempty" jsonschema:"substring over subject, SANs, issuer, owner"`
}

func registerCerts(r *mcpreg.Registry, svc *certs.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "list_certificates", Title: "List certificates", Feature: "certs",
		Description: "TLS certificates in the cluster (TLS Secrets, webhook CAs, cert-manager Certificates): subject, SANs, issuer, expiry and days left, where they are used, plus issues (expired, expiring, host not covered). No key material." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in certsArgs) (any, error) {
		rep, err := svc.Certificates(ctx, call.Cluster, certs.Filter{Namespace: in.Namespace, ExpiringWithinDays: in.ExpiringDays, Search: in.Search})
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(rep, listCap, rep.Warnings), nil
	})
}

// ---------------------------------------------------------------------------
// rbac

type whoCanArgs struct {
	mcpreg.ClusterArg
	Verb        string `json:"verb" jsonschema:"get, list, create, delete, ..."`
	Resource    string `json:"resource" jsonschema:"plural resource, e.g. secrets"`
	Group       string `json:"group,omitempty" jsonschema:"API group, empty for core"`
	Subresource string `json:"subresource,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name,omitempty" jsonschema:"one object name"`
}

type subjectArgs struct {
	mcpreg.ClusterArg
	SubjectKind      string `json:"subjectKind" jsonschema:"User, Group or ServiceAccount"`
	SubjectName      string `json:"subjectName"`
	SubjectNamespace string `json:"subjectNamespace,omitempty" jsonschema:"ServiceAccount namespace"`
	Namespace        string `json:"namespace,omitempty" jsonschema:"evaluate in this namespace (default all)"`
}

type riskArgs struct {
	mcpreg.ClusterArg
	Namespace     string `json:"namespace,omitempty"`
	IncludeSystem bool   `json:"includeSystem,omitempty" jsonschema:"include Kubernetes' own system bindings"`
}

func registerRBAC(r *mcpreg.Registry, svc *rbac.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "who_can", Title: "Who can", Feature: "rbac",
		Description: "Subjects (users, groups, service accounts) allowed a verb on a resource, with the binding and role that grant it." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in whoCanArgs) (any, error) {
		res, err := svc.WhoCan(ctx, call.Cluster, rbac.Query{Verb: in.Verb, Group: in.Group, Resource: in.Resource, Subresource: in.Subresource, Namespace: in.Namespace, Name: in.Name})
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(res, listCap, res.Warnings), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "subject_permissions", Title: "Subject permissions", Feature: "rbac",
		Description: "What one user/group/service account may do: resource x verb matrix, the bindings it comes from, and risky grants." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in subjectArgs) (any, error) {
		m, err := svc.SubjectPermissions(ctx, call.Cluster, rbac.SubjectRef{Kind: in.SubjectKind, Name: in.SubjectName, Namespace: in.SubjectNamespace}, in.Namespace)
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(m, listCap, m.Warnings), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "risky_grants", Title: "Risky grants", Feature: "rbac",
		Description: "RBAC grants that allow privilege escalation or secret access (wildcards, escalate/bind/impersonate, secrets read, exec, node proxy), with subjects and scope." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in riskArgs) (any, error) {
		rs, err := svc.RiskyGrants(ctx, call.Cluster, rbac.RiskOptions{IncludeSystem: in.IncludeSystem, Namespace: in.Namespace})
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(map[string]any{"risks": rs}, listCap, nil), nil
	})
}

// ---------------------------------------------------------------------------
// upgrade

type upgradeArgs struct {
	mcpreg.ClusterArg
	TargetVersion string `json:"targetVersion,omitempty" jsonschema:"e.g. 1.32; default the next minor"`
}

func registerUpgrade(r *mcpreg.Registry, svc *upgrade.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "upgrade_readiness", Title: "Upgrade readiness", Feature: "upgrade",
		Description: "Readiness for a Kubernetes minor upgrade: deprecated/removed APIs in use (live objects and Helm manifests) with replacements, and node version skew." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in upgradeArgs) (any, error) {
		rep, err := svc.UpgradeReport(ctx, call.Cluster, in.TargetVersion, false)
		if err != nil {
			return explain(err, "", "")
		}
		return respond(rep, listCap, nil), nil
	})
}

// ---------------------------------------------------------------------------
// network

type endpointArg struct {
	Kind      string `json:"kind" jsonschema:"Pod, Deployment, Service, ... or External"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	IP        string `json:"ip,omitempty" jsonschema:"for External"`
}

func (e endpointArg) ref() network.Ref {
	return network.Ref{Kind: e.Kind, Namespace: e.Namespace, Name: e.Name, IP: e.IP}
}

type pathArgs struct {
	mcpreg.ClusterArg
	Source      endpointArg `json:"source"`
	Destination endpointArg `json:"destination"`
	Port        int32       `json:"port" jsonschema:"destination port"`
	Protocol    string      `json:"protocol,omitempty" jsonschema:"TCP (default), UDP or SCTP"`
}

type coverageArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace" jsonschema:"namespace to report"`
}

type probeArgs struct {
	pathArgs
	MimicLabels bool `json:"mimicLabels,omitempty" jsonschema:"give the probe pod the source pod's labels"`
	Confirm     bool `json:"confirm" jsonschema:"must be true; confirm with the user first"`
}

func registerNetwork(r *mcpreg.Registry, svc *network.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "check_network_path", Title: "Check network path", Feature: "network",
		Description: "Can source reach destination on a port? Evaluates Services/endpoints and NetworkPolicies hop by hop and says which policy blocks or allows it. Static analysis; no traffic is sent." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in pathArgs) (any, error) {
		res, err := svc.CheckPath(ctx, call.Cluster, network.PathRequest{Source: in.Source.ref(), Destination: in.Destination.ref(), Port: in.Port, Protocol: in.Protocol})
		if err != nil {
			return explain(err, "", in.Source.Namespace)
		}
		return respond(res, 50, nil), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "network_policy_coverage", Title: "Network policy coverage", Feature: "network",
		Description: "Which workloads of a namespace are isolated by NetworkPolicies (ingress/egress), the policies and the enforcer detected." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in coverageArgs) (any, error) {
		rep, err := svc.PolicyCoverage(ctx, call.Cluster, in.Namespace)
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(rep, listCap, rep.Warnings), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "network_probe", Title: "Network probe", Feature: "network", Write: true, Destructive: true,
		Description: "Starts a short-lived probe pod next to the source and tries a real connection to the destination port. Creates a pod in the cluster: confirm with the user and pass confirm=true.",
	}, func(ctx context.Context, call *mcpreg.Call, in probeArgs) (any, error) {
		if !in.Confirm {
			return nil, errors.New("network_probe creates a pod; ask the user, then call again with confirm=true")
		}
		res, err := svc.Probe(ctx, call.Cluster, network.ProbeRequest{Source: in.Source.ref(), Destination: in.Destination.ref(), Port: in.Port, MimicLabels: in.MimicLabels, Confirm: true})
		if err != nil {
			return nil, err
		}
		return respond(res, 50, nil), nil
	})
}

// ---------------------------------------------------------------------------
// compare

type compareArgs struct {
	mcpreg.ClusterArg
	Kind          string `json:"kind"`
	APIVersion    string `json:"apiVersion,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	Name          string `json:"name"`
	OtherCluster  string `json:"otherCluster,omitempty" jsonschema:"compare with the same or another object in this cluster"`
	OtherNs       string `json:"otherNamespace,omitempty"`
	OtherName     string `json:"otherName,omitempty" jsonschema:"defaults to name"`
	IncludeStatus bool   `json:"includeStatus,omitempty"`
}

type driftArgs struct {
	objectArgs
	Source string `json:"source,omitempty" jsonschema:"last-applied (default) or helm"`
}

func registerCompare(r *mcpreg.Registry, svc *compare.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "compare_resources", Title: "Compare resources", Feature: "compare",
		Description: "Semantic diff of two objects (e.g. the same Deployment in two clusters or namespaces): summary of meaningful differences (image, replicas, env, resources) and field changes, server defaults ignored. Secret values are never shown." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in compareArgs) (any, error) {
		g, v := groupVersion(in.Kind, in.APIVersion)
		a := compare.Ref{Cluster: call.Cluster, Group: g, Version: v, Kind: in.Kind, Namespace: in.Namespace, Name: in.Name}
		b := a
		if in.OtherCluster != "" {
			b.Cluster = in.OtherCluster
		}
		if in.OtherNs != "" {
			b.Namespace = in.OtherNs
		}
		if in.OtherName != "" {
			b.Name = in.OtherName
		}
		if a == b {
			return nil, errors.New("give otherCluster, otherNamespace or otherName to compare against")
		}
		res, err := svc.Compare(ctx, a, b, compare.Options{IncludeStatus: in.IncludeStatus})
		if err != nil {
			return explain(err, in.Kind, in.Namespace)
		}
		return respond(redactCompare(in.Kind, res), listCap, nil), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "get_drift", Title: "Get drift", Feature: "compare",
		Description: "How the live object drifted from what was declared (kubectl last-applied, or the Helm release manifest): changed and added fields." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in driftArgs) (any, error) {
		g, v := groupVersion(in.Kind, in.APIVersion)
		src := compare.SourceLastApplied
		if in.Source == string(compare.SourceHelm) {
			src = compare.SourceHelm
		}
		res, err := svc.Drift(ctx, compare.Ref{Cluster: call.Cluster, Group: g, Version: v, Kind: in.Kind, Namespace: in.Namespace, Name: in.Name}, src, compare.Options{})
		if err != nil {
			return explain(err, in.Kind, in.Namespace)
		}
		return respond(redactCompare(in.Kind, res), listCap, nil), nil
	})
}

// redactCompare hides values of Secret fields (data/stringData), which a
// generic scrubber cannot recognise as secret.
func redactCompare(kind string, res *compare.Result) *compare.Result {
	if res == nil || kind != "Secret" {
		return res
	}
	cp := *res
	cp.Changes = make([]compare.FieldChange, len(res.Changes))
	for i, c := range res.Changes {
		if strings.HasPrefix(c.Path, "data") || strings.HasPrefix(c.Path, "stringData") {
			if c.A != "" {
				c.A = "<redacted>"
			}
			if c.B != "" {
				c.B = "<redacted>"
			}
		}
		cp.Changes[i] = c
	}
	return &cp
}

// ---------------------------------------------------------------------------
// images

type imageArgs struct {
	mcpreg.ClusterArg
	Image     string `json:"image" jsonschema:"image reference, e.g. nginx:1.27 or repo@sha256:..."`
	Namespace string `json:"namespace,omitempty" jsonschema:"with pod: use the pod's pull secrets and node platform"`
	Pod       string `json:"pod,omitempty"`
	Container string `json:"container,omitempty"`
	Platform  string `json:"platform,omitempty" jsonschema:"os/arch, e.g. linux/arm64"`
}

func registerImages(r *mcpreg.Registry, svc *images.Service) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "inspect_image", Title: "Inspect image", Feature: "images",
		Description: "Registry metadata of a container image: digest, platforms, size, layers with their creating commands, config (entrypoint, env names, user, exposed ports, labels). Does not read files inside the image." + reasonsDoc,
	}, func(ctx context.Context, call *mcpreg.Call, in imageArgs) (any, error) {
		info, err := svc.ImageInfo(ctx, in.Image, images.PullContext{Cluster: call.Cluster, Namespace: in.Namespace, Pod: in.Pod, Container: in.Container, Platform: in.Platform})
		if err != nil {
			return explain(err, "", in.Namespace)
		}
		return respond(info, 60, nil), nil
	})
}

// ---------------------------------------------------------------------------
// integrations (served by REST handlers; called in-process)

type cnpgArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty" jsonschema:"with name: one cluster's status"`
	Name      string `json:"name,omitempty"`
}

type fluxArgs struct {
	mcpreg.ClusterArg
	Kind      string `json:"kind" jsonschema:"Flux kind, e.g. Kustomization, HelmRelease, GitRepository"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type noArgs struct {
	mcpreg.ClusterArg
}

func registerIntegrations(r *mcpreg.Registry, router http.Handler) {
	call := func(ctx context.Context, method, path, cluster string) (map[string]any, error) {
		return mcp.CallRouter(ctx, router, method, path, url.Values{"cluster": {cluster}}, nil)
	}

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "gitops_status", Title: "GitOps status", Feature: "gitops",
		Description: "Argo CD Applications and Flux objects (Kustomizations, HelmReleases, sources) in one list with sync and health status, revision and last error." + reasonsDoc,
	}, func(ctx context.Context, c *mcpreg.Call, _ noArgs) (any, error) {
		out, err := call(ctx, http.MethodGet, "/cluster/gitops/status", c.Cluster)
		if err != nil {
			return explain(err, "", "")
		}
		return respond(out, listCap, nil), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "cnpg_cluster_status", Title: "CloudNativePG status", Feature: "gitops",
		Description: "CloudNativePG PostgreSQL clusters: instances, primary, replication and backup status. Give namespace+name for one cluster's detail." + reasonsDoc,
	}, func(ctx context.Context, c *mcpreg.Call, in cnpgArgs) (any, error) {
		path := "/cluster/cnpg/clusters"
		if in.Name != "" {
			path += "/" + url.PathEscape(in.Namespace) + "/" + url.PathEscape(in.Name)
		}
		out, err := call(ctx, http.MethodGet, path, c.Cluster)
		if err != nil {
			return explain(err, "Cluster", in.Namespace)
		}
		return respond(out, listCap, nil), nil
	})

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name: "flux_reconcile", Title: "Flux reconcile", Feature: "gitops", Write: true, Idempotent: true,
		Description: "Asks Flux to reconcile one object now (sets the reconcile.fluxcd.io/requestedAt annotation).",
	}, func(ctx context.Context, c *mcpreg.Call, in fluxArgs) (any, error) {
		path := fmt.Sprintf("/cluster/gitops/flux/%s/%s/%s/reconcile", url.PathEscape(in.Kind), url.PathEscape(in.Namespace), url.PathEscape(in.Name))
		out, err := call(ctx, http.MethodPost, path, c.Cluster)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]any{"ok": true}
		}
		return out, nil
	})
}

// ---------------------------------------------------------------------------
// diagnose enrichment

func registerDiagnose(d Deps) {
	if d.Health != nil {
		svc := d.Health
		mcp.RegisterDashboardSection("issues", func(ctx context.Context, cluster, ns string) (any, error) {
			f := health.IssueFilter{Cluster: cluster}
			if ns != "" {
				f.Namespaces = []string{ns}
			}
			rep, err := svc.Issues(ctx, f)
			if err != nil {
				return nil, err
			}
			top := rep.Issues
			if len(top) > 5 {
				top = top[:5]
			}
			titles := make([]string, 0, len(top))
			for _, is := range top {
				titles = append(titles, string(is.Severity)+": "+is.Title+" ("+is.Resource.Kind+" "+is.Resource.Namespace+"/"+is.Resource.Name+")")
			}
			return map[string]any{"summary": rep.Summary, "top": titles}, nil
		})
		mcp.RegisterDashboardSection("audit", func(ctx context.Context, cluster, ns string) (any, error) {
			sc := health.AuditScope{Cluster: cluster}
			if ns != "" {
				sc.Namespaces = []string{ns}
			}
			rep, err := svc.Audit(ctx, sc)
			if err != nil {
				return nil, err
			}
			return map[string]any{"score": rep.Score, "resources": rep.Resources}, nil
		})
		mcp.RegisterDiagnoseEnricher("issues", func(ctx context.Context, cluster string, o mcp.Ref) (any, error) {
			rep, err := svc.Issues(ctx, health.IssueFilter{Cluster: cluster, Resource: &health.ResourceRef{APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name}})
			if err != nil || len(rep.Issues) == 0 {
				return nil, err
			}
			out, _ := shape(rep.Issues, 10)
			return out, nil
		})
	}
	if d.Changes != nil {
		svc := d.Changes
		mcp.RegisterDiagnoseEnricher("recentChanges", func(ctx context.Context, cluster string, o mcp.Ref) (any, error) {
			g, v := groupVersion(o.Kind, o.APIVersion)
			h, err := svc.ChangesFor(ctx, changes.ResourceRef{Cluster: cluster, Group: g, Version: v, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name})
			if err != nil || h == nil || len(h.Entries) == 0 {
				return nil, err
			}
			out, _ := shape(h.Entries, 5)
			return out, nil
		})
	}
}
