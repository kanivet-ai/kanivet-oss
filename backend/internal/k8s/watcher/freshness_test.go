package watcher

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

func (c *captureBroadcaster) topicStatuses() []*TopicStatusMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*TopicStatusMessage
	for _, m := range c.direct {
		if ts, ok := m.(*TopicStatusMessage); ok {
			out = append(out, ts)
		}
	}
	return out
}

// directTypes lists the direct messages in order, as "sync", "sync!" (a failed
// one), "bulk", "status:<state>".
func (c *captureBroadcaster) directTypes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.direct {
		switch m := m.(type) {
		case *InitialSyncMessage:
			if m.Failed {
				out = append(out, "sync!")
			} else {
				out = append(out, "sync")
			}
		case *BulkListMessage:
			out = append(out, "bulk")
		case *TopicStatusMessage:
			out = append(out, "status:"+m.State)
		}
	}
	return out
}

// A watch that keeps failing used to have its last list replayed to every
// revisit as a fresh, authoritative snapshot. The rows still go out, so the
// view is not emptied, but under a failed sync and the topic's status.
func TestReplayOfFailingTopicIsNotAuthoritative(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "name", sortOrder: "asc"}
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	s.cache.Set(testTopic, map[string]interface{}{"name": "a", "namespace": "ns"})
	s.noteTopicFailing(context.Background(), testTopic, errors.New("the server is currently unable to handle the request"), codeListFailed)

	s.Resync(testTopic)

	for _, bl := range hub.bulkLists() {
		if bl.Epoch != 0 {
			t.Fatalf("a failing topic's rows were replayed under epoch %d", bl.Epoch)
		}
	}
	syncs := hub.syncCompletes()
	if len(syncs) != 1 || !syncs[0].Failed || syncs[0].Epoch != 0 || syncs[0].ErrorCode != codeListFailed || syncs[0].Error == "" {
		t.Fatalf("want one failed, non-authoritative sync_complete, got %+v", syncs)
	}
	got := hub.directTypes()
	want := []string{"status:failing", "bulk", "sync!", "status:failing"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("messages %v, want %v (the replayed status right after the sync)", got, want)
	}
	if st := hub.topicStatuses()[1]; st.FailingSince == 0 || st.ErrorCode != codeListFailed || st.Topic != testTopic || st.Channel != "items" {
		t.Fatalf("replayed status %+v", st)
	}
}

// On a cold start the persisted snapshot is sent before the live list. Until
// that list completes the rows are unconfirmed, and a revisit must not be told
// they are a current snapshot.
func TestReplayOfUnconfirmedSnapshotIsNotAuthoritative(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "name", sortOrder: "asc"}
	s := newListSyncService(hub)
	registerWatch(t, s, testTopic)
	s.cache.Set(testTopic, map[string]interface{}{"name": "old", "namespace": "ns"})
	s.noteTopicPreloaded(testTopic)

	s.Resync(testTopic)
	if syncs := hub.syncCompletes(); len(syncs) != 1 || !syncs[0].Failed || syncs[0].Epoch != 0 || syncs[0].LastSyncAt != 0 {
		t.Fatalf("an unconfirmed snapshot was replayed as %+v", syncs)
	}

	// The live list confirms the rows: replays are authoritative again, and
	// say when the list was.
	l := &fakeListLister{items: []unstructured.Unstructured{mkListObj("a")}}
	if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", testGVR, "", testTopic, l); err != nil {
		t.Fatal(err)
	}
	s.Resync(testTopic)
	syncs := hub.syncCompletes()
	last := syncs[len(syncs)-1]
	if last.Failed || last.Epoch == 0 || last.LastSyncAt == 0 {
		t.Fatalf("after a live list the replay sent %+v", last)
	}
}

// statusFeed records the failure reports a feed gets.
type statusFeed struct {
	noFeed
	failing, recovered, synced atomic.Int64
}

func (f *statusFeed) Failing(error) { f.failing.Add(1) }
func (f *statusFeed) Recovered()    { f.recovered.Add(1) }
func (f *statusFeed) Synced()       { f.synced.Add(1) }

type statusSink struct{ feed *statusFeed }

func (s statusSink) Attach(string) ObjectFeed { return s.feed }

