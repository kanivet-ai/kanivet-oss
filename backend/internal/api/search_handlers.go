package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/search"
)

func (h *Handler) Search(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("query parameter 'q' is required"))
		return
	}

	options := search.SearchOptions{Limit: 20, Offset: 0}

	if limitStr := c.Query("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit <= 100 {
			options.Limit = limit
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil && offset >= 0 {
			options.Offset = offset
		}
	}

	if clusters := c.QueryArray("clusters"); len(clusters) > 0 {
		options.Clusters = clusters
	}
	if namespaces := c.QueryArray("namespaces"); len(namespaces) > 0 {
		options.Namespaces = namespaces
	}
	if kinds := c.QueryArray("kinds"); len(kinds) > 0 {
		options.Kinds = kinds
	}

	results, err := h.search.Search(query, options)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"results": results, "query": query, "count": len(results)}, nil)
}

func (h *Handler) SearchRecentQueries(c *gin.Context) {
	limit := 10
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}
	resources, err := h.search.GetRecentSearches(limit)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"resources": resources}, nil)
}

func (h *Handler) SaveSearchHistory(c *gin.Context) {
	var req struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Namespace  string `json:"namespace"`
		Cluster    string `json:"cluster"`
		APIVersion string `json:"apiVersion"`
		Category   string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" || req.Kind == "" || req.Cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("name, kind, and cluster are required"))
		return
	}
	if err := h.search.SaveSearchHistory(search.RecentResource{
		Name:       req.Name,
		Kind:       req.Kind,
		Namespace:  req.Namespace,
		Cluster:    req.Cluster,
		APIVersion: req.APIVersion,
		Category:   req.Category,
	}); err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"saved": true}, nil)
}

func (h *Handler) IndexCluster(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}

	go func() {
		if err := h.search.IndexCluster(cluster); err != nil {
			log.Printf("Failed to index cluster %s: %v", cluster, err)
		}
		if h.watcherBridge != nil {
			if err := h.watcherBridge.StartWatchingCluster(cluster); err != nil {
				log.Printf("Failed to start watching cluster %s: %v", cluster, err)
			}
		}
	}()

	h.respond(c, http.StatusAccepted, gin.H{
		"message": fmt.Sprintf("Indexing and watching started for cluster %s", cluster),
		"cluster": cluster,
	}, nil)
}

func (h *Handler) GetSearchIndexingStatus(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}
	status, err := h.search.GetIndexingStatus(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, status, nil)
}
