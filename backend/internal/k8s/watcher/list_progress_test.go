package watcher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// streamLister hands its full list over item by item, the way a lister that
// decodes the response as it arrives does. It can stop before one item until
// it is released, or fail there.
type streamLister struct {
	items []unstructured.Unstructured
	// hideCount makes the first page keep the size of the list to itself.
	hideCount bool
	holdAt    int
	release   chan struct{}
	failAt    int
	watch     watch.Interface
}

func newStreamLister(n int) *streamLister {
	l := &streamLister{holdAt: -1, failAt: -1}
	for i := range n {
		l.items = append(l.items, mkListObj(fmt.Sprintf("d-%03d", i)))
	}
	return l
}

func (l *streamLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if opts.Limit == 0 {
		return nil, errors.New("a lister that streams was asked for its whole list")
	}
	n := min(int(opts.Limit), len(l.items))
	meta := map[string]interface{}{"resourceVersion": "42"}
	if n < len(l.items) {
		meta["continue"] = "more"
		if !l.hideCount {
			meta["remainingItemCount"] = int64(len(l.items) - n)
		}
	}
	return &unstructured.UnstructuredList{Object: map[string]interface{}{"metadata": meta}, Items: l.items[:n]}, nil
}

func (l *streamLister) ListEach(ctx context.Context, opts metav1.ListOptions, each func(runtime.Object) error) (string, error) {
	for i := range l.items {
		if i == l.failAt {
			return "", errors.New("connection reset by peer")
		}
		if i == l.holdAt {
			select {
			case <-l.release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if err := each(&l.items[i]); err != nil {
			return "", err
		}
	}
	return "42", nil
}

func (l *streamLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	if l.watch == nil {
		return nil, fmt.Errorf("watch not supported in fake")
	}
	return l.watch, nil
}

func (l *streamLister) Namespace(string) resourceLister { return l }

var deploymentsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

const deploymentsTopic = "items:c:apps:v1:deployments:"

// fullRows counts the rows of the full projection a sync has sent so far.
func fullRows(hub *captureBroadcaster) map[string]bool {
	names := map[string]bool{}
	for _, bl := range hub.bulkLists() {
		if bl.IsMinimal {
			continue
		}
		for _, item := range bl.Items {
			names[item["name"].(string)] = true
		}
	}
	return names
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// The point of reading a list as a stream: its rows go out while the rest of
// it is still on the way, not once its last byte is in.
func TestListSyncSendsRowsWhileTheListIsStillArriving(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	lister := newStreamLister(250)
	lister.holdAt, lister.release = 200, make(chan struct{})

	done := make(chan error, 1)
	go func() {
		_, err := s.fetchAndBroadcastListSync(context.Background(), "c", deploymentsGVR, "", deploymentsTopic, lister)
		done <- err
	}()

	waitFor(t, "the pages decoded so far", func() bool { return len(fullRows(hub)) >= 200 })
	select {
	case err := <-done:
		t.Fatalf("the sync ended (%v) with the list still held back", err)
	default:
	}
	if got := len(fullRows(hub)); got != 200 {
		t.Fatalf("%d rows sent, want the 200 that had arrived", got)
	}
	if syncs := hub.syncCompletes(); len(syncs) != 0 {
		t.Fatalf("sync_complete sent before the list ended: %+v", syncs)
	}

	close(lister.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := len(fullRows(hub)); got != 250 {
		t.Fatalf("%d rows sent in all, want 250", got)
	}
	if syncs := hub.syncCompletes(); len(syncs) != 1 || syncs[0].ItemCount != 250 {
		t.Fatalf("expected one sync_complete for 250 items, got %+v", syncs)
	}
	for _, bl := range hub.bulkLists() {
		if len(bl.Items) > listPageSize {
			t.Fatalf("a page of %d rows, want at most %d", len(bl.Items), listPageSize)
		}
		if bl.Total != 250 {
			t.Fatalf("a page says the list holds %d items, want 250", bl.Total)
		}
	}
}

// The first page's rows are not sent a second time when the full list reaches
// them unchanged.
func TestListSyncDoesNotResendTheFirstPageFromTheStream(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", deploymentsGVR, "", deploymentsTopic, newStreamLister(60)); err != nil {
		t.Fatal(err)
	}
	sent := 0
	for _, bl := range hub.bulkLists() {
		if !bl.IsMinimal {
			sent += len(bl.Items)
		}
	}
	if sent != 60 {
		t.Fatalf("%d full rows sent for a list of 60", sent)
	}
}

func TestListSyncTotalIsLeftOutWhenTheServerDoesNotGiveIt(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	lister := newStreamLister(60)
	lister.hideCount = true
	if _, err := s.fetchAndBroadcastListSync(context.Background(), "c", deploymentsGVR, "", deploymentsTopic, lister); err != nil {
		t.Fatal(err)
	}
	for _, bl := range hub.bulkLists() {
		if bl.Total != 0 {
			t.Fatalf("a page claims %d items with no count from the server", bl.Total)
		}
		if _, has := bl.GetData()["total"]; has {
			t.Fatal("an unknown total is sent to the client")
		}
	}
}

// A list that fails part way keeps the rows it delivered (they are real) but
// is not a sync: no sync_complete, and the error goes back to the loop.
func TestListSyncFailingMidStreamIsNotASync(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	lister := newStreamLister(250)
	lister.failAt = 150

	_, err := s.fetchAndBroadcastListSync(context.Background(), "c", deploymentsGVR, "", deploymentsTopic, lister)
	if err == nil || err.Error() != "connection reset by peer" {
		t.Fatalf("err = %v, want the list's own error", err)
	}
	if got := len(fullRows(hub)); got != 100 {
		t.Fatalf("%d rows sent, want the one full page that arrived", got)
	}
	if syncs := hub.syncCompletes(); len(syncs) != 0 {
		t.Fatalf("a failed list sent sync_complete: %+v", syncs)
	}
}

// recordingSink is an ObjectSink whose feeds write what they are told to one
// log.
type recordingSink struct {
	mu  sync.Mutex
	log []string
}

func (r *recordingSink) add(format string, args ...any) {
	r.mu.Lock()
	r.log = append(r.log, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *recordingSink) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

func (r *recordingSink) Attach(cluster string) ObjectFeed {
	r.add("attach %s", cluster)
	return recordingFeed{r}
}

type recordingFeed struct{ r *recordingSink }

func nameOf(obj runtime.Object) string { return obj.(*unstructured.Unstructured).GetName() }

func (f recordingFeed) Reset()                    { f.r.add("reset") }
func (f recordingFeed) Put(obj runtime.Object)    { f.r.add("put %s", nameOf(obj)) }
func (f recordingFeed) Delete(obj runtime.Object) { f.r.add("delete %s", nameOf(obj)) }
func (f recordingFeed) Synced()                   { f.r.add("synced") }
func (f recordingFeed) Close()                    { f.r.add("close") }

// A sink gets everything the watch learns, in order: the listing, bracketed
// so it can tell what is gone, then the live changes, then the end.
func TestWatchFeedsItsObjectSink(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	sink := &recordingSink{}
	s.SetObjectSink("apps", "v1", "deployments", sink)
	events := watch.NewFake()
	lister := newStreamLister(2)
	lister.watch = events

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(ctx, "c", deploymentsGVR, "", deploymentsTopic, lister)
	}()

	added, gone := mkListObj("new"), mkListObj("d-000")
	events.Add(&added)
	events.Delete(&gone)
	waitFor(t, "the watch events", func() bool { return len(sink.calls()) >= 7 })
	cancel()
	<-done

	want := []string{"attach c", "reset", "put d-000", "put d-001", "synced", "put new", "delete d-000", "close"}
	if got := sink.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sink saw\n %v\nwant\n %v", got, want)
	}
}

// A listing that did not finish must not be closed with Synced: the sink
// would take what it had not been given yet for deleted.
func TestFailedListIsNotSyncedToItsSink(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	sink := &recordingSink{}
	s.SetObjectSink("apps", "v1", "deployments", sink)
	lister := newStreamLister(3)
	lister.failAt = 2

	ctx := context.Background()
	closeFeed := s.openFeed(ctx, deploymentsTopic)
	if _, err := s.fetchAndBroadcastListSync(ctx, "c", deploymentsGVR, "", deploymentsTopic, lister); err == nil {
		t.Fatal("expected the list's error")
	}
	closeFeed()

	want := []string{"attach c", "reset", "put d-000", "put d-001", "close"}
	if got := sink.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sink saw\n %v\nwant\n %v", got, want)
	}
}

// Only the cluster-wide watch holds every object of the kind.
func TestNamespacedWatchHasNoObjectFeed(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{})
	sink := &recordingSink{}
	s.SetObjectSink("apps", "v1", "deployments", sink)
	ctx := context.Background()
	s.openFeed(ctx, "items:c:apps:v1:deployments:ns")()
	s.openFeed(ctx, "items:c:apps:v1:statefulsets:")()
	if got := sink.calls(); len(got) != 0 {
		t.Fatalf("sink saw %v, want nothing", got)
	}
	if _, none := s.feedFor(ctx, "items:c:apps:v1:deployments:ns").(noFeed); !none {
		t.Fatal("a namespaced topic has a feed")
	}
}

