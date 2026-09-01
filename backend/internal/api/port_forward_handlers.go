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
)

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
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	log.Printf("Creating port forward to remote port %d", request.RemotePort)

	pf, err := h.k8s.CreatePortForward(cluster, namespace, podName, request.RemotePort)
	if err != nil {
		log.Printf("Failed to create port forward: %v", err)
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to create port forward: %v", err))
		return
	}

	log.Printf("Port forward created successfully: %+v", pf)
	h.respond(c, http.StatusOK, gin.H{
		"id":         pf.ID,
		"localPort":  pf.LocalPort,
		"remotePort": pf.RemotePort,
		"active":     pf.Active,
		"createdAt":  pf.CreatedAt,
	}, nil)
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
		Port int `json:"port" binding:"required"`
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

	pf, err := h.k8s.CreatePortForward(cluster, namespace, selectedPod.Name, targetPort)
	if err != nil {
		log.Printf("Failed to create port forward: %v", err)
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to create port forward: %v", err))
		return
	}

	log.Printf("Service port forward created successfully: %+v", pf)
	h.respond(c, http.StatusOK, gin.H{
		"id":         pf.ID,
		"localPort":  pf.LocalPort,
		"remotePort": pf.RemotePort,
		"active":     pf.Active,
		"createdAt":  pf.CreatedAt,
	}, nil)
}

func (h *Handler) StopPortForward(c *gin.Context) {
	id := c.Param("id")
	if len(id) > 0 && id[0] == '/' {
		id = id[1:]
	}
	log.Printf("Received request to stop port forward: %s", id)

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
