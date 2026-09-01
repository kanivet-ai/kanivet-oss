package watcher

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type fakeListLister struct {
	items []unstructured.Unstructured
}

func (f *fakeListLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	list := &unstructured.UnstructuredList{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"resourceVersion": "42"},
	}}
	if opts.Limit > 0 && int(opts.Limit) < len(f.items) {
		list.Items = f.items[:opts.Limit]
		return list, nil
	}
	list.Items = f.items
	return list, nil
}

func (f *fakeListLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, fmt.Errorf("watch not supported in fake")
}

func (f *fakeListLister) Namespace(ns string) resourceLister { return f }

func mkListObj(name string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name":              name,
			"namespace":         "ns",
			"uid":               "uid-" + name,
			"resourceVersion":   "1",
			"creationTimestamp": "2026-01-01T00:00:00Z",
		},
	}}
}

// The quick 25-item first page and the concurrent full RV=0 list overlap;
// the reported item count must be the deduplicated set, not the sum of pages.
func TestListSyncItemCountNotDoubleCounted(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	const total = 30
	lister := &fakeListLister{}
	for i := 0; i < total; i++ {
		lister.items = append(lister.items, mkListObj(fmt.Sprintf("d-%02d", i)))
	}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	topic := "items:cluster-a:apps:v1:deployments:ns"

	rv, err := s.fetchAndBroadcastListSync(context.Background(), "cluster-a", gvr, "ns", topic, lister)
	if err != nil {
		t.Fatal(err)
	}
	if rv != "42" {
		t.Fatalf("expected list RV 42, got %q", rv)
	}
	syncs := hub.syncCompletes()
	if len(syncs) != 1 {
		t.Fatalf("expected 1 sync_complete, got %d", len(syncs))
	}
	if syncs[0].ItemCount != total {
		t.Fatalf("expected item count %d, got %d", total, syncs[0].ItemCount)
	}
	if got := s.cache.Count(topic); got != total {
		t.Fatalf("expected %d cached items, got %d", total, got)
	}
}
