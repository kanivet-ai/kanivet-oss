package certs

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler serves the certificate report over HTTP.
type Handler struct{ service *Service }

func NewHandler(s *Service) *Handler { return &Handler{service: s} }

// ParseFilter reads a Filter from query parameters:
// namespace, status (comma list), expiringWithin (days), search, source,
// kind + resourceNamespace + name (resource scope), refresh=1.
func ParseFilter(q func(string) string) Filter {
	f := Filter{
		Namespace: q("namespace"),
		Search:    q("search"),
		Source:    q("source"),
		Refresh:   q("refresh") == "1",
	}
	if s := q("status"); s != "" {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				f.Statuses = append(f.Statuses, Status(p))
			}
		}
	}
	if d, err := strconv.Atoi(q("expiringWithin")); err == nil && d > 0 {
		f.ExpiringWithinDays = d
	}
	if kind, name := q("kind"), q("name"); kind != "" && name != "" {
		f.Resource = &Ref{Kind: kind, Name: name, Namespace: q("resourceNamespace")}
	}
	return f
}

// List handles GET /certs.
func (h *Handler) List(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter required"})
		return
	}
	r, err := h.service.Certificates(c.Request.Context(), cluster, ParseFilter(c.Query))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": r})
}
