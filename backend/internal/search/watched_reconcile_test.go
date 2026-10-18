package search

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/search/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podDoc(cluster, name string, at time.Time) storage.SearchableResource {
	return storage.SearchableResource{
		ID: storage.BuildResourceID(cluster, "", "v1", "pods", "ns", name), Cluster: cluster, Kind: "Pod",
		APIVersion: "v1", Version: "v1", Name: name, Namespace: "ns", Category: "Workloads", UpdatedAt: at,
	}
}

// A deleted CRD, or an API version no longer served, used to stay a search
// kind forever: kind indexing only ever added definitions.
func TestIndexResourceKindsDropsKindsNoLongerDiscovered(t *testing.T) {
	s := newTestSearchService()
	s.index = storage.NewShardedIndex(storage.DefaultSearchShards)
	s.eventHandler = &ResourceEventHandler{index: s.index}
	key := s.cache.BuildKey("api-resources", "c")
	discover := func(rs ...metav1.APIResource) {
		s.cache.Set(key, rs, time.Hour)
		if err := s.indexResourceKinds("c"); err != nil {
			t.Fatal(err)
		}
	}
	verbs := []string{"list", "watch"}
	pods := metav1.APIResource{Name: "pods", Kind: "Pod", Version: "v1", Namespaced: true, Verbs: verbs}
	widgetsV1beta1 := metav1.APIResource{Name: "widgets", Kind: "Widget", Group: "example.io", Version: "v1beta1", Namespaced: true, Verbs: verbs}
	widgetsV1 := widgetsV1beta1
	widgetsV1.Version = "v1"
	gadgets := metav1.APIResource{Name: "gadgets", Kind: "Gadget", Group: "gadgets.io", Version: "v1", Verbs: verbs}

	discover(pods, widgetsV1beta1, gadgets)
	if n := len(s.index.KindDefinitions("c")); n != 3 {
		t.Fatalf("%d kind definitions, want 3", n)
	}
	// The Gadget CRD is deleted and Widget moves to v1.
	discover(pods, widgetsV1)
	got := map[string]bool{}
	for _, k := range s.index.KindDefinitions("c") {
		got[k.ID] = true
	}
	want := []string{"kind:c::v1:Pod", "kind:c:example.io:v1:Widget"}
	if len(got) != len(want) {
		t.Fatalf("kind definitions %v, want %v", got, want)
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("kind definitions %v, want %v", got, want)
		}
	}
}

// Pods deleted while the app was closed stay in the persisted index; the
// overview keeps the pods watch alive, so the sweep used to skip pods for good
// and the watcher never reported deletions of objects it had not seen.
func TestSweepReconcilesWatchedTypesOncePerSession(t *testing.T) {
	s := newTestSearchService()
	s.index = storage.NewShardedIndex(storage.DefaultSearchShards)
	s.eventHandler = &ResourceEventHandler{index: s.index}
	s.stopChan = make(chan struct{})
	s.indexingStatus = make(map[string]*IndexingStatus)
	s.indexedResources = make(map[string]map[string]time.Time)
	s.resourceVersions = make(map[string]map[string]string)
	s.cache.Set(s.cache.BuildKey("api-resources", "c"), []metav1.APIResource{
		{Name: "pods", Kind: "Pod", Version: "v1", Namespaced: true, Verbs: []string{"list", "watch"}},
	}, time.Hour)
	s.isWatched = func(cluster, group, version, resource string) bool { return resource == "pods" }

	lastSession := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{"alive", "deleted-while-closed"} {
		if err := s.index.Index(podDoc("c", name, lastSession)); err != nil {
			t.Fatal(err)
		}
	}
	// More pods than the watcher's snapshot carries: one was never delivered.
	var lists atomic.Int32
	s.listResources = func(cluster, group, version, resource, kind string) ([]storage.SearchableResource, string, error) {
		lists.Add(1)
		if resource != "pods" {
			return nil, "", fmt.Errorf("unexpected list of %s", resource)
		}
		now := time.Now()
		return []storage.SearchableResource{podDoc("c", "alive", now), podDoc("c", "past-snapshot-cap", now)}, "1", nil
	}

	s.performSmartIndexing("c")

	if s.index.HasDocument(storage.BuildResourceID("c", "", "v1", "pods", "ns", "deleted-while-closed")) {
		t.Fatal("a pod deleted while the app was closed is still indexed")
	}
	if !s.index.HasDocument(storage.BuildResourceID("c", "", "v1", "pods", "ns", "alive")) {
		t.Fatal("the live pod was removed")
	}
	if !s.index.HasDocument(storage.BuildResourceID("c", "", "v1", "pods", "ns", "past-snapshot-cap")) {
		t.Fatal("a live pod the watcher never delivered was not indexed")
	}
	if n := lists.Load(); n != 1 {
		t.Fatalf("%d lists, want 1", n)
	}

	s.performSmartIndexing("c")
	if n := lists.Load(); n != 1 {
		t.Fatalf("a watched type was listed again in the same session (%d lists)", n)
	}
}
