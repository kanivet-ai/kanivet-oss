package rightsizing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

// Load control for someone's production metrics store. Many Kanivet users can
// point at the same Mimir and none of them coordinate, so each client behaves
// like a TCP sender: it probes for capacity slowly, backs off quickly at the
// first sign of congestion, and stops entirely when the store is in trouble.
// Independent clients following additive-increase / multiplicative-decrease
// converge to a fair share of capacity without talking to each other.
//
//   - Concurrency: a per-cluster limit that grows by 1/limit per fast response
//     and shrinks ×0.7 when latency climbs well above the best seen (queueing),
//     ×0.5 on an overload answer (429, 5xx, timeout, cut-off response).
//   - Latency is compared per thousand samples returned, within a query shape,
//     so a big namespace and a small one, or a 4-day batch and today's few
//     hours, are judged on the same scale.
//   - Retries: exponential backoff with full jitter, the store's Retry-After
//     when it gives one, and a budget of about one retry per ten requests so a
//     struggling store never sees a retry storm.
//   - Circuit breaker: three overloads in a row stop all queries to that
//     cluster for a jittered minute or two; then a single probe decides.
//   - Priority: a user opening a workload's evidence goes ahead of background
//     report queries.

const (
	// limitMin below 1 is a fractional window: one query at a time with a
	// pause after it, so many users on a small store can together send less
	// than one query at a time each.
	limitMin     = 0.1
	limitMax     = 6.0
	limitInitial = 2.0
	// fractionalStep is the additive increase below one query in flight.
	fractionalStep = 0.1

	// Latency well above the best seen means the store is queueing.
	queueRatio    = 2.0
	healthyRatio  = 1.5
	queueDecrease = 0.7
	errorDecrease = 0.5
	// baselineWindow is how many recent responses the baseline latency is the
	// minimum of, per query shape and answer size. A recent minimum, not an
	// all-time best, keeps clients that started at different times on the
	// same baseline, so they react to congestion alike (the latecomer problem
	// of delay-based control). Grouping by answer size matters most: latency
	// is a fixed overhead plus a cost per sample, so without it a big
	// namespace reads as congestion next to a small one. A median or quartile
	// baseline was tried and lost the early warning when clients were
	// congested from their first query.
	baselineWindow = 50
	// baselineMinSamples is how many responses a class needs before its
	// latency is trusted as a congestion signal.
	baselineMinSamples = 8
	recentEWMA         = 0.3

	breakerTrips = 3
	breakerMin   = 60 * time.Second
	breakerMax   = 120 * time.Second

	retryBudgetPerRequest = 0.1
	retryBudgetMax        = 10.0
	backoffBase           = time.Second
	backoffCap            = 20 * time.Second
	maxAttempts           = 4
)

// ErrStoreBusy means the circuit breaker is open: the store recently signalled
// overload and queries are paused.
var ErrStoreBusy = errors.New("metrics store is busy; queries are paused")

type priority int

const (
	priorityBackground priority = iota
	priorityInteractive
)

type priorityKey struct{}

// interactive marks queries a user is waiting on, which go first.
func interactive(ctx context.Context) context.Context {
	return context.WithValue(ctx, priorityKey{}, priorityInteractive)
}

func priorityOf(ctx context.Context) priority {
	if p, ok := ctx.Value(priorityKey{}).(priority); ok {
		return p
	}
	return priorityBackground
}

// outcome is how one query went, as the limiter needs to know it.
type outcome struct {
	latency time.Duration
	// work is the size of the answer in thousands of samples (at least 1).
	work float64
	// class groups queries of the same shape; each has its own baseline,
	// since a per-pod query costs far more per hour than an aggregate one.
	class      string
	overload   bool
	retryAfter time.Duration
}

// limiter is the adaptive concurrency limit for one metrics store.
type limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	rand  func() float64
	limit float64

	inflight int
	waiting  [2][]chan struct{} // by priority

	classes map[string]*latencyStats
	lastCut time.Time

	overloads   int
	pausedUntil time.Time
	probing     bool // half-open: one query decides whether to resume

	budget float64

	pauseMin, pauseMax time.Duration

	// nextAllowed paces queries while the limit is below one.
	nextAllowed time.Time

	// Decision counts, for the log.
	grows, queueCuts, overloadCuts int
	maxRatio                       float64
}

func newLimiter(now func() time.Time, rnd func() float64) *limiter {
	return &limiter{now: now, rand: rnd, limit: limitInitial, budget: retryBudgetMax / 2, pauseMin: breakerMin, pauseMax: breakerMax}
}

var limiters sync.Map // cluster -> *limiter

func limiterFor(cluster string) *limiter {
	l, _ := limiters.LoadOrStore(cluster, newLimiter(time.Now, rand.Float64))
	return l.(*limiter)
}

// slots is how many queries may run at once now.
func (l *limiter) slots() int {
	if l.probing {
		return 1
	}
	return max(1, int(math.Floor(l.limit)))
}

