package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/podcache"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

const clusterInfoTTL = 10 * time.Minute

// maxDashboardEvents is how many recent events the dashboard shows.
const maxDashboardEvents = 20

// RecentEventsFunc returns a cluster's n most recent events from a store kept
// current by a watch, or false while that store cannot vouch for them yet.
type RecentEventsFunc func(cluster string, n int) ([]db.K8sEvent, bool)

type DashboardHandler struct {
	k8sClient k8s.Interface
	hub       *core.Hub
	// pods, when set, serves pods from a shared watch instead of listing
	// every pod in the cluster on each refresh.
	pods podcache.Lister
	// recentEvents, when set, serves the recent events panel instead of a
	// limited LIST, whose page holds the first events in key order
	// (namespace/name), not the newest.
	recentEvents  RecentEventsFunc
	mu            sync.RWMutex
	watchers      map[string]*dashboardWatcher
	infoMu        sync.Mutex
	info          map[string]clusterInfoEntry
	metricsProbes map[string]metricsProbeEntry
}

type clusterInfoEntry struct {
	version, platform, provider, arch string
	at                                time.Time
}

type metricsProbeEntry struct {
	available bool
	at        time.Time
}

type dashboardWatcher struct {
	refreshChan chan struct{}
	cluster     string
	cancelFunc  context.CancelFunc
	stopChan    chan struct{}
}

type DashboardMetrics struct {
	ResourceCounts   map[string]int         `json:"resourceCounts"`
	PodStatus        *PodStatusMetrics      `json:"podStatus"`
	NodeStatus       *NodeStatusMetrics     `json:"nodeStatus"`
	WorkloadStatus   *WorkloadStatusMetrics `json:"workloadStatus"`
	Events           []DashboardEvent       `json:"events"`
	ClusterInfo      ClusterInfo            `json:"clusterInfo"`
	MetricsAvailable bool                   `json:"metricsAvailable"`
	CriticalAlerts   []CriticalAlert        `json:"criticalAlerts"`
	ResourceCapacity *ResourceCapacity      `json:"resourceCapacity"`
}

type PodStatusMetrics struct {
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Failed    int `json:"failed"`
	Succeeded int `json:"succeeded"`
	Unknown   int `json:"unknown"`
}

type NodeStatusMetrics struct {
	Ready         int `json:"ready"`
	NotReady      int `json:"notReady"`
	Schedulable   int `json:"schedulable"`
	Unschedulable int `json:"unschedulable"`
}

type WorkloadStatusMetrics struct {
	Deployments  WorkloadHealth `json:"deployments"`
	StatefulSets WorkloadHealth `json:"statefulSets"`
	DaemonSets   WorkloadHealth `json:"daemonSets"`
}

type WorkloadHealth struct {
	Healthy int `json:"healthy"`
	Total   int `json:"total"`
}

type DashboardEvent struct {
	Time      string `json:"time"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Object    string `json:"object"`
	Namespace string `json:"namespace,omitempty"`
}

type ClusterInfo struct {
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	Provider     string `json:"provider"`
	Architecture string `json:"architecture"`
	NodeCount    int    `json:"nodeCount"`
}

type CriticalAlert struct {
	Severity  string `json:"severity"`
	Type      string `json:"type"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Age       string `json:"age"`
}

type ResourceCapacity struct {
	CPU    CapacityMetric `json:"cpu"`
	Memory CapacityMetric `json:"memory"`
}

type CapacityMetric struct {
	Requested   int64   `json:"requested"`
	Allocatable int64   `json:"allocatable"`
	Percentage  float64 `json:"percentage"`
	Unit        string  `json:"unit"`
}

type DashboardMessage struct {
	core.BaseMessage
	Cluster string            `json:"cluster"`
	Data    *DashboardMetrics `json:"data"`
}

