package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/kanivet/backend/internal/argo"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/rightsizing"
)

func registerExtraTools(r *mcpreg.Registry, d *Deps) {
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "list_helm_releases", Feature: "helm", Title: "List Helm releases",
		Description: "Helm releases (name, namespace, chart, version, status, revision, updated). Failed or pending releases sort first."}, d.listHelm)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "get_helm_release", Feature: "helm", Title: "Get Helm release",
		Description: "One Helm release: status, notes, revision history, and optionally user values (include=values) or the rendered manifest (include=manifest). Secrets are redacted."}, d.getHelm)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "list_argo_apps", Feature: "argo", Title: "List Argo CD applications",
		Description: "Argo CD Applications with sync and health status, source repo/path/revision and destination. Out-of-sync or unhealthy apps sort first."}, d.listArgo)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "get_argo_app", Feature: "argo", Title: "Get Argo CD application",
		Description: "One Argo CD Application: minified spec/status including per-resource sync/health, conditions and the last operation."}, d.getArgo)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "get_metrics", Feature: "metrics", Title: "Get usage metrics",
		Description: "Current CPU/memory usage from metrics-server. target=pods (optionally one namespace) or nodes (with requests/allocatable load). Sorted by usage, top N."}, d.getMetrics)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "get_costs", Feature: "finops", Title: "Get costs",
		Description: "FinOps cost data. view=summary (default), namespaces, workloads (optionally namespace), nodes or recommendations."}, d.getCosts)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "get_rightsizing", Feature: "finops", Title: "Get rightsizing",
		Description: "Rightsizing recommendations from usage history (requests/limits vs observed), with estimated savings. May be partial while history loads."}, d.getRightsizing)
}

// ---------------------------------------------------------------------------

type helmListArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty"`
}

func (d *Deps) listHelm(ctx context.Context, call *mcpreg.Call, in helmListArgs) (any, error) {
	if d.Helm == nil {
		return nil, errors.New("helm not available")
	}
	rels, err := d.Helm.ListReleases(ctx, call.Cluster, in.Namespace, in.Namespace == "")
	if err != nil {
		return nil, err
	}
	type row struct {
		Name     string `json:"name"`
		NS       string `json:"ns"`
		Chart    string `json:"chart"`
		Version  string `json:"version,omitempty"`
		App      string `json:"app,omitempty"`
		Status   string `json:"status"`
		Revision int    `json:"rev"`
		Updated  string `json:"updated"`
	}
	out := make([]row, 0, len(rels))
	for _, r := range rels {
		out = append(out, row{r.Name, r.Namespace, r.Chart, r.ChartVersion, r.AppVersion, r.Status, r.Revision, r.Updated.UTC().Format("2006-01-02T15:04Z")})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return (out[i].Status != "deployed") && (out[j].Status == "deployed")
	})
	return map[string]any{"releases": out}, nil
}

type helmGetArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Include   string `json:"include,omitempty" jsonschema:"comma list: values, allValues, manifest"`
}

func (d *Deps) getHelm(ctx context.Context, call *mcpreg.Call, in helmGetArgs) (any, error) {
	if d.Helm == nil {
		return nil, errors.New("helm not available")
	}
	rel, err := d.Helm.GetReleaseBasic(ctx, call.Cluster, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"name": rel.Name, "namespace": rel.Namespace, "chart": rel.Chart, "chartVersion": rel.ChartVersion,
		"appVersion": rel.AppVersion, "status": rel.Status, "revision": rel.Revision, "updated": rel.Updated,
		"description": rel.Description,
	}
	if rel.Notes != "" {
		out["notes"] = truncateText(ScrubText(rel.Notes), 1500)
	}
	if h, err := d.Helm.GetReleaseHistory(ctx, call.Cluster, in.Namespace, in.Name, 10); err == nil {
		out["history"] = h
	}
	inc := strings.Split(in.Include, ",")
	for _, i := range inc {
		switch strings.TrimSpace(i) {
		case "values", "allValues":
			v, err := d.Helm.GetReleaseValues(ctx, call.Cluster, in.Namespace, in.Name, strings.TrimSpace(i) == "allValues")
			if err == nil {
				out["values"] = Redact(map[string]any{"v": v})["v"]
			}
		case "manifest":
			m, err := d.Helm.GetReleaseManifest(ctx, call.Cluster, in.Namespace, in.Name)
			if err == nil {
				out["manifest"] = RedactManifest(m)
			}
		}
	}
	return out, nil
}

// RedactManifest redacts every document of a multi-document YAML stream.
func RedactManifest(m string) string {
	docs := strings.Split(m, "\n---")
	var b strings.Builder
	for i, doc := range docs {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			continue
		}
		clean, err := yaml.Marshal(Redact(Minify(obj)))
		if err != nil {
			continue
		}
		if i > 0 {
			b.WriteString("---\n")
		}
		b.Write(clean)
	}
	return b.String()
}

