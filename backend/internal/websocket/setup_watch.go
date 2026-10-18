package websocket

import (
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
)

// watchAPI is the part of the watcher service the subscribe path drives.
// *watcher.Service implements it.
type watchAPI interface {
	StartWatch(cluster, group, version, kind, namespace, sortBy, sortOrder string) error
	StopWatch(cluster, group, version, kind, namespace string)
	Resync(topic string)
}

// watchRefs keeps the watcher's refcount balanced against the live
// (connection, topic) subscriptions. A pair owns one watch ref from the moment
// StartWatch succeeds until its subscription is removed, whichever way and
// whenever that happens: an explicit unsubscribe, a disconnect, or Broadcast
// auto-unsubscribing a closed connection, even if that lands between Subscribe
// and StartWatch.
type watchRefs struct {
	mu   sync.Mutex
	refs map[watchRefKey]*watchRef
}

type watchRefKey struct {
	topic string
	conn  core.ConnectionID
}

// watchRef is one pair's ownership record. removed means the subscription went
// away while StartWatch was still running, so whoever finishes it must give
// the ref back.
type watchRef struct {
	started bool
	removed bool
}

func newWatchRefs() *watchRefs {
	return &watchRefs{refs: make(map[watchRefKey]*watchRef)}
}

// reserve registers a pair's record before it subscribes, so a removal that
// races the subscribe finds it. It reports false when a record already exists.
func (w *watchRefs) reserve(key watchRefKey) (*watchRef, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.refs[key]; exists {
		return nil, false
	}
	ref := &watchRef{}
	w.refs[key] = ref
	return ref, true
}

// release drops a record that never got a watch ref (subscribe failed or the
// pair already existed).
func (w *watchRefs) release(key watchRefKey, ref *watchRef) {
	w.mu.Lock()
	if w.refs[key] == ref {
		delete(w.refs, key)
	}
	w.mu.Unlock()
}

// removedBeforeStart reports whether the subscription was already removed, so
// StartWatch can be skipped.
func (w *watchRefs) removedBeforeStart(ref *watchRef) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return ref.removed
}

// finishStart records the outcome of StartWatch. It returns true when the
// caller must StopWatch: StartWatch took a ref for a subscription that was
// removed while it ran, and the removal callback could not release it. When
// StartWatch failed no ref was taken, so the record is simply dropped.
func (w *watchRefs) finishStart(key watchRefKey, ref *watchRef, startErr error) (stopNow bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ref.removed {
		return startErr == nil
	}
	if startErr != nil {
		if w.refs[key] == ref {
			delete(w.refs, key)
		}
		return false
	}
	ref.started = true
	return false
}

// onRemoved is the subscription-removed callback. It returns true when the
// pair holds a started watch ref that must be stopped.
func (w *watchRefs) onRemoved(key watchRefKey) (stop bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ref := w.refs[key]
	if ref == nil {
		return false
	}
	delete(w.refs, key)
	if ref.started {
		return true
	}
	ref.removed = true
	return false
}

// desyncReporter is implemented by watch APIs that can tell a topic's
// subscribers they keep missing updates. *watcher.Service implements it.
type desyncReporter interface {
	SetDesynced(topic string, desynced bool)
}

// resyncCoordinator re-pushes a topic's cached snapshot after a client missed
// updates. Resync is topic-wide (it reaches every subscriber), so requests are
// debounced and rate-limited per topic. A request that arrives while a resync
// is already sending, which includes drops of the resync's own chunks or its
// sync_complete, schedules one more pass after the cooldown, so a quiet topic
// does not stay on a partial snapshot. Past maxReruns passes in a row the
// topic is reported desynced and retried on the slower slowRetries delays
// until a pass goes through.
type resyncCoordinator struct {
	api          watchAPI
	initialDelay time.Duration
	cooldown     time.Duration
	maxReruns    int
	// slowRetries are the delays between the passes past maxReruns; the last
	// one repeats.
	slowRetries []time.Duration

	mu     sync.Mutex
	topics map[string]*resyncState
}

type resyncState struct {
	scheduled bool // a goroutine exists and has not read the cache yet
	running   bool // Resync is executing
	rerun     bool // a drop happened while running
	last      time.Time
	gone      bool // the topic emptied; drop the state when idle
	desynced  bool // reported desynced and not yet cleared
}

func newResyncCoordinator(api watchAPI) *resyncCoordinator {
	return &resyncCoordinator{
		api:          api,
		initialDelay: 250 * time.Millisecond,
		cooldown:     2 * time.Second,
		maxReruns:    3,
		slowRetries:  []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second},
		topics:       make(map[string]*resyncState),
	}
}

func (r *resyncCoordinator) setDesynced(topic string, desynced bool) {
	if d, ok := r.api.(desyncReporter); ok {
		d.SetDesynced(topic, desynced)
	}
}

func (r *resyncCoordinator) request(topic string) {
	r.mu.Lock()
	st := r.topics[topic]
	if st == nil {
		st = &resyncState{}
		r.topics[topic] = st
	}
	st.gone = false
	if st.running {
		// The pass in flight may already have read the cache; run once more.
		st.rerun = true
		r.mu.Unlock()
		return
	}
	if st.scheduled {
		// Not yet read the cache: the pending pass covers this drop.
		r.mu.Unlock()
		return
	}
	st.scheduled = true
	// Within the cooldown of the previous pass the request waits it out
	// instead of being lost.
	delay := r.initialDelay
	if since := time.Since(st.last); !st.last.IsZero() && since < r.cooldown {
		delay = max(delay, r.cooldown-since)
	}
	r.mu.Unlock()
	go r.run(topic, st, delay)
}

func (r *resyncCoordinator) run(topic string, st *resyncState, delay time.Duration) {
	for rerun := 0; ; rerun++ {
		time.Sleep(delay)
		r.mu.Lock()
		st.scheduled = false
		st.running = true
		st.rerun = false
		r.mu.Unlock()

		r.api.Resync(topic)

		r.mu.Lock()
		st.running = false
		st.last = time.Now()
		again := st.rerun && !st.gone
		// A pass that lost nothing caught the subscribers up; one whose
		// topic emptied has nobody left to tell.
		cleared := !again && st.desynced
		if cleared {
			st.desynced = false
		}
		reported := false
		delay = r.cooldown
		if again && rerun >= r.maxReruns {
			// It used to give up here until the next drop, which on a
			// quiet topic never came: the client stayed on a partial
			// snapshot with nothing telling it so.
			if !st.desynced {
				log.Printf("[WS] resync of %s keeps losing messages; reporting it desynced and retrying more slowly", topic)
				st.desynced, reported = true, true
			}
			delay = r.slowRetries[min(rerun-r.maxReruns, len(r.slowRetries)-1)]
		}
		if again {
			st.scheduled = true
		} else if st.gone {
			delete(r.topics, topic)
		}
		r.mu.Unlock()
		if reported {
			r.setDesynced(topic, true)
		}
		if cleared {
			r.setDesynced(topic, false)
		}
		if !again {
			return
		}
	}
}

// topicClosed forgets a topic nobody is subscribed to any more.
func (r *resyncCoordinator) topicClosed(topic string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.topics[topic]
	if st == nil {
		return
	}
	if st.scheduled || st.running {
		st.gone = true
		return
	}
	delete(r.topics, topic)
}
