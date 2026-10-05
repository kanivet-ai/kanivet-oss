package rightsizing

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"maps"
	"math"
	"math/rand/v2"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/podcache"
	"github.com/kanivet/backend/internal/metrics"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// reportTTL is how long a computed report is served before a read starts
	// a fresh computation in the background. Two weeks of history hardly
	// move in an hour, and every refresh is real load on the metrics store.
	reportTTL = time.Hour
	// unavailableTTL is the same for "no history source" answers, short so a
	// newly configured metrics store is picked up quickly.
	unavailableTTL = time.Minute
	computeTimeout = 10 * time.Minute
	// refreshJitter spreads background refreshes of reports already shown.
	refreshJitter = 20 * time.Second
	// namespaceConcurrency bounds parallel namespace fetches: each moves up to
	// tens of megabytes through one port-forward.
	namespaceConcurrency = 3
	hoursPerMonth        = 24 * 30
	defaultWindow        = 14 * day
)

// metricsSource is the slice of metrics.Service rightsizing needs.
type metricsSource interface {
	historyQuerier
	HistorySource(ctx context.Context, cluster string) (*metrics.ProviderInfo, bool)
}

type rateSource interface {
	NodeRates(ctx context.Context, cluster string) (map[string]finops.Rates, error)
}

type store interface {
	SaveRightsizingReport(key, cluster string, data []byte) error
	GetRightsizingReport(key string) ([]byte, error)
	ListRightsizingDismissals(cluster string) ([]db.RightsizingDismissal, error)
	SaveRightsizingDismissal(d *db.RightsizingDismissal) error
	DeleteRightsizingDismissal(cluster, namespace, vclusterNamespace, kind, name, container string) error
	ForgetRightsizingClusters(match func(cluster string) bool) error
}

type Service struct {
	k8s     k8s.Interface
	metrics metricsSource
	rates   rateSource
	pods    podcache.Lister
	store   store
	now     func() time.Time

	mu   sync.Mutex
	runs map[string]*run
	// forget orders ForgetCluster against reading and writing persisted
	// reports, so a report for the old metrics settings can't be loaded or
	// saved while it is being forgotten. Taken before mu.
	forget sync.RWMutex
}

// run is the state of one cluster/profile/window report.
type run struct {
	cluster   string
	ready     *Report // last finished report
	readyAt   time.Time
	computing bool
	progress  Progress
	loaded    bool   // persisted report looked up
	lastError string // why the last refresh failed, while ready is older
}

func NewService(k8sClient k8s.Interface, metricsSvc metricsSource, rates rateSource, pods podcache.Lister, st store) *Service {
	if d, ok := st.(*db.DB); ok && d == nil {
		st = nil // the database failed to open; run without persistence
	}
	return &Service{
		k8s:     k8sClient,
		metrics: metricsSvc,
		rates:   rates,
		pods:    pods,
		store:   st,
		now:     time.Now,
		runs:    map[string]*run{},
	}
}

// ParseWindow accepts 7d, 14d or 28d; anything else is 14 days.
func ParseWindow(s string) time.Duration {
	switch s {
	case "7d":
		return 7 * day
	case "28d":
		return 28 * day
	}
	return defaultWindow
}

func windowLabel(w time.Duration) string { return fmt.Sprintf("%dd", int(w/day)) }

func runKey(cluster string, p Profile, w time.Duration, provider ...string) string {
	key := cluster + "|" + string(p) + "|" + windowLabel(w)
	if len(provider) > 0 && provider[0] != "" && provider[0] != "auto" {
		key += "|provider=" + provider[0]
	}
	return key
}

// GetReport returns the newest report for a cluster without waiting on a
// computation. A missing or old report starts one in the background; until it
// finishes the last report (marked stale) or a progress-only report is
// returned, and the caller polls.
func (s *Service) GetReport(cluster string, profile Profile, window time.Duration, refresh bool, known string, provider string) *Report {
	key := runKey(cluster, profile, window, provider)
	s.mu.Lock()
	r := s.runs[key]
	if r == nil {
		r = &run{cluster: cluster}
		s.runs[key] = r
	}
	needLoad := !r.loaded
	r.loaded = true
	s.mu.Unlock()
	// SQLite reads stay outside the lock every poller and progress update takes.
	if needLoad {
		s.forget.RLock()
		if rep := s.loadPersisted(key); rep != nil {
			s.mu.Lock()
			if r.ready == nil {
				r.ready, r.readyAt = rep, rep.ComputedAt
			}
			s.mu.Unlock()
		}
		s.forget.RUnlock()
	}
	out := s.snapshot(r, cluster, profile, window, refresh, provider)
	if out.Status != StatusReady {
		return out
	}
	list := s.dismissalList(cluster)
	out.Version = reportVersion(out, list)
	if known != "" && known == out.Version {
		// The caller has these workloads: skip copying, re-applying
		// dismissals to and serialising a report of thousands of them.
		out.Workloads, out.Unchanged = nil, true
		return out
	}
	return withDismissals(out, dismissalIndexOf(list))
}

