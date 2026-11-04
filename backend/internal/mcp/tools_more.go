package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/kanivet/backend/internal/crossplane"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/nodeload"
)

func registerMoreTools(r *mcpreg.Registry, d *Deps) {
	registerRead(r, mcpreg.Spec{Name: "get_workload_logs", Title: "Get workload logs",
		Description: "Logs of every pod of a Deployment/StatefulSet/DaemonSet/ReplicaSet/Job merged into one time-ordered stream, each line prefixed pod/container. Filters: grep (regex), since (Go duration), tail (lines per container), maxPods, maxLinesPerPod; errors are kept first when lines must be dropped; secrets scrubbed."}, d.workloadLogs)

	registerRead(r, mcpreg.Spec{Name: "query_prometheus", Feature: "metrics", Title: "Query Prometheus",
		Description: "Run PromQL against the cluster's Prometheus (or Thanos/VictoriaMetrics/Mimir, as Kanivet discovered it). Instant by default; give range (Go duration, e.g. 1h) and step for a range query. Bounded: at most maxSeries series and 120 points each."}, d.queryPrometheus)
	registerRead(r, mcpreg.Spec{Name: "discover_metrics", Feature: "metrics", Title: "Discover metrics",
		Description: "Metric names in the cluster's Prometheus with type and help text, filtered by a substring/regex; with metric set, its label names and sample label values. Use before query_prometheus."}, d.discoverMetrics)
	registerRead(r, mcpreg.Spec{Name: "get_prometheus_rules", Feature: "metrics", Title: "Get Prometheus rules",
		Description: "Alerting and recording rules with their state (firing/pending/inactive), active alert labels, health and last error. Filter by type, state or name substring."}, d.prometheusRules)

	registerRead(r, mcpreg.Spec{Name: "get_dashboard", Title: "Cluster dashboard",
		Description: "One-call overview of a cluster or namespace: object counts, failing pods, unhealthy workloads, recent warning events and, when available, health issue counts and the audit score."}, d.dashboard)
	registerRead(r, mcpreg.Spec{Name: "top_resources", Feature: "metrics", Title: "Top resources",
		Description: "Pods or nodes ranked by current CPU or memory (metrics-server), joined with the owning workload, requests and limits (pods) or allocatable and pod counts (nodes)."}, d.topResources)

	registerRead(r, mcpreg.Spec{Name: "list_namespaces", Title: "List namespaces",
		Description: "Namespaces with phase, age and labels."}, d.listNamespaces)
	registerRead(r, mcpreg.Spec{Name: "list_packages", Title: "List packages",
		Description: "What is installed on the cluster: Helm releases, Argo CD Applications, Flux HelmReleases and Kustomizations, and CRDs grouped by API group."}, d.listPackages)

	registerRead(r, mcpreg.Spec{Name: "crossplane_trace", Feature: "crossplane", Title: "Crossplane trace",
		Description: "Tree of a Crossplane claim or composite resource down to its managed resources, each with Ready/Synced and the condition message. Give apiVersion and kind of the root."}, d.crossplaneTrace)
	registerRead(r, mcpreg.Spec{Name: "list_vclusters", Feature: "vcluster", Title: "List vclusters",
		Description: "Virtual clusters (vcluster) running on this host cluster with namespace, status and version."}, d.listVClusters)
	registerRead(r, mcpreg.Spec{Name: "get_node_load", Title: "Get node load",
		Description: "Per node: pod count, CPU/memory requests and limits against allocatable, and current usage when metrics-server is present."}, d.nodeLoad)

	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "patch_resource", Destructive: true, Feature: "actions", Title: "Patch resource",
		Description: "Patch one object with a JSON patch, merge patch or strategic merge patch (built-in kinds). Use dryRun=true first and show the user the result."}, d.patchResource)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "manage_cronjob", Write: true, Feature: "actions", Title: "Manage CronJob",
		Description: "Trigger a CronJob now (creates a Job), or suspend/resume its schedule."}, d.manageCronJob)
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: "manage_argo_rollout", Destructive: true, Feature: "argo", Title: "Manage Argo Rollout",
		Description: "Argo Rollouts actions on a Rollout: promote (next step; full=true skips all steps), abort, retry, pause, resume. Only when the Argo Rollouts CRD is installed."}, d.manageRollout)
}

// ---------------------------------------------------------------------------
// get_workload_logs

type workloadLogsArgs struct {
	mcpreg.ClusterArg
	Kind           string `json:"kind" jsonschema:"Deployment, StatefulSet, DaemonSet, ReplicaSet or Job"`
	Namespace      string `json:"namespace"`
	Name           string `json:"name"`
	Container      string `json:"container,omitempty" jsonschema:"only this container"`
	Grep           string `json:"grep,omitempty" jsonschema:"case-insensitive regex"`
	Since          string `json:"since,omitempty" jsonschema:"Go duration, e.g. 15m"`
	Tail           int    `json:"tail,omitempty" jsonschema:"lines read per container (default 200, max 2000)"`
	MaxPods        int    `json:"maxPods,omitempty" jsonschema:"pods read (default 10, max 50); unhealthiest first"`
	MaxLinesPerPod int    `json:"maxLinesPerPod,omitempty" jsonschema:"lines kept per pod (default 100)"`
	MaxLines       int    `json:"maxLines,omitempty" jsonschema:"total lines returned (default 400)"`
}

