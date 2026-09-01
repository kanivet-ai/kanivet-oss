package search

import (
	"testing"

	"github.com/kanivet/backend/internal/search/storage"
)

// Reproduces the reported bug: a pod indexed on add but never removed on delete.
// Runs against the production multi-shard index to catch shard-routing mismatches
// between Index (routes by resource.Cluster) and Remove (routes by ID prefix).
func TestOnDeleteRemovesFromSearch(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards)}

	res := map[string]interface{}{
		"name":       "pod-1",
		"namespace":  "default",
		"kind":       "Pod",
		"apiVersion": "v1",
	}

	if _, err := h.OnAdd("test-cluster", res); err != nil {
		t.Fatalf("OnAdd: %v", err)
	}
	if h.index.DocumentCount() != 1 {
		t.Fatalf("expected 1 doc after add, got %d", h.index.DocumentCount())
	}
	got, _ := h.index.Search(storage.SearchQuery{Text: "pod-1", Limit: 10})
	if len(got) != 1 {
		t.Fatalf("expected pod-1 searchable after add, got %d results", len(got))
	}

	if err := h.OnDelete("test-cluster", res); err != nil {
		t.Fatalf("OnDelete: %v", err)
	}
	if h.index.DocumentCount() != 0 {
		t.Fatalf("BUG: expected 0 docs after delete, got %d", h.index.DocumentCount())
	}
	got, _ = h.index.Search(storage.SearchQuery{Text: "pod-1", Limit: 10})
	if len(got) != 0 {
		t.Fatalf("BUG: pod-1 still searchable after delete, got %d results", len(got))
	}
}

// The real-world failure: a watch DELETE event arrives with kind stripped, so
// listadapters.Simplify falls back to the plural resource name ("pods"), which
// no longer matches the singular Kind ("Pod") used at index time. The delete
// must still remove the document.
func TestOnDeletePluralKindStillRemoves(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards)}
	add := map[string]interface{}{"name": "pod-1", "namespace": "default", "kind": "Pod", "apiVersion": "v1"}
	if _, err := h.OnAdd("c", add); err != nil {
		t.Fatalf("OnAdd: %v", err)
	}
	del := map[string]interface{}{"name": "pod-1", "namespace": "default", "kind": "pods", "apiVersion": "v1"}
	if err := h.onDeleteWithCoords("c", "", "v1", "pods", del); err != nil {
		t.Fatalf("OnDelete: %v", err)
	}
	if h.index.DocumentCount() != 0 {
		t.Fatalf("BUG: plural-kind delete left %d docs", h.index.DocumentCount())
	}
}

// A stripped delete with no kind at all in the payload must still remove using
// the topic coordinates.
func TestOnDeleteMissingKindStillRemoves(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards)}
	add := map[string]interface{}{"name": "pod-1", "namespace": "default", "kind": "Pod", "apiVersion": "v1"}
	if _, err := h.OnAdd("c", add); err != nil {
		t.Fatalf("OnAdd: %v", err)
	}
	del := map[string]interface{}{"name": "pod-1", "namespace": "default"}
	if err := h.onDeleteWithCoords("c", "", "v1", "pods", del); err != nil {
		t.Fatalf("OnDelete: %v", err)
	}
	if h.index.DocumentCount() != 0 {
		t.Fatalf("BUG: missing-kind delete left %d docs", h.index.DocumentCount())
	}
}

// Same flow across many clusters to stress shard routing on delete.
func TestOnDeleteRemovesAcrossClusters(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards)}
	clusters := []string{"prod", "staging", "dev", "us-east-1", "eu-west-2", "asia"}
	for _, c := range clusters {
		res := map[string]interface{}{"name": "web", "namespace": "default", "kind": "Pod", "apiVersion": "v1"}
		if _, err := h.OnAdd(c, res); err != nil {
			t.Fatalf("OnAdd %s: %v", c, err)
		}
	}
	if h.index.DocumentCount() != len(clusters) {
		t.Fatalf("expected %d docs, got %d", len(clusters), h.index.DocumentCount())
	}
	for _, c := range clusters {
		res := map[string]interface{}{"name": "web", "namespace": "default", "kind": "Pod", "apiVersion": "v1"}
		if err := h.OnDelete(c, res); err != nil {
			t.Fatalf("OnDelete %s: %v", c, err)
		}
	}
	if h.index.DocumentCount() != 0 {
		t.Fatalf("BUG: expected 0 docs after deleting all, got %d", h.index.DocumentCount())
	}
}
