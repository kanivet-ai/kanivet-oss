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
	"github.com/kanivet/backend/internal/cnpg"
	"github.com/kanivet/backend/internal/gitops"
	"github.com/kanivet/backend/internal/models"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Flux CD and CloudNativePG integration endpoints. The heavy lifting lives in
// internal/gitops and internal/cnpg; this file only resolves clients, caches
// and maps errors.

const (
	categoryFlux = "flux"
	categoryCNPG = "cnpg"
)

func (h *Handler) detectFlux(cluster string) *gitops.FluxDetection {
	key := h.cache.BuildKey("flux-detect", cluster)
	if v, ok := h.cache.Get(key); ok {
		if det, ok := v.(*gitops.FluxDetection); ok && det != nil {
			return det
		}
	}
	det := &gitops.FluxDetection{Kinds: []gitops.FluxKind{}}
	dyn, err := h.k8s.GetDynamicClient(cluster)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if d, derr := gitops.DetectFlux(ctx, dyn); derr == nil && d != nil {
			det = d
		}
	}
	ttl := 10 * time.Minute
	if !det.Installed {
		ttl = 30 * time.Second
	}
	h.cache.Set(key, det, ttl)
	return det
}

func (h *Handler) detectCNPG(cluster string) *cnpg.Detection {
	key := h.cache.BuildKey("cnpg-detect", cluster)
	if v, ok := h.cache.Get(key); ok {
		if det, ok := v.(*cnpg.Detection); ok && det != nil {
			return det
		}
	}
	det := &cnpg.Detection{Kinds: []cnpg.Kind{}}
	dyn, err := h.k8s.GetDynamicClient(cluster)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if d, derr := cnpg.Detect(ctx, dyn); derr == nil && d != nil {
			det = d
		}
	}
	ttl := 10 * time.Minute
	if !det.Installed {
		ttl = 30 * time.Second
	}
	h.cache.Set(key, det, ttl)
	return det
}

// integrationCategoryHidden reports whether a sidebar category belongs to an
// integration that is not installed on this cluster.
func (h *Handler) integrationCategoryHidden(cluster, id string) bool {
	switch id {
	case categoryFlux:
		return !h.detectFlux(cluster).Installed
	case categoryCNPG:
		return !h.detectCNPG(cluster).Installed
	}
	return false
}

// getIntegrationResources lists the resource types of the flux or cnpg
// sidebar category, with counts when asked.
func (h *Handler) getIntegrationResources(cluster, category string, withCounts bool) ([]models.Resource, error) {
	out := []models.Resource{}
	switch category {
	case categoryFlux:
		for _, k := range h.detectFlux(cluster).Kinds {
			out = append(out, models.Resource{Name: k.Plural, Group: k.Group, Version: k.Version, Kind: k.Kind, Namespaced: true})
		}
	case categoryCNPG:
		for _, k := range h.detectCNPG(cluster).Kinds {
			out = append(out, models.Resource{Name: k.Plural, Group: cnpg.Group, Version: k.Version, Kind: k.Kind, Namespaced: true})
		}
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
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				gvr := schema.GroupVersionResource{Group: out[idx].Group, Version: out[idx].Version, Resource: out[idx].Name}
				count, err := h.k8s.GetResourceCount(ctx, cluster, gvr)
				if err != nil {
					log.Printf("[Integrations] failed to count %s: %v", out[idx].Name, err)
					return
				}
				out[idx].Count = &count
			}(i)
		}
		wg.Wait()
	}
	return out, nil
}

// GetIntegrationsDetection reports which of Argo CD, Flux and CNPG are
// installed, plus the Argo namespace the GitOps owner badge needs.
func (h *Handler) GetIntegrationsDetection(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	argoDet := h.detectArgo(cluster)
	h.respond(c, http.StatusOK, gin.H{
		"argo":          argoDet.Installed,
		"argoNamespace": h.argoNamespace(cluster),
		"flux":          h.detectFlux(cluster),
		"cnpg":          h.detectCNPG(cluster),
	}, nil)
}

