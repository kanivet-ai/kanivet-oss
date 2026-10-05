package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/k8s"
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

	podName, targetPort, err := k8s.ResolveServicePod(c.Request.Context(), clientset, namespace, serviceName, int32(request.Port))
	if err != nil {
		h.respond(c, statusForServicePodResolveError(err), nil, err)
		return
	}

	log.Printf("Creating port forward to pod %s on port %d (service port %d)", podName, targetPort, request.Port)

	pf, err := h.k8s.CreatePortForward(cluster, namespace, podName, targetPort)
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

func statusForServicePodResolveError(err error) int {
	switch {
	case errors.Is(err, k8s.ErrServiceNotFound), errors.Is(err, k8s.ErrNoPodsMatchSelector):
		return http.StatusNotFound
	case errors.Is(err, k8s.ErrServiceNoSelector), errors.Is(err, k8s.ErrServicePortNotFound), errors.Is(err, k8s.ErrNamedPortNotFound):
		return http.StatusBadRequest
	case errors.Is(err, k8s.ErrNoRunningPodsForService), errors.Is(err, k8s.ErrNoReadyPodsForService):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
