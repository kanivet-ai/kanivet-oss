package search

import (
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A Gateway indexed from a watch event on the gateways topic and one indexed
// with no topic coordinates must be the same document: the topic's resource
// name and the pluralized kind agree, and the document keeps the real Kind.
func TestWatchEventUsesTopicResourceNameForID(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	coords := resourceCoords{group: "gateway.networking.k8s.io", version: "v1", resource: "gateways", kind: "Gateway"}
	item := map[string]interface{}{"name": "edge", "namespace": "ns", "kind": "Gateway", "apiVersion": "gateway.networking.k8s.io/v1"}
	if _, err := h.OnAddWithCoords("c", coords, item); err != nil {
		t.Fatal(err)
	}
	want := storage.BuildResourceID("c", "gateway.networking.k8s.io", "v1", "gateways", "ns", "edge")
	if !h.index.HasDocument(want) {
		t.Fatalf("document %q not indexed", want)
	}
	if _, err := h.OnAdd("c", item); err != nil {
		t.Fatal(err)
	}
	if n := h.index.DocumentCount(); n != 1 {
		t.Fatalf("watch path with and without coordinates produced %d documents, want 1", n)
	}
	results, err := h.index.Search(storage.SearchQuery{Text: "edge", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Resource.Kind != "Gateway" {
		t.Fatalf("want one Gateway result, got %+v", results)
	}
}

// A tombstone delete that lost its kind is still removed through the topic's
// resource name, even for a kind the suffix rules would pluralize differently.
func TestDeleteWithoutKindRemovesIrregularPlural(t *testing.T) {
	h := &ResourceEventHandler{index: storage.NewShardedIndex(1)}
	coords := resourceCoords{group: "monitoring.coreos.com", version: "v1", resource: "prometheuses", kind: "Prometheus"}
	item := map[string]interface{}{"name": "main", "namespace": "ns", "kind": "Prometheus", "apiVersion": "monitoring.coreos.com/v1"}
	if _, err := h.OnAddWithCoords("c", coords, item); err != nil {
		t.Fatal(err)
	}
	if err := h.onDeleteWithCoords("c", coords, map[string]interface{}{"name": "main", "namespace": "ns"}); err != nil {
		t.Fatal(err)
	}
	if n := h.index.DocumentCount(); n != 0 {
		t.Fatalf("delete left %d documents", n)
	}
}

// Rows written by earlier releases describe one object under several IDs.
// Until the sweep replaces them, search must still show the object once, with
// the plural resource name filled in.
func TestSearchDedupesLegacyDocumentShapes(t *testing.T) {
	s := newTestSearchService()
	now := time.Now()
	for _, doc := range []storage.SearchableResource{
		{ID: "c/apps/v1/deployments/ns/web", Cluster: "c", Kind: "Deployment", APIVersion: "apps/v1", Group: "apps", Version: "v1", Namespace: "ns", Name: "web", Category: "Workloads", UpdatedAt: now},
		{ID: "c/apps/v1/deployment/ns/web", Cluster: "c", Kind: "Deployment", APIVersion: "apps/v1", Group: "apps", Version: "v1", Namespace: "ns", Name: "web", Category: "Workloads", UpdatedAt: now},
		{ID: "c///deployments/ns/web", Cluster: "c", Kind: "deployments", APIVersion: "apps/v1", Group: "apps", Version: "v1", Namespace: "ns", Name: "web", Category: "Workloads", UpdatedAt: now},
		{ID: "c/apps/v1/deployments/ns/web-canary", Cluster: "c", Kind: "Deployment", APIVersion: "apps/v1", Group: "apps", Version: "v1", Namespace: "ns", Name: "web-canary", Category: "Workloads", UpdatedAt: now},
	} {
		if err := s.index.Index(doc); err != nil {
			t.Fatal(err)
		}
	}
	results, err := s.searchWithKinds(SearchQuery{Text: "web", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var regular []SearchResult
	for _, r := range results {
		if r.Resource.Kind != "KindDefinition" {
			regular = append(regular, r)
		}
	}
	if len(regular) != 2 {
		t.Fatalf("want 2 distinct objects, got %d: %+v", len(regular), regular)
	}
	for _, r := range regular {
		if r.Resource.Resource != "deployments" {
			t.Errorf("result %s carries resource %q, want deployments", r.Resource.ID, r.Resource.Resource)
		}
	}
}

// Search results and kind definitions carry the resource name discovery
// reported, so the UI never has to pluralize a kind itself.
func TestResourceNameForPrefersDiscovery(t *testing.T) {
	s := newTestSearchService()
	s.rememberAPIResources("c", []metav1.APIResource{
		{Name: "redis", Kind: "Redis", Group: "cache.example.com", Version: "v1"},
		{Name: "gateways", Kind: "Gateway", Group: "gateway.networking.k8s.io", Version: "v1"},
	})
	if got := s.ResourceNameFor("c", "cache.example.com", "v1", "Redis"); got != "redis" {
		t.Errorf("ResourceNameFor(Redis) = %q", got)
	}
	if got := s.ResourceNameFor("c", "gateway.networking.k8s.io", "v1", "gateways"); got != "gateways" {
		t.Errorf("ResourceNameFor(gateways) = %q", got)
	}
	if got := s.ResourceNameFor("other", "apps", "v1", "Deployment"); got != "deployments" {
		t.Errorf("ResourceNameFor without discovery = %q", got)
	}
	if got := s.kindFor("c", "gateway.networking.k8s.io", "v1", "gateways"); got != "Gateway" {
		t.Errorf("kindFor(gateways) = %q", got)
	}
}

// Discovery lists every served version of a resource; indexing keeps one.
func TestPreferredAPIResourcesKeepsOnePerResource(t *testing.T) {
	s := newTestSearchService()
	in := []metav1.APIResource{
		{Name: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Group: "autoscaling", Version: "v2"},
		{Name: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Group: "autoscaling", Version: "v1"},
		{Name: "pods", Kind: "Pod", Group: "", Version: "v1"},
	}
	out := s.preferredAPIResources("c", in)
	if len(out) != 2 {
		t.Fatalf("want 2 resources, got %d", len(out))
	}
	if out[0].Version != "v2" {
		t.Fatalf("without preference data the first served version is kept, got %s", out[0].Version)
	}
}