// GetGitOpsStatus returns Argo apps and Flux objects as one list.
func (h *Handler) GetGitOpsStatus(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	key := h.cache.BuildKey("gitops-status", cluster)
	data, err := h.cache.GetOrSet(key, 5*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return gitops.GitOpsStatus(ctx, dyn, h.hasArgoCD(cluster), h.detectFlux(cluster))
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"status": data}, nil)
}

// GetFluxObjects lists Flux objects, optionally filtered by category.
func (h *Handler) GetFluxObjects(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	key := h.cache.BuildKey("flux-objects", cluster)
	data, err := h.cache.GetOrSet(key, 5*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return gitops.ListFlux(ctx, dyn, h.detectFlux(cluster))
	})
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	items, _ := data.([]gitops.FluxObject)
	h.respond(c, http.StatusOK, gin.H{"items": items, "summary": gitops.SummarizeFlux(items)}, nil)
}

func fluxParams(c *gin.Context) (kind, namespace, name string) {
	namespace = c.Param("namespace")
	if namespace == "_" {
		namespace = ""
	}
	return c.Param("kind"), namespace, c.Param("name")
}

// GetFluxObject returns one Flux object with conditions and inventory.
func (h *Handler) GetFluxObject(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	kind, namespace, name := fluxParams(c)
	dyn, err := h.k8s.GetDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d, err := gitops.GetFluxObject(ctx, dyn, h.detectFlux(cluster), kind, namespace, name)
	if err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"object": d}, nil)
}

func (h *Handler) invalidateGitOps(cluster string) {
	h.cache.Delete(h.cache.BuildKey("gitops-status", cluster))
	h.cache.Delete(h.cache.BuildKey("flux-objects", cluster))
	h.cache.DeleteByPrefix(h.cache.BuildKey("detail", cluster) + ":")
}

// FluxReconcile annotates a Flux object so its controller reconciles now.
func (h *Handler) FluxReconcile(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	kind, namespace, name := fluxParams(c)
	dyn, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := gitops.FluxReconcile(ctx, dyn, h.detectFlux(cluster), kind, namespace, name, gitops.ReconcileOptions{WithSource: c.Query("withSource") == "true"})
	if err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.invalidateGitOps(cluster)
	h.respond(c, http.StatusOK, gin.H{"result": res}, nil)
}

func (h *Handler) fluxSetSuspended(c *gin.Context, suspend bool) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	kind, namespace, name := fluxParams(c)
	dyn, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := gitops.FluxSetSuspended(ctx, dyn, h.detectFlux(cluster), kind, namespace, name, suspend); err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.invalidateGitOps(cluster)
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

func (h *Handler) FluxSuspend(c *gin.Context) { h.fluxSetSuspended(c, true) }
func (h *Handler) FluxResume(c *gin.Context)  { h.fluxSetSuspended(c, false) }

// ---- CloudNativePG ----

func (h *Handler) GetCNPGClusters(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	key := h.cache.BuildKey("cnpg-clusters", cluster)
	data, err := h.cache.GetOrSet(key, 5*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return cnpg.ListClusters(ctx, dyn, h.detectCNPG(cluster))
	})
	if err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"items": data}, nil)
}

// cnpgDeps wires pod facts and metrics-based replication lag from the typed
// client. Both are best-effort.
func (h *Handler) cnpgDeps(cluster string) cnpg.Deps {
	kube, err := h.k8s.GetClientForCluster(cluster)
	if err != nil || kube == nil {
		return cnpg.Deps{}
	}
	return cnpg.Deps{
		Pods: func(ctx context.Context, namespace, name string) ([]cnpg.PodFacts, error) {
			list, err := kube.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "cnpg.io/cluster=" + name + ",cnpg.io/podRole=instance"})
			if err != nil {
				return nil, err
			}
			out := make([]cnpg.PodFacts, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, podFacts(&list.Items[i]))
			}
			return out, nil
		},
		Lag: func(ctx context.Context, namespace, pod string) (float64, error) {
			lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			raw, err := kube.CoreV1().Pods(namespace).ProxyGet("http", pod, "9187", "/metrics", nil).DoRaw(lctx)
			if err != nil {
				return 0, err
			}
			v, ok := cnpg.ParseReplicationLag(string(raw))
			if !ok {
				return 0, fmt.Errorf("no replication lag metric")
			}
			return v, nil
		},
	}
}

