package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/models"
)

func (h *Handler) AddNavigationEntry(c *gin.Context) {
	tabID := strings.TrimPrefix(c.Param("tabId"), "/")
	var req struct {
		ClusterID string                 `json:"clusterId"`
		Entry     models.NavigationEntry `json:"entry"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.navigation.AddEntry(tabID, req.ClusterID, req.Entry)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) GetNavigationHistory(c *gin.Context) {
	tabID := strings.TrimPrefix(c.Param("tabId"), "/")
	history := h.navigation.GetHistory(tabID)
	if history == nil {
		c.JSON(http.StatusOK, gin.H{"history": []models.NavigationEntry{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": history.Entries})
}

func (h *Handler) NavigateBack(c *gin.Context) {
	tabID := strings.TrimPrefix(c.Param("tabId"), "/")
	entry := h.navigation.NavigateBack(tabID)
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No previous navigation entry"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

func (h *Handler) NavigateForward(c *gin.Context) {
	tabID := strings.TrimPrefix(c.Param("tabId"), "/")
	entry := h.navigation.NavigateForward(tabID)
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No forward navigation entry"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

func (h *Handler) ClearNavigationHistory(c *gin.Context) {
	tabID := strings.TrimPrefix(c.Param("tabId"), "/")
	h.navigation.ClearHistory(tabID)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