type logLine struct {
	t    time.Time
	src  string
	text string
	err  bool
}

func (d *Deps) workloadLogs(ctx context.Context, call *mcpreg.Call, in workloadLogsArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	switch in.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
	default:
		return nil, fmt.Errorf("kind must be Deployment, StatefulSet, DaemonSet, ReplicaSet or Job")
	}
	tail := clamp(in.Tail, 200, 2000)
	maxPods := clamp(in.MaxPods, 10, 50)
	perPod := clamp(in.MaxLinesPerPod, 100, 2000)
	maxLines := clamp(in.MaxLines, 400, 3000)
	var grep *regexp.Regexp
	if in.Grep != "" {
		var err error
		if grep, err = regexp.Compile("(?i)" + in.Grep); err != nil {
			return nil, fmt.Errorf("invalid grep regex: %w", err)
		}
	}
	var sinceSec *int64
	if in.Since != "" {
		dur, err := time.ParseDuration(in.Since)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		s := int64(dur / time.Second)
		sinceSec = &s
	}
	pods, err := d.podsFor(ctx, call.Cluster, in.Namespace, in.Kind, in.Name)
	if err != nil {
		return nil, err
	}
	var reasons []Reason
	if len(pods) == 0 {
		reasons = append(reasons, Reason{Code: ReasonObjectNotFound, Kind: "Pod", Namespace: in.Namespace, Detail: "the workload has no pods"})
	}
	sort.SliceStable(pods, func(i, j int) bool { return podTrouble(&pods[i]) > podTrouble(&pods[j]) })
	if len(pods) > maxPods {
		reasons = append(reasons, Truncated("Pod", maxPods, len(pods)))
		pods = pods[:maxPods]
	}
	cs, err := d.K8s.GetClientForCluster(call.Cluster)
	if err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var all []logLine
	var errs []string
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, p := range pods {
		p := p
		var containers []string
		for _, c := range p.Spec.Containers {
			if in.Container == "" || c.Name == in.Container {
				containers = append(containers, c.Name)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var podLines []logLine
			for _, c := range containers {
				t := int64(tail)
				opts := &corev1.PodLogOptions{Container: c, Timestamps: true, TailLines: &t, SinceSeconds: sinceSec}
				raw, err := cs.CoreV1().Pods(p.Namespace).GetLogs(p.Name, opts).DoRaw(ctx)
				if err != nil {
					mu.Lock()
					errs = append(errs, p.Name+"/"+c+": "+truncateText(err.Error(), 160))
					mu.Unlock()
					continue
				}
				src := p.Name + "/" + c
				if len(containers) == 1 {
					src = p.Name
				}
				podLines = append(podLines, parseTimestamped(raw, src, grep)...)
			}
			podLines = keepPrioritised(podLines, perPod)
			mu.Lock()
			all = append(all, podLines...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	total := len(all)
	all = keepPrioritised(all, maxLines)
	sort.SliceStable(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	lines := make([]string, 0, len(all))
	var prev string
	repeat := 0
	for _, l := range all {
		s := l.src + " | " + l.text
		if l.text == prev {
			repeat++
			continue
		}
		if repeat > 0 {
			lines = append(lines, fmt.Sprintf("  (previous line repeated %d more times)", repeat))
			repeat = 0
		}
		prev = l.text
		lines = append(lines, s)
	}
	if repeat > 0 {
		lines = append(lines, fmt.Sprintf("  (previous line repeated %d more times)", repeat))
	}
	if total > len(all) {
		reasons = append(reasons, Reason{Code: ReasonTruncated, Kind: "Pod", Detail: fmt.Sprintf("kept %d of %d matching lines (errors first); narrow with grep or since", len(all), total)})
	}
	for _, e := range errs {
		reasons = append(reasons, ReasonFor(errors.New(e), "Pod", in.Namespace))
	}
	out := map[string]any{"pods": len(pods), "lines": lines}
	if len(reasons) > 0 {
		out["reasons"] = reasons
	}
	return out, nil
}

func parseTimestamped(raw []byte, src string, grep *regexp.Regexp) []logLine {
	var out []logLine
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		ts, text, ok := strings.Cut(line, " ")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil {
			text = line
		}
		if grep != nil && !grep.MatchString(text) {
			continue
		}
		text = truncateText(ScrubText(text), 500)
		out = append(out, logLine{t: t, src: src, text: text, err: errorLineRe.MatchString(text)})
	}
	return out
}

// keepPrioritised keeps at most n lines, preferring error-looking lines and
// then the most recent ones.
func keepPrioritised(ls []logLine, n int) []logLine {
	if len(ls) <= n {
		return ls
	}
	idx := make([]int, len(ls))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		la, lb := ls[idx[a]], ls[idx[b]]
		if la.err != lb.err {
			return la.err
		}
		return la.t.After(lb.t)
	})
	keep := make([]logLine, 0, n)
	for _, i := range idx[:n] {
		keep = append(keep, ls[i])
	}
	return keep
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// ---------------------------------------------------------------------------
// Prometheus

type promQueryArgs struct {
	mcpreg.ClusterArg
	Query     string `json:"query" jsonschema:"PromQL"`
	Range     string `json:"range,omitempty" jsonschema:"Go duration for a range query ending now, e.g. 1h"`
	Step      string `json:"step,omitempty" jsonschema:"range step, default range/60"`
	MaxSeries int    `json:"maxSeries,omitempty" jsonschema:"default 20, max 100"`
}

func (d *Deps) queryPrometheus(ctx context.Context, call *mcpreg.Call, in promQueryArgs) (any, error) {
	if d.Metrics == nil {
		return nil, errors.New("metrics not available")
	}
	maxSeries := clamp(in.MaxSeries, 20, 100)
	type series struct {
		Labels map[string]string `json:"labels"`
		Value  *float32          `json:"value,omitempty"`
		Points [][2]any          `json:"points,omitempty"`
	}
	now := time.Now()
	var out []series
	var total int
	if in.Range == "" {
		res, err := d.Metrics.QueryInstant(ctx, call.Cluster, in.Query, now)
		if err != nil {
			return nil, err
		}
		total = len(res)
		for i, s := range res {
			if i >= maxSeries {
				break
			}
			sr := series{Labels: s.Labels}
			if n := len(s.Values); n > 0 {
				v := s.Values[n-1]
				sr.Value = &v
			}
			out = append(out, sr)
		}
	} else {
		rng, err := time.ParseDuration(in.Range)
		if err != nil {
			return nil, fmt.Errorf("range: %w", err)
		}
		step := rng / 60
		if in.Step != "" {
			if step, err = time.ParseDuration(in.Step); err != nil {
				return nil, fmt.Errorf("step: %w", err)
			}
		}
		if step < time.Second {
			step = time.Second
		}
		if rng/step > 120 {
			step = rng / 120
		}
		res, err := d.Metrics.QueryRange(ctx, call.Cluster, in.Query, now.Add(-rng), now, step)
		if err != nil {
			return nil, err
		}
		total = len(res)
		for i, s := range res {
			if i >= maxSeries {
				break
			}
			sr := series{Labels: s.Labels}
			for j := range s.Values {
				if j < len(s.Times) {
					sr.Points = append(sr.Points, [2]any{time.Unix(s.Times[j], 0).UTC().Format("15:04:05"), s.Values[j]})
				}
			}
			out = append(out, sr)
		}
	}
	resp := map[string]any{"series": out, "total": total}
	if total > len(out) {
		resp["reasons"] = []Reason{Truncated("series", len(out), total)}
	}
	return resp, nil
}

type discoverArgs struct {
	mcpreg.ClusterArg
	Match  string `json:"match,omitempty" jsonschema:"substring or regex over metric names"`
	Metric string `json:"metric,omitempty" jsonschema:"one metric: return its labels and sample values"`
	Limit  int    `json:"limit,omitempty" jsonschema:"default 100, max 500"`
}

func (d *Deps) discoverMetrics(ctx context.Context, call *mcpreg.Call, in discoverArgs) (any, error) {
	if d.Metrics == nil {
		return nil, errors.New("metrics not available")
	}
	limit := clamp(in.Limit, 100, 500)
	if in.Metric != "" {
		params := url.Values{"match[]": {in.Metric}, "start": {fmt.Sprint(time.Now().Add(-10 * time.Minute).Unix())}}
		b, _, err := d.Metrics.PromGet(ctx, call.Cluster, "/api/v1/series", params, 2<<20)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data []map[string]string `json:"data"`
		}
		_ = json.Unmarshal(b, &resp)
		labels := map[string]map[string]bool{}
		for _, s := range resp.Data {
			for k, v := range s {
				if k == "__name__" {
					continue
				}
				if labels[k] == nil {
					labels[k] = map[string]bool{}
				}
				if len(labels[k]) < 15 {
					labels[k][v] = true
				}
			}
		}
		out := map[string][]string{}
		for k, vs := range labels {
			for v := range vs {
				out[k] = append(out[k], v)
			}
			sort.Strings(out[k])
		}
		meta := d.metricMeta(ctx, call.Cluster, in.Metric)
		return map[string]any{"metric": in.Metric, "series": len(resp.Data), "labels": out, "meta": meta[in.Metric]}, nil
	}
	b, _, err := d.Metrics.PromGet(ctx, call.Cluster, "/api/v1/label/__name__/values", nil, 8<<20)
	if err != nil {
		return nil, err
	}
	var names struct {
		Data []string `json:"data"`
	}
	_ = json.Unmarshal(b, &names)
	var re *regexp.Regexp
	if in.Match != "" {
		if re, err = regexp.Compile("(?i)" + in.Match); err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(in.Match))
		}
	}
	meta := d.metricMeta(ctx, call.Cluster, "")
	type row struct {
		Name string `json:"name"`
		Type string `json:"type,omitempty"`
		Help string `json:"help,omitempty"`
	}
	var rows []row
	matched := 0
	for _, n := range names.Data {
		if re != nil && !re.MatchString(n) {
			continue
		}
		matched++
		if len(rows) < limit {
			m := meta[n]
			rows = append(rows, row{Name: n, Type: m.Type, Help: truncateText(m.Help, 140)})
		}
	}
	out := map[string]any{"metrics": rows, "matched": matched}
	if matched > len(rows) {
		out["reasons"] = []Reason{Truncated("metric", len(rows), matched)}
	}
	return out, nil
}