func (m *DashboardMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func NewDashboardHandler(k8sClient k8s.Interface, hub *core.Hub) *DashboardHandler {
	return &DashboardHandler{
		k8sClient:     k8sClient,
		hub:           hub,
		watchers:      make(map[string]*dashboardWatcher),
		info:          make(map[string]clusterInfoEntry),
		metricsProbes: make(map[string]metricsProbeEntry),
	}
}

// SetPodLister makes the dashboard read pods from a shared pod cache.
func (h *DashboardHandler) SetPodLister(l podcache.Lister) { h.pods = l }

// SetRecentEvents makes the dashboard read recent events from an event store.
func (h *DashboardHandler) SetRecentEvents(f RecentEventsFunc) { h.recentEvents = f }

// fromWatchCache lets the apiserver answer a full LIST from its watch cache
// instead of a quorum read of etcd; a dashboard tolerates that staleness.
var fromWatchCache = metav1.ListOptions{ResourceVersion: "0"}

// listAllPods returns every pod in the cluster. Cached pods are shared and
// must not be modified.
func (h *DashboardHandler) listAllPods(ctx context.Context, cluster string, clientset kubernetes.Interface) ([]*v1.Pod, error) {
	if h.pods != nil {
		return h.pods.List(ctx, cluster)
	}
	list, err := clientset.CoreV1().Pods("").List(ctx, fromWatchCache)
	if err != nil {
		return nil, err
	}
	pods := make([]*v1.Pod, len(list.Items))
	for i := range list.Items {
		pods[i] = &list.Items[i]
	}
	return pods, nil
}

func (h *DashboardHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload map[string]interface{}
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return err
	}

	action, _ := payload["action"].(string)
	cluster, _ := payload["cluster"].(string)

	if cluster == "" {
		return fmt.Errorf("cluster parameter is required")
	}

	topic := topics.BuildDashboardTopic(cluster)

	switch action {
	case "start":
		log.Printf("[Dashboard] Starting dashboard stream for cluster: %s", cluster)
		if _, err := h.hub.Subscribe(topic, conn); err != nil {
			return err
		}
		h.startDashboardStream(cluster, topic)
		return nil

	case "stop":
		log.Printf("[Dashboard] Stopping dashboard stream for cluster: %s", cluster)
		if _, err := h.hub.Unsubscribe(topic, conn); err != nil {
			return err
		}
		if len(h.hub.Subscribers(topic)) == 0 {
			h.stopDashboardStream(cluster)
		}
		return nil

	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

func (h *DashboardHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"dashboard"}
}

func (h *DashboardHandler) HasActiveStream(cluster string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.watchers[cluster]
	return ok
}

// OnConnectionClose stops streams whose topic has no remaining subscribers.
// UnregisterConnection invokes close handlers before UnsubscribeAll, so the
// closing connection must be excluded from the count here.
func (h *DashboardHandler) OnConnectionClose(conn *core.Connection) {
	h.mu.RLock()
	clusters := make([]string, 0, len(h.watchers))
	for cluster := range h.watchers {
		clusters = append(clusters, cluster)
	}
	h.mu.RUnlock()
	for _, cluster := range clusters {
		remaining := 0
		for _, sub := range h.hub.Subscribers(topics.BuildDashboardTopic(cluster)) {
			if sub.ID() != conn.ID() {
				remaining++
			}
		}
		if remaining == 0 {
			log.Printf("[Dashboard] Last subscriber for %s disconnected, stopping stream", cluster)
			h.stopDashboardStream(cluster)
		}
	}
}

func (h *DashboardHandler) startDashboardStream(cluster, topic string) {
	h.mu.Lock()
	if watcher, exists := h.watchers[cluster]; exists {
		select {
		case watcher.refreshChan <- struct{}{}:
		default:
		}
		h.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	watcher := &dashboardWatcher{
		cluster:     cluster,
		cancelFunc:  cancel,
		stopChan:    make(chan struct{}),
		refreshChan: make(chan struct{}, 1),
	}
	h.watchers[cluster] = watcher
	h.mu.Unlock()

	go h.streamDashboard(ctx, cluster, topic, watcher.stopChan, watcher.refreshChan)
}

func (h *DashboardHandler) stopDashboardStream(cluster string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if watcher, exists := h.watchers[cluster]; exists {
		watcher.cancelFunc()
		close(watcher.stopChan)
		delete(h.watchers, cluster)
		log.Printf("[Dashboard] Stopped streaming for cluster: %s", cluster)
	}
}

func (h *DashboardHandler) streamDashboard(ctx context.Context, cluster, topic string, stopChan, refreshChan chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	sendUpdate := func() {
		metrics, err := h.fetchDashboardMetrics(ctx, cluster)
		if err != nil {
			log.Printf("[Dashboard] Error fetching metrics for %s: %v", cluster, err)
			return
		}

		msg := &DashboardMessage{
			BaseMessage: core.BaseMessage{
				MessageType: "dashboard",
				Timestamp:   time.Now(),
			},
			Cluster: cluster,
			Data:    metrics,
		}

		if err := h.hub.Broadcast(topic, msg); err != nil {
			log.Printf("[Dashboard] Error broadcasting dashboard update: %v", err)
		}
	}

	sendUpdate()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		case <-ticker.C:
			sendUpdate()
		case <-refreshChan:
			sendUpdate()
		}
	}
}