// A failing list says so at once, to the topic's subscribers (failed sync,
// topic_status) and its feed; the list that then succeeds sends "ok" after its
// sync_complete.
func TestFailingListReportsAndRecovers(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	feed := &statusFeed{}
	s.SetObjectSink("apps", "v1", "deployments", statusSink{feed})
	l := &healingLister{}
	l.items = []unstructured.Unstructured{mkListObj("a")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(ctx, "c", testGVR, "", testTopic, l)
	}()
	defer func() { cancel(); <-done }()

	waitFor(t, "the failure to be reported", func() bool { return len(hub.topicStatuses()) > 0 })
	syncs := hub.syncCompletes()
	if len(syncs) != 1 || !syncs[0].Failed || syncs[0].ErrorCode == "" || syncs[0].Error == "" {
		t.Fatalf("the failed list closed with %+v", syncs)
	}
	st := hub.topicStatuses()[0]
	if st.State != topicStateFailing || st.FailingSince == 0 || st.LastSyncAt != 0 || st.ErrorCode != syncs[0].ErrorCode {
		t.Fatalf("first status %+v", st)
	}
	if feed.failing.Load() == 0 {
		t.Fatal("the feed was not told its watch is failing")
	}

	l.healed.Store(true)
	s.RetryNow("c")
	waitFor(t, "the topic to recover", func() bool {
		sts := hub.topicStatuses()
		return sts[len(sts)-1].State == topicStateOK
	})
	got := hub.directTypes()
	if got[len(got)-2] != "sync" || got[len(got)-1] != "status:ok" {
		t.Fatalf("recovery sent %v, want an authoritative sync then status ok", got)
	}
	sts := hub.topicStatuses()
	if ok := sts[len(sts)-1]; ok.LastSyncAt == 0 || ok.Error != "" {
		t.Fatalf("ok status %+v", ok)
	}
	for _, st := range sts[:len(sts)-1] {
		if st.State != topicStateFailing {
			t.Fatalf("status %+v before the recovery", st)
		}
	}
}

// While a topic keeps failing its status is re-sent at most every
// topicStatusRefresh, to refresh the error shown, not on every attempt.
func TestFailingStatusIsRefreshedNotRepeated(t *testing.T) {
	old := topicStatusRefresh
	topicStatusRefresh = 30 * time.Millisecond
	defer func() { topicStatusRefresh = old }()
	hub := &captureBroadcaster{}
	s := newServiceForResync(hub)
	ctx := context.Background()

	s.noteTopicFailing(ctx, testTopic, errors.New("one"), codeWatchFailed)
	s.noteTopicFailing(ctx, testTopic, errors.New("two"), codeWatchFailed)
	if n := len(hub.topicStatuses()); n != 1 {
		t.Fatalf("%d statuses for two quick failures, want 1", n)
	}
	time.Sleep(40 * time.Millisecond)
	s.noteTopicFailing(ctx, testTopic, errors.New("three"), codeWatchFailed)
	sts := hub.topicStatuses()
	if len(sts) != 2 || sts[1].Error != "three" || sts[1].FailingSince != sts[0].FailingSince {
		t.Fatalf("statuses %+v, want the refresh to carry the latest error and the first failure's time", sts)
	}

	s.noteTopicRecovered(ctx, testTopic)
	s.noteTopicRecovered(ctx, testTopic)
	if sts := hub.topicStatuses(); len(sts) != 3 || sts[2].State != topicStateOK {
		t.Fatalf("recovery sent %+v, want one ok", sts)
	}
}

// failingWatchLister lists fine; its watches fail until healed, and once
// healed hold open until the context ends.
type failingWatchLister struct {
	fakeListLister
	healed  atomic.Bool
	watches atomic.Int64
}

func (l *failingWatchLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	l.watches.Add(1)
	if !l.healed.Load() {
		return nil, errors.New("Get \"https://c/apis\": net/http: TLS handshake timeout")
	}
	w := watch.NewFake()
	go func() { <-ctx.Done(); w.Stop() }()
	return w, nil
}

func (l *failingWatchLister) Namespace(string) resourceLister { return l }

// A watch that fails after its list leaves the rows frozen: the topic is
// reported failing, and "ok" once a watch holds again, resuming where the
// last one was, without a relist.
func TestFailingWatchReportsAndRecoversWithoutRelist(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	feed := &statusFeed{}
	s.SetObjectSink("apps", "v1", "deployments", statusSink{feed})
	l := &failingWatchLister{}
	l.items = []unstructured.Unstructured{mkListObj("a")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(ctx, "c", testGVR, "", testTopic, l)
	}()
	defer func() { cancel(); <-done }()

	waitFor(t, "the watch failure", func() bool { return len(hub.topicStatuses()) > 0 })
	if st := hub.topicStatuses()[0]; st.State != topicStateFailing || st.ErrorCode != codeWatchFailed || st.LastSyncAt == 0 {
		t.Fatalf("status %+v, want failing with the list's time", st)
	}
	l.healed.Store(true)
	s.RetryNow("c")
	waitFor(t, "the watch to hold again", func() bool {
		sts := hub.topicStatuses()
		return sts[len(sts)-1].State == topicStateOK
	})
	if feed.recovered.Load() == 0 {
		t.Fatal("the feed was not told the watch recovered")
	}
	if n := len(hub.syncCompletes()); n != 1 {
		t.Fatalf("%d syncs, want only the first list's", n)
	}
}

