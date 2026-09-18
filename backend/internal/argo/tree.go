package argo

import (
	"context"
	"sort"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type ResourceResolver func(group, version, kind string) (resource string, namespaced bool, err error)

func DiscoveryResolver(d discovery.DiscoveryInterface) ResourceResolver {
	return func(group, version, kind string) (string, bool, error) {
		gv := version
		if group != "" {
			gv = group + "/" + version
		}
		list, err := d.ServerResourcesForGroupVersion(gv)
		if err != nil {
			return "", false, err
		}
		kindLower := strings.ToLower(kind)
		kindNorm := strings.ReplaceAll(kindLower, "s", "")
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			if strings.EqualFold(r.Kind, kind) {
				return r.Name, r.Namespaced, nil
			}
			nameLower := strings.ToLower(r.Name)
			nameNorm := strings.ReplaceAll(nameLower, "s", "")
			if kindNorm == nameNorm {
				return r.Name, r.Namespaced, nil
			}
		}
		return "", false, nil
	}
}

const treeMaxConcurrency = 10

type ResourceNode struct {
	Group      string         `json:"group"`
	Version    string         `json:"version"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Status     string         `json:"status,omitempty"`
	Health     string         `json:"health,omitempty"`
	SyncStatus string         `json:"syncStatus,omitempty"`
	Message    string         `json:"message,omitempty"`
	Exists     bool           `json:"exists"`
	Children   []ResourceNode `json:"children,omitempty"`
}

type AppSummary struct {
	Name             string          `json:"name"`
	Namespace        string          `json:"namespace"`
	Project          string          `json:"project,omitempty"`
	SyncStatus       string          `json:"syncStatus"`
	Health           string          `json:"health"`
	Revision         string          `json:"revision,omitempty"`
	TargetRevision   string          `json:"targetRevision,omitempty"`
	RepoURL          string          `json:"repoUrl,omitempty"`
	Path             string          `json:"path,omitempty"`
	DestServer       string          `json:"destServer,omitempty"`
	DestName         string          `json:"destName,omitempty"`
	DestNamespace    string          `json:"destNamespace,omitempty"`
	Resources        []ResourceNode  `json:"resources"`
	Conditions       []Condition     `json:"conditions,omitempty"`
	Operation        *OperationState `json:"operation,omitempty"`
	History          []HistoryEntry  `json:"history,omitempty"`
	ReconciledAt     string          `json:"reconciledAt,omitempty"`
	LastSyncedAt     string          `json:"lastSyncedAt,omitempty"`
	LastSyncPhase    string          `json:"lastSyncPhase,omitempty"`
	RefreshRequested bool            `json:"refreshRequested,omitempty"`
}

type HistoryEntry struct {
	ID         int64  `json:"id"`
	Revision   string `json:"revision,omitempty"`
	DeployedAt string `json:"deployedAt,omitempty"`
	Source     string `json:"source,omitempty"`
	Initiator  string `json:"initiator,omitempty"`
}

type OperationState struct {
	Phase      string               `json:"phase,omitempty"`
	Message    string               `json:"message,omitempty"`
	StartedAt  string               `json:"startedAt,omitempty"`
	FinishedAt string               `json:"finishedAt,omitempty"`
	SyncResult *OperationSyncResult `json:"syncResult,omitempty"`
	Resources  []OperationResource  `json:"resources,omitempty"`
}

type OperationSyncResult struct {
	Revision  string              `json:"revision,omitempty"`
	Source    map[string]string   `json:"source,omitempty"`
	Resources []OperationResource `json:"resources,omitempty"`
}

type OperationResource struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	HookType  string `json:"hookType,omitempty"`
	HookPhase string `json:"hookPhase,omitempty"`
	SyncPhase string `json:"syncPhase,omitempty"`
}

type Condition struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func BuildAppTree(ctx context.Context, dyn dynamic.Interface, resolver ResourceResolver, namespace, name string) (*AppSummary, error) {
	app, err := GetApplication(ctx, dyn, namespace, name)
	if err != nil {
		return nil, err
	}

	summary := &AppSummary{
		Name:      app.GetName(),
		Namespace: app.GetNamespace(),
	}

	spec, _, _ := unstructured.NestedMap(app.Object, "spec")
	if spec != nil {
		summary.Project, _, _ = unstructured.NestedString(spec, "project")
		if src, ok, _ := unstructured.NestedMap(spec, "source"); ok {
			summary.RepoURL, _, _ = unstructured.NestedString(src, "repoURL")
			summary.Path, _, _ = unstructured.NestedString(src, "path")
			summary.TargetRevision, _, _ = unstructured.NestedString(src, "targetRevision")
		}
		if dest, ok, _ := unstructured.NestedMap(spec, "destination"); ok {
			summary.DestServer, _, _ = unstructured.NestedString(dest, "server")
			summary.DestName, _, _ = unstructured.NestedString(dest, "name")
			summary.DestNamespace, _, _ = unstructured.NestedString(dest, "namespace")
		}
	}

	if anns := app.GetAnnotations(); anns != nil {
		if _, has := anns["argocd.argoproj.io/refresh"]; has {
			summary.RefreshRequested = true
		}
	}

	status, _, _ := unstructured.NestedMap(app.Object, "status")
	if status != nil {
		summary.ReconciledAt, _, _ = unstructured.NestedString(status, "reconciledAt")
		if syncMap, ok, _ := unstructured.NestedMap(status, "sync"); ok {
			summary.SyncStatus, _, _ = unstructured.NestedString(syncMap, "status")
			summary.Revision, _, _ = unstructured.NestedString(syncMap, "revision")
		}
		if healthMap, ok, _ := unstructured.NestedMap(status, "health"); ok {
			summary.Health, _, _ = unstructured.NestedString(healthMap, "status")
		}
		if conditions, ok, _ := unstructured.NestedSlice(status, "conditions"); ok {
			for _, c := range conditions {
				if cm, ok := c.(map[string]interface{}); ok {
					t, _, _ := unstructured.NestedString(cm, "type")
					m, _, _ := unstructured.NestedString(cm, "message")
					if t != "" {
						summary.Conditions = append(summary.Conditions, Condition{Type: t, Message: m})
					}
				}
			}
		}
		if resources, ok, _ := unstructured.NestedSlice(status, "resources"); ok {
			summary.Resources = buildResourceNodes(ctx, dyn, resolver, summary.DestNamespace, IsLocalDestination(summary.DestServer, summary.DestName), resources)
		}
		if opState, ok, _ := unstructured.NestedMap(status, "operationState"); ok && opState != nil {
			summary.Operation = extractOperationState(opState)
			if summary.Operation != nil {
				if summary.Operation.FinishedAt != "" {
					summary.LastSyncedAt = summary.Operation.FinishedAt
				}
				summary.LastSyncPhase = summary.Operation.Phase
			}
		}
		if history, ok, _ := unstructured.NestedSlice(status, "history"); ok {
			summary.History = extractHistory(history)
		}
	}

	if summary.LastSyncedAt == "" && len(summary.History) > 0 {
		latest := summary.History[len(summary.History)-1]
		if latest.DeployedAt != "" {
			summary.LastSyncedAt = latest.DeployedAt
			if summary.LastSyncPhase == "" {
				summary.LastSyncPhase = "Succeeded"
			}
		}
	}

	return summary, nil
}

func extractHistory(history []interface{}) []HistoryEntry {
	out := make([]HistoryEntry, 0, len(history))
	for _, h := range history {
		hm, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		entry := HistoryEntry{}
		if id, ok := hm["id"]; ok {
			switch v := id.(type) {
			case int64:
				entry.ID = v
			case float64:
				entry.ID = int64(v)
			}
		}
		entry.Revision, _, _ = unstructured.NestedString(hm, "revision")
		entry.DeployedAt, _, _ = unstructured.NestedString(hm, "deployedAt")
		entry.Initiator, _, _ = unstructured.NestedString(hm, "initiatedBy", "username")
		if src, ok, _ := unstructured.NestedMap(hm, "source"); ok && src != nil {
			repo, _, _ := unstructured.NestedString(src, "repoURL")
			path, _, _ := unstructured.NestedString(src, "path")
			if repo != "" || path != "" {
				entry.Source = repo + " @ " + path
			}
		}
		out = append(out, entry)
	}
	return out
}

func extractOperationState(opState map[string]interface{}) *OperationState {
	op := &OperationState{}
	op.Phase, _, _ = unstructured.NestedString(opState, "phase")
	op.Message, _, _ = unstructured.NestedString(opState, "message")
	op.StartedAt, _, _ = unstructured.NestedString(opState, "startedAt")
	op.FinishedAt, _, _ = unstructured.NestedString(opState, "finishedAt")
	if sr, ok, _ := unstructured.NestedMap(opState, "syncResult"); ok && sr != nil {
		op.SyncResult = &OperationSyncResult{}
		op.SyncResult.Revision, _, _ = unstructured.NestedString(sr, "revision")
		if resources, ok, _ := unstructured.NestedSlice(sr, "resources"); ok {
			op.SyncResult.Resources = parseOpResources(resources)
			op.Resources = op.SyncResult.Resources
		}
	}
	return op
}

func parseOpResources(resources []interface{}) []OperationResource {
	out := make([]OperationResource, 0, len(resources))
	for _, r := range resources {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		entry := OperationResource{}
		entry.Group, _, _ = unstructured.NestedString(rm, "group")
		entry.Version, _, _ = unstructured.NestedString(rm, "version")
		entry.Kind, _, _ = unstructured.NestedString(rm, "kind")
		entry.Namespace, _, _ = unstructured.NestedString(rm, "namespace")
		entry.Name, _, _ = unstructured.NestedString(rm, "name")
		entry.Status, _, _ = unstructured.NestedString(rm, "status")
		entry.Message, _, _ = unstructured.NestedString(rm, "message")
		entry.HookType, _, _ = unstructured.NestedString(rm, "hookType")
		entry.HookPhase, _, _ = unstructured.NestedString(rm, "hookPhase")
		entry.SyncPhase, _, _ = unstructured.NestedString(rm, "syncPhase")
		out = append(out, entry)
	}
	return out
}

func IsLocalDestination(server, name string) bool {
	if isLocalServer(server) && (name == "" || name == "in-cluster") {
		return true
	}
	if name == "in-cluster" && server == "" {
		return true
	}
	return false
}

func isLocalServer(server string) bool {
	if server == "" {
		return true
	}
	switch server {
	case "https://kubernetes.default.svc",
		"https://kubernetes.default.svc:443",
		"in-cluster":
		return true
	}
	return false
}

func buildResourceNodes(ctx context.Context, dyn dynamic.Interface, resolver ResourceResolver, defaultNamespace string, localCluster bool, resources []interface{}) []ResourceNode {
	nodes := make([]ResourceNode, len(resources))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeMaxConcurrency)

	for i, r := range resources {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		group, _, _ := unstructured.NestedString(rm, "group")
		version, _, _ := unstructured.NestedString(rm, "version")
		kind, _, _ := unstructured.NestedString(rm, "kind")
		ns, _, _ := unstructured.NestedString(rm, "namespace")
		nm, _, _ := unstructured.NestedString(rm, "name")
		statusVal, _, _ := unstructured.NestedString(rm, "status")
		healthMap, _, _ := unstructured.NestedMap(rm, "health")
		health := ""
		message := ""
		if healthMap != nil {
			health, _, _ = unstructured.NestedString(healthMap, "status")
			message, _, _ = unstructured.NestedString(healthMap, "message")
		}

		node := ResourceNode{
			Group:      group,
			Version:    version,
			Kind:       kind,
			Namespace:  ns,
			Name:       nm,
			SyncStatus: statusVal,
			Health:     health,
			Message:    message,
		}
		nodes[i] = node

		if !localCluster {
			continue
		}

		wg.Add(1)
		go func(idx int, n ResourceNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resourceName := ""
			namespaced := true
			if resolver != nil {
				if r, ns, err := resolver(n.Group, n.Version, n.Kind); err == nil && r != "" {
					resourceName = r
					namespaced = ns
				}
			}
			if resourceName == "" {
				resourceName = kindToResource(n.Kind)
			}

			gvr := schema.GroupVersionResource{
				Group:    n.Group,
				Version:  n.Version,
				Resource: resourceName,
			}

			lookupNs := n.Namespace
			if lookupNs == "" && namespaced {
				lookupNs = defaultNamespace
			}

			var live *unstructured.Unstructured
			var err error
			if namespaced && lookupNs != "" {
				live, err = dyn.Resource(gvr).Namespace(lookupNs).Get(ctx, n.Name, metav1.GetOptions{})
			} else {
				live, err = dyn.Resource(gvr).Get(ctx, n.Name, metav1.GetOptions{})
			}
			if err != nil || live == nil {
				return
			}
			nodes[idx].Exists = true
			if lookupNs != "" && nodes[idx].Namespace == "" {
				nodes[idx].Namespace = lookupNs
			}
			if phase, ok, _ := unstructured.NestedString(live.Object, "status", "phase"); ok && phase != "" {
				nodes[idx].Status = phase
			}
		}(i, node)
	}

	wg.Wait()

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		if nodes[i].Namespace != nodes[j].Namespace {
			return nodes[i].Namespace < nodes[j].Namespace
		}
		return nodes[i].Name < nodes[j].Name
	})
	return nodes
}

func kindToResource(kind string) string {
	lower := strings.ToLower(kind)
	if strings.HasSuffix(lower, "s") {
		return lower + "es"
	}
	if strings.HasSuffix(lower, "y") {
		return strings.TrimSuffix(lower, "y") + "ies"
	}
	return lower + "s"
}