// reportVersion changes whenever the workloads a poll would get change: a new
// computation, or a dismissal added, removed or expired.
func reportVersion(r *Report, list []db.RightsizingDismissal) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d", r.ComputedAt.UnixNano())
	for _, d := range list {
		fmt.Fprintf(h, "|%d:%d", d.ID, d.Until)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// snapshot starts a computation when one is due and copies the run's state.
func (s *Service) snapshot(r *run, cluster string, profile Profile, window time.Duration, refresh bool, provider string) *Report {
	key := runKey(cluster, profile, window, provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl := reportTTL
	if r.ready != nil && (r.ready.Status != StatusReady || r.lastError != "") {
		ttl = unavailableTTL
	}
	fresh := r.ready != nil && s.now().Sub(r.readyAt) < ttl
	if (!fresh || refresh) && !r.computing {
		r.computing = true
		r.progress = Progress{Stage: "Finding workloads"}
		// A background refresh of a report still on screen starts after a
		// random delay, so users who opened Kanivet at the same moment don't
		// all hit the metrics store together. A first load or an explicit
		// refresh has someone waiting and starts now.
		var delay time.Duration
		if r.ready != nil && !refresh {
			delay = time.Duration(rand.Float64() * float64(refreshJitter))
		}
		go func() {
			time.Sleep(delay)
			s.computeAndStore(key, cluster, profile, window, provider)
		}()
	}
	if r.ready == nil {
		p := r.progress
		return &Report{Cluster: cluster, Status: StatusComputing, Profile: profile, Window: windowLabel(window), Progress: &p, Workloads: []WorkloadReport{}}
	}
	out := *r.ready
	out.RefreshError = r.lastError
	if r.computing {
		p := r.progress
		out.Progress = &p
		out.Stale = true
	}
	return &out
}

// Cached returns the last finished report without starting anything.
func (s *Service) Cached(cluster string, profile Profile, window time.Duration, provider ...string) *Report {
	s.mu.Lock()
	r := s.runs[runKey(cluster, profile, window, provider...)]
	if r == nil || r.ready == nil {
		s.mu.Unlock()
		return nil
	}
	out := *r.ready
	s.mu.Unlock()
	return withDismissals(&out, s.dismissalIndex(cluster))
}

func (s *Service) setProgress(key string, p Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.runs[key]; r != nil {
		// A pause notice keeps the counts it interrupts.
		if p.Paused {
			p.Done, p.Total = r.progress.Done, r.progress.Total
		}
		r.progress = p
	}
}

func (s *Service) computeAndStore(key, cluster string, profile Profile, window time.Duration, provider string) {
	ctx, cancel := context.WithTimeout(metrics.WithHistoryProvider(context.Background(), provider), computeTimeout)
	defer cancel()
	start := s.now()
	var prev *Report
	s.mu.Lock()
	// The run this computation belongs to. ForgetCluster replaces it when the
	// metrics settings change under a running computation, whose result then
	// describes the old source and is dropped.
	mine := s.runs[key]
	if mine != nil {
		prev = mine.ready
	}
	s.mu.Unlock()

	rep := func() (rep *Report) {
		// A panic fails this report like any other error: the last good one
		// stays on screen, with the refresh error, and the run can retry.
		defer func() {
			if r := recover(); r != nil {
				err := panicked("computing a report for "+cluster, r)
				rep = &Report{Cluster: cluster, Profile: profile, Window: windowLabel(window), Status: StatusError, Error: err.Error(), Workloads: []WorkloadReport{}}
			}
		}()
		return s.compute(ctx, cluster, profile, window, prev, func(p Progress) {
			s.mu.Lock()
			current := s.runs[key] == mine
			s.mu.Unlock()
			if current {
				s.setProgress(key, p)
			}
		})
	}()
	// The step-by-step explanations are a fifth of a report's size and only
	// the evidence drawer shows them; it computes its own.
	for i := range rep.Workloads {
		for j := range rep.Workloads[i].Containers {
			c := &rep.Workloads[i].Containers[j]
			c.CPU.Explain, c.Memory.Explain = nil, nil
		}
	}
	rep.ComputedAt = s.now()
	// A report reads tens of megabytes of history that is garbage once it's
	// analysed. Hand it back to the OS now rather than over the next minutes,
	// so a laptop isn't left holding a peak it no longer uses.
	defer debug.FreeOSMemory()
	rep.DurationMs = s.now().Sub(start).Milliseconds()
	log.Printf("[Rightsizing] %s %s/%s: %s, %d workloads in %s", cluster, profile, windowLabel(window), rep.Status, len(rep.Workloads), time.Since(start).Round(time.Millisecond))

	s.forget.RLock()
	defer s.forget.RUnlock()
	s.mu.Lock()
	r := s.runs[key]
	if r == nil || r != mine {
		s.mu.Unlock()
		log.Printf("[Rightsizing] %s: metrics settings changed during the computation; dropping its report", cluster)
		return
	}
	r.computing = false
	// A failed refresh keeps the last good report rather than replacing it
	// with an error, and says why the refresh failed.
	r.lastError = ""
	// Only a failure keeps the old report; "no history", "needs a tenant" and
	// "no container data" are answers, and replace it.
	if rep.Status != StatusError || r.ready == nil || r.ready.Status != StatusReady {
		r.ready, r.readyAt = rep, rep.ComputedAt
	} else {
		r.lastError = rep.Error
		if r.lastError == "" {
			r.lastError = rep.Source.Reason
		}
		r.readyAt = rep.ComputedAt // retry after the short TTL, not at once
	}
	s.mu.Unlock()
	if rep.Status == StatusReady && s.store != nil {
		if data, err := packReport(rep); err == nil {
			if err := s.store.SaveRightsizingReport(key, cluster, data); err != nil {
				log.Printf("[Rightsizing] persist report: %v", err)
			}
		}
		// And what cached evidence needs besides history, so that after a
		// restart the drawer still shows the report's numbers at once.
		if rep.evidenceInputs != nil {
			if data, err := packJSON(rep.evidenceInputs.saved(rep.AsOf)); err == nil {
				_ = s.store.SaveRightsizingReport(inputsKey(key), cluster, data)
			}
		}
	}
}

// ForgetCluster drops every report, in memory and on disk, and every signal
// probe read through the given cluster's metrics source. It is called when
// that source's settings change (another Mimir tenant or instance): the
// report key names the provider but not the tenant, so without this a report
// for the old tenant would be served until it expired, and again after a
// restart. Virtual clusters read history through their host, so theirs go
// too.
func (s *Service) ForgetCluster(cluster string) {
	history := resolveTarget(cluster).history
	sameSource := func(c string) bool { return c == cluster || resolveTarget(c).history == history }
	s.forget.Lock()
	defer s.forget.Unlock()
	s.mu.Lock()
	for key, r := range s.runs {
		if sameSource(r.cluster) {
			delete(s.runs, key)
		}
	}
	s.mu.Unlock()
	probeEpoch.Add(1)
	probeCache.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, history+"|") {
			probeCache.Delete(k)
		}
		return true
	})
	if s.store != nil {
		if err := s.store.ForgetRightsizingClusters(sameSource); err != nil {
			log.Printf("[Rightsizing] forget reports for %s: %v", cluster, err)
		}
	}
}

func (s *Service) loadPersisted(key string) *Report {
	if s.store == nil {
		return nil
	}
	data, err := s.store.GetRightsizingReport(key)
	if err != nil || len(data) == 0 {
		return nil
	}
	rep := unpackReport(data)
	if rep != nil && rep.Status == StatusReady {
		rep.evidenceInputs = s.loadInputs(key, rep.AsOf)
	}
	return rep
}

