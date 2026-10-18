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
}

// lockTopicPass waits for the topic's other snapshot passes to finish, up to
// timeout (0 waits as long as ctx lasts), and reports whether it got the
// topic. unlock must be called once the pass is over.
func (s *Service) lockTopicPass(ctx context.Context, topic string, timeout time.Duration) (unlock func(), ok bool) {
	s.passMu.Lock()
	if s.passes == nil {
		s.passes = make(map[string]*topicPass)
	}
	p := s.passes[topic]
	if p == nil {
		p = &topicPass{ch: make(chan struct{}, 1)}
		s.passes[topic] = p
	}
	p.refs++
	s.passMu.Unlock()

	done := func() {
		s.passMu.Lock()
		p.refs--
		if p.refs == 0 && s.passes[topic] == p {
			delete(s.passes, topic)
		}
		s.passMu.Unlock()
	}
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	select {
	case p.ch <- struct{}{}:
		return func() {
			<-p.ch
			done()
		}, true
	case <-ctx.Done():
	case <-expired:
	}
	done()
	return func() {}, false
}
