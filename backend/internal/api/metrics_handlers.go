package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/metrics"
)

// Metrics API endpoints

func (h *Handler) DetectMetricsProvider(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}

	providers, err := h.metrics.DetectAllProviders(cluster)
	h.respond(c, http.StatusOK, gin.H{"providers": providers}, err)
}

// GetMetricsSettings returns the saved per-cluster metrics overrides (currently
// just the Mimir tenant). Returns an empty object if nothing has been saved
// for this cluster.
func (h *Handler) GetMetricsSettings(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	settings, err := h.db.GetClusterMetricsSettings(cluster)
	if err != nil {
		h.respond(c, http.StatusOK, gin.H{"clusterName": cluster, "mimirTenant": ""}, nil)
		return
	}
	h.respond(c, http.StatusOK, settings, nil)
}

// PutMetricsSettings persists per-cluster metrics overrides — the Mimir tenant
// (X-Scope-OrgID) and/or the chosen Mimir service. Fields are pointers so a
// request updates only the keys it sends; an empty string clears that override.
func (h *Handler) PutMetricsSettings(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	var body struct {
		MimirTenant    *string `json:"mimirTenant"`
		MimirService   *string `json:"mimirService"`
		MimirNamespace *string `json:"mimirNamespace"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}
	if body.MimirTenant != nil {
		if err := h.db.SetClusterMimirTenant(cluster, *body.MimirTenant); err != nil {
			h.respond(c, http.StatusInternalServerError, nil, err)
			return
		}
	}
	if body.MimirService != nil || body.MimirNamespace != nil {
		ns, svc := "", ""
		if body.MimirNamespace != nil {
			ns = *body.MimirNamespace
		}
		if body.MimirService != nil {
			svc = *body.MimirService
		}
		if err := h.db.SetClusterMimirService(cluster, ns, svc); err != nil {
			h.respond(c, http.StatusInternalServerError, nil, err)
			return
		}
	}
	// Drop cached metrics + the cached Mimir auto-pick so the next query uses
	// the new tenant/service immediately.
	h.cache.DeleteByPrefix("metrics:")
	h.cache.DeleteByPrefix("mimir-info:")
	settings, _ := h.db.GetClusterMetricsSettings(cluster)
	if settings == nil {
		h.respond(c, http.StatusOK, gin.H{"clusterName": cluster}, nil)
		return
	}
	h.respond(c, http.StatusOK, settings, nil)
}

// ListMimirServices returns every Mimir gateway service discovered in the
// cluster so the operator can pick which one Kanivet queries. Clusters often
// expose more than one (a host-level Mimir plus vcluster-mapped copies) and
// only some hold the container metrics, so the choice can't be inferred.
func (h *Handler) ListMimirServices(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	mp := h.metrics.MimirProviderForDiscovery()
	if mp == nil {
		h.respond(c, http.StatusOK, gin.H{"services": []any{}}, nil)
		return
	}
	services, err := mp.DetectAllMimirServices(cluster)
	if err != nil {
		h.respond(c, http.StatusOK, gin.H{"services": []any{}, "error": err.Error()}, nil)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"services": services}, nil)
}

// DiscoverMetricsTenants probes the cluster's Mimir for tenants whose label
// index returns data. Optional ?hint=foo&hint=bar lets the user prime the
// candidate list with values they suspect (e.g. their org name).
func (h *Handler) DiscoverMetricsTenants(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	mp := h.metrics.MimirProviderForDiscovery()
	if mp == nil {
		h.respond(c, http.StatusOK, gin.H{"tenants": []string{}}, nil)
		return
	}
	hints := c.QueryArray("hint")
	tenants, err := mp.DiscoverTenants(cluster, hints)
	if err != nil {
		h.respond(c, http.StatusOK, gin.H{"tenants": []string{}, "error": err.Error()}, nil)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"tenants": tenants}, nil)
}

func (h *Handler) InstallMetricsProvider(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}

	var request struct {
		Provider  string `json:"provider" binding:"required"`
		Namespace string `json:"namespace"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	if request.Namespace == "" {
		request.Namespace = "kanivet-monitoring"
	}

	err := h.metrics.InstallProvider(cluster, request.Provider, request.Namespace)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to install metrics provider: %v", err))
		return
	}

	h.respond(c, http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully installed %s in namespace %s", request.Provider, request.Namespace),
	}, nil)
}

func (h *Handler) QueryPodMetrics(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}

	namespace := c.Param("namespace")
	podName := c.Param("pod")

	if namespace == "_" {
		namespace = "default"
	}

	var request struct {
		ContainerName string `json:"containerName,omitempty"`
		MetricType    string `json:"metricType" binding:"required"`
		TimeRange     string `json:"timeRange" binding:"required"`
		Provider      string `json:"provider,omitempty"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	query := metrics.MetricQuery{
		PodName:       podName,
		Namespace:     namespace,
		ContainerName: request.ContainerName,
		MetricType:    request.MetricType,
		TimeRange:     request.TimeRange,
	}

	result, err := h.metrics.QueryMetrics(cluster, request.Provider, query)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to query metrics: %v", err))
		return
	}

	h.respond(c, http.StatusOK, result, nil)
}

func (h *Handler) GetAvailableMetricProviders(c *gin.Context) {
	providers := h.metrics.GetAvailableProviders()
	h.respond(c, http.StatusOK, gin.H{"providers": providers}, nil)
}
