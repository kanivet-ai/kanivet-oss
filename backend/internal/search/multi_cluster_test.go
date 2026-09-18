package search

import (
	"fmt"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
)

func indexPod(t *testing.T, s *Service, cluster, name string) {
	t.Helper()
	err := s.index.Index(storage.SearchableResource{
		ID:         fmt.Sprintf("%s:default:Pod:%s", cluster, name),
		Cluster:    cluster,
		Kind:       "Pod",
		APIVersion: "v1",
		Version:    "v1",
		Name:       name,
		Namespace:  "default",
		Category:   "Workloads",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("index %s/%s: %v", cluster, name, err)
	}
}

func TestParseQueryKeepsEverySelectedCluster(t *testing.T) {
	s := newTestSearchService()

	multi := s.parseQuery("api", SearchOptions{Clusters: []string{"cluster-c", "cluster-a", "cluster-b"}})
	if multi.Cluster != "" {
		t.Fatalf("multi-cluster query must not collapse to one cluster, got Cluster=%q", multi.Cluster)
	}
	if got := fmt.Sprint(multi.Clusters); got != "[cluster-a cluster-b cluster-c]" {
		t.Fatalf("want all three clusters sorted, got %s", got)
	}

	single := s.parseQuery("api", SearchOptions{Clusters: []string{"cluster-a"}})
	if single.Cluster != "cluster-a" || len(single.Clusters) != 0 {
		t.Fatalf("single selection should use the exact-match fast path, got %+v", single)
	}
}

// "Search selected clusters" with three clusters checked must return hits
// from all three, and nothing from an unchecked cluster.
func TestSearchSpansEverySelectedCluster(t *testing.T) {
	s := newTestSearchService()
	indexPod(t, s, "cluster-a", "api-a")
	indexPod(t, s, "cluster-b", "api-b")
	indexPod(t, s, "cluster-c", "api-c")
	indexPod(t, s, "cluster-d", "api-d")

	results, err := s.Search("api", SearchOptions{Clusters: []string{"cluster-c", "cluster-a", "cluster-b"}, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Resource.Cluster] = true
	}
	for _, want := range []string{"cluster-a", "cluster-b", "cluster-c"} {
		if !seen[want] {
			t.Fatalf("results missing selected cluster %s; got clusters %v", want, seen)
		}
	}
	if seen["cluster-d"] {
		t.Fatalf("results include unselected cluster-d: %v", seen)
	}
}

func TestSearchCacheKeyDistinguishesClusterSets(t *testing.T) {
	s := newTestSearchService()
	one := s.parseQuery("api", SearchOptions{Clusters: []string{"cluster-a"}, Limit: 20})
	two := s.parseQuery("api", SearchOptions{Clusters: []string{"cluster-a", "cluster-b"}, Limit: 20})
	if s.buildSearchCacheKey("api", one) == s.buildSearchCacheKey("api", two) {
		t.Fatal("selecting an extra cluster must not hit the single-cluster cache entry")
	}

	// Invalidating any selected cluster must miss the multi-cluster entry.
	before := s.buildSearchCacheKey("api", two)
	s.invalidateSearchCacheForCluster("cluster-b")
	if s.buildSearchCacheKey("api", two) == before {
		t.Fatal("cache key unchanged after invalidating one of the selected clusters")
	}
}
