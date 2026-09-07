package storage

import (
	"fmt"
	"sync"
	"testing"
)

func TestShardedRoutesAndSearchesAcrossClusters(t *testing.T) {
	s := NewShardedIndex(4)
	for c := 0; c < 6; c++ {
		cluster := fmt.Sprintf("cluster-%d", c)
		for i := 0; i < 5; i++ {
			r := testResource(cluster, "apps", "v1", "Deployment", "ns", fmt.Sprintf("deploy-%d", i))
			if err := s.Index(r); err != nil {
				t.Fatalf("index: %v", err)
			}
		}
	}
	if got := s.DocumentCount(); got != 30 {
		t.Fatalf("expected 30 docs across shards, got %d", got)
	}
	// Cross-cluster search must find matches from every cluster.
	res, err := s.Search(SearchQuery{Text: "deploy", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 30 {
		t.Fatalf("expected 30 results across all shards, got %d", len(res))
	}
	// Cluster filter must restrict to that cluster's shard.
	res, err = s.Search(SearchQuery{Text: "deploy", Clusters: []string{"cluster-2"}, Limit: 100})
	if err != nil {
		t.Fatalf("search filtered: %v", err)
	}
	if len(res) != 5 {
		t.Fatalf("expected 5 results for cluster-2, got %d", len(res))
	}
	for _, r := range res {
		if r.Resource.Cluster != "cluster-2" {
			t.Fatalf("cluster filter leaked: got %s", r.Resource.Cluster)
		}
	}
}

func TestShardedMatchesSingleIndexResults(t *testing.T) {
	single := NewMemoryIndex()
	sharded := NewShardedIndex(4)
	for c := 0; c < 5; c++ {
		cluster := fmt.Sprintf("cluster-%d", c)
		for _, kind := range []string{"Deployment", "Service", "Pod"} {
			for i := 0; i < 4; i++ {
				r := testResource(cluster, "apps", "v1", kind, "ns", fmt.Sprintf("%s-%d", kind, i))
				if err := single.Index(r); err != nil {
					t.Fatal(err)
				}
				if err := sharded.Index(r); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	q := SearchQuery{Text: "deploy", Limit: 100}
	want, _ := single.Search(q)
	got, _ := sharded.Search(q)
	if len(want) != len(got) {
		t.Fatalf("result count mismatch: single=%d sharded=%d", len(want), len(got))
	}
	// Scores must be sorted descending in the sharded result, same as single.
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Fatalf("sharded results not sorted by score at %d: %v < %v", i, got[i-1].Score, got[i].Score)
		}
	}
}

func TestShardedRemove(t *testing.T) {
	s := NewShardedIndex(4)
	r := testResource("cluster-a", "apps", "v1", "Deployment", "ns", "to-remove")
	if err := s.Index(r); err != nil {
		t.Fatal(err)
	}
	if s.DocumentCount() != 1 {
		t.Fatalf("expected 1 doc")
	}
	if err := s.Remove(r.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if s.DocumentCount() != 0 {
		t.Fatalf("expected 0 docs after remove, got %d", s.DocumentCount())
	}
}

func TestShardedSwapDataDistributes(t *testing.T) {
	// Build a full IndexData spanning several clusters, then swap it into the
	// sharded index. Every doc must end up searchable on its cluster's shard.
	d := NewIndexData()
	var prepared []PreparedResource
	for c := 0; c < 5; c++ {
		cluster := fmt.Sprintf("cluster-%d", c)
		for i := 0; i < 6; i++ {
			r := testResource(cluster, "apps", "v1", "Deployment", "ns", fmt.Sprintf("d-%d", i))
			prepared = append(prepared, PrepareResource(r))
		}
	}
	BatchIndexToData(d, prepared)

	s := NewShardedIndex(4)
	s.SwapData(d)

	if got := s.DocumentCount(); got != 30 {
		t.Fatalf("expected 30 docs after swap, got %d", got)
	}
	res, _ := s.Search(SearchQuery{Text: "deploy", Clusters: []string{"cluster-3"}, Limit: 100})
	if len(res) != 6 {
		t.Fatalf("expected 6 docs for cluster-3 after swap, got %d", len(res))
	}
}

func TestShardedConcurrentIndexAcrossClusters(t *testing.T) {
	s := NewShardedIndex(8)
	var wg sync.WaitGroup
	for c := 0; c < 16; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			cluster := fmt.Sprintf("cluster-%d", c)
			for i := 0; i < 100; i++ {
				r := testResource(cluster, "apps", "v1", "Deployment", "ns", fmt.Sprintf("d-%d", i))
				if err := s.Index(r); err != nil {
					t.Errorf("index: %v", err)
					return
				}
			}
		}(c)
	}
	wg.Wait()
	if got := s.DocumentCount(); got != 1600 {
		t.Fatalf("expected 1600 docs, got %d", got)
	}
}

func TestShardedSingleShardIsEquivalent(t *testing.T) {
	// A 1-shard sharded index must behave exactly like a bare MemoryIndex.
	s := NewShardedIndex(1)
	single := NewMemoryIndex()
	for i := 0; i < 10; i++ {
		r := testResource("c", "apps", "v1", "Deployment", "ns", fmt.Sprintf("d-%d", i))
		_ = s.Index(r)
		_ = single.Index(r)
	}
	a, _ := s.Search(SearchQuery{Text: "deploy", Limit: 100})
	b, _ := single.Search(SearchQuery{Text: "deploy", Limit: 100})
	if len(a) != len(b) {
		t.Fatalf("single-shard mismatch: %d vs %d", len(a), len(b))
	}
}

func TestRemoveByCoordinatesIgnoresKindForm(t *testing.T) {
	idx := NewMemoryIndex()
	r := testResource("c", "", "v1", "Pod", "ns", "pod-1")
	if err := idx.Index(r); err != nil {
		t.Fatal(err)
	}
	// Removing with the wrong kind via exact ID fails...
	if err := idx.Remove("c//v1/pods/ns/pod-1"); err == nil {
		t.Fatal("expected exact remove with plural kind to miss")
	}
	// ...but coordinate-based removal finds it regardless of kind form.
	if n := idx.RemoveByCoordinates("c", "ns", "pod-1"); n != 1 {
		t.Fatalf("expected 1 removed by coordinates, got %d", n)
	}
	if idx.DocumentCount() != 0 {
		t.Fatalf("expected 0 docs, got %d", idx.DocumentCount())
	}
}

func TestReconcileTypeRemovesStaleAcrossKindForms(t *testing.T) {
	idx := NewMemoryIndex()
	// Two pods indexed via the watch path (kind "Pod") and one via the reindex
	// path (kind "pods") — same cluster/group/version, different kind forms.
	mk := func(kind, name string) SearchableResource {
		r := testResource("c", "", "v1", kind, "ns", name)
		return r
	}
	if err := idx.Index(mk("Pod", "alive-1")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index(mk("Pod", "stale-watch")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index(mk("pods", "stale-reindex")); err != nil {
		t.Fatal(err)
	}
	if idx.DocumentCount() != 3 {
		t.Fatalf("expected 3, got %d", idx.DocumentCount())
	}

	// Live set from the cluster: only alive-1 remains.
	live := map[string]struct{}{"ns/alive-1": {}}
	removed := idx.ReconcileType("c", "", "v1", "pods", live)
	if len(removed) != 2 {
		t.Fatalf("expected 2 stale removed, got %d", len(removed))
	}
	if idx.DocumentCount() != 1 {
		t.Fatalf("expected 1 doc after reconcile, got %d", idx.DocumentCount())
	}
}

func TestReconcileTypeScopedToResourceType(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("c", "", "v1", "Pod", "ns", "p1")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index(testResource("c", "apps", "v1", "Deployment", "ns", "d1")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index(testResource("c", "", "v1", "Service", "ns", "s1")); err != nil {
		t.Fatal(err)
	}
	// Reconcile pods with an empty live set — must NOT touch the deployment or
	// the service that shares core/v1 with pods.
	removed := idx.ReconcileType("c", "", "v1", "pods", map[string]struct{}{})
	if len(removed) != 1 {
		t.Fatalf("expected 1 pod removed, got %d", len(removed))
	}
	if idx.DocumentCount() != 2 {
		t.Fatalf("deployment and service must survive, got %d docs", idx.DocumentCount())
	}
}
