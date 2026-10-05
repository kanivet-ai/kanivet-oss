package rightsizing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/metrics"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testLimiter() (*limiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_000_000, 0)}
	return newLimiter(c.now, func() float64 { return 0.5 }), c
}

func oneQuery(l *limiter, o outcome) {
	if err := l.acquire(context.Background()); err != nil {
		panic(err)
	}
	l.release(o)
}

func TestLimiterHalvesOnOverloadAndTripsBreaker(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 4
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	if l.limit != 2 {
		t.Fatalf("limit %v after one overload, want 2", l.limit)
	}
	if l.state().PausedFor != 0 {
		t.Fatal("one overload must not open the breaker")
	}
	// Each sent after the cut before it: a new congestion event every time.
	clock.t = clock.t.Add(2 * time.Second)
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	clock.t = clock.t.Add(2 * time.Second)
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	if l.limit != 0.5 || l.state().PausedFor < breakerMin {
		t.Fatalf("after three overloads: limit %v paused %v", l.limit, l.state().PausedFor)
	}
}

// When every query in flight hits a full store at once, that is one
// congestion event: one cut, as TCP halves once per window, and nothing
// towards the breaker. Queries sent after the cut start a new event.
func TestLimiterCutsOncePerCongestionEvent(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 6
	for range 6 {
		_ = l.acquire(context.Background())
	}
	for range 6 {
		l.release(outcome{latency: 50 * time.Millisecond, overload: true})
	}
	if st := l.state(); st.Limit != 3 || st.OverloadCuts != 1 || st.PausedFor != 0 {
		t.Fatalf("one burst of 6 parallel 429s: limit 6 → %.2f after %d cuts, paused %v", st.Limit, st.OverloadCuts, st.PausedFor)
	}
	clock.t = clock.t.Add(time.Second)
	oneQuery(l, outcome{latency: 50 * time.Millisecond, overload: true})
	if l.limit != 1.5 {
		t.Fatalf("limit %v after a second event, want 1.5", l.limit)
	}
}

// A latency cut just before doesn't hide the first overload of a new event,
// and an answer that belongs to an event already cut for still honours the
// store's Retry-After.
func TestLimiterOverloadRightAfterALatencyCut(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 4
	l.lastCut = clock.t
	oneQuery(l, outcome{latency: time.Second, overload: true})
	if l.limit != 2 {
		t.Fatalf("limit %v: the overload was taken for part of the latency cut", l.limit)
	}
	oneQuery(l, outcome{latency: time.Second, overload: true, retryAfter: 30 * time.Second})
	if st := l.state(); st.Limit != 2 || st.PausedFor != 30*time.Second {
		t.Fatalf("same event: limit %v paused %v, want 2 and the store's 30s", st.Limit, st.PausedFor)
	}
}

// After a pause the next query probes the store. A late answer to a query
// sent before the pause belongs to the event that caused it, so it can't
// fail the probe; the probe's own overload does.
func TestLimiterLateAnswerDoesNotFailTheProbe(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 6
	for range 2 {
		_ = l.acquire(context.Background())
	}
	// The store turns one query away at once and asks for a second's pause;
	// the other is still on its way back.
	l.release(outcome{latency: 50 * time.Millisecond, overload: true, retryAfter: time.Second})
	clock.t = clock.t.Add(2 * time.Second)
	probe := make(chan error, 1)
	go func() { probe <- l.acquire(context.Background()) }()
	waitQueued(t, l, 1)
	l.release(outcome{latency: 3 * time.Second, overload: true})
	if st := l.state(); st.OverloadCuts != 1 || st.PausedFor != 0 {
		t.Fatalf("a late answer from before the pause: %d overload cuts, paused %v", st.OverloadCuts, st.PausedFor)
	}
	if err := <-probe; err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(100 * time.Millisecond)
	l.release(outcome{latency: 100 * time.Millisecond, overload: true})
	if p := l.state().PausedFor; p < breakerMin {
		t.Fatalf("a failed probe must re-open the breaker: paused %v", p)
	}
}

func TestLimiterHonoursRetryAfter(t *testing.T) {
	l, _ := testLimiter()
	oneQuery(l, outcome{latency: time.Second, overload: true, retryAfter: 30 * time.Second})
	if p := l.state().PausedFor; p != 30*time.Second {
		t.Fatalf("paused %v, want the store's 30s", p)
	}
}

