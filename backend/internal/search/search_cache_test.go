package search

import (
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/search/storage"
)

func newTestSearchService() *Service {
	return &Service{
		cache:                cache.New(time.Minute, time.Minute),
		config:               defaultConfig(),
		index:                storage.NewShardedIndex(1),
		recentSearches:       make([]RecentResource, 0, 10),
		clusterSearchVersion: make(map[string]uint64),
	}
}

func TestBuildSearchCacheKeyIsCanonical(t *testing.T) {
	s := newTestSearchService()

	q1 := s.parseQuery(" Pods ", SearchOptions{
		Clusters:   []string{"cluster-b", "cluster-a"},
		Namespaces: []string{"ns-b", "ns-a"},
		Kinds:      []string{"Deployment", "Pod"},
		Limit:      20,
		Offset:     5,
	})
	q2 := s.parseQuery("pods", SearchOptions{
		Clusters:   []string{"cluster-a", "cluster-b"},
		Namespaces: []string{"ns-a", "ns-b"},
		Kinds:      []string{"Pod", "Deployment"},
		Limit:      20,
		Offset:     5,
	})

	k1 := s.buildSearchCacheKey(" Pods ", q1)
	k2 := s.buildSearchCacheKey("pods", q2)
	if k1 != k2 {
		t.Fatalf("expected canonical cache key equality, got %q != %q", k1, k2)
	}
}

func TestInvalidateSearchCacheForClusterChangesVersion(t *testing.T) {
	s := newTestSearchService()
	q := s.parseQuery("deploy", SearchOptions{
		Clusters: []string{"cluster-a"},
		Limit:    20,
	})

	before := s.buildSearchCacheKey("deploy", q)
	s.invalidateSearchCacheForCluster("cluster-a")
	after := s.buildSearchCacheKey("deploy", q)
	if before == after {
		t.Fatalf("expected cache key to change after cluster invalidation")
	}
}

func TestInvalidateSearchCacheForClusterChangesGlobalVersion(t *testing.T) {
	s := newTestSearchService()
	q := s.parseQuery("deploy", SearchOptions{Limit: 20})

	before := s.buildSearchCacheKey("deploy", q)
	s.invalidateSearchCacheForCluster("cluster-a")
	after := s.buildSearchCacheKey("deploy", q)
	if before == after {
		t.Fatalf("expected global cache key to change after cluster invalidation")
	}
}

func TestOnInvalidateChangesClusterScopedKey(t *testing.T) {
	s := newTestSearchService()
	q := s.parseQuery("deploy", SearchOptions{
		Clusters: []string{"cluster-a"},
		Limit:    20,
	})

	before := s.buildSearchCacheKey("deploy", q)
	s.OnInvalidate("search:*")
	after := s.buildSearchCacheKey("deploy", q)
	if before == after {
		t.Fatalf("expected cluster-scoped cache key to change after global invalidation")
	}
}