func (h *DashboardHandler) fetchDashboardMetrics(ctx context.Context, cluster string) (*DashboardMetrics, error) {
	metrics := &DashboardMetrics{
		ResourceCounts: make(map[string]int),
	}

	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	// The metrics-server probe runs alongside everything else: it is cached,
	// but when it is not, its discovery call should not delay the update.
	metricsDone := make(chan bool, 1)
	go func() { metricsDone <- h.metricsAvailable(ctx, cluster) }()

	var pods []*v1.Pod
	var nodes *v1.NodeList
	var podErr, nodeErr error
	var prefetchWg sync.WaitGroup
	prefetchWg.Add(2)
	go func() {
		defer prefetchWg.Done()
		pods, podErr = h.listAllPods(ctx, cluster, clientset)
	}()
	go func() {
		defer prefetchWg.Done()
		nodes, nodeErr = clientset.CoreV1().Nodes().List(ctx, fromWatchCache)
	}()
	prefetchWg.Wait()

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Counts for lists already in hand come for free; only the rest are
	// queried. Workloads and services are listed in full below, and those
	// fetches record their counts.
	counted := map[string]bool{"deployments": true, "statefulsets": true, "daemonsets": true}
	if podErr == nil {
		metrics.ResourceCounts[":pods"] = len(pods)
		counted["pods"] = true
		counted["services"] = true // listed by fetchCriticalAlerts
	}
	if nodeErr == nil {
		metrics.ResourceCounts[":nodes"] = len(nodes.Items)
		counted["nodes"] = true
	}

	wg.Add(7)

	go func() {
		defer wg.Done()
		if err := h.fetchResourceCounts(ctx, cluster, metrics, &mu, counted); err != nil {
			log.Printf("[Dashboard] Failed to fetch resource counts: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if podErr != nil {
			log.Printf("[Dashboard] Failed to fetch pod status: %v", podErr)
			return
		}
		h.computePodStatus(pods, metrics, &mu)
	}()

	go func() {
		defer wg.Done()
		if nodeErr != nil {
			log.Printf("[Dashboard] Failed to fetch node status: %v", nodeErr)
			return
		}
		h.computeNodeStatus(nodes, metrics, &mu)
	}()

	go func() {
		defer wg.Done()
		if err := h.fetchWorkloadStatus(ctx, cluster, metrics, &mu); err != nil {
			log.Printf("[Dashboard] Failed to fetch workload status: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := h.fetchRecentEvents(ctx, cluster, metrics, &mu); err != nil {
			log.Printf("[Dashboard] Failed to fetch recent events: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if podErr != nil {
			log.Printf("[Dashboard] Failed to fetch critical alerts: %v", podErr)
			return
		}
		if err := h.fetchCriticalAlerts(ctx, cluster, pods, metrics, &mu); err != nil {
			log.Printf("[Dashboard] Failed to fetch critical alerts: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if podErr != nil || nodeErr != nil {
			log.Printf("[Dashboard] Failed to fetch resource capacity: pods=%v nodes=%v", podErr, nodeErr)
			return
		}
		h.computeResourceCapacity(pods, nodes, metrics, &mu)
	}()

	wg.Wait()
	metrics.MetricsAvailable = <-metricsDone

	if info, ok := h.clusterInfo(ctx, cluster, clientset, nodes); ok {
		mu.Lock()
		nodeCount := 0
		if metrics.NodeStatus != nil {
			nodeCount = metrics.NodeStatus.Ready + metrics.NodeStatus.NotReady
		}
		metrics.ClusterInfo = ClusterInfo{
			Version:      info.version,
			Platform:     info.platform,
			Provider:     info.provider,
			Architecture: info.arch,
			NodeCount:    nodeCount,
		}
		mu.Unlock()
	}

	return metrics, nil
}

// clusterInfo returns version/platform/provider/arch, refreshed at most every
// clusterInfoTTL: the previous path re-ran the full cluster status probe
// (listing every node and namespace) plus a node list on every 30s tick.
func (h *DashboardHandler) clusterInfo(ctx context.Context, cluster string, clientset kubernetes.Interface, nodes *v1.NodeList) (clusterInfoEntry, bool) {
	h.infoMu.Lock()
	e, ok := h.info[cluster]
	h.infoMu.Unlock()
	if ok && time.Since(e.at) < clusterInfoTTL {
		return e, true
	}
	v, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return e, ok
	}
	e = clusterInfoEntry{version: v.Major + "." + v.Minor, platform: v.Platform, at: time.Now()}
	if nodes != nil && len(nodes.Items) > 0 {
		e.provider, e.arch = providerAndArchFromNode(&nodes.Items[0])
	} else {
		e.provider, e.arch = h.detectProviderAndArch(ctx, cluster)
	}
	h.infoMu.Lock()
	h.info[cluster] = e
	h.infoMu.Unlock()
	return e, true
}

func (h *DashboardHandler) fetchResourceCounts(ctx context.Context, cluster string, metrics *DashboardMetrics, mu *sync.Mutex, counted map[string]bool) error {
	resourceTypes := []struct {
		name  string
		group string
		gvr   schema.GroupVersionResource
	}{
		{"pods", "", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}},
		{"nodes", "", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}},
		{"namespaces", "", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}},
		{"services", "", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}},
		{"deployments", "apps", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{"statefulsets", "apps", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
		{"daemonsets", "apps", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
		{"storageclasses", "storage.k8s.io", schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}},
	}

	var countWg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for _, rt := range resourceTypes {
		if counted[rt.name] {
			continue
		}
		countWg.Add(1)
		sem <- struct{}{}

		go func(name, group string, gvr schema.GroupVersionResource) {
			defer func() {
				<-sem
				countWg.Done()
			}()

			count, err := h.k8sClient.GetResourceCount(ctx, cluster, gvr)
			if err != nil {
				count = 0
			}

			key := fmt.Sprintf("%s:%s", group, name)
			mu.Lock()
			metrics.ResourceCounts[key] = count
			mu.Unlock()
		}(rt.name, rt.group, rt.gvr)
	}

	countWg.Wait()
	return nil
}

func (h *DashboardHandler) computePodStatus(pods []*v1.Pod, metrics *DashboardMetrics, mu *sync.Mutex) {
	podStatus := &PodStatusMetrics{}
	for _, pod := range pods {
		switch pod.Status.Phase {
		case v1.PodRunning:
			podStatus.Running++
		case v1.PodPending:
			podStatus.Pending++
		case v1.PodFailed:
			podStatus.Failed++
		case v1.PodSucceeded:
			podStatus.Succeeded++
		default:
			podStatus.Unknown++
		}
	}
	mu.Lock()
	metrics.PodStatus = podStatus
	mu.Unlock()
}

func (h *DashboardHandler) computeNodeStatus(nodes *v1.NodeList, metrics *DashboardMetrics, mu *sync.Mutex) {
	nodeStatus := &NodeStatusMetrics{}
	for _, node := range nodes.Items {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			nodeStatus.Ready++
		} else {
			nodeStatus.NotReady++
		}
		if node.Spec.Unschedulable {
			nodeStatus.Unschedulable++
		} else {
			nodeStatus.Schedulable++
		}
	}
	mu.Lock()
	metrics.NodeStatus = nodeStatus
	mu.Unlock()
}

