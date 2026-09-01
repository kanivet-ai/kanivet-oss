package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/models"
	"github.com/kanivet/backend/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (h *Handler) GetCategoriesQuery(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	out := make([]models.Category, 0, len(categories))
	hasArgo := h.hasArgoCD(cluster)
	for _, cat := range categories {
		if cat.ID == "argocd" && !hasArgo {
			continue
		}
		out = append(out, cat)
	}
	h.respond(c, http.StatusOK, gin.H{"categories": out}, nil)
}

func (h *Handler) ListResourcesQuery(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	category := c.Query("category")
	skipCounts := c.Query("skipCounts") == "true"

	if skipCounts {
		switch category {
		case "custom":
			cacheKey := h.cache.BuildKey("api-resources", cluster)
			data, err := h.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
				return h.k8s.ListAPIResources(cluster)
			})
			if err != nil {
				h.respond(c, http.StatusInternalServerError, nil, err)
				return
			}
			resources := data.([]metav1.APIResource)
			h.respond(c, http.StatusOK, gin.H{"resources": h.filterCustomResources(resources)}, nil)
		case "crossplane":
			resources, err := h.getCrossplaneResourcesWithoutCounts(cluster)
			if err != nil {
				h.respond(c, http.StatusInternalServerError, nil, err)
				return
			}
			h.respond(c, http.StatusOK, gin.H{"resources": resources}, nil)
		case "argocd":
			resources, err := h.getArgoResources(cluster, false)
			if err != nil {
				h.respond(c, http.StatusInternalServerError, nil, err)
				return
			}
			h.respond(c, http.StatusOK, gin.H{"resources": resources}, nil)
		default:
			resources, exists := categoryMap[category]
			if !exists {
				h.respond(c, http.StatusOK, gin.H{"resources": []models.Resource{}}, nil)
				return
			}
			resourcesWithoutCounts := make([]models.Resource, len(resources))
			for i, res := range resources {
				resourcesWithoutCounts[i] = models.Resource{
					Name:       res.Name,
					Group:      res.Group,
					Version:    res.Version,
					Kind:       res.Kind,
					Namespaced: res.Namespaced,
				}
			}
			h.respond(c, http.StatusOK, gin.H{"resources": resourcesWithoutCounts}, nil)
		}
		return
	}

	cacheKey := h.cache.BuildKey("resources", cluster, category)
	data, err := h.cache.GetOrSet(cacheKey, 2*time.Minute, func() (interface{}, error) {
		switch category {
		case "custom":
			resources, err := h.k8s.ListAPIResources(cluster)
			if err != nil {
				return nil, err
			}
			return h.filterCustomResources(resources), nil
		case "crossplane":
			return h.getCrossplaneResources(cluster)
		case "argocd":
			return h.getArgoResources(cluster, true)
		default:
			return h.getPredefinedResources(cluster, category)
		}
	})
	h.respond(c, http.StatusOK, gin.H{"resources": data}, err)
}

func (h *Handler) GetResourceDetailsQuery(c *gin.Context) {
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

	dynamicClient, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}

	resourceName := h.k8s.GetResourceName(cluster, group, version, kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourceName}
	cacheKey := h.cache.BuildKey("detail", cluster, group, version, kind, namespace, name)

	data, err := h.cache.GetOrSet(cacheKey, 10*time.Second, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		var resource *unstructured.Unstructured
		var err error
		if namespace != "" {
			resource, err = dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		} else {
			resource, err = dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		}
		if err != nil {
			return nil, err
		}
		// Events are loaded separately via GetResourceEventsQuery so the detail
		// view renders immediately instead of blocking on event enrichment.
		h.cleanVerboseFields(resource)
		return resource, nil
	})

	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "the server could not find the requested resource") {
			h.respond(c, http.StatusNotFound, nil, err)
		} else {
			h.respond(c, http.StatusInternalServerError, nil, err)
		}
		return
	}
	h.respond(c, http.StatusOK, data, nil)
}