// packReport stores a report gzipped: JSON of thousands of workloads repeats
// the same field names and strings, and shrinks about tenfold.
func packReport(rep *Report) ([]byte, error) { return packJSON(rep) }

func packJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unpackReport(data []byte) *Report {
	var rep Report
	if unpackJSON(data, &rep) != nil {
		return nil
	}
	return &rep
}

// unpackJSON reads what packJSON wrote, or plain JSON as reports were once
// stored.
func unpackJSON(data []byte, v any) error {
	var r io.Reader = bytes.NewReader(data)
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		r = zr
	}
	return json.NewDecoder(r).Decode(v)
}

// ---- Live workloads ---------------------------------------------------------

type liveContainer struct {
	name string
	res  Resources
	jvm  *JVMInfo
}

// filterLabels are the pod labels teams find their workloads by.
var filterLabels = []string{
	"app.kubernetes.io/name", "app.kubernetes.io/instance", "app.kubernetes.io/part-of",
	"app.kubernetes.io/managed-by", "app", "team", "owner",
}

type liveWorkload struct {
	labels        map[string]string
	ref           finops.WorkloadRef
	hostNamespace string
	keys          map[string]map[seriesKey]struct{} // container -> history keys
	containers    []liveContainer
	newest        time.Time
	replicas      int
	nodes         []string
	isJob         bool
	// newestHash is the pod-template-hash of the newest pod: a Deployment's
	// running version, which its pod names carry.
	newestHash string
}

func (w *liveWorkload) id() string {
	return w.ref.Namespace + "/" + w.ref.VClusterNamespace + "/" + w.ref.Kind + "/" + w.ref.Name
}

func containerResources(c v1.Container) Resources {
	return Resources{
		CPURequest: float64(c.Resources.Requests.Cpu().MilliValue()) / 1000,
		CPULimit:   float64(c.Resources.Limits.Cpu().MilliValue()) / 1000,
		MemRequest: float64(c.Resources.Requests.Memory().Value()),
		MemLimit:   float64(c.Resources.Limits.Memory().Value()),
	}
}