// A watch that was stopped and is still unwinding must not write to the feed
// of the watch that replaced it, nor take it away when it ends.
func TestStoppedWatchCannotReachItsReplacementsFeed(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{})
	s.SetObjectSink("apps", "v1", "deployments", &recordingSink{})
	type key struct{}
	oldCtx := context.WithValue(context.Background(), key{}, "old")
	newCtx := context.WithValue(context.Background(), key{}, "new")

	closeOld := s.openFeed(oldCtx, deploymentsTopic)
	closeNew := s.openFeed(newCtx, deploymentsTopic)
	if _, none := s.feedFor(oldCtx, deploymentsTopic).(noFeed); !none {
		t.Fatal("the stopped watch still reaches a feed")
	}
	closeOld()
	if _, none := s.feedFor(newCtx, deploymentsTopic).(noFeed); none {
		t.Fatal("the stopped watch ending took the running watch's feed away")
	}
	closeNew()
	if _, none := s.feedFor(newCtx, deploymentsTopic).(noFeed); !none {
		t.Fatal("a feed outlived its watch")
	}
}

// Retain is for a reader of the sink: it must not make the watcher resend the
// whole list to the topic's subscribers the way a new subscription does.
func TestRetainSendsSubscribersNothing(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	s.indexedClusters = map[string]bool{}
	registerWatch(t, s, deploymentsTopic)
	s.cache.Set(deploymentsTopic, map[string]interface{}{"name": "a", "namespace": "ns"})

	if err := s.Retain("c", "apps", "v1", "deployments"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	hub.mu.Lock()
	sent := len(hub.direct) + len(hub.batched)
	hub.mu.Unlock()
	if sent != 0 {
		t.Fatalf("Retain sent %d messages to the topic", sent)
	}

	// The reference is real: the subscriber leaving does not stop the watch
	// while the reader holds it, and giving it back does.
	s.StopWatch("c", "apps", "v1", "deployments", "")
	s.manager.mu.Lock()
	refs := s.manager.watches[deploymentsTopic].refCount
	s.manager.mu.Unlock()
	if refs != 1 {
		t.Fatalf("refcount = %d after the subscriber left, want the reader's 1", refs)
	}
	s.Release("c", "apps", "v1", "deployments")
	s.manager.mu.Lock()
	refs = s.manager.watches[deploymentsTopic].refCount
	_, pending := s.manager.pendingCleanups[deploymentsTopic]
	s.manager.mu.Unlock()
	if refs != 0 || !pending {
		t.Fatalf("refcount = %d, cleanup pending = %v after Release, want 0 and true", refs, pending)
	}
}
