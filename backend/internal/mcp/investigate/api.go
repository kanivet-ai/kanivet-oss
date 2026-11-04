package investigate

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes adds /ai/* under the (session-protected) API group.
func (m *Manager) RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/ai")
	g.GET("/agents", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"agents": m.detect()})
	})
	g.GET("/investigations", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"investigations": m.List()})
	})
	g.POST("/investigations", func(c *gin.Context) {
		var body struct {
			Agent  string `json:"agent"`
			Target Target `json:"target"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		inv, err := m.Start(body.Agent, body.Target)
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, ErrNoAgent) {
				code = http.StatusPreconditionFailed
			}
			c.JSON(code, gin.H{"error": err.Error(), "agents": m.detect()})
			return
		}
		snap, _ := m.Snapshot(inv.ID, 0)
		c.JSON(http.StatusOK, snap)
	})
	g.GET("/investigations/:id", func(c *gin.Context) {
		after, _ := strconv.Atoi(c.Query("after"))
		snap, ok := m.Snapshot(c.Param("id"), after)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "investigation not found"})
			return
		}
		c.JSON(http.StatusOK, snap)
	})
	g.POST("/investigations/:id/cancel", func(c *gin.Context) {
		if !m.Cancel(c.Param("id")) {
			c.JSON(http.StatusNotFound, gin.H{"error": "investigation not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}
