package helm

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ListReleases(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Query("namespace")
	allNamespaces := c.Query("allNamespaces") == "true" || namespace == ""

	releases, err := h.service.ListReleases(c.Request.Context(), cluster, namespace, allNamespaces)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, releases)
}

func (h *Handler) GetRelease(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")

	if namespace == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace and name are required"})
		return
	}

	release, err := h.service.GetRelease(c.Request.Context(), cluster, namespace, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, release)
}

func (h *Handler) GetReleaseValues(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")
	allValues := c.Query("all") == "true"

	values, err := h.service.GetReleaseValues(c.Request.Context(), cluster, namespace, name, allValues)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, values)
}

func (h *Handler) GetReleaseManifest(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")

	manifest, err := h.service.GetReleaseManifest(c.Request.Context(), cluster, namespace, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"manifest": manifest})
}

func (h *Handler) GetReleaseHistory(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")

	// Default limit for "load more" pattern
	limit := 10
	if limitStr := c.Query("limit"); limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	// Fetch one extra to detect if there are more
	history, err := h.service.GetReleaseHistory(c.Request.Context(), cluster, namespace, name, limit+1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hasMore := len(history) > limit
	if hasMore {
		history = history[:limit] // Trim to requested limit
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": history,
		"hasMore": hasMore,
	})
}

func (h *Handler) RollbackRelease(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")

	var req struct {
		Revision int `json:"revision"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "revision is required"})
		return
	}

	if err := h.service.RollbackRelease(c.Request.Context(), cluster, namespace, name, req.Revision); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "rollback successful"})
}

func (h *Handler) UninstallRelease(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")
	keepHistory := c.Query("keepHistory") == "true"

	if err := h.service.UninstallRelease(c.Request.Context(), cluster, namespace, name, keepHistory); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "uninstall successful"})
}

func (h *Handler) UpgradeReleaseValues(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	namespace := c.Param("namespace")
	name := c.Param("name")

	var req struct {
		Values map[string]any `json:"values"`
		DryRun bool           `json:"dryRun"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	release, manifest, err := h.service.UpgradeReleaseValues(c.Request.Context(), cluster, namespace, name, req.Values, req.DryRun)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"release":  release,
		"manifest": manifest,
		"dryRun":   req.DryRun,
	})
}

