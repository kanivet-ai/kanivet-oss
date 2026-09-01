package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/crossplane"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (h *Handler) ListClusters(c *gin.Context) {
	start := time.Now()
	data, err := h.cache.GetOrSet("clusters", 1*time.Minute, func() (interface{}, error) {
		clusters, err := h.k8s.ListClusters()
		if err == nil {
			log.Printf("[API] ListClusters fetched %d clusters in %v", len(clusters), time.Since(start))
		}
		return clusters, err
	})
	log.Printf("[API] ListClusters total took %v (cache hit: %v)", time.Since(start), time.Since(start) < 5*time.Millisecond)
	h.respond(c, http.StatusOK, gin.H{"clusters": data}, err)
}

func (h *Handler) RefreshClusters(c *gin.Context) {
	h.cache.Delete("clusters")
	h.k8s.RefreshClusterCache("")
	clusters, err := h.k8s.ListClusters()
	if err != nil {
		h.respond(c, http.StatusInternalServerError, gin.H{"clusters": []interface{}{}}, err)
		return
	}
	log.Printf("RefreshClusters - Found %d clusters", len(clusters))
	h.cache.Set("clusters", clusters, 1*time.Minute)
	h.respond(c, http.StatusOK, gin.H{"clusters": clusters}, nil)
}

func (h *Handler) GetClusterStatusQuery(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	force := c.Query("force") == "true"
	if h.statusManager != nil && !force {
		if status := h.statusManager.GetStatus(cluster); status != nil {
			if !status.Healthy && status.Error != "" {
				h.broadcastClusterError(cluster, status.Error)
			}
			h.respond(c, http.StatusOK, status, nil)
			return
		}
	}
	if force {
		h.k8s.RefreshClusterCache(cluster)
		if h.statusManager != nil {
			if status := h.statusManager.RefreshCluster(cluster); status != nil {
				if !status.Healthy && status.Error != "" {
					h.broadcastClusterError(cluster, status.Error)
				}
				h.respond(c, http.StatusOK, status, nil)
				return
			}
		}
	}
	cacheKey := h.cache.BuildKey("status", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 30*time.Second, func() (interface{}, error) {
		return h.k8s.GetClusterStatus(cluster)
	})
	if status, ok := data.(*clusterStatus); ok && status != nil {
		if !status.Healthy && status.Error != "" {
			h.broadcastClusterError(cluster, status.Error)
		}
	}
	h.respond(c, http.StatusOK, data, err)
}

