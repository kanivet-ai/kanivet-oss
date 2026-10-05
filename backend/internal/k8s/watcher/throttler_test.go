package watcher

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// A watch event evicts the API's cached detail for its object (keyed on the
// resolved resource name, as the API keys it), but leaves the category counts
// alone unless the object came or went, and the cluster status alone unless a
// node or namespace did.
func TestResourceEventInvalidatesOnlyWhatItChanges(t *testing.T) {
	bus := cache.NewInvalidationBus()
	c := cache.NewCacheWithInvalidation(time.Minute, 0, bus)
	detail := c.BuildKey("detail", "c", "apps", "v1", "deployments", "ns", "d1")
	counts := c.BuildKey("resources", "c", "workloads")
	status := c.BuildKey("status", "c")

	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	s.invalidationBus = bus
	flushed := make(chan struct{}, 8)
	s.invalThrottler = newInvalidationThrottler(5*time.Millisecond, func(patterns []string) {
		for _, p := range patterns {
			bus.InvalidatePattern(p)
		}
		flushed <- struct{}{}
	})
	emit := func(topic string, gvr schema.GroupVersionResource, action, name string) {
		t.Helper()
		s.emitResourceEvent(topic, gvr, action, map[string]interface{}{"name": name, "namespace": "ns"}, true)
		select {
		case <-flushed:
		case <-time.After(time.Second):
			t.Fatal("invalidations were never flushed")
		}
	}
	reset := func() {
		for _, k := range []string{detail, counts, status} {
			c.Set(k, "cached", time.Minute)
		}
	}
	cached := func(k string) bool { _, ok := c.Get(k); return ok }
	deployments := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	reset()
	emit("items:c:apps:v1:deployments:", deployments, "modified", "d1")
	if cached(detail) {
		t.Error("modified event left the object's cached detail in place")
	}
	if !cached(counts) || !cached(status) {
		t.Error("modified event dropped the category counts or the cluster status")
	}

	reset()
	emit("items:c:apps:v1:deployments:", deployments, "added", "d2")
	if cached(counts) {
		t.Error("added event left stale category counts")
	}
	if !cached(status) || !cached(detail) {
		t.Error("adding a deployment dropped the cluster status or another object's detail")
	}

	reset()
	emit("items:c::v1:nodes:", schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, "deleted", "n1")
	if cached(status) {
		t.Error("deleting a node left the cluster status (and its node count) cached")
	}
}

func TestInvalidationThrottlerDedupesAndFlushes(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]string
	it := newInvalidationThrottler(20*time.Millisecond, func(patterns []string) {
		mu.Lock()
		flushed = append(flushed, patterns)
		mu.Unlock()
	})
	for i := 0; i < 100; i++ {
		it.add("resources:c1:*", "dashboard:c1")
	}
	it.add("detail:c1:g:v:pods:ns:p1")

	deadline := time.After(time.Second)
	for {
		mu.Lock()
		n := len(flushed)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for flush")
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("expected single flush, got %d", len(flushed))
	}
	got := append([]string(nil), flushed[0]...)
	sort.Strings(got)
	want := []string{"dashboard:c1", "detail:c1:g:v:pods:ns:p1", "resources:c1:*"}
	if len(got) != len(want) {
		t.Fatalf("expected %d unique patterns, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected patterns %v, got %v", want, got)
		}
	}
}

func TestInvalidationThrottlerShutdownStopsTimer(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	it := newInvalidationThrottler(10*time.Millisecond, func([]string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	it.add("x")
	it.shutdown()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("expected no flush after shutdown, got %d", calls)
	}
}

func TestResourceCacheCount(t *testing.T) {
	c := NewResourceCache()
	if c.Count("t") != 0 {
		t.Fatalf("expected 0 for empty topic")
	}
	c.Set("t", map[string]interface{}{"name": "a", "namespace": "ns"})
	c.Set("t", map[string]interface{}{"name": "b", "namespace": "ns"})
	c.Set("t", map[string]interface{}{"name": "a", "namespace": "ns"})
	if got := c.Count("t"); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	c.Delete("t", map[string]interface{}{"name": "a", "namespace": "ns"})
	if got := c.Count("t"); got != 1 {
		t.Fatalf("expected 1 after delete, got %d", got)
	}
}