func TestLimiterGrowsSlowlyWhenHealthyAndCaps(t *testing.T) {
	l, _ := testLimiter()
	for range 200 {
		// Keep every slot busy so growth is earned, then release one.
		n := l.slots()
		for range n {
			_ = l.acquire(context.Background())
		}
		for range n {
			l.release(outcome{latency: 100 * time.Millisecond, work: 24})
		}
	}
	if l.limit != limitMax {
		t.Fatalf("limit %v, want the %v ceiling", l.limit, limitMax)
	}
}

func TestLimiterIdleCapacityIsNotGrowth(t *testing.T) {
	l, _ := testLimiter()
	for range 100 {
		oneQuery(l, outcome{latency: 100 * time.Millisecond, work: 24}) // one at a time
	}
	if l.limit != limitInitial {
		t.Fatalf("limit grew to %v without ever using its slots", l.limit)
	}
}

// A query class without enough history to judge its latency is no evidence
// that the store has room, above one query at a time or below it.
func TestLimiterColdClassIsNoEvidenceForGrowth(t *testing.T) {
	l, _ := testLimiter()
	l.limit = 1 // one slot, so every answer had it fully used
	for range baselineMinSamples - 1 {
		oneQuery(l, outcome{latency: 100 * time.Millisecond, work: 24})
	}
	if l.limit != 1 {
		t.Fatalf("limit grew to %v on a class with no history", l.limit)
	}
	oneQuery(l, outcome{latency: 100 * time.Millisecond, work: 24})
	if l.limit <= 1 {
		t.Fatalf("limit %v: a fast answer in a class with history should earn growth", l.limit)
	}

	l, clock := testLimiter()
	l.limit = 0.5
	for range baselineMinSamples - 1 {
		oneQuery(l, outcome{latency: 100 * time.Millisecond, work: 24})
		clock.t = clock.t.Add(time.Second) // past the pacing gap
	}
	if l.limit != 0.5 {
		t.Fatalf("fractional limit climbed to %v on a class with no history", l.limit)
	}
}

func TestLimiterCutsWhenLatencyShowsQueueing(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 5
	for range 10 {
		oneQuery(l, outcome{latency: 200 * time.Millisecond, work: 24})
	}
	before := l.limit
	for range 10 {
		clock.t = clock.t.Add(5 * time.Second)
		oneQuery(l, outcome{latency: time.Second, work: 24}) // 5× slower per hour of range
	}
	if l.limit >= before || l.limit < limitMin {
		t.Fatalf("limit %v → %v under queueing", before, l.limit)
	}
}

func TestLimiterComparesLatencyPerUnitOfWork(t *testing.T) {
	l, _ := testLimiter()
	l.limit = 3
	oneQuery(l, outcome{latency: 2 * time.Second, work: 24})
	before := l.limit
	oneQuery(l, outcome{latency: 200 * time.Millisecond, work: 1}) // slower per sample, but a tiny answer
	oneQuery(l, outcome{latency: 2 * time.Second, work: 24})
	if l.limit < before*queueDecrease*0.99 {
		t.Fatalf("a short query should not read as queueing: %v → %v", before, l.limit)
	}
}

// Below one query in flight, the pause after each query is measured in how
// long answered queries take: a 429 that came back at once must not shrink it.
func TestLimiterPacesByAnsweredLatency(t *testing.T) {
	l, clock := testLimiter()
	for range 10 {
		oneQuery(l, outcome{latency: time.Second, work: 24}) // one at a time: no growth
	}
	oneQuery(l, outcome{latency: 20 * time.Millisecond, overload: true})
	clock.t = clock.t.Add(time.Second)
	oneQuery(l, outcome{latency: 20 * time.Millisecond, overload: true})
	if l.limit != 0.5 {
		t.Fatalf("limit %v, want 0.5", l.limit)
	}
	if gap := l.nextAllowed.Sub(clock.t); gap != time.Second {
		t.Fatalf("pacing gap %v after a fast 429 at limit 0.5, want the 1s an answer takes", gap)
	}
}