// After giving up on a panicking watch the topic used to freeze: its manager
// entry stayed, so every resubscribe replayed the frozen cache. The topic is
// now reported stopped, and the next subscribe starts the watch over.
func TestGivenUpWatchIsReportedAndRestartedOnSubscribe(t *testing.T) {
	old := panicRestartBackoff
	panicRestartBackoff = time.Millisecond
	defer func() { panicRestartBackoff = old }()

	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	l := &panicLister{}
	l.panics.Store(1000)
	l.items = []unstructured.Unstructured{mkListObj("a")}

	var starts atomic.Int64
	start := func() (bool, error) {
		return s.manager.StartWatch(testTopic, func(ctx context.Context) error {
			if starts.Add(1) == 1 {
				go s.runWatchLoop(ctx, "c", testGVR, "", testTopic, l)
			}
			return nil
		})
	}
	if already, err := start(); already || err != nil {
		t.Fatal(already, err)
	}
	waitFor(t, "the watch to give up", func() bool { return len(hub.topicStatuses()) > 0 })
	st := hub.topicStatuses()[0]
	if st.State != topicStateFailing || st.ErrorCode != codeWatchStopped {
		t.Fatalf("status %+v, want failing watch_stopped", st)
	}
	if syncs := hub.syncCompletes(); len(syncs) != 1 || !syncs[0].Failed || syncs[0].ErrorCode != codeWatchStopped {
		t.Fatalf("syncs %+v", syncs)
	}
	waitFor(t, "the watch to be marked stopped", func() bool { return !s.manager.isLive(testTopic) })

	already, err := start()
	if already || err != nil || starts.Load() != 2 {
		t.Fatalf("resubscribe: alreadyWatching=%v err=%v starts=%d, want the watch started over", already, err, starts.Load())
	}
	// The subscriber of the stopped watch kept its reference.
	s.manager.StopWatch(testTopic)
	if !s.manager.isLive(testTopic) {
		t.Fatal("one of two subscribers leaving ended the restarted watch")
	}
}

// A stopped watch's goroutine can still be finishing a page or an event after
// its topic's state was cleared; it must not write it back.
func TestStoppedWatchWritesNothing(t *testing.T) {
	hub := &captureBroadcaster{}
	s := newListSyncService(hub)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.broadcastPage(ctx, testTopic, []map[string]interface{}{{"name": "a", "namespace": "ns"}}, nil, "age", "desc", false, 1, 0)
	obj := mkListObj("b")
	s.handleResourceEventSimple(ctx, watch.Event{Type: watch.Added, Object: &obj}, "c", testGVR, testTopic)

	if n := s.cache.Count(testTopic); n != 0 {
		t.Fatalf("a stopped watch wrote %d rows to the cache", n)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.direct)+len(hub.batched) != 0 {
		t.Fatalf("a stopped watch sent %d messages", len(hub.direct)+len(hub.batched))
	}
}

// Two snapshot passes used to interleave: the second's forced flush released
// a deletion the first was holding behind a page that still listed the
// object, the page then added it back, and the client, which keeps only the
// newest epoch's bookkeeping, never pruned it.
func TestOverlappingResyncsDoNotResurrectDeletedObjects(t *testing.T) {
	hub := &quietTopicHub{}
	s := newListSyncService(hub)
	topic := "items:c::v1:pods:"
	registerWatch(t, s, topic)
	for i := range 150 {
		s.cache.Set(topic, map[string]interface{}{"name": fmt.Sprintf("a%03d", i), "namespace": "ns", "uid": fmt.Sprintf("u%d", i), "resourceVersion": "10"})
	}
	s.cache.Set(topic, map[string]interface{}{"name": "z", "namespace": "ns", "uid": "uz", "resourceVersion": "10"})
	second := make(chan struct{})
	hub.onBulk = func() {
		gone := map[string]interface{}{"name": "z", "namespace": "ns", "uid": "uz", "resourceVersion": "11"}
		s.emitResourceEvent(topic, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "deleted", gone, false)
		go func() {
			defer close(second)
			s.Resync(topic) // a second pass, while the first is still sending
		}()
		// Give it the time to run through, as it did before passes were
		// serialized; now it waits for the first to end.
		select {
		case <-second:
		case <-time.After(200 * time.Millisecond):
		}
	}

	s.Resync(topic)
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second pass never ran")
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	deleted := -1
	var lastSync *InitialSyncMessage
	for i, m := range hub.sent {
		switch m := m.(type) {
		case *ResourceEventMessage:
			if m.Action == "deleted" && m.Item["name"] == "z" {
				deleted = i
			}
		case *BulkListMessage:
			for _, item := range m.Items {
				if item["name"] == "z" && deleted >= 0 {
					t.Fatalf("message %d lists z after its deletion (message %d)", i, deleted)
				}
			}
		case *InitialSyncMessage:
			lastSync = m
		}
	}
	if deleted < 0 || lastSync == nil {
		t.Fatalf("deletion at %d, last sync %+v", deleted, lastSync)
	}
	// The pass that ends last is the one whose rows the client keeps: the
	// second, which no longer has z.
	for _, m := range hub.sent {
		if bl, ok := m.(*BulkListMessage); ok && bl.Epoch > lastSync.Epoch {
			t.Fatalf("a page of epoch %d went out after the last sync (epoch %d)", bl.Epoch, lastSync.Epoch)
		}
	}
}

