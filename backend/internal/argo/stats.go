package argo

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type AppListEntry struct {
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
	Project        string `json:"project,omitempty"`
	SyncStatus     string `json:"syncStatus"`
	Health         string `json:"health"`
	DestNamespace  string `json:"destNamespace,omitempty"`
	DestName       string `json:"destName,omitempty"`
	DestServer     string `json:"destServer,omitempty"`
	RepoURL        string `json:"repoUrl,omitempty"`
	Path           string `json:"path,omitempty"`
	Revision       string `json:"revision,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
	ReconciledAt   string `json:"reconciledAt,omitempty"`
	LastSyncedAt   string `json:"lastSyncedAt,omitempty"`
	LastSyncPhase  string `json:"lastSyncPhase,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
	OperationPhase string `json:"operationPhase,omitempty"`
}

type AppStats struct {
	Total         int            `json:"total"`
	BySync        map[string]int `json:"bySync"`
	ByHealth      map[string]int `json:"byHealth"`
	ByProject     map[string]int `json:"byProject"`
	ByNamespace   map[string]int `json:"byNamespace"`
	Syncing       int            `json:"syncing"`
	RecentSync1h  int            `json:"recentSync1h"`
	RecentSync24h int            `json:"recentSync24h"`
	TopUnhealthy  []AppListEntry `json:"topUnhealthy,omitempty"`
}

func ListApplicationsSummary(ctx context.Context, dyn dynamic.Interface) ([]AppListEntry, error) {
	gvr := schema.GroupVersionResource{Group: GroupArgoproj, Version: "v1alpha1", Resource: "applications"}
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := dyn.Resource(gvr).List(timeoutCtx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]AppListEntry, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		out = append(out, appEntryFromUnstructured(item))
	}
	return out, nil
}

func appEntryFromUnstructured(u *unstructured.Unstructured) AppListEntry {
	entry := AppListEntry{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
	}
	if t := u.GetCreationTimestamp(); !t.IsZero() {
		entry.CreatedAt = t.UTC().Format(time.RFC3339)
	}
	spec, _, _ := unstructured.NestedMap(u.Object, "spec")
	if spec != nil {
		entry.Project, _, _ = unstructured.NestedString(spec, "project")
		if src, ok, _ := unstructured.NestedMap(spec, "source"); ok {
			entry.RepoURL, _, _ = unstructured.NestedString(src, "repoURL")
			entry.Path, _, _ = unstructured.NestedString(src, "path")
			entry.TargetRevision, _, _ = unstructured.NestedString(src, "targetRevision")
		}
		if dest, ok, _ := unstructured.NestedMap(spec, "destination"); ok {
			entry.DestNamespace, _, _ = unstructured.NestedString(dest, "namespace")
			entry.DestName, _, _ = unstructured.NestedString(dest, "name")
			entry.DestServer, _, _ = unstructured.NestedString(dest, "server")
		}
	}
	status, _, _ := unstructured.NestedMap(u.Object, "status")
	if status != nil {
		entry.ReconciledAt, _, _ = unstructured.NestedString(status, "reconciledAt")
		if syncMap, ok, _ := unstructured.NestedMap(status, "sync"); ok {
			entry.SyncStatus, _, _ = unstructured.NestedString(syncMap, "status")
			entry.Revision, _, _ = unstructured.NestedString(syncMap, "revision")
		}
		if healthMap, ok, _ := unstructured.NestedMap(status, "health"); ok {
			entry.Health, _, _ = unstructured.NestedString(healthMap, "status")
		}
		if opState, ok, _ := unstructured.NestedMap(status, "operationState"); ok {
			entry.OperationPhase, _, _ = unstructured.NestedString(opState, "phase")
			entry.LastSyncedAt, _, _ = unstructured.NestedString(opState, "finishedAt")
			if entry.LastSyncPhase == "" {
				entry.LastSyncPhase = entry.OperationPhase
			}
		}
		if entry.LastSyncedAt == "" {
			if history, ok, _ := unstructured.NestedSlice(status, "history"); ok && len(history) > 0 {
				if last, ok := history[len(history)-1].(map[string]interface{}); ok {
					entry.LastSyncedAt, _, _ = unstructured.NestedString(last, "deployedAt")
					if entry.LastSyncPhase == "" {
						entry.LastSyncPhase = "Succeeded"
					}
				}
			}
		}
	}
	return entry
}

func ComputeStats(entries []AppListEntry) *AppStats {
	now := time.Now()
	stats := &AppStats{
		Total:       len(entries),
		BySync:      map[string]int{},
		ByHealth:    map[string]int{},
		ByProject:   map[string]int{},
		ByNamespace: map[string]int{},
	}
	unhealthy := make([]AppListEntry, 0, 8)
	for _, e := range entries {
		stats.BySync[orUnknown(e.SyncStatus)]++
		stats.ByHealth[orUnknown(e.Health)]++
		if e.Project != "" {
			stats.ByProject[e.Project]++
		}
		if e.Namespace != "" {
			stats.ByNamespace[e.Namespace]++
		}
		if e.OperationPhase == "Running" || e.OperationPhase == "Terminating" {
			stats.Syncing++
		}
		if e.LastSyncedAt != "" {
			if t, err := time.Parse(time.RFC3339, e.LastSyncedAt); err == nil {
				delta := now.Sub(t)
				if delta < time.Hour {
					stats.RecentSync1h++
				}
				if delta < 24*time.Hour {
					stats.RecentSync24h++
				}
			}
		}
		switch e.Health {
		case "Degraded", "Missing":
			unhealthy = append(unhealthy, e)
		}
	}
	sort.Slice(unhealthy, func(i, j int) bool {
		if unhealthy[i].Health != unhealthy[j].Health {
			return unhealthy[i].Health == "Degraded"
		}
		return unhealthy[i].Name < unhealthy[j].Name
	})
	if len(unhealthy) > 5 {
		unhealthy = unhealthy[:5]
	}
	stats.TopUnhealthy = unhealthy
	return stats
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}
