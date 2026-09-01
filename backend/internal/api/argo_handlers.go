package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/argo"
	"github.com/kanivet/backend/internal/models"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (h *Handler) detectArgo(cluster string) *argo.Detection {
	cacheKey := h.cache.BuildKey("argo-detect", cluster)
	if v, ok := h.cache.Get(cacheKey); ok {
		if det, ok := v.(*argo.Detection); ok && det != nil {
			return det
		}
	}
	meta, metaErr := h.k8s.GetMetadataClient(cluster)
	dyn, dynErr := h.k8s.GetDynamicClient(cluster)
	if metaErr != nil && dynErr != nil {
		det := &argo.Detection{}
		h.cache.Set(cacheKey, det, 10*time.Second)
		return det
	}
	kube, _ := h.k8s.GetClientForCluster(cluster)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	det, err := argo.Detect(ctx, kube, meta, dyn)
	if err != nil || det == nil {
		det = &argo.Detection{}
		h.cache.Set(cacheKey, det, 10*time.Second)
		return det
	}
	ttl := 10 * time.Minute
	if !det.Installed {
		ttl = 30 * time.Second
	}
	h.cache.Set(cacheKey, det, ttl)
	return det
}

func (h *Handler) hasArgoCD(cluster string) bool {
	return h.detectArgo(cluster).Installed
}

func (h *Handler) getArgoResources(cluster string, withCounts bool) ([]models.Resource, error) {
	dyn, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	argoResources, err := argo.ListResources(ctx, dyn)
	if err != nil {
		return nil, err
	}
	out := make([]models.Resource, 0, len(argoResources))
	for _, r := range argoResources {
		out = append(out, models.Resource{
			Name:       r.Name,
			Group:      r.Group,
			Version:    r.Version,
			Kind:       r.Kind,
			Namespaced: r.Namespaced,
		})
	}
	if withCounts {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5)
		for i := range out {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				countCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				gvr := schema.GroupVersionResource{Group: out[idx].Group, Version: out[idx].Version, Resource: out[idx].Name}
				count, err := h.k8s.GetResourceCount(countCtx, cluster, gvr)
				if err != nil {
					log.Printf("[Argo] failed to count %s: %v", out[idx].Name, err)
					return
				}
				out[idx].Count = &count
			}(i)
		}
		wg.Wait()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

func (h *Handler) GetArgoDetection(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	h.respond(c, http.StatusOK, gin.H{"detection": h.detectArgo(cluster)}, nil)
}

type argoTopologyCacheEntry struct {
	Topology []argo.TopologyNode `json:"topology"`
	Summary  *argo.AppSummary    `json:"summary"`
}

