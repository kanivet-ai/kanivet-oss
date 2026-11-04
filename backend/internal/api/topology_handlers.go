package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kanivet/backend/internal/topology"
)

var (
	topologyOnce sync.Once
	topologySvc  *topology.Service
)

// Topology returns the handler's shared topology service (one namespace
// snapshot cache for HTTP and any MCP wrapper).
func (h *Handler) Topology() *topology.Service {
	topologyOnce.Do(func() {
		topologySvc = topology.NewService(func(cluster string) (topology.Source, error) {
			dyn, err := h.k8s.GetDynamicClient(cluster)
			if err != nil {
				return nil, err
			}
			md, err := h.k8s.GetMetadataClient(cluster)
			if err != nil {
				return nil, err
			}
			return &topology.ClientSource{Dynamic: dyn, Metadata: md}, nil
		}, topology.DefaultTTL)
	})
	return topologySvc
}

func topologyStatus(err error) int {
	if strings.Contains(err.Error(), "required") {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

// GetTopologyNeighbors: GET /cluster/topology/neighbors/:group/:version/:kind/:namespace/:name
// ?depth=1&context=<namespace for cluster-scoped kinds>&includeIdle=true
func (h *Handler) GetTopologyNeighbors(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	ref := topology.Ref{
		Group: c.Param("group"), Version: c.Param("version"), Kind: c.Param("kind"),
		Namespace: c.Param("namespace"), Name: c.Param("name"),
	}
	if ref.Group == "_" || ref.Group == "core" {
		ref.Group = ""
	}
	if ref.Namespace == "_" {
		ref.Namespace = ""
	}
	depth, _ := strconv.Atoi(c.DefaultQuery("depth", "1"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	g, err := h.Topology().Neighbors(ctx, cluster, ref, c.Query("context"), depth, c.Query("includeIdle") == "true")
	if err != nil {
		h.respond(c, topologyStatus(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, g, nil)
}

// GetTopologyNamespace: GET /cluster/topology/namespace/:namespace?kinds=Pod,Service&includeIdle=true
func (h *Handler) GetTopologyNamespace(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	f := topology.Filters{IncludeIdle: c.Query("includeIdle") == "true"}
	if k := c.Query("kinds"); k != "" {
		f.Kinds = strings.Split(k, ",")
	}
	f.MaxNodes, _ = strconv.Atoi(c.Query("max"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	g, err := h.Topology().NamespaceGraph(ctx, cluster, c.Param("namespace"), f)
	if err != nil {
		h.respond(c, topologyStatus(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, g, nil)
}

// GetTopologyOverview: GET /cluster/topology/overview, the per-namespace
// health rollup behind the cluster view.
func (h *Handler) GetTopologyOverview(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	ov, err := h.Topology().ClusterOverview(ctx, cluster)
	if err != nil {
		h.respond(c, topologyStatus(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, ov, nil)
}
