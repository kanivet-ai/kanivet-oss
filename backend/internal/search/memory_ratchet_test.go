package search

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
)

func TestKindFormsConvergeToOneDocument(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	singular := map[string]interface{}{"name": "web", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}
	plural := map[string]interface{}{"name": "web", "namespace": "ns", "kind": "pods", "apiVersion": "v1"}
	if _, err := h.OnAdd("c", singular); err != nil {
		t.Fatal(err)
	}
	if _, err := h.OnAdd("c", plural); err != nil {
		t.Fatal(err)
	}
	if got := h.index.DocumentCount(); got != 1 {
		t.Fatalf("kind forms produced %d docs, want 1", got)
	}
	if err := h.OnDelete("c", singular); err != nil {
		t.Fatal(err)
	}
	if got := h.index.DocumentCount(); got != 0 {
		t.Fatalf("delete left %d docs", got)
	}
}

func TestDeletedThenRecreatedPodIsSearchable(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(storage.DefaultSearchShards)}
	add := map[string]interface{}{"name": "web-1", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}
	if _, err := h.OnAdd("c", add); err != nil {
		t.Fatal(err)
	}
	del := map[string]interface{}{"name": "web-1", "namespace": "ns"}
	if err := h.onDeleteWithCoords("c", "", "v1", "pods", del); err != nil {
		t.Fatal(err)
	}
	if got := h.index.DocumentCount(); got != 0 {
		t.Fatalf("delete left %d docs", got)
	}
	changed, err := h.OnAdd("c", add)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("recreated pod skipped by stale fingerprint")
	}
	if got := h.index.DocumentCount(); got != 1 {
		t.Fatalf("recreated pod not indexed: %d docs", got)
	}
	res, _ := h.index.Search(storage.SearchQuery{Text: "web-1", Limit: 5})
	if len(res) == 0 {
		t.Fatal("recreated pod not searchable")
	}
}

func TestEvictedDocIsReindexedDespiteFingerprint(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	add := map[string]interface{}{"name": "evictme", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}
	if _, err := h.OnAdd("c", add); err != nil {
		t.Fatal(err)
	}
	searchable := h.convertToSearchable("c", add)
	if err := h.index.Remove(searchable.ID); err != nil {
		t.Fatalf("simulated eviction failed: %v", err)
	}
	changed, err := h.OnAdd("c", add)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || h.index.DocumentCount() != 1 {
		t.Fatalf("evicted doc not re-indexed: changed=%v docs=%d", changed, h.index.DocumentCount())
	}
}

func TestFilterChangedReindexesMissingDocs(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	r := storage.SearchableResource{ID: "c/g/v/pods/ns/a", Cluster: "c", Kind: "Pod", Name: "a", Namespace: "ns"}
	if got := h.filterChanged([]storage.SearchableResource{r}); len(got) != 1 {
		t.Fatalf("first pass: %d", len(got))
	}
	if err := h.index.Index(r); err != nil {
		t.Fatal(err)
	}
	if got := h.filterChanged([]storage.SearchableResource{r}); len(got) != 0 {
		t.Fatalf("doc present + same fp must skip, got %d", len(got))
	}
	if err := h.index.Remove(r.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.filterChanged([]storage.SearchableResource{r}); len(got) != 1 {
		t.Fatalf("evicted doc must reindex despite fp, got %d", len(got))
	}
}

func TestSearchCacheVersionCoalescesBursts(t *testing.T) {
	s := newTestSearchService()
	cur := time.Unix(1000, 0)
	s.now = func() time.Time { return cur }
	s.invalidateSearchCacheForCluster("c")
	g1, c1 := s.searchVersions("c")
	s.invalidateSearchCacheForCluster("c")
	s.invalidateSearchCacheForCluster("c")
	g2, c2 := s.searchVersions("c")
	if g2 != g1 || c2 != c1 {
		t.Fatalf("burst within interval bumped version: %d.%d -> %d.%d", g1, c1, g2, c2)
	}
	cur = cur.Add(3 * time.Second)
	g3, c3 := s.searchVersions("c")
	if g3 != g1+1 || c3 != c1+1 {
		t.Fatalf("pending bump not applied after interval: %d.%d -> %d.%d", g1, c1, g3, c3)
	}
	g4, c4 := s.searchVersions("c")
	if g4 != g3 || c4 != c3 {
		t.Fatalf("no pending change but version moved: %d.%d -> %d.%d", g3, c3, g4, c4)
	}
}

func TestServiceRemoveClusterPurgesState(t *testing.T) {
	idx := storage.NewShardedIndex(storage.DefaultSearchShards)
	h := &ResourceEventHandler{index: idx}
	s := newTestSearchService()
	s.index = idx
	s.eventHandler = h
	s.activeClusters = map[string]time.Time{"gone": time.Now(), "kept": time.Now()}
	s.watchedTopics = map[string]bool{"items:gone:apps:v1:Pod:": true, "items:kept:apps:v1:Pod:": true}
	s.indexedResources = map[string]map[string]time.Time{"gone": {}, "kept": {}}
	s.indexingStatus = map[string]*IndexingStatus{"gone": {}, "kept": {}}
	s.resourceVersions = map[string]map[string]string{"gone": {}, "kept": {}}
	for _, c := range []string{"gone", "kept"} {
		if _, err := h.OnAdd(c, map[string]interface{}{"name": "web", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"}); err != nil {
			t.Fatal(err)
		}
	}
	if removed := s.RemoveCluster("gone"); removed != 1 {
		t.Fatalf("removed %d docs, want 1", removed)
	}
	if idx.DocumentCount() != 1 {
		t.Fatalf("expected 1 doc left, got %d", idx.DocumentCount())
	}
	if _, ok := s.activeClusters["gone"]; ok {
		t.Fatal("activeClusters not purged")
	}
	if s.watchedTopics["items:gone:apps:v1:Pod:"] {
		t.Fatal("watchedTopics not purged")
	}
	if !s.watchedTopics["items:kept:apps:v1:Pod:"] {
		t.Fatal("other cluster topics must survive")
	}
	if _, ok := s.indexedResources["gone"]; ok {
		t.Fatal("indexedResources not purged")
	}
	h.fpMu.RLock()
	for id := range h.fingerprints {
		if len(id) >= 5 && id[:5] == "gone/" {
			h.fpMu.RUnlock()
			t.Fatalf("fingerprint survived for %s", id)
		}
	}
	h.fpMu.RUnlock()
	changed, _ := h.OnAdd("gone", map[string]interface{}{"name": "web", "namespace": "ns", "kind": "Pod", "apiVersion": "v1"})
	if !changed {
		t.Fatal("re-added doc after RemoveCluster must index")
	}
}

func TestDBDeletesUseSingleWorker(t *testing.T) {
	var processed atomic.Int64
	h := &ResourceEventHandler{
		index:    storage.NewShardedIndex(1),
		dbDelete: func(id string) error { time.Sleep(200 * time.Microsecond); processed.Add(1); return nil },
	}
	before := runtime.NumGoroutine()
	for i := 0; i < 300; i++ {
		h.deleteFromDBAsync("c/g/v/pods/ns/x")
	}
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Fatalf("delete path spawned %d goroutines", grew)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processed.Load() < 300 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if processed.Load() != 300 {
		t.Fatalf("only %d/300 deletes processed", processed.Load())
	}
}
