package watcher

import (
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// quietTopicHub models the event batcher on a topic that has been quiet for
// a whole interval: a live event goes out at once, unless the topic is held,
// in which case it waits for FlushTopic.
type quietTopicHub struct {
	mu     sync.Mutex
	held   int
	queued []Message
	sent   []Message
	// onBulk runs once, as the first snapshot page goes out.
	onBulk func()
}

func (h *quietTopicHub) Broadcast(topic string, m Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held > 0 {
		h.queued = append(h.queued, m)
	} else {
		h.sent = append(h.sent, m)
	}
	return nil
}

func (h *quietTopicHub) BroadcastDirect(topic string, m Message) error {
	h.mu.Lock()
	hook := h.onBulk
	if _, ok := m.(*BulkListMessage); ok {
		h.onBulk = nil
	} else {
		hook = nil
	}
	h.mu.Unlock()
	if hook != nil {
		hook()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, m)
	return nil
}

func (h *quietTopicHub) FlushTopic(string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, h.queued...)
	h.queued = nil
	return nil
}

func (h *quietTopicHub) HoldTopic(string) func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held++
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.held--
		if h.held == 0 {
			h.sent = append(h.sent, h.queued...)
			h.queued = nil
		}
	}
}

func (h *quietTopicHub) BroadcastAll(Message) error                { return nil }
func (h *quietTopicHub) SetSortPreference(_, _, _ string)          {}
func (h *quietTopicHub) GetSortPreference(string) (string, string) { return "name", "asc" }
func (h *quietTopicHub) CleanupTopic(string)                       {}

// A resync reads its snapshot, then sends it page by page while the watch
// keeps running. An object deleted after the snapshot was read must not have
// its deletion reach the client ahead of the page that still lists it: the
// page would add it back and the epoch's sync keep it, a row for an object
// that no longer exists.
func TestResyncSendsLiveEventsAfterItsSnapshot(t *testing.T) {
	hub := &quietTopicHub{}
	s := newServiceForResync(hub)
	topic := "items:c::v1:pods:ns"
	registerWatch(t, s, topic)
	gone := map[string]interface{}{"name": "x", "namespace": "ns", "uid": "ux"}
	s.cache.Set(topic, gone)
	s.cache.Set(topic, map[string]interface{}{"name": "y", "namespace": "ns", "uid": "uy"})
	hub.onBulk = func() {
		s.emitResourceEvent(topic, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "deleted", gone, false)
	}

	s.Resync(topic)

	hub.mu.Lock()
	defer hub.mu.Unlock()
	page, deletion, done := -1, -1, -1
	for i, m := range hub.sent {
		switch m := m.(type) {
		case *BulkListMessage:
			for _, item := range m.Items {
				if item["name"] == "x" {
					page = i
				}
			}
		case *ResourceEventMessage:
			if m.Action == "deleted" && m.Item["name"] == "x" {
				deletion = i
			}
		case *InitialSyncMessage:
			done = i
		}
	}
	if page < 0 || deletion < 0 || done < 0 {
		t.Fatalf("page %d, deletion %d, sync %d: something was not sent", page, deletion, done)
	}
	if deletion < page {
		t.Fatalf("the deletion (message %d) went out before the page listing the object (message %d)", deletion, page)
	}
}