// GetResourceEventsQuery returns just the events for a resource. Loaded
// separately from the detail object so the detail view renders without waiting
// on event enrichment. Results are briefly cached; the event listener keeps the
// underlying DB fresh.
func (h *Handler) GetResourceEventsQuery(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	name := c.Param("name")
	if namespace == "_" {
		namespace = ""
	}

	cacheKey := h.cache.BuildKey("resource-events", cluster, namespace, name)
	data, _ := h.cache.GetOrSet(cacheKey, 5*time.Second, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		events := h.fetchResourceEvents(ctx, cluster, namespace, name, "")
		if events == nil {
			events = []interface{}{}
		}
		return events, nil
	})
	h.respond(c, http.StatusOK, data, nil)
}

func (h *Handler) ScaleResource(c *gin.Context) {
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

	var payload struct {
		Replicas *int32 `json:"replicas" binding:"required,min=0"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	ctx := context.Background()
	err := h.k8s.ScaleResource(ctx, cluster, group, version, kind, namespace, name, *payload.Replicas)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to scale resource: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, group, version, kind, namespace, name)
	h.cache.Delete(detailKey)
	resourceName := h.k8s.GetResourceName(cluster, group, version, kind)
	topic := fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, group, version, resourceName, namespace)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("Successfully scaled %s/%s to %d replicas", namespace, name, *payload.Replicas),
	}, nil)
}

func (h *Handler) RestartResource(c *gin.Context) {
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

	ctx := context.Background()
	err := h.k8s.RestartResource(ctx, cluster, group, version, kind, namespace, name)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to restart resource: %v", err))
		return
	}

	detailKey := h.cache.BuildKey("detail", cluster, group, version, kind, namespace, name)
	h.cache.Delete(detailKey)
	resourceName := h.k8s.GetResourceName(cluster, group, version, kind)
	topic := fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, group, version, resourceName, namespace)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}
	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	h.respond(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("Successfully initiated restart for %s/%s", namespace, name),
	}, nil)
}

func (h *Handler) TriggerCronJob(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	name := c.Param("name")

	if namespace == "_" {
		namespace = ""
	}

	ctx := context.Background()
	jobName, err := h.k8s.TriggerCronJob(ctx, cluster, namespace, name)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to trigger cronjob: %v", err))
		return
	}

	topic := fmt.Sprintf("items:%s:batch:v1:jobs:%s", cluster, namespace)
	if h.invalidationBus != nil {
		h.invalidationBus.Invalidate(topic)
	}

	h.respond(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("Successfully created job %s from cronjob %s/%s", jobName, namespace, name),
		"jobName": jobName,
	}, nil)
}

func (h *Handler) BulkRestartResources(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}

	var request struct {
		Resources []struct {
			Group     string `json:"group"`
			Version   string `json:"version"`
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"resources"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	ctx := context.Background()
	errors := make([]string, 0)

	for _, resource := range request.Resources {
		group := resource.Group
		namespace := resource.Namespace
		if group == "_" {
			group = ""
		}
		if namespace == "_" {
			namespace = ""
		}
		err := h.k8s.RestartResource(ctx, cluster, group, resource.Version, resource.Kind, namespace, resource.Name)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: %v", namespace, resource.Name, err))
		}
	}

	dashboardKey := h.cache.BuildKey("dashboard", cluster)
	h.cache.Delete(dashboardKey)

	if h.invalidationBus != nil && len(request.Resources) > 0 {
		seenTopics := make(map[string]bool)
		for _, resource := range request.Resources {
			resourceName := h.k8s.GetResourceName(cluster, resource.Group, resource.Version, resource.Kind)
			topic := fmt.Sprintf("items:%s:%s:%s:%s:%s", cluster, resource.Group, resource.Version, resourceName, resource.Namespace)
			if !seenTopics[topic] {
				h.invalidationBus.Invalidate(topic)
				seenTopics[topic] = true
			}
		}
	}

	if len(errors) > 0 {
		h.respond(c, http.StatusPartialContent, gin.H{
			"message": fmt.Sprintf("Restarted %d resources with %d errors", len(request.Resources)-len(errors), len(errors)),
			"errors":  errors,
		}, nil)
		return
	}
	h.respond(c, http.StatusOK, gin.H{
		"message": fmt.Sprintf("Successfully restarted %d resources", len(request.Resources)),
	}, nil)
}

func (h *Handler) GetRolloutStatus(c *gin.Context) {
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

	ctx := context.Background()
	status, err := h.k8s.GetRolloutStatus(ctx, cluster, group, version, kind, namespace, name)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, fmt.Errorf("failed to get rollout status: %v", err))
		return
	}
	h.respond(c, http.StatusOK, status, nil)
}

func (h *Handler) GetBatchRolloutStatus(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}

	var request struct {
		Items []k8sRolloutStatusRequest `json:"items"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}

	if len(request.Items) == 0 {
		h.respond(c, http.StatusOK, gin.H{"results": []interface{}{}}, nil)
		return
	}

	ctx := context.Background()
	results := h.k8s.GetBatchRolloutStatus(ctx, cluster, request.Items)
	h.respond(c, http.StatusOK, gin.H{"results": results}, nil)
}

