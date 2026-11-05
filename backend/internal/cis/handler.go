package cis

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler serves the service under /api/v1/cluster-health/cis.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes on a group.
func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("", h.benchmark)
	g.GET("/provider", h.provider)
	g.GET("/override", h.getOverride)
	g.PUT("/override", h.setOverride)
}

func (h *Handler) fail(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	if err == errNoCluster {
		code = http.StatusBadRequest
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

func (h *Handler) benchmark(c *gin.Context) {
	rep, err := h.svc.Benchmark(c.Request.Context(), Options{Cluster: c.Query("cluster"), Profile: c.Query("profile"), Refresh: c.Query("refresh") == "1"})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

func (h *Handler) provider(c *gin.Context) {
	info, err := h.svc.DetectProvider(c.Request.Context(), c.Query("cluster"), c.Query("refresh") == "1")
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

func (h *Handler) getOverride(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Override(c.Query("cluster")))
}

func (h *Handler) setOverride(c *gin.Context) {
	var o Override
	if err := c.ShouldBindJSON(&o); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.SetOverride(c.Query("cluster"), o); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.svc.Override(c.Query("cluster")))
}