type metricMetadata struct {
	Type string `json:"type"`
	Help string `json:"help"`
}

func (d *Deps) metricMeta(ctx context.Context, cluster, metric string) map[string]metricMetadata {
	params := url.Values{}
	if metric != "" {
		params.Set("metric", metric)
	}
	b, _, err := d.Metrics.PromGet(ctx, cluster, "/api/v1/metadata", params, 8<<20)
	out := map[string]metricMetadata{}
	if err != nil {
		return out
	}
	var resp struct {
		Data map[string][]metricMetadata `json:"data"`
	}
	_ = json.Unmarshal(b, &resp)
	for k, v := range resp.Data {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

type rulesArgs struct {
	mcpreg.ClusterArg
	Type  string `json:"type,omitempty" jsonschema:"alert or record"`
	State string `json:"state,omitempty" jsonschema:"firing, pending or inactive (alerts)"`
	Name  string `json:"name,omitempty" jsonschema:"substring of the rule or group name"`
	Limit int    `json:"limit,omitempty" jsonschema:"default 100, max 500"`
}

func (d *Deps) prometheusRules(ctx context.Context, call *mcpreg.Call, in rulesArgs) (any, error) {
	if d.Metrics == nil {
		return nil, errors.New("metrics not available")
	}
	params := url.Values{}
	if in.Type != "" {
		params.Set("type", in.Type)
	}
	b, cut, err := d.Metrics.PromGet(ctx, call.Cluster, "/api/v1/rules", params, 16<<20)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Groups []struct {
				Name  string `json:"name"`
				File  string `json:"file"`
				Rules []struct {
					Name      string            `json:"name"`
					Type      string            `json:"type"`
					State     string            `json:"state"`
					Health    string            `json:"health"`
					LastError string            `json:"lastError"`
					Query     string            `json:"query"`
					Labels    map[string]string `json:"labels"`
					Alerts    []struct {
						Labels map[string]string `json:"labels"`
						State  string            `json:"state"`
					} `json:"alerts"`
				} `json:"rules"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil && !cut {
		return nil, fmt.Errorf("rules: %w", err)
	}
	limit := clamp(in.Limit, 100, 500)
	type row struct {
		Group     string              `json:"group"`
		Name      string              `json:"name"`
		Type      string              `json:"type"`
		State     string              `json:"state,omitempty"`
		Severity  string              `json:"severity,omitempty"`
		Health    string              `json:"health,omitempty"`
		LastError string              `json:"lastError,omitempty"`
		Query     string              `json:"query"`
		Active    []map[string]string `json:"active,omitempty"`
	}
	var rows []row
	matched := 0
	q := strings.ToLower(in.Name)
	for _, g := range resp.Data.Groups {
		for _, rl := range g.Rules {
			typ := rl.Type
			if typ == "alerting" {
				typ = "alert"
			} else if typ == "recording" {
				typ = "record"
			}
			if in.State != "" && rl.State != in.State {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(rl.Name+" "+g.Name), q) {
				continue
			}
			matched++
			if len(rows) >= limit {
				continue
			}
			rw := row{Group: g.Name, Name: rl.Name, Type: typ, State: rl.State, Severity: rl.Labels["severity"], Health: rl.Health, LastError: rl.LastError, Query: truncateText(rl.Query, 300)}
			for i, a := range rl.Alerts {
				if i >= 5 {
					break
				}
				rw.Active = append(rw.Active, a.Labels)
			}
			rows = append(rows, rw)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return stateRank(rows[i].State) < stateRank(rows[j].State) })
	out := map[string]any{"rules": rows, "matched": matched}
	var reasons []Reason
	if matched > len(rows) {
		reasons = append(reasons, Truncated("rule", len(rows), matched))
	}
	if cut {
		reasons = append(reasons, Reason{Code: ReasonTruncated, Detail: "rules response too large; filter by type or name"})
	}
	if len(reasons) > 0 {
		out["reasons"] = reasons
	}
	return out, nil
}

func stateRank(s string) int {
	switch s {
	case "firing":
		return 0
	case "pending":
		return 1
	}
	return 2
}

// ---------------------------------------------------------------------------
// get_dashboard / top_resources

// DashboardSection adds a feature's numbers (issue counts, audit score) to
// get_dashboard. It returns nil when it has nothing to say.
type DashboardSection func(ctx context.Context, cluster, namespace string) (any, error)

var (
	dashMu       sync.RWMutex
	dashSections = map[string]DashboardSection{}
)

// RegisterDashboardSection adds (or replaces) a named get_dashboard section.
func RegisterDashboardSection(name string, fn DashboardSection) {
	dashMu.Lock()
	defer dashMu.Unlock()
	dashSections[name] = fn
}

type dashboardArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace,omitempty" jsonschema:"one namespace; default the whole cluster"`
}

var dashboardKinds = []string{"Node", "Namespace", "Pod", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Service", "Ingress", "PersistentVolumeClaim"}

func (d *Deps) dashboard(ctx context.Context, call *mcpreg.Call, in dashboardArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	now := time.Now()
	counts := map[string]int{}
	var failingPods, unhealthy []Row
	var reasons []Reason
	for _, k := range dashboardKinds {
		ki, err := d.resolveKind(call.Cluster, k)
		if err != nil {
			continue
		}
		if in.Namespace != "" && !ki.Namespaced {
			continue
		}
		items, _, err := d.listObjects(ctx, call.Cluster, ki, in.Namespace, "")
		if err != nil {
			reasons = append(reasons, ReasonFor(err, k, in.Namespace))
			continue
		}
		counts[k] = len(items)
		for _, it := range items {
			if _, ok := it["kind"]; !ok {
				it = shallowWithKind(it, ki.Kind)
			}
			r := Summarize(it, now)
			bad := !healthyStatus(r.Status) || ((r.Status == "Running" || r.Status == "Ready") && r.Ready != "" && !readyComplete(r.Ready))
			if !bad {
				continue
			}
			switch k {
			case "Pod":
				failingPods = append(failingPods, r)
			case "Deployment", "StatefulSet", "DaemonSet", "Job":
				unhealthy = append(unhealthy, r)
			}
		}
	}
	out := map[string]any{"counts": counts}
	if in.Namespace != "" {
		out["namespace"] = in.Namespace
	}
	cut := func(name string, rows []Row, n int) {
		out[name+"Total"] = len(rows)
		if len(rows) > n {
			reasons = append(reasons, Truncated(name, n, len(rows)))
			rows = rows[:n]
		}
		out[name] = rows
	}
	cut("failingPods", failingPods, 20)
	cut("unhealthyWorkloads", unhealthy, 20)
	if evs, err := d.events(ctx, call.Cluster, in.Namespace, "", "", true, time.Hour); err == nil {
		groups := DedupeEvents(evs, now)
		out["warningEventsLastHour"] = len(groups)
		if len(groups) > 10 {
			groups = groups[:10]
		}
		out["recentWarnings"] = groups
	} else {
		reasons = append(reasons, ReasonFor(err, "Event", in.Namespace))
	}
	dashMu.RLock()
	for name, fn := range dashSections {
		v, err := fn(ctx, call.Cluster, in.Namespace)
		if err != nil {
			reasons = append(reasons, ReasonFor(err, "", in.Namespace))
			continue
		}
		if v != nil {
			out[name] = v
		}
	}
	dashMu.RUnlock()
	if len(reasons) > 0 {
		out["reasons"] = reasons
	}
	return out, nil
}

type topArgs struct {
	mcpreg.ClusterArg
	Target    string `json:"target,omitempty" jsonschema:"pods (default) or nodes"`
	SortBy    string `json:"sortBy,omitempty" jsonschema:"cpu (default) or memory"`
	Namespace string `json:"namespace,omitempty" jsonschema:"pods only"`
	Limit     int    `json:"limit,omitempty" jsonschema:"default 20, max 200"`
}

func (d *Deps) topResources(ctx context.Context, call *mcpreg.Call, in topArgs) (any, error) {
	if d.Metrics == nil || d.K8s == nil {
		return nil, errors.New("metrics not available")
	}
	limit := clamp(in.Limit, 20, 200)
	byMem := in.SortBy == "memory"
	if in.Target == "nodes" {
		list, err := d.Metrics.ListNodeMetrics(ctx, call.Cluster)
		if err != nil {
			return nil, fmt.Errorf("metrics-server unavailable: %w", err)
		}
		type row struct {
			Name      string `json:"name"`
			CPUm      int64  `json:"cpuMillicores"`
			MemMi     int64  `json:"memoryMi"`
			CPUPct    int    `json:"cpuPctOfAllocatable,omitempty"`
			MemPct    int    `json:"memPctOfAllocatable,omitempty"`
			Pods      int    `json:"pods,omitempty"`
			ReqCPUPct int    `json:"cpuRequestedPct,omitempty"`
			ReqMemPct int    `json:"memRequestedPct,omitempty"`
		}
		var load map[string]*nodeload.Load
		if d.NodeLoad != nil {
			if rep, err := d.NodeLoad.Load(ctx, call.Cluster); err == nil {
				load = rep.Nodes
			}
		}
		var rows []row
		cs, _ := d.K8s.GetClientForCluster(call.Cluster)
		alloc := map[string]corev1.ResourceList{}
		if cs != nil {
			if nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
				for _, n := range nodes.Items {
					alloc[n.Name] = n.Status.Allocatable
				}
			}
		}
		for _, it := range list.Items {
			u, _ := it.Object["usage"].(map[string]any)
			r := row{Name: it.GetName(), CPUm: cpuMilli(fmt.Sprint(u["cpu"])), MemMi: memMi(fmt.Sprint(u["memory"]))}
			a, hasAlloc := alloc[r.Name]
			allocCPU, allocMem := a.Cpu().MilliValue(), a.Memory().Value()
			if hasAlloc && allocCPU > 0 {
				r.CPUPct = int(r.CPUm * 100 / allocCPU)
			}
			if hasAlloc && allocMem > 0 {
				r.MemPct = int(r.MemMi * (1 << 20) * 100 / allocMem)
			}
			if l := load[r.Name]; l != nil {
				r.Pods = l.Pods
				if allocCPU > 0 {
					r.ReqCPUPct = int(l.CPURequests * 100 / allocCPU)
				}
				if allocMem > 0 {
					r.ReqMemPct = int(l.MemoryRequests * 100 / allocMem)
				}
			}
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool {
			if byMem {
				return rows[i].MemMi > rows[j].MemMi
			}
			return rows[i].CPUm > rows[j].CPUm
		})
		total := len(rows)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		out := map[string]any{"target": "nodes", "total": total, "items": rows}
		if total > len(rows) {
			out["reasons"] = []Reason{Truncated("Node", len(rows), total)}
		}
		return out, nil
	}

	list, err := d.Metrics.ListPodMetrics(ctx, call.Cluster, in.Namespace)
	if err != nil {
		return nil, fmt.Errorf("metrics-server unavailable: %w", err)
	}
	cs, err := d.K8s.GetClientForCluster(call.Cluster)
	if err != nil {
		return nil, err
	}
	pods, err := cs.CoreV1().Pods(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	byName := map[string]*corev1.Pod{}
	for i := range pods.Items {
		p := &pods.Items[i]
		byName[p.Namespace+"/"+p.Name] = p
	}
	type row struct {
		Name     string `json:"name"`
		NS       string `json:"ns"`
		Owner    string `json:"owner,omitempty"`
		Node     string `json:"node,omitempty"`
		CPUm     int64  `json:"cpuMillicores"`
		MemMi    int64  `json:"memoryMi"`
		ReqCPUm  int64  `json:"cpuRequestM,omitempty"`
		LimCPUm  int64  `json:"cpuLimitM,omitempty"`
		ReqMemMi int64  `json:"memRequestMi,omitempty"`
		LimMemMi int64  `json:"memLimitMi,omitempty"`
	}
	var rows []row
	for _, it := range list.Items {
		r := row{Name: it.GetName(), NS: it.GetNamespace()}
		cl, _ := it.Object["containers"].([]any)
		for _, c := range cl {
			cm, _ := c.(map[string]any)
			u, _ := cm["usage"].(map[string]any)
			r.CPUm += cpuMilli(fmt.Sprint(u["cpu"]))
			r.MemMi += memMi(fmt.Sprint(u["memory"]))
		}
		if p := byName[r.NS+"/"+r.Name]; p != nil {
			r.Node = p.Spec.NodeName
			if len(p.OwnerReferences) > 0 {
				o := p.OwnerReferences[0]
				r.Owner = o.Kind + "/" + o.Name
			}
			for _, c := range p.Spec.Containers {
				r.ReqCPUm += c.Resources.Requests.Cpu().MilliValue()
				r.LimCPUm += c.Resources.Limits.Cpu().MilliValue()
				r.ReqMemMi += c.Resources.Requests.Memory().Value() / (1 << 20)
				r.LimMemMi += c.Resources.Limits.Memory().Value() / (1 << 20)
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if byMem {
			return rows[i].MemMi > rows[j].MemMi
		}
		return rows[i].CPUm > rows[j].CPUm
	})
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := map[string]any{"target": "pods", "total": total, "items": rows}
	if total > len(rows) {
		out["reasons"] = []Reason{Truncated("Pod", len(rows), total)}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// list_namespaces / list_packages

func (d *Deps) listNamespaces(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	cs, err := d.K8s.GetClientForCluster(call.Cluster)
	if err != nil {
		return nil, err
	}
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	type row struct {
		Name   string            `json:"name"`
		Phase  string            `json:"phase"`
		Age    string            `json:"age"`
		Labels map[string]string `json:"labels,omitempty"`
	}
	now := time.Now()
	out := make([]row, 0, len(list.Items))
	for _, n := range list.Items {
		lbl := map[string]string{}
		for k, v := range n.Labels {
			if k != "kubernetes.io/metadata.name" {
				lbl[k] = v
			}
		}
		if len(lbl) == 0 {
			lbl = nil
		}
		out = append(out, row{Name: n.Name, Phase: string(n.Status.Phase), Age: shortAge(now.Sub(n.CreationTimestamp.Time)), Labels: lbl})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return map[string]any{"namespaces": out, "total": len(out)}, nil
}

func (d *Deps) listPackages(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	out := map[string]any{}
	var reasons []Reason
	if d.Helm != nil {
		rels, err := d.Helm.ListReleases(ctx, call.Cluster, "", true)
		if err != nil {
			reasons = append(reasons, ReasonFor(err, "HelmRelease", ""))
		} else {
			rows := make([]string, 0, len(rels))
			for _, r := range rels {
				rows = append(rows, fmt.Sprintf("%s/%s %s-%s (%s)", r.Namespace, r.Name, r.Chart, r.ChartVersion, r.Status))
			}
			sort.Strings(rows)
			out["helm"] = rows
		}
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	listNames := func(gvr schema.GroupVersionResource, kind string, status func(map[string]any) string) {
		l, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1000})
		if err != nil {
			r := ReasonFor(err, kind, "")
			if r.Code == ReasonObjectNotFound {
				r.Code = ReasonKindNotInstalled
			}
			if r.Code != ReasonKindNotInstalled {
				reasons = append(reasons, r)
			}
			return
		}
		rows := make([]string, 0, len(l.Items))
		for _, it := range l.Items {
			s := it.GetNamespace() + "/" + it.GetName()
			if st := status(it.Object); st != "" {
				s += " (" + st + ")"
			}
			rows = append(rows, s)
		}
		sort.Strings(rows)
		out[kind] = rows
	}
	cond := func(obj map[string]any) string {
		st, _ := obj["status"].(map[string]any)
		cs, _ := st["conditions"].([]any)
		for _, c := range cs {
			cm, _ := c.(map[string]any)
			if str(cm, "type") == "Ready" {
				return "Ready=" + str(cm, "status")
			}
		}
		return ""
	}
	listNames(schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}, "argoApplications", func(o map[string]any) string {
		st, _ := o["status"].(map[string]any)
		sync, _ := st["sync"].(map[string]any)
		h, _ := st["health"].(map[string]any)
		return strings.Trim(str(sync, "status")+"/"+str(h, "status"), "/")
	})
	listNames(schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}, "fluxHelmReleases", cond)
	listNames(schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}, "fluxKustomizations", cond)

	crds, err := dyn.Resource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}).List(ctx, metav1.ListOptions{})
	if err != nil {
		reasons = append(reasons, ReasonFor(err, "CustomResourceDefinition", ""))
	} else {
		groups := map[string][]string{}
		for _, c := range crds.Items {
			spec, _ := c.Object["spec"].(map[string]any)
			names, _ := spec["names"].(map[string]any)
			g := str(spec, "group")
			groups[g] = append(groups[g], str(names, "kind"))
		}
		for g := range groups {
			sort.Strings(groups[g])
		}
		out["crdGroups"] = groups
		out["crdCount"] = len(crds.Items)
	}
	if len(reasons) > 0 {
		out["reasons"] = reasons
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Kanivet-only: crossplane, vclusters, node load

type traceArgs struct {
	mcpreg.ClusterArg
	APIVersion string `json:"apiVersion" jsonschema:"e.g. example.org/v1alpha1"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"claims are namespaced; composites are not"`
	Name       string `json:"name"`
}

type traceNode struct {
	Kind      string      `json:"kind"`
	Name      string      `json:"name"`
	Namespace string      `json:"ns,omitempty"`
	Ready     bool        `json:"ready"`
	Synced    bool        `json:"synced"`
	Status    string      `json:"status,omitempty"`
	Message   string      `json:"message,omitempty"`
	Children  []traceNode `json:"children,omitempty"`
}

func slimTrace(n crossplane.ResourceNode) traceNode {
	t := traceNode{Kind: n.Kind, Name: n.Name, Namespace: n.Namespace, Ready: n.Ready, Synced: n.Synced, Status: n.Status, Message: truncateText(ScrubText(n.Message), 300)}
	for _, c := range n.Children {
		t.Children = append(t.Children, slimTrace(c))
	}
	return t
}

func (d *Deps) crossplaneTrace(ctx context.Context, call *mcpreg.Call, in traceArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	ki, err := d.resolveKind(call.Cluster, in.Kind+"."+groupOf(in.APIVersion))
	if err != nil {
		if ki, err = d.resolveKind(call.Cluster, in.Kind); err != nil {
			return nil, err
		}
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	root, err := crossplane.NewTracer(dyn).Trace(ctx, ki.GVR, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	return slimTrace(*root), nil
}

func groupOf(apiVersion string) string {
	if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
		return apiVersion[:i]
	}
	return ""
}

func (d *Deps) listVClusters(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	vcs, err := d.K8s.ListVClusters(call.Cluster)
	if err != nil {
		return nil, err
	}
	return map[string]any{"vclusters": vcs, "total": len(vcs)}, nil
}

func (d *Deps) nodeLoad(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.NodeLoad == nil {
		return nil, errors.New("node load not available")
	}
	rep, err := d.NodeLoad.Load(ctx, call.Cluster)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"nodes": rep.Nodes}
	if rep.UsageError != "" {
		out["reasons"] = []Reason{{Code: ReasonReadError, Kind: "NodeMetrics", Detail: "usage unavailable: " + rep.UsageError}}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// write tools

type patchArgs struct {
	mcpreg.ClusterArg
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty" jsonschema:"merge (default), json or strategic"`
	Patch     string `json:"patch" jsonschema:"the patch document as JSON"`
	DryRun    bool   `json:"dryRun,omitempty" jsonschema:"validate on the server without persisting"`
}

func (d *Deps) patchResource(ctx context.Context, call *mcpreg.Call, in patchArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if in.Kind == "Secret" {
		return nil, errors.New("patching Secrets through MCP is not allowed")
	}
	if !json.Valid([]byte(in.Patch)) {
		return nil, errors.New("patch must be valid JSON")
	}
	pt := types.MergePatchType
	switch in.Type {
	case "", "merge":
	case "json":
		pt = types.JSONPatchType
	case "strategic":
		pt = types.StrategicMergePatchType
	default:
		return nil, fmt.Errorf("type must be merge, json or strategic")
	}
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	opts := metav1.PatchOptions{FieldManager: "kanivet-mcp"}
	if in.DryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	ri := dyn.Resource(ki.GVR)
	var res map[string]any
	if ki.Namespaced {
		ns := in.Namespace
		if ns == "" {
			ns = "default"
		}
		u, err := ri.Namespace(ns).Patch(ctx, in.Name, pt, []byte(in.Patch), opts)
		if err != nil {
			return nil, err
		}
		res = u.Object
	} else {
		u, err := ri.Patch(ctx, in.Name, pt, []byte(in.Patch), opts)
		if err != nil {
			return nil, err
		}
		res = u.Object
	}
	return map[string]any{"dryRun": in.DryRun, "result": Redact(Minify(res))}, nil
}

type cronArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Action    string `json:"action" jsonschema:"trigger, suspend or resume"`
}

func (d *Deps) manageCronJob(ctx context.Context, call *mcpreg.Call, in cronArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	switch in.Action {
	case "trigger":
		job, err := d.K8s.TriggerCronJob(ctx, call.Cluster, in.Namespace, in.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "job": job}, nil
	case "suspend", "resume":
		cs, err := d.K8s.GetClientForCluster(call.Cluster)
		if err != nil {
			return nil, err
		}
		patch := fmt.Sprintf(`{"spec":{"suspend":%t}}`, in.Action == "suspend")
		if _, err := cs.BatchV1().CronJobs(in.Namespace).Patch(ctx, in.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: "kanivet-mcp"}); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "suspended": in.Action == "suspend"}, nil
	}
	return nil, errors.New("action must be trigger, suspend or resume")
}

type rolloutArgs struct {
	mcpreg.ClusterArg
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Action    string `json:"action" jsonschema:"promote, abort, retry, pause or resume"`
	Full      bool   `json:"full,omitempty" jsonschema:"promote: skip all remaining steps"`
}

var rolloutGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}

func (d *Deps) manageRollout(ctx context.Context, call *mcpreg.Call, in rolloutArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if !d.rolloutsInstalled(call.Cluster) {
		return Explain(fmt.Errorf("no matches for kind \"Rollout\" in version \"argoproj.io/v1alpha1\": Argo Rollouts is not installed"), "Rollout", in.Namespace), nil
	}
	dyn, err := d.K8s.GetDynamicClient(call.Cluster)
	if err != nil {
		return nil, err
	}
	ri := dyn.Resource(rolloutGVR).Namespace(in.Namespace)
	opts := metav1.PatchOptions{FieldManager: "kanivet-mcp"}
	spec := func(p string) error {
		_, err := ri.Patch(ctx, in.Name, types.MergePatchType, []byte(p), opts)
		return err
	}
	status := func(p string) error {
		_, err := ri.Patch(ctx, in.Name, types.MergePatchType, []byte(p), opts, "status")
		return err
	}
	switch in.Action {
	case "promote":
		if in.Full {
			err = status(`{"status":{"promoteFull":true}}`)
		} else {
			err = status(`{"status":{"pauseConditions":null}}`)
		}
		if err == nil {
			err = spec(`{"spec":{"paused":false}}`)
		}
	case "abort":
		err = status(`{"status":{"abort":true}}`)
	case "retry":
		err = status(`{"status":{"abort":false}}`)
	case "pause":
		err = spec(`{"spec":{"paused":true}}`)
	case "resume":
		err = spec(`{"spec":{"paused":false}}`)
	default:
		return nil, errors.New("action must be promote, abort, retry, pause or resume")
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "action": in.Action}, nil
}

func (d *Deps) rolloutsInstalled(cluster string) bool {
	res, err := d.apiResources(cluster)
	if err != nil {
		return false
	}
	for _, r := range res {
		if r.Group == rolloutGVR.Group && r.Name == rolloutGVR.Resource {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// exec_pod (registered only when the "allow agents to run commands" setting
// is on; see Service.syncExecTool)

const execToolName = "exec_pod"

type execArgs struct {
	mcpreg.ClusterArg
	Namespace      string   `json:"namespace"`
	Pod            string   `json:"pod"`
	Container      string   `json:"container,omitempty"`
	Command        []string `json:"command" jsonschema:"argv, e.g. [\"ls\",\"-la\",\"/app\"]; no shell unless you pass one"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"default 20, max 120"`
}

const execOutputCap = 64 << 10

type cappedBuffer struct {
	buf bytes.Buffer
	cut bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := execOutputCap - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.cut = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.cut = true
	}
	return len(p), nil
}

func registerExecTool(r *mcpreg.Registry, d *Deps) {
	mcpreg.RegisterOn(r, mcpreg.Spec{Name: execToolName, Destructive: true, Feature: "actions", Title: "Exec in pod",
		Description: "Run one non-interactive command in a container (argv, no shell by default) with a timeout; returns exit code, stdout and stderr separately, each capped at 64 KiB. Output is RAW: it is not scrubbed and may contain secrets. Enabled by the user in Kanivet (AI & MCP > Allow agents to run commands in pods)."}, d.execPod)
}

func (d *Deps) execPod(ctx context.Context, call *mcpreg.Call, in execArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if call.Endpoint != "full" {
		return nil, errors.New("exec_pod is only served on /mcp")
	}
	if len(in.Command) == 0 {
		return nil, errors.New("command is required")
	}
	timeout := time.Duration(clamp(in.TimeoutSeconds, 20, 120)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cs, cfg, err := d.K8s.GetClientAndConfig(call.Cluster)
	if err != nil {
		return nil, err
	}
	container := in.Container
	if container == "" {
		p, err := cs.CoreV1().Pods(in.Namespace).Get(ctx, in.Pod, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if len(p.Spec.Containers) == 0 {
			return nil, errors.New("pod has no containers")
		}
		container = p.Spec.Containers[0].Name
	}
	req := cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(in.Namespace).Name(in.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: in.Command, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(cfg, http.MethodPost, req.URL())
	if err != nil {
		return nil, err
	}
	var stdout, stderr cappedBuffer
	start := time.Now()
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr})
	out := map[string]any{"container": container, "exitCode": 0, "stdout": stdout.buf.String(), "stderr": stderr.buf.String(), "durationMs": time.Since(start).Milliseconds()}
	if stdout.cut || stderr.cut {
		out["truncated"] = true
	}
	if err != nil {
		var ce interface{ ExitStatus() int }
		switch {
		case errors.As(err, &ce):
			out["exitCode"] = ce.ExitStatus()
		case ctx.Err() != nil:
			out["exitCode"] = -1
			out["error"] = fmt.Sprintf("timed out after %s", timeout)
		default:
			return nil, err
		}
	}
	return out, nil
}