func (h *DashboardHandler) fetchWorkloadStatus(ctx context.Context, cluster string, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return err
	}

	workloadStatus := &WorkloadStatusMetrics{}
	counts := map[string]int{}

	deployments, err := clientset.AppsV1().Deployments("").List(ctx, fromWatchCache)
	if err == nil {
		workloadStatus.Deployments.Total = len(deployments.Items)
		counts["apps:deployments"] = len(deployments.Items)
		for _, d := range deployments.Items {
			if d.Status.ReadyReplicas > 0 && d.Status.ReadyReplicas == d.Status.Replicas {
				workloadStatus.Deployments.Healthy++
			}
		}
	}

	statefulSets, err := clientset.AppsV1().StatefulSets("").List(ctx, fromWatchCache)
	if err == nil {
		workloadStatus.StatefulSets.Total = len(statefulSets.Items)
		counts["apps:statefulsets"] = len(statefulSets.Items)
		for _, s := range statefulSets.Items {
			if s.Status.ReadyReplicas > 0 && s.Status.ReadyReplicas == s.Status.Replicas {
				workloadStatus.StatefulSets.Healthy++
			}
		}
	}

	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, fromWatchCache)
	if err == nil {
		workloadStatus.DaemonSets.Total = len(daemonSets.Items)
		counts["apps:daemonsets"] = len(daemonSets.Items)
		for _, d := range daemonSets.Items {
			if d.Status.NumberReady > 0 && d.Status.NumberReady == d.Status.DesiredNumberScheduled {
				workloadStatus.DaemonSets.Healthy++
			}
		}
	}

	mu.Lock()
	metrics.WorkloadStatus = workloadStatus
	for k, n := range counts {
		metrics.ResourceCounts[k] = n
	}
	mu.Unlock()
	return nil
}

