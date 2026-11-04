package network

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler serves the network API under /api/v1/network.
type Handler struct {
	service *Service
}

func NewHandler(s *Service) *Handler { return &Handler{service: s} }

// Register adds the routes to a router group (v1.Group("/network")).
func (h *Handler) Register(g gin.IRoutes) {
	g.GET("/targets", h.GetTargets)
	g.POST("/path", h.CheckPath)
	g.GET("/coverage", h.GetCoverage)
	g.GET("/resource", h.GetResource)
	g.GET("/policy", h.GetPolicy)
	g.GET("/flows", h.GetFlows)
	g.POST("/probe", h.Probe)
}

func cluster(c *gin.Context) (string, bool) {
	cl := c.Query("cluster")
	if cl == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return "", false
	}
	return cl, true
}

func fail(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func (h *Handler) GetTargets(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	t, err := h.service.Targets(c.Request.Context(), cl)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": t})
}

func (h *Handler) CheckPath(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	var req PathRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	res, err := h.service.CheckPath(c.Request.Context(), cl, req)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *Handler) GetCoverage(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	rep, err := h.service.PolicyCoverage(c.Request.Context(), cl, c.Query("namespace"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rep})
}

func (h *Handler) GetResource(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	r := Ref{Kind: c.Query("kind"), Namespace: c.Query("namespace"), Name: c.Query("name")}
	if r.Kind == "" || r.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind and name are required"})
		return
	}
	v, err := h.service.ResourceNetwork(c.Request.Context(), cl, r)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": v})
}

func (h *Handler) GetPolicy(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	d, err := h.service.PolicyDetail(c.Request.Context(), cl, c.Query("namespace"), c.Query("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

func (h *Handler) GetFlows(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	window := 5 * time.Minute
	if m, err := strconv.Atoi(c.Query("windowMinutes")); err == nil && m > 0 && m <= 24*60 {
		window = time.Duration(m) * time.Minute
	}
	c.JSON(http.StatusOK, gin.H{"data": h.service.Flows(c.Request.Context(), cl, c.Query("namespace"), window)})
}

func (h *Handler) Probe(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	var req ProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	res, err := h.service.Probe(c.Request.Context(), cl, req)
	if errors.Is(err, ErrNotConfirmed) {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}
