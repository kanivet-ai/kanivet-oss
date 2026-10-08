package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/websocket/core"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func shrinkLogRetries(t *testing.T) {
	t.Helper()
	oldF, oldFM, oldW, oldWM := followRetryDelay, followMaxRetryDelay, watchRetryDelay, watchMaxRetryDelay
	followRetryDelay, followMaxRetryDelay = 100*time.Microsecond, time.Millisecond
	watchRetryDelay, watchMaxRetryDelay = time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() {
		followRetryDelay, followMaxRetryDelay, watchRetryDelay, watchMaxRetryDelay = oldF, oldFM, oldW, oldWM
	})
}

// runBG runs fn on a goroutine that the test cancels and waits for at its end,
// so nothing still reads the shrunken retry delays when they are restored.
func runBG(t *testing.T, cancel context.CancelFunc, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func apiClient(t *testing.T, h http.Handler) kubernetes.Interface {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: 10000, Burst: 10000})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func podJSON(name, phase, rv string) []byte {
	b, _ := json.Marshal(corev1.Pod{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", ResourceVersion: rv},
		Status:     corev1.PodStatus{Phase: corev1.PodPhase(phase)},
	})
	return b
}

func writeStatus(w http.ResponseWriter, code int, reason, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   "Failure", Message: msg, Reason: metav1.StatusReason(reason), Code: int32(code),
	})
}

// A pod in CrashLoopBackOff has no log stream for minutes at a time. The
// follower used to give up after 30 failed attempts and never followed the
// pod's next start.
func TestFollowPodKeepsRetryingCrashLoopingPod(t *testing.T) {
	shrinkLogRetries(t)
	var logCalls atomic.Int64
	client := apiClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			if logCalls.Add(1) <= 60 {
				writeStatus(w, 400, "BadRequest", "container is waiting to start: CrashLoopBackOff")
				return
			}
			fmt.Fprint(w, "2026-01-01T00:00:01Z back again\n")
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(podJSON("p", "Running", "1"))
		}
	}))

	out := make(chan logEntry, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runBG(t, cancel, func() { followPod(ctx, client, "ns", "p", "", time.Time{}, 0, out) })

	select {
	case e := <-out:
		if e.data != "back again" {
			t.Fatalf("got %q", e.data)
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("follower gave up after %d log attempts", logCalls.Load())
	}
}

func TestFollowPodStopsWhenPodIsGoneOrFinished(t *testing.T) {
	shrinkLogRetries(t)
	for _, tc := range []struct {
		name  string
		serve func(w http.ResponseWriter, r *http.Request)
	}{
		{"deleted", func(w http.ResponseWriter, r *http.Request) {
			writeStatus(w, 404, "NotFound", "pod not found")
		}},
		{"succeeded", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/log") {
				writeStatus(w, 400, "BadRequest", "previous terminated container not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(podJSON("p", "Succeeded", "1"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := apiClient(t, http.HandlerFunc(tc.serve))
			done := make(chan struct{})
			go func() {
				followPod(context.Background(), client, "ns", "p", "", time.Time{}, 0, make(chan logEntry, 1))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("follower of a gone pod never returned")
			}
		})
	}
}

// A line over the scanner's limit stopped the scan for good, and the retry
// from the same timestamp hit it again every time.
func TestFetchTailTruncatesOverlongLinesAndKeepsReading(t *testing.T) {
	client := apiClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "2026-01-01T00:00:01Z "+strings.Repeat("x", 3*maxLineBytes)+"\n")
		fmt.Fprint(w, "2026-01-01T00:00:02Z after the long line\n")
		fmt.Fprint(w, "2026-01-01T00:00:03Z last line without newline")
	}))
	entries, err := fetchTail(context.Background(), client, "ns", "p", "", 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if len(entries[0].data) != maxLineBytes-len("2026-01-01T00:00:01Z ") {
		t.Fatalf("long line not truncated to the limit: %d bytes", len(entries[0].data))
	}
	if entries[1].data != "after the long line" || entries[2].data != "last line without newline" {
		t.Fatalf("lines after the long one lost: %q, %q", entries[1].data, entries[2].data)
	}
}

