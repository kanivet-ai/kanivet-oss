package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ListVClusters(c *gin.Context) {
	host, ok := h.requireCluster(c)
	if !ok {
		return
	}
	vcs, err := h.k8s.ListVClusters(host)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("list vclusters: %w", err))
		return
	}
	h.respond(c, http.StatusOK, gin.H{"vclusters": vcs}, nil)
}

func (h *Handler) ConnectVCluster(c *gin.Context) {
	var req struct {
		Host      string `json:"host" binding:"required"`
		Namespace string `json:"namespace" binding:"required"`
		Name      string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request: %v", err))
		return
	}
	conn, err := h.k8s.ConnectVCluster(req.Host, req.Namespace, req.Name)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "vcluster_not_ready") {
			status = http.StatusNotFound
		}
		h.respond(c, status, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{
		"id":        conn.ID,
		"host":      conn.Host,
		"namespace": conn.Namespace,
		"name":      conn.Name,
		"localPort": conn.LocalPort,
	}, nil)
}

func (h *Handler) DisconnectVCluster(c *gin.Context) {
	id := c.Param("id")
	if len(id) > 0 && id[0] == '/' {
		id = id[1:]
	}
	if id == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("id is required"))
		return
	}
	if err := h.k8s.DisconnectVCluster(id); err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("disconnect: %w", err))
		return
	}
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}
