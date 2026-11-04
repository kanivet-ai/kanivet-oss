package upgrade

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler serves the upgrade readiness HTTP API.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// GetReport: GET /api/v1/upgrade/report?cluster=&target=1.31&refresh=1
func (h *Handler) GetReport(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}
	rep, err := h.service.UpgradeReport(c.Request.Context(), cluster, c.Query("target"), c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rep)
}

// GetCatalog: GET /api/v1/upgrade/catalog. The inspector badge checks an
// object's apiVersion and last-applied apiVersion against it.
func (h *Handler) GetCatalog(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.Catalog())
}