func (h *Handler) DeleteResources(c *gin.Context) {
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

	var request struct {
		Items []struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}
	if len(request.Items) == 0 {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("no items to delete"))
		return
	}

	dynamicClient, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}

	resourcePlural := utils.PluralizeKind(kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourcePlural}

	var errors []string
	var successCount int

	for _, item := range request.Items {
		var deleteErr error
		if item.Namespace != "" {
			deleteErr = dynamicClient.Resource(gvr).Namespace(item.Namespace).Delete(context.Background(), item.Name, metav1.DeleteOptions{})
		} else {
			deleteErr = dynamicClient.Resource(gvr).Delete(context.Background(), item.Name, metav1.DeleteOptions{})
		}
		if deleteErr != nil {
			errors = append(errors, fmt.Sprintf("Failed to delete %s/%s: %v", item.Namespace, item.Name, deleteErr))
		} else {
			successCount++
			detailKey := h.cache.BuildKey("detail", cluster, group, version, kind, item.Namespace, item.Name)
			h.cache.Delete(detailKey)
		}
	}

	if successCount > 0 {
		resourceName := h.k8s.GetResourceName(cluster, group, version, kind)
		topic := fmt.Sprintf("items:%s:%s:%s:%s:*", cluster, group, version, resourceName)
		if h.invalidationBus != nil {
			h.invalidationBus.InvalidatePattern(topic)
		}
		dashboardKey := h.cache.BuildKey("dashboard", cluster)
		h.cache.Delete(dashboardKey)
	}

	if len(errors) > 0 {
		h.respond(c, http.StatusPartialContent, gin.H{"deleted": successCount, "errors": errors}, nil)
	} else {
		h.respond(c, http.StatusOK, gin.H{
			"deleted": successCount,
			"message": fmt.Sprintf("Successfully deleted %d resource(s)", successCount),
		}, nil)
	}
}

func (h *Handler) RemoveFinalizers(c *gin.Context) {
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

	var request struct {
		Items []struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}
	if len(request.Items) == 0 {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("no items to process"))
		return
	}

	dynamicClient, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}

	resourcePlural := utils.PluralizeKind(kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourcePlural}
	errors := []string{}
	successCount := 0

	for _, item := range request.Items {
		obj, err := dynamicClient.Resource(gvr).Namespace(item.Namespace).Get(context.Background(), item.Name, metav1.GetOptions{})
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: failed to get resource: %v", item.Namespace, item.Name, err))
			continue
		}
		finalizers, found, err := unstructured.NestedStringSlice(obj.Object, "metadata", "finalizers")
		if err == nil && found && len(finalizers) > 0 {
			err = unstructured.SetNestedStringSlice(obj.Object, []string{}, "metadata", "finalizers")
			if err == nil {
				_, err = dynamicClient.Resource(gvr).Namespace(item.Namespace).Update(context.Background(), obj, metav1.UpdateOptions{})
			}
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: %v", item.Namespace, item.Name, err))
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		resourceName := h.k8s.GetResourceName(cluster, group, version, kind)
		topic := fmt.Sprintf("items:%s:%s:%s:%s:*", cluster, group, version, resourceName)
		if h.invalidationBus != nil {
			h.invalidationBus.InvalidatePattern(topic)
		}
		dashboardKey := h.cache.BuildKey("dashboard", cluster)
		h.cache.Delete(dashboardKey)
	}

	result := gin.H{"success": successCount, "failed": len(errors), "total": len(request.Items)}
	if len(errors) > 0 {
		result["errors"] = errors
		h.respond(c, http.StatusPartialContent, result, nil)
	} else {
		h.respond(c, http.StatusOK, result, nil)
	}
}

