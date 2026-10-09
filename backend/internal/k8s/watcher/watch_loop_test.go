package watcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type closedWatch struct{ ch chan watch.Event }

func (c *closedWatch) Stop()                          {}
func (c *closedWatch) ResultChan() <-chan watch.Event { return c.ch }

// closingLister lists fine but every watch it opens is already closed, like
// a proxy or a dying port-forward dropping each stream at once.
type closingLister struct {
	fakeListLister
	watches atomic.Int64
}

func (l *closingLister) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	l.watches.Add(1)
	ch := make(chan watch.Event)
	close(ch)
	return &closedWatch{ch: ch}, nil
}

func (l *closingLister) Namespace(string) resourceLister { return l }

// Watches are not client-side rate limited, so a watch that closes right
// away must count as a failure and back off instead of being reopened in a
// tight loop.
func TestWatchLoopBacksOffWhenWatchClosesImmediately(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	l := &closingLister{}
	l.items = []unstructured.Unstructured{mkListObj("a")}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	s.runWatchLoop(ctx, "c", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "", "items:c:apps:v1:deployments:", l)

	if n := l.watches.Load(); n > 2 {
		t.Fatalf("%d watches opened in 300ms against a server that closes them at once", n)
	}
}

func TestBackoffDelayIsJitteredWithinBounds(t *testing.T) {
	for attempt := 1; attempt <= 8; attempt++ {
		base := time.Duration(1<<min(attempt-1, 5)) * time.Second
		if base > 30*time.Second {
			base = 30 * time.Second
		}
		distinct := map[time.Duration]bool{}
		for range 200 {
			d := backoffDelay(attempt)
			if d < base/2 || d >= base {
				t.Fatalf("attempt %d: delay %v outside [%v, %v)", attempt, d, base/2, base)
			}
			distinct[d] = true
		}
		if len(distinct) < 2 {
			t.Fatalf("attempt %d: every retry waits the same %v, so topics retry in lockstep", attempt, base)
		}
	}
}

// useKubeconfig points a fresh k8s.Client at a kubeconfig with one context
// per server URL, isolated from the user's own.
func useKubeconfig(t *testing.T, servers map[string]string) *k8s.Client {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := "apiVersion: v1\nkind: Config\nclusters:\n"
	for name, url := range servers {
		cfg += "- name: " + name + "\n  cluster:\n    server: " + url + "\n"
	}
	cfg += "contexts:\n"
	for name := range servers {
		cfg += "- name: " + name + "\n  context:\n    cluster: " + name + "\n    user: u\n"
	}
	cfg += "users:\n- name: u\n  user:\n    token: t\n"
	path := filepath.Join(home, "kubeconfig")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	return k8s.NewClient()
}

// StartWatch runs on the WebSocket connection's only read loop and under the
// watch manager's lock, so it must not wait on discovery or client setup.
func TestStartWatchDoesNotWaitOnDiscovery(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	s := NewService(useKubeconfig(t, map[string]string{"slow": srv.URL}), &captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	defer s.Shutdown()
	defer close(release)

	done := make(chan error, 2)
	go func() { done <- s.StartWatch("slow", "apps", "v1", "deployments", "", "age", "desc") }()
	go func() { done <- s.StartWatch("slow", "", "v1", "pods", "", "age", "desc") }()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("StartWatch: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("StartWatch blocked on the cluster's discovery")
		}
	}
}

// Discovery and client setup now run before the first list, in the watch
// goroutine. A second subscriber arriving meanwhile must wait for that list
// rather than be handed the still-empty cache as an authoritative snapshot,
// which would make the client drop every row it already shows.
func TestRevisitBeforeFirstListDoesNotReplayEmptyCache(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := NewService(useKubeconfig(t, map[string]string{"slow": srv.URL}), hub)
	defer s.Shutdown()
	defer close(release)

	for range 2 {
		if err := s.StartWatch("slow", "apps", "v1", "deployments", "", "age", "desc"); err != nil {
			t.Fatalf("StartWatch: %v", err)
		}
	}
	time.Sleep(300 * time.Millisecond)

	for _, sc := range hub.syncCompletes() {
		if sc.Epoch != 0 {
			t.Fatalf("revisit before the first list sent an authoritative sync_complete for %d items", sc.ItemCount)
		}
	}
}

