package watcher

import (
	"context"
	"log"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

// TopicStatusMessage tells a topic's subscribers whether its rows are still
// being kept current. "failing" is sent on the first failed list or watch
// attempt and refreshed while the failures go on; "ok" once the watch has
// caught up again.
type TopicStatusMessage struct {
	core.BaseMessage `json:",inline"`
	Channel          string `json:"channel"`
	Topic            string `json:"topic"`
	State            string `json:"state"`
	Error            string `json:"error,omitempty"`
	ErrorCode        string `json:"errorCode,omitempty"`
	// FailingSince and LastSyncAt are unix milliseconds, 0 when unset.
	FailingSince int64 `json:"failingSince,omitempty"`
	LastSyncAt   int64 `json:"lastSyncAt,omitempty"`
}

func (m *TopicStatusMessage) Marshal() ([]byte, error) {
	return core.MarshalMessage(m)
}

func (m *TopicStatusMessage) GetData() map[string]interface{} {
	data := map[string]interface{}{
		"type":    m.MessageType,
		"channel": m.Channel,
		"topic":   m.Topic,
		"state":   m.State,
	}
	if m.Error != "" {
		data["error"] = m.Error
	}
	if m.ErrorCode != "" {
		data["errorCode"] = m.ErrorCode
	}
	if m.FailingSince > 0 {
		data["failingSince"] = m.FailingSince
	}
	if m.LastSyncAt > 0 {
		data["lastSyncAt"] = m.LastSyncAt
	}
	return data
}

const (
	topicStateOK      = "ok"
	topicStateFailing = "failing"

	// Error codes of failures ClassifyClusterError does not name.
	codeListFailed   = "list_failed"
	codeWatchFailed  = "watch_failed"
	codeDesynced     = "desynced"
	codeWatchStopped = "watch_stopped"
)

// topicStatusRefresh is how often a topic that keeps failing has its status
// sent again, so the error its subscribers show stays the current one.
var topicStatusRefresh = 10 * time.Second

// topicHealth is how current a topic's cached rows are. Guarded by healthMu.
type topicHealth struct {
	// lastSyncAt is when a list last completed.
	lastSyncAt time.Time
	// unconfirmed is set while the cache holds a persisted snapshot that no
	// live list has confirmed yet.
	unconfirmed bool
	// failingSince, err and code describe the list or watch failures since
	// the watch was last current.
	failingSince time.Time
	err, code    string
	// desyncedSince is set while subscribers keep missing messages and
	// resyncs cannot catch them up.
	desyncedSince time.Time
	// sentAt is when "failing" was last sent.
	sentAt time.Time
}

func (h *topicHealth) failing() bool { return !h.failingSince.IsZero() || !h.desyncedSince.IsZero() }

// status is the topic_status the health stands for.
func (h *topicHealth) status(topic string) *TopicStatusMessage {
	msg := &TopicStatusMessage{
		BaseMessage: core.BaseMessage{MessageType: "topic_status", Timestamp: time.Now()},
		Channel:     "items",
		Topic:       topic,
		State:       topicStateOK,
		LastSyncAt:  unixMilli(h.lastSyncAt),
	}
	if !h.failing() {
		return msg
	}
	msg.State = topicStateFailing
	if !h.failingSince.IsZero() {
		msg.Error, msg.ErrorCode, msg.FailingSince = h.err, h.code, unixMilli(h.failingSince)
	} else {
		msg.Error, msg.ErrorCode, msg.FailingSince = "updates for this list keep being lost", codeDesynced, unixMilli(h.desyncedSince)
	}
	return msg
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// healthLocked returns the topic's health, creating it. healthMu must be held.
func (s *Service) healthLocked(topic string) *topicHealth {
	if s.health == nil {
		s.health = make(map[string]*topicHealth)
	}
	h := s.health[topic]
	if h == nil {
		h = &topicHealth{}
		s.health[topic] = h
	}
	return h
}

func (s *Service) clearTopicHealth(topic string) {
	s.healthMu.Lock()
	delete(s.health, topic)
	s.healthMu.Unlock()
}

// topicHealthOf returns a copy of the topic's health.
func (s *Service) topicHealthOf(topic string) topicHealth {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if h := s.health[topic]; h != nil {
		return *h
	}
	return topicHealth{}
}

// noteTopicPreloaded records that the topic's cache was filled from a
// persisted snapshot.
func (s *Service) noteTopicPreloaded(topic string) {
	s.healthMu.Lock()
	s.healthLocked(topic).unconfirmed = true
	s.healthMu.Unlock()
}

// failureCode names a failed attempt: as ClassifyClusterError does where it
// recognizes the error, by what failed otherwise.
func failureCode(err error, fallback string) string {
	if ok, code, _ := isCredentialError(err); ok {
		return code
	}
	return fallback
}

// noteTopicFailing records a failed list or watch attempt of the watch under
// ctx, tells the topic's feed, and sends "failing" on the first failure and
// then at most every topicStatusRefresh.
func (s *Service) noteTopicFailing(ctx context.Context, topic string, err error, code string) {
	if ctx.Err() != nil {
		return
	}
	if fr, ok := s.feedFor(ctx, topic).(FailureReporter); ok {
		fr.Failing(err)
	}
	now := time.Now()
	s.healthMu.Lock()
	h := s.healthLocked(topic)
	first := h.failingSince.IsZero()
	if first {
		h.failingSince = now
	}
	if err != nil {
		h.err = err.Error()
	}
	h.code = code
	send := first || now.Sub(h.sentAt) >= topicStatusRefresh
	var msg *TopicStatusMessage
	if send {
		h.sentAt = now
		msg = h.status(topic)
	}
	s.healthMu.Unlock()
	if msg != nil {
		s.sendTopicStatus(msg)
	}
}

// noteTopicSynced records that a list of the watch under ctx completed,
// returning when, and whether "ok" is due once its sync_complete is out.
func (s *Service) noteTopicSynced(ctx context.Context, topic string) (at time.Time, recovered *TopicStatusMessage) {
	at = time.Now()
	if ctx.Err() != nil {
		return at, nil
	}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	h := s.healthLocked(topic)
	wasFailing := h.failing()
	h.lastSyncAt, h.unconfirmed = at, false
	h.failingSince, h.err, h.code = time.Time{}, "", ""
	if wasFailing && !h.failing() {
		recovered = h.status(topic)
	}
	return at, recovered
}

// noteTopicRecovered records that the watch under ctx is delivering again
// after failing: it resumed from where it was, so nothing was missed.
func (s *Service) noteTopicRecovered(ctx context.Context, topic string) {
	if ctx.Err() != nil {
		return
	}
	s.healthMu.Lock()
	h := s.healthLocked(topic)
	if h.failingSince.IsZero() {
		s.healthMu.Unlock()
		return
	}
	h.failingSince, h.err, h.code = time.Time{}, "", ""
	var msg *TopicStatusMessage
	if !h.failing() {
		msg = h.status(topic)
	}
	s.healthMu.Unlock()
	if rr, ok := s.feedFor(ctx, topic).(RecoveryReporter); ok {
		rr.Recovered()
	}
	if msg != nil {
		s.sendTopicStatus(msg)
	}
}

// SetDesynced records whether the topic's subscribers keep missing messages
// that resyncs cannot make up for, and tells them.
func (s *Service) SetDesynced(topic string, desynced bool) {
	s.healthMu.Lock()
	h := s.healthLocked(topic)
	if !h.desyncedSince.IsZero() == desynced {
		s.healthMu.Unlock()
		return
	}
	wasFailing := h.failing()
	h.desyncedSince = time.Time{}
	if desynced {
		h.desyncedSince = time.Now()
	}
	var msg *TopicStatusMessage
	switch {
	case desynced && h.failingSince.IsZero():
		h.sentAt = time.Now()
		msg = h.status(topic)
	case !desynced && wasFailing && !h.failing():
		msg = h.status(topic)
	}
	s.healthMu.Unlock()
	if msg != nil {
		s.sendTopicStatus(msg)
	}
}

func (s *Service) sendTopicStatus(msg *TopicStatusMessage) {
	if err := s.hub.BroadcastDirect(msg.Topic, msg); err != nil {
		log.Printf("Failed to broadcast topic status for %s: %v", msg.Topic, err)
	}
}