func (h *Handler) ForceRefreshResources(c *gin.Context) {
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

	var request struct {
		Items []struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("invalid request body: %v", err))
		return
	}
	if len(request.Items) == 0 {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("no items to process"))
		return
	}

	dynamicClient, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}

	resourcePlural := utils.PluralizeKind(kind)
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resourcePlural}
	errors := []string{}
	successCount := 0

	for _, item := range request.Items {
		obj, err := dynamicClient.Resource(gvr).Namespace(item.Namespace).Get(context.Background(), item.Name, metav1.GetOptions{})
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: failed to get resource: %v", item.Namespace, item.Name, err))
			continue
		}
		annotations, _, err := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
		if err != nil {
			annotations = make(map[string]string)
		}
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations["kanivet.io/force-refreshed-at"] = time.Now().Format(time.RFC3339)
		err = unstructured.SetNestedStringMap(obj.Object, annotations, "metadata", "annotations")
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: failed to set annotations: %v", item.Namespace, item.Name, err))
			continue
		}
		_, err = dynamicClient.Resource(gvr).Namespace(item.Namespace).Update(context.Background(), obj, metav1.UpdateOptions{})
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: failed to update resource: %v", item.Namespace, item.Name, err))
		} else {
			successCount++
		}
	}

	result := gin.H{"success": successCount, "failed": len(errors), "total": len(request.Items)}
	if len(errors) > 0 {
		result["errors"] = errors
		h.respond(c, http.StatusPartialContent, result, nil)
	} else {
		h.respond(c, http.StatusOK, result, nil)
	}
}

func (h *Handler) ListAPIResources(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.respond(c, http.StatusBadRequest, nil, fmt.Errorf("cluster parameter is required"))
		return
	}
	cacheKey := h.cache.BuildKey("api-resources", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		return h.k8s.ListAPIResources(cluster)
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, data, nil)
}

func (h *Handler) InvalidateAPIResourcesCache(c *gin.Context) {
	cluster := c.Query("cluster")
	if cluster == "" {
		h.cache.DeleteByPrefix("api-resources:")
		h.respond(c, http.StatusOK, gin.H{"message": "API resources cache invalidated for all clusters"}, nil)
		return
	}
	cacheKey := h.cache.BuildKey("api-resources", cluster)
	h.cache.Delete(cacheKey)
	h.respond(c, http.StatusOK, gin.H{"message": fmt.Sprintf("API resources cache invalidated for cluster %s", cluster)}, nil)
}

func (h *Handler) filterCustomResources(resources []metav1.APIResource) []models.Resource {
	var filtered []models.Resource
	seen := make(map[string]bool)
	for _, r := range resources {
		if r.Group != "" && !isBuiltinResource(r.Name, r.Group) && !strings.Contains(r.Name, "/") {
			key := fmt.Sprintf("%s/%s/%s/%s", r.Group, r.Version, r.Kind, r.Name)
			if !seen[key] {
				seen[key] = true
				filtered = append(filtered, models.Resource{
					Name:       r.Name,
					Group:      r.Group,
					Version:    r.Version,
					Kind:       r.Kind,
					Namespaced: r.Namespaced,
				})
			}
		}
	}
	return filtered
}

func (h *Handler) getPredefinedResources(cluster string, category string) ([]models.Resource, error) {
	templateResources, exists := categoryMap[category]
	if !exists {
		return []models.Resource{}, nil
	}
	resources := make([]models.Resource, len(templateResources))
	copy(resources, templateResources)
	ctx := context.Background()
	h.parallelResourceCount(ctx, cluster, resources)
	return resources, nil
}

