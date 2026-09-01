package watcher

import (
	"sort"
	"sync"
	"testing"
	"time"
)

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
