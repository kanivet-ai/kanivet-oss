package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

const clusterInfoTTL = 10 * time.Minute

type DashboardHandler struct {
	k8sClient k8s.Interface
	hub       *core.Hub
	mu        sync.RWMutex
	watchers  map[string]*dashboardWatcher
	infoMu    sync.Mutex
	info      map[string]clusterInfoEntry
}

type clusterInfoEntry struct {
	version, platform, provider, arch string
	at                                time.Time
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
		k8sClient: k8sClient,
		hub:       hub,
		watchers:  make(map[string]*dashboardWatcher),
		info:      make(map[string]clusterInfoEntry),
	}
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

	metricsAvailable := h.checkMetricsAvailable(ctx, cluster)
	metrics.MetricsAvailable = metricsAvailable

	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	var pods *v1.PodList
	var nodes *v1.NodeList
	var podErr, nodeErr error
	var prefetchWg sync.WaitGroup
	prefetchWg.Add(2)
	go func() {
		defer prefetchWg.Done()
		pods, podErr = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer prefetchWg.Done()
		nodes, nodeErr = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	}()
	prefetchWg.Wait()

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Counts for lists already in hand come for free; only the rest are queried.
	counted := map[string]bool{}
	if podErr == nil {
		metrics.ResourceCounts[":pods"] = len(pods.Items)
		counted["pods"] = true
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

func (h *DashboardHandler) computePodStatus(pods *v1.PodList, metrics *DashboardMetrics, mu *sync.Mutex) {
	podStatus := &PodStatusMetrics{}
	for _, pod := range pods.Items {
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

	deployments, err := clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err == nil {
		workloadStatus.Deployments.Total = len(deployments.Items)
		for _, d := range deployments.Items {
			if d.Status.ReadyReplicas > 0 && d.Status.ReadyReplicas == d.Status.Replicas {
				workloadStatus.Deployments.Healthy++
			}
		}
	}

	statefulSets, err := clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		workloadStatus.StatefulSets.Total = len(statefulSets.Items)
		for _, s := range statefulSets.Items {
			if s.Status.ReadyReplicas > 0 && s.Status.ReadyReplicas == s.Status.Replicas {
				workloadStatus.StatefulSets.Healthy++
			}
		}
	}

	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		workloadStatus.DaemonSets.Total = len(daemonSets.Items)
		for _, d := range daemonSets.Items {
			if d.Status.NumberReady > 0 && d.Status.NumberReady == d.Status.DesiredNumberScheduled {
				workloadStatus.DaemonSets.Healthy++
			}
		}
	}

	mu.Lock()
	metrics.WorkloadStatus = workloadStatus
	mu.Unlock()
	return nil
}

func (h *DashboardHandler) fetchRecentEvents(ctx context.Context, cluster string, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return err
	}

	events, err := clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{
		Limit: 100,
	})
	if err != nil {
		return err
	}

	type eventWithTime struct {
		event DashboardEvent
		time  time.Time
	}

	eventsWithTime := make([]eventWithTime, 0, len(events.Items))
	now := time.Now()

	for _, event := range events.Items {
		eventTime := event.LastTimestamp.Time
		if eventTime.IsZero() {
			eventTime = event.EventTime.Time
		}
		if eventTime.IsZero() {
			eventTime = event.FirstTimestamp.Time
		}

		if eventTime.IsZero() {
			continue
		}

		timeAgo := formatTimeAgo(now.Sub(eventTime))

		eventsWithTime = append(eventsWithTime, eventWithTime{
			event: DashboardEvent{
				Time:      timeAgo,
				Type:      event.Type,
				Reason:    event.Reason,
				Message:   event.Message,
				Object:    fmt.Sprintf("%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Name),
				Namespace: event.InvolvedObject.Namespace,
			},
			time: eventTime,
		})
	}

	sort.Slice(eventsWithTime, func(i, j int) bool {
		return eventsWithTime[i].time.After(eventsWithTime[j].time)
	})

	dashEvents := make([]DashboardEvent, 0, len(eventsWithTime))
	for _, e := range eventsWithTime {
		dashEvents = append(dashEvents, e.event)
	}

	mu.Lock()
	if len(dashEvents) > 20 {
		metrics.Events = dashEvents[:20]
	} else {
		metrics.Events = dashEvents
	}
	log.Printf("[Dashboard] Fetched %d events for cluster %s", len(metrics.Events), cluster)
	if len(metrics.Events) > 0 {
		log.Printf("[Dashboard] Sample event: Type=%s, Reason=%s", metrics.Events[0].Type, metrics.Events[0].Reason)
	}
	mu.Unlock()
	return nil
}

func (h *DashboardHandler) checkMetricsAvailable(ctx context.Context, cluster string) bool {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return true
	}

	_, err = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return true
	}

	apiGroups, err := clientset.Discovery().ServerGroups()
	if err != nil {
		return true
	}

	for _, group := range apiGroups.Groups {
		if group.Name == "metrics.k8s.io" {
			return true
		}
	}

	return false
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

func (h *DashboardHandler) fetchCriticalAlerts(ctx context.Context, cluster string, pods *v1.PodList, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8sClient.GetClientForCluster(cluster)
	if err != nil {
		return err
	}

	alerts := make([]CriticalAlert, 0)
	now := time.Now()

	for _, pod := range pods.Items {
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

	jobs, err := clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
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

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
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

	services, err := clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err == nil {
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

func (h *DashboardHandler) computeResourceCapacity(pods *v1.PodList, nodes *v1.NodeList, metrics *DashboardMetrics, mu *sync.Mutex) {
	var totalCPUAllocatable, totalMemoryAllocatable int64
	for _, node := range nodes.Items {
		totalCPUAllocatable += node.Status.Allocatable.Cpu().MilliValue()
		totalMemoryAllocatable += node.Status.Allocatable.Memory().Value()
	}

	var totalCPURequested, totalMemoryRequested int64
	for _, pod := range pods.Items {
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
