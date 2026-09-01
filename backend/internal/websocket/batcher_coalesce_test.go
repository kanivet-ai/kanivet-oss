package websocket

import (
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

type fakeMsg struct {
	core.BaseMessage
	action string
	item   map[string]interface{}
}

func (m *fakeMsg) Marshal() ([]byte, error) {
	return []byte(`{"action":"` + m.action + `"}`), nil
}

func (m *fakeMsg) GetData() map[string]interface{} {
	return map[string]interface{}{"action": m.action, "item": m.item}
}

func mod(uid, rv string) *fakeMsg {
	return &fakeMsg{action: "modified", item: map[string]interface{}{"uid": uid, "name": uid, "resourceVersion": rv}}
}

func batchLen(eb *EventBatcher, topic string) int {
	b := eb.batches[topic]
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

func TestAddEventCoalescesByUID(t *testing.T) {
	eb := NewEventBatcher(nil, time.Hour, 100)
	for i := 0; i < 50; i++ {
		if err := eb.AddEvent("t", mod("pod-a", "100")); err != nil {
			t.Fatal(err)
		}
	}
	if got := batchLen(eb, "t"); got != 1 {
		t.Fatalf("50 modifies of one object should coalesce to 1 event, got %d", got)
	}
}

func TestAddEventKeepsDistinctObjects(t *testing.T) {
	eb := NewEventBatcher(nil, time.Hour, 100)
	eb.AddEvent("t", mod("pod-a", "1"))
	eb.AddEvent("t", mod("pod-b", "1"))
	eb.AddEvent("t", mod("pod-a", "2"))
	if got := batchLen(eb, "t"); got != 2 {
		t.Fatalf("two distinct objects should yield 2 events, got %d", got)
	}
}

func TestAddEventLatestWins(t *testing.T) {
	eb := NewEventBatcher(nil, time.Hour, 100)
	eb.AddEvent("t", mod("pod-a", "1"))
	eb.AddEvent("t", &fakeMsg{action: "deleted", item: map[string]interface{}{"uid": "pod-a", "name": "pod-a"}})
	b := eb.batches["t"]
	if len(b.events) != 1 || b.events[0].action != "deleted" {
		t.Fatalf("delete should supersede prior modify in-place, got len=%d action=%q", len(b.events), b.events[0].action)
	}
}
