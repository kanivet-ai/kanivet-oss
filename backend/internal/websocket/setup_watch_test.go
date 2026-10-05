package websocket

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
)

// fakeWatch is a watchAPI that tracks refcounts the way the real manager
// does, and notes any StopWatch that had nothing to release.
type fakeWatch struct {
	mu         sync.Mutex
	refs       int
	underflow  int
	stops      int
	startErr   error
	onStart    func()
	resyncs    int
	onResync   func(call int)
	resyncCall []time.Time
}

func (f *fakeWatch) StartWatch(cluster, group, version, kind, namespace, sortBy, sortOrder string) error {
	if f.onStart != nil {
		f.onStart()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.refs++
	return nil
}

func (f *fakeWatch) StopWatch(cluster, group, version, kind, namespace string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.refs == 0 {
		f.underflow++
		return
	}
	f.refs--
}

func (f *fakeWatch) Resync(topic string) {
	f.mu.Lock()
	f.resyncs++
	n := f.resyncs
	f.resyncCall = append(f.resyncCall, time.Now())
	hook := f.onResync
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
}

func (f *fakeWatch) snapshot() (refs, underflow, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refs, f.underflow, f.stops
}

type watchHarness struct {
	srv    *Server
	fake   *fakeWatch
	ts     *httptest.Server
	topic  string
	client *websocket.Conn
}

func newWatchHarness(t *testing.T, fake *fakeWatch) *watchHarness {
	t.Helper()
	srv := NewServer()
	srv.installWatchHandlers(fake)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Shutdown(t.Context())
	})
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()
	h := &watchHarness{srv: srv, fake: fake, ts: ts, client: client, topic: topics.BuildItemsTopic("c", "", "v1", "pods", "ns")}
	deadline := time.Now().Add(2 * time.Second)
	for srv.Hub().CountConnections() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return h
}

func (h *watchHarness) send(t *testing.T, typ string) {
	t.Helper()
	msg := `{"type":"` + typ + `","payload":{"topic":"` + h.topic + `"}}`
	if err := h.client.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSubscribeUnsubscribeBalancesWatchRefs(t *testing.T) {
	fake := &fakeWatch{}
	h := newWatchHarness(t, fake)
	h.send(t, "subscribe")
	waitFor(t, "a watch ref", func() bool { r, _, _ := fake.snapshot(); return r == 1 })
	h.send(t, "unsubscribe")
	waitFor(t, "the ref to be released", func() bool { r, _, s := fake.snapshot(); return r == 0 && s == 1 })
	if _, u, _ := fake.snapshot(); u != 0 {
		t.Fatalf("%d StopWatch calls had no ref to release", u)
	}
}

// Broadcast auto-unsubscribes a closed connection. When that lands between
// Subscribe and StartWatch, StopWatch used to run before StartWatch and the
// ref StartWatch then took was never released.
func TestRemovalBetweenSubscribeAndStartWatchDoesNotLeakRef(t *testing.T) {
	fake := &fakeWatch{}
	var h *watchHarness
	fake.onStart = func() {
		h.srv.Hub().RangeConnections(func(c *core.Connection) bool {
			_ = c.Close()
			_ = h.srv.Hub().Broadcast(h.topic, core.NewOutgoingMessage("event", map[string]any{"x": 1}))
			return false
		})
	}
	h = newWatchHarness(t, fake)
	h.send(t, "subscribe")
	waitFor(t, "the subscribe to run", func() bool { return h.srv.Hub().CountConnections() == 0 })
	time.Sleep(100 * time.Millisecond)
	r, u, _ := fake.snapshot()
	if r != 0 || u != 0 {
		t.Fatalf("refs=%d underflow=%d, want both 0", r, u)
	}
}

// A failed StartWatch took no ref, so tearing the subscription down must not
// StopWatch one.
func TestFailedStartWatchDoesNotStopUncountedWatch(t *testing.T) {
	fake := &fakeWatch{startErr: errors.New("no such resource")}
	h := newWatchHarness(t, fake)
	h.send(t, "subscribe")
	waitFor(t, "the subscription to be torn down", func() bool {
		return !h.srv.Hub().HasSubscribers(h.topic)
	})
	time.Sleep(100 * time.Millisecond)
	r, u, s := fake.snapshot()
	if r != 0 || u != 0 || s != 0 {
		t.Fatalf("refs=%d underflow=%d stops=%d, want none", r, u, s)
	}
}

// Disconnecting releases the pair's ref exactly once.
func TestDisconnectReleasesWatchRef(t *testing.T) {
	fake := &fakeWatch{}
	h := newWatchHarness(t, fake)
	h.send(t, "subscribe")
	waitFor(t, "a watch ref", func() bool { r, _, _ := fake.snapshot(); return r == 1 })
	_ = h.client.Close()
	waitFor(t, "the ref to be released", func() bool { r, u, s := fake.snapshot(); return r == 0 && u == 0 && s == 1 })
}

func newTestCoordinator(f *fakeWatch) *resyncCoordinator {
	r := newResyncCoordinator(f)
	r.initialDelay = 10 * time.Millisecond
	r.cooldown = 60 * time.Millisecond
	return r
}

func resyncCount(f *fakeWatch) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resyncs
}

