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
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

// Load control for someone's production metrics store. Many Kanivet users can
// point at the same Mimir and none of them coordinate, so each client behaves
// like a TCP sender: it probes for capacity slowly, backs off quickly at the
// first sign of congestion, and stops entirely when the store is in trouble.
// Independent clients following the same rule converge to a fair share of
// capacity without talking to each other.
//
//   - Concurrency: a per-cluster limit set the way Netflix's Gradient2 and FAST
//     TCP set theirs. The gradient is a query class's uncongested latency over
//     its latency now (at most 1, at least ½); each answer moves the limit a
//     fifth of the way per round trip towards limit − inUse×(1 − gradient) +
//     queueSize. At rest a client keeps queueSize of its own queries waiting
//     at the store (Little's law): 0.3 of a query up to three slots, growing
//     as √limit above, as Gradient2's does, so a lone user keeps the store
//     busy. The queue is counted in queries, not as a latency ratio that a
//     slow port-forward dilutes, so users with different round trips or
//     query sizes hold the same share of the store; n users keep about 0.3n
//     queries queued between them. A paced user keeps the same queue as one
//     holding slots: its own query always finds theirs at the store, theirs
//     find its only now and then, so it reads more queueing than they do,
//     and a smaller queue would leave it the smaller share.
//   - Overload (429, 5xx, timeout, cut-off response) halves the limit, once
//     per congestion event: the answers to queries already in flight at a cut
//     are the same event, not new ones.
//   - Latency is compared per thousand samples returned, within a query shape,
//     so a big namespace and a small one, or a 4-day batch and today's few
//     hours, are judged on the same scale.
//   - Baseline: a busy store's uncongested latency can't be read off its
//     answers, and a long average or recent minimum of them creeps up with
//     the congestion it should measure (TCP Vegas's persistent-congestion
//     problem): latecomers take the queue for the baseline, and everyone
//     climbs to the ceiling. So every client steps back at the same moments,
//     as BBR's ProbeRTT does: for the first eight seconds of each minute by
//     the wall clock, a lull, it gives up half the queries it has in flight,
//     at most one. With every Kanivet on NTP time the store drains, and the
//     answers to queries sent from two seconds into the lull measure its
//     uncongested latency. The answers to the first and second half of that
//     span are averaged apart, each over its last 32, however many lulls
//     that takes, so a slow class that gets two a lull is as sure of its
//     baseline as a fast one, only later; a class's first answers seed it
//     until a lull measures it. The baseline is the two halves pooled, but
//     when they disagree by more than their noise explains, the store didn't
//     stay drained: a Kanivet whose clock is off came back while this one
//     was still measuring, or load that never steps back, a dashboard,
//     refilled it. The lower half is then the baseline. Limits hold still
//     through a lull.
//   - Below one query in flight the limit paces: one query, then a pause, so
//     many users on a small store can together send less than one at a time
//     each.
//   - Retries: exponential backoff with full jitter, the store's Retry-After
//     when it gives one, and a budget of about one retry per ten requests so a
//     struggling store never sees a retry storm.
//   - Circuit breaker: overload in three congestion events (round trips) in
//     a row stops all queries to that cluster for a jittered minute or two;
//     then a single probe decides.
//   - Priority: a user opening a workload's evidence goes ahead of background
//     report queries, and is told the store is paused rather than kept
//     waiting out the pause.