// ---------------------------------------------------------------------------

func (d *Deps) listArgo(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	apps, err := argo.ListApplicationsSummary(ctx, dyn)
	if err != nil {
		return nil, fmt.Errorf("argo cd not found or not readable: %w", err)
	}
	sort.SliceStable(apps, func(i, j int) bool {
		bad := func(a argo.AppListEntry) bool { return a.SyncStatus != "Synced" || a.Health != "Healthy" }
		return bad(apps[i]) && !bad(apps[j])
	})
	return map[string]any{"stats": argo.ComputeStats(apps), "apps": apps}, nil
}

type argoGetArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace" jsonschema:"namespace of the Application object (often argocd)"`
	Name      string `json:"name"`
}

func (d *Deps) getArgo(ctx context.Context, call *mcpreg.Call, in argoGetArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	u, err := argo.GetApplication(ctx, dyn, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	obj := Redact(Minify(u.Object))
	if st, ok := obj["status"].(map[string]any); ok {
		delete(st, "history")
		if h, ok := u.Object["status"].(map[string]any)["history"].([]any); ok && len(h) > 0 {
			st["lastHistory"] = h[len(h)-1]
		}
	}
	b, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// ---------------------------------------------------------------------------

type metricsArgs struct {
	mcpreg.ClusterArg
	Target    string `json:"target,omitempty" jsonschema:"pods (default) or nodes"`
	Namespace string `json:"namespace,omitempty"`
	SortBy    string `json:"sortBy,omitempty" jsonschema:"cpu (default) or memory"`
	Top       int    `json:"top,omitempty" jsonschema:"default 25"`
}

func (d *Deps) getMetrics(ctx context.Context, call *mcpreg.Call, in metricsArgs) (any, error) {
	if d.Metrics == nil {
		return nil, errors.New("metrics not available")
	}
	top := in.Top
	if top <= 0 {
		top = 25
	}
	type usage struct {
		Name      string `json:"name"`
		NS        string `json:"ns,omitempty"`
		CPUm      int64  `json:"cpuMillicores"`
		MemMi     int64  `json:"memoryMi"`
		Container string `json:"containers,omitempty"`
	}
	var rows []usage
	if in.Target == "nodes" {
		list, err := d.Metrics.ListNodeMetrics(ctx, call.Cluster)
		if err != nil {
			return nil, fmt.Errorf("metrics-server unavailable: %w", err)
		}
		for _, it := range list.Items {
			u, _ := it.Object["usage"].(map[string]any)
			rows = append(rows, usage{Name: it.GetName(), CPUm: cpuMilli(fmt.Sprint(u["cpu"])), MemMi: memMi(fmt.Sprint(u["memory"]))})
		}
	} else {
		list, err := d.Metrics.ListPodMetrics(ctx, call.Cluster, in.Namespace)
		if err != nil {
			return nil, fmt.Errorf("metrics-server unavailable: %w", err)
		}
		for _, it := range list.Items {
			r := usage{Name: it.GetName(), NS: it.GetNamespace()}
			cs, _ := it.Object["containers"].([]any)
			var names []string
			for _, c := range cs {
				cm, _ := c.(map[string]any)
				u, _ := cm["usage"].(map[string]any)
				r.CPUm += cpuMilli(fmt.Sprint(u["cpu"]))
				r.MemMi += memMi(fmt.Sprint(u["memory"]))
				names = append(names, str(cm, "name"))
			}
			if len(names) > 1 {
				r.Container = strings.Join(names, ",")
			}
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if in.SortBy == "memory" {
			return rows[i].MemMi > rows[j].MemMi
		}
		return rows[i].CPUm > rows[j].CPUm
	})
	total := len(rows)
	if len(rows) > top {
		rows = rows[:top]
	}
	out := map[string]any{"target": orDefault(in.Target, "pods"), "total": total, "items": rows}
	if in.Target == "nodes" && d.NodeLoad != nil {
		if rep, err := d.NodeLoad.Load(ctx, call.Cluster); err == nil {
			out["load"] = rep.Nodes
		}
	}
	return out, nil
}

func cpuMilli(s string) int64 {
	var v float64
	switch {
	case strings.HasSuffix(s, "n"):
		fmt.Sscanf(strings.TrimSuffix(s, "n"), "%g", &v)
		return int64(v / 1e6)
	case strings.HasSuffix(s, "u"):
		fmt.Sscanf(strings.TrimSuffix(s, "u"), "%g", &v)
		return int64(v / 1e3)
	case strings.HasSuffix(s, "m"):
		fmt.Sscanf(strings.TrimSuffix(s, "m"), "%g", &v)
		return int64(v)
	}
	fmt.Sscanf(s, "%g", &v)
	return int64(v * 1000)
}

func memMi(s string) int64 {
	units := map[string]float64{"Ki": 1.0 / 1024, "Mi": 1, "Gi": 1024, "Ti": 1024 * 1024, "k": 1000.0 / 1048576, "M": 1e6 / 1048576, "G": 1e9 / 1048576}
	for suf, mul := range units {
		if strings.HasSuffix(s, suf) {
			var v float64
			fmt.Sscanf(strings.TrimSuffix(s, suf), "%g", &v)
			return int64(v * mul)
		}
	}
	var v float64
	fmt.Sscanf(s, "%g", &v)
	return int64(v / 1048576)
}

// ---------------------------------------------------------------------------

type costArgs struct {
	mcpreg.ClusterArg
	View      string `json:"view,omitempty" jsonschema:"summary (default), namespaces, workloads, nodes, recommendations"`
	Namespace string `json:"namespace,omitempty"`
	Top       int    `json:"top,omitempty" jsonschema:"rows to return (default 30)"`
}

func (d *Deps) getCosts(ctx context.Context, call *mcpreg.Call, in costArgs) (any, error) {
	if d.FinOps == nil {
		return nil, errors.New("finops not available")
	}
	top := in.Top
	if top <= 0 {
		top = 30
	}
	var v any
	var err error
	switch in.View {
	case "", "summary":
		v, err = d.FinOps.GetClusterCostSummary(ctx, call.Cluster)
	case "namespaces":
		v, err = d.FinOps.GetNamespaceCosts(ctx, call.Cluster)
	case "workloads":
		v, err = d.FinOps.GetWorkloadCosts(ctx, call.Cluster, in.Namespace)
	case "nodes":
		v, err = d.FinOps.GetNodeCosts(ctx, call.Cluster)
	case "recommendations":
		v, err = d.FinOps.GetCostRecommendations(ctx, call.Cluster)
	default:
		return nil, fmt.Errorf("unknown view %q", in.View)
	}
	if err != nil {
		return nil, err
	}
	return compactValue(v, top), nil
}

type rightsizingArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty"`
	Profile   string `json:"profile,omitempty" jsonschema:"conservative, balanced (default), aggressive"`
	Top       int    `json:"top,omitempty" jsonschema:"default 25"`
}

func (d *Deps) getRightsizing(ctx context.Context, call *mcpreg.Call, in rightsizingArgs) (any, error) {
	if d.Rightsizing == nil {
		return nil, errors.New("rightsizing not available")
	}
	rep := d.Rightsizing.GetReport(call.Cluster, rightsizing.ParseProfile(in.Profile), rightsizing.ParseWindow(""), false, "", "")
	top := in.Top
	if top <= 0 {
		top = 25
	}
	v := compactValue(rep, top)
	if in.Namespace != "" {
		v = filterNamespace(v, in.Namespace)
	}
	return v, nil
}

// compactValue round-trips through JSON, trims every array to n items (noting
// how many were dropped) and prunes empty fields.
func compactValue(v any, n int) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var g any
	if json.Unmarshal(b, &g) != nil {
		return v
	}
	return prune(trimArrays(g, n))
}

