package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/incidents"
)

type IncidentTimelineHandler struct {
	service *incidents.Service
}

func NewIncidentTimelineHandler(service *incidents.Service) *IncidentTimelineHandler {
	return &IncidentTimelineHandler{service: service}
}

func (h *IncidentTimelineHandler) GetTimeline(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cluster parameter is required"})
		return
	}

	f := incidents.TimelineFilters{
		Cluster:        cluster,
		Namespaces:     splitCSVParam(c.Query("namespaces")),
		Kinds:          splitCSVParam(c.Query("kinds")),
		Search:         c.Query("search"),
		IncludeRoutine: c.Query("includeRoutine") == "true",
		Limit:          parseIntDefault(c.Query("limit"), 500),
	}
	for _, s := range splitCSVParam(c.Query("severities")) {
		f.Severities = append(f.Severities, incidents.Severity(s))
	}
	if since := c.Query("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			f.Since = t
		}
	}
	if until := c.Query("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			f.Until = t
		}
	}

	resp, err := h.service.GetTimeline(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func splitCSVParam(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
