package transport

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

type TopicEmptyCallback func(topic string)

// SubscriptionRemovedCallback fires exactly once for every (connection, topic)
// pair that is actually removed, whether via Unsubscribe or UnsubscribeAll.
// Watch refcounts are driven solely by this so they mirror real subscriptions.
type SubscriptionRemovedCallback func(topic string, conn *core.Connection)

// BackpressureDropCallback is invoked when a broadcast to a connection is
// dropped because the connection's send buffer was full (ErrRateLimitExceeded).
// The handler should re-push the latest cached state for the topic to the
// connection so it recovers from the missed update instead of silently
// desyncing.
type BackpressureDropCallback func(topic string, conn *core.Connection)

type DefaultSubscriptionManager struct {
	mu                    sync.RWMutex
	topicToConns          map[string]map[core.ConnectionID]*core.Connection
	connToTopics          map[core.ConnectionID]map[string]struct{}
	onTopicEmpty          TopicEmptyCallback
	onBackpressureDrop    BackpressureDropCallback
	onSubscriptionRemoved SubscriptionRemovedCallback

	failLogMu sync.Mutex
	failLog   map[core.ConnectionID]*sendFailLog
}

func NewSubscriptionManager() *DefaultSubscriptionManager {
	return &DefaultSubscriptionManager{
		failLog:      make(map[core.ConnectionID]*sendFailLog),
		topicToConns: make(map[string]map[core.ConnectionID]*core.Connection),
		connToTopics: make(map[core.ConnectionID]map[string]struct{}),
	}
}

func (sm *DefaultSubscriptionManager) SetOnTopicEmpty(callback TopicEmptyCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onTopicEmpty = callback
}

func (sm *DefaultSubscriptionManager) SetOnBackpressureDrop(callback BackpressureDropCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onBackpressureDrop = callback
}

func (sm *DefaultSubscriptionManager) SetOnSubscriptionRemoved(callback SubscriptionRemovedCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onSubscriptionRemoved = callback
}

func (sm *DefaultSubscriptionManager) Subscribe(topic string, conn *core.Connection) (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.topicToConns[topic] == nil {
		sm.topicToConns[topic] = make(map[core.ConnectionID]*core.Connection)
	}
	if _, exists := sm.topicToConns[topic][conn.ID()]; exists {
		return false, nil
	}
	sm.topicToConns[topic][conn.ID()] = conn

	if sm.connToTopics[conn.ID()] == nil {
		sm.connToTopics[conn.ID()] = make(map[string]struct{})
	}
	sm.connToTopics[conn.ID()][topic] = struct{}{}

	return true, nil
}

func (sm *DefaultSubscriptionManager) Unsubscribe(topic string, conn *core.Connection) (bool, error) {
	var removed, topicEmpty bool
	var emptyCallback TopicEmptyCallback
	var removedCallback SubscriptionRemovedCallback

	sm.mu.Lock()
	if conns, ok := sm.topicToConns[topic]; ok {
		if _, had := conns[conn.ID()]; had {
			removed = true
			removedCallback = sm.onSubscriptionRemoved
			delete(conns, conn.ID())
			if len(conns) == 0 {
				delete(sm.topicToConns, topic)
				topicEmpty = true
				emptyCallback = sm.onTopicEmpty
			}
		}
	}
	if topics, ok := sm.connToTopics[conn.ID()]; ok {
		delete(topics, topic)
		if len(topics) == 0 {
			delete(sm.connToTopics, conn.ID())
		}
	}
	sm.mu.Unlock()

	if removed && removedCallback != nil {
		removedCallback(topic, conn)
	}
	if topicEmpty && emptyCallback != nil {
		emptyCallback(topic)
	}
	return removed, nil
}