// blockingLister serves the quick first page at once and holds the full list
// until released.
type blockingLister struct {
	fakeListLister
	release chan struct{}
}

func (l *blockingLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if opts.Limit == 0 {
		select {
		case <-l.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return l.fakeListLister.List(ctx, opts)
}

func (l *blockingLister) Namespace(string) resourceLister { return l }

// A replay that starts while a relist is sending must not interleave with it:
// it would read the cache between the relist's pages and send a sync of its
// own, under a newer epoch, in the middle of the relist's.
func TestReplayWaitsForTheRelistInFlight(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "name", sortOrder: "asc"}
	s := newListSyncService(hub)
	registerWatch(t, s, testTopic)
	l := &blockingLister{release: make(chan struct{})}
	for i := range 30 {
		l.items = append(l.items, mkListObj(fmt.Sprintf("d-%02d", i)))
	}
	listed := make(chan struct{})
	go func() {
		defer close(listed)
		if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", testGVR, "", testTopic, l); err != nil {
			t.Error(err)
		}
	}()
	waitFor(t, "the relist's first page", func() bool { return len(hub.bulkLists()) > 0 })

	replayed := make(chan struct{})
	go func() {
		defer close(replayed)
		s.Resync(testTopic)
	}()
	time.Sleep(50 * time.Millisecond)
	if n := len(hub.syncCompletes()); n != 0 {
		t.Fatalf("%d syncs went out in the middle of the relist", n)
	}
	close(l.release)
	<-listed
	<-replayed

	syncs := hub.syncCompletes()
	if len(syncs) != 2 || syncs[0].Epoch >= syncs[1].Epoch {
		t.Fatalf("syncs %+v, want the relist's then the replay's", syncs)
	}
	replay := syncs[1]
	if replay.ItemCount != 30 {
		t.Fatalf("the replay sent %d rows, want the relist's 30", replay.ItemCount)
	}
	var seenSync bool
	for _, m := range hub.direct {
		switch m := m.(type) {
		case *InitialSyncMessage:
			seenSync = true
		case *BulkListMessage:
			if m.Epoch == replay.Epoch && !seenSync {
				t.Fatal("a replay page went out before the relist's sync")
			}
		}
	}
}

// A topic whose resyncs keep losing messages is reported desynced; its rows
// are still current, so a replay stays authoritative but carries the status.
func TestDesyncedTopicIsReportedAndCleared(t *testing.T) {
	hub := &captureBroadcaster{}
	s := newServiceForResync(hub)
	registerWatch(t, s, testTopic)
	s.cache.Set(testTopic, map[string]interface{}{"name": "a", "namespace": "ns"})

	s.SetDesynced(testTopic, true)
	s.SetDesynced(testTopic, true)
	s.Resync(testTopic)
	s.SetDesynced(testTopic, false)

	if got, want := fmt.Sprint(hub.directTypes()), "[status:failing bulk sync status:failing status:ok]"; got != want {
		t.Fatalf("messages %s, want %s", got, want)
	}
	if st := hub.topicStatuses()[0]; st.ErrorCode != codeDesynced || st.FailingSince == 0 {
		t.Fatalf("desync status %+v", st)
	}
	if sc := hub.syncCompletes()[0]; sc.Failed || sc.Epoch == 0 {
		t.Fatalf("replay of a desynced topic sent %+v, want an authoritative sync", sc)
	}
}

var (
	_ FailureReporter  = (*statusFeed)(nil)
	_ RecoveryReporter = (*statusFeed)(nil)
)