const (
	// limitMin below 1 is a fractional window: one query at a time with a
	// pause after it, so many users on a small store can together send less
	// than one query at a time each.
	limitMin     = 0.1
	limitMax     = 6.0
	limitInitial = 2.0

	// smoothing is how far towards its target the limit moves per round trip
	// (Gradient2's smoothing, FAST's γ). A round trip is limit answers, or
	// one answer and its pause below one, so a paced user climbs back as fast
	// per second as a busy one.
	smoothing = 0.2
	// gradientMin keeps one slow answer from more than halving the target.
	gradientMin = 0.5
	// queueAlpha is the queue a client keeps at the store, in queries, up to
	// queueKnee slots, and above the knee it grows as √(limit/queueKnee). A
	// constant pulls users towards equal shares hardest (the additive part
	// of Chiu and Jain's argument); a little more for a big limit keeps a
	// lone user's report from idling cores. Users together keep about
	// queueAlpha each queued: more makes every query wait, and less makes a
	// share swing with every percent of baseline error, since the queue a
	// client reads is the small gap between two latencies.
	queueAlpha = 0.3
	queueKnee  = 3.0
	shortEWMA  = 1.0 / 8 // Jacobson's SRTT gain
	// baselineMinSamples is how many responses a class needs before its
	// latency is trusted, as a sign of congestion or of room to grow. Its
	// first answers seed the baseline until a lull measures it.
	baselineMinSamples = 8
	// baselineSamples is how many lull answers each half of the baseline
	// averages: a count, not a time, so a class answered twice a lull
	// averages as many as one answered fifty times, over more lulls.
	baselineSamples = 32
	// splitZ is how many standard errors apart the two halves of the lulls'
	// answers may be before the higher half is taken for a store that didn't
	// drain.
	splitZ = 2.0

	// lullEvery and lullFor place the lulls on the wall clock; lullDepth is
	// the share of its slots in use a client keeps through one, and
	// lullMaxCut caps the slots it gives up: a client's own queue is a
	// fraction of a query, so one slot drains its share, and a lone user on a
	// store it keeps busy idles it little. The answers measured are those to
	// queries sent from a quarter of the way through, once the queries sent
	// before the lull have cleared, and early enough for their class to
	// answer within it.
	lullEvery  = time.Minute
	lullFor    = 8 * time.Second
	lullDepth  = 0.5
	lullMaxCut = 1.0

	errorDecrease = 0.5

	breakerTrips = 3
	breakerMin   = 60 * time.Second
	breakerMax   = 120 * time.Second
	// interactiveWait is the longest pause a query someone is waiting on
	// sits out; past it the query fails with ErrStoreBusy at once, so the
	// UI can say why and when to try again.
	interactiveWait = 3 * time.Second

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
	class    string
	overload bool
	// failed is an error that isn't overload, such as a query the store
	// rejected: the store answered, but the latency measures nothing.
	failed     bool
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
	// lastOverloadCut tells a new congestion event from the rest of the one
	// already cut for.
	lastOverloadCut time.Time

	overloads   int
	pausedUntil time.Time
	probing     bool // half-open: one query decides whether to resume

	budget float64

	pauseMin, pauseMax time.Duration

	// nextAllowed paces queries while the limit is below one, by srtt, the
	// smoothed latency of answered queries.
	nextAllowed time.Time
	srtt        time.Duration
	// lulls is when the lulls are; inLull is whether the last answer came
	// during one, when effective() is less than the limit. Like the limit,
	// it changes only on an answer.
	lulls  lullSchedule
	inLull bool

	// Decision counts, for the log.
	grows, queueCuts, overloadCuts int
	maxRatio                       float64
}

func newLimiter(now func() time.Time, rnd func() float64) *limiter {
	return &limiter{now: now, rand: rnd, limit: limitInitial, lulls: lullSchedule{lullEvery, lullFor}, budget: retryBudgetMax / 2, pauseMin: breakerMin, pauseMax: breakerMax}
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
	return max(1, int(math.Floor(l.effective())))
}

// effective is the limit in force, less through a lull.
func (l *limiter) effective() float64 {
	if !l.inLull {
		return l.limit
	}
	if l.limit < 1 {
		return l.limit * lullDepth
	}
	// Half the slots in use, not half the limit: 1.9 is one slot, and
	// halving it to 0.95 would barely slow it.
	whole := math.Floor(l.limit)
	return math.Max(whole*lullDepth, whole-lullMaxCut)
}

