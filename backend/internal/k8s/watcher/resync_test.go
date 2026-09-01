package watcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type captureBroadcaster struct {
	mu             sync.Mutex
	direct         []Message
	batched        []Message
	failDirectCall int
	directCalls    int
	sortBy         string
	sortOrder      string
}

func (c *captureBroadcaster) Broadcast(topic string, message Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batched = append(c.batched, message)
	return nil
}

func (c *captureBroadcaster) BroadcastDirect(topic string, message Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.directCalls++
	if c.failDirectCall > 0 && c.directCalls == c.failDirectCall {
		return fmt.Errorf("simulated backpressure on call %d", c.directCalls)
	}
	c.direct = append(c.direct, message)
	return nil
}

func (c *captureBroadcaster) BroadcastAll(message Message) error        { return nil }
func (c *captureBroadcaster) FlushTopic(topic string) error             { return nil }
func (c *captureBroadcaster) SetSortPreference(topic, by, order string) {}
func (c *captureBroadcaster) GetSortPreference(topic string) (string, string) {
	return c.sortBy, c.sortOrder
}
func (c *captureBroadcaster) CleanupTopic(topic string) {}

func (c *captureBroadcaster) bulkLists() []*BulkListMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*BulkListMessage
	for _, m := range c.direct {
		if bl, ok := m.(*BulkListMessage); ok {
			out = append(out, bl)
		}
	}
	return out
}

func (c *captureBroadcaster) syncCompletes() []*InitialSyncMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*InitialSyncMessage
	for _, m := range c.batched {
		if sc, ok := m.(*InitialSyncMessage); ok {
			out = append(out, sc)
		}
	}
	return out
}

func newServiceForResync(hub Broadcaster) *Service {
	return &Service{
		hub:       hub,
		manager:   NewWatchManager(),
		cache:     NewResourceCache(),
		syncState: make(map[string]*syncStatus),
		epochs:    make(map[string]uint64),
	}
}

func registerWatch(t *testing.T, s *Service, topic string) {
	t.Helper()
	if _, err := s.manager.StartWatch(topic, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestResyncCarriesEpochAndCompletes(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	topic := "items:cluster-a:apps:v1:Deployment:ns"
	registerWatch(t, s, topic)
	s.cache.Set(topic, map[string]interface{}{"name": "d1", "namespace": "ns"})
	s.cache.Set(topic, map[string]interface{}{"name": "d2", "namespace": "ns"})

	s.Resync(topic)

	bulks := hub.bulkLists()
	if len(bulks) == 0 {
		t.Fatal("expected Resync to re-broadcast cached items via BroadcastDirect, got none")
	}
	for _, bl := range bulks {
		if bl.Epoch == 0 {
			t.Fatalf("bulk_list chunk missing epoch: %+v", bl)
		}
	}
	syncs := hub.syncCompletes()
	if len(syncs) != 1 {
		t.Fatalf("expected exactly one sync_complete after resync, got %d", len(syncs))
	}
	if syncs[0].Epoch != bulks[0].Epoch {
		t.Fatalf("sync_complete epoch %d != bulk_list epoch %d", syncs[0].Epoch, bulks[0].Epoch)
	}
	if syncs[0].ItemCount != 2 {
		t.Fatalf("expected sync_complete count 2, got %d", syncs[0].ItemCount)
	}
}

func TestResyncEmptyWatchedTopicSendsAuthoritativeEmpty(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	topic := "items:cluster-a:apps:v1:Deployment:ns"
	registerWatch(t, s, topic)

	s.Resync(topic)

	syncs := hub.syncCompletes()
	if len(syncs) != 1 {
		t.Fatalf("watched-but-empty topic must broadcast an authoritative empty snapshot, got %d sync_completes", len(syncs))
	}
	if syncs[0].Epoch == 0 || syncs[0].ItemCount != 0 {
		t.Fatalf("expected epoch>0 and count 0, got epoch=%d count=%d", syncs[0].Epoch, syncs[0].ItemCount)
	}
}

func TestResyncUnwatchedTopicIsNoop(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)

	s.Resync("items:cluster-a:apps:v1:Deployment:ns")

	if hub.directCalls != 0 || len(hub.syncCompletes()) != 0 {
		t.Fatalf("expected no broadcast for unwatched topic, got %d direct and %d syncs", hub.directCalls, len(hub.syncCompletes()))
	}
}

func TestResyncContinuesPastChunkErrors(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc", failDirectCall: 2}
	s := newServiceForResync(hub)
	topic := "items:cluster-a:apps:v1:Deployment:ns"
	registerWatch(t, s, topic)
	for i := 0; i < 350; i++ {
		s.cache.Set(topic, map[string]interface{}{"name": fmt.Sprintf("d%03d", i), "namespace": "ns"})
	}

	s.Resync(topic)

	if hub.directCalls != 3 {
		t.Fatalf("one failed chunk must not truncate the stream: expected 3 direct calls, got %d", hub.directCalls)
	}
	if len(hub.syncCompletes()) != 1 {
		t.Fatalf("sync_complete must still be sent after a chunk error, got %d", len(hub.syncCompletes()))
	}
}

func TestNextEpochMonotonicPerTopic(t *testing.T) {
	s := newServiceForResync(&captureBroadcaster{})
	a1 := s.nextEpoch("a")
	a2 := s.nextEpoch("a")
	b1 := s.nextEpoch("b")
	if a2 <= a1 || b1 == 0 {
		t.Fatalf("epochs must be monotonic per topic: a1=%d a2=%d b1=%d", a1, a2, b1)
	}
}
