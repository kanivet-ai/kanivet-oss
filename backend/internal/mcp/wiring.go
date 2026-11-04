package mcp

import (
	"log"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/kanivet/backend/internal/mcp/investigate"
)

// Lazy lets main mount /mcp and /mcp-readonly before the session-secret
// middleware is installed, while the service itself is built later, once
// the services it reads from exist. Requests before Set get 503.
type Lazy struct{ p atomic.Pointer[Service] }

// Mount registers the endpoints on router.
func (l *Lazy) Mount(router gin.IRoutes) {
	h := func(full bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			s := l.p.Load()
			if s == nil {
				http.Error(c.Writer, "kanivet mcp: starting", http.StatusServiceUnavailable)
				return
			}
			s.Handler(full).ServeHTTP(c.Writer, c.Request)
		}
	}
	router.Any("/mcp", h(true))
	router.Any("/mcp-readonly", h(false))
}

// Set publishes the service.
func (l *Lazy) Set(s *Service) { l.p.Store(s) }

// Setup builds the MCP service and the investigation manager, registers their
// UI routes on v1 and publishes the service on lazy. It logs and returns nil
// when the state directory is unusable, leaving the rest of the backend up.
func Setup(lazy *Lazy, v1 *gin.RouterGroup, deps Deps) *Service {
	svc, err := New(DefaultStateDir(), deps)
	if err != nil {
		log.Printf("[MCP] disabled: %v", err)
		return nil
	}
	svc.RegisterAPI(v1)
	inv := investigate.NewManager(func() (string, string, bool) {
		st := svc.Store().Settings()
		return svc.ReadOnlyURL(), st.Token, st.Enabled
	})
	inv.RegisterRoutes(v1)
	lazy.Set(svc)
	return svc
}