// liveWorkloads groups pods into workloads and records, per container, the
// history keys their pods map to. The newest pod's spec is the template the
// recommendation compares against.
func liveWorkloads(pods []*v1.Pod, vclusterScope bool) []*liveWorkload {
	byID := map[string]*liveWorkload{}
	var order []string
	for _, p := range pods {
		if p.DeletionTimestamp != nil {
			continue
		}
		ref := finops.PodWorkload(p, vclusterScope)
		isJob := ref.Kind == "Job" || ref.Kind == "CronJob"
		// Finished pods only matter for a CronJob, whose next run reuses the
		// template. A one-off Job that has finished has nothing left to
		// resize, and its next run, if any, is a new Job.
		if ref.Kind != "CronJob" && (p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed) {
			continue
		}
		w := &liveWorkload{ref: ref, hostNamespace: p.Namespace, keys: map[string]map[seriesKey]struct{}{}, isJob: isJob}
		if existing := byID[w.id()]; existing != nil {
			w = existing
		} else {
			byID[w.id()] = w
			order = append(order, w.id())
		}
		if p.Status.Phase == v1.PodRunning || p.Status.Phase == v1.PodPending {
			w.replicas++
		}
		if p.Spec.NodeName != "" {
			w.nodes = append(w.nodes, p.Spec.NodeName)
		}
		vns, wk := workloadKey(p.Name)
		var cs []liveContainer
		for _, c := range p.Spec.InitContainers {
			if c.RestartPolicy != nil && *c.RestartPolicy == v1.ContainerRestartPolicyAlways {
				cs = append(cs, liveContainer{c.Name, containerResources(c), jvmOf(c)})
			}
		}
		for _, c := range p.Spec.Containers {
			cs = append(cs, liveContainer{c.Name, containerResources(c), jvmOf(c)})
		}
		for _, c := range cs {
			if w.keys[c.name] == nil {
				w.keys[c.name] = map[seriesKey]struct{}{}
			}
			w.keys[c.name][seriesKey{p.Namespace, vns, wk, c.name}] = struct{}{}
		}
		hash := p.Labels["pod-template-hash"]
		if hash == "" {
			hash = p.Labels["rollouts-pod-template-hash"] // an Argo Rollout's pod
		}
		if t := p.CreationTimestamp.Time; w.containers == nil || t.After(w.newest) {
			w.containers, w.newest, w.newestHash = cs, t, hash
			w.labels = map[string]string{}
			for _, k := range filterLabels {
				if v := p.Labels[k]; v != "" {
					w.labels[k] = v
				}
			}
		}
	}
	out := make([]*liveWorkload, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

// hpaTargets maps namespace/kind/name to the resource an HPA scales on, when
// it scales on CPU or memory utilisation.
func hpaTargets(hpas []autoscalingv2.HorizontalPodAutoscaler) map[string]*hpaTarget {
	out := map[string]*hpaTarget{}
	for _, h := range hpas {
		var t *hpaTarget
		for _, m := range h.Spec.Metrics {
			var name v1.ResourceName
			var target autoscalingv2.MetricTarget
			switch {
			case m.Type == autoscalingv2.ResourceMetricSourceType && m.Resource != nil:
				name, target = m.Resource.Name, m.Resource.Target
			case m.Type == autoscalingv2.ContainerResourceMetricSourceType && m.ContainerResource != nil:
				name, target = m.ContainerResource.Name, m.ContainerResource.Target
			default:
				continue
			}
			if target.Type != autoscalingv2.UtilizationMetricType || target.AverageUtilization == nil {
				continue
			}
			if name == v1.ResourceCPU || (name == v1.ResourceMemory && t == nil) {
				t = &hpaTarget{resource: string(name), utilization: *target.AverageUtilization, name: h.Name}
			}
		}
		if t != nil {
			out[h.Namespace+"/"+h.Spec.ScaleTargetRef.Kind+"/"+h.Spec.ScaleTargetRef.Name] = t
		}
	}
	return out
}

func listHPAs(ctx context.Context, cs kubernetes.Interface) []autoscalingv2.HorizontalPodAutoscaler {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, err := cs.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

// ---- Computation -------------------------------------------------------------

// target is the cluster a report reads from: the host for a vcluster, whose
// pods are real pods in one host namespace.
type target struct {
	cluster       string // what the user selected
	history       string // where pods and metrics live
	hostNamespace string // vcluster only
	vcluster      string
}

func resolveTarget(cluster string) target {
	if host, ns, name, ok := k8s.ParseVClusterID(cluster); ok {
		return target{cluster: cluster, history: host, hostNamespace: ns, vcluster: name}
	}
	return target{cluster: cluster, history: cluster}
}

func (s *Service) listPods(ctx context.Context, t target) ([]*v1.Pod, error) {
	var pods []*v1.Pod
	if s.pods != nil {
		l, err := s.pods.List(ctx, t.history)
		if err != nil {
			return nil, err
		}
		pods = l
	} else {
		cs, err := s.k8s.GetClientForCluster(t.history)
		if err != nil {
			return nil, err
		}
		l, err := cs.CoreV1().Pods(t.hostNamespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range l.Items {
			pods = append(pods, &l.Items[i])
		}
	}
	if t.vcluster == "" {
		return pods, nil
	}
	out := pods[:0:0]
	for _, p := range pods {
		if p.Namespace == t.hostNamespace && p.Labels[k8s.VClusterManagedByLabel] == t.vcluster {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Service) compute(ctx context.Context, cluster string, profile Profile, window time.Duration, prev *Report, progress func(Progress)) *Report {
	t := resolveTarget(cluster)
	step := stepFor(window)
	asOf := s.now()
	g := newGrid(asOf, window, step)
	rep := &Report{
		Cluster:   cluster,
		Profile:   profile,
		Window:    windowLabel(window),
		Step:      fmtStep(step),
		AsOf:      g.end,
		Workloads: []WorkloadReport{},
	}

	info, metricsServer := s.metrics.HistorySource(ctx, t.history)
	rep.Source = SourceInfo{MetricsServer: metricsServer}
	if info != nil {
		rep.Source.Type, rep.Source.Flavor, rep.Source.Namespace, rep.Source.Service, rep.Source.Reason = info.Type, info.Flavor, info.Namespace, info.Service, info.Reason
	}
	switch {
	case info != nil && !info.Found && strings.HasPrefix(info.Reason, "detection failed"):
		// Couldn't look (expired credentials, unreachable API server): saying
		// "no Prometheus" here would send the user to the wrong fix.
		rep.Status, rep.Error = StatusError, strings.TrimPrefix(info.Reason, "detection failed: ")
		return rep
	case info == nil || !info.Found:
		rep.Status = StatusNoHistorySource
		return rep
	case info.NeedsTenant:
		rep.Status = StatusNeedsTenant
		return rep
	}

	pods, err := s.listPods(ctx, t)
	if err != nil {
		rep.Status, rep.Error = StatusError, "list pods: "+err.Error()
		return rep
	}
	workloads := liveWorkloads(pods, t.vcluster != "")

	progress(Progress{Stage: "Checking which signals the metrics store has"})
	// Every query goes through the cluster's adaptive limiter; range queries
	// also through the day-chunk cache, above it, so a cached day takes no
	// slot.
	ctx = s.withSourceOf(ctx, t.history)
	cq := newControlled(safeQuerier{s.metrics})
	ch := newChunker(cq, s.chunks())
	// Every query sees the same days finished or in progress, as of the
	// report's own clock, so cached evidence can read them back the same way.
	ch.now = func() time.Time { return asOf }
	ch.tails, ch.run = true, runKey(cluster, profile, window, metrics.HistoryProvider(ctx))
	defer func() {
		st := limiterFor(t.history).state()
		log.Printf("[Rightsizing] %s: %d queries to the metrics store, %d day chunks from cache, %d fetched, limit %.1f (grew %d, queue cuts %d, overload cuts %d, max latency ratio %.1f)",
			cluster, cq.count(), ch.hits.Load(), ch.misses.Load(), st.Limit, st.Grows, st.QueueCuts, st.OverloadCuts, st.MaxRatio)
		if n := ch.partials.Load(); n > 0 {
			log.Printf("[Rightsizing] %s: the metrics store answered %d day(s) only in part; used for this report, not cached", cluster, n)
		}
		if p, ok := s.store.(interface{ PruneRightsizingChunks() }); ok && s.store != nil {
			p.PruneRightsizingChunks()
		}
	}()
	stopWatch := watchPause(t.history, progress)
	defer stopWatch()
	epoch := probeEpoch.Load()
	pr := probeSignals(ctx, cq, t.history, g.end)
	fresh := pr.err == nil
	if !fresh {
		last, ok := lastProbe(probeKey(ctx, t.history), prev)
		if !ok {
			rep.Status, rep.Error = StatusError, "could not check which signals the metrics store has: "+trimErr(pr.err)
			return rep
		}
		log.Printf("[Rightsizing] %s: probing signals failed, using the last probe: %v", cluster, pr.err)
		pr = last
	}
	rep.Signals = signalsOf(pr)
	if pr.cpuSeries == 0 {
		rep.Status = StatusNoContainerData
		return rep
	}

	var hpas map[string]*hpaTarget
	if cs, err := s.k8s.GetClientForCluster(cluster); err == nil {
		hpas = hpaTargets(listHPAs(ctx, cs))
		pr.inPlace = supportsInPlaceResize(cs)
	}
	rates, _ := s.rates.NodeRates(ctx, cluster)
	rep.evidenceInputs = &evidenceInputs{workloads: workloads, probe: pr, hpas: hpas, rates: rates, source: historySource(ctx), now: asOf}
	if fresh {
		storeProbe(probeKey(ctx, t.history), probeEntry{pr, s.now()}, epoch)
	}
	dismissals := s.dismissalIndex(cluster)

	// Each namespace's workloads, and the history keys (and those of Jobs)
	// they are read for.
	byNS := map[string][]*liveWorkload{}
	wantNS, jobsNS := map[string]map[seriesKey]struct{}{}, map[string]map[seriesKey]struct{}{}
	for _, w := range workloads {
		ns := w.hostNamespace
		if byNS[ns] == nil {
			wantNS[ns], jobsNS[ns] = map[seriesKey]struct{}{}, map[seriesKey]struct{}{}
		}
		byNS[ns] = append(byNS[ns], w)
		for _, keys := range w.keys {
			maps.Copy(wantNS[ns], keys)
			if w.isJob {
				maps.Copy(jobsNS[ns], keys)
			}
		}
	}
	// The namespaces with the most history go first: only a few are read at
	// once, and a large one left for last keeps the whole report waiting.
	namespaces := slices.Collect(maps.Keys(byNS))
	slices.SortFunc(namespaces, func(a, b string) int {
		if c := cmp.Compare(len(wantNS[b]), len(wantNS[a])); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})

	prevRecs := previousRecs(prev)
	var (
		mu      sync.Mutex
		done    int
		results []WorkloadReport
		errs    []string
	)
	sem := make(chan struct{}, namespaceConcurrency)
	var wg sync.WaitGroup
	progress(Progress{Done: 0, Total: len(namespaces), Stage: "Reading usage history"})
	for _, ns := range namespaces {
		// Taken here, so namespaces start in the order above.
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var out []WorkloadReport
			err := func() (err error) {
				// A panic fails this namespace alone; the previous report's
				// results for it are carried over like for any failure.
				defer func() {
					if r := recover(); r != nil {
						out, err = nil, panicked("analysing namespace "+ns, r)
					}
				}()
				h, err := fetchHistory(ctx, ch, t.history, scope{namespace: ns, want: wantNS[ns], jobs: jobsNS[ns]}, g, pr, window)
				if err != nil {
					return err
				}
				rep.evidenceInputs.setStarts(ns, h.starts)
				for _, w := range byNS[ns] {
					if wr := s.analyzeWorkload(cluster, w, h, g, pr, profile, hpas, rates, prevRecs); wr != nil {
						out = append(out, *wr)
					}
				}
				return nil
			}()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, ns+": "+trimErr(err))
			}
			results = append(results, out...)
			done++
			progress(Progress{Done: done, Total: len(namespaces), Stage: "Reading usage history"})
		}()
	}
	wg.Wait()

	if len(results) == 0 && len(errs) > 0 {
		rep.Status, rep.Error = StatusError, strings.Join(errs[:min(len(errs), 3)], "; ")
		return rep
	}
	if len(errs) > 0 {
		// Keep the previous results for namespaces that failed this time, so
		// one flaky query doesn't silently shrink the savings. Host scope
		// only: there a report's namespace is the host namespace queried.
		carried := 0
		if prev != nil && prev.Status == StatusReady && t.vcluster == "" {
			failedNS := map[string]bool{}
			for _, e := range errs {
				failedNS[strings.SplitN(e, ":", 2)[0]] = true
			}
			for _, w := range prev.Workloads {
				if failedNS[w.Namespace] {
					results = append(results, w)
					carried++
				}
			}
		}
		rep.Error = fmt.Sprintf("%d namespace(s) could not be read: %s", len(errs), strings.Join(errs[:min(len(errs), 3)], "; "))
		if carried > 0 {
			rep.Error += fmt.Sprintf(". Showing their previous results (%d workloads) until the next refresh.", carried)
		}
	}
	for i := range results {
		applyDismissals(&results[i], dismissals)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].RiskScore != results[j].RiskScore {
			return results[i].RiskScore > results[j].RiskScore
		}
		return results[i].Namespace+"/"+results[i].Name < results[j].Namespace+"/"+results[j].Name
	})
	rep.Workloads = results
	rep.Calibration = calibrate(results, profile)
	rep.Summary = summarize(results)
	rep.Status = StatusReady
	return rep
}

func signalsOf(pr probe) Signals {
	s := Signals{OOMKills: pr.ksm, Throttling: pr.throttle != throttleNone, StartupExclusion: pr.startTime, RequestHistory: pr.requests, ThrottleKind: pr.throttle, MemoryMetric: "working-set"}
	if pr.memMetric == memUsage {
		s.MemoryMetric = "usage"
	}
	return s
}

func trimErr(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

// panicked reports a panic recovered in a background computation and turns
// it into an error. Reports run outside any request handler's recovery, and
// analyse whatever a metrics store returns: a bug there must fail the report
// or the namespace, not take the whole backend down with it.
func panicked(where string, r any) error {
	log.Printf("[PANIC] rightsizing, %s: %v", where, r)
	faults.CaptureExceptionWithContext(
		fmt.Errorf("panic in rightsizing, %s: %v", where, r),
		map[string]any{"panic": r, "stack": string(debug.Stack())},
	)
	return fmt.Errorf("internal error %s: %v", where, r)
}

func seedFor(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// containerSeries is one live container's history, merged across every key
// its pods map to.
type containerSeries struct {
	cpu            *pooled
	podMeans       []float64
	mem, throttle  []float64
	burst          []float64
	podFirst       map[string]int64 // pod name -> first CPU sample (unix)
	oom, restarts  []time.Time
	startup        float64
	cpuReq, memReq []float64
}

// supportsInPlaceResize says whether the cluster resizes running pods in
// place: on by default from Kubernetes 1.33 (beta), stable in 1.35. A
// startup boost needs it.
func supportsInPlaceResize(cs kubernetes.Interface) bool {
	v, err := cs.Discovery().ServerVersion()
	if err != nil {
		return false
	}
	major, _ := strconv.Atoi(strings.TrimRight(v.Major, "+"))
	minor, _ := strconv.Atoi(strings.TrimRight(v.Minor, "+"))
	return major > 1 || (major == 1 && minor >= 33)
}

// workloadSelector picks one pod label that identifies a workload, for
// configs that select its pods.
func workloadSelector(labels map[string]string) map[string]string {
	for _, k := range []string{"app.kubernetes.io/instance", "app.kubernetes.io/name", "app"} {
		if v := labels[k]; v != "" {
			return map[string]string{k: v}
		}
	}
	return nil
}

// versionSince is when the running version first reported usage: the first
// sample from any pod whose name carries the newest pod-template-hash. Pod
// creation times can't tell this apart from pods merely replaced on new
// nodes; the history can. Zero when unknown (no hash, or not seen).
func versionSince(hash string, podFirst map[string]int64) time.Time {
	if hash == "" {
		return time.Time{}
	}
	first := int64(math.MaxInt64)
	for pod, t := range podFirst {
		if carriesHash(pod, hash) {
			first = min(first, t)
		}
	}
	if first == math.MaxInt64 {
		return time.Time{}
	}
	return time.Unix(first, 0).UTC()
}

// carriesHash says whether a pod name carries a pod-template-hash: whole, as
// <name>-<hash>-<random>, or cut short when the API server truncated the
// name to 63 characters, as <name>-<start of hash><random>. At least four
// characters of the hash must be left to tell versions apart.
func carriesHash(pod, hash string) bool {
	if strings.Contains(pod, "-"+hash+"-") {
		return true
	}
	if len(pod) != 63 {
		return false
	}
	i := strings.LastIndexByte(pod, '-')
	if i < 0 || len(pod)-i-1-5 < 4 {
		return false
	}
	return strings.HasPrefix(hash, pod[i+1:len(pod)-5])
}

// containerHistory merges the series of every key a container's pods map to.
func containerHistory(h *history, keys map[seriesKey]struct{}, n int) containerSeries {
	cs := containerSeries{startup: math.NaN()}
	ks := make([]seriesKey, 0, len(keys))
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].wk < ks[j].wk })
	var cpus []*pooled
	for _, k := range ks {
		if v, ok := h.cpu[k]; ok {
			cpus = append(cpus, v)
		}
		cs.podMeans = append(cs.podMeans, h.podMeans[k]...)
		if v, ok := h.mem[k]; ok {
			cs.mem = mergeSeries(cs.mem, v, maxF)
		}
		if v, ok := h.throttle[k]; ok {
			cs.throttle = mergeSeries(cs.throttle, v, maxF)
		}
		if v, ok := h.burst[k]; ok {
			cs.burst = mergeSeries(cs.burst, v, maxF)
		}
		if v, ok := h.cpuReq[k]; ok {
			cs.cpuReq = mergeSeries(cs.cpuReq, v, maxF)
		}
		if v, ok := h.memReq[k]; ok {
			cs.memReq = mergeSeries(cs.memReq, v, maxF)
		}
		cs.oom = append(cs.oom, h.oom[k]...)
		cs.restarts = append(cs.restarts, h.restarts[k]...)
		if v, ok := h.startupPeak[k]; ok {
			cs.startup = math.Max(nanTo(cs.startup, 0), v)
		}
	}
	cs.cpu = mergePooled(n, cpus)
	for _, k := range ks {
		for pod, t := range h.podFirst[k] {
			if cs.podFirst == nil {
				cs.podFirst = map[string]int64{}
			}
			if old, ok := cs.podFirst[pod]; !ok || t < old {
				cs.podFirst[pod] = t
			}
		}
	}
	if cs.mem == nil {
		cs.mem = make([]float64, n)
		for i := range cs.mem {
			cs.mem[i] = math.NaN()
		}
	}
	return cs
}