func trimArrays(v any, n int) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if arr, ok := val.([]any); ok && len(arr) > n {
				t[k] = trimArrays(arr[:n], n)
				t[k+"Omitted"] = len(arr) - n
				continue
			}
			t[k] = trimArrays(val, n)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = trimArrays(val, n)
		}
		if len(t) > n {
			return t[:n]
		}
		return t
	}
	return v
}

func filterNamespace(v any, ns string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = filterNamespace(val, ns)
		}
		return t
	case []any:
		out := t[:0]
		for _, val := range t {
			if m, ok := val.(map[string]any); ok {
				if n, ok := m["namespace"].(string); ok && n != ns {
					continue
				}
			}
			out = append(out, filterNamespace(val, ns))
		}
		return out
	}
	return v
}

// ---------------------------------------------------------------------------

// callAPI invokes one of the backend's own REST handlers in-process.
func (d *Deps) callAPI(ctx context.Context, method, path string, query url.Values, body any) (map[string]any, error) {
	if d.Router == nil {
		return nil, errors.New("backend router not available")
	}
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	u := "/api/v1" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req := httptest.NewRequest(method, u, rd).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	if s := os.Getenv("KANIVET_SESSION_SECRET"); s != "" {
		req.Header.Set("X-Session-Secret", s)
	}
	rec := httptest.NewRecorder()
	d.Router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code >= http.StatusBadRequest {
		msg := strings.TrimSpace(rec.Body.String())
		if e, ok := out["error"]; ok {
			msg = fmt.Sprint(e)
		}
		return nil, fmt.Errorf("%s %s: %d %s", method, path, rec.Code, truncateText(msg, 300))
	}
	return out, nil
}
