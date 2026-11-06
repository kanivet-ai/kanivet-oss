package watcher

import (
	"context"
	"time"
)

// topicPass serializes the passes that send a topic's rows as a snapshot: a
// list sync, a cached replay, a snapshot preload. Each flushes the topic's
// held events and closes with a sync_complete the client prunes by, so two
// interleaved passes undo each other: one pass's flush releases the events
// another holds back behind the pages it is still sending, a deletion goes out
// before the older page that brings the object back, and the client, which
// keeps only the newest epoch's bookkeeping, never prunes it again.
type topicPass struct {
	ch   chan struct{}
	refs int
	// replayQueued is set, under passMu, by a resync that found the topic
	// busy. The pass holding the topic starts that replay when it releases
	// it, so a resync is never lost to a long list pass, and any number of
	// requests queued behind one pass make a single replay.
	replayQueued bool
}

// passFor returns the topic's pass with a ref taken. Callers hold passMu.
func (s *Service) passFor(topic string) *topicPass {
	if s.passes == nil {
		s.passes = make(map[string]*topicPass)
	}
	p := s.passes[topic]
	if p == nil {
		p = &topicPass{ch: make(chan struct{}, 1)}
		s.passes[topic] = p
	}
	p.refs++
	return p
}

// releasePass drops a ref on p, reporting whether a replay was queued behind
// it. Only the holder clears the queue (release true): it is the pass the
// replay waited on.
func (s *Service) releasePass(topic string, p *topicPass, release bool) (queued bool) {
	s.passMu.Lock()
	defer s.passMu.Unlock()
	if release {
		queued, p.replayQueued = p.replayQueued, false
	}
	p.refs--
	if p.refs == 0 && s.passes[topic] == p {
		delete(s.passes, topic)
	}
	return queued
}

// holderUnlock is the unlock of a pass that got the topic.
func (s *Service) holderUnlock(topic string, p *topicPass) func() {
	return func() {
		<-p.ch
		if s.releasePass(topic, p, true) {
			go s.runQueuedReplay(topic)
		}
	}
}

// lockTopicPass waits for the topic's other snapshot passes to finish, up to
// timeout (0 waits as long as ctx lasts), and reports whether it got the
// topic. unlock must be called once the pass is over.
func (s *Service) lockTopicPass(ctx context.Context, topic string, timeout time.Duration) (unlock func(), ok bool) {
	s.passMu.Lock()
	p := s.passFor(topic)
	s.passMu.Unlock()

	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	select {
	case p.ch <- struct{}{}:
		return s.holderUnlock(topic, p), true
	case <-ctx.Done():
	case <-expired:
	}
	s.releasePass(topic, p, false)
	return func() {}, false
}

// tryLockTopicPassOrQueue takes the topic's pass if it is free. If another
// pass holds it, it queues one replay for that pass to start when it is done
// and reports false. The check and the queueing happen under passMu, which
// the holder takes after giving the topic up, so a queued replay is never
// missed.
func (s *Service) tryLockTopicPassOrQueue(topic string) (unlock func(), ok bool) {
	s.passMu.Lock()
	p := s.passFor(topic)
	select {
	case p.ch <- struct{}{}:
		s.passMu.Unlock()
		return s.holderUnlock(topic, p), true
	default:
	}
	p.replayQueued = true
	s.passMu.Unlock()
	s.releasePass(topic, p, false)
	return func() {}, false
}
