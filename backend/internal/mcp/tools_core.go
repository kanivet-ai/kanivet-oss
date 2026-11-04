package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/yaml"

	"github.com/kanivet/backend/internal/incidents"
	"github.com/kanivet/backend/internal/mcp/mcpreg"
	"github.com/kanivet/backend/internal/search"
	"github.com/kanivet/backend/internal/topics"
)

var errNoK8s = errors.New("kubernetes client not available")

func registerCoreTools(r *mcpreg.Registry, d *Deps) {
	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:            "list_clusters",
		Title:           "List clusters",
		ClusterOptional: true,
		Description:     "Kube contexts Kanivet knows, which one is open in the UI (the default for every tool), and the cloud provider.",
	}, d.listClusters)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "list_resources",
		Title:       "List resources",
		Description: "Compact one-line rows (name, ns, status, ready, restarts, node, age) for a kind. Filter by namespace, label selector, name substring, or problems=true for only unhealthy objects. Reads Kanivet's watch cache when warm.",
	}, d.listResources)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "get_resource",
		Title:       "Get resource",
		Description: "One object as minified YAML: managedFields, defaults and empty fields removed; Secret values and secret-looking strings redacted.",
	}, d.getResource)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "get_events",
		Title:       "Get events",
		Description: "Kubernetes events, deduplicated (same object+reason+message folded with a count), newest first. Scope to an object with kind/name, or a namespace; warningsOnly defaults to true.",
	}, d.getEvents)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "get_logs",
		Title:       "Get logs",
		Description: "Recent log lines of a pod, or of a workload's pods (Deployment/StatefulSet/DaemonSet/Job). Filter with grep (regex) or errorsOnly; repeated lines are collapsed and secrets scrubbed. previous=true reads the last crashed container.",
	}, d.getLogs)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "search",
		Title:       "Search",
		Description: "Full-text search over Kanivet's resource index (names, labels, annotations, images) across kinds. Fastest way to find a resource when you know part of its name.",
	}, d.search)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "get_incidents",
		Title:       "Get incidents",
		Description: "Kanivet's incident classifier over recent cluster events: grouped, severity-ranked problems (critical/warning) with affected objects and counts. Start here for 'what is wrong in this cluster'.",
	}, d.getIncidents)

	mcpreg.RegisterOn(r, mcpreg.Spec{
		Name:        "diagnose",
		Title:       "Diagnose",
		Description: "Everything needed to explain one resource in a single call: health verdict and findings, owner chain, related objects (pods, node, services, config refs), deduplicated events and the tail of failing containers' logs.",
	}, d.diagnose)

	registerExtraTools(r, d)
	registerWriteTools(r, d)
}

// ---------------------------------------------------------------------------

type noArgs struct {
	mcpreg.ClusterArg
}

func (d *Deps) listClusters(ctx context.Context, call *mcpreg.Call, _ noArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	cl, err := d.K8s.ListClusters()
	if err != nil {
		return nil, err
	}
	type row struct {
		Name     string `json:"name"`
		Provider string `json:"provider,omitempty"`
		Default  bool   `json:"default,omitempty"`
	}
	out := make([]row, 0, len(cl))
	for _, c := range cl {
		out = append(out, row{Name: c.Name, Provider: c.Provider, Default: c.Name == call.Cluster})
	}
	return map[string]any{"clusters": out, "default": call.Cluster}, nil
}

// ---------------------------------------------------------------------------

type listArgs struct {
	mcpreg.ClusterArg
	Kind          string `json:"kind" jsonschema:"kind, plural or short name: Pod, deployments, svc, certificates.cert-manager.io"`
	Namespace     string `json:"namespace,omitempty" jsonschema:"namespace; empty for all"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"e.g. app=web,tier!=db"`
	NameContains  string `json:"nameContains,omitempty"`
	Problems      bool   `json:"problems,omitempty" jsonschema:"only objects that are not healthy"`
	Limit         int    `json:"limit,omitempty" jsonschema:"max rows (default 100, max 500)"`
}

