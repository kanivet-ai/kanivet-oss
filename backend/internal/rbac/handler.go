package rbac

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Handler serves the Service over HTTP under /api/v1/rbac.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register adds the routes to r (the /api/v1 group).
func (h *Handler) Register(r gin.IRouter) {
	g := r.Group("/rbac")
	g.GET("/summary", h.summary)
	g.GET("/who-can", h.whoCan)
	g.GET("/subject", h.subject)
	g.GET("/role", h.role)
	g.GET("/binding", h.binding)
	g.GET("/risks", h.risks)
	g.GET("/self", h.self)
	g.GET("/can-i", h.canI)
	g.POST("/refresh", h.refresh)
}

func fail(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, ErrNoAccess) || apierrors.IsForbidden(err) {
		code = http.StatusForbidden
	}
	c.JSON(code, gin.H{"error": err.Error()})
}

func cluster(c *gin.Context) (string, bool) {
	cl := c.Query("cluster")
	if cl == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return "", false
	}
	return cl, true
}

// queryFrom reads verb/group/resource/... ; group "core" is the core group
// and an absent group matches any.
func queryFrom(c *gin.Context) Query {
	group, ok := c.GetQuery("group")
	if !ok {
		group = "*"
	}
	if group == "core" {
		group = ""
	}
	return Query{
		Verb: c.Query("verb"), Group: group, Resource: c.Query("resource"),
		Subresource: c.Query("subresource"), Name: c.Query("name"), Namespace: c.Query("namespace"),
	}
}

func (h *Handler) summary(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	subjects, info, err := h.svc.Subjects(c.Request.Context(), cl)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"subjects": subjects, "info": info})
}

func (h *Handler) whoCan(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	q := queryFrom(c)
	if q.Verb == "" || q.Resource == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "verb and resource are required"})
		return
	}
	res, err := h.svc.WhoCan(c.Request.Context(), cl, q)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) subject(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	s := SubjectRef{Kind: c.Query("kind"), Name: c.Query("name"), Namespace: c.Query("subjectNamespace")}
	if s.Kind == "" || s.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind and name are required"})
		return
	}
	m, err := h.svc.SubjectPermissions(c.Request.Context(), cl, s, c.Query("namespace"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *Handler) role(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	m, err := h.svc.RolePermissions(c.Request.Context(), cl, c.Query("kind"), c.Query("namespace"), c.Query("name"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *Handler) binding(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	m, subjects, err := h.svc.BindingPermissions(c.Request.Context(), cl, c.Query("kind"), c.Query("namespace"), c.Query("name"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"matrix": m, "subjects": subjects})
}

func (h *Handler) risks(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	rs, err := h.svc.RiskyGrants(c.Request.Context(), cl, RiskOptions{
		IncludeSystem: c.Query("includeSystem") == "true", Namespace: c.Query("namespace"),
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"risks": rs})
}

func (h *Handler) self(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	res, err := h.svc.SelfPermissions(c.Request.Context(), cl, c.Query("namespace"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) canI(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	res, err := h.svc.SelfCanI(c.Request.Context(), cl, queryFrom(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) refresh(c *gin.Context) {
	cl, ok := cluster(c)
	if !ok {
		return
	}
	h.svc.Invalidate(cl)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
