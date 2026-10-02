package rightsizing

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	l, _ := testLimiter()
	l.limit = 4
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	if l.limit != 2 {
		t.Fatalf("limit %v after one overload, want 2", l.limit)
	}
	if l.state().PausedFor != 0 {
		t.Fatal("one overload must not open the breaker")
	}
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	oneQuery(l, outcome{latency: time.Second, work: 24, overload: true})
	if l.limit != 0.5 || l.state().PausedFor < breakerMin {
		t.Fatalf("after three overloads: limit %v paused %v", l.limit, l.state().PausedFor)
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
	time.AfterFunc(warmup, func() {
		for u := range users {
			atWarmup[u] = done[u].Load()
		}
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
	if share := float64(lateRejected) / float64(lateServed+lateRejected); share > 0.2 {
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

type cacheHitSource struct{ historyQuerier }

func (cacheHitSource) CachedQueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	return []metrics.HistorySeries{{Values: []float32{42}}}, nil
}
func (cacheHitSource) CachedQueryInstant(context.Context, string, string, time.Time) ([]metrics.HistorySeries, error) {
	return []metrics.HistorySeries{{Values: []float32{42}}}, nil
}

func TestCachedQueriesBypassPausedLimiter(t *testing.T) {
	q := newControlled(cacheHitSource{})
	q.lim = func(string) *limiter { t.Fatal("cache hit entered upstream limiter"); return nil }
	if res, err := q.QueryRange(context.Background(), "cluster", "cpu", time.Now(), time.Now(), time.Minute); err != nil || res[0].Values[0] != 42 {
		t.Fatalf("result=%v err=%v", res, err)
	}
	if res, err := q.QueryInstant(context.Background(), "cluster", "cpu", time.Now()); err != nil || res[0].Values[0] != 42 {
		t.Fatalf("result=%v err=%v", res, err)
	}
}