func (h *Handler) getCrossplaneResources(cluster string) ([]models.Resource, error) {
	cacheKey := h.cache.BuildKey("crossplane-xrds", cluster)
	xrdsData, err := h.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		return h.k8s.GetCrossplaneXRDs(cluster)
	})
	if err != nil {
		return nil, err
	}
	xrds := xrdsData.([]map[string]interface{})

	var resources []models.Resource
	seen := make(map[string]bool)
	for _, xrd := range xrds {
		group := xrd["group"].(string)
		version := xrd["version"].(string)
		kind := xrd["kind"].(string)
		plural := xrd["plural"].(string)
		namespaced := xrd["namespaced"].(bool)
		key := fmt.Sprintf("%s/%s/%s", group, version, plural)
		if !seen[key] {
			seen[key] = true
			resources = append(resources, models.Resource{
				Name:       plural,
				Group:      group,
				Version:    version,
				Kind:       kind,
				Namespaced: namespaced,
			})
		}
	}

	ctx := context.Background()
	h.parallelResourceCount(ctx, cluster, resources)
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Group != resources[j].Group {
			return resources[i].Group < resources[j].Group
		}
		return resources[i].Name < resources[j].Name
	})
	return resources, nil
}

func (h *Handler) getCrossplaneResourcesWithoutCounts(cluster string) ([]models.Resource, error) {
	cacheKey := h.cache.BuildKey("crossplane-xrds", cluster)
	xrdsData, err := h.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		return h.k8s.GetCrossplaneXRDs(cluster)
	})
	if err != nil {
		return nil, err
	}
	xrds := xrdsData.([]map[string]interface{})

	var resources []models.Resource
	seen := make(map[string]bool)
	for _, xrd := range xrds {
		group := xrd["group"].(string)
		version := xrd["version"].(string)
		kind := xrd["kind"].(string)
		plural := xrd["plural"].(string)
		namespaced := xrd["namespaced"].(bool)
		key := fmt.Sprintf("%s/%s/%s", group, version, plural)
		if !seen[key] {
			seen[key] = true
			resources = append(resources, models.Resource{
				Name:       plural,
				Group:      group,
				Version:    version,
				Kind:       kind,
				Namespaced: namespaced,
			})
		}
	}

	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Group != resources[j].Group {
			return resources[i].Group < resources[j].Group
		}
		return resources[i].Name < resources[j].Name
	})
	go h.publishCrossplaneCounts(cluster, resources)
	return resources, nil
}

func (h *Handler) GetWorkloadPods(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	kind := c.Param("kind")
	namespace := c.Param("namespace")
	name := c.Param("name")

	if kind == "" || namespace == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind, namespace, and name are required"})
		return
	}

	client, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get client: %v", err)})
		return
	}

	ctx := context.Background()
	var labelSelector string

	switch strings.ToLower(kind) {
	case "deployment":
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get deployment: %v", err)})
			return
		}
		labelSelector = metav1.FormatLabelSelector(deployment.Spec.Selector)
	case "statefulset":
		statefulSet, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get statefulset: %v", err)})
			return
		}
		labelSelector = metav1.FormatLabelSelector(statefulSet.Spec.Selector)
	case "daemonset":
		daemonSet, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get daemonset: %v", err)})
			return
		}
		labelSelector = metav1.FormatLabelSelector(daemonSet.Spec.Selector)
	case "replicaset":
		replicaSet, err := client.AppsV1().ReplicaSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get replicaset: %v", err)})
			return
		}
		labelSelector = metav1.FormatLabelSelector(replicaSet.Spec.Selector)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unsupported workload kind: %s", kind)})
		return
	}

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to list pods: %v", err)})
		return
	}

	podList := make([]gin.H, 0, len(pods.Items))
	for _, pod := range pods.Items {
		var cpuLimit, cpuRequest, memLimit, memRequest int64
		for _, container := range pod.Spec.Containers {
			if container.Resources.Limits != nil {
				if cpu, ok := container.Resources.Limits[corev1ResourceCPU]; ok {
					cpuLimit += cpu.MilliValue()
				}
				if mem, ok := container.Resources.Limits[corev1ResourceMemory]; ok {
					memLimit += mem.Value()
				}
			}
			if container.Resources.Requests != nil {
				if cpu, ok := container.Resources.Requests[corev1ResourceCPU]; ok {
					cpuRequest += cpu.MilliValue()
				}
				if mem, ok := container.Resources.Requests[corev1ResourceMemory]; ok {
					memRequest += mem.Value()
				}
			}
		}
		containers := make([]string, 0, len(pod.Spec.Containers))
		for _, c := range pod.Spec.Containers {
			containers = append(containers, c.Name)
		}
		podList = append(podList, gin.H{
			"name":             pod.Name,
			"status":           string(pod.Status.Phase),
			"node":             pod.Spec.NodeName,
			"containers":       containers,
			"ready":            getPodReadyStatus(pod),
			"restarts":         getPodRestartCount(pod),
			"age":              pod.CreationTimestamp.Time,
			"resourceLimits":   gin.H{"cpu": cpuLimit, "memory": memLimit},
			"resourceRequests": gin.H{"cpu": cpuRequest, "memory": memRequest},
		})
	}
	c.JSON(http.StatusOK, gin.H{"pods": podList, "count": len(podList)})
}