func (d *Deps) listObjects(ctx context.Context, cluster string, ki kindInfo, namespace, selector string) ([]map[string]any, string, error) {
	// Warm watch cache first: no API call.
	if d.Watcher != nil && selector == "" && d.Watcher.IsWatching(cluster, ki.GVR.Group, ki.GVR.Version, ki.GVR.Resource) {
		for _, k := range []string{ki.Kind, ki.GVR.Resource} {
			items := d.Watcher.GetCache().GetAll(topics.BuildItemsTopic(cluster, ki.GVR.Group, ki.GVR.Version, k, ""))
			if len(items) > 0 {
				if namespace != "" {
					kept := items[:0:0]
					for _, it := range items {
						if md, _ := it["metadata"].(map[string]any); str(md, "namespace") == namespace {
							kept = append(kept, it)
						}
					}
					items = kept
				}
				return items, "watch-cache", nil
			}
		}
	}
	if d.K8s == nil {
		return nil, "", errNoK8s
	}
	dyn, err := d.K8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, "", err
	}
	opts := metav1.ListOptions{LabelSelector: selector, Limit: 2000}
	var list *unstructured.UnstructuredList
	if ki.Namespaced && namespace != "" {
		list, err = dyn.Resource(ki.GVR).Namespace(namespace).List(ctx, opts)
	} else {
		list, err = dyn.Resource(ki.GVR).List(ctx, opts)
	}
	if err != nil {
		return nil, "", err
	}
	out := make([]map[string]any, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Object)
	}
	return out, "api", nil
}

func healthyStatus(s string) bool {
	switch s {
	case "Running", "Ready", "Succeeded", "Completed", "Complete", "Bound", "Active", "Available", "Synced", "Healthy", "ScaledDown", "SuccessCriteriaMet", "":
		return true
	}
	return false
}

func (d *Deps) listResources(ctx context.Context, call *mcpreg.Call, in listArgs) (any, error) {
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	items, source, err := d.listObjects(ctx, call.Cluster, ki, in.Namespace, in.LabelSelector)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	now := time.Now()
	rows := make([]Row, 0, len(items))
	for _, it := range items {
		if _, ok := it["kind"]; !ok {
			it = shallowWithKind(it, ki.Kind)
		}
		r := Summarize(it, now)
		if in.NameContains != "" && !strings.Contains(strings.ToLower(r.Name), strings.ToLower(in.NameContains)) {
			continue
		}
		if in.Problems && healthyStatus(r.Status) && !(r.Ready != "" && !readyComplete(r.Ready)) {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return rows[i].Name < rows[j].Name
	})
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return map[string]any{"kind": ki.Kind, "apiVersion": ki.APIVersion(), "total": total, "truncated": total > len(rows), "source": source, "items": rows}, nil
}

func readyComplete(ready string) bool {
	parts := strings.SplitN(ready, "/", 2)
	return len(parts) == 2 && parts[0] == parts[1]
}

func shallowWithKind(m map[string]any, kind string) map[string]any {
	c := make(map[string]any, len(m)+1)
	for k, v := range m {
		c[k] = v
	}
	c["kind"] = kind
	return c
}

// ---------------------------------------------------------------------------

type getArgs struct {
	mcpreg.ClusterArg
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Format    string `json:"format,omitempty" jsonschema:"yaml (default) or json"`
}

func (d *Deps) fetch(ctx context.Context, cluster string, ki kindInfo, namespace, name string) (map[string]any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	dyn, err := d.K8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, err
	}
	var u *unstructured.Unstructured
	if ki.Namespaced {
		if namespace == "" {
			namespace = "default"
		}
		u, err = dyn.Resource(ki.GVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		u, err = dyn.Resource(ki.GVR).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}
	return u.Object, nil
}

