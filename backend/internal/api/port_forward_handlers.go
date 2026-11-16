package api

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kanivet/backend/internal/k8s"
)

func portForwardListItem(pf *k8s.PortForward) gin.H {
	name := pf.SourceName
	if name == "" {
		name = pf.PodName
	}
	kind := strings.ToLower(pf.SourceKind)
	if kind == "" {
		kind = "pod"
	}
	item := gin.H{
		"id":         pf.ID,
		"cluster":    pf.Cluster,
		"namespace":  pf.Namespace,
		"podName":    pf.PodName,
		"name":       name,
		"kind":       kind,
		"localPort":  pf.LocalPort,
		"remotePort": pf.RemotePort,
		"active":     pf.Active,
		"createdAt":  pf.CreatedAt,
	}
	if pf.SourcePort > 0 {
		item["servicePort"] = pf.SourcePort
	}
	return item
}

func (h *Handler) ListPortForwards(c *gin.Context) {
	cluster := c.Query("cluster")
	forwards := h.k8s.ListPortForwards(cluster)
	items := make([]gin.H, 0, len(forwards))
	for _, pf := range forwards {
		items = append(items, portForwardListItem(pf))
	}
	h.respond(c, http.StatusOK, gin.H{"portForwards": items}, nil)
}

func (h *Handler) CreatePortForward(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	podName := c.Param("pod")

	if namespace == "_" {
		namespace = "default"
	}

	log.Printf("Port forward request: cluster=%s, namespace=%s, pod=%s", cluster, namespace, podName)

	var request struct {
		RemotePort int `json:"remotePort" binding:"required"`
		LocalPort  int `json:"localPort"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	log.Printf("Creating port forward to remote port %d", request.RemotePort)

	pf, err := h.k8s.CreatePortForward(cluster, namespace, podName, request.RemotePort, request.LocalPort)
	if err != nil {
		log.Printf("Failed to create port forward: %v", err)
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to create port forward: %v", err))
		return
	}

	pf.SourceName = podName
	pf.SourceKind = "Pod"
	log.Printf("Port forward created successfully: %+v", pf)
	h.respond(c, http.StatusOK, portForwardListItem(pf), nil)
}

func (h *Handler) CreateServicePortForward(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	serviceName := c.Param("service")

	if namespace == "_" {
		namespace = "default"
	}

	log.Printf("Service port forward request: cluster=%s, namespace=%s, service=%s", cluster, namespace, serviceName)

	var request struct {
		Port      int `json:"port" binding:"required"`
		LocalPort int `json:"localPort"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	clientset, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to get cluster client: %v", err))
		return
	}

	svc, err := clientset.CoreV1().Services(namespace).Get(c.Request.Context(), serviceName, metav1.GetOptions{})
	if err != nil {
		h.respond(c, http.StatusNotFound, nil, fmt.Errorf("service not found: %v", err))
		return
	}

	if len(svc.Spec.Selector) == 0 {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("service has no selector"))
		return
	}

	var servicePort *v1.ServicePort
	for i := range svc.Spec.Ports {
		if svc.Spec.Ports[i].Port == int32(request.Port) {
			servicePort = &svc.Spec.Ports[i]
			break
		}
	}
	if servicePort == nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("port %d not found on service", request.Port))
		return
	}

	selector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
	pods, err := clientset.CoreV1().Pods(namespace).List(c.Request.Context(), metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to list pods for service: %v", err))
		return
	}
	if len(pods.Items) == 0 {
		h.respond(c, http.StatusNotFound, nil, fmt.Errorf("no pods found matching service selector %s", selector))
		return
	}

	type podCandidate struct {
		pod   *v1.Pod
		ready bool
		score int
	}
	candidates := make([]podCandidate, 0, len(pods.Items))
	runningCount := 0
	readyCount := 0

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != v1.PodRunning {
			continue
		}
		runningCount++
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
				ready = true
				break
			}
		}
		if ready {
			readyCount++
		}
		score := 0
		if ready {
			score += 100
		}
		if pod.DeletionTimestamp == nil {
			score += 10
		}
		candidates = append(candidates, podCandidate{pod: pod, ready: ready, score: score})
	}

	if len(candidates) == 0 {
		totalPods := len(pods.Items)
		h.respond(c, http.StatusServiceUnavailable, nil,
			fmt.Errorf("no running pods found for service (found %d total pods, none in Running phase)", totalPods))
		return
	}
	if readyCount == 0 {
		h.respond(c, http.StatusServiceUnavailable, nil,
			fmt.Errorf("no ready pods found for service (found %d running pods, none ready)", runningCount))
		return
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].pod.Name < candidates[j].pod.Name
	})

	selectedPod := candidates[0].pod
	log.Printf("Selected pod %s (ready=%v, score=%d) from %d candidates",
		selectedPod.Name, candidates[0].ready, candidates[0].score, len(candidates))

	targetPort := 0
	if servicePort.TargetPort.IntVal != 0 {
		targetPort = int(servicePort.TargetPort.IntVal)
	} else if servicePort.TargetPort.StrVal != "" {
		portName := servicePort.TargetPort.StrVal
		for _, container := range selectedPod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == portName {
					targetPort = int(port.ContainerPort)
					break
				}
			}
			if targetPort != 0 {
				break
			}
		}
		if targetPort == 0 {
			h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("named port %s not found in pod containers", portName))
			return
		}
	} else {
		targetPort = int(servicePort.Port)
	}

	log.Printf("Creating port forward to pod %s on port %d (service port %d)", selectedPod.Name, targetPort, request.Port)

	pf, err := h.k8s.CreatePortForward(cluster, namespace, selectedPod.Name, targetPort, request.LocalPort)
	if err != nil {
		log.Printf("Failed to create port forward: %v", err)
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to create port forward: %v", err))
		return
	}

	pf.SourceName = serviceName
	pf.SourceKind = "Service"
	pf.SourcePort = request.Port
	log.Printf("Service port forward created successfully: %+v", pf)
	h.respond(c, http.StatusOK, portForwardListItem(pf), nil)
}

