package websocket

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

// The hand-assembled envelope must decode to exactly what the encoder would
// have produced for the same struct, so the frontend contract is unchanged.
func TestBatchedMessageMarshalMatchesEncoder(t *testing.T) {
	ev1, _ := jsonv2.Marshal(map[string]any{"type": "event", "action": "modified", "item": map[string]any{"name": "api-\"quoted\"", "namespace": "prod", "restarts": 3}})
	ev2, _ := jsonv2.Marshal(map[string]any{"type": "sync_complete", "itemCount": 2, "epoch": 7})
	bm := &BatchedMessage{
		BaseMessage: core.BaseMessage{MessageType: "batch", ID: "abc", Timestamp: time.Date(2026, 9, 18, 16, 4, 5, 123456789, time.UTC)},
		Topic:       "items:arn:aws:eks:eu-west-1:123456789012:cluster/plat é::v1:pods:",
		Events:      []json.RawMessage{ev1, ev2, nil},
		Count:       3,
	}

	got, err := bm.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	type envelope struct {
		core.BaseMessage
		Topic  string            `json:"topic"`
		Events []json.RawMessage `json:"events"`
		Count  int               `json:"count"`
	}
	want, err := jsonv2.Marshal(&envelope{BaseMessage: bm.BaseMessage, Topic: bm.Topic, Events: bm.Events, Count: bm.Count})
	if err != nil {
		t.Fatalf("reference marshal: %v", err)
	}

	var gotMap, wantMap map[string]any
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Fatalf("assembled envelope is not valid JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatalf("reference envelope is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(gotMap, wantMap) {
		t.Fatalf("envelope mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestBatchedMessageMarshalOmitsEmptyID(t *testing.T) {
	bm := &BatchedMessage{BaseMessage: core.BaseMessage{MessageType: "batch"}, Topic: "t", Count: 0}
	got, err := bm.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if _, has := m["id"]; has {
		t.Fatalf("empty id must be omitted, got %s", got)
	}
	if evs, ok := m["events"].([]any); !ok || len(evs) != 0 {
		t.Fatalf("events must be an empty array, got %s", got)
	}
}