func nanTo(v, def float64) float64 {
	if math.IsNaN(v) {
		return def
	}
	return v
}

type recKey struct{ ns, vns, kind, name, container string }

type prevRec struct {
	cpu, mem float64
	at       time.Time
	// change is the request change the previous report saw for this
	// workload, so "you followed the advice" survives later refreshes.
	change *Change
}

func previousRecs(prev *Report) map[recKey]prevRec {
	out := map[recKey]prevRec{}
	if prev == nil {
		return out
	}
	for _, w := range prev.Workloads {
		for _, c := range w.Containers {
			out[recKey{w.Namespace, w.VClusterNamespace, w.Kind, w.Name, c.Container}] = prevRec{c.CPU.Recommended, c.Memory.Recommended, prev.ComputedAt, w.Change}
		}
	}
	return out
}

func (s *Service) analyzeWorkload(cluster string, w *liveWorkload, h *history, g grid, pr probe, profile Profile, hpas map[string]*hpaTarget, rates map[string]finops.Rates, prevRecs map[recKey]prevRec) *WorkloadReport {
	wr := &WorkloadReport{
		Namespace:         w.ref.Namespace,
		Kind:              w.ref.Kind,
		Name:              w.ref.Name,
		VCluster:          w.ref.VCluster,
		VClusterNamespace: w.ref.VClusterNamespace,
		Replicas:          w.replicas,
		Containers:        []ContainerReport{},
		Labels:            w.labels,
	}
	hpa := hpaFor(hpas, w)
	cpuRate, memRate, priced := workloadRates(w.nodes, rates)
	wr.Priced = priced
	for _, c := range w.containers {
		hs := containerHistory(h, w.keys[c.name], g.n)
		in := containerInput{
			container: c.name, start: g.start, step: g.step,
			cpu: hs.cpu, podMeans: hs.podMeans, mem: hs.mem, throttle: hs.throttle, cpuBurst: hs.burst,
			throttleKind: pr.throttle, memIsUsage: pr.memMetric == memUsage,
			oomTimes: hs.oom, restartTimes: hs.restarts, startupCPUPeak: hs.startup,
			current: c.res, isJob: w.isJob, hpa: hpa, profile: profile,
			seed: seedFor(cluster, w.id(), c.name), jvm: c.jvm, versionSince: versionSince(w.newestHash, hs.podFirst),
			inPlaceResize: pr.inPlace,
		}
		cr := analyze(in)
		if cr.StartupBoost != nil {
			cr.StartupBoost.Selector = workloadSelector(w.labels)
		}
		if priced {
			reps := cr.AvgReplicas
			if reps == 0 {
				reps = float64(w.replicas)
			}
			cr.CPUMonthly = cpuRate * hoursPerMonth * reps
			cr.MemMonthly = memRate * hoursPerMonth * reps
		}
		// Money: what the change in requests is worth over a month at the
		// average replica count. A resource left as it is (no data, or within
		// noise) recommends today's value, so it adds nothing here.
		if priced && cr.Verdict != VerdictInsufficient && cr.Verdict != VerdictRight {
			cost := func(cpuCores, memBytes float64) float64 {
				return cpuCores*cr.CPUMonthly + memBytes/gib*cr.MemMonthly
			}
			now := cost(c.res.CPURequest, c.res.MemRequest)
			cr.MonthlySavings = now - cost(cr.CPU.Recommended, cr.Memory.Recommended)
			wr.MonthlyCost += now
			wr.MonthlySavings += cr.MonthlySavings
			// The range comes from the ends of the need's interval, never
			// crossing the recommendation itself (rounding can put it past one).
			// Resources kept as they are stay put at both ends.
			cpuHi, cpuLo := cr.CPU.Recommended, cr.CPU.Recommended
			if cr.CPU.Recommended != c.res.CPURequest {
				cpuHi = max(cr.CPU.Recommended, roundCPU(nanTo(cr.CPU.High, cr.CPU.Recommended)))
				cpuLo = min(cr.CPU.Recommended, roundCPU(nanTo(cr.CPU.Low, cr.CPU.Recommended)))
			}
			memHi, memLo := cr.Memory.Recommended, cr.Memory.Recommended
			if cr.Memory.Recommended != c.res.MemRequest {
				memHi = max(cr.Memory.Recommended, roundMem(cr.Memory.High))
				memLo = min(cr.Memory.Recommended, roundMem(cr.Memory.Low))
			}
			wr.SavingsLow += now - cost(cpuHi, memHi)
			wr.SavingsHigh += now - cost(cpuLo, memLo)
		}
		wr.Containers = append(wr.Containers, cr)
		if ch := detectChange(in, cr, hs.cpuReq, hs.memReq, h.requestsGrid, profile, prevRecs[recKey{w.ref.Namespace, w.ref.VClusterNamespace, w.ref.Kind, w.ref.Name, c.name}]); ch != nil {
			if wr.Change == nil || ch.At.After(wr.Change.At) {
				wr.Change = ch
			}
		}
	}
	if len(wr.Containers) == 0 {
		return nil
	}
	wr.Verdict, wr.Confidence = rollUp(wr.Containers)
	wr.RiskScore = riskScore(wr)
	return wr
}