type eventWithTime struct {
	event DashboardEvent
	time  time.Time
}

// newestEvents orders events newest first and keeps maxDashboardEvents.
func newestEvents(events []eventWithTime) []DashboardEvent {
	sort.Slice(events, func(i, j int) bool {
		return events[i].time.After(events[j].time)
	})
	out := make([]DashboardEvent, 0, min(len(events), maxDashboardEvents))
	for _, e := range events {
		if len(out) == maxDashboardEvents {
			break
		}
		out = append(out, e.event)
	}
	return out
}

func dashboardEvent(now, t time.Time, eventType, reason, message, kind, name, namespace string) eventWithTime {
	return eventWithTime{
		event: DashboardEvent{
			Time:      formatTimeAgo(now.Sub(t)),
			Type:      eventType,
			Reason:    reason,
			Message:   message,
			Object:    fmt.Sprintf("%s/%s", kind, name),
			Namespace: namespace,
		},
		time: t,
	}
}

// firstSet returns the first non-zero time: when an event last happened is
// lastTimestamp for core events and eventTime for events.k8s.io ones.
func firstSet(times ...time.Time) time.Time {
	for _, t := range times {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func (h *DashboardHandler) fetchRecentEvents(ctx context.Context, cluster string, metrics *DashboardMetrics, mu *sync.Mutex) error {
	var dashEvents []DashboardEvent
	if rows, ok := h.storedRecentEvents(cluster); ok {
		dashEvents = rows
	} else {
		listed, err := h.listRecentEvents(ctx, cluster)
		if err != nil {
			return err
		}
		dashEvents = listed
	}

	mu.Lock()
	metrics.Events = dashEvents
	log.Printf("[Dashboard] Fetched %d events for cluster %s", len(metrics.Events), cluster)
	if len(metrics.Events) > 0 {
		log.Printf("[Dashboard] Sample event: Type=%s, Reason=%s", metrics.Events[0].Type, metrics.Events[0].Reason)
	}
	mu.Unlock()
	return nil
}

// storedRecentEvents reads the newest events from the event store, which a
// watch keeps current for the whole cluster.
func (h *DashboardHandler) storedRecentEvents(cluster string) ([]DashboardEvent, bool) {
	if h.recentEvents == nil {
		return nil, false
	}
	rows, ok := h.recentEvents(cluster, maxDashboardEvents)
	if !ok {
		return nil, false
	}
	now := time.Now()
	events := make([]eventWithTime, 0, len(rows))
	for _, e := range rows {
		t := firstSet(e.LastTimestamp, e.EventTime, e.FirstTimestamp)
		if t.IsZero() {
			continue
		}
		events = append(events, dashboardEvent(now, t, e.Type, e.Reason, e.Message, e.InvolvedObjectKind, e.InvolvedObjectName, e.InvolvedObjectNamespace))
	}
	return newestEvents(events), true
}

// listRecentEvents is the fallback until the event store has synced. A limited
// LIST returns the first events in key order, so on a busy cluster these are
// recent events of the alphabetically first namespaces only.
func (h *DashboardHandler) listRecentEvents(ctx context.Context, cluster string) ([]DashboardEvent, error) {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	list, err := clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 100})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	events := make([]eventWithTime, 0, len(list.Items))
	for _, e := range list.Items {
		t := firstSet(e.LastTimestamp.Time, e.EventTime.Time, e.FirstTimestamp.Time)
		if t.IsZero() {
			continue
		}
		events = append(events, dashboardEvent(now, t, e.Type, e.Reason, e.Message, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.InvolvedObject.Namespace))
	}
	return newestEvents(events), nil
}

