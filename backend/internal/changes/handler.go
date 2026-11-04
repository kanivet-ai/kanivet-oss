package changes

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Handler serves the service over HTTP.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts GET /changes/resource, /changes/snapshot and /changes/feed.
func (h *Handler) Register(r gin.IRoutes) {
	r.GET("/changes/resource", h.resource)
	r.GET("/changes/snapshot", h.snapshot)
	r.GET("/changes/feed", h.feed)
}

func status(err error) int {
	switch {
	case apierrors.IsNotFound(err):
		return http.StatusNotFound
	case apierrors.IsForbidden(err):
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

func (h *Handler) resource(c *gin.Context) {
	ref := ResourceRef{
		Cluster: c.Query("cluster"), Group: c.Query("group"), Version: c.Query("version"),
		Kind: c.Query("kind"), Namespace: c.Query("namespace"), Name: c.Query("name"),
	}
	if ref.Cluster == "" || ref.Kind == "" || ref.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster, kind and name are required"})
		return
	}
	hist, err := h.svc.ChangesFor(c.Request.Context(), ref)
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, hist)
}

func (h *Handler) snapshot(c *gin.Context) {
	snap, err := h.svc.Snapshot(c.Request.Context(), c.Query("cluster"), c.Query("id"))
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Query("id"), "snapshot": snap})
}

func csv(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (h *Handler) feed(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster is required"})
		return
	}
	f := Filter{Namespaces: csv(c.Query("namespaces")), Kinds: csv(c.Query("kinds"))}
	for _, v := range csv(c.Query("categories")) {
		f.Categories = append(f.Categories, Category(v))
	}
	for _, v := range csv(c.Query("sources")) {
		f.Sources = append(f.Sources, Source(v))
	}
	if s := c.Query("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Since = t
		}
	}
	if l, err := strconv.Atoi(c.Query("limit")); err == nil {
		f.Limit = l
	}
	feed, err := h.svc.RecentChanges(c.Request.Context(), cluster, f)
	if err != nil {
		c.JSON(status(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, feed)
}
