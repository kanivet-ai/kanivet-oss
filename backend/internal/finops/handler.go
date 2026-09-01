package finops

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

type streamChunk struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) GetClusterCostSummary(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	summary, err := h.service.GetClusterCostSummary(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": summary})
}

func (h *Handler) GetNodeCosts(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	costs, err := h.service.GetNodeCosts(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": costs})
}

func (h *Handler) GetPodCosts(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	namespace := c.Query("namespace")
	costs, err := h.service.GetPodCosts(c.Request.Context(), cluster, namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": costs})
}

func (h *Handler) GetNamespaceCosts(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	costs, err := h.service.GetNamespaceCosts(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": costs})
}

func (h *Handler) GetWorkloadCosts(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	namespace := c.Query("namespace")
	costs, err := h.service.GetWorkloadCosts(c.Request.Context(), cluster, namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": costs})
}

func (h *Handler) GetCostRecommendations(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	recommendations, err := h.service.GetCostRecommendations(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": recommendations})
}

func (h *Handler) GetPricingStatus(c *gin.Context) {
	status := h.service.GetPricingStatus()
	c.JSON(http.StatusOK, gin.H{"data": status})
}

func (h *Handler) PreloadPricing(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}
	go h.service.PreloadPricing(c.Request.Context(), cluster)
	c.JSON(http.StatusOK, gin.H{"data": "preloading"})
}

func (h *Handler) GetPricingDebug(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	debug, err := h.service.GetPricingDebug(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": debug})
}

func (h *Handler) StreamDashboardEndpoint(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	c.Writer.Header().Set("Content-Type", "application/x-ndjson")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)
	enc := json.NewEncoder(c.Writer)
	var mu sync.Mutex
	send := func(t string, d interface{}) {
		mu.Lock()
		defer mu.Unlock()
		if err := enc.Encode(streamChunk{Type: t, Data: d}); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	if err := h.service.StreamDashboard(c.Request.Context(), cluster, send); err != nil {
		send("error", err.Error())
	}
	send("done", nil)
}

func (h *Handler) GetDashboard(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	dashboard, err := h.service.GetDashboard(c.Request.Context(), cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": dashboard})
}

func (h *Handler) GetResourceCost(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}

	kind := c.Param("kind")
	namespace := c.Param("namespace")
	name := c.Param("name")

	if kind == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind and name are required"})
		return
	}

	ctx := c.Request.Context()
	var cost interface{}
	var err error

	switch kind {
	case "Pod":
		pods, e := h.service.GetPodCosts(ctx, cluster, namespace)
		if e != nil {
			err = e
			break
		}
		for _, p := range pods {
			if p.PodName == name && p.Namespace == namespace {
				cost = p
				break
			}
		}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		workloads, e := h.service.GetWorkloadCosts(ctx, cluster, namespace)
		if e != nil {
			err = e
			break
		}
		for _, w := range workloads {
			if w.Name == name && w.Namespace == namespace && w.Kind == kind {
				cost = w
				break
			}
		}
	case "Namespace":
		namespaces, e := h.service.GetNamespaceCosts(ctx, cluster)
		if e != nil {
			err = e
			break
		}
		for _, n := range namespaces {
			if n.Namespace == name {
				cost = n
				break
			}
		}
	case "Node":
		nodes, e := h.service.GetNodeCosts(ctx, cluster)
		if e != nil {
			err = e
			break
		}
		for _, n := range nodes {
			if n.NodeName == name {
				cost = n
				break
			}
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported resource kind for cost calculation"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if cost == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found or has no cost data"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": cost})
}