// metricsAvailable caches the metrics-server probe for clusterInfoTTL: its
// aggregated discovery download ran on every 30s tick ahead of everything
// else. Only definite answers are cached; a failed probe reports available
// and is retried on the next update.
func (h *DashboardHandler) metricsAvailable(ctx context.Context, cluster string) bool {
	h.infoMu.Lock()
	e, ok := h.metricsProbes[cluster]
	h.infoMu.Unlock()
	if ok && time.Since(e.at) < clusterInfoTTL {
		return e.available
	}
	available, definite := h.checkMetricsAvailable(ctx, cluster)
	if definite {
		h.infoMu.Lock()
		h.metricsProbes[cluster] = metricsProbeEntry{available: available, at: time.Now()}
		h.infoMu.Unlock()
	}
	return available
}

// checkMetricsAvailable reports whether the cluster serves metrics.k8s.io, and
// whether that answer is definite rather than the default after an error.
func (h *DashboardHandler) checkMetricsAvailable(ctx context.Context, cluster string) (available, definite bool) {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return true, false
	}

	_, err = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return true, false
	}

	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		return true, false
	}

	for _, group := range apiGroups.Groups {
		if group.Name == "metrics.k8s.io" {
			return true, true
		}
	}

	return false, true
}

func formatTimeAgo(duration time.Duration) string {
	if duration < time.Minute {
		return "Just now"
	}
	if duration < time.Hour {
		minutes := int(duration.Minutes())
		return fmt.Sprintf("%dm ago", minutes)
	}
	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		return fmt.Sprintf("%dh ago", hours)
	}
	days := int(duration.Hours() / 24)
	return fmt.Sprintf("%dd ago", days)
}