func (h *Handler) GetBatchClusterStatus(c *gin.Context) {
	var payload struct {
		Clusters   []string `json:"clusters" binding:"required"`
		Force      bool     `json:"force"`
		CachedOnly bool     `json:"cachedOnly"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	if len(payload.Clusters) == 0 {
		h.respond(c, http.StatusOK, gin.H{"statuses": map[string]interface{}{}}, nil)
		return
	}
	statuses := make(map[string]interface{})
	if payload.CachedOnly {
		if h.statusManager != nil {
			allStatuses := h.statusManager.GetAllStatuses()
			for _, cl := range payload.Clusters {
				if status, ok := allStatuses[cl]; ok {
					statuses[cl] = status
				}
			}
		}
		h.respond(c, http.StatusOK, gin.H{"statuses": statuses}, nil)
		return
	}
	if len(payload.Clusters) > 200 {
		payload.Clusters = payload.Clusters[:200]
	}
	if h.statusManager != nil && !payload.Force {
		allStatuses := h.statusManager.GetAllStatuses()
		for _, cl := range payload.Clusters {
			if status, ok := allStatuses[cl]; ok {
				statuses[cl] = status
			}
		}
		if len(statuses) == len(payload.Clusters) {
			h.respond(c, http.StatusOK, gin.H{"statuses": statuses}, nil)
			return
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 20)
	for _, cluster := range payload.Clusters {
		if _, exists := statuses[cluster]; exists {
			continue
		}
		wg.Add(1)
		go func(cl string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if payload.Force {
				h.k8s.RefreshClusterCache(cl)
				if h.statusManager != nil {
					if status := h.statusManager.RefreshCluster(cl); status != nil {
						mu.Lock()
						statuses[cl] = status
						mu.Unlock()
						return
					}
				}
			}
			cacheKey := h.cache.BuildKey("status", cl)
			data, _ := h.cache.GetOrSet(cacheKey, 30*time.Second, func() (interface{}, error) {
				return h.k8s.GetClusterStatus(cl)
			})
			mu.Lock()
			statuses[cl] = data
			mu.Unlock()
		}(cluster)
	}
	wg.Wait()
	h.respond(c, http.StatusOK, gin.H{"statuses": statuses}, nil)
}

func (h *Handler) ListNamespaces(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	cacheKey := h.cache.BuildKey("namespaces", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 1*time.Minute, func() (interface{}, error) {
		return h.k8s.ListNamespaces(cluster)
	})
	h.respond(c, http.StatusOK, gin.H{"namespaces": data}, err)
}

func (h *Handler) ListClusterGroups(c *gin.Context) {
	start := time.Now()
	if h.db == nil {
		log.Printf("ERROR: Database not initialized in ListClusterGroups")
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	groups, err := h.db.GetGroups()
	log.Printf("[API] ListClusterGroups took %v", time.Since(start))
	if err != nil {
		log.Printf("ERROR: Failed to get cluster groups: %v", err)
	}
	h.respond(c, http.StatusOK, gin.H{"groups": groups}, err)
}

func (h *Handler) CreateClusterGroup(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	var payload struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	group, err := h.db.CreateGroup(payload.Name, payload.Description)
	h.respond(c, http.StatusCreated, group, err)
}

func (h *Handler) UpdateGroupOrder(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	var payload []struct {
		ID    uint `json:"id"`
		Order int  `json:"order"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		log.Printf("Failed to bind group order payload: %v", err)
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	groupOrders := make([]struct {
		ID    uint
		Order int
	}, len(payload))
	for i, p := range payload {
		groupOrders[i] = struct {
			ID    uint
			Order int
		}{ID: p.ID, Order: p.Order}
	}
	err := h.db.UpdateGroupOrder(groupOrders)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) UpdateClusterGroup(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	id := c.Param("id")
	var groupID uint
	if _, err := fmt.Sscan(id, &groupID); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid group ID"))
		return
	}
	var payload struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	err := h.db.UpdateGroup(groupID, payload.Name, payload.Description)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) DeleteClusterGroup(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	id := c.Param("id")
	var groupID uint
	if _, err := fmt.Sscan(id, &groupID); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid group ID"))
		return
	}
	err := h.db.DeleteGroup(groupID)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) AssignClusterToGroup(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	var payload struct {
		ClusterName string `json:"clusterName" binding:"required"`
		GroupID     uint   `json:"groupId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	err := h.db.AssignClusterToGroup(payload.ClusterName, payload.GroupID)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) RemoveClusterFromGroup(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	clusterName := c.Param("clusterName")
	if len(clusterName) > 0 && clusterName[0] == '/' {
		clusterName = clusterName[1:]
	}
	err := h.db.RemoveClusterFromGroup(clusterName)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) GetClustersByGroup(c *gin.Context) {
	start := time.Now()
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	clusters, err := h.db.GetClustersByGroup()
	log.Printf("[API] GetClustersByGroup took %v", time.Since(start))
	h.respond(c, http.StatusOK, gin.H{"clustersByGroup": clusters}, err)
}

func (h *Handler) GetClusterAssignments(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	assignments, err := h.db.GetAllClusterAssignments()
	h.respond(c, http.StatusOK, gin.H{"assignments": assignments}, err)
}

func (h *Handler) GetClusterAliases(c *gin.Context) {
	start := time.Now()
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	aliases, err := h.db.GetAllClusterAliases()
	log.Printf("[API] GetClusterAliases took %v", time.Since(start))
	h.respond(c, http.StatusOK, gin.H{"aliases": aliases}, err)
}

func (h *Handler) SetClusterAlias(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	var payload struct {
		ClusterName string `json:"clusterName" binding:"required"`
		Alias       string `json:"alias" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	err := h.db.SetClusterAlias(payload.ClusterName, payload.Alias)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) DeleteClusterAlias(c *gin.Context) {
	if h.db == nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("database not initialized"))
		return
	}
	clusterName := c.Param("clusterName")
	if len(clusterName) > 0 && clusterName[0] == '/' {
		clusterName = clusterName[1:]
	}
	err := h.db.DeleteClusterAlias(clusterName)
	h.respond(c, http.StatusOK, gin.H{"success": err == nil}, err)
}

