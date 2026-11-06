package watcher

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// listedServiceWithRows is a watched topic that has listed once and holds n
// cached rows.
func listedServiceWithRows(t *testing.T, hub *captureBroadcaster, n int) *Service {
	t.Helper()
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	st := s.pendingSync(context.Background(), testTopic)
	s.finishSync(context.Background(), st, nil)
	for i := range n {
		s.cache.Set(testTopic, map[string]interface{}{"name": fmt.Sprintf("p%03d", i), "namespace": "ns"})
	}
	return s
}

func replayedRows(hub *captureBroadcaster) int {
	n := 0
	for _, m := range hub.bulkLists() {
		n += len(m.Items)
	}
	return n
}

// A resync requested while a long list pass held the topic used to wait out
// cachedDataSyncWait, send a "timeout" sync with no rows, and return as if it
// had replayed: a client whose chunks were dropped stayed on a subset of the
// list until it reloaded. It now queues a replay that runs as soon as the
// pass releases the topic.
func TestResyncDuringLongListPassReplaysOnceThePassEnds(t *testing.T) {
	old := cachedDataSyncWait
	cachedDataSyncWait = 50 * time.Millisecond
	defer func() { cachedDataSyncWait = old }()

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := listedServiceWithRows(t, hub, 300)
	// A relist (fetchAndBroadcastListSync) holds the topic for its whole download.
	unlock, ok := s.lockTopicPass(context.Background(), testTopic, 0)
	if !ok {
		t.Fatal("lock")
	}

	if !s.Resync(testTopic) {
		t.Fatal("Resync reported failure for a replay it queued")
	}
	// Outlast cachedDataSyncWait while the pass still holds the topic.
	time.Sleep(3 * cachedDataSyncWait)
	if n := len(hub.bulkLists()); n != 0 {
		t.Fatalf("%d pages replayed while the list pass held the topic", n)
	}
	for _, m := range hub.syncCompletes() {
		if m.Failed && m.ErrorCode == "timeout" {
			t.Fatalf("resync gave up with a timeout sync instead of queueing: %+v", m)
		}
	}

	unlock()
	waitFor(t, "the queued replay", func() bool { return replayedRows(hub) == 300 })
	waitFor(t, "its sync_complete", func() bool {
		for _, m := range hub.syncCompletes() {
			if !m.Failed && m.Epoch > 0 && m.ItemCount == 300 {
				return true
			}
		}
		return false
	})
}

// Any number of resyncs queued behind one pass make a single replay.
func TestResyncsQueuedBehindOnePassCoalesce(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := listedServiceWithRows(t, hub, 10)
	unlock, ok := s.lockTopicPass(context.Background(), testTopic, 0)
	if !ok {
		t.Fatal("lock")
	}
	for range 5 {
		if !s.Resync(testTopic) {
			t.Fatal("Resync reported failure for a replay it queued")
		}
	}
	unlock()
	waitFor(t, "the queued replay", func() bool { return len(hub.syncCompletes()) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(hub.syncCompletes()); n != 1 {
		t.Fatalf("%d replays for 5 queued resyncs, want 1", n)
	}
	if n := replayedRows(hub); n != 10 {
		t.Fatalf("%d rows replayed, want 10", n)
	}
}

// A queued replay that finds the topic taken again by another pass queues
// behind that one rather than being lost or racing it.
func TestQueuedReplayWaitsForTheNextPassToo(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := listedServiceWithRows(t, hub, 10)
	first, _ := s.lockTopicPass(context.Background(), testTopic, 0)
	s.Resync(testTopic)
	// A second pass is already waiting, and may take the topic as the
	// first releases it, before the queued replay does.
	got := make(chan func())
	go func() {
		u, _ := s.lockTopicPass(context.Background(), testTopic, 0)
		got <- u
	}()
	time.Sleep(20 * time.Millisecond)
	first()
	second := <-got
	time.Sleep(50 * time.Millisecond)
	if n := len(hub.bulkLists()); n != 0 {
		t.Fatalf("%d pages replayed while the second pass held the topic", n)
	}
	second()
	waitFor(t, "the queued replay", func() bool { return replayedRows(hub) == 10 })
}

// A resync whose topic is still on its first list reports that it replayed
// nothing, so the coordinator tries again.
func TestResyncReportsFailureWhileTheFirstListIsInFlight(t *testing.T) {
	old := cachedDataSyncWait
	cachedDataSyncWait = 30 * time.Millisecond
	defer func() { cachedDataSyncWait = old }()

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	st := s.pendingSync(context.Background(), testTopic)
	defer s.finishSync(context.Background(), st, nil)
	if s.Resync(testTopic) {
		t.Fatal("Resync reported success with the first list still in flight")
	}
	if n := len(hub.bulkLists()); n != 0 {
		t.Fatalf("%d pages replayed", n)
	}
}