func (d *Deps) getResource(ctx context.Context, call *mcpreg.Call, in getArgs) (any, error) {
	ki, err := d.resolveKind(call.Cluster, in.Kind)
	if err != nil {
		return nil, err
	}
	obj, err := d.fetch(ctx, call.Cluster, ki, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	clean := Redact(Minify(obj))
	if strings.EqualFold(in.Format, "json") {
		return clean, nil
	}
	b, err := yaml.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// ---------------------------------------------------------------------------

type eventsArgs struct {
	mcpreg.ClusterArg
	Namespace    string `json:"namespace,omitempty"`
	Kind         string `json:"kind,omitempty" jsonschema:"involved object kind"`
	Name         string `json:"name,omitempty" jsonschema:"involved object name"`
	WarningsOnly *bool  `json:"warningsOnly,omitempty" jsonschema:"default true"`
	SinceMinutes int    `json:"sinceMinutes,omitempty" jsonschema:"default 360"`
	Limit        int    `json:"limit,omitempty" jsonschema:"default 50"`
}

// EventGroup is a deduplicated event.
type EventGroup struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Object  string `json:"object"`
	Message string `json:"message"`
	Count   int32  `json:"count"`
	First   string `json:"first"`
	Last    string `json:"last"`
}

var digitsRe = regexp.MustCompile(`[0-9a-f]{5,}|\d+`)

// DedupeEvents folds events that differ only in ids/counters.
func DedupeEvents(evs []corev1.Event, now time.Time) []EventGroup {
	type acc struct {
		g     EventGroup
		first time.Time
		last  time.Time
	}
	groups := map[string]*acc{}
	var order []string
	for _, e := range evs {
		obj := e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name
		if e.InvolvedObject.Namespace != "" {
			obj = e.InvolvedObject.Kind + "/" + e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name
		}
		key := obj + "|" + e.Reason + "|" + digitsRe.ReplaceAllString(e.Message, "#")
		first, last := eventTimes(e)
		count := e.Count
		if count == 0 {
			count = 1
		}
		if e.Series != nil && e.Series.Count > count {
			count = e.Series.Count
		}
		a, ok := groups[key]
		if !ok {
			a = &acc{g: EventGroup{Type: e.Type, Reason: e.Reason, Object: obj, Message: truncateText(ScrubText(e.Message), 300)}, first: first, last: last}
			groups[key] = a
			order = append(order, key)
		}
		a.g.Count += count
		if first.Before(a.first) {
			a.first = first
		}
		if last.After(a.last) {
			a.last = last
			a.g.Message = truncateText(ScrubText(e.Message), 300)
		}
	}
	out := make([]EventGroup, 0, len(groups))
	lasts := map[string]time.Time{}
	for _, k := range order {
		a := groups[k]
		a.g.First = shortAge(now.Sub(a.first)) + " ago"
		a.g.Last = shortAge(now.Sub(a.last)) + " ago"
		out = append(out, a.g)
		lasts[a.g.Object+a.g.Reason+a.g.Message] = a.last
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lasts[out[i].Object+out[i].Reason+out[i].Message].After(lasts[out[j].Object+out[j].Reason+out[j].Message])
	})
	return out
}

func eventTimes(e corev1.Event) (time.Time, time.Time) {
	first := e.FirstTimestamp.Time
	last := e.LastTimestamp.Time
	if !e.EventTime.IsZero() {
		if first.IsZero() {
			first = e.EventTime.Time
		}
		if last.IsZero() {
			last = e.EventTime.Time
		}
	}
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		last = e.Series.LastObservedTime.Time
	}
	if first.IsZero() {
		first = e.CreationTimestamp.Time
	}
	if last.IsZero() {
		last = first
	}
	return first, last
}