// acquire waits for a slot. It fails fast with ErrStoreBusy while the breaker
// is open, unless the caller can afford to wait out the pause: a user waiting
// on the answer only sits out the last few seconds of one.
//
// throttle_sim_test.go replays acquire and controlled.do as events, since a
// simulation can't block: a change to either needs the same change there.
func (l *limiter) acquire(ctx context.Context) (err error) {
	p := priorityOf(ctx)
	// woken is set once wake() has picked this caller for a free slot. If it
	// gives up after that, cancelled or told the store is busy, it passes the
	// turn on, or the slot sits idle until the next release, which may never
	// come.
	woken := false
	defer func() {
		if err != nil && woken {
			l.mu.Lock()
			l.wake()
			l.mu.Unlock()
		}
	}()
	for {
		l.mu.Lock()
		if !l.pausedUntil.IsZero() && !l.now().Before(l.pausedUntil) {
			// The pause is over, whether or not anyone waited it out: one
			// query decides whether to resume.
			l.pausedUntil = time.Time{}
			l.probing = true
		}
		if wait := l.pausedUntil.Sub(l.now()); wait > 0 {
			l.mu.Unlock()
			if p == priorityInteractive && wait > interactiveWait {
				return ErrStoreBusy
			}
			if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
				return ErrStoreBusy
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
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
			// Gone from the queue means wake() picked us as we gave up.
			woken = !l.dropWaiter(p, ch)
			l.mu.Unlock()
			return ctx.Err()
		case <-ch:
			woken = true
		}
	}
}

// dropWaiter takes ch out of the queue; false means wake() already did.
func (l *limiter) dropWaiter(p priority, ch chan struct{}) bool {
	q := l.waiting[p]
	for i, c := range q {
		if c == ch {
			l.waiting[p] = append(q[:i], q[i+1:]...)
			return true
		}
	}
	return false
}

// abandon returns the slot of a query its caller gave up on. That says
// nothing about the store: no latency sample, no cut, and a half-open probe
// stays open for the next query to decide.
func (l *limiter) abandon() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight--
	l.wake()
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
	case o.overload && now.Sub(l.lastOverloadCut) <= o.latency:
		// Sent before the last overload cut: part of the congestion event
		// already cut for, as when every query in flight hits a full store
		// at once. It neither cuts again nor counts towards the breaker,
		// but the store's Retry-After still holds. Nor can it fail a
		// half-open probe: the probe went out after the pause, so after
		// the cut, and its own answer decides.
		if until := now.Add(o.retryAfter); o.retryAfter > 0 && until.After(l.pausedUntil) {
			l.pausedUntil = until
		}
	case o.overload:
		l.overloadCuts++
		l.overloads++
		l.limit = math.Max(limitMin, l.limit*errorDecrease)
		l.lastOverloadCut = now
		pause := time.Duration(0)
		if l.probing || l.overloads >= breakerTrips {
			pause = l.pauseMin + time.Duration(l.rand()*float64(l.pauseMax-l.pauseMin))
		}
		pause = max(pause, o.retryAfter)
		if pause > 0 {
			l.pausedUntil = now.Add(pause)
		}
		l.probing = false
	case o.failed:
		// The store answered, so the overload streak ends, but how long it
		// took to reject a query says nothing about load.
		l.overloads = 0
		l.probing = false
	default:
		l.overloads = 0
		l.probing = false
		if l.srtt == 0 {
			l.srtt = o.latency
		} else {
			l.srtt += (o.latency - l.srtt) / 8
		}
		if l.classes == nil {
			l.classes = map[string]*latencyStats{}
		}
		key := fmt.Sprintf("%s#%d", o.class, sizeBucket(int(o.work*1000)))
		st := l.classes[key]
		if st == nil {
			st = &latencyStats{}
			l.classes[key] = st
		}
		sent := now.Add(-o.latency)
		ratio, gradient, warm := st.observe(o.latency.Seconds(), sent, l.lulls)
		l.maxRatio = math.Max(l.maxRatio, ratio)
		if !warm {
			// Too little history to tell a fast answer from a slow one, so
			// it is no evidence for growth, nor for a cut.
			break
		}
		if l.lulls.in(sent) || l.lulls.in(now) {
			// The store was quieter than this client's limit makes it, by
			// design: no evidence either way.
			break
		}
		// FAST's update in Gradient2's form: the limit settles where its
		// own queries waiting at the store, inUse×(1 − gradient), number
		// queueSize. That queue also absorbs latency noise, which only ever
		// lowers the gradient.
		target := l.limit - inUse(l.limit)*(1-gradient)
		if usedFully {
			// Room shown by a client that didn't use its slots is no
			// evidence the store has any.
			target += queueSize(l.limit)
		}
		before := l.limit
		step := math.Min(1, smoothing/l.limit)
		l.limit = math.Max(limitMin, math.Min(limitMax, l.limit+step*(target-l.limit)))
		switch {
		case l.limit > before:
			l.grows++
		case l.limit < before:
			l.queueCuts++
		}
	}
	l.inLull = l.lulls.in(now)
	if eff := l.effective(); eff < 1 {
		// Pace by how long an answered query takes, not by this response:
		// a 429 comes back in milliseconds.
		rtt := l.srtt
		if rtt == 0 {
			rtt = o.latency
		}
		l.nextAllowed = now.Add(time.Duration(float64(rtt) * (1/eff - 1)))
	} else {
		l.nextAllowed = time.Time{}
	}
	l.wake()
}

