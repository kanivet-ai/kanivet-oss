package themes

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) ListThemes(c *gin.Context) {
	themes, err := h.service.ListAllThemes()
	if err != nil {
		log.Printf("Failed to list themes: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"themes": themes})
}

func (h *Handler) GetTheme(c *gin.Context) {
	themeID := c.Param("id")
	if themeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "theme ID is required"})
		return
	}

	theme, err := h.service.GetTheme(themeID)
	if err != nil {
		log.Printf("Failed to get theme %s: %v", themeID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"theme": theme})
}

func (h *Handler) DeleteTheme(c *gin.Context) {
	themeID := c.Param("id")
	if themeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "theme ID is required"})
		return
	}

	if err := h.service.DeleteTheme(themeID); err != nil {
		log.Printf("Failed to delete theme %s: %v", themeID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "theme deleted successfully"})
}

func (h *Handler) ApplyTheme(c *gin.Context) {
	var req ApplyThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Invalid apply theme request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.ApplyTheme(req.ThemeID); err != nil {
		log.Printf("Failed to apply theme %s: %v", req.ThemeID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "theme applied successfully"})
}

func (h *Handler) GetCurrentSettings(c *gin.Context) {
	settings, err := h.service.GetSettings()
	if err != nil {
		log.Printf("Failed to get theme settings: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

func (h *Handler) SearchMarketplaceThemes(c *gin.Context) {
	query := c.Query("q")
	limitStr := c.DefaultQuery("limit", "50")

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		log.Printf("Invalid limit parameter: %s", limitStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
		return
	}

	themes, err := h.service.SearchMarketplaceThemes(query, limit)
	if err != nil {
		log.Printf("Failed to search marketplace themes (query=%s, limit=%d): %v", query, limit, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"themes": themes})
}

func (h *Handler) DownloadTheme(c *gin.Context) {
	var req DownloadThemeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Invalid download theme request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Downloading theme: %s from %s", req.MarketplaceTheme.DisplayName, req.MarketplaceTheme.DownloadURL)
	theme, err := h.service.DownloadAndInstallTheme(&req.MarketplaceTheme)
	if err != nil {
		log.Printf("Failed to download and install theme %s: %v", req.MarketplaceTheme.DisplayName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Successfully downloaded and installed theme: %s", theme.Name)
	c.JSON(http.StatusOK, gin.H{"theme": theme})
}