// Drops that arrive before the pending resync reads the cache are covered by it.
func TestResyncRequestsAreCoalesced(t *testing.T) {
	f := &fakeWatch{}
	r := newTestCoordinator(f)
	for i := 0; i < 20; i++ {
		r.request("items:t")
	}
	time.Sleep(150 * time.Millisecond)
	if n := resyncCount(f); n != 1 {
		t.Fatalf("%d resyncs for 20 requests, want 1", n)
	}
}

// A drop while a resync is sending (its own chunks or sync_complete being
// dropped) re-arms one more pass after the cooldown.
func TestDropDuringResyncRearms(t *testing.T) {
	f := &fakeWatch{}
	r := newTestCoordinator(f)
	f.onResync = func(call int) {
		if call == 1 {
			r.request("items:t") // a chunk of this pass was dropped
		}
	}
	r.request("items:t")
	waitFor(t, "a second resync", func() bool { return resyncCount(f) >= 2 })
	time.Sleep(200 * time.Millisecond)
	if n := resyncCount(f); n != 2 {
		t.Fatalf("%d resyncs, want exactly 2", n)
	}
	f.mu.Lock()
	gap := f.resyncCall[1].Sub(f.resyncCall[0])
	f.mu.Unlock()
	if gap < r.cooldown {
		t.Fatalf("second resync came %v after the first, before the %v cooldown", gap, r.cooldown)
	}
}

// A drop shortly after a resync finished used to be discarded; it now waits
// out the cooldown and then resyncs.
func TestDropWithinCooldownIsDeferredNotLost(t *testing.T) {
	f := &fakeWatch{}
	r := newTestCoordinator(f)
	r.request("items:t")
	waitFor(t, "the first resync", func() bool { return resyncCount(f) == 1 })
	time.Sleep(10 * time.Millisecond)
	r.request("items:t")
	waitFor(t, "the deferred resync", func() bool { return resyncCount(f) == 2 })
}

// A client that keeps losing the resync cannot make it loop forever.
func TestResyncRerunsAreBounded(t *testing.T) {
	f := &fakeWatch{}
	r := newTestCoordinator(f)
	r.cooldown = 20 * time.Millisecond
	f.onResync = func(int) { r.request("items:t") }
	r.request("items:t")
	time.Sleep(600 * time.Millisecond)
	if n := resyncCount(f); n != r.maxReruns+1 {
		t.Fatalf("%d resyncs, want %d", n, r.maxReruns+1)
	}
}

func TestResyncStateIsPrunedWhenTopicCloses(t *testing.T) {
	f := &fakeWatch{}
	r := newTestCoordinator(f)
	r.request("items:t")
	waitFor(t, "the resync", func() bool { return resyncCount(f) == 1 })
	time.Sleep(10 * time.Millisecond)
	r.topicClosed("items:t")
	r.mu.Lock()
	n := len(r.topics)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d topic states left after the topic closed", n)
	}
}