func (h *Handler) TaintNode(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeName := c.Param("name")

	if cluster == "" || nodeName == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster and node name are required"))
		return
	}

	var payload struct {
		Key    string `json:"key" binding:"required"`
		Value  string `json:"value"`
		Effect string `json:"effect" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request payload: %v", err))
		return
	}

	ctx := c.Request.Context()
	err := h.k8s.TaintNode(ctx, cluster, nodeName, payload.Key, payload.Value, payload.Effect)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to taint node: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, "", "v1", "Node", "", nodeName)
	h.cache.Delete(detailKey)
	topic := fmt.Sprintf("items:%s::v1:nodes:", cluster)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully tainted node %s", nodeName),
	}, nil)
}

func (h *Handler) RemoveTaint(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeName := c.Param("name")

	if cluster == "" || nodeName == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster and node name are required"))
		return
	}

	var payload struct {
		Key string `json:"key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request payload: %v", err))
		return
	}

	ctx := c.Request.Context()
	err := h.k8s.RemoveTaint(ctx, cluster, nodeName, payload.Key)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to remove taint: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, "", "v1", "Node", "", nodeName)
	h.cache.Delete(detailKey)
	topic := fmt.Sprintf("items:%s::v1:nodes:", cluster)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully removed taint from node %s", nodeName),
	}, nil)
}

func (h *Handler) DrainNode(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeName := c.Param("name")

	if cluster == "" || nodeName == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster and node name are required"))
		return
	}

	var payload struct {
		IgnoreDaemonsets bool `json:"ignoreDaemonsets"`
		DeleteEmptyDir   bool `json:"deleteEmptyDir"`
		GracePeriod      int  `json:"gracePeriod"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request payload: %v", err))
		return
	}
	if payload.GracePeriod == 0 {
		payload.GracePeriod = 30
	}

	ctx := c.Request.Context()
	result, err := h.k8s.DrainNode(ctx, cluster, nodeName, payload.IgnoreDaemonsets, payload.DeleteEmptyDir, payload.GracePeriod)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to drain node: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, "", "v1", "Node", "", nodeName)
	h.cache.Delete(detailKey)
	nodeTopic := fmt.Sprintf("items:%s::v1:nodes:", cluster)
	podTopic := fmt.Sprintf("items:%s::v1:pods:*", cluster)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(nodeTopic)
		h.invalidationBus.InvalidatePattern(podTopic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully drained node %s", nodeName),
		"result":  result,
	}, nil)
}

func (h *Handler) CordonNode(c *gin.Context) {
	cluster := c.Query("cluster")
	nodeName := c.Param("name")

	if cluster == "" || nodeName == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster and node name are required"))
		return
	}

	var payload struct {
		Unschedulable bool `json:"unschedulable"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request payload: %v", err))
		return
	}

	ctx := c.Request.Context()
	err := h.k8s.CordonNode(ctx, cluster, nodeName, payload.Unschedulable)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to cordon/uncordon node: %v", err))
		return
	}

	action := "cordoned"
	if !payload.Unschedulable {
		action = "uncordoned"
	}

	detailKey := h.cache.BuildKey("detail", cluster, "", "v1", "Node", "", nodeName)
	h.cache.Delete(detailKey)
	topic := fmt.Sprintf("items:%s::v1:nodes:", cluster)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Successfully %s node %s", action, nodeName),
	}, nil)
}

func (h *Handler) CheckCrossplaneResource(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	group := c.Param("group")
	version := c.Param("version")
	kind := c.Param("kind")

	if group == "_" {
		group = ""
	}

	cacheKey := h.cache.BuildKey("crossplane-check", cluster, group, version, kind)
	data, err := h.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		isCrossplane, err := h.k8s.IsCrossplaneResource(cluster, group, version, kind)
		if err != nil {
			log.Printf("Error checking if resource is Crossplane: %v", err)
			return false, nil
		}
		return isCrossplane, nil
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"isCrossplane": data}, nil)
}

func (h *Handler) TraceCrossplaneResource(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	group := c.Param("group")
	version := c.Param("version")
	kind := c.Param("kind")
	namespace := c.Param("namespace")
	name := c.Param("name")

	if group == "_" {
		group = ""
	}
	if namespace == "_" {
		namespace = ""
	}

	resource, err := h.k8s.ResolveKindToResource(cluster, group, version, kind)
	if err != nil {
		if kind == strings.ToLower(kind) && strings.HasSuffix(kind, "s") {
			resource = kind
		} else {
			resource = strings.ToLower(kind) + "s"
		}
		log.Printf("Failed to resolve kind %s to resource, using %s: %v", kind, resource, err)
	}

	dynamicClient, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}

	tracer := crossplane.NewTracer(dynamicClient)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	trace, err := tracer.Trace(ctx, gvr, namespace, name)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"trace": trace}, nil)
}