// A watch is cancelled before the one replacing it starts. Whatever the
// stopped watch's goroutine still does must not end the replacement's sync,
// or revisits would skip the replacement's list as failed.
func TestStoppedWatchDoesNotEndItsReplacementsSync(t *testing.T) {
	s := newServiceForResync(&captureBroadcaster{})
	topic := "items:c:apps:v1:deployments:"
	oldCtx, cancelOld := context.WithCancel(context.Background())
	old := s.pendingSync(oldCtx, topic)
	cancelOld()

	newCtx, cancelNew := context.WithCancel(context.Background())
	defer cancelNew()
	cur := s.pendingSync(newCtx, topic)
	if cur != old {
		t.Fatal("the replacement must take over the unfinished sync, and whoever waits on it")
	}

	s.finishSync(oldCtx, old, context.Canceled)
	s.finishSync(oldCtx, s.pendingSync(oldCtx, topic), context.Canceled)
	select {
	case <-cur.done:
		t.Fatal("the stopped watch ended its replacement's sync")
	default:
	}

	s.finishSync(newCtx, cur, nil)
	select {
	case <-cur.done:
		if cur.err != nil {
			t.Fatalf("sync ended with %v", cur.err)
		}
	default:
		t.Fatal("the replacement could not end its own sync")
	}
}

// A cluster whose client cannot be built still gets one sync_complete so the
// UI leaves its loading state, and a revisit does not replay the empty cache
// as an authoritative (row-dropping) snapshot.
func TestWatchOnUnreachableClusterCompletesSyncOnce(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := NewService(useKubeconfig(t, map[string]string{}), hub)
	defer s.Shutdown()
	topic := "items:missing:apps:v1:deployments:"

	if err := s.StartWatch("missing", "apps", "v1", "deployments", "", "age", "desc"); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(hub.syncCompletes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	syncs := hub.syncCompletes()
	if len(syncs) != 1 || syncs[0].Epoch != 0 || syncs[0].ItemCount != 0 {
		t.Fatalf("expected one non-authoritative empty sync_complete, got %+v", syncs)
	}

	s.sendCachedData(topic, "age", "desc")

	// A revisit must not replay the empty cache as an authoritative
	// snapshot, but it gets the same non-authoritative signal as the first
	// subscriber so it leaves its loading state.
	got := hub.syncCompletes()
	if len(got) != 2 || got[1].Epoch != 0 || got[1].ItemCount != 0 {
		t.Fatalf("a revisit must get one non-authoritative sync_complete, got %+v", got)
	}
	if len(hub.bulkLists()) != 0 {
		t.Fatal("a revisit replayed the empty cache as a snapshot")
	}
}

func TestRetryNowEndsTheBackoffOfItsClusterOnly(t *testing.T) {
	s := newListSyncService(&captureBroadcaster{sortBy: "age", sortOrder: "desc"})
	retry := s.retrySignal("c")
	attempts := 9 // the wait that follows is 15 to 30 seconds
	woken := make(chan bool, 1)
	go func() { woken <- sleepBackoff(context.Background(), retry, &attempts) }()

	s.RetryNow("other")
	select {
	case <-woken:
		t.Fatal("another cluster's retry ended the wait")
	case <-time.After(50 * time.Millisecond):
	}

	s.RetryNow("c")
	select {
	case wanted := <-woken:
		if !wanted {
			t.Fatal("the watch was reported as no longer wanted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watch is still waiting after RetryNow")
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d after a retry, want the backoff started over", attempts)
	}

	// The next backoff is waited out again.
	select {
	case <-s.retrySignal("c"):
		t.Fatal("a retry that is over ends the following backoff too")
	default:
	}
}

// healingLister fails every list until it is healed.
type healingLister struct {
	recordingLister
	healed atomic.Bool
	failed atomic.Int64
}

func (l *healingLister) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if !l.healed.Load() {
		l.failed.Add(1)
		return nil, errors.New("the server is currently unable to handle the request")
	}
	return l.fakeListLister.List(ctx, opts)
}

func (l *healingLister) Namespace(string) resourceLister { return l }

// A status check that finds the cluster back must not leave its lists to
// their backoff: after a sign-in they stayed stale for up to half a minute.
func TestWatchLoopListsAtOnceWhenItsClusterAnswersAgain(t *testing.T) {
	hub := &captureBroadcaster{sortBy: "age", sortOrder: "desc"}
	s := newListSyncService(hub)
	l := &healingLister{}
	l.items = []unstructured.Unstructured{mkListObj("a"), mkListObj("b")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runWatchLoop(ctx, "c", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "", "items:c:apps:v1:deployments:", l)
	}()
	defer func() { cancel(); <-done }()

	// Into the second attempt: the wait that follows it is one to two seconds.
	deadline := time.Now().Add(10 * time.Second)
	for l.failed.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the list was not retried")
		}
		time.Sleep(10 * time.Millisecond)
	}

	l.healed.Store(true)
	s.RetryNow("c")

	for deadline = time.Now().Add(400 * time.Millisecond); s.cache.Count("items:c:apps:v1:deployments:") != 2; {
		if time.Now().After(deadline) {
			t.Fatal("the list was not fetched within 400ms of the cluster answering again")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