func (h *Handler) GetArgoApplicationTopology(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	name := c.Param("name")
	if namespace == "_" {
		namespace = ""
	}

	cacheKey := h.cache.BuildKey("argo-topology", cluster, namespace, name)
	data, err := h.cache.GetOrSet(cacheKey, 5*time.Second, func() (interface{}, error) {
		return h.buildArgoTopology(cluster, namespace, name)
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	entry, _ := data.(*argoTopologyCacheEntry)
	if entry == nil {
		entry = &argoTopologyCacheEntry{}
	}
	h.respond(c, http.StatusOK, gin.H{"topology": entry.Topology, "summary": entry.Summary}, nil)
}

func (h *Handler) buildArgoTopology(cluster, namespace, name string) (*argoTopologyCacheEntry, error) {
	hostDyn, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, err
	}
	hostDisc, _ := h.k8s.GetDiscoveryClient(cluster)
	var hostResolver argo.ResourceResolver
	if hostDisc != nil {
		hostResolver = argo.DiscoveryResolver(hostDisc)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := argo.BuildAppTree(ctx, hostDyn, hostResolver, namespace, name)
	if err != nil {
		return nil, err
	}

	targetDyn := hostDyn
	targetResolver := hostResolver

	if !argo.IsLocalDestination(summary.DestServer, summary.DestName) {
		ref := h.resolveArgoDestinationVCluster(cluster, summary.DestName, summary.DestServer)
		if ref != nil {
			conn, vErr := h.k8s.ConnectVCluster(cluster, ref.Namespace, ref.Name)
			if vErr == nil && conn != nil {
				if dyn, derr := h.k8s.GetDynamicClient(conn.ID); derr == nil {
					targetDyn = dyn
				}
				if disc, derr := h.k8s.GetDiscoveryClient(conn.ID); derr == nil {
					targetResolver = argo.DiscoveryResolver(disc)
				}
			} else if vErr != nil {
				log.Printf("[Argo] topology: vcluster connect failed for %s/%s: %v", ref.Namespace, ref.Name, vErr)
			}
		}
	}

	app, err := argo.GetApplication(ctx, hostDyn, namespace, name)
	if err != nil {
		return nil, err
	}

	nodes, err := argo.BuildTopology(ctx, targetDyn, targetResolver, summary, *app)
	if err != nil {
		return nil, err
	}
	return &argoTopologyCacheEntry{Topology: nodes, Summary: summary}, nil
}

func (h *Handler) resolveArgoDestinationVCluster(cluster, destName, destServer string) *argo.VClusterRef {
	cacheKey := h.cache.BuildKey("argo-destinations", cluster)
	data, _ := h.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		kube, err := h.k8s.GetClientForCluster(cluster)
		if err != nil {
			return &argo.DestinationMap{ByName: map[string]argo.Destination{}, ByServer: map[string]argo.Destination{}}, nil
		}
		ns := h.argoNamespace(cluster)
		vcs, _ := h.k8s.ListVClusters(cluster)
		entries := make([]argo.VClusterEntry, 0, len(vcs))
		for _, vc := range vcs {
			entries = append(entries, argo.VClusterEntry{Namespace: vc.Namespace, Name: vc.Name})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return argo.BuildDestinationMap(ctx, kube, ns, entries)
	})
	m, _ := data.(*argo.DestinationMap)
	if m == nil {
		return nil
	}
	if destName != "" {
		if d, ok := m.ByName[destName]; ok && d.Kind == argo.DestinationVCluster {
			return d.VCluster
		}
	}
	if destServer != "" {
		if d, ok := m.ByServer[destServer]; ok && d.Kind == argo.DestinationVCluster {
			return d.VCluster
		}
	}
	return nil
}

func (h *Handler) GetArgoApplicationTree(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace := c.Param("namespace")
	name := c.Param("name")
	if namespace == "_" {
		namespace = ""
	}
	dyn, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	disc, _ := h.k8s.GetDiscoveryClient(cluster)
	var resolver argo.ResourceResolver
	if disc != nil {
		resolver = argo.DiscoveryResolver(disc)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	summary, err := argo.BuildAppTree(ctx, dyn, resolver, namespace, name)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"summary": summary}, nil)
}

func (h *Handler) GetArgoApplicationsSummary(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	cacheKey := h.cache.BuildKey("argo-apps-summary", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 10*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return []argo.AppListEntry{}, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		entries, err := argo.ListApplicationsSummary(ctx, dyn)
		if err != nil {
			return []argo.AppListEntry{}, nil
		}
		return entries, nil
	})
	if err != nil {
		h.respond(c, http.StatusOK, gin.H{"items": []argo.AppListEntry{}}, nil)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"items": data}, nil)
}

func (h *Handler) GetArgoStats(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	cacheKey := h.cache.BuildKey("argo-stats", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 10*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return argo.ComputeStats(nil), nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		entries, err := argo.ListApplicationsSummary(ctx, dyn)
		if err != nil {
			return argo.ComputeStats(nil), nil
		}
		return argo.ComputeStats(entries), nil
	})
	if err != nil {
		h.respond(c, http.StatusOK, gin.H{"stats": argo.ComputeStats(nil)}, nil)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"stats": data}, nil)
}