// Answers are compared only with others of about the same size: every
// bucket spans a factor of two.
func TestSizeBucketsSpanAFactorOfTwo(t *testing.T) {
	for n := 1; n < 1_000_000; n = n*5/4 + 1 {
		if d := sizeBucket(2*n) - sizeBucket(n); d != 1 {
			t.Fatalf("%d and %d samples are %d buckets apart, want 1", n, 2*n, d)
		}
	}
}

func TestRetryBudgetPreventsStorms(t *testing.T) {
	l, _ := testLimiter()
	granted := 0
	for range 50 {
		if l.allowRetry() {
			granted++
		}
	}
	if granted != int(retryBudgetMax/2) {
		t.Fatalf("granted %d retries from a fresh budget, want %d", granted, int(retryBudgetMax/2))
	}
	for range 30 {
		oneQuery(l, outcome{latency: 10 * time.Millisecond})
	}
	if !l.allowRetry() {
		t.Fatal("successful requests should earn retry budget back")
	}
}

func TestBackoffHasFullJitterUnderACap(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	seen := map[time.Duration]bool{}
	for n := range 10 {
		d := backoff(n, rng.Float64)
		if d < 0 || d > backoffCap {
			t.Fatalf("attempt %d: %v", n, d)
		}
		seen[d] = true
	}
	if len(seen) < 8 {
		t.Fatal("delays should be spread, not identical")
	}
}

func TestInteractiveGoesFirst(t *testing.T) {
	l, _ := testLimiter()
	l.limit = 1
	_ = l.acquire(context.Background()) // the one slot is busy
	order := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = l.acquire(context.Background())
		order <- "background"
		l.release(outcome{latency: time.Millisecond})
	}()
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer wg.Done()
		_ = l.acquire(interactive(context.Background()))
		order <- "interactive"
		l.release(outcome{latency: time.Millisecond})
	}()
	time.Sleep(20 * time.Millisecond)
	// Free exactly one slot without the release growing the limit, so only
	// one waiter can go: it must be the interactive one.
	l.mu.Lock()
	l.inflight--
	l.wake()
	l.mu.Unlock()
	wg.Wait()
	if first := <-order; first != "interactive" {
		t.Fatalf("%s went first", first)
	}
}

// A waiter whose context ends just as wake() hands it the free slot must pass
// the slot on, or the next waiter sleeps with nothing in flight until some
// other query returns, if any ever does.
func TestLimiterCancelledWaiterPassesItsTurnOn(t *testing.T) {
	l, _ := testLimiter()
	l.limit = 1
	_ = l.acquire(context.Background()) // the one slot is busy
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() { gaveUp <- l.acquire(ctx) }()
	waitQueued(t, l, 1)
	next := make(chan error, 1)
	go func() { next <- l.acquire(context.Background()) }()
	waitQueued(t, l, 2)

	// The first waiter's context ends while the slot is being freed: its
	// select has taken ctx.Done and it waits for the lock while wake()
	// picks it for the slot.
	l.mu.Lock()
	cancel()
	time.Sleep(50 * time.Millisecond)
	l.inflight--
	l.wake()
	l.mu.Unlock()

	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("the next waiter is stuck although the slot is free")
	}
}

// A user told at once that the store is paused gives up the turn wake()
// handed it, and the turn goes on to the report queued behind, which would
// otherwise sit with the slot free until some other query returns.
func TestLimiterBusyUserPassesItsTurnOn(t *testing.T) {
	l, _ := testLimiter()
	l.now = time.Now
	l.limit, l.overloads = 1, breakerTrips-1
	_ = l.acquire(context.Background()) // the one slot is busy
	user := make(chan error, 1)
	go func() { user <- l.acquire(interactive(context.Background())) }()
	waitQueued(t, l, 1)
	// The report can't wait out a breaker pause before its deadline, so once
	// it has the turn it is told at once too.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	report := make(chan error, 1)
	go func() { report <- l.acquire(ctx) }()
	waitQueued(t, l, 2)

	l.release(outcome{latency: time.Second, overload: true}) // trips the breaker
	if err := <-user; err != ErrStoreBusy {
		t.Fatalf("user: %v, want ErrStoreBusy", err)
	}
	if err := <-report; err != ErrStoreBusy {
		t.Fatalf("report: %v, want ErrStoreBusy: the user's turn never reached it", err)
	}
}

