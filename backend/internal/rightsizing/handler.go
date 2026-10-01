package rightsizing

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetReport returns the cluster's report at once: ready, stale while a fresh
// one computes, or progress only. Poll while Status is "computing" or the
// report carries Progress.
func (h *Handler) GetReport(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}
	rep := h.service.GetReport(cluster, ParseProfile(c.Query("profile")), ParseWindow(c.Query("window")), c.Query("refresh") == "1", c.Query("known"))
	c.JSON(http.StatusOK, gin.H{"data": rep})
}

func (h *Handler) GetWorkload(c *gin.Context) {
	q := WorkloadQuery{
		Cluster:           c.Query("cluster"),
		Namespace:         c.Query("namespace"),
		VClusterNamespace: c.Query("vclusterNamespace"),
		Kind:              c.Query("kind"),
		Name:              c.Query("name"),
		Profile:           ParseProfile(c.Query("profile")),
		Window:            ParseWindow(c.Query("window")),
	}
	if q.Cluster == "" || q.Namespace == "" || q.Kind == "" || q.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster, namespace, kind and name are required"})
		return
	}
	ev, err := h.service.GetEvidence(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ev})
}

func (h *Handler) Dismiss(c *gin.Context) {
	var req DismissRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Cluster == "" || req.Namespace == "" || req.Kind == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster, namespace, kind and name are required"})
		return
	}
	if err := h.service.Dismiss(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "ok"})
}

func (h *Handler) Undismiss(c *gin.Context) {
	var req DismissRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Cluster == "" || req.Namespace == "" || req.Kind == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster, namespace, kind and name are required"})
		return
	}
	if err := h.service.Undismiss(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "ok"})
}