func (d *Deps) events(ctx context.Context, cluster, namespace, kind, name string, warningsOnly bool, since time.Duration) ([]corev1.Event, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	cs, err := d.K8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	var sel []string
	if name != "" {
		sel = append(sel, "involvedObject.name="+name)
	}
	if kind != "" {
		sel = append(sel, "involvedObject.kind="+kind)
	}
	if warningsOnly {
		sel = append(sel, "type=Warning")
	}
	list, err := cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: strings.Join(sel, ","), Limit: 1000})
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-since)
	out := list.Items[:0]
	for _, e := range list.Items {
		_, last := eventTimes(e)
		if since > 0 && last.Before(cutoff) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (d *Deps) getEvents(ctx context.Context, call *mcpreg.Call, in eventsArgs) (any, error) {
	warn := in.WarningsOnly == nil || *in.WarningsOnly
	since := time.Duration(in.SinceMinutes) * time.Minute
	if in.SinceMinutes <= 0 {
		since = 6 * time.Hour
	}
	kind := in.Kind
	if kind != "" {
		if ki, err := d.resolveKind(call.Cluster, kind); err == nil {
			kind = ki.Kind
		}
	}
	evs, err := d.events(ctx, call.Cluster, in.Namespace, kind, in.Name, warn, since)
	if err != nil {
		return nil, err
	}
	groups := DedupeEvents(evs, time.Now())
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	total := len(groups)
	if len(groups) > limit {
		groups = groups[:limit]
	}
	return map[string]any{"total": total, "rawEvents": len(evs), "events": groups}, nil
}

// ---------------------------------------------------------------------------

type logsArgs struct {
	mcpreg.ClusterArg
	Namespace    string `json:"namespace"`
	Name         string `json:"name" jsonschema:"pod name, or a workload name when kind is set"`
	Kind         string `json:"kind,omitempty" jsonschema:"Pod (default), Deployment, StatefulSet, DaemonSet, Job, ReplicaSet"`
	Container    string `json:"container,omitempty"`
	Tail         int    `json:"tail,omitempty" jsonschema:"lines per container (default 150, max 2000)"`
	SinceMinutes int    `json:"sinceMinutes,omitempty"`
	Grep         string `json:"grep,omitempty" jsonschema:"case-insensitive regex; only matching lines"`
	ErrorsOnly   bool   `json:"errorsOnly,omitempty" jsonschema:"only lines that look like errors/warnings/panics"`
	Previous     bool   `json:"previous,omitempty" jsonschema:"logs of the previous (crashed) container instance"`
	MaxPods      int    `json:"maxPods,omitempty" jsonschema:"for workloads, pods to sample (default 3)"`
}

var errorLineRe = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|exception|fail(ed|ure)?|warn(ing)?|traceback|oom|killed|refused|timeout|denied)\b`)

// FilterLogLines applies grep/errorsOnly, scrubs secrets and collapses
// consecutive duplicate lines.
func FilterLogLines(text string, grep *regexp.Regexp, errorsOnly bool, maxLines int) (lines []string, dropped int) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var prev string
	repeat := 0
	flush := func() {
		if repeat > 0 {
			lines = append(lines, fmt.Sprintf("  … previous line repeated %d more times", repeat))
			repeat = 0
		}
	}
	for sc.Scan() {
		l := sc.Text()
		if grep != nil && !grep.MatchString(l) {
			dropped++
			continue
		}
		if errorsOnly && !errorLineRe.MatchString(l) {
			dropped++
			continue
		}
		l = truncateText(ScrubText(l), 600)
		if l == prev {
			repeat++
			continue
		}
		flush()
		lines = append(lines, l)
		prev = l
	}
	flush()
	if maxLines > 0 && len(lines) > maxLines {
		dropped += len(lines) - maxLines
		lines = lines[len(lines)-maxLines:]
	}
	return lines, dropped
}

func (d *Deps) podsFor(ctx context.Context, cluster, namespace, kind, name string) ([]corev1.Pod, error) {
	cs, err := d.K8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	if kind == "" || strings.EqualFold(kind, "pod") || strings.EqualFold(kind, "pods") {
		p, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return []corev1.Pod{*p}, nil
	}
	ki, err := d.resolveKind(cluster, kind)
	if err != nil {
		return nil, err
	}
	obj, err := d.fetch(ctx, cluster, ki, namespace, name)
	if err != nil {
		return nil, err
	}
	spec, _ := obj["spec"].(map[string]any)
	selm, _ := spec["selector"].(map[string]any)
	ml, _ := selm["matchLabels"].(map[string]any)
	if ki.Kind == "Job" && len(ml) == 0 {
		ml = map[string]any{"job-name": name}
	}
	if len(ml) == 0 {
		return nil, fmt.Errorf("%s %s has no matchLabels selector", ki.Kind, name)
	}
	set := labels.Set{}
	for k, v := range ml {
		set[k], _ = v.(string)
	}
	list, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: set.String()})
	if err != nil {
		return nil, err
	}
	pods := list.Items
	// Unhealthy pods first: they are what an investigation wants to read.
	sort.SliceStable(pods, func(i, j int) bool { return podTrouble(&pods[i]) > podTrouble(&pods[j]) })
	return pods, nil
}

func podTrouble(p *corev1.Pod) int {
	score := 0
	for _, c := range p.Status.ContainerStatuses {
		if !c.Ready {
			score += 10
		}
		score += int(c.RestartCount)
	}
	if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodSucceeded {
		score += 20
	}
	return score
}

type containerLogs struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container"`
	Previous  bool     `json:"previous,omitempty"`
	Lines     []string `json:"lines"`
	Filtered  int      `json:"filtered,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (d *Deps) readLogs(ctx context.Context, cluster string, pod corev1.Pod, container string, tail int64, since time.Duration, previous bool, grep *regexp.Regexp, errorsOnly bool) containerLogs {
	out := containerLogs{Pod: pod.Name, Container: container, Previous: previous, Lines: []string{}}
	cs, err := d.K8s.GetClientForCluster(cluster)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	opts := &corev1.PodLogOptions{Container: container, Previous: previous}
	// Read more than we show when filtering, so a grep finds something.
	read := tail
	if grep != nil || errorsOnly {
		read = tail * 10
		if read > 5000 {
			read = 5000
		}
	}
	opts.TailLines = &read
	if since > 0 {
		s := int64(since.Seconds())
		opts.SinceSeconds = &s
	}
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rc, err := cs.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts).Stream(lctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer rc.Close()
	data, _ := io.ReadAll(io.LimitReader(rc, 4<<20))
	out.Lines, out.Filtered = FilterLogLines(string(data), grep, errorsOnly, int(tail))
	return out
}

func (d *Deps) getLogs(ctx context.Context, call *mcpreg.Call, in logsArgs) (any, error) {
	if d.K8s == nil {
		return nil, errNoK8s
	}
	if in.Namespace == "" {
		in.Namespace = "default"
	}
	tail := int64(in.Tail)
	if tail <= 0 {
		tail = 150
	}
	if tail > 2000 {
		tail = 2000
	}
	var grep *regexp.Regexp
	if in.Grep != "" {
		var err error
		if grep, err = regexp.Compile("(?i)" + in.Grep); err != nil {
			return nil, fmt.Errorf("invalid grep regex: %w", err)
		}
	}
	pods, err := d.podsFor(ctx, call.Cluster, in.Namespace, in.Kind, in.Name)
	if err != nil {
		return nil, err
	}
	maxPods := in.MaxPods
	if maxPods <= 0 {
		maxPods = 3
	}
	if len(pods) > maxPods {
		pods = pods[:maxPods]
	}
	since := time.Duration(in.SinceMinutes) * time.Minute
	var out []containerLogs
	for _, p := range pods {
		containers := []string{}
		if in.Container != "" {
			containers = append(containers, in.Container)
		} else {
			for _, c := range p.Spec.Containers {
				containers = append(containers, c.Name)
			}
		}
		for _, c := range containers {
			out = append(out, d.readLogs(ctx, call.Cluster, p, c, tail, since, in.Previous, grep, in.ErrorsOnly))
		}
	}
	return map[string]any{"logs": out}, nil
}

// ---------------------------------------------------------------------------

type searchArgs struct {
	mcpreg.ClusterArg
	Query       string   `json:"query"`
	Kinds       []string `json:"kinds,omitempty"`
	Namespaces  []string `json:"namespaces,omitempty"`
	AllClusters bool     `json:"allClusters,omitempty" jsonschema:"search every indexed cluster instead of one"`
	Limit       int      `json:"limit,omitempty" jsonschema:"default 25"`
}

func (d *Deps) search(ctx context.Context, call *mcpreg.Call, in searchArgs) (any, error) {
	if d.Search == nil {
		return nil, errors.New("search index not available")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	opts := search.SearchOptions{Kinds: in.Kinds, Namespaces: in.Namespaces, Limit: limit}
	if !in.AllClusters {
		opts.Clusters = []string{call.Cluster}
	}
	res, err := d.Search.Search(in.Query, opts)
	if err != nil {
		return nil, err
	}
	type hit struct {
		Cluster    string `json:"cluster,omitempty"`
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion,omitempty"`
		Namespace  string `json:"ns,omitempty"`
		Name       string `json:"name"`
		Match      string `json:"match,omitempty"`
		Stale      bool   `json:"stale,omitempty"`
	}
	out := make([]hit, 0, len(res))
	for _, r := range res {
		h := hit{Kind: r.Resource.Kind, APIVersion: r.Resource.APIVersion, Namespace: r.Resource.Namespace, Name: r.Resource.Name, Stale: r.Stale}
		if in.AllClusters {
			h.Cluster = r.Resource.Cluster
		}
		if len(r.Matches) > 0 && r.Matches[0].Field != "name" {
			h.Match = r.Matches[0].Field + "=" + truncateText(ScrubText(r.Matches[0].Value), 80)
		}
		out = append(out, h)
	}
	return map[string]any{"results": out}, nil
}