func portForwardIDParam(c *gin.Context) string {
	id := c.Param("id")
	if len(id) > 0 && id[0] == '/' {
		id = id[1:]
	}
	return id
}

func (h *Handler) StartPortForwardByID(c *gin.Context) {
	var body struct {
		ID          string `json:"id"`
		Cluster     string `json:"cluster"`
		Namespace   string `json:"namespace"`
		PodName     string `json:"podName"`
		RemotePort  int    `json:"remotePort"`
		LocalPort   int    `json:"localPort"`
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		ServicePort int    `json:"servicePort"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("port forward id required"))
		return
	}

	pf, err := h.k8s.ResumePortForward(body.ID)
	if err != nil && strings.Contains(err.Error(), "not found") {
		cluster := strings.TrimSpace(body.Cluster)
		namespace := strings.TrimSpace(body.Namespace)
		podName := strings.TrimSpace(body.PodName)
		if cluster == "" || namespace == "" || podName == "" || body.RemotePort <= 0 {
			h.respond(c, http.StatusNotFound, nil, err)
			return
		}
		pf, err = h.k8s.CreatePortForward(cluster, namespace, podName, body.RemotePort, body.LocalPort)
		if err == nil && strings.EqualFold(strings.TrimSpace(body.Kind), "service") {
			if name := strings.TrimSpace(body.Name); name != "" {
				pf.SourceName = name
				pf.SourceKind = "Service"
			}
			if body.ServicePort > 0 {
				pf.SourcePort = body.ServicePort
			}
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			h.respond(c, http.StatusNotFound, nil, err)
		} else {
			h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to start port forward: %v", err))
		}
		return
	}
	h.respond(c, http.StatusOK, portForwardListItem(pf), nil)
}

func (h *Handler) stopPortForwardCore(c *gin.Context, id string, remove bool) {
	if id == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("port forward id required"))
		return
	}
	log.Printf("Received request to stop port forward: %s", id)

	if remove {
		err := h.k8s.RemovePortForward(id)
		if err != nil {
			h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to remove port forward: %v", err))
			return
		}
		h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
		return
	}

	err := h.k8s.StopPortForward(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			h.respond(c, http.StatusNotFound, nil, fmt.Errorf("port forward not found: %s", id))
		} else {
			h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to stop port forward: %v", err))
		}
		return
	}
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

func (h *Handler) StopPortForward(c *gin.Context) {
	h.stopPortForwardCore(c, portForwardIDParam(c), c.Query("remove") == "1")
}

func (h *Handler) StopPortForwardByID(c *gin.Context) {
	var body struct {
		ID     string `json:"id"`
		Remove bool   `json:"remove"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.ID == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("port forward id required"))
		return
	}
	h.stopPortForwardCore(c, body.ID, body.Remove || c.Query("remove") == "1")
}