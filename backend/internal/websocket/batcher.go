package websocket

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
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

// ErrBatcherClosed is returned by AddEvent once the batcher has been shut
// down: nothing would ever flush the event.
var ErrBatcherClosed = errors.New("event batcher is shut down")

// defaultMaxFrameBytes bounds a batch frame when the hub does not report its
// message size limit. It stays under the hub's 10MB default.
const defaultMaxFrameBytes = 8 << 20

// frameSizeLimiter is implemented by hubs that know their per-message limit
// (core.Hub does), so a batch is never cut larger than a connection accepts.
type frameSizeLimiter interface {
	MaxMessageSize() int64
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
	// lifeMu orders wg.Add (flush goroutines) before Shutdown's wg.Wait;
	// closed is set under it by Shutdown.
	lifeMu sync.Mutex
	closed bool
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
	// lastFlush is when events last went out, guarded by mu.
	lastFlush time.Time
	// held counts the HoldTopic calls not yet released, guarded by mu. While
	// it is set events queue: only FlushTopic sends them.
	held int
	// flushing is set while a size-triggered flush goroutine is pending, so a
	// burst past maxBatchSize spawns one goroutine rather than one per event.
	// Guarded by mu.
	flushing bool
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

// lockedBatch returns the topic's batch, created if needed, with its mu held.
// The lookup takes only the read lock; the write lock is needed once per topic.
func (eb *EventBatcher) lockedBatch(topic string) *topicBatch {
	eb.mu.RLock()
	if batch, exists := eb.batches[topic]; exists {
		batch.mu.Lock()
		eb.mu.RUnlock()
		return batch
	}
	eb.mu.RUnlock()

	eb.mu.Lock()
	batch, exists := eb.batches[topic]
	if !exists {
		batch = &topicBatch{
			topic:     topic,
			events:    make([]batchedEvent, 0, eb.maxBatchSize),
			index:     make(map[string]int, eb.maxBatchSize),
			batcher:   eb,
			lastFlush: time.Now(),
		}
		eb.batches[topic] = batch
	}
	batch.mu.Lock()
	eb.mu.Unlock()
	return batch
}

// scheduleFlush arms the batch's flush timer. Flush at most once per
// interval, measured from the last flush rather than from this event: a
// change on a topic that has been quiet for a whole interval goes out at once
// instead of waiting the full window, while the rest of its burst still
// coalesces (the timer runs on its own goroutine) and a busy topic keeps
// flushing once per interval. batch.mu must be held.
func (eb *EventBatcher) scheduleFlush(batch *topicBatch) {
	if batch.timer != nil || batch.held > 0 {
		return
	}
	topic := batch.topic
	delay := max(eb.batchInterval-time.Since(batch.lastFlush), 0)
	batch.timer = time.AfterFunc(delay, func() {
		if eb.ctx.Err() != nil {
			return
		}
		eb.flushBatch(topic, false)
	})
}

// HoldTopic queues the topic's events until the returned release is called;
// FlushTopic still sends them. A resync holds its topic from before it reads
// the snapshot it sends until its sync_complete is out. Without the hold a
// change made after the snapshot was read goes out at once on a quiet topic,
// ahead of the snapshot's pages, and an older page then undoes it on the
// client: a deleted object comes back, a new one is pruned by the sync.
func (eb *EventBatcher) HoldTopic(topic string) (release func()) {
	batch := eb.lockedBatch(topic)
	batch.held++
	if batch.timer != nil {
		batch.timer.Stop()
		batch.timer = nil
	}
	batch.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			batch.mu.Lock()
			defer batch.mu.Unlock()
			batch.held--
			if len(batch.events) > 0 {
				eb.scheduleFlush(batch)
			}
		})
	}
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

	batch := eb.lockedBatch(topic)
	defer batch.mu.Unlock()

	// Checked under batch.mu: Shutdown cancels before it flushes each batch
	// under this same lock, so an event accepted here is always flushed.
	if eb.ctx.Err() != nil {
		return ErrBatcherClosed
	}

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

	eb.scheduleFlush(batch)

	if len(batch.events) >= eb.maxBatchSize && batch.held == 0 && !batch.flushing && eb.startFlushGoroutine() {
		batch.flushing = true
		if batch.timer != nil {
			batch.timer.Stop()
			batch.timer = nil
		}
		go func(t string) {
			defer eb.wg.Done()
			if err := eb.flushBatch(t, false); err != nil {
				log.Printf("Failed to flush batch for topic %s: %v", t, err)
			}
			batch.mu.Lock()
			batch.flushing = false
			batch.mu.Unlock()
		}(topic)
	}

	return nil
}

// startFlushGoroutine registers a flush goroutine with the wait group,
// refusing once Shutdown has begun so Add never races Wait.
func (eb *EventBatcher) startFlushGoroutine() bool {
	eb.lifeMu.Lock()
	defer eb.lifeMu.Unlock()
	if eb.closed {
		return false
	}
	eb.wg.Add(1)
	return true
}

// frameBudget is the most event bytes one batch frame may carry.
func (eb *EventBatcher) frameBudget() int {
	limit := int64(defaultMaxFrameBytes)
	if l, ok := eb.hub.(frameSizeLimiter); ok && l.MaxMessageSize() > 0 {
		limit = l.MaxMessageSize()
	}
	// Headroom for the envelope (type, id, timestamp, topic, count) and a
	// margin so the whole frame stays below the connection's limit.
	budget := limit - limit/16 - 2048
	if budget < 1024 {
		budget = 1024
	}
	return int(budget)
}

// flushBatch sends the topic's queued events. Unless force is set it leaves a
// held topic's events queued.
func (eb *EventBatcher) flushBatch(topic string, force bool) error {
	eb.mu.RLock()
	batch, exists := eb.batches[topic]
	eb.mu.RUnlock()
	if !exists {
		return nil
	}
	return eb.flushExtracted(batch, force)
}

func (eb *EventBatcher) flushExtracted(batch *topicBatch, force bool) error {
	batch.flushMu.Lock()
	defer batch.flushMu.Unlock()

	batch.mu.Lock()
	if batch.held > 0 && !force {
		batch.mu.Unlock()
		return nil
	}
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
	batch.lastFlush = time.Now()
	batch.flushing = false
	batch.mu.Unlock()

	return eb.sendEvents(batch.topic, events)
}

// sendEvents broadcasts events as one or more batch frames, each holding at
// most maxBatchSize events and fitting the connection message limit. A
// connection rejects an oversized frame as a whole, so without the split every
// subscriber would lose the entire batch. A single event too large for any
// frame is logged and skipped. All frames are attempted; the first error is
// returned.
func (eb *EventBatcher) sendEvents(topic string, events []batchedEvent) error {
	budget := eb.frameBudget()
	raws := make([]json.RawMessage, 0, len(events))
	for _, e := range events {
		if len(e.data)+1 > budget {
			log.Printf("[Batcher] dropping %d-byte event for topic %s: larger than the %d-byte frame limit", len(e.data), topic, budget)
			continue
		}
		raws = append(raws, e.data)
	}

	var firstErr error
	for start := 0; start < len(raws); {
		end, size := start, 0
		for end < len(raws) && end-start < eb.maxBatchSize && size+len(raws[end])+1 <= budget {
			size += len(raws[end]) + 1
			end++
		}
		chunk := raws[start:end:end]
		batchedMsg := &BatchedMessage{
			BaseMessage: core.BaseMessage{
				MessageType: "batch",
				Timestamp:   time.Now(),
			},
			Topic:  topic,
			Events: chunk,
			Count:  len(chunk),
		}
		if err := eb.hub.Broadcast(topic, batchedMsg); err != nil && firstErr == nil {
			firstErr = err
		}
		start = end
	}
	return firstErr
}

func (eb *EventBatcher) FlushTopic(topic string) error {
	return eb.flushBatch(topic, true)
}

func (eb *EventBatcher) CleanupTopic(topic string) {
	eb.mu.Lock()
	batch, exists := eb.batches[topic]
	delete(eb.batches, topic)
	delete(eb.sortPrefs, topic)
	eb.mu.Unlock()
	if exists {
		if err := eb.flushExtracted(batch, true); err != nil {
			log.Printf("CleanupTopic: failed to flush topic %s before cleanup: %v", topic, err)
		}
	}
}

func (eb *EventBatcher) Shutdown() {
	eb.lifeMu.Lock()
	eb.closed = true
	eb.lifeMu.Unlock()
	eb.cancel()
	eb.mu.RLock()
	batches := make([]*topicBatch, 0, len(eb.batches))
	for _, b := range eb.batches {
		batches = append(batches, b)
	}
	eb.mu.RUnlock()
	for _, b := range batches {
		if err := eb.flushExtracted(b, true); err != nil {
			log.Printf("Shutdown: failed to flush batch for topic %s: %v", b.topic, err)
		}
	}
	eb.wg.Wait()
}