// ---------------------------------------------------------------------------

type incidentsArgs struct {
	mcpreg.ClusterArg
	Namespace    string `json:"namespace,omitempty"`
	Severity     string `json:"severity,omitempty" jsonschema:"critical or warning (default both)"`
	SinceMinutes int    `json:"sinceMinutes,omitempty" jsonschema:"default 180"`
	Limit        int    `json:"limit,omitempty" jsonschema:"default 40"`
}

func (d *Deps) getIncidents(ctx context.Context, call *mcpreg.Call, in incidentsArgs) (any, error) {
	if d.Incidents == nil {
		return nil, errors.New("incident classifier not available")
	}
	since := in.SinceMinutes
	if since <= 0 {
		since = 180
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 40
	}
	f := incidents.TimelineFilters{Cluster: call.Cluster, Since: time.Now().Add(-time.Duration(since) * time.Minute), Limit: 500}
	if in.Namespace != "" {
		f.Namespaces = []string{in.Namespace}
	}
	switch strings.ToLower(in.Severity) {
	case "critical":
		f.Severities = []incidents.Severity{incidents.SeverityCritical}
	case "warning":
		f.Severities = []incidents.Severity{incidents.SeverityWarning}
	default:
		f.Severities = []incidents.Severity{incidents.SeverityCritical, incidents.SeverityWarning}
	}
	resp, err := d.Incidents.GetTimeline(ctx, f)
	if err != nil {
		return nil, err
	}
	type inc struct {
		Severity string `json:"severity"`
		Reason   string `json:"reason"`
		Object   string `json:"object"`
		Message  string `json:"message"`
		Count    int32  `json:"count"`
		Last     string `json:"last"`
	}
	now := time.Now()
	out := make([]inc, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		obj := e.Kind + "/" + e.Name
		if e.Namespace != "" {
			obj = e.Kind + "/" + e.Namespace + "/" + e.Name
		}
		out = append(out, inc{Severity: string(e.Severity), Reason: e.Reason, Object: obj, Message: truncateText(ScrubText(e.Message), 240), Count: e.Count, Last: shortAge(now.Sub(e.LastTimestamp)) + " ago"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity == "critical" && out[j].Severity != "critical" })
	total := len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return map[string]any{"summary": resp.Summary, "total": total, "incidents": out, "note": "Requires Kanivet's event listener for this cluster; events before it started are not included."}, nil
}