func waitQueued(t *testing.T, l *limiter, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		l.mu.Lock()
		queued := len(l.waiting[priorityBackground]) + len(l.waiting[priorityInteractive])
		l.mu.Unlock()
		if queued >= n {
			return
		}
	}
	t.Fatalf("%d waiters never queued", n)
}

func TestBreakerFailsFastWhenTheCallerCannotWait(t *testing.T) {
	l, _ := testLimiter()
	l.pausedUntil = time.Now().Add(time.Minute)
	l.now = time.Now
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.acquire(ctx); err != ErrStoreBusy {
		t.Fatalf("err %v, want ErrStoreBusy", err)
	}
}

// A user opening a workload's evidence is told the store is paused rather
// than kept waiting a minute or two. Only the last seconds of a pause are
// worth sitting out.
func TestBreakerTellsAWaitingUserAtOnce(t *testing.T) {
	l, _ := testLimiter()
	l.now = time.Now
	l.pausedUntil = time.Now().Add(time.Minute)
	done := make(chan error, 1)
	go func() { done <- l.acquire(interactive(context.Background())) }()
	select {
	case err := <-done:
		if err != ErrStoreBusy {
			t.Fatalf("err %v, want ErrStoreBusy", err)
		}
	case <-time.After(time.Second):
		t.Fatal("an interactive query is waiting out a minute's pause")
	}

	l, _ = testLimiter()
	l.now = time.Now
	l.pausedUntil = time.Now().Add(50 * time.Millisecond)
	if err := l.acquire(interactive(context.Background())); err != nil {
		t.Fatalf("a pause about to end: %v", err)
	}
}

