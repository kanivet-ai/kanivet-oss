package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

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

type dashboardChunk struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func (h *Handler) StreamClusterDashboard(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}

	c.Writer.Header().Set("Content-Type", "application/x-ndjson")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)
	enc := json.NewEncoder(c.Writer)
	var writeMu sync.Mutex
	send := func(t string, d interface{}) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := enc.Encode(dashboardChunk{Type: t, Data: d}); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	clientset, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		send("error", err.Error())
		return
	}

	var pods *v1.PodList
	var nodes *v1.NodeList
	var deploys *appsv1.DeploymentList
	var stss *appsv1.StatefulSetList
	var dss *appsv1.DaemonSetList
	var podErr, nodeErr, deployErr, stsErr, dsErr error
	podsReady := make(chan struct{})
	nodesReady := make(chan struct{})
	deploysReady := make(chan struct{})
	stssReady := make(chan struct{})
	dssReady := make(chan struct{})
	go func() {
		defer close(podsReady)
		pods, podErr = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer close(nodesReady)
		nodes, nodeErr = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer close(deploysReady)
		deploys, deployErr = clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer close(stssReady)
		stss, stsErr = clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer close(dssReady)
		dss, dsErr = clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	}()

	metrics := &DashboardMetrics{ResourceCounts: make(map[string]int)}
	var mu sync.Mutex

	sendCountsSnapshot := func() {
		mu.Lock()
		snap := make(map[string]int, len(metrics.ResourceCounts))
		for k, v := range metrics.ResourceCounts {
			snap[k] = v
		}
		mu.Unlock()
		send("resourceCounts", snap)
	}
	setCount := func(group, name string, n int) {
		mu.Lock()
		metrics.ResourceCounts[fmt.Sprintf("%s:%s", group, name)] = n
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(9)

	go func() {
		defer wg.Done()
		avail := h.checkMetricsAvailable(ctx, cluster)
		mu.Lock()
		metrics.MetricsAvailable = avail
		mu.Unlock()
		send("metricsAvailable", avail)
	}()

	go func() {
		defer wg.Done()
		if err := h.fetchDynamicResourceCounts(ctx, cluster, []schema.GroupVersionResource{
			{Group: "", Version: "v1", Resource: "namespaces"},
			{Group: "", Version: "v1", Resource: "services"},
			{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"},
		}, metrics, &mu); err != nil {
			log.Printf("Failed to fetch dynamic resource counts: %v", err)
		}
		sendCountsSnapshot()
	}()

	go func() {
		defer wg.Done()
		<-podsReady
		if podErr != nil {
			return
		}
		setCount("", "pods", len(pods.Items))
		sendCountsSnapshot()
		h.computePodStatus(pods, metrics, &mu)
		mu.Lock()
		ps := metrics.PodStatus
		mu.Unlock()
		send("podStatus", ps)
	}()

	go func() {
		defer wg.Done()
		<-nodesReady
		if nodeErr != nil {
			return
		}
		setCount("", "nodes", len(nodes.Items))
		sendCountsSnapshot()
		h.computeNodeStatus(nodes, metrics, &mu)
		mu.Lock()
		ns := metrics.NodeStatus
		mu.Unlock()
		send("nodeStatus", ns)
	}()

	go func() {
		defer wg.Done()
		<-deploysReady
		<-stssReady
		<-dssReady
		if deployErr == nil {
			setCount("apps", "deployments", len(deploys.Items))
		}
		if stsErr == nil {
			setCount("apps", "statefulsets", len(stss.Items))
		}
		if dsErr == nil {
			setCount("apps", "daemonsets", len(dss.Items))
		}
		sendCountsSnapshot()
		ws := computeWorkloadStatus(deploys, stss, dss, deployErr, stsErr, dsErr)
		mu.Lock()
		metrics.WorkloadStatus = ws
		mu.Unlock()
		send("workloadStatus", ws)
	}()

	go func() {
		defer wg.Done()
		if err := h.fetchRecentEvents(ctx, cluster, metrics, &mu); err != nil {
			log.Printf("Failed to fetch recent events: %v", err)
			return
		}
		mu.Lock()
		ev := metrics.Events
		mu.Unlock()
		send("events", ev)
	}()

	go func() {
		defer wg.Done()
		<-podsReady
		if podErr != nil {
			return
		}
		if err := h.fetchCriticalAlerts(ctx, cluster, pods, metrics, &mu); err != nil {
			log.Printf("Failed to fetch critical alerts: %v", err)
			return
		}
		mu.Lock()
		al := metrics.CriticalAlerts
		mu.Unlock()
		send("criticalAlerts", al)
	}()

	go func() {
		defer wg.Done()
		<-podsReady
		<-nodesReady
		if podErr != nil || nodeErr != nil {
			return
		}
		h.computeResourceCapacity(pods, nodes, metrics, &mu)
		mu.Lock()
		rc := metrics.ResourceCapacity
		mu.Unlock()
		send("resourceCapacity", rc)
	}()

	go func() {
		defer wg.Done()
		<-nodesReady
		clusterStatus, _ := h.k8s.GetClusterStatus(cluster)
		info := ClusterInfo{Provider: "Unknown", Architecture: "Unknown"}
		if nodeErr == nil && nodes != nil {
			info.NodeCount = len(nodes.Items)
			if len(nodes.Items) > 0 {
				info.Provider, info.Architecture = providerAndArchFromNode(&nodes.Items[0])
			}
		}
		if clusterStatus != nil {
			info.Version = clusterStatus.Version
			info.Platform = clusterStatus.Platform
		}
		mu.Lock()
		metrics.ClusterInfo = info
		mu.Unlock()
		send("clusterInfo", info)
	}()

	wg.Wait()
	send("done", nil)
}

func (h *Handler) GetClusterDashboard(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	cacheKey := h.cache.BuildKey("dashboard", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 30*time.Second, func() (interface{}, error) {
		return h.fetchDashboardMetrics(cluster)
	})
	if err != nil {
		log.Printf("Failed to fetch dashboard metrics: %v", err)
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, data, nil)
}

func (h *Handler) fetchDashboardMetrics(cluster string) (*DashboardMetrics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	metrics := &DashboardMetrics{
		ResourceCounts: make(map[string]int),
	}

	metrics.MetricsAvailable = h.checkMetricsAvailable(ctx, cluster)

	clientset, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	var pods *v1.PodList
	var nodes *v1.NodeList
	var deploys *appsv1.DeploymentList
	var stss *appsv1.StatefulSetList
	var dss *appsv1.DaemonSetList
	var podErr, nodeErr, deployErr, stsErr, dsErr error
	var prefetchWg sync.WaitGroup
	prefetchWg.Add(5)
	go func() {
		defer prefetchWg.Done()
		pods, podErr = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer prefetchWg.Done()
		nodes, nodeErr = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer prefetchWg.Done()
		deploys, deployErr = clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer prefetchWg.Done()
		stss, stsErr = clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer prefetchWg.Done()
		dss, dsErr = clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	}()
	prefetchWg.Wait()

	var wg sync.WaitGroup
	var mu sync.Mutex

	wg.Add(5)

	go func() {
		defer wg.Done()
		if err := h.fetchDynamicResourceCounts(ctx, cluster, []schema.GroupVersionResource{
			{Group: "", Version: "v1", Resource: "namespaces"},
			{Group: "", Version: "v1", Resource: "services"},
			{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"},
		}, metrics, &mu); err != nil {
			log.Printf("Failed to fetch dynamic resource counts: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if podErr == nil {
			mu.Lock()
			metrics.ResourceCounts[":pods"] = len(pods.Items)
			mu.Unlock()
			h.computePodStatus(pods, metrics, &mu)
			if err := h.fetchCriticalAlerts(ctx, cluster, pods, metrics, &mu); err != nil {
				log.Printf("Failed to fetch critical alerts: %v", err)
			}
		} else {
			log.Printf("Failed to fetch pods: %v", podErr)
		}
	}()

	go func() {
		defer wg.Done()
		if nodeErr == nil {
			mu.Lock()
			metrics.ResourceCounts[":nodes"] = len(nodes.Items)
			mu.Unlock()
			h.computeNodeStatus(nodes, metrics, &mu)
		} else {
			log.Printf("Failed to fetch nodes: %v", nodeErr)
		}
		if podErr == nil && nodeErr == nil {
			h.computeResourceCapacity(pods, nodes, metrics, &mu)
		}
	}()

	go func() {
		defer wg.Done()
		ws := computeWorkloadStatus(deploys, stss, dss, deployErr, stsErr, dsErr)
		mu.Lock()
		metrics.WorkloadStatus = ws
		if deployErr == nil {
			metrics.ResourceCounts["apps:deployments"] = len(deploys.Items)
		}
		if stsErr == nil {
			metrics.ResourceCounts["apps:statefulsets"] = len(stss.Items)
		}
		if dsErr == nil {
			metrics.ResourceCounts["apps:daemonsets"] = len(dss.Items)
		}
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		if err := h.fetchRecentEvents(ctx, cluster, metrics, &mu); err != nil {
			log.Printf("Failed to fetch recent events: %v", err)
		}
	}()

	wg.Wait()

	clusterStatus, _ := h.k8s.GetClusterStatus(cluster)
	info := ClusterInfo{Provider: "Unknown", Architecture: "Unknown"}
	if nodeErr == nil && nodes != nil {
		info.NodeCount = len(nodes.Items)
		if len(nodes.Items) > 0 {
			info.Provider, info.Architecture = providerAndArchFromNode(&nodes.Items[0])
		}
	}
	if clusterStatus != nil {
		info.Version = clusterStatus.Version
		info.Platform = clusterStatus.Platform
	}
	mu.Lock()
	metrics.ClusterInfo = info
	mu.Unlock()

	return metrics, nil
}

func (h *Handler) fetchDynamicResourceCounts(ctx context.Context, cluster string, gvrs []schema.GroupVersionResource, metrics *DashboardMetrics, mu *sync.Mutex) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, gvr := range gvrs {
		wg.Add(1)
		sem <- struct{}{}
		go func(g schema.GroupVersionResource) {
			defer func() {
				<-sem
				wg.Done()
			}()
			count, err := h.k8s.GetResourceCount(ctx, cluster, g)
			if err != nil {
				log.Printf("Error getting count for %s: %v", g.Resource, err)
				count = 0
			}
			mu.Lock()
			metrics.ResourceCounts[fmt.Sprintf("%s:%s", g.Group, g.Resource)] = count
			mu.Unlock()
		}(gvr)
	}
	wg.Wait()
	return nil
}

func (h *Handler) computePodStatus(pods *v1.PodList, metrics *DashboardMetrics, mu *sync.Mutex) {
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

func (h *Handler) computeNodeStatus(nodes *v1.NodeList, metrics *DashboardMetrics, mu *sync.Mutex) {
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

func computeWorkloadStatus(deploys *appsv1.DeploymentList, stss *appsv1.StatefulSetList, dss *appsv1.DaemonSetList, dErr, sErr, dsErr error) *WorkloadStatusMetrics {
	ws := &WorkloadStatusMetrics{}
	if dErr == nil && deploys != nil {
		ws.Deployments.Total = len(deploys.Items)
		for _, d := range deploys.Items {
			if d.Status.ReadyReplicas > 0 && d.Status.ReadyReplicas == d.Status.Replicas {
				ws.Deployments.Healthy++
			}
		}
	}
	if sErr == nil && stss != nil {
		ws.StatefulSets.Total = len(stss.Items)
		for _, s := range stss.Items {
			if s.Status.ReadyReplicas > 0 && s.Status.ReadyReplicas == s.Status.Replicas {
				ws.StatefulSets.Healthy++
			}
		}
	}
	if dsErr == nil && dss != nil {
		ws.DaemonSets.Total = len(dss.Items)
		for _, d := range dss.Items {
			if d.Status.NumberReady > 0 && d.Status.NumberReady == d.Status.DesiredNumberScheduled {
				ws.DaemonSets.Healthy++
			}
		}
	}
	return ws
}

func (h *Handler) fetchRecentEvents(ctx context.Context, cluster string, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8s.GetClientForCluster(cluster)
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
	mu.Unlock()
	return nil
}

func (h *Handler) checkMetricsAvailable(ctx context.Context, cluster string) bool {
	clientset, err := h.k8s.GetClientForCluster(cluster)
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

func (h *Handler) fetchCriticalAlerts(ctx context.Context, cluster string, pods *v1.PodList, metrics *DashboardMetrics, mu *sync.Mutex) error {
	clientset, err := h.k8s.GetClientForCluster(cluster)
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
	mu.Unlock()
	return nil
}

func (h *Handler) computeResourceCapacity(pods *v1.PodList, nodes *v1.NodeList, metrics *DashboardMetrics, mu *sync.Mutex) {
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

func providerAndArchFromNode(node *v1.Node) (string, string) {
	labels := node.Labels
	provider := "Unknown"
	if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
		provider = "AWS EKS"
	} else if it, ok := labels["node.kubernetes.io/instance-type"]; ok && len(it) > 3 {
		switch it[0:3] {
		case "t2.", "t3.", "m5.":
			provider = "AWS EC2"
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
