package awsidentity

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/faults"
	"github.com/kanivet/backend/internal/k8s"
)

const (
	explainTimeout    = 30 * time.Second
	identitiesTimeout = 60 * time.Second
)

type Handler struct {
	svc *Service
}

func NewHandler(k k8s.Interface, cloudSvc *cloud.Service, c *cache.Cache, database *db.DB) *Handler {
	return &Handler{svc: NewService(k, cloudSvc, c, database)}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/cluster/aws/identity/status", h.Status)
	rg.GET("/cluster/aws/identity/explain", h.Explain)
	rg.GET("/cluster/aws/identity/identities", h.Identities)
	rg.POST("/cluster/aws/identity/simulate", h.Simulate)
	rg.GET("/cluster/aws/identity/credentials", h.GetCredentials)
	rg.PUT("/cluster/aws/identity/credentials", h.SetCredentials)
	rg.DELETE("/cluster/aws/identity/credentials", h.ClearCredentials)
}

func requireCluster(c *gin.Context) (string, bool) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return "", false
	}
	return cluster, true
}

func respondError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrBadRequest):
		code = http.StatusBadRequest
	case apierrors.IsNotFound(err):
		code = http.StatusNotFound
	case apierrors.IsForbidden(err):
		code = http.StatusForbidden
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	}
	if code >= 500 {
		faults.CaptureExceptionWithContext(err, map[string]any{
			"path":   c.Request.URL.Path,
			"method": c.Request.Method,
			"status": code,
		})
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

func (h *Handler) Status(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	c.JSON(http.StatusOK, h.svc.Status(ctx, cluster))
}

func (h *Handler) Explain(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	exp, err := h.svc.Explain(ctx, cluster, c.Query("namespace"), c.Query("serviceAccount"), c.Query("pod"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, exp)
}

func (h *Handler) Identities(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), identitiesTimeout)
	defer cancel()
	resp, err := h.svc.ListIdentities(ctx, cluster)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) Simulate(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	var req SimulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	resp, err := h.svc.Simulate(ctx, cluster, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetCredentials(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	c.JSON(http.StatusOK, h.svc.GetCredentials(ctx, cluster))
}

func (h *Handler) SetCredentials(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	var override CredentialsOverride
	if err := c.ShouldBindJSON(&override); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	creds, err := h.svc.SetCredentialsOverride(ctx, cluster, override)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, creds)
}

func (h *Handler) ClearCredentials(c *gin.Context) {
	cluster, ok := requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), explainTimeout)
	defer cancel()
	creds, err := h.svc.ClearCredentialsOverride(ctx, cluster)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, creds)
}
