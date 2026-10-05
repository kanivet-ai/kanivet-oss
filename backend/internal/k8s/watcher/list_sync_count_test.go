package watcher

import (
	"context"
	"fmt"
	"testing"
	"time"

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

func newListSyncService(hub Broadcaster) *Service {
	s := newServiceForResync(hub)
	s.countThrottler = newCountThrottler(time.Hour, func(string, string, string, int) {})
	return s
}

// The client only drops the rows a sync no longer covers once it has seen
// itemCount rows tagged with the sync's epoch, so every list page must carry
// it.
func TestListSyncPagesCarryTheSyncEpoch(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	lister := &fakeListLister{}
	for i := 0; i < 30; i++ {
		lister.items = append(lister.items, mkListObj(fmt.Sprintf("d-%02d", i)))
	}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", gvr, "", "items:c:apps:v1:deployments:", lister); err != nil {
		t.Fatal(err)
	}

	syncs := hub.syncCompletes()
	if len(syncs) != 1 || syncs[0].Epoch == 0 {
		t.Fatalf("expected one authoritative sync_complete, got %+v", syncs)
	}
	seen := map[string]bool{}
	for _, bl := range hub.bulkLists() {
		if bl.Epoch != syncs[0].Epoch {
			t.Fatalf("bulk_list page epoch %d, sync_complete epoch %d", bl.Epoch, syncs[0].Epoch)
		}
		for _, item := range bl.Items {
			seen[itemKeyOf(item)] = true
		}
	}
	if len(seen) != syncs[0].ItemCount {
		t.Fatalf("pages carried %d distinct rows, sync_complete claims %d", len(seen), syncs[0].ItemCount)
	}
}

// splitLister serves a quick first page that still has an object the full
// list, fetched a moment later, no longer has.
type splitLister struct {
	first, full []unstructured.Unstructured
}

func (l *splitLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	list := &unstructured.UnstructuredList{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"resourceVersion": "42"},
	}}
	if opts.Limit > 0 {
		list.Items = l.first
	} else {
		list.Items = l.full
	}
	return list, nil
}

func (l *splitLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, fmt.Errorf("watch not supported in fake")
}

func (l *splitLister) Namespace(ns string) resourceLister { return l }

// The watch resumes from the full list's resourceVersion, so an object
// deleted between the two lists never gets a delete event: the sync itself
// must drop it.
func TestListSyncDropsObjectDeletedBetweenTheTwoLists(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	lister := &splitLister{
		first: []unstructured.Unstructured{mkListObj("a"), mkListObj("gone"), mkListObj("c")},
		full:  []unstructured.Unstructured{mkListObj("a"), mkListObj("c")},
	}
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	topic := "items:c:apps:v1:deployments:"

	if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", gvr, "", topic, lister); err != nil {
		t.Fatal(err)
	}

	if syncs := hub.syncCompletes(); len(syncs) != 1 || syncs[0].ItemCount != 2 {
		t.Fatalf("expected a sync_complete for 2 items, got %+v", syncs)
	}
	for _, item := range s.cache.GetAll(topic) {
		if item["name"] == "gone" {
			t.Fatal("the object deleted between the lists is still cached")
		}
	}
	deleted := false
	hub.mu.Lock()
	for _, m := range hub.batched {
		if ev, ok := m.(*ResourceEventMessage); ok && ev.Action == "deleted" && ev.Item["name"] == "gone" {
			deleted = true
		}
	}
	hub.mu.Unlock()
	if !deleted {
		t.Fatal("no delete event for the object only the first page showed")
	}
}

// failingFirstPageLister fails the quick first page while the full list is
// in flight; the full list returns only once its context ends.
type failingFirstPageLister struct {
	fullDone chan error
}

func (l *failingFirstPageLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if opts.Limit > 0 {
		return nil, fmt.Errorf("first page failed")
	}
	<-ctx.Done()
	l.fullDone <- ctx.Err()
	return nil, ctx.Err()
}

func (l *failingFirstPageLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return nil, fmt.Errorf("watch not supported in fake")
}

func (l *failingFirstPageLister) Namespace(ns string) resourceLister { return l }

func TestListSyncCancelsFullListWhenFirstPageFails(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	lister := &failingFirstPageLister{fullDone: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	if _, err := s.fetchAndBroadcastListSync(ctx, "c", gvr, "", "items:c:apps:v1:deployments:", lister); err == nil {
		t.Fatal("expected the first page's error")
	}

	select {
	case <-lister.fullDone:
	case <-time.After(time.Second):
		t.Fatal("the full list kept running after the first page failed")
	}
}