// acquire waits for a slot. It fails fast with ErrStoreBusy while the breaker
// is open, unless the caller can afford to wait out the pause.
func (l *limiter) acquire(ctx context.Context) error {
	p := priorityOf(ctx)
	for {
		l.mu.Lock()
		if wait := l.pausedUntil.Sub(l.now()); wait > 0 {
			l.mu.Unlock()
			if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
				return ErrStoreBusy
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			l.mu.Lock()
			if !l.pausedUntil.IsZero() && !l.now().Before(l.pausedUntil) {
				l.pausedUntil = time.Time{}
				l.probing = true
			}
		}
		// Below one query in flight, wait out the pacing gap first.
		if gap := l.nextAllowed.Sub(l.now()); gap > 0 && l.inflight == 0 {
			l.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(gap):
			}
			continue
		}
		// Interactive work jumps the background queue.
		if l.inflight < l.slots() && (p == priorityInteractive || len(l.waiting[priorityInteractive]) == 0) {
			l.inflight++
			l.budget = math.Min(retryBudgetMax, l.budget+retryBudgetPerRequest)
			l.mu.Unlock()
			return nil
		}
		ch := make(chan struct{})
		l.waiting[p] = append(l.waiting[p], ch)
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			l.mu.Lock()
			l.dropWaiter(p, ch)
			l.mu.Unlock()
			return ctx.Err()
		case <-ch:
		}
	}
}

func (l *limiter) dropWaiter(p priority, ch chan struct{}) {
	q := l.waiting[p]
	for i, c := range q {
		if c == ch {
			l.waiting[p] = append(q[:i], q[i+1:]...)
			return
		}
	}
}

// wake lets waiting queries retry acquire, interactive first.
func (l *limiter) wake() {
	free := l.slots() - l.inflight
	for _, p := range []priority{priorityInteractive, priorityBackground} {
		for free > 0 && len(l.waiting[p]) > 0 {
			close(l.waiting[p][0])
			l.waiting[p] = l.waiting[p][1:]
			free--
		}
	}
}

// release returns a slot and adjusts the limit from how the query went.
func (l *limiter) release(o outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight--
	now := l.now()
	usedFully := l.inflight+1 >= l.slots()

	switch {
	case o.overload:
		l.overloadCuts++
		l.overloads++
		l.limit = math.Max(limitMin, l.limit*errorDecrease)
		l.lastCut = now
		pause := time.Duration(0)
		if l.probing || l.overloads >= breakerTrips {
			pause = l.pauseMin + time.Duration(l.rand()*float64(l.pauseMax-l.pauseMin))
		}
		pause = max(pause, o.retryAfter)
		if pause > 0 {
			l.pausedUntil = now.Add(pause)
		}
		l.probing = false
	default:
		l.overloads = 0
		l.probing = false
		if l.classes == nil {
			l.classes = map[string]*latencyStats{}
		}
		key := fmt.Sprintf("%s#%d", o.class, sizeBucket(int(o.work*1000)))
		st := l.classes[key]
		if st == nil {
			st = &latencyStats{}
			l.classes[key] = st
		}
		ratio := st.observe(o.latency.Seconds())
		l.maxRatio = math.Max(l.maxRatio, ratio)
		switch {
		case ratio > queueRatio:
			// Cut at most once per round trip, or one slow burst would
			// collapse the limit before the first cut takes effect.
			// Latency is a soft signal: it never takes a client below one
			// query at a time. Only explicit overload does that.
			if now.Sub(l.lastCut) > o.latency && l.limit > 1 {
				l.queueCuts++
				l.limit = math.Max(1, l.limit*queueDecrease)
				l.lastCut = now
			}
		case l.limit < 1 && ratio <= queueRatio:
			// Below one query at a time a client adds almost nothing to the
			// queue, so it climbs back on any answer that isn't queued, like
			// TCP's increase on every ack, at half speed while latency is
			// elevated. Waiting for clearly healthy latency starved it while
			// a bigger client kept latency in the 1.5-2x band.
			step := fractionalStep
			if ratio >= healthyRatio {
				step /= 2 // creeping: latency is up, though not queueing
			}
			l.limit = math.Min(1, l.limit+step)
		case ratio < healthyRatio && usedFully:
			l.grows++
			l.limit = math.Min(limitMax, l.limit+1/l.limit)
		}
	}
	if l.limit < 1 {
		l.nextAllowed = now.Add(time.Duration(float64(o.latency) * (1/l.limit - 1)))
	} else {
		l.nextAllowed = time.Time{}
	}
	l.wake()
}

// latencyStats tracks one query class and answer size: a windowed-median
// baseline and an EWMA of recent latency.
type latencyStats struct {
	window []float64
	recent float64
}

