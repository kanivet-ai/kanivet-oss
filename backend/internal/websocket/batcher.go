package websocket

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

type sortPreference struct {
	sortBy    string
	sortOrder string
}

type topicBroadcaster interface {
	Broadcast(topic string, msg core.Message) error
}

type EventBatcher struct {
	hub           topicBroadcaster
	batchInterval time.Duration
	maxBatchSize  int
	batches       map[string]*topicBatch
	sortPrefs     map[string]*sortPreference
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

type batchedEvent struct {
	data   json.RawMessage
	item   map[string]interface{}
	action string
}

type topicBatch struct {
	topic   string
	events  []batchedEvent
	index   map[string]int
	mu      sync.Mutex
	flushMu sync.Mutex
	timer   *time.Timer
	batcher *EventBatcher
}

func coalesceKey(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	if uid, _ := item["uid"].(string); uid != "" {
		return "u:" + uid
	}
	ns, _ := item["namespace"].(string)
	name, _ := item["name"].(string)
	if name == "" {
		return ""
	}
	if ns == "" {
		ns = "default"
	}
	return "n:" + ns + "/" + name
}

type BatchedMessage struct {
	core.BaseMessage
	Topic  string            `json:"topic"`
	Events []json.RawMessage `json:"events"`
	Count  int               `json:"count"`
}

// Marshal writes the batch envelope by hand. Every event is JSON this process
// produced moments earlier, so pushing them through the encoder again as
// []json.RawMessage only re-validates known-good bytes: on a 100-event batch
// that made the envelope 13x more expensive than splicing. The shape matches
// what encoding/json/v2 produced for the struct, minus the never-set metadata.
func (bm *BatchedMessage) Marshal() ([]byte, error) {
	size := 96 + len(bm.MessageType) + len(bm.ID) + len(bm.Topic)
	for _, e := range bm.Events {
		size += len(e) + 1
	}
	buf := make([]byte, 0, size)
	buf = append(buf, `{"type":`...)
	typ, err := jsonv2.Marshal(string(bm.MessageType))
	if err != nil {
		return nil, err
	}
	buf = append(buf, typ...)
	if bm.ID != "" {
		id, err := jsonv2.Marshal(bm.ID)
		if err != nil {
			return nil, err
		}
		buf = append(buf, `,"id":`...)
		buf = append(buf, id...)
	}
	ts, err := bm.Timestamp.MarshalJSON()
	if err != nil {
		return nil, err
	}
	buf = append(buf, `,"timestamp":`...)
	buf = append(buf, ts...)
	topic, err := jsonv2.Marshal(bm.Topic)
	if err != nil {
		return nil, err
	}
	buf = append(buf, `,"topic":`...)
	buf = append(buf, topic...)
	buf = append(buf, `,"events":[`...)
	for i, e := range bm.Events {
		if i > 0 {
			buf = append(buf, ',')
		}
		if len(e) == 0 {
			buf = append(buf, "null"...)
			continue
		}
		buf = append(buf, e...)
	}
	buf = append(buf, `],"count":`...)
	buf = strconv.AppendInt(buf, int64(bm.Count), 10)
	buf = append(buf, '}')
	return buf, nil
}

func NewEventBatcher(hub topicBroadcaster, batchInterval time.Duration, maxBatchSize int) *EventBatcher {
	ctx, cancel := context.WithCancel(context.Background())
	if maxBatchSize < 100 {
		maxBatchSize = 100
	}
	return &EventBatcher{
		hub:           hub,
		batchInterval: batchInterval,
		maxBatchSize:  maxBatchSize,
		batches:       make(map[string]*topicBatch),
		sortPrefs:     make(map[string]*sortPreference),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (eb *EventBatcher) SetSortPreference(topic, sortBy, sortOrder string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if sortBy == "" {
		sortBy = "age"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}
	eb.sortPrefs[topic] = &sortPreference{sortBy: sortBy, sortOrder: sortOrder}
}

func (eb *EventBatcher) GetSortPreference(topic string) (sortBy, sortOrder string) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	if sp := eb.sortPrefs[topic]; sp != nil {
		return sp.sortBy, sp.sortOrder
	}
	return "age", "desc"
}

func (eb *EventBatcher) AddEvent(topic string, msg core.Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	var item map[string]interface{}
	var action string
	if g, ok := msg.(interface{ GetData() map[string]interface{} }); ok {
		d := g.GetData()
		item, _ = d["item"].(map[string]interface{})
		action, _ = d["action"].(string)
	}

	eb.mu.Lock()
	batch, exists := eb.batches[topic]
	if !exists {
		batch = &topicBatch{
			topic:   topic,
			events:  make([]batchedEvent, 0, eb.maxBatchSize),
			index:   make(map[string]int, eb.maxBatchSize),
			batcher: eb,
		}
		eb.batches[topic] = batch
	}
	batch.mu.Lock()
	eb.mu.Unlock()
	defer batch.mu.Unlock()

	ev := batchedEvent{data: data, item: item, action: action}
	if key := coalesceKey(item); key != "" {
		if i, ok := batch.index[key]; ok {
			batch.events[i] = ev
		} else {
			batch.index[key] = len(batch.events)
			batch.events = append(batch.events, ev)
		}
	} else {
		batch.events = append(batch.events, ev)
	}

	if batch.timer == nil {
		batch.timer = time.AfterFunc(eb.batchInterval, func() {
			if eb.ctx.Err() != nil {
				return
			}
			eb.flushBatch(topic)
		})
	}

	if len(batch.events) >= eb.maxBatchSize {
		if batch.timer != nil {
			batch.timer.Stop()
			batch.timer = nil
		}
		eb.wg.Add(1)
		go func(t string) {
			defer eb.wg.Done()
			if err := eb.flushBatch(t); err != nil {
				log.Printf("Failed to flush batch for topic %s: %v", t, err)
			}
		}(topic)
	}

	return nil
}

func (eb *EventBatcher) flushBatch(topic string) error {
	eb.mu.RLock()
	batch, exists := eb.batches[topic]
	eb.mu.RUnlock()
	if !exists {
		return nil
	}
	return eb.flushExtracted(batch)
}

func (eb *EventBatcher) flushExtracted(batch *topicBatch) error {
	batch.flushMu.Lock()
	defer batch.flushMu.Unlock()

	batch.mu.Lock()
	if batch.timer != nil {
		batch.timer.Stop()
		batch.timer = nil
	}
	if len(batch.events) == 0 {
		batch.mu.Unlock()
		return nil
	}
	events := batch.events
	batch.events = make([]batchedEvent, 0, eb.maxBatchSize)
	batch.index = make(map[string]int, eb.maxBatchSize)
	batch.mu.Unlock()

	raws := make([]json.RawMessage, len(events))
	for i, e := range events {
		raws[i] = e.data
	}
	batchedMsg := &BatchedMessage{
		BaseMessage: core.BaseMessage{
			MessageType: "batch",
			Timestamp:   time.Now(),
		},
		Topic:  batch.topic,
		Events: raws,
		Count:  len(raws),
	}

	return eb.hub.Broadcast(batch.topic, batchedMsg)
}

func (eb *EventBatcher) FlushTopic(topic string) error {
	return eb.flushBatch(topic)
}

func (eb *EventBatcher) CleanupTopic(topic string) {
	eb.mu.Lock()
	batch, exists := eb.batches[topic]
	delete(eb.batches, topic)
	delete(eb.sortPrefs, topic)
	eb.mu.Unlock()
	if exists {
		if err := eb.flushExtracted(batch); err != nil {
			log.Printf("CleanupTopic: failed to flush topic %s before cleanup: %v", topic, err)
		}
	}
}

func (eb *EventBatcher) Shutdown() {
	eb.cancel()
	eb.mu.RLock()
	batches := make([]*topicBatch, 0, len(eb.batches))
	for _, b := range eb.batches {
		batches = append(batches, b)
	}
	eb.mu.RUnlock()
	for _, b := range batches {
		if err := eb.flushExtracted(b); err != nil {
			log.Printf("Shutdown: failed to flush batch for topic %s: %v", b.topic, err)
		}
	}
	eb.wg.Wait()
}