// inUse is how many queries the limit keeps in flight, as the update counts
// them: the limit itself when paced, and above one the whole slots plus half
// the fraction. Counting none of the fraction is honest, but then two users
// holding one slot each on a store with room for less sit content while a
// third, paced, sees their full queue and yields; counting all of it leaves a
// user at 1.9 thinking it holds nearly two, stuck on one slot while others
// hold two.
func inUse(limit float64) float64 {
	if limit < 1 {
		return limit
	}
	whole := math.Floor(limit)
	return whole + (limit-whole)/2
}

// queueSize is how many of its own queries a client keeps waiting at the
// store when the gradient settles (FAST's α, Gradient2's queueSize).
func queueSize(limit float64) float64 {
	if limit <= queueKnee {
		return queueAlpha
	}
	return queueAlpha * math.Sqrt(limit/queueKnee)
}

// lullSchedule places a lull of length at the start of every period of the
// wall clock that every Kanivet shares.
type lullSchedule struct{ every, length time.Duration }

// offset is how far into its period t is.
func (s lullSchedule) offset(t time.Time) time.Duration {
	off := time.Duration(t.UnixNano() % int64(s.every))
	if off < 0 {
		off += s.every
	}
	return off
}

func (s lullSchedule) in(t time.Time) bool { return s.offset(t) < s.length }

// latencyStats tracks one query class and answer size: short, an EWMA of its
// latency now, and its latency on an uncongested store, as the lulls measure
// it.
type latencyStats struct {
	n     int
	short float64
	// seed is the mean of the class's first answers: its baseline until a
	// lull measures it.
	seed float64
	// halves is its latency to queries sent in each half of the lulls'
	// measured span (see lullHalf).
	halves [2]runningMean
}

// runningMean is the mean of about the last baselineSamples values: a plain
// mean until there are that many, then an EWMA of that gain. dev is the same
// average of each value's distance from the mean before it.
type runningMean struct {
	mean, dev float64
	n         int
}

func (m *runningMean) add(x float64) {
	m.n = min(m.n+1, baselineSamples)
	if m.n > 1 {
		m.dev += (math.Abs(x-m.mean) - m.dev) / float64(m.n)
	}
	m.mean += (x - m.mean) / float64(m.n)
}

