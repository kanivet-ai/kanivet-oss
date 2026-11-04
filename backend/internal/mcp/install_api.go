package mcp

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/kanivet/backend/internal/mcp/install"
)

var (
	installerOnce sync.Once
	installer     *install.Manager
)

func (s *Service) installer() *install.Manager {
	installerOnce.Do(func() { installer = install.New(install.DefaultEnv()) })
	return installer
}

// InstallTarget is what client entries should contain right now.
func (s *Service) InstallTarget() install.Target {
	t := install.Target{ReadOnlyURL: s.ReadOnlyURL(), FullURL: s.FullURL(), Token: s.Token(), BridgeArgs: []string{"mcp-stdio"}}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		t.BridgeCommand = exe
	}
	return t
}

func (s *Service) registerInstallAPI(g *gin.RouterGroup) {
	clients := func(c *gin.Context, err error) {
		body := gin.H{"clients": s.installer().Statuses(c.Request.Context(), s.InstallTarget())}
		if err != nil {
			body["error"] = err.Error()
		}
		c.JSON(http.StatusOK, body)
	}
	g.GET("/clients", func(c *gin.Context) { clients(c, nil) })
	g.GET("/clients/:id/plan", func(c *gin.Context) {
		action := c.DefaultQuery("action", "install")
		p, err := s.installer().Plan(c.Param("id"), s.InstallTarget(), install.Mode(c.Query("mode")), action)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, p)
	})
	g.POST("/clients/:id/install", func(c *gin.Context) {
		var body struct {
			Mode install.Mode `json:"mode"`
		}
		_ = c.ShouldBindJSON(&body)
		st, err := s.installer().Install(c.Request.Context(), c.Param("id"), s.InstallTarget(), body.Mode)
		resp := gin.H{"client": st}
		if err != nil {
			resp["error"] = err.Error()
		}
		c.JSON(http.StatusOK, resp)
	})
	g.POST("/clients/:id/uninstall", func(c *gin.Context) {
		st, err := s.installer().Uninstall(c.Request.Context(), c.Param("id"), s.InstallTarget())
		resp := gin.H{"client": st}
		if err != nil {
			resp["error"] = err.Error()
		}
		c.JSON(http.StatusOK, resp)
	})
	g.POST("/clients/update-all", func(c *gin.Context) {
		_, err := s.installer().UpdateOutdated(c.Request.Context(), s.InstallTarget())
		clients(c, err)
	})
}