// While the breaker holds a cluster's queries back, the evidence drawer gets
// a 503 at once saying why and when to ask again. A vcluster's queries go to
// its host's store, so the host's pause is the one that counts.
func TestEvidenceSaysWhenTheStoreIsPaused(t *testing.T) {
	host := t.Name()
	l := limiterFor(host)
	defer limiters.Delete(host)
	l.mu.Lock()
	l.pausedUntil = time.Now().Add(time.Minute)
	l.mu.Unlock()
	s := NewService(&k8s.MockClient{}, &evidenceHistory{calls: map[string]int{}}, evidenceFixtures{}, evidenceFixtures{}, nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	q := url.Values{"cluster": {k8s.VClusterIDPrefix + host + ":vc-apps:vc"}, "provider": {"mimir"}, "namespace": {"apps"}, "kind": {"Deployment"}, "name": {"api"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/rightsizing/workload?"+q.Encode(), nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewHandler(s).GetWorkload(c)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the evidence request is waiting out the pause")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d %s, want 503", w.Code, w.Body.String())
	}
	secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || secs < 55 || secs > 60 {
		t.Fatalf("Retry-After %q, want the minute's pause", w.Header().Get("Retry-After"))
	}
	var body struct {
		Error             string `json:"error"`
		RetryAfterSeconds int    `json:"retryAfterSeconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RetryAfterSeconds != secs || !strings.Contains(body.Error, fmt.Sprintf("Resuming in %ds", secs)) {
		t.Fatalf("body %+v with Retry-After %d", body, secs)
	}
}

// pausingHistory answers like evidenceHistory, but the breaker opens as the
// store answers the first restarts query.
type pausingHistory struct {
	*evidenceHistory
	l *limiter
}

func (h pausingHistory) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if strings.Contains(query, "kube_pod_container_status_restarts_total") {
		h.l.mu.Lock()
		h.l.pausedUntil = time.Now().Add(time.Minute)
		h.l.mu.Unlock()
	}
	return h.evidenceHistory.QueryRange(ctx, cluster, query, start, end, step)
}

// When the breaker opens partway through, the restarts and OOM kills still
// to load fail at once. Evidence without them would read as a workload that
// never ran out of memory, so the drawer is told the store is busy instead.
func TestEvidenceSaysWhenAPauseCutItShort(t *testing.T) {
	cluster := t.Name()
	defer limiters.Delete(cluster)
	h := pausingHistory{&evidenceHistory{calls: map[string]int{}}, limiterFor(cluster)}
	s := NewService(&k8s.MockClient{}, h, evidenceFixtures{}, evidenceFixtures{}, nil)
	_, err := s.GetEvidence(context.Background(), WorkloadQuery{
		Cluster: cluster, Provider: "mimir", Namespace: "apps", Kind: "Deployment", Name: "api",
		Profile: ProfileBalanced, Window: defaultWindow,
	})
	if err != ErrStoreBusy {
		t.Fatalf("err %v, want ErrStoreBusy", err)
	}
}

// stuckStore never answers on its own: a query ends when its context does,
// with the error net/http gives for that.
type stuckStore struct{}

func (stuckStore) QueryRange(ctx context.Context, _, _ string, _, _ time.Time, _ time.Duration) ([]metrics.HistorySeries, error) {
	<-ctx.Done()
	return nil, &url.Error{Op: "Get", URL: "http://mimir/api/v1/query_range", Err: ctx.Err()}
}

func (s stuckStore) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	return s.QueryRange(ctx, cluster, query, at, at, time.Minute)
}

// erringStore answers every query at once with err.
type erringStore struct{ err error }

func (s erringStore) QueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	return nil, s.err
}

func (s erringStore) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	return s.QueryRange(ctx, cluster, query, at, at, time.Minute)
}

func controlledBy(q historyQuerier, l *limiter) *controlled {
	return &controlled{q: q, sent: new(int64), mu: &sync.Mutex{}, lim: func(string) *limiter { return l }, rand: func() float64 { return 0 }}
}

// When a report runs out of time, the queries it has in flight fail with its
// own deadline, which says nothing about the store: no cut, no pause. A
// timeout of the HTTP client leaves the caller's context alone and still
// counts as overload.
func TestControlledOwnDeadlineIsNotOverload(t *testing.T) {
	l, _ := testLimiter()
	l.limit = 6
	c := controlledBy(stuckStore{}, l)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.QueryRange(ctx, "c", "q", time.Time{}, time.Time{}.Add(time.Hour), time.Minute); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err %v, want the report's deadline", err)
			}
		}()
	}
	wg.Wait()
	if st := l.state(); st.Limit != 6 || st.OverloadCuts != 0 || st.PausedFor != 0 || st.InFlight != 0 {
		t.Fatalf("own deadline: limit 6 → %.2f, %d overload cuts, paused %v, %d in flight", st.Limit, st.OverloadCuts, st.PausedFor, st.InFlight)
	}

	c.q = erringStore{&url.Error{Op: "Get", URL: "http://mimir/api/v1/query_range", Err: context.DeadlineExceeded}}
	_, _ = c.QueryRange(context.Background(), "c", "q", time.Time{}, time.Time{}.Add(time.Hour), time.Minute)
	if st := l.state(); st.OverloadCuts == 0 {
		t.Fatal("an http.Client timeout must still count as overload")
	}
}

// A query the store rejected, or one its caller gave up on, took as long as
// it did for reasons other than load: neither may enter the latency
// baseline, or every real answer after a fast failure reads as queueing.
func TestControlledKeepsFailuresOutOfTheBaseline(t *testing.T) {
	l, _ := testLimiter()
	c := controlledBy(erringStore{&metrics.QueryError{Type: "bad_data", Message: "parse error"}}, l)
	if _, err := c.QueryInstant(context.Background(), "c", "count(x)", time.Time{}); err == nil {
		t.Fatal("want the store's error")
	}
	c.q = stuckStore{}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, cancel)
	if _, err := c.QueryInstant(ctx, "c", "count(x)", time.Time{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	if len(l.classes) != 0 || l.inflight != 0 {
		t.Fatalf("failed queries reached the baseline: %d classes, %d in flight", len(l.classes), l.inflight)
	}
}

// After a pause the next query is a probe that decides whether to resume,
// whether or not anyone waited the pause out. A probe its caller gave up on
// decided nothing, so the breaker stays half-open.
func TestControlledCancelledProbeKeepsBreakerHalfOpen(t *testing.T) {
	l, clock := testLimiter()
	l.limit = 6
	oneQuery(l, outcome{latency: 50 * time.Millisecond, overload: true, retryAfter: 30 * time.Second})
	clock.t = clock.t.Add(31 * time.Second)
	c := controlledBy(stuckStore{}, l)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := c.QueryRange(ctx, "c", "q", time.Time{}, time.Time{}.Add(time.Hour), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("probe: %v", err)
	}
	l.mu.Lock()
	probing, slots := l.probing, l.slots()
	l.mu.Unlock()
	if !probing || slots != 1 {
		t.Fatalf("a cancelled probe closed the breaker: probing=%v, %d slots open", probing, slots)
	}
}

// ---- Simulation -------------------------------------------------------------

// overloadedStore behaves like a metrics store with fixed capacity: up to
// capacity queries run at full speed, more queue (latency grows with the
// excess), and past twice the capacity it sheds load with 429s.
type overloadedStore struct {
	capacity int
	base     time.Duration

	mu       sync.Mutex
	inflight int
	peak     int
	rejected atomic.Int64
	served   atomic.Int64
}

func (s *overloadedStore) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	s.mu.Lock()
	s.inflight++
	n := s.inflight
	s.peak = max(s.peak, n)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		s.mu.Unlock()
	}()
	s.mu.Lock()
	capacity := s.capacity
	s.mu.Unlock()
	if n > 2*capacity {
		s.rejected.Add(1)
		time.Sleep(s.base / 4)
		return nil, &metrics.HTTPStatusError{Store: "mimir", Code: 429}
	}
	time.Sleep(s.base * time.Duration(1+max(0, n-capacity)))
	s.served.Add(1)
	return nil, nil
}

func (s *overloadedStore) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	return s.QueryRange(ctx, cluster, query, at, at, time.Minute)
}

// Three users, each with their own Kanivet (their own limiter, no
// coordination), hammer one store with far more parallel work than it can
// take. AIMD must keep the store near capacity, shed almost nothing, and give
// each user a fair share.
func TestSimulatedUsersShareAnOverloadedStoreFairly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing simulation")
	}
	store := &overloadedStore{capacity: 4, base: 10 * time.Millisecond}
	const users, workers = 3, 12
	// Fairness is judged on the steady state: AIMD clients converge to equal
	// shares, but in the first second one may lose a few rounds of 429s and
	// trail by chance, which made a whole-run comparison flaky.
	const warmup, run = time.Second, 4 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), run)
	defer cancel()

	done := make([]atomic.Int64, users)
	var atWarmup [users]int64
	snapped := make(chan struct{})
	time.AfterFunc(warmup, func() {
		for u := range users {
			atWarmup[u] = done[u].Load()
		}
		close(snapped)
	})
	lims := make([]*limiter, users)
	var wg sync.WaitGroup
	for u := range users {
		l := newLimiter(time.Now, rand.New(rand.NewPCG(uint64(u), 9)).Float64)
		lims[u] = l
		l.pauseMin, l.pauseMax = 50*time.Millisecond, 100*time.Millisecond
		c := &controlled{q: store, sent: new(int64), mu: &sync.Mutex{}, lim: func(string) *limiter { return l }, rand: lockedRand(uint64(u), 7)}
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					if _, err := c.QueryRange(ctx, "c", "q", time.Time{}, time.Time{}.Add(24*time.Hour), time.Minute); err == nil {
						done[u].Add(1)
					}
				}
			}()
		}
	}
	wg.Wait()
	<-snapped

	served, rejected := store.served.Load(), store.rejected.Load()
	if served == 0 {
		t.Fatal("nothing served")
	}
	if share := float64(rejected) / float64(served+rejected); share > 0.05 {
		t.Errorf("store shed %.1f%% of queries; the clients should back off before that", share*100)
	}
	// 36 workers wanted to run at once against a capacity of 4.
	if store.peak > 3*store.capacity {
		t.Errorf("peak concurrency %d against capacity %d", store.peak, store.capacity)
	}
	steady := make([]int64, users)
	for u := range users {
		steady[u] = done[u].Load() - atWarmup[u]
	}
	lo, hi := slices.Min(steady), slices.Max(steady)
	if float64(lo) < 0.5*float64(hi) {
		t.Errorf("unfair steady-state shares: %v", steady)
		for u, l := range lims {
			st := l.state()
			t.Logf("user %d: limit %.2f grows %d queue cuts %d overload cuts %d max ratio %.1f", u, st.Limit, st.Grows, st.QueueCuts, st.OverloadCuts, st.MaxRatio)
		}
	}
	t.Logf("served %d, rejected %d, peak %d, steady-state per user %v", served, rejected, store.peak, steady)
}

// The store's capacity collapses mid-run, as in a Mimir incident. The clients
// must notice and back off so the store mostly stops shedding load, instead of
// keeping it pinned at the rejection point.
func TestSimulatedStoreDegradationMakesClientsBackOff(t *testing.T) {
	if testing.Short() {
		t.Skip("timing simulation")
	}
	store := &overloadedStore{capacity: 6, base: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for u := range 3 {
		l := newLimiter(time.Now, rand.New(rand.NewPCG(uint64(u), 3)).Float64)
		l.pauseMin, l.pauseMax = 100*time.Millisecond, 200*time.Millisecond
		c := &controlled{q: store, sent: new(int64), mu: &sync.Mutex{}, lim: func(string) *limiter { return l }, rand: lockedRand(uint64(u), 5)}
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					_, _ = c.QueryRange(ctx, "c", "q", time.Time{}, time.Time{}.Add(24*time.Hour), time.Minute)
				}
			}()
		}
	}
	time.Sleep(1500 * time.Millisecond)
	store.mu.Lock()
	store.capacity = 1
	store.mu.Unlock()
	rejectedAtDrop := store.rejected.Load()
	servedAtDrop := store.served.Load()
	time.Sleep(time.Second) // let them adapt
	adaptRejected, adaptServed := store.rejected.Load(), store.served.Load()
	wg.Wait()
	lateRejected := store.rejected.Load() - adaptRejected
	lateServed := store.served.Load() - adaptServed
	t.Logf("before drop: served %d rejected %d; settled after drop: served %d rejected %d", servedAtDrop, rejectedAtDrop, lateServed, lateRejected)
	if lateServed == 0 {
		t.Fatal("clients stopped completely; they should keep a trickle going")
	}
	// Latency alone never takes a client below one query in flight (so a
	// small client isn't starved by a big one), so three uncoordinated
	// clients against a store that now rejects above two settle into a
	// trickle of rejections: 14-24% across runs and machines. Without
	// backing off nearly everything would be rejected: 30 workers on a
	// store that takes two. The breaker pause here is 100-200ms; in
	// production it is a minute or two, which sheds far less.
	if share := float64(lateRejected) / float64(lateServed+lateRejected); share > 1.0/3 {
		t.Errorf("after adapting, %.0f%% of queries still rejected", share*100)
	}
}

// lockedRand is a seeded source safe for the many goroutines that share one
// controlled querier, as the global source is in production.
func lockedRand(seed1, seed2 uint64) func() float64 {
	var mu sync.Mutex
	r := rand.New(rand.NewPCG(seed1, seed2))
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		return r.Float64()
	}
}

// Day chunks are served above the limiter: a cache hit takes no slot and
// feeds no latency into its baseline, and a chunks-only read never queries.
func TestCachedDaysBypassLimiter(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	chunks := &memChunks{data: map[string][]byte{}}
	g := newGrid(now.Add(-2*day), day, time.Hour) // finished days only
	warm := newChunker(&countingStore{}, chunks)
	warm.now = func() time.Time { return now }
	if _, err := warm.rangeQuery(context.Background(), "cluster", "cpu", g, nil); err != nil {
		t.Fatal(err)
	}
	q := newControlled(&countingStore{})
	q.lim = func(string) *limiter { t.Fatal("cache hit entered upstream limiter"); return nil }
	c := newChunker(q, chunks)
	c.now = warm.now
	res, err := c.rangeQuery(context.Background(), "cluster", "cpu", g, nil)
	if err != nil || len(res) == 0 || c.hits.Load() == 0 {
		t.Fatalf("result=%v err=%v hits=%d", res, err, c.hits.Load())
	}
	if _, err := q.QueryInstant(withChunksOnly(context.Background()), "cluster", "cpu", now); err != errNotCached {
		t.Fatalf("chunks-only instant query: %v", err)
	}
}
