package cache

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func getWithin(t *testing.T, d time.Duration, get func() (interface{}, error)) interface{} {
	t.Helper()
	type result struct {
		v   interface{}
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := get()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("get: %v", r.err)
		}
		return r.v
	case <-time.After(d):
		t.Fatalf("get did not return within %v", d)
		return nil
	}
}

func cachedValue(c *Cache, key string) interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.items[key].value
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An expired value used to make the next caller wait for the whole fetch.
// Within the stale window it is returned at once and refreshed once, in the
// background.
func TestGetOrSetSWRServesStaleWhileOneRefreshRuns(t *testing.T) {
	c := New(time.Minute, 0)
	var fetches atomic.Int32
	release := make(chan struct{})
	fetch := func() (interface{}, error) {
		if fetches.Add(1) == 1 {
			return "v1", nil
		}
		<-release
		return "v2", nil
	}
	get := func() (interface{}, error) { return c.GetOrSetSWR("k", 20*time.Millisecond, time.Minute, fetch) }

	if v := getWithin(t, time.Second, get); v != "v1" {
		t.Fatalf("first get = %v", v)
	}
	time.Sleep(40 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if v := getWithin(t, 200*time.Millisecond, get); v != "v1" {
			t.Fatalf("stale get = %v, want v1", v)
		}
	}
	waitFor(t, func() bool { return fetches.Load() == 2 })
	close(release)
	waitFor(t, func() bool { return cachedValue(c, "k") == "v2" })
	if n := fetches.Load(); n != 2 {
		t.Fatalf("%d fetches, want 2", n)
	}
}

func TestGetOrSetSWRWaitsPastTheStaleWindow(t *testing.T) {
	c := New(time.Minute, 0)
	n := 0
	fetch := func() (interface{}, error) { n++; return n, nil }
	if v, _ := c.GetOrSetSWR("k", 10*time.Millisecond, 10*time.Millisecond, fetch); v != 1 {
		t.Fatalf("first get = %v", v)
	}
	time.Sleep(30 * time.Millisecond)
	if v, _ := c.GetOrSetSWR("k", 10*time.Millisecond, 10*time.Millisecond, fetch); v != 2 {
		t.Fatalf("get past the stale window = %v, want a fresh value", v)
	}
}

func TestGetOrSetSWRKeepsStaleValueWhenRefreshFails(t *testing.T) {
	c := New(time.Minute, 0)
	if _, err := c.GetOrSetSWR("k", 10*time.Millisecond, time.Minute, func() (interface{}, error) { return "v1", nil }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	var failed atomic.Bool
	failing := func() (interface{}, error) { failed.Store(true); return nil, errors.New("apiserver down") }
	if v := getWithin(t, 200*time.Millisecond, func() (interface{}, error) { return c.GetOrSetSWR("k", 10*time.Millisecond, time.Minute, failing) }); v != "v1" {
		t.Fatalf("get = %v", v)
	}
	waitFor(t, func() bool { return failed.Load() })
	waitFor(t, func() bool {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return len(c.fetching) == 0
	})
	if v, err := c.GetOrSetSWR("k", 10*time.Millisecond, time.Minute, failing); err != nil || v != "v1" {
		t.Fatalf("after a failed refresh got %v, %v; want the stale value", v, err)
	}
}

// Watch events invalidate a cluster's entries several times a second. An entry
// kept stale-while-revalidate is expired rather than dropped, so the next read
// still answers at once; other entries are dropped as before.
func TestInvalidateExpiresSWREntriesAndDropsOthers(t *testing.T) {
	c := New(time.Minute, 0)
	if _, err := c.GetOrSetSWR("resources:c1:workloads", time.Minute, time.Minute, func() (interface{}, error) { return "counts", nil }); err != nil {
		t.Fatal(err)
	}
	c.Set("resources:c1:plain", "plain", time.Minute)
	c.Set("resources:c2:workloads", "other cluster", time.Minute)

	c.Invalidate("resources:c1:*")

	if _, ok := c.Get("resources:c1:plain"); ok {
		t.Error("plain entry survived invalidation")
	}
	if _, ok := c.Get("resources:c1:workloads"); ok {
		t.Error("stale-while-revalidate entry still fresh after invalidation")
	}
	if v, ok := c.Get("resources:c2:workloads"); !ok || v != "other cluster" {
		t.Error("invalidation reached another cluster's entry")
	}
	refreshed := make(chan struct{})
	v := getWithin(t, 200*time.Millisecond, func() (interface{}, error) {
		return c.GetOrSetSWR("resources:c1:workloads", time.Minute, time.Minute, func() (interface{}, error) {
			defer close(refreshed)
			return "new counts", nil
		})
	})
	if v != "counts" {
		t.Fatalf("read after invalidation = %v, want the previous counts", v)
	}
	<-refreshed
	waitFor(t, func() bool { return cachedValue(c, "resources:c1:workloads") == "new counts" })

	c.Invalidate("resources:c1:workloads")
	c.Delete("resources:c1:workloads")
	if _, err := c.GetOrSetSWR("resources:c1:workloads", time.Minute, time.Minute, func() (interface{}, error) { return "refetched", nil }); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("resources:c1:workloads"); v != "refetched" {
		t.Fatalf("after Delete the next read must wait for a fetch, got %v", v)
	}
}