func (h *Handler) GetServiceAccountAssociatedResources(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	name := c.Param("name")

	if namespace == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "namespace and name are required"})
		return
	}

	client, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get client: %v", err)})
		return
	}
	dynamicClient, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get dynamic client: %v", err)})
		return
	}

	ctx := context.Background()
	result := gin.H{
		"roleBindings":        []interface{}{},
		"clusterRoleBindings": []interface{}{},
		"pods":                []interface{}{},
	}

	roleBindingGVR := schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
	roleBindings, err := dynamicClient.Resource(roleBindingGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		filteredRoleBindings := []interface{}{}
		for _, rb := range roleBindings.Items {
			subjects, found, _ := unstructured.NestedSlice(rb.Object, "subjects")
			if found {
				for _, subject := range subjects {
					if subjectMap, ok := subject.(map[string]interface{}); ok {
						if subjectMap["kind"] == "ServiceAccount" &&
							subjectMap["name"] == name &&
							(subjectMap["namespace"] == nil || subjectMap["namespace"] == namespace) {
							filteredRoleBindings = append(filteredRoleBindings, rb.Object)
							break
						}
					}
				}
			}
		}
		result["roleBindings"] = filteredRoleBindings
	}

	clusterRoleBindingGVR := schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	clusterRoleBindings, err := dynamicClient.Resource(clusterRoleBindingGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		filteredClusterRoleBindings := []interface{}{}
		for _, crb := range clusterRoleBindings.Items {
			subjects, found, _ := unstructured.NestedSlice(crb.Object, "subjects")
			if found {
				for _, subject := range subjects {
					if subjectMap, ok := subject.(map[string]interface{}); ok {
						if subjectMap["kind"] == "ServiceAccount" &&
							subjectMap["name"] == name &&
							subjectMap["namespace"] == namespace {
							filteredClusterRoleBindings = append(filteredClusterRoleBindings, crb.Object)
							break
						}
					}
				}
			}
		}
		result["clusterRoleBindings"] = filteredClusterRoleBindings
	}

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		filteredPods := []corev1Pod{}
		for _, pod := range pods.Items {
			if pod.Spec.ServiceAccountName == name {
				filteredPods = append(filteredPods, pod)
			}
		}
		result["pods"] = filteredPods
	}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetDetailTabState(c *gin.Context) {
	cluster := c.Query("cluster")
	group := c.Query("group")
	version := c.Query("version")
	kind := c.Query("kind")
	namespace := c.Query("namespace")
	name := c.Query("name")
	state := h.detailTabs.GetTabState(cluster, group, version, kind, namespace, name)
	h.respond(c, http.StatusOK, state, nil)
}

