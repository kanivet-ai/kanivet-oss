package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

type seqMsg struct {
	core.BaseMessage
	action string
	item   map[string]interface{}
}

func (m *seqMsg) Marshal() ([]byte, error) {
	return json.Marshal(map[string]interface{}{"action": m.action, "item": m.item})
}

func (m *seqMsg) GetData() map[string]interface{} {
	return map[string]interface{}{"action": m.action, "item": m.item}
}

type recordingBroadcaster struct {
	mu         sync.Mutex
	batches    [][]json.RawMessage
	delayFirst time.Duration
	onFirst    func()
	calls      int
}

func (r *recordingBroadcaster) Broadcast(topic string, msg core.Message) error {
	r.mu.Lock()
	r.calls++
	first := r.calls == 1
	r.mu.Unlock()
	if first {
		if r.onFirst != nil {
			r.onFirst()
		}
		if r.delayFirst > 0 {
			time.Sleep(r.delayFirst)
		}
	}
	bm, ok := msg.(*BatchedMessage)
	if !ok {
		return fmt.Errorf("unexpected message type %T", msg)
	}
	r.mu.Lock()
	r.batches = append(r.batches, bm.Events)
	r.mu.Unlock()
	return nil
}

func (r *recordingBroadcaster) flatActions(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, batch := range r.batches {
		for _, raw := range batch {
			var ev struct {
				Action string                 `json:"action"`
				Item   map[string]interface{} `json:"item"`
			}
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("unmarshal event: %v", err)
			}
			out = append(out, ev.Action+":"+ev.Item["uid"].(string))
		}
	}
	return out
}

func TestFlushPreservesArrivalOrderForMixedActions(t *testing.T) {
	rec := &recordingBroadcaster{}
	eb := NewEventBatcher(rec, time.Hour, 100)
	eb.SetSortPreference("t", "age", "desc")
	eb.AddEvent("t", &seqMsg{action: "deleted", item: map[string]interface{}{
		"uid": "old", "name": "pod-a", "namespace": "ns", "creationTimestamp": "2026-01-01T00:00:00Z",
	}})
	eb.AddEvent("t", &seqMsg{action: "added", item: map[string]interface{}{
		"uid": "new", "name": "pod-a", "namespace": "ns", "creationTimestamp": "2026-07-01T00:00:00Z",
	}})
	if err := eb.FlushTopic("t"); err != nil {
		t.Fatal(err)
	}
	got := rec.flatActions(t)
	want := []string{"deleted:old", "added:new"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("flush reordered events: got %v want %v", got, want)
	}
}

func TestConcurrentOverflowFlushesDeliverInOrder(t *testing.T) {
	rec := &recordingBroadcaster{delayFirst: 50 * time.Millisecond}
	eb := NewEventBatcher(rec, time.Hour, 100)
	total := 300
	for i := 0; i < total; i++ {
		eb.AddEvent("t", &seqMsg{action: "modified", item: map[string]interface{}{
			"uid": fmt.Sprintf("%06d", i), "name": fmt.Sprintf("pod-%06d", i),
		}})
	}
	if err := eb.FlushTopic("t"); err != nil {
		t.Fatal(err)
	}
	eb.Shutdown()
	got := rec.flatActions(t)
	if len(got) != total {
		t.Fatalf("expected %d events delivered, got %d", total, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("events delivered out of order at %d: %s then %s", i, got[i-1], got[i])
		}
	}
}

func TestCleanupTopicDoesNotDropConcurrentEvents(t *testing.T) {
	rec := &recordingBroadcaster{}
	eb := NewEventBatcher(rec, 20*time.Millisecond, 100)
	rec.onFirst = func() {
		eb.AddEvent("t", &seqMsg{action: "deleted", item: map[string]interface{}{"uid": "late", "name": "late"}})
	}
	eb.AddEvent("t", &seqMsg{action: "added", item: map[string]interface{}{"uid": "early", "name": "early"}})
	eb.CleanupTopic("t")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		all := rec.flatActions(t)
		if len(all) >= 2 {
			if all[0] != "added:early" || all[1] != "deleted:late" {
				t.Fatalf("unexpected delivery: %v", all)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event added during cleanup was dropped: delivered=%v", rec.flatActions(t))
}