// workloadRates averages the per-resource rates of the nodes a workload's
// pods run on.
func workloadRates(nodes []string, rates map[string]finops.Rates) (cpu, mem float64, priced bool) {
	var n float64
	for _, node := range nodes {
		if r, ok := rates[node]; ok && r.Priced {
			cpu += r.CPU
			mem += r.Memory
			n++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return cpu / n, mem / n, true
}

var verdictOrder = []Verdict{VerdictUnder, VerdictNoRequests, VerdictOver, VerdictHPACoupled, VerdictRight, VerdictInsufficient}

func rollUp(cs []ContainerReport) (Verdict, string) {
	best := len(verdictOrder)
	conf := "high"
	rank := map[string]int{"low": 0, "medium": 1, "high": 2}
	for _, c := range cs {
		if i := slices.Index(verdictOrder, c.Verdict); i >= 0 && i < best {
			best = i
		}
		if c.Verdict != VerdictInsufficient && rank[c.Confidence] < rank[conf] {
			conf = c.Confidence
		}
	}
	if best == len(verdictOrder) {
		return VerdictInsufficient, "low"
	}
	if verdictOrder[best] == VerdictInsufficient {
		conf = "low"
	}
	return verdictOrder[best], conf
}

// riskScore orders the triage list: reliability risks first (OOM kills and
// throttling above warnings), then money.
func riskScore(w *WorkloadReport) float64 {
	sev := 0.0
	for _, c := range w.Containers {
		for _, f := range c.Findings {
			switch f.Severity {
			case "critical":
				sev = max(sev, 4)
			case "warning":
				if c.Verdict == VerdictUnder {
					sev = max(sev, 3)
				}
			}
		}
	}
	switch w.Verdict {
	case VerdictUnder:
		sev = max(sev, 3)
	case VerdictNoRequests:
		sev = max(sev, 2)
	case VerdictOver:
		sev = max(sev, 1)
	case VerdictInsufficient:
		sev = -1
	}
	return sev*1e6 + math.Abs(w.MonthlySavings)
}

func calibrate(ws []WorkloadReport, profile Profile) Calibration {
	c := Calibration{CPUTarget: 1 - profile.params().cpuQuantile}
	var ex []float64
	for _, w := range ws {
		for _, cr := range w.Containers {
			if cr.Backtest == nil {
				continue
			}
			c.Containers++
			if cr.Backtest.Calibrated {
				c.Calibrated++
			}
			if cr.Backtest.MemBreached {
				c.MemBreaches++
			}
			ex = append(ex, cr.Backtest.CPUExceedance)
		}
	}
	if len(ex) > 0 {
		c.MedianCPUExceedance = median(ex)
	}
	return c
}

func summarize(ws []WorkloadReport) Summary {
	var s Summary
	for _, w := range ws {
		s.Workloads++
		if len(w.Dismissed) > 0 && dismissedWhole(w) {
			s.Dismissed++
			continue
		}
		switch w.Verdict {
		case VerdictOver:
			s.Over++
		case VerdictUnder:
			s.Under++
		case VerdictRight:
			s.Right++
		case VerdictInsufficient:
			s.Insufficient++
		case VerdictHPACoupled:
			s.HPACoupled++
		case VerdictNoRequests:
			s.NoRequests++
		}
		// One definition everywhere (summary, rows, FinOps): savings are the
		// workloads whose recommended requests cost less than today's.
		if w.MonthlySavings > 0 {
			s.Shrinking++
			s.MonthlySavings += w.MonthlySavings
			s.SavingsLow += max(w.SavingsLow, 0)
			s.SavingsHigh += max(w.SavingsHigh, 0)
		} else {
			s.AddedCost += -w.MonthlySavings
		}
	}
	return s
}

// detectChange finds the latest request change in the window and judges how
// the container has done since.
func detectChange(in containerInput, cr ContainerReport, cpuReq, memReq []float64, rg grid, profile Profile, prev prevRec) *Change {
	find := func(series []float64) (int, float64, float64) {
		last, lastVal := -1, math.NaN()
		at, from, to := -1, 0.0, 0.0
		for i, v := range series {
			if math.IsNaN(v) {
				continue
			}
			if last >= 0 && math.Abs(v-lastVal) > 0.01*math.Max(lastVal, 1e-9) {
				at, from, to = i, lastVal, v
			}
			last, lastVal = i, v
		}
		return at, from, to
	}
	ci, cFrom, cTo := find(cpuReq)
	mi, mFrom, mTo := find(memReq)
	if ci < 0 && mi < 0 {
		return nil
	}
	ch := &Change{}
	if ci >= mi {
		ch.Resource, ch.From, ch.To = "cpu", cFrom, cTo
		ch.At = rg.start.Add(time.Duration(ci) * rg.step)
	} else {
		ch.Resource, ch.From, ch.To = "memory", mFrom, mTo
		ch.At = rg.start.Add(time.Duration(mi) * rg.step)
	}
	ch.DaysObserved = rg.end.Sub(ch.At).Hours() / 24
	if ch.DaysObserved < 0.25 {
		return nil // too recent to say anything
	}
	in.prepare()
	from := int(ch.At.Sub(in.start) / in.step)
	from = min(max(from, 0), len(in.mem))
	cpuAfter := in.cpu.span(from, in.cpu.steps())
	var memAfter []float64
	for i := from; i < len(in.mem); i++ {
		if !math.IsNaN(in.mem[i]) {
			memAfter = append(memAfter, in.mem[i])
		}
	}
	for _, t := range in.oomTimes {
		if !t.Before(ch.At) {
			ch.OOMKillsAfter++
		}
	}
	if len(memAfter) > 0 {
		ch.PeakAfter = maxOf(memAfter)
	}
	target := 1 - profile.params().cpuQuantile
	if ch.Resource == "cpu" {
		ch.TimeAboveAfter = fractionAbove(sortedCopy(cpuAfter), ch.To)
		ch.Healthy = ch.OOMKillsAfter == 0 && ch.TimeAboveAfter <= 2*target
	} else {
		ch.TimeAboveAfter = fractionAbove(sortedCopy(memAfter), ch.To)
		limit := in.current.MemLimit
		ch.Healthy = ch.OOMKillsAfter == 0 && (limit == 0 || ch.PeakAfter <= memLimitPressure*limit)
	}
	switch {
	case prev.change != nil && prev.change.At.Equal(ch.At) && prev.change.Resource == ch.Resource:
		ch.FollowedAdvice = prev.change.FollowedAdvice
	case prev.at.Before(ch.At) && !prev.at.IsZero():
		want := prev.cpu
		if ch.Resource == "memory" {
			want = prev.mem
		}
		ch.FollowedAdvice = want > 0 && math.Abs(ch.To-want)/want <= 0.15
	}
	fmtV := fmtCores
	if ch.Resource == "memory" {
		fmtV = fmtBytes
	}
	since := fmt.Sprintf("%.0f days", ch.DaysObserved)
	if ch.DaysObserved < 1.5 {
		since = fmt.Sprintf("%.0f hours", ch.DaysObserved*24)
	}
	verdict := "holding up"
	switch {
	case ch.OOMKillsAfter > 0:
		verdict = fmt.Sprintf("OOM-killed %s since", times(ch.OOMKillsAfter))
	case !ch.Healthy:
		verdict = fmt.Sprintf("over the new request %.1f%% of the time", ch.TimeAboveAfter*100)
	default:
		verdict = fmt.Sprintf("holding up: over the new request %.1f%% of the time, no OOM kills", ch.TimeAboveAfter*100)
	}
	name := "CPU"
	if ch.Resource == "memory" {
		name = "Memory"
	}
	ch.Summary = fmt.Sprintf("%s request changed %s → %s %s ago, %s.", name, fmtV(ch.From), fmtV(ch.To), since, verdict)
	return ch
}

// ---- Dismissals -------------------------------------------------------------

func (s *Service) dismissalIndex(cluster string) map[string][]Dismissal {
	return dismissalIndexOf(s.dismissalList(cluster))
}

func (s *Service) dismissalList(cluster string) []db.RightsizingDismissal {
	if s.store == nil {
		return nil
	}
	list, err := s.store.ListRightsizingDismissals(cluster)
	if err != nil {
		return nil
	}
	return list
}

func dismissalIndexOf(list []db.RightsizingDismissal) map[string][]Dismissal {
	out := map[string][]Dismissal{}
	for _, d := range list {
		var until *time.Time
		if d.Until > 0 {
			t := time.Unix(d.Until, 0)
			until = &t
		}
		k := d.Namespace + "/" + d.VClusterNamespace + "/" + d.Kind + "/" + d.Name
		out[k] = append(out[k], Dismissal{Container: d.Container, Reason: d.Reason, Until: until, CreatedAt: time.Unix(d.CreatedAt, 0)})
	}
	return out
}

func applyDismissals(w *WorkloadReport, idx map[string][]Dismissal) {
	w.Dismissed = idx[w.Namespace+"/"+w.VClusterNamespace+"/"+w.Kind+"/"+w.Name]
}

func dismissedWhole(w WorkloadReport) bool {
	for _, d := range w.Dismissed {
		if d.Container == "" {
			return true
		}
	}
	covered := 0
	for _, c := range w.Containers {
		for _, d := range w.Dismissed {
			if d.Container == c.Container {
				covered++
				break
			}
		}
	}
	return covered == len(w.Containers)
}

// withDismissals re-applies the current dismissals to a cached report so a
// dismissal shows immediately, without recomputing.
func withDismissals(r *Report, idx map[string][]Dismissal) *Report {
	if r.Status != StatusReady {
		return r
	}
	ws := make([]WorkloadReport, len(r.Workloads))
	copy(ws, r.Workloads)
	for i := range ws {
		applyDismissals(&ws[i], idx)
	}
	r.Workloads = ws
	r.Summary = summarize(ws)
	return r
}

type DismissRequest struct {
	Cluster           string `json:"cluster"`
	Namespace         string `json:"namespace"`
	VClusterNamespace string `json:"vclusterNamespace"`
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	Container         string `json:"container"`
	Reason            string `json:"reason"`
	// SnoozeDays hides it for that many days; 0 is permanent.
	SnoozeDays int `json:"snoozeDays"`
}

var errNoStore = errors.New("dismissals need the local database, which is unavailable")

func (s *Service) Dismiss(req DismissRequest) error {
	if s.store == nil {
		return errNoStore
	}
	d := &db.RightsizingDismissal{Cluster: req.Cluster, Namespace: req.Namespace, VClusterNamespace: req.VClusterNamespace, Kind: req.Kind, Name: req.Name, Container: req.Container, Reason: req.Reason}
	if req.SnoozeDays > 0 {
		d.Until = s.now().Add(time.Duration(req.SnoozeDays) * day).Unix()
	}
	return s.store.SaveRightsizingDismissal(d)
}

func (s *Service) Undismiss(req DismissRequest) error {
	if s.store == nil {
		return errNoStore
	}
	return s.store.DeleteRightsizingDismissal(req.Cluster, req.Namespace, req.VClusterNamespace, req.Kind, req.Name, req.Container)
}

// chunks is the day-chunk cache, or nil without a database.
func (s *Service) chunks() chunkStore {
	if s.store == nil {
		return nil
	}
	if c, ok := s.store.(chunkStore); ok {
		return c
	}
	return nil
}

// watchPause keeps the report's progress line honest while the circuit
// breaker holds queries back: the user sees why nothing is moving.
func watchPause(cluster string, progress func(Progress)) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if st := limiterFor(cluster).state(); st.PausedFor > 0 {
					progress(Progress{Stage: busyMessage(st), Paused: true})
				}
			}
		}
	}()
	return func() { close(done) }
}