func (h *Handler) GetArgoManagedResources(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	name := c.Param("name")
	client, err := h.buildArgoClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := client.ManagedResources(ctx, name)
	if err != nil {
		log.Printf("[Argo] managed-resources failed for %s: %v", name, err)
		h.respond(c, http.StatusBadGateway, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"items": items}, nil)
}

func (h *Handler) buildArgoClient(cluster string) (*argo.Client, error) {
	kube, cfg, err := h.k8s.GetClientAndConfig(cluster)
	if err != nil {
		return nil, err
	}
	det := h.detectArgo(cluster)
	return argo.NewClient(kube, cfg, det.Namespace, det.ServerName), nil
}

type syncResourceBody struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type syncRequestBody struct {
	Prune     bool               `json:"prune"`
	DryRun    bool               `json:"dryRun"`
	Strategy  string             `json:"strategy"`
	Force     bool               `json:"force"`
	Replace   bool               `json:"replace"`
	Revision  string             `json:"revision"`
	Resources []syncResourceBody `json:"resources"`
}

func (h *Handler) ArgoSyncApplication(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	name := c.Param("name")
	var body syncRequestBody
	_ = c.ShouldBindJSON(&body)
	client, err := h.buildArgoClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	refs := make([]argo.SyncResourceRef, 0, len(body.Resources))
	for _, r := range body.Resources {
		refs = append(refs, argo.SyncResourceRef{
			Group:     r.Group,
			Kind:      r.Kind,
			Namespace: r.Namespace,
			Name:      r.Name,
		})
	}
	if err := client.Sync(ctx, name, argo.SyncRequest{
		Prune:     body.Prune,
		DryRun:    body.DryRun,
		Strategy:  body.Strategy,
		Force:     body.Force,
		Replace:   body.Replace,
		Revision:  body.Revision,
		Resources: refs,
	}); err != nil {
		log.Printf("[Argo] sync failed for %s: %v", name, err)
		h.respond(c, http.StatusBadGateway, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

func (h *Handler) ArgoRefreshApplication(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	name := c.Param("name")
	hard := c.Query("hard") == "true"
	client, err := h.buildArgoClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Refresh(ctx, name, hard); err != nil {
		log.Printf("[Argo] refresh failed for %s: %v", name, err)
		h.respond(c, http.StatusBadGateway, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

func (h *Handler) GetArgoDestinations(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	cacheKey := h.cache.BuildKey("argo-destinations", cluster)
	data, err := h.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		kube, err := h.k8s.GetClientForCluster(cluster)
		if err != nil {
			return &argo.DestinationMap{ByName: map[string]argo.Destination{}, ByServer: map[string]argo.Destination{}}, nil
		}
		ns := h.argoNamespace(cluster)
		vcs, _ := h.k8s.ListVClusters(cluster)
		entries := make([]argo.VClusterEntry, 0, len(vcs))
		for _, vc := range vcs {
			entries = append(entries, argo.VClusterEntry{Namespace: vc.Namespace, Name: vc.Name})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return argo.BuildDestinationMap(ctx, kube, ns, entries)
	})
	if err != nil {
		log.Printf("[Argo] destination map failed: %v", err)
	}
	h.respond(c, http.StatusOK, gin.H{"destinations": data}, nil)
}

func (h *Handler) argoNamespace(cluster string) string {
	if det := h.detectArgo(cluster); det.Namespace != "" {
		return det.Namespace
	}
	return "argocd"
}

type rollbackRequestBody struct {
	ID    int64 `json:"id"`
	Prune bool  `json:"prune"`
}

func (h *Handler) ArgoRollbackApplication(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	name := c.Param("name")
	var body rollbackRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		h.respond(c, http.StatusBadRequest, nil, err)
		return
	}
	client, err := h.buildArgoClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Rollback(ctx, name, body.ID, body.Prune); err != nil {
		log.Printf("[Argo] rollback failed for %s: %v", name, err)
		h.respond(c, http.StatusBadGateway, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}
