package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testResource(cluster, group, version, kind, namespace, name string) SearchableResource {
	id := fmt.Sprintf("%s/%s/%s/%s/%s/%s", cluster, group, version, strings.ToLower(kind), namespace, name)
	return SearchableResource{
		ID:          id,
		Cluster:     cluster,
		Group:       group,
		Version:     version,
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		Category:    "Workloads",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
}

func TestSearchRespectsOffsetLimitAndClusters(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("cluster-a", "apps", "v1", "Deployment", "ns-a", "alpha-deploy")); err != nil {
		t.Fatalf("index alpha: %v", err)
	}
	if err := idx.Index(testResource("cluster-a", "apps", "v1", "Deployment", "ns-a", "beta-deploy")); err != nil {
		t.Fatalf("index beta: %v", err)
	}
	if err := idx.Index(testResource("cluster-b", "apps", "v1", "Deployment", "ns-b", "gamma-deploy")); err != nil {
		t.Fatalf("index gamma: %v", err)
	}

	results, err := idx.Search(SearchQuery{
		Text:     "deploy",
		Clusters: []string{"cluster-a"},
		Offset:   1,
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Resource.Cluster != "cluster-a" {
		t.Fatalf("expected cluster-a, got %s", results[0].Resource.Cluster)
	}
}

func TestListResourceIDsByTypeTracksRemovals(t *testing.T) {
	idx := NewMemoryIndex()
	r1 := testResource("cluster-a", "", "v1", "Pod", "ns-a", "pod-a")
	r2 := testResource("cluster-a", "", "v1", "Pod", "ns-a", "pod-b")
	r3 := testResource("cluster-a", "", "v1", "Service", "ns-a", "svc-a")

	for _, r := range []SearchableResource{r1, r2, r3} {
		if err := idx.Index(r); err != nil {
			t.Fatalf("index %s: %v", r.ID, err)
		}
	}

	ids := idx.ListResourceIDsByType("cluster-a", "", "v1", "Pod")
	if len(ids) != 2 {
		t.Fatalf("expected 2 pod ids, got %d", len(ids))
	}

	if err := idx.BatchRemove([]string{r1.ID}); err != nil {
		t.Fatalf("batch remove: %v", err)
	}
	ids = idx.ListResourceIDsByType("cluster-a", "", "v1", "Pod")
	if len(ids) != 1 {
		t.Fatalf("expected 1 pod id after remove, got %d", len(ids))
	}
	if ids[0] != r2.ID {
		t.Fatalf("expected remaining id %s, got %s", r2.ID, ids[0])
	}
}

func TestFindKindsLikeUsesIndexedResourceTypes(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("cluster-a", "apps", "v1", "Deployment", "ns-a", "d1")); err != nil {
		t.Fatalf("index deployment: %v", err)
	}
	if err := idx.Index(testResource("cluster-b", "", "v1", "Service", "ns-b", "s1")); err != nil {
		t.Fatalf("index service: %v", err)
	}

	infos := idx.FindKindsLike("dep", []string{"cluster-a"}, 10)
	if len(infos) == 0 {
		t.Fatalf("expected at least one kind match")
	}
	if infos[0].Cluster != "cluster-a" {
		t.Fatalf("expected cluster-a, got %s", infos[0].Cluster)
	}
	if !strings.EqualFold(infos[0].Kind, "Deployment") {
		t.Fatalf("expected Deployment kind, got %s", infos[0].Kind)
	}
}

func TestSuggestionsReturnsPrefixMatches(t *testing.T) {
	idx := NewMemoryIndex()
	if err := idx.Index(testResource("cluster-a", "apps", "v1", "Deployment", "ns-a", "deploy-app")); err != nil {
		t.Fatalf("index resource: %v", err)
	}

	suggestions := idx.Suggestions("dep", 10)
	if len(suggestions) == 0 {
		t.Fatalf("expected suggestions for prefix dep")
	}
}