// long is the class's uncongested latency: the seed until a lull measures
// it, then the two halves' means pooled, or the lower of them when they
// disagree by more than their noise explains.
func (s *latencyStats) long() float64 {
	a, b := s.halves[0], s.halves[1]
	switch {
	case a.n == 0 && b.n == 0:
		return s.seed
	case a.n == 0:
		return b.mean
	case b.n == 0:
		return a.mean
	}
	na, nb := float64(a.n), float64(b.n)
	sd := 1.25 * (a.dev*na + b.dev*nb) / (na + nb) // mean absolute deviation to standard deviation
	if math.Abs(a.mean-b.mean) > splitZ*sd*math.Sqrt(1/na+1/nb) {
		return math.Min(a.mean, b.mean)
	}
	return (a.mean*na + b.mean*nb) / (na + nb)
}

// observe records a latency and returns short/long and the gradient,
// long/short within [gradientMin, 1]. Until the class has enough history to
// judge, warm is false.
func (s *latencyStats) observe(latency float64, sent time.Time, lulls lullSchedule) (ratio, gradient float64, warm bool) {
	s.n++
	if s.n == 1 {
		s.short = latency
	} else {
		s.short += shortEWMA * (latency - s.short)
	}
	measured := s.halves[0].n+s.halves[1].n > 0
	if s.n <= baselineMinSamples {
		s.seed += (latency - s.seed) / float64(s.n)
	} else if h, ok := s.lullHalf(lulls.offset(sent), lulls.length); ok {
		s.halves[h].add(latency)
	} else if !measured && s.seed > 2*s.short {
		// Gradient2's guard: latency at half the seed means it was taken in
		// congestion, so pull it down without waiting for a lull. Once a
		// lull has measured the baseline it stays: in a class whose latency
		// spreads widely, a run of fast answers would only ratchet it down.
		s.seed *= 0.95
	}
	if s.n < baselineMinSamples {
		return 1, 1, false
	}
	long := s.long()
	ratio = s.short / math.Max(long, 1e-9)
	gradient = math.Max(gradientMin, math.Min(1, long/math.Max(s.short, 1e-9)))
	return ratio, gradient, true
}

// lullHalf says in which half of the lull's measured span a query sent off
// into the lull falls, if in either. The span starts a quarter of the way in,
// once the queries sent before the lull have mostly cleared, and ends early
// enough for the class to answer within the lull. A class too slow for that
// is still measured to three quarters of the way, a little high, or a
// baseline seeded during congestion would never come down.
func (s *latencyStats) lullHalf(off, length time.Duration) (int, bool) {
	from := length / 4
	until := max(length-time.Duration(s.long()*float64(time.Second)), length*3/4)
	switch {
	case off < from || off >= until:
		return 0, false
	case off < (from+until)/2:
		return 0, true
	}
	return 1, true
}

// sizeBucket groups answers by size on a log scale: latency is a fixed
// overhead plus a cost per sample, so only answers of similar size compare.
func sizeBucket(samples int) int {
	// Floor, not truncation toward zero, or bucket 0 spans 4x where the
	// others span 2x.
	return int(math.Floor(math.Log2(float64(max(samples, 1)) / 1000 * 4)))
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
	if errors.Is(err, io.ErrUnexpectedEOF) || metrics.IsConnReset(err) || strings.Contains(err.Error(), "unexpected EOF") {
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
		if err != nil && ctx.Err() != nil {
			// Our caller cancelled or ran out of time, which says nothing
			// about the store. An http.Client timeout leaves ctx alone, so
			// it still counts as overload.
			lim.abandon()
			return nil, err
		}
		over, retryAfter := false, time.Duration(0)
		if err != nil {
			over, retryAfter = overloaded(err)
		}
		samples := 0
		for _, s := range res {
			samples += len(s.Times)
		}
		lim.release(outcome{latency: time.Since(start), work: float64(samples) / 1000, class: class, overload: over, failed: err != nil && !over, retryAfter: retryAfter})
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