func podFacts(p *corev1.Pod) cnpg.PodFacts {
	f := cnpg.PodFacts{Name: p.Name, Node: p.Spec.NodeName, Phase: string(p.Status.Phase), Role: p.Labels["cnpg.io/instanceRole"]}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			f.Ready = true
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		f.Restarts += cs.RestartCount
	}
	return f
}

func cnpgParams(c *gin.Context) (string, string) { return c.Param("namespace"), c.Param("name") }

func (h *Handler) GetCNPGClusterStatus(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace, name := cnpgParams(c)
	key := h.cache.BuildKey("cnpg-cluster", cluster, namespace, name)
	data, err := h.cache.GetOrSet(key, 5*time.Second, func() (interface{}, error) {
		dyn, err := h.k8s.GetDynamicClient(cluster)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return cnpg.GetClusterStatus(ctx, dyn, h.detectCNPG(cluster), namespace, name, h.cnpgDeps(cluster))
	})
	if err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.respond(c, http.StatusOK, gin.H{"status": data}, nil)
}

func (h *Handler) invalidateCNPG(cluster, namespace, name string) {
	h.cache.Delete(h.cache.BuildKey("cnpg-clusters", cluster))
	h.cache.Delete(h.cache.BuildKey("cnpg-cluster", cluster, namespace, name))
	h.cache.DeleteByPrefix(h.cache.BuildKey("detail", cluster) + ":")
}

func (h *Handler) CNPGBackupNow(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace, name := cnpgParams(c)
	dyn, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := cnpg.BackupNow(ctx, dyn, h.detectCNPG(cluster), namespace, name, time.Time{})
	if err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.invalidateCNPG(cluster, namespace, name)
	h.respond(c, http.StatusOK, gin.H{"result": res}, nil)
}

func (h *Handler) CNPGPromote(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace, name := cnpgParams(c)
	var body struct {
		Instance string `json:"instance"`
	}
	_ = c.ShouldBindJSON(&body)
	dyn, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cnpg.Promote(ctx, dyn, h.detectCNPG(cluster), namespace, name, body.Instance, time.Time{}); err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.invalidateCNPG(cluster, namespace, name)
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

func (h *Handler) CNPGRestart(c *gin.Context) {
	cluster, ok := h.requireCluster(c)
	if !ok {
		return
	}
	namespace, name := cnpgParams(c)
	dyn, err := h.k8s.GetInteractiveDynamicClient(cluster)
	if err != nil {
		h.respond(c, http.StatusInternalServerError, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cnpg.Restart(ctx, dyn, h.detectCNPG(cluster), namespace, name, time.Time{}); err != nil {
		h.respond(c, statusForK8sErr(err), nil, err)
		return
	}
	h.invalidateCNPG(cluster, namespace, name)
	h.respond(c, http.StatusOK, gin.H{"success": true}, nil)
}

// statusForK8sErr maps API server errors to HTTP codes the UI can explain;
// our own validation errors (suspended, not an instance, ...) become 409.
func statusForK8sErr(err error) int {
	type statusErr interface{ Status() metav1.Status }
	if se, ok := err.(statusErr); ok {
		if code := int(se.Status().Code); code >= 400 {
			return code
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not installed"):
		return http.StatusNotFound
	case strings.Contains(msg, "suspended"), strings.Contains(msg, "already"), strings.Contains(msg, "not an instance"), strings.Contains(msg, "no backup configuration"), strings.Contains(msg, "required"):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
