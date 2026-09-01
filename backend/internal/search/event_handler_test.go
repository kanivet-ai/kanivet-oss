package search

import (
	"testing"

	"github.com/kanivet/backend/internal/search/storage"
)

func TestFilterChangedSkipsUnchangedResources(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	r1 := storage.SearchableResource{ID: "c/g/v/pod/ns/a", Cluster: "c", Kind: "Pod", Name: "a", Namespace: "ns"}
	r2 := storage.SearchableResource{ID: "c/g/v/pod/ns/b", Cluster: "c", Kind: "Pod", Name: "b", Namespace: "ns"}

	changed := h.filterChanged([]storage.SearchableResource{r1, r2})
	if len(changed) != 2 {
		t.Fatalf("expected 2 changed on first pass, got %d", len(changed))
	}
	for _, r := range changed {
		if err := h.index.Index(r); err != nil {
			t.Fatal(err)
		}
	}

	changed = h.filterChanged([]storage.SearchableResource{r1, r2})
	if len(changed) != 0 {
		t.Fatalf("expected 0 changed on identical pass, got %d", len(changed))
	}

	r2.Labels = map[string]string{"app": "x"}
	changed = h.filterChanged([]storage.SearchableResource{r1, r2})
	if len(changed) != 1 || changed[0].ID != r2.ID {
		t.Fatalf("expected only r2 changed, got %v", changed)
	}
}

func TestOnAddReportsChanged(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	res := map[string]interface{}{"name": "a", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}

	changed, err := h.OnAdd("c", res)
	if err != nil || !changed {
		t.Fatalf("expected first add to change, got changed=%v err=%v", changed, err)
	}
	changed, err = h.OnAdd("c", res)
	if err != nil || changed {
		t.Fatalf("expected identical add to be skipped, got changed=%v err=%v", changed, err)
	}
}
