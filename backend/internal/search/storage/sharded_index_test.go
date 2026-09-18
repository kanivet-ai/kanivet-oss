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

func TestBulkLoaderRoutesEveryDocumentToItsClusterShard(t *testing.T) {
	// Feed documents spanning several clusters through the loader in two
	// batches. Every doc must land on the shard live indexing would pick, so
	// ID lookups and cluster-filtered searches keep working after the swap.
	var prepared []PreparedResource
	for c := 0; c < 5; c++ {
		cluster := fmt.Sprintf("cluster-%d", c)
		for i := 0; i < 6; i++ {
			r := testResource(cluster, "apps", "v1", "Deployment", "ns", fmt.Sprintf("d-%d", i))
			prepared = append(prepared, PrepareResource(r))
		}
	}

	s := NewShardedIndex(4)
	loader := s.NewBulkLoader(len(prepared))
	loader.Add(prepared[:17])
	loader.Add(prepared[17:])
	loader.Finish()

	if got := loader.Added(); got != 30 {
		t.Fatalf("expected loader to report 30 added, got %d", got)
	}
	if got := s.DocumentCount(); got != 30 {
		t.Fatalf("expected 30 docs after load, got %d", got)
	}
	for _, p := range prepared {
		if !s.HasDocument(p.Resource.ID) {
			t.Fatalf("doc %s not found on its cluster's shard", p.Resource.ID)
		}
	}
	res, _ := s.Search(SearchQuery{Text: "deploy", Clusters: []string{"cluster-3"}, Limit: 100})
	if len(res) != 6 {
		t.Fatalf("expected 6 docs for cluster-3 after load, got %d", len(res))
	}
	// Live indexing after the load must coexist with loaded documents.
	if err := s.Index(testResource("cluster-3", "apps", "v1", "Deployment", "ns", "d-live")); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Search(SearchQuery{Text: "deploy", Clusters: []string{"cluster-3"}, Limit: 100})
	if len(res) != 7 {
		t.Fatalf("expected 7 docs for cluster-3 after live index, got %d", len(res))
	}
}

func TestClusterFromIDHandlesKindDefinitionsWithColonsInCluster(t *testing.T) {
	cases := map[string]string{
		"c1/apps/v1/deployments/ns/api":                                         "c1",
		"arn:aws:eks:eu-west-1:123:cluster/prod/apps/v1/deployments/ns/api":     "arn:aws:eks:eu-west-1:123:cluster",
		"kind:c1:apps:v1:Deployment":                                            "c1",
		"kind:arn:aws:eks:eu-west-1:123:cluster/prod:apps:v1:Deployment":        "arn:aws:eks:eu-west-1:123:cluster/prod",
		"kind:vcluster:arn:aws:eks:eu-west-1:123:cluster/prod:team:dev::v1:Pod": "vcluster:arn:aws:eks:eu-west-1:123:cluster/prod:team:dev",
	}
	for id, want := range cases {
		if got := clusterFromID(id); got != want {
			t.Errorf("clusterFromID(%q) = %q, want %q", id, got, want)
		}
	}
	// A kind definition indexed by cluster must be found again by ID.
	s := NewShardedIndex(8)
	kind := SearchableResource{ID: "kind:arn:aws:eks:eu-west-1:123:cluster/prod:apps:v1:Deployment", Cluster: "arn:aws:eks:eu-west-1:123:cluster/prod", Kind: "KindDefinition", Name: "Deployment", Group: "apps", Version: "v1"}
	if err := s.Index(kind); err != nil {
		t.Fatal(err)
	}
	if !s.HasDocument(kind.ID) {
		t.Fatal("kind definition not found on its cluster's shard")
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
