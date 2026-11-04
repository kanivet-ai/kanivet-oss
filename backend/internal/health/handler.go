package health

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler serves the service over HTTP under /api/v1/cluster-health.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes on a group.
func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/issues", h.issues)
	g.GET("/audit", h.audit)
	g.GET("/resource", h.resource)
	g.GET("/checks", h.checks)
	g.GET("/ignores", h.listIgnores)
	g.POST("/ignores", h.addIgnore)
	g.DELETE("/ignores", h.removeIgnore)
}

func csv(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func refFromQuery(c *gin.Context) *ResourceRef {
	if c.Query("kind") == "" || c.Query("name") == "" {
		return nil
	}
	return &ResourceRef{APIVersion: c.Query("apiVersion"), Kind: c.Query("kind"), Namespace: c.Query("namespace"), Name: c.Query("name")}
}

func (h *Handler) fail(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	if err == errNoCluster {
		code = http.StatusBadRequest
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

func (h *Handler) refreshIfAsked(c *gin.Context) {
	if c.Query("refresh") == "1" {
		h.svc.Refresh(c.Query("cluster"))
	}
}

func (h *Handler) issues(c *gin.Context) {
	h.refreshIfAsked(c)
	f := IssueFilter{Cluster: c.Query("cluster"), Namespaces: csv(c.Query("namespaces")), Kinds: csv(c.Query("kinds")), Search: c.Query("search"), Resource: refFromQuery(c)}
	for _, s := range csv(c.Query("severities")) {
		f.Severities = append(f.Severities, Severity(s))
	}
	rep, err := h.svc.Issues(c.Request.Context(), f)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

func (h *Handler) audit(c *gin.Context) {
	h.refreshIfAsked(c)
	rep, err := h.svc.Audit(c.Request.Context(), AuditScope{Cluster: c.Query("cluster"), Namespaces: csv(c.Query("namespaces")), Resource: refFromQuery(c), IncludeSystem: c.Query("includeSystem") == "true"})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

func (h *Handler) resource(c *gin.Context) {
	ref := refFromQuery(c)
	if ref == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind and name are required"})
		return
	}
	rep, err := h.svc.Resource(c.Request.Context(), c.Query("cluster"), *ref)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

func (h *Handler) checks(c *gin.Context) { c.JSON(http.StatusOK, h.svc.Checks()) }

func (h *Handler) listIgnores(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Ignores(c.Query("cluster")))
}

func (h *Handler) addIgnore(c *gin.Context) {
	var ig Ignore
	if err := c.ShouldBindJSON(&ig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.AddIgnore(ig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.svc.Ignores(ig.Cluster))
}

func (h *Handler) removeIgnore(c *gin.Context) {
	var ig Ignore
	if err := c.ShouldBindJSON(&ig); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.RemoveIgnore(ig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.svc.Ignores(ig.Cluster))
}