// observe records a latency and returns recent / baseline, or 1 until the
// class has enough history to judge.
func (s *latencyStats) observe(latency float64) float64 {
	s.window = append(s.window, latency)
	if len(s.window) > baselineWindow {
		s.window = s.window[1:]
	}
	if s.recent == 0 {
		s.recent = latency
	} else {
		s.recent = recentEWMA*latency + (1-recentEWMA)*s.recent
	}
	if len(s.window) < baselineMinSamples {
		return 1
	}
	return s.recent / math.Max(minOf(s.window), 1e-9)
}

// sizeBucket groups answers by size on a log scale: latency is a fixed
// overhead plus a cost per sample, so only answers of similar size compare.
func sizeBucket(samples int) int {
	return int(math.Log2(float64(max(samples, 1)) / 1000 * 4))
}

var namespaceValue = regexp.MustCompile(`(namespace|pod)(=~?)"[^"]*"`)

// queryClass is a query with its namespace and pod values blanked: the same
// query shape across namespaces is one class.
func queryClass(query string) string {
	return namespaceValue.ReplaceAllString(query, `$1$2""`)
}

// allowRetry spends retry budget; false means retrying now would add to a
// storm and the caller should give up.
func (l *limiter) allowRetry() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.budget < 1 {
		return false
	}
	l.budget--
	return true
}

// LoadState is a snapshot for the UI and logs.
type LoadState struct {
	Limit       float64
	InFlight    int
	PausedFor   time.Duration
	RetryBudget float64

	Grows, QueueCuts, OverloadCuts int
	MaxRatio                       float64
}

func (l *limiter) state() LoadState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return LoadState{Limit: l.limit, InFlight: l.inflight, PausedFor: max(0, l.pausedUntil.Sub(l.now())), RetryBudget: l.budget,
		Grows: l.grows, QueueCuts: l.queueCuts, OverloadCuts: l.overloadCuts, MaxRatio: l.maxRatio}
}

// overloaded reports whether an error means the store is struggling, as
// opposed to a query it rejected or a problem on our side.
func overloaded(err error) (bool, time.Duration) {
	var se *metrics.HTTPStatusError
	if errors.As(err, &se) {
		return se.Retryable(), se.RetryAfter
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || strings.Contains(err.Error(), "unexpected EOF") {
		return true, 0
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true, 0
	}
	return false, 0
}

// backoff is the full-jitter delay before retry attempt n (0-based): a
// uniform draw up to an exponentially growing cap, so clients that failed
// together don't retry together.
func backoff(n int, rnd func() float64) time.Duration {
	ceiling := math.Min(float64(backoffCap), float64(backoffBase)*math.Pow(2, float64(n)))
	return time.Duration(rnd() * ceiling)
}

// controlled is a querier behind the cluster's limiter, with polite retries.
type controlled struct {
	q    historyQuerier
	sent *int64
	mu   *sync.Mutex
	lim  func(cluster string) *limiter
	rand func() float64
}

func newControlled(q historyQuerier) *controlled {
	var n int64
	return &controlled{q: q, sent: &n, mu: &sync.Mutex{}, lim: limiterFor, rand: rand.Float64}
}

func (c *controlled) count() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.sent
}

func (c *controlled) do(ctx context.Context, cluster, class string, call func() ([]metrics.HistorySeries, error)) ([]metrics.HistorySeries, error) {
	lim := c.lim(cluster)
	for attempt := 0; ; attempt++ {
		if err := lim.acquire(ctx); err != nil {
			return nil, err
		}
		c.mu.Lock()
		*c.sent++
		c.mu.Unlock()
		start := time.Now()
		res, err := call()
		over, retryAfter := false, time.Duration(0)
		if err != nil {
			over, retryAfter = overloaded(err)
		}
		samples := 0
		for _, s := range res {
			samples += len(s.Times)
		}
		lim.release(outcome{latency: time.Since(start), work: float64(samples) / 1000, class: class, overload: over, retryAfter: retryAfter})
		if err == nil || !over || attempt+1 >= maxAttempts || !lim.allowRetry() {
			return res, err
		}
		wait := max(backoff(attempt, c.rand), retryAfter)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
	}
}

func (c *controlled) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if chunksOnly(ctx) {
		return nil, errNotCached
	}
	return c.do(ctx, cluster, queryClass(query), func() ([]metrics.HistorySeries, error) {
		return c.q.QueryRange(ctx, cluster, query, start, end, step)
	})
}

func (c *controlled) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	if chunksOnly(ctx) {
		return nil, errNotCached
	}
	return c.do(ctx, cluster, "instant:"+queryClass(query), func() ([]metrics.HistorySeries, error) {
		return c.q.QueryInstant(ctx, cluster, query, at)
	})
}

// busyMessage is what the report says while the breaker holds queries back.
func busyMessage(s LoadState) string {
	return fmt.Sprintf("The metrics store signalled overload, so Kanivet paused its queries. Resuming in %ds.", int(s.PausedFor.Seconds()+0.5))
}

func minOf(xs []float64) float64 {
	m := math.Inf(1)
	for _, x := range xs {
		m = math.Min(m, x)
	}
	return m
}