func (h *Handler) SetDetailTabState(c *gin.Context) {
	var req struct {
		Cluster   string `json:"cluster"`
		Group     string `json:"group"`
		Version   string `json:"version"`
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		ActiveTab string `json:"activeTab"`
	}
	if err := c.BindJSON(&req); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	h.detailTabs.SetTabState(req.Cluster, req.Group, req.Version, req.Kind, req.Namespace, req.Name, req.ActiveTab)
	h.respond(c, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// fetchResourceEvents returns the events for a resource as a JSON-ready slice.
// It prefers the event listener's DB-backed cache and falls back to a live API
// list. uid may be empty. This no longer blocks any detail response — it backs
// the dedicated events endpoint so the detail view loads instantly and pulls
// events separately.
func (h *Handler) fetchResourceEvents(ctx context.Context, cluster, namespace, name, uid string) []interface{} {
	if !h.eventListener.IsListening(cluster) {
		log.Printf("Starting event listener for cluster %s on first resource events request", cluster)
		if err := h.eventListener.StartListening(cluster); err != nil {
			log.Printf("Failed to start event listener for cluster %s: %v", cluster, err)
			return h.buildEventsDirect(ctx, cluster, namespace, name)
		}
		// The listener's initial DB sync is still in flight on first start, so
		// serve this request from the live API rather than waiting for it.
		return h.buildEventsDirect(ctx, cluster, namespace, name)
	}

	events, err := h.db.GetEventsForResource(cluster, namespace, name, uid)
	if err != nil {
		log.Printf("Failed to get events from database for %s/%s: %v", namespace, name, err)
		return h.buildEventsDirect(ctx, cluster, namespace, name)
	}
	if len(events) == 0 {
		return h.buildEventsDirect(ctx, cluster, namespace, name)
	}

	eventsList := make([]interface{}, len(events))
	for i, event := range events {
		eventMap := map[string]interface{}{
			"type":    event.Type,
			"reason":  event.Reason,
			"message": event.Message,
			"count":   event.Count,
		}
		if !event.FirstTimestamp.IsZero() {
			eventMap["firstTimestamp"] = event.FirstTimestamp
		}
		if !event.LastTimestamp.IsZero() {
			eventMap["lastTimestamp"] = event.LastTimestamp
		}
		if !event.EventTime.IsZero() {
			eventMap["eventTime"] = event.EventTime
		}
		if event.SourceComponent != "" {
			eventMap["source"] = event.SourceComponent
		}
		eventsList[i] = eventMap
	}
	return eventsList
}

func (h *Handler) buildEventsDirect(ctx context.Context, cluster, namespace, name string) []interface{} {
	client, err := h.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil
	}
	fieldSelector := fmt.Sprintf("involvedObject.name=%s", name)
	if namespace != "" {
		fieldSelector = fmt.Sprintf("involvedObject.name=%s,involvedObject.namespace=%s", name, namespace)
	}
	timeout := int64(2)
	limit := int64(50)
	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector:  fieldSelector,
		TimeoutSeconds: &timeout,
		Limit:          limit,
	})
	if err != nil || len(events.Items) == 0 {
		return nil
	}
	sort.Slice(events.Items, func(i, j int) bool {
		timeI := events.Items[i].EventTime.Time
		if timeI.IsZero() {
			timeI = events.Items[i].LastTimestamp.Time
		}
		timeJ := events.Items[j].EventTime.Time
		if timeJ.IsZero() {
			timeJ = events.Items[j].LastTimestamp.Time
		}
		return timeI.After(timeJ)
	})
	max := 20
	if len(events.Items) < max {
		max = len(events.Items)
	}
	items := events.Items[:max]
	eventsList := make([]interface{}, len(items))
	for i, event := range items {
		eventMap := map[string]interface{}{
			"type":    event.Type,
			"reason":  event.Reason,
			"message": event.Message,
			"count":   event.Count,
		}
		if !event.FirstTimestamp.IsZero() {
			eventMap["firstTimestamp"] = event.FirstTimestamp.Time
		}
		if !event.LastTimestamp.IsZero() {
			eventMap["lastTimestamp"] = event.LastTimestamp.Time
		}
		if !event.EventTime.IsZero() {
			eventMap["eventTime"] = event.EventTime.Time
		}
		if event.Source.Component != "" {
			eventMap["source"] = event.Source.Component
		}
		eventsList[i] = eventMap
	}
	return eventsList
}