func (h *DashboardHandler) fetchCriticalAlerts(ctx context.Context, cluster string, pods []*v1.Pod, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return err
	}

	alerts := make([]CriticalAlert, 0)
	now := time.Now()

	for _, pod := range pods {
		age := formatTimeAgo(now.Sub(pod.CreationTimestamp.Time))
		podRef := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.RestartCount > 5 {
				alerts = append(alerts, CriticalAlert{
					Severity:  "high",
					Type:      "HighRestarts",
					Resource:  podRef,
					Namespace: pod.Namespace,
					Reason:    fmt.Sprintf("Container %s restarted %d times", containerStatus.Name, containerStatus.RestartCount),
					Message:   "High restart count indicates instability",
					Age:       age,
				})
			}

			if containerStatus.State.Waiting != nil {
				waiting := containerStatus.State.Waiting
				if waiting.Reason == "CrashLoopBackOff" || waiting.Reason == "ImagePullBackOff" || waiting.Reason == "ErrImagePull" {
					alerts = append(alerts, CriticalAlert{
						Severity:  "critical",
						Type:      waiting.Reason,
						Resource:  podRef,
						Namespace: pod.Namespace,
						Reason:    waiting.Reason,
						Message:   waiting.Message,
						Age:       age,
					})
				}
			}
		}

		if pod.Status.Phase == v1.PodPending {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == v1.PodScheduled && condition.Status == v1.ConditionFalse {
					alerts = append(alerts, CriticalAlert{
						Severity:  "medium",
						Type:      "PodPending",
						Resource:  podRef,
						Namespace: pod.Namespace,
						Reason:    condition.Reason,
						Message:   condition.Message,
						Age:       age,
					})
				}
			}
		}
	}

	jobs, err := clientset.BatchV1().Jobs("").List(ctx, fromWatchCache)
	if err == nil {
		for _, job := range jobs.Items {
			if job.Status.Failed > 0 {
				age := formatTimeAgo(now.Sub(job.CreationTimestamp.Time))
				alerts = append(alerts, CriticalAlert{
					Severity:  "high",
					Type:      "JobFailed",
					Resource:  fmt.Sprintf("%s/%s", job.Namespace, job.Name),
					Namespace: job.Namespace,
					Reason:    "JobFailed",
					Message:   fmt.Sprintf("%d pods failed", job.Status.Failed),
					Age:       age,
				})
			}
		}
	}

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims("").List(ctx, fromWatchCache)
	if err == nil {
		for _, pvc := range pvcs.Items {
			if pvc.Status.Phase == v1.ClaimPending {
				age := formatTimeAgo(now.Sub(pvc.CreationTimestamp.Time))
				alerts = append(alerts, CriticalAlert{
					Severity:  "medium",
					Type:      "PVCPending",
					Resource:  fmt.Sprintf("%s/%s", pvc.Namespace, pvc.Name),
					Namespace: pvc.Namespace,
					Reason:    "PersistentVolumeClaim pending",
					Message:   "Waiting for volume to be provisioned",
					Age:       age,
				})
			}
		}
	}

	services, err := clientset.CoreV1().Services("").List(ctx, fromWatchCache)
	if err == nil {
		mu.Lock()
		metrics.ResourceCounts[":services"] = len(services.Items)
		mu.Unlock()
		for _, svc := range services.Items {
			if svc.Spec.Type == v1.ServiceTypeLoadBalancer && len(svc.Status.LoadBalancer.Ingress) == 0 {
				age := formatTimeAgo(now.Sub(svc.CreationTimestamp.Time))
				if age != "Just now" {
					alerts = append(alerts, CriticalAlert{
						Severity:  "medium",
						Type:      "LoadBalancerPending",
						Resource:  fmt.Sprintf("%s/%s", svc.Namespace, svc.Name),
						Namespace: svc.Namespace,
						Reason:    "LoadBalancer pending",
						Message:   "Waiting for external IP assignment",
						Age:       age,
					})
				}
			}
		}
	}

	sort.Slice(alerts, func(i, j int) bool {
		severityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
		return severityOrder[alerts[i].Severity] < severityOrder[alerts[j].Severity]
	})

	mu.Lock()
	if len(alerts) > 15 {
		metrics.CriticalAlerts = alerts[:15]
	} else {
		metrics.CriticalAlerts = alerts
	}
	log.Printf("[Dashboard] Fetched %d critical alerts for cluster %s", len(metrics.CriticalAlerts), cluster)
	if len(metrics.CriticalAlerts) > 0 {
		log.Printf("[Dashboard] Sample alert: Severity=%s, Type=%s, Resource=%s", metrics.CriticalAlerts[0].Severity, metrics.CriticalAlerts[0].Type, metrics.CriticalAlerts[0].Resource)
	}
	mu.Unlock()
	return nil
}

