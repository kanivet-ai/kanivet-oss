package nodeload

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// loadTimeout bounds a request that waits for the pod cache's first sync on
// a cluster whose pods are not being watched yet.
const loadTimeout = 30 * time.Second

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetNodeLoad(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), loadTimeout)
	defer cancel()
	report, err := h.service.Load(ctx, cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}