func (sm *DefaultSubscriptionManager) UnsubscribeAll(conn *core.Connection) error {
	var removedTopics []string
	var emptyTopics []string
	var emptyCallback TopicEmptyCallback
	var removedCallback SubscriptionRemovedCallback

	sm.failLogMu.Lock()
	delete(sm.failLog, conn.ID())
	sm.failLogMu.Unlock()

	sm.mu.Lock()
	topics, ok := sm.connToTopics[conn.ID()]
	if !ok {
		sm.mu.Unlock()
		return nil
	}

	emptyCallback = sm.onTopicEmpty
	removedCallback = sm.onSubscriptionRemoved
	for topic := range topics {
		if conns, ok := sm.topicToConns[topic]; ok {
			if _, had := conns[conn.ID()]; had {
				removedTopics = append(removedTopics, topic)
				delete(conns, conn.ID())
				if len(conns) == 0 {
					delete(sm.topicToConns, topic)
					emptyTopics = append(emptyTopics, topic)
				}
			}
		}
	}
	delete(sm.connToTopics, conn.ID())
	sm.mu.Unlock()

	if removedCallback != nil {
		for _, topic := range removedTopics {
			removedCallback(topic, conn)
		}
	}
	if emptyCallback != nil {
		for _, topic := range emptyTopics {
			emptyCallback(topic)
		}
	}
	return nil
}

func (sm *DefaultSubscriptionManager) Broadcast(topic string, msg core.Message) error {
	sm.mu.RLock()
	conns, ok := sm.topicToConns[topic]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}

	connections := make([]*core.Connection, 0, len(conns))
	for _, conn := range conns {
		connections = append(connections, conn)
	}
	onDrop := sm.onBackpressureDrop
	sm.mu.RUnlock()

	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	var sendErrors []error
	var dropped []*core.Connection
	for _, conn := range connections {
		if err := conn.Send(data); err != nil {
			sm.logSendFailure(topic, conn.ID(), err, len(data))
			sendErrors = append(sendErrors, err)
			// Only unsubscribe on permanent errors (connection closed).
			// Transient errors like ErrRateLimitExceeded should not cause unsubscription
			// as the client connection is still valid and will recover.
			if errors.Is(err, core.ErrConnectionClosed) {
				_, _ = sm.Unsubscribe(topic, conn)
			} else if errors.Is(err, core.ErrRateLimitExceeded) {
				// The client missed this update because its buffer was full.
				// Flag it for a resync so it recovers the latest state instead
				// of silently desyncing.
				dropped = append(dropped, conn)
			}
		}
	}

	if onDrop != nil {
		for _, conn := range dropped {
			onDrop(topic, conn)
		}
	}

	if len(sendErrors) > 0 {
		return sendErrors[0]
	}

	return nil
}

// sendFailLogInterval spaces the send-failure log lines per connection: a
// client under backpressure fails every message of every topic it follows.
const sendFailLogInterval = 5 * time.Second

type sendFailLog struct {
	last       time.Time
	suppressed int
}

// logSendFailure logs a failed send at most once per connection per
// sendFailLogInterval, with a count of the failures it swallowed.
func (sm *DefaultSubscriptionManager) logSendFailure(topic string, id core.ConnectionID, err error, size int) {
	now := time.Now()
	sm.failLogMu.Lock()
	e := sm.failLog[id]
	if e == nil {
		if len(sm.failLog) >= 4096 {
			// Entries are removed on UnsubscribeAll; this only guards a
			// straggler send that raced a disconnect from growing the map.
			clear(sm.failLog)
		}
		e = &sendFailLog{}
		sm.failLog[id] = e
	}
	if !e.last.IsZero() && now.Sub(e.last) < sendFailLogInterval {
		e.suppressed++
		sm.failLogMu.Unlock()
		return
	}
	suppressed := e.suppressed
	e.suppressed = 0
	e.last = now
	sm.failLogMu.Unlock()
	if suppressed > 0 {
		log.Printf("[SubMgr] Send failed for topic %s: %v (data size: %d bytes), connection %s; %d similar failures suppressed", topic, err, size, id, suppressed)
		return
	}
	log.Printf("[SubMgr] Send failed for topic %s: %v (data size: %d bytes), connection %s", topic, err, size, id)
}

func (sm *DefaultSubscriptionManager) GetSubscribers(topic string) []*core.Connection {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	conns, ok := sm.topicToConns[topic]
	if !ok {
		return nil
	}

	result := make([]*core.Connection, 0, len(conns))
	for _, conn := range conns {
		result = append(result, conn)
	}

	return result
}

func (sm *DefaultSubscriptionManager) HasSubscribers(topic string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.topicToConns[topic]) > 0
}

func (sm *DefaultSubscriptionManager) GetTopics(conn *core.Connection) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	topics, ok := sm.connToTopics[conn.ID()]
	if !ok {
		return nil
	}

	result := make([]string, 0, len(topics))
	for topic := range topics {
		result = append(result, topic)
	}

	return result
}