func (h *DashboardHandler) computeResourceCapacity(pods []*v1.Pod, nodes *v1.NodeList, metrics *DashboardMetrics, mu *sync.Mutex) {
	var totalCPUAllocatable, totalMemoryAllocatable int64
	for _, node := range nodes.Items {
		totalCPUAllocatable += node.Status.Allocatable.Cpu().MilliValue()
		totalMemoryAllocatable += node.Status.Allocatable.Memory().Value()
	}

	var totalCPURequested, totalMemoryRequested int64
	for _, pod := range pods {
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			continue
		}
		for _, container := range pod.Spec.Containers {
			totalCPURequested += container.Resources.Requests.Cpu().MilliValue()
			totalMemoryRequested += container.Resources.Requests.Memory().Value()
		}
	}

	cpuPercentage := 0.0
	if totalCPUAllocatable > 0 {
		cpuPercentage = float64(totalCPURequested) / float64(totalCPUAllocatable) * 100
	}
	memoryPercentage := 0.0
	if totalMemoryAllocatable > 0 {
		memoryPercentage = float64(totalMemoryRequested) / float64(totalMemoryAllocatable) * 100
	}

	capacity := &ResourceCapacity{
		CPU: CapacityMetric{
			Requested:   totalCPURequested / 1000,
			Allocatable: totalCPUAllocatable / 1000,
			Percentage:  cpuPercentage,
			Unit:        "cores",
		},
		Memory: CapacityMetric{
			Requested:   totalMemoryRequested / (1024 * 1024 * 1024),
			Allocatable: totalMemoryAllocatable / (1024 * 1024 * 1024),
			Percentage:  memoryPercentage,
			Unit:        "GiB",
		},
	}

	mu.Lock()
	metrics.ResourceCapacity = capacity
	mu.Unlock()
}

func (h *DashboardHandler) detectProviderAndArch(ctx context.Context, cluster string) (string, string) {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return "Unknown", "Unknown"
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		return "Unknown", "Unknown"
	}
	return providerAndArchFromNode(&nodes.Items[0])
}

func providerAndArchFromNode(node *v1.Node) (string, string) {
	labels := node.Labels

	provider := "Unknown"
	if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
		provider = "AWS EKS"
	} else if _, ok := labels["node.kubernetes.io/instance-type"]; ok {
		instanceType := labels["node.kubernetes.io/instance-type"]
		if len(instanceType) > 0 {
			switch {
			case len(instanceType) > 3 && (instanceType[0:3] == "t2." || instanceType[0:3] == "t3." || instanceType[0:3] == "m5."):
				provider = "AWS EC2"
			}
		}
	}
	if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
		provider = "GCP GKE"
	} else if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
		provider = "Azure AKS"
	} else if _, ok := labels["node.kubernetes.io/kops-instancegroup"]; ok {
		provider = "kops"
	} else if _, ok := labels["vcluster.loft.sh/managed-by"]; ok {
		provider = "vcluster"
	} else if prov, ok := labels["node.kubernetes.io/provider"]; ok && provider == "Unknown" {
		provider = prov
	}

	arch := node.Status.NodeInfo.Architecture
	if arch == "" {
		arch = "Unknown"
	}

	return provider, arch
}

func (h *DashboardHandler) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for cluster, watcher := range h.watchers {
		watcher.cancelFunc()
		close(watcher.stopChan)
		log.Printf("[Dashboard] Shutdown: stopped streaming for cluster: %s", cluster)
	}
	h.watchers = make(map[string]*dashboardWatcher)
}