// tail comes from the client; it must not size an allocation.
func TestFetchTailIgnoresHugeTailForAllocation(t *testing.T) {
	client := apiClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "2026-01-01T00:00:01Z one\n")
	}))
	entries, err := fetchTail(context.Background(), client, "ns", "p", "", 1<<50, false)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
}

type recordingSender struct {
	mu      sync.Mutex
	payload [][]byte
	fail    func(n int, data []byte) error
	calls   int
}

func (r *recordingSender) Send(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.fail != nil {
		if err := r.fail(r.calls, data); err != nil {
			return err
		}
	}
	r.payload = append(r.payload, data)
	return nil
}

func sequences(t *testing.T, r *recordingSender) []int {
	t.Helper()
	var seqs []int
	for _, p := range r.payload {
		var m struct {
			Payload struct {
				Type     string `json:"type"`
				Sequence int    `json:"sequence"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(p, &m); err != nil {
			t.Fatal(err)
		}
		if m.Payload.Type == "batch" {
			seqs = append(seqs, m.Payload.Sequence)
		}
	}
	return seqs
}

func entriesOf(n, size int) []logEntry {
	out := make([]logEntry, n)
	for i := range out {
		out[i] = logEntry{data: strings.Repeat("x", size), podName: "p", timestamp: time.Unix(int64(i), 0)}
	}
	return out
}

// A batch that failed to send must not consume a sequence number.
func TestBatchSequenceAdvancesOnlyOnSuccess(t *testing.T) {
	h := &LogsHandler{}
	s := &recordingSender{fail: func(n int, _ []byte) error {
		if n == 2 {
			return errors.New("closed")
		}
		return nil
	}}
	seq := 1
	h.sendEntryBatches(context.Background(), s, "k", entriesOf(logBatchSize*3, 1), &seq)
	if got := sequences(t, s); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("sequences %v, want [1 2]", got)
	}
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
}

// A buffer that is full for a moment delays a batch, it does not lose it.
func TestBatchWaitsForFullSendBuffer(t *testing.T) {
	h := &LogsHandler{}
	s := &recordingSender{fail: func(n int, _ []byte) error {
		if n <= 3 {
			return core.ErrRateLimitExceeded
		}
		return nil
	}}
	seq := 1
	h.sendEntryBatches(context.Background(), s, "k", entriesOf(5, 1), &seq)
	if got := sequences(t, s); len(got) != 1 || got[0] != 1 {
		t.Fatalf("batch lost under backpressure: %v", got)
	}
}

// Batches of long lines are cut by size, and one the connection still refuses
// is split rather than dropped.
func TestBatchesAreBoundedBySizeAndSplitWhenTooLarge(t *testing.T) {
	h := &LogsHandler{}
	s := &recordingSender{fail: func(_ int, data []byte) error {
		if len(data) > 300*1024 {
			return core.ErrInvalidMessage
		}
		return nil
	}}
	seq := 1
	h.sendEntryBatches(context.Background(), s, "k", entriesOf(100, 20*1024), &seq) // 2 MB of lines
	total := 0
	for _, p := range s.payload {
		total += strings.Count(string(p), `"podName"`)
		if len(p) > 300*1024 {
			t.Fatalf("a %d byte batch went out", len(p))
		}
	}
	if total != 100 {
		t.Fatalf("%d of 100 lines delivered", total)
	}
}

func TestFencedConnStopsSendingOnceSuperseded(t *testing.T) {
	f := &fencedConn{}
	f.fence()
	if err := f.Send([]byte("x")); !errors.Is(err, errStreamSuperseded) {
		t.Fatalf("send after fence: %v", err)
	}
}

// pod watch plumbing --------------------------------------------------------

type podAPI struct {
	lists     atomic.Int64
	watches   atomic.Int64
	watchFunc func(n int64, w http.ResponseWriter, r *http.Request)
	listPods  func(n int64) (items []string, rv string)
}

func (a *podAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("watch") == "true" {
		a.watchFunc(a.watches.Add(1), w, r)
		return
	}
	n := a.lists.Add(1)
	items, rv := a.listPods(n)
	var parts []string
	for _, name := range items {
		parts = append(parts, string(podJSON(name, "Running", "1")))
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":%q},"items":[%s]}`, rv, strings.Join(parts, ","))
}

func watchEvent(w http.ResponseWriter, typ string, pod []byte) {
	fmt.Fprintf(w, `{"type":%q,"object":%s}`+"\n", typ, pod)
	w.(http.Flusher).Flush()
}

type followRecorder struct {
	mu   sync.Mutex
	pods []string
}

func (f *followRecorder) start(pod string, _ time.Time, _ int64) {
	f.mu.Lock()
	f.pods = append(f.pods, pod)
	f.mu.Unlock()
}

func (f *followRecorder) has(pod string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pods {
		if p == pod {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func podNamesIn(t *testing.T, s *recordingSender) [][]string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, p := range s.payload {
		var m struct {
			Payload struct {
				Type string `json:"type"`
				Pods []struct {
					Name string `json:"name"`
				} `json:"pods"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(p, &m); err != nil || m.Payload.Type != "pods" {
			continue
		}
		var names []string
		for _, pod := range m.Payload.Pods {
			names = append(names, pod.Name)
		}
		out = append(out, names)
	}
	return out
}

// Every pod event used to cost a full pod list, and the watch was never
// reopened once it ended.
func TestWatchPodsResumesAfterWatchEndsWithoutListingPerEvent(t *testing.T) {
	shrinkLogRetries(t)
	api := &podAPI{
		listPods: func(int64) ([]string, string) { return []string{"a"}, "10" },
		watchFunc: func(n int64, w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch n {
			case 1: // delivers one event, then the connection ends
				if rv := r.URL.Query().Get("resourceVersion"); rv != "10" {
					t.Errorf("first watch resourceVersion = %q, want the list's", rv)
				}
				watchEvent(w, "ADDED", podJSON("b", "Running", "11"))
			case 2: // must resume from the last event, not relist
				if rv := r.URL.Query().Get("resourceVersion"); rv != "11" {
					t.Errorf("resumed watch resourceVersion = %q, want 11", rv)
				}
				watchEvent(w, "ADDED", podJSON("c", "Running", "12"))
				<-r.Context().Done()
			default:
				<-r.Context().Done()
			}
		},
	}
	client := apiClient(t, api)
	h := &LogsHandler{}
	s := &recordingSender{}
	rec := &followRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runBG(t, cancel, func() {
		h.watchPods(ctx, s, client, logsPayload{Namespace: "ns", Key: "k"}, "app=x", "", 100, rec.start)
	})

	waitUntil(t, "pod c followed after the watch was reopened", func() bool { return rec.has("c") })
	if !rec.has("a") || !rec.has("b") {
		t.Fatal("pods a and b were not followed")
	}
	if n := api.lists.Load(); n != 1 {
		t.Fatalf("%d pod lists for 2 events, want 1", n)
	}
	waitUntil(t, "updated pod list", func() bool {
		for _, names := range podNamesIn(t, s) {
			if strings.Join(names, ",") == "a,b,c" {
				return true
			}
		}
		return false
	})
}

func TestWatchPodsRelistsWhenResourceVersionIsTooOld(t *testing.T) {
	shrinkLogRetries(t)
	api := &podAPI{
		listPods: func(n int64) ([]string, string) {
			if n == 1 {
				return []string{"a"}, "10"
			}
			return []string{"a", "late"}, "50"
		},
		watchFunc: func(n int64, w http.ResponseWriter, r *http.Request) {
			if n == 1 {
				writeStatus(w, 410, "Expired", "too old resource version")
				return
			}
			<-r.Context().Done()
		},
	}
	client := apiClient(t, api)
	h := &LogsHandler{}
	rec := &followRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runBG(t, cancel, func() {
		h.watchPods(ctx, &recordingSender{}, client, logsPayload{Namespace: "ns", Key: "k"}, "app=x", "", 100, rec.start)
	})

	waitUntil(t, "relist after 410", func() bool { return rec.has("late") })
}
