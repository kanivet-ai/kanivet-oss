package watcher

import (
	"context"
	"testing"
	"time"
)

func noopWatch(context.Context) error { return nil }

// helper: count live watch entries
func (m *WatchManager) liveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.watches)
}

func TestWatchManagerEvictsGracePendingWhenOverCap(t *testing.T) {
	m := NewWatchManager()
	m.SetMaxWatches(3)
	defer m.Shutdown()

	// Start 3 watches and immediately release them so they enter grace period.
	for _, k := range []string{"a", "b", "c"} {
		if _, err := m.StartWatch(k, noopWatch); err != nil {
			t.Fatalf("start %s: %v", k, err)
		}
		m.StopWatch(k) // refCount -> 0, grace-pending
	}
	if got := m.liveCount(); got != 3 {
		t.Fatalf("expected 3 live watches, got %d", got)
	}

	// A 4th watch pushes us over the cap of 3. The oldest grace-pending watch
	// ("a") must be reclaimed immediately rather than waiting the grace period.
	if _, err := m.StartWatch("d", noopWatch); err != nil {
		t.Fatalf("start d: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if m.liveCount() <= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.liveCount(); got > 3 {
		t.Fatalf("expected eviction to keep live watches <= cap 3, got %d", got)
	}
	if m.HasWatch("a") {
		t.Fatalf("expected oldest grace-pending watch 'a' to be evicted")
	}
	if !m.HasWatch("d") {
		t.Fatalf("expected newest watch 'd' to remain")
	}
}

func TestWatchManagerNeverEvictsActiveWatches(t *testing.T) {
	m := NewWatchManager()
	m.SetMaxWatches(2)
	defer m.Shutdown()

	// Two actively-viewed watches (refCount stays > 0).
	for _, k := range []string{"x", "y"} {
		if _, err := m.StartWatch(k, noopWatch); err != nil {
			t.Fatalf("start %s: %v", k, err)
		}
	}
	// A third active watch exceeds the cap, but none are grace-pending, so
	// nothing may be evicted: active watches are what the user is viewing.
	if _, err := m.StartWatch("z", noopWatch); err != nil {
		t.Fatalf("start z: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	for _, k := range []string{"x", "y", "z"} {
		if !m.HasWatch(k) {
			t.Fatalf("active watch %s must not be evicted", k)
		}
	}
}

func TestWatchManagerNoCapByDefault(t *testing.T) {
	m := NewWatchManager()
	defer m.Shutdown()
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if _, err := m.StartWatch(k, noopWatch); err != nil {
			t.Fatalf("start %s: %v", k, err)
		}
		m.StopWatch(k)
	}
	if got := m.liveCount(); got != 5 {
		t.Fatalf("expected no eviction without a cap, got %d live", got)
	}
}
