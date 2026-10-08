package watcher

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

var testGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

const testTopic = "items:c:apps:v1:deployments:"

// A subscriber arriving after the topic's first list failed used to get
// nothing at all and stayed in its loading state.
func TestLateSubscriberOfFailedSyncGetsTerminalSignal(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	st := s.pendingSync(context.Background(), testTopic)
	s.finishSync(context.Background(), st, errors.New("list failed"))

	s.sendCachedData(testTopic, "age", "desc")

	syncs := hub.syncCompletes()
	if len(syncs) != 1 || syncs[0].ItemCount != 0 || syncs[0].Epoch != 0 {
		t.Fatalf("expected one sync_complete(0 items, epoch 0), got %+v", syncs)
	}
	if len(hub.bulkLists()) != 0 {
		t.Fatal("a failed sync must not replay the cache as a snapshot")
	}
}

func TestLateSubscriberStillWaitingForFirstListGetsTerminalSignalAfterWait(t *testing.T) {
	old := cachedDataSyncWait
	cachedDataSyncWait = 50 * time.Millisecond
	defer func() { cachedDataSyncWait = old }()

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	s.pendingSync(context.Background(), testTopic) // never finishes

	s.sendCachedData(testTopic, "age", "desc")

	if syncs := hub.syncCompletes(); len(syncs) != 1 || syncs[0].Epoch != 0 {
		t.Fatalf("expected a terminal sync_complete after the wait, got %+v", syncs)
	}
}

// recordingLister records the options and context deadlines of its calls.
type recordingLister struct {
	fakeListLister
	mu            sync.Mutex
	watchTimeouts []int64
	watchDeadline []bool
	listDeadline  []bool
}

func (l *recordingLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	_, ok := ctx.Deadline()
	l.mu.Lock()
	l.listDeadline = append(l.listDeadline, ok)
	l.mu.Unlock()
	return l.fakeListLister.List(ctx, opts)
}

func (l *recordingLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	_, ok := ctx.Deadline()
	l.mu.Lock()
	if opts.TimeoutSeconds != nil {
		l.watchTimeouts = append(l.watchTimeouts, *opts.TimeoutSeconds)
	} else {
		l.watchTimeouts = append(l.watchTimeouts, 0)
	}
	l.watchDeadline = append(l.watchDeadline, ok)
	l.mu.Unlock()
	ch := make(chan watch.Event)
	return &closedWatch{ch: ch}, nil // stays open until the test ends
}

func (l *recordingLister) Namespace(string) resourceLister { return l }

// A half-open connection delivers nothing and never closes. Without a server
// side timeout and a client side deadline the watch would hang forever.
func TestWatchesAndListsCarryTimeouts(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	l := &recordingLister{}
	l.items = []unstructured.Unstructured{mkListObj("a")}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	s.runWatchLoop(ctx, "c", testGVR, "", testTopic, l)

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.watchTimeouts) == 0 {
		t.Fatal("no watch was opened")
	}
	if got := l.watchTimeouts[0]; got < 300 || got > 600 {
		t.Fatalf("watch TimeoutSeconds = %d, want jittered 300-600", got)
	}
	if !l.watchDeadline[0] {
		t.Fatal("watch context has no client side deadline")
	}
	if len(l.listDeadline) == 0 {
		t.Fatal("no list was made")
	}
	for i, ok := range l.listDeadline {
		if !ok {
			t.Fatalf("list call %d has no deadline", i)
		}
	}
}

// panicLister panics in its first n List calls.
type panicLister struct {
	fakeListLister
	panics  atomic.Int64
	lists   atomic.Int64
	watches atomic.Int64
}

func (l *panicLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	l.lists.Add(1)
	// Only the first page runs on the loop's own goroutine.
	if opts.Limit > 0 && l.panics.Add(-1) >= 0 {
		panic("boom")
	}
	return l.fakeListLister.List(ctx, opts)
}

func (l *panicLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	l.watches.Add(1)
	return &closedWatch{ch: make(chan watch.Event)}, nil
}

func (l *panicLister) Namespace(string) resourceLister { return l }

// One topic's panic used to be re-raised and crash the backend, taking every
// cluster down with it.
func TestWatchLoopSurvivesPanicAndRestarts(t *testing.T) {
	old := panicRestartBackoff
	panicRestartBackoff = 5 * time.Millisecond
	defer func() { panicRestartBackoff = old }()

	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	l := &panicLister{}
	l.panics.Store(2)
	l.items = []unstructured.Unstructured{mkListObj("a")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(ctx, "c", testGVR, "", testTopic, l)
	}()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for l.watches.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l.watches.Load() == 0 {
		t.Fatalf("watch loop did not recover (%d lists)", l.lists.Load())
	}
	cancel()
	<-done
}

func TestWatchLoopGivesUpAfterRepeatedPanicsAndReleasesWaiters(t *testing.T) {
	old := panicRestartBackoff
	panicRestartBackoff = time.Millisecond
	defer func() { panicRestartBackoff = old }()

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	l := &panicLister{}
	l.panics.Store(1000)
	l.items = []unstructured.Unstructured{mkListObj("a")}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(context.Background(), "c", testGVR, "", testTopic, l)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch loop kept restarting forever")
	}
	if n := l.lists.Load(); n < 2 || n > 20 {
		t.Fatalf("expected a bounded number of restarts, got %d lists", n)
	}
}

// Every failing topic of a vcluster used to evict the caches and wait up to
// 30s on its own.
func TestVClusterRecoveryIsCoalescedPerCluster(t *testing.T) {
	var evicts, waits atomic.Int64
	release := make(chan struct{})
	s := newServiceForResync(&captureBroadcaster{})
	s.vcEvict = func(string) { evicts.Add(1) }
	s.vcWait = func(context.Context, string) bool {
		waits.Add(1)
		<-release
		return true
	}

	var wg sync.WaitGroup
	results := make([]bool, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.recoverVCluster(context.Background(), "vcluster-x")
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if evicts.Load() != 1 || waits.Load() != 1 {
		t.Fatalf("evicts=%d waits=%d, want 1 each", evicts.Load(), waits.Load())
	}
	for i, ok := range results {
		if !ok {
			t.Fatalf("caller %d did not see the shared result", i)
		}
	}
}

func TestVClusterRecoveryCallerStopsWaitingWhenItsContextEnds(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := newServiceForResync(&captureBroadcaster{})
	s.vcEvict = func(string) {}
	s.vcWait = func(context.Context, string) bool { <-release; return true }
	go s.recoverVCluster(context.Background(), "vcluster-y")
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if s.recoverVCluster(ctx, "vcluster-y") {
		t.Fatal("a cancelled waiter must not report healthy")
	}
	if time.Since(start) > time.Second {
		t.Fatal("waiter ignored its context")
	}
}
