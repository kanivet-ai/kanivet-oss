package rightsizing

// A discrete-event simulation of the metrics-store limiter. Simulated Kanivet
// users, each with its own real limiter, share one simulated store. Time is
// the simulation's, on one goroutine, so a run is deterministic per seed and
// hours of store time take about a second. It measures what the limiter is
// for: a fair share of the store for every user, little queueing at the
// store, few overload answers and pauses, and fast reports when there is room.
//
// It is a harness for comparing limiter designs, not a regression test: it
// prints tables, and fails only when the simulation itself goes wrong (see
// simClient.check). It is skipped unless KANIVET_AIMD_SIM=1:
//
//	KANIVET_AIMD_SIM=1 go test -p 1 -count=1 -run TestThrottleSim -v ./internal/rightsizing/
//
// KANIVET_AIMD_SCEN is a regular expression that must match a whole scenario
// ID (S1-S13, U1-U6, R1-R9, E1-E3), e.g. 'S(6|8)|R[4-6]'. KANIVET_AIMD_SEEDS
// sets the seeds per scenario (default 15), and KANIVET_AIMD_SEED0 shifts
// them: seeds SEED0+1 to SEED0+SEEDS.
//
// Each seed starts the store's clock at its own time of day: Unix 1,000,000 s
// plus an offset drawn from the seed, uniform over 24 hours. A limiter that
// schedules anything by the wall clock is so measured at many phases of its
// schedule rather than one. KANIVET_AIMD_EPOCH=n starts every seed at Unix
// 1,000,000+n s instead (0 is the fixed start of earlier versions); "rand"
// is the default.
//
// Seeds run GOMAXPROCS at a time; the tables don't depend on it. The full
// suite takes about a minute on one core (GOMAXPROCS=1).
//
// The model:
//   - The store is processor sharing over cap cores, at most one core per
//     query. With reject > 0 it answers 429 at once while reject×cap queries
//     run (a query-frontend queue limit); with timeout > 0 a query that runs
//     that long fails (Prometheus' query timeout, a 503). Both are overload
//     to the limiter. It can also refuse a share of queries as bad (a 422),
//     whatever its load. It sends no Retry-After.
//   - Each user has an RTT, half each way. Latency is timed around the whole
//     call, as controlled.do times it.
//   - Each user's clock may be off the store's by a fixed skew: its limiter's
//     now() is the simulation's clock plus the skew (S12).
//   - Besides the Kanivet users there may be load the limiter doesn't
//     control: a source that keeps a fixed number of queries at the store,
//     sending the next the moment one is answered or refused, whatever the
//     store says (S13).
//   - Users run closed loops of workers (S, U), or reports: bursts of queries
//     under a computeTimeout deadline, idle in between (R, E). E users also
//     open evidence: a few interactive queries at once.
//   - The limiter is the real one, newLimiter on the simulation's clock, and
//     release, abandon, wake, dropWaiter, slots, allowRetry and backoff are
//     the real functions. Only acquire and controlled.do are mirrored,
//     because they block; see simAcquire and simClient.do.
//
// The metrics, counted after the warmup, as the mean over seeds ± the
// half-width of its 95% confidence interval (Student's t, n-1 degrees of
// freedom, so the interval is for the mean, not the spread of seeds):
//   - jain(busy), jain(ok): Jain's fairness index over Kanivet users of store
//     time and of queries answered; 1 is equal shares, 1/n one user with all.
//     Load the limiter doesn't control is not in it.
//   - util: store core-seconds used, by anyone, over those there were.
//   - queue p50/p99: queueing delay at the store, its time there less the
//     query's own demand, in seconds; stretch: its time there over demand.
//     Every query the store answered counts, uncontrolled load's too.
//   - overload %: 429s and timeouts among the store's answers, to anyone.
//   - paused %: user-time with the breaker open. limit: the mean limit.
//   - conv: seconds from the last user's start until every minute's
//     jain(busy) stays at least 0.9, "never" when the last minute's isn't.
//     (k/n): k of n seeds converged, and the mean is over theirs.
//   - post-drop ovl %, recover: after a capacity drop, the overload share
//     from a minute after it, and the seconds until every 30 seconds' share
//     stays under 5%. (k/n): k of n seeds recovered, and the mean is theirs.
//   - bg share, bg ovl %: the uncontrolled source's share of store time, and
//     the overload share of its answers.
//   - report s, report p90 s, reports, late %, failed q %: report times, how
//     many, cut off by the deadline, and queries that failed after retries.
//   - evidence p50/p90 s, evidence failed %: time to answer an evidence open,
//     and opens with a failed query, such as ErrStoreBusy.

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- Event loop ----

type simEvent struct {
	at  time.Time
	seq uint64 // events due at the same time run in the order scheduled
	fn  func()
}

type simEvents []*simEvent

func (q simEvents) Len() int { return len(q) }
func (q simEvents) Less(i, j int) bool {
	if !q[i].at.Equal(q[j].at) {
		return q[i].at.Before(q[j].at)
	}
	return q[i].seq < q[j].seq
}
func (q simEvents) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *simEvents) Push(x any)   { *q = append(*q, x.(*simEvent)) }
func (q *simEvents) Pop() any {
	old := *q
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return e
}

// simLoop is the simulation's clock and its pending events.
type simLoop struct {
	now    time.Time
	events simEvents
	seq    uint64
}

func (s *simLoop) at(t time.Time, fn func()) {
	if t.Before(s.now) {
		t = s.now
	}
	s.seq++
	heap.Push(&s.events, &simEvent{at: t, seq: s.seq, fn: fn})
}

func (s *simLoop) after(d time.Duration, fn func()) { s.at(s.now.Add(d), fn) }

// run runs every event due up to until, then sets the clock to until.
func (s *simLoop) run(until time.Time) {
	for len(s.events) > 0 && !s.events[0].at.After(until) {
		e := heap.Pop(&s.events).(*simEvent)
		s.now = e.at
		e.fn()
	}
	s.now = until
}

func simSeconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// ---- Store ----

// simAnswer is how the store answered a query.
type simAnswer int

const (
	simOK         simAnswer = iota
	simOverloaded           // 429 or query timeout: overloaded() says true
	simRefused              // a 4xx for the query itself: overloaded() says false
)

var (
	errSimOverloaded = errors.New("simulated store: overloaded")
	errSimRefused    = errors.New("simulated store: bad query")
)

func (a simAnswer) err() error {
	switch a {
	case simOverloaded:
		return errSimOverloaded
	case simRefused:
		return errSimRefused
	}
	return nil
}

// simRefuseLatency is how long the store takes to refuse a query.
const simRefuseLatency = 20 * time.Millisecond

// simQuery is one query as the store sees it.
type simQuery struct {
	demand  float64 // seconds of one store core
	samples int     // size of the answer
	class   string
	bad     bool // refused as malformed or too large
}

// simQueries draws a user's queries.
type simQueries func(r *rand.Rand) simQuery

// simLognormal costs demand times a lognormal factor of mean 1 and log-sd sigma.
func simLognormal(demand, sigma float64, samples int) simQueries {
	return func(r *rand.Rand) simQuery {
		f := 1.0
		if sigma > 0 {
			f = math.Exp(r.NormFloat64()*sigma - sigma*sigma/2)
		}
		return simQuery{demand: demand * f, samples: samples, class: "q"}
	}
}

type simJob struct {
	rem, demand float64
	client      int
	arrive      time.Time
	done        func(simAnswer)
	finished    bool
}

// simStore is a processor-sharing metrics store.
type simStore struct {
	sim     *simLoop
	cap     float64       // cores
	reject  float64       // 429 while reject×cap queries run (0: never)
	timeout time.Duration // a query running this long fails (0: never)
	jobs    []*simJob
	last    time.Time // the jobs have run up to here
	ver     int       // invalidates the scheduled completion

	busy       float64   // core-seconds served
	clientBusy []float64 // core-seconds served per user
	// Answers so far.
	served, rejected, timeouts, refused, cancelled int
	// Per query answered from recordFrom on: queueing delay at the store
	// (latency there minus demand), and stretch (latency over demand).
	recordFrom      time.Time
	qdelay, stretch []float64
}

func (p *simStore) advance() {
	dt := p.sim.now.Sub(p.last).Seconds()
	p.last = p.sim.now
	n := len(p.jobs)
	if n == 0 || dt <= 0 {
		return
	}
	rate := math.Min(1, p.cap/float64(n))
	for _, j := range p.jobs {
		j.rem -= dt * rate
		p.clientBusy[j.client] += dt * rate
	}
	p.busy += dt * math.Min(float64(n), p.cap)
}

// reschedule plans the next completion; call it after advance and any change
// to the jobs or the cores.
func (p *simStore) reschedule() {
	p.ver++
	v := p.ver
	if len(p.jobs) == 0 {
		return
	}
	rate := math.Min(1, p.cap/float64(len(p.jobs)))
	m := math.Inf(1)
	for _, j := range p.jobs {
		m = math.Min(m, j.rem)
	}
	p.sim.after(simSeconds(math.Max(0, m/rate))+time.Nanosecond, func() {
		if p.ver != v {
			return
		}
		p.advance()
		p.complete()
		p.reschedule()
	})
}

func (p *simStore) complete() {
	var fin []*simJob
	p.jobs = slices.DeleteFunc(p.jobs, func(j *simJob) bool {
		if j.rem <= 1e-6 {
			fin = append(fin, j)
			return true
		}
		return false
	})
	for _, j := range fin {
		j.finished = true
		p.served++
		if lat := p.sim.now.Sub(j.arrive).Seconds(); !p.sim.now.Before(p.recordFrom) {
			p.qdelay = append(p.qdelay, math.Max(0, lat-j.demand))
			p.stretch = append(p.stretch, lat/j.demand)
		}
		j.done(simOK)
	}
}

// submit starts a query; the job is nil when the store answers at once.
func (p *simStore) submit(client int, q simQuery, done func(simAnswer)) *simJob {
	p.advance()
	if q.bad {
		p.refused++
		p.sim.after(simRefuseLatency, func() { done(simRefused) })
		return nil
	}
	if p.reject > 0 && len(p.jobs) >= max(1, int(math.Round(p.reject*p.cap))) {
		p.rejected++
		p.sim.after(simRefuseLatency, func() { done(simOverloaded) })
		return nil
	}
	j := &simJob{rem: q.demand, demand: q.demand, client: client, arrive: p.sim.now, done: done}
	p.jobs = append(p.jobs, j)
	if p.timeout > 0 {
		p.sim.after(p.timeout, func() {
			if j.finished {
				return
			}
			p.remove(j)
			p.timeouts++
			done(simOverloaded)
		})
	}
	p.reschedule()
	return j
}

// cancel drops a query whose caller hung up: Prometheus stops evaluating a
// query when its request's context ends.
func (p *simStore) cancel(j *simJob) {
	if j == nil || j.finished {
		return
	}
	p.remove(j)
	p.cancelled++
}

func (p *simStore) remove(j *simJob) {
	p.advance()
	p.jobs = slices.DeleteFunc(p.jobs, func(x *simJob) bool { return x == j })
	j.finished = true
	p.reschedule()
}

// ---- Users ----

type simReportStat struct {
	start           time.Time
	took            float64 // s
	late            bool    // cut off by the deadline
	queries, failed int
}

type simOpenStat struct {
	start  time.Time
	took   float64 // s
	failed bool    // some query failed, e.g. ErrStoreBusy
}

// simClient is one Kanivet user with its limiter.
type simClient struct {
	id    int
	sc    *simScenario
	sim   *simLoop
	store *simStore
	lim   *limiter
	rtt   time.Duration
	skew  time.Duration // the user's clock less the store's
	query simQueries
	// Each kind of draw has its own stream, so that, say, a limiter that
	// draws more pause jitter doesn't change the queries anyone sends: two
	// limiter designs face the same workload (common random numbers).
	rq, rb, rs *rand.Rand // queries; retry backoff; the user's schedule

	queued  []*simAcquire // in lim.waiting, in queue order
	inCall  int           // queries holding a slot: lim.inflight must agree
	stalled []*simAcquire // queued at the last sample while a slot was free

	ok, failed int // queries answered, and failed after any retries
	reports    []simReportStat
	opens      []simOpenStat
}

// simAcquire is one call of limiter.acquire. The simulation runs on one
// goroutine and cannot block in a select, so each wait in acquire becomes an
// event and the loop body runs again when it fires.
//
// It mirrors acquire statement by statement and must be kept in step with
// it: any change to acquire's pause, probe, pacing, priority, queueing or
// hand-off logic needs the same change here, or the simulation measures a
// limiter that doesn't exist. A context is modelled by its deadline only,
// which is on the simulation's clock; l.now() is the user's (simClient.now).
type simAcquire struct {
	c        *simClient
	p        priority
	deadline time.Time // zero: none
	done     func(error)
	woken    bool
	ch       chan struct{} // while queued in lim.waiting
	gen      int           // bumped when woken, so its deadline timer is moot
}

// now is the user's clock, which its limiter reads.
func (c *simClient) now() time.Time { return c.sim.now.Add(c.skew) }

func (c *simClient) acquire(p priority, deadline time.Time, done func(error)) {
	a := &simAcquire{c: c, p: p, deadline: deadline, done: done}
	// A new call runs after whatever else is due now, as a new goroutine
	// would: a waiter that release just woke runs first.
	c.sim.after(0, a.loop)
}

// loop is one pass of acquire's for loop.
func (a *simAcquire) loop() {
	l, now := a.c.lim, a.c.now()
	l.mu.Lock()
	if !l.pausedUntil.IsZero() && !now.Before(l.pausedUntil) {
		l.pausedUntil = time.Time{}
		l.probing = true
	}
	if wait := l.pausedUntil.Sub(now); wait > 0 {
		l.mu.Unlock()
		if a.p == priorityInteractive && wait > interactiveWait {
			a.finish(ErrStoreBusy)
			return
		}
		if !a.deadline.IsZero() && a.deadline.Sub(a.c.sim.now) < wait {
			a.finish(ErrStoreBusy)
			return
		}
		a.sleep(wait)
		return
	}
	if gap := l.nextAllowed.Sub(now); gap > 0 && l.inflight == 0 {
		l.mu.Unlock()
		a.sleep(gap)
		return
	}
	if l.inflight < l.slots() && (a.p == priorityInteractive || len(l.waiting[priorityInteractive]) == 0) {
		l.inflight++
		l.budget = math.Min(retryBudgetMax, l.budget+retryBudgetPerRequest)
		l.mu.Unlock()
		a.finish(nil)
		return
	}
	a.ch = make(chan struct{})
	l.waiting[a.p] = append(l.waiting[a.p], a.ch)
	l.mu.Unlock()
	a.c.queued = append(a.c.queued, a)
	if a.deadline.IsZero() {
		return
	}
	gen := a.gen
	a.c.sim.at(a.deadline, func() {
		if a.gen != gen {
			return // woken first
		}
		l.mu.Lock()
		a.woken = !l.dropWaiter(a.p, a.ch)
		l.mu.Unlock()
		a.c.queued = slices.DeleteFunc(a.c.queued, func(x *simAcquire) bool { return x == a })
		a.finish(context.DeadlineExceeded)
	})
}

// sleep is acquire's select on ctx.Done() and time.After(d).
func (a *simAcquire) sleep(d time.Duration) {
	if !a.deadline.IsZero() && a.deadline.Before(a.c.sim.now.Add(d)) {
		a.c.sim.at(a.deadline, func() { a.finish(context.DeadlineExceeded) })
		return
	}
	a.c.sim.after(d, a.loop)
}

// finish returns from acquire, running its deferred hand-off.
func (a *simAcquire) finish(err error) {
	if err != nil && a.woken {
		l := a.c.lim
		l.mu.Lock()
		l.wake()
		l.mu.Unlock()
		a.c.dispatch()
	}
	a.done(err)
}

// dispatch resumes the waiters wake() picked, whose channels it closed: each
// goes round acquire's loop again, interactive ones first as wake() picks
// them. Call it after anything that may call wake().
func (c *simClient) dispatch() {
	var woke []*simAcquire
	c.queued = slices.DeleteFunc(c.queued, func(a *simAcquire) bool {
		select {
		case <-a.ch:
			woke = append(woke, a)
			return true
		default:
			return false
		}
	})
	if n := len(c.lim.waiting[0]) + len(c.lim.waiting[1]); n != len(c.queued) {
		panic(fmt.Sprintf("simulation out of step with the limiter: %d waiters, %d queued", n, len(c.queued)))
	}
	slices.SortStableFunc(woke, func(x, y *simAcquire) int { return int(y.p) - int(x.p) })
	for _, a := range woke {
		a.woken = true
		a.gen++
		a.ch = nil
		c.sim.after(0, a.loop)
	}
}

// do mirrors controlled.do and must be kept in step with it: acquire, the
// call, release with the outcome or abandon when the caller gave up, and
// retries with backoff while the budget allows.
func (c *simClient) do(p priority, deadline time.Time, q simQuery, done func(error)) {
	c.try(p, deadline, q, 0, done)
}

func (c *simClient) try(p priority, deadline time.Time, q simQuery, attempt int, done func(error)) {
	ctxDone := func() bool { return !deadline.IsZero() && !c.sim.now.Before(deadline) }
	c.acquire(p, deadline, func(err error) {
		if err != nil {
			done(err)
			return
		}
		c.inCall++
		start := c.sim.now
		c.call(deadline, q, func(ans simAnswer, err error) {
			l := c.lim
			c.inCall--
			if err != nil && ctxDone() {
				l.abandon()
				c.dispatch()
				done(err)
				return
			}
			over := ans == simOverloaded
			samples := 0
			if err == nil {
				samples = q.samples
			}
			l.release(outcome{latency: c.sim.now.Sub(start), work: float64(samples) / 1000, class: q.class, overload: over, failed: err != nil && !over})
			c.dispatch()
			if err == nil || !over || attempt+1 >= maxAttempts || !l.allowRetry() {
				done(err)
				return
			}
			wait := backoff(attempt, c.rb.Float64)
			if !deadline.IsZero() && deadline.Before(c.sim.now.Add(wait)) {
				c.sim.at(deadline, func() { done(err) })
				return
			}
			c.sim.after(wait, func() { c.try(p, deadline, q, attempt+1, done) })
		})
	})
}

// call is the query's HTTP round trip: half the RTT there, the store, half
// back. Like a request on an http.Client it fails with the context's error
// once the deadline passes, and the store then drops the query.
func (c *simClient) call(deadline time.Time, q simQuery, done func(simAnswer, error)) {
	if !deadline.IsZero() && !c.sim.now.Before(deadline) {
		done(simOK, context.DeadlineExceeded)
		return
	}
	ended := false
	var job *simJob
	end := func(ans simAnswer, err error) {
		if !ended {
			ended = true
			done(ans, err)
		}
	}
	if !deadline.IsZero() {
		c.sim.at(deadline, func() {
			if !ended {
				c.store.cancel(job)
			}
			end(simOK, context.DeadlineExceeded)
		})
	}
	c.sim.after(c.rtt/2, func() {
		if ended {
			return
		}
		job = c.store.submit(c.id, q, func(ans simAnswer) {
			c.sim.after(c.rtt/2, func() { end(ans, ans.err()) })
		})
	})
}

// check panics when the simulation and the limiter disagree, or a waiter has
// been left queued since the last sample with a slot free and no pause or
// pacing to wait out: a lost wake-up, in the limiter or in the mirror.
func (c *simClient) check() {
	l, now := c.lim, c.now()
	if l.inflight != c.inCall {
		panic(fmt.Sprintf("user %d at %v: limiter has %d in flight, simulation %d", c.id, now, l.inflight, c.inCall))
	}
	held := l.pausedUntil.After(now) || (l.nextAllowed.After(now) && l.inflight == 0) || l.inflight >= l.slots()
	var stalled []*simAcquire
	if !held {
		stalled = slices.Clone(c.queued)
	}
	for _, a := range stalled {
		if slices.Contains(c.stalled, a) {
			panic(fmt.Sprintf("user %d at %v: a waiter stayed queued with %d of %d slots in use", c.id, now, l.inflight, l.slots()))
		}
	}
	c.stalled = stalled
}

func (c *simClient) tally(err error) {
	if err == nil {
		c.ok++
	} else {
		c.failed++
	}
}

func (c *simClient) between(r [2]time.Duration) time.Duration {
	return r[0] + time.Duration(c.rs.Float64()*float64(r[1]-r[0]))
}

// worker is one worker of a closed loop: a background query, then the next.
func (c *simClient) worker() {
	c.do(priorityBackground, time.Time{}, c.query(c.rq), func(err error) {
		c.tally(err)
		c.worker()
	})
}

type simReport struct {
	start, deadline   time.Time
	remaining, active int
	stat              simReportStat
}

// startReport sends a report's queries, workers at a time, under the
// report's deadline; once it ends the user is idle for a while.
func (c *simClient) startReport() {
	now := c.sim.now
	deadline := c.sc.deadline
	if deadline == 0 {
		deadline = computeTimeout
	}
	r := &simReport{start: now, deadline: now.Add(deadline), remaining: c.sc.report}
	for r.remaining > 0 && r.active < c.sc.workers {
		c.reportQuery(r)
	}
}

func (c *simClient) reportQuery(r *simReport) {
	r.remaining--
	r.active++
	c.do(priorityBackground, r.deadline, c.query(c.rq), func(err error) {
		c.tally(err)
		r.active--
		r.stat.queries++
		if err != nil {
			r.stat.failed++
		}
		now := c.sim.now
		if r.remaining > 0 && now.Before(r.deadline) {
			c.reportQuery(r)
			return
		}
		if r.active > 0 {
			return
		}
		r.stat.start, r.stat.took, r.stat.late = r.start, now.Sub(r.start).Seconds(), !now.Before(r.deadline)
		c.reports = append(c.reports, r.stat)
		c.sim.after(c.between(c.sc.idle), c.startReport)
	})
}

// openEvidence asks a workload's evidence: interactive queries, all at once.
func (c *simClient) openEvidence() {
	start := c.sim.now
	left, failed := c.sc.evidence, false
	for range c.sc.evidence {
		c.do(priorityInteractive, time.Time{}, c.sc.evidenceQuery(c.rq), func(err error) {
			c.tally(err)
			failed = failed || err != nil
			if left--; left > 0 {
				return
			}
			c.opens = append(c.opens, simOpenStat{start: start, took: c.sim.now.Sub(start).Seconds(), failed: failed})
			c.sim.after(c.between(c.sc.evidenceEvery), c.openEvidence)
		})
	}
}

// simBackground is load no limiter controls, such as another tool or a
// dashboard on the same store: it keeps n queries at the store, sending the
// next the moment one is answered or refused, with no backoff and no lulls.
type simBackground struct {
	id    int // its index in the store's per-client tallies
	sim   *simLoop
	store *simStore
	query simQueries
	rq    *rand.Rand

	ok, overloaded int // answers, and 429s or timeouts among them
}

func (b *simBackground) send() {
	b.store.submit(b.id, b.query(b.rq), func(ans simAnswer) {
		if ans == simOverloaded {
			b.overloaded++
		} else {
			b.ok++
		}
		// The next goes out as a new event, not from inside the store's.
		b.sim.after(0, b.send)
	})
}

// ---- Scenarios ----

type simScenario struct {
	id, name string
	clients  int
	// workers is how many queries a user keeps going: the closed loop's
	// size, or how many of a report's queries it sends at once.
	workers  int
	cap      float64
	capAfter float64 // the store's cores from dropAt on (0: no drop)
	dropAt   time.Duration
	reject   float64
	timeout  time.Duration
	stagger  time.Duration   // user i starts at i×stagger
	rtts     []time.Duration // per user, cycled
	skews    []time.Duration // per user, cycled: its clock less the store's
	queries  []simQueries    // per user, cycled
	dur      time.Duration
	warmup   time.Duration // metrics count from here

	// background > 0: a source no limiter controls also keeps this many
	// queries at the store, drawn from queries[0] (see simBackground).
	background int

	// report > 0: each user runs reports of this many queries, idle for a
	// time drawn from idle in between, instead of a closed loop. A report
	// ends at its deadline (0: computeTimeout) whatever is left.
	report   int
	idle     [2]time.Duration
	deadline time.Duration

	// evidence > 0: each user also opens evidence, this many interactive
	// queries at once, every evidenceEvery.
	evidence      int
	evidenceEvery [2]time.Duration
	evidenceQuery simQueries
}

// simDur prints a duration without zero trailing units: 2m, 3h, 40ms.
func simDur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// shape describes the scenario's setup for the table's legend.
func (sc simScenario) shape() string {
	d := simDur
	users := fmt.Sprintf("%d users", sc.clients)
	if sc.clients == 1 {
		users = "1 user"
	}
	var b []string
	if sc.report > 0 {
		b = append(b, fmt.Sprintf("%s x %d-query reports, %d at once, idle %s-%s", users, sc.report, sc.workers, d(sc.idle[0]), d(sc.idle[1])))
	} else {
		b = append(b, fmt.Sprintf("%s x %d workers", users, sc.workers))
	}
	if sc.deadline > 0 {
		b = append(b, "report deadline "+d(sc.deadline))
	}
	if sc.evidence > 0 {
		b = append(b, fmt.Sprintf("evidence %d queries every %s-%s", sc.evidence, d(sc.evidenceEvery[0]), d(sc.evidenceEvery[1])))
	}
	if sc.capAfter > 0 {
		b = append(b, fmt.Sprintf("%g cores, %g from %s", sc.cap, sc.capAfter, d(sc.dropAt)))
	} else {
		b = append(b, fmt.Sprintf("%g cores", sc.cap))
	}
	if sc.reject > 0 {
		b = append(b, fmt.Sprintf("429 at %gx cores", sc.reject))
	}
	if sc.timeout > 0 {
		b = append(b, "timeout "+d(sc.timeout))
	}
	if sc.stagger > 0 {
		b = append(b, "starts "+d(sc.stagger)+" apart")
	}
	if len(sc.rtts) > 0 {
		var r []string
		for _, x := range sc.rtts {
			r = append(r, d(x))
		}
		b = append(b, "rtt "+strings.Join(r, "/"))
	}
	if len(sc.skews) > 0 {
		var r []string
		for _, x := range sc.skews {
			r = append(r, fmt.Sprintf("%+gs", x.Seconds()))
		}
		b = append(b, "user clocks "+strings.Join(r, "/")+" off the store's")
	}
	if sc.background > 0 {
		b = append(b, fmt.Sprintf("plus %d queries always in flight from an uncontrolled source", sc.background))
	}
	b = append(b, d(sc.dur)+", measured from "+d(sc.warmup))
	return strings.Join(b, ", ")
}

// simContention is the S set: users contending for a store, 3 users x 12
// workers on 4 cores with 429s at 2x unless said otherwise, 0.5s queries.
func simContention() []simScenario {
	q := func(sigma float64) []simQueries { return []simQueries{simLognormal(0.5, sigma, 4000)} }
	base := simScenario{clients: 3, workers: 12, cap: 4, reject: 2, dur: 30 * time.Minute, warmup: 5 * time.Minute, queries: q(0)}
	with := func(id, name string, f func(*simScenario)) simScenario {
		s := base
		s.id, s.name = id, name
		f(&s)
		return s
	}
	return []simScenario{
		with("S1", "equal start, deterministic service", func(*simScenario) {}),
		with("S2", "equal start, lognormal sigma 0.5", func(s *simScenario) { s.queries = q(0.5) }),
		with("S3", "staggered starts, sigma 0.3", func(s *simScenario) {
			s.stagger, s.queries = 120*time.Second, q(0.3)
		}),
		with("S4", "different RTTs, sigma 0.3", func(s *simScenario) {
			s.rtts, s.queries = []time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond}, q(0.3)
		}),
		with("S5", "different query sizes 0.1s/0.5s/2s, sigma 0.3", func(s *simScenario) {
			s.queries = []simQueries{simLognormal(0.1, 0.3, 800), simLognormal(0.5, 0.3, 4000), simLognormal(2, 0.3, 16000)}
		}),
		with("S6", "many users on a small store (fractional limits), sigma 0.3", func(s *simScenario) {
			s.clients, s.workers, s.cap, s.queries = 8, 8, 2, q(0.3)
		}),
		with("S7", "capacity drop, sigma 0.3", func(s *simScenario) {
			s.cap, s.capAfter, s.dropAt, s.warmup, s.queries = 6, 1.5, 10*time.Minute, 11*time.Minute, q(0.3)
		}),
		with("S8", "Prometheus-like (no 429, query timeout), sigma 0.3", func(s *simScenario) {
			s.clients, s.workers, s.reject, s.timeout, s.queries = 6, 20, 0, 120*time.Second, q(0.3)
		}),
		with("S9", "Prometheus-like (no 429, query timeout), sigma 0.1", func(s *simScenario) {
			s.clients, s.workers, s.reject, s.timeout, s.queries = 6, 20, 0, 120*time.Second, q(0.1)
		}),
		with("S10", "staggered starts, sigma 0.1", func(s *simScenario) {
			s.stagger, s.queries = 120*time.Second, q(0.1)
		}),
		with("S11", "many users on a small store (fractional limits), sigma 0.1", func(s *simScenario) {
			s.clients, s.workers, s.cap, s.queries = 8, 8, 2, q(0.1)
		}),
		// Users' clocks are never quite the store's or each other's: NTP
		// keeps them within tens of milliseconds, a laptop without it drifts
		// by seconds.
		with("S12", "clock skew, sigma 0.3", func(s *simScenario) {
			s.skews, s.queries = []time.Duration{0, 1500 * time.Millisecond, -3 * time.Second}, q(0.3)
		}),
		// Load that never backs off, a dashboard or another tool, takes what
		// it takes; the Kanivet users should share the rest fairly.
		with("S13", "unresponsive background load, sigma 0.3", func(s *simScenario) {
			s.background, s.queries = 2, q(0.3)
		}),
	}
}

// simUncontended is the U set: one user alone on a store with room to
// spare. The limit should climb to its ceiling: healthy latency, however
// spread, must not read as queueing.
func simUncontended() []simScenario {
	base := simScenario{clients: 1, workers: 30, cap: 32, dur: 20 * time.Minute, warmup: 2 * time.Minute}
	var out []simScenario
	for i, sigma := range []float64{0, 0.3, 0.5, 0.8} {
		s := base
		s.id, s.name = fmt.Sprintf("U%d", i+1), fmt.Sprintf("0.5s queries, lognormal sigma %.1f", sigma)
		s.queries = []simQueries{simLognormal(0.5, sigma, 4000)}
		out = append(out, s)
	}
	s := base
	s.id, s.name = "U5", "30% results-cache hits at 1/20 the cost, sigma 0.2"
	s.queries = []simQueries{func(r *rand.Rand) simQuery {
		q := simLognormal(0.5, 0.2, 4000)(r)
		if r.Float64() < 0.3 {
			q.demand /= 20
		}
		return q
	}}
	out = append(out, s)
	s = base
	s.id, s.name = "U6", "answer sizes across one size bucket (2,000-3,999 samples), cost 0.1s + 0.2s per 1,000, sigma 0.1"
	s.queries = []simQueries{func(r *rand.Rand) simQuery {
		n := 2000 + r.IntN(2000)
		return simLognormal(0.1+0.2*float64(n)/1000, 0.1, n)(r)
	}}
	return append(out, s)
}

// simReportBase is bursty, realistic use: each user runs a report of 200
// queries, 40 at once, then the store is quiet until their next one.
func simReportBase(users int, sigma float64) simScenario {
	return simScenario{clients: users, workers: 40, cap: 4, reject: 2, dur: 3 * time.Hour, warmup: 20 * time.Minute,
		report: 200, idle: [2]time.Duration{time.Minute, 5 * time.Minute}, rtts: []time.Duration{40 * time.Millisecond},
		queries: []simQueries{simLognormal(0.5, sigma, 4000)}}
}

// simReports is the R set: reports for 1, 3 and 8 users at three latency
// spreads.
func simReports() []simScenario {
	var out []simScenario
	for _, sigma := range []float64{0.1, 0.3, 0.6} {
		for _, users := range []int{1, 3, 8} {
			s := simReportBase(users, sigma)
			s.id, s.name = fmt.Sprintf("R%d", len(out)+1), fmt.Sprintf("reports, sigma %.1f", sigma)
			out = append(out, s)
		}
	}
	return out
}

// simEvidence is the E set: R at sigma 0.3 while users also open evidence,
// whose queries someone is waiting on. The store also refuses 2% of report
// queries outright (too many samples, say), answers that must neither feed
// the latency baseline nor count as overload.
func simEvidence() []simScenario {
	var out []simScenario
	for _, users := range []int{1, 3, 8} {
		s := simReportBase(users, 0.3)
		s.id, s.name = fmt.Sprintf("E%d", len(out)+1), "reports and evidence, 2% of report queries refused, sigma 0.3"
		report := s.queries[0]
		s.queries = []simQueries{func(r *rand.Rand) simQuery {
			q := report(r)
			q.bad = r.Float64() < 0.02
			return q
		}}
		s.evidence, s.evidenceEvery = 6, [2]time.Duration{time.Minute, 4 * time.Minute}
		ev := simLognormal(0.2, 0.3, 1500)
		s.evidenceQuery = func(r *rand.Rand) simQuery {
			q := ev(r)
			q.class = "evidence"
			return q
		}
		out = append(out, s)
	}
	return out
}

// ---- Running and measuring ----

type simSnap struct {
	t      time.Time
	busy   []float64 // per user: store core-seconds so far
	ok     []int     // per user: queries answered so far
	limits []float64
	paused int // users whose breaker is open
	// Store totals so far.
	over, served int
	storeBusy    float64
	// The uncontrolled source's store core-seconds and answers so far.
	bgBusy           float64
	bgOK, bgOverload int
}

// simResult is one seeded run: metrics by key (NaN: never happened, for
// conv and recover), and a line of per-user detail.
type simResult struct {
	m      map[string]float64
	detail string
}

// simRun runs a scenario with the store's clock starting at t0.
func simRun(sc simScenario, seed uint64, t0 time.Time) simResult {
	s := &simLoop{now: t0}
	warm := t0.Add(sc.warmup)
	// The uncontrolled source, if any, is client sc.clients at the store.
	st := &simStore{sim: s, cap: sc.cap, reject: sc.reject, timeout: sc.timeout, last: t0, clientBusy: make([]float64, sc.clients+1), recordFrom: warm}
	clients := make([]*simClient, sc.clients)
	for i := range clients {
		stream := func(k uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, uint64(i)*4+k)) }
		c := &simClient{id: i, sc: &sc, sim: s, store: st, query: sc.queries[i%len(sc.queries)], rq: stream(0), rb: stream(2), rs: stream(3)}
		if len(sc.skews) > 0 {
			c.skew = sc.skews[i%len(sc.skews)]
		}
		c.lim = newLimiter(c.now, stream(1).Float64)
		if len(sc.rtts) > 0 {
			c.rtt = sc.rtts[i%len(sc.rtts)]
		}
		clients[i] = c
		start := t0.Add(time.Duration(i) * sc.stagger)
		if sc.report > 0 {
			s.at(start.Add(time.Duration(c.rs.Float64()*float64(sc.idle[1]))), c.startReport)
		} else {
			s.at(start, func() {
				for range sc.workers {
					c.worker()
				}
			})
		}
		if sc.evidence > 0 {
			s.at(start.Add(c.between(sc.evidenceEvery)), c.openEvidence)
		}
	}
	bg := &simBackground{id: sc.clients, sim: s, store: st, query: sc.queries[0], rq: rand.New(rand.NewPCG(seed, uint64(sc.clients)*4))}
	if sc.background > 0 {
		s.at(t0, func() {
			for range sc.background {
				bg.send()
			}
		})
	}
	if sc.capAfter > 0 {
		s.at(t0.Add(sc.dropAt), func() {
			st.advance()
			st.cap = sc.capAfter
			st.reschedule()
		})
	}
	const tick = 5 * time.Second
	var snaps []simSnap
	var sample func()
	sample = func() {
		st.advance()
		sn := simSnap{t: s.now, busy: slices.Clone(st.clientBusy[:sc.clients]), over: st.rejected + st.timeouts, served: st.served, storeBusy: st.busy,
			bgBusy: st.clientBusy[sc.clients], bgOK: bg.ok, bgOverload: bg.overloaded}
		for _, c := range clients {
			c.check()
			ls := c.lim.state()
			sn.ok = append(sn.ok, c.ok)
			sn.limits = append(sn.limits, ls.Limit)
			if ls.PausedFor > 0 {
				sn.paused++
			}
		}
		snaps = append(snaps, sn)
		s.after(tick, sample)
	}
	s.at(t0, sample)
	s.run(t0.Add(sc.dur))

	m := map[string]float64{}
	wi := slices.IndexFunc(snaps, func(sn simSnap) bool { return !sn.t.Before(warm) })
	w, e := snaps[wi], snaps[len(snaps)-1]
	busy := make([]float64, sc.clients)
	oks := make([]float64, sc.clients)
	bgBusy := e.bgBusy - w.bgBusy
	total := bgBusy // all store time, for shares
	for i := range clients {
		busy[i] = e.busy[i] - w.busy[i]
		oks[i] = float64(e.ok[i] - w.ok[i])
		total += busy[i]
	}
	m["jainBusy"], m["jainOK"] = simJain(busy), simJain(oks)
	if sc.background > 0 {
		m["bgShare"] = bgBusy / math.Max(total, 1e-9)
		m["bgOvl"] = simShare(e.bgOverload-w.bgOverload, e.bgOK-w.bgOK)
	}
	cores := sc.cap
	if sc.capAfter > 0 {
		cores = sc.capAfter
	}
	m["util"] = (e.storeBusy - w.storeBusy) / (cores * (sc.dur - sc.warmup).Seconds())
	m["q50"], m["q99"] = simPct(st.qdelay, 0.5), simPct(st.qdelay, 0.99)
	m["st50"], m["st99"] = simPct(st.stretch, 0.5), simPct(st.stretch, 0.99)
	m["ovl"] = simShare(e.over-w.over, e.served-w.served)
	limits := make([]float64, sc.clients)
	pausedTicks := 0
	steady := snaps[wi:]
	for _, sn := range steady {
		for i, v := range sn.limits {
			limits[i] += v / float64(len(steady))
		}
		pausedTicks += sn.paused
	}
	m["limit"] = simMean(limits)
	m["paused"] = 100 * float64(pausedTicks) / float64(len(steady)*sc.clients)
	if sc.report == 0 && sc.clients > 1 {
		m["conv"] = simConvergence(snaps, t0.Add(time.Duration(sc.clients-1)*sc.stagger))
	}
	if sc.capAfter > 0 {
		drop := t0.Add(sc.dropAt)
		a := snaps[slices.IndexFunc(snaps, func(sn simSnap) bool { return !sn.t.Before(drop.Add(time.Minute)) })]
		m["postDrop"] = simShare(e.over-a.over, e.served-a.served)
		m["recover"] = simRecovery(snaps, drop)
	}
	if sc.report > 0 {
		var took []float64
		late, queries, failed := 0, 0, 0
		for _, c := range clients {
			for _, r := range c.reports {
				if r.start.Before(warm) {
					continue
				}
				took = append(took, r.took)
				queries += r.queries
				failed += r.failed
				if r.late {
					late++
				}
			}
		}
		m["reports"] = float64(len(took))
		m["repMean"], m["rep90"] = simMean(took), simPct(took, 0.9)
		m["late"] = 100 * float64(late) / math.Max(1, float64(len(took)))
		m["repFail"] = 100 * float64(failed) / math.Max(1, float64(queries))
	}
	if sc.evidence > 0 {
		var took []float64
		failed := 0
		for _, c := range clients {
			for _, o := range c.opens {
				if o.start.Before(warm) {
					continue
				}
				took = append(took, o.took)
				if o.failed {
					failed++
				}
			}
		}
		m["ev50"], m["ev90"] = simPct(took, 0.5), simPct(took, 0.9)
		m["evFail"] = 100 * float64(failed) / math.Max(1, float64(len(took)))
	}

	var per []string
	var grows, queueCuts, overloadCuts int
	var maxRatio float64
	for i, c := range clients {
		per = append(per, fmt.Sprintf("%.2f/%.2f", limits[i], busy[i]/math.Max(total, 1e-9)))
		ls := c.lim.state()
		grows, queueCuts, overloadCuts = grows+ls.Grows, queueCuts+ls.QueueCuts, overloadCuts+ls.OverloadCuts
		maxRatio = math.Max(maxRatio, ls.MaxRatio)
	}
	if sc.background > 0 {
		per = append(per, fmt.Sprintf("uncontrolled %d/%.2f", sc.background, bgBusy/math.Max(total, 1e-9)))
	}
	detail := fmt.Sprintf("limit/share %s; grows %d, queue cuts %d, overload cuts %d, max ratio %.1f",
		strings.Join(per, " "), grows, queueCuts, overloadCuts, maxRatio)
	return simResult{m: m, detail: detail}
}

// simConvergence is how long after the last user started the store's shares
// became fair and stayed so: from then on, every minute's Jain index of store
// time is at least 0.9. NaN if the last minute is unfair.
func simConvergence(snaps []simSnap, lastStart time.Time) float64 {
	const win = 12 // ticks: one minute
	conv := math.NaN()
	for i := len(snaps) - 1; i >= win && !snaps[i-win].t.Before(lastStart); i-- {
		d := make([]float64, len(snaps[i].busy))
		for k := range d {
			d[k] = snaps[i].busy[k] - snaps[i-win].busy[k]
		}
		if simJain(d) < 0.9 {
			break
		}
		conv = snaps[i-win].t.Sub(lastStart).Seconds()
	}
	return conv
}

// simRecovery is how long after a capacity drop the overload answers settled:
// from then on, every 30 seconds' overload share is under 5%. NaN if the last
// 30 seconds' isn't.
func simRecovery(snaps []simSnap, drop time.Time) float64 {
	const win = 6 // ticks: 30 seconds
	rec := math.NaN()
	for i := len(snaps) - 1; i >= win && !snaps[i-win].t.Before(drop); i-- {
		if simShare(snaps[i].over-snaps[i-win].over, snaps[i].served-snaps[i-win].served) >= 5 {
			break
		}
		rec = snaps[i-win].t.Sub(drop).Seconds()
	}
	return rec
}

// simShare is overload answers as a percentage of answers.
func simShare(over, served int) float64 {
	return 100 * float64(over) / math.Max(1, float64(over+served))
}

// simJain is Jain's fairness index: 1 when all are equal, 1/n when one has all.
func simJain(x []float64) float64 {
	var s, s2 float64
	for _, v := range x {
		s += v
		s2 += v * v
	}
	if s2 == 0 {
		return 1
	}
	return s * s / (float64(len(x)) * s2)
}

func simPct(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	ys := slices.Clone(xs)
	slices.Sort(ys)
	return ys[min(len(ys)-1, int(p*float64(len(ys))))]
}

func simMean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// ---- Tables ----

type simColumn struct {
	key, head string
	prec      int
}

// simCounted are the metrics that may never happen in a seed, such as a
// recovery: their cells always say in how many seeds they did.
var simCounted = map[string]bool{"conv": true, "recover": true}

var simContinuousColumns = []simColumn{
	{"jainBusy", "jain(busy)", 3}, {"jainOK", "jain(ok)", 3}, {"util", "util", 3},
	{"q50", "queue p50 s", 3}, {"q99", "queue p99 s", 3}, {"st50", "stretch p50", 2}, {"st99", "stretch p99", 2},
	{"ovl", "overload %", 2}, {"paused", "paused %", 1}, {"limit", "limit", 2}, {"conv", "conv s", 0},
	{"postDrop", "post-drop ovl %", 2}, {"recover", "recover s", 0},
	{"bgShare", "bg share", 3}, {"bgOvl", "bg ovl %", 2},
}

var simReportColumns = []simColumn{
	{"repMean", "report s", 1}, {"rep90", "report p90 s", 1}, {"reports", "reports", 0}, {"late", "late %", 1},
	{"repFail", "failed q %", 2}, {"ev50", "evidence p50 s", 2}, {"ev90", "evidence p90 s", 2}, {"evFail", "evidence failed %", 1},
	{"jainBusy", "jain(busy)", 3}, {"util", "util", 3}, {"q50", "queue p50 s", 3}, {"q99", "queue p99 s", 3},
	{"st50", "stretch p50", 2}, {"st99", "stretch p99", 2}, {"ovl", "overload %", 2}, {"paused", "paused %", 1}, {"limit", "limit", 2},
}

// simT975 is the 97.5th percentile of Student's t with df degrees of
// freedom: a 95% confidence interval for a mean of df+1 samples is the mean
// ± simT975(df)·sd/√(df+1). Exact to four places up to 30, then the
// Cornish-Fisher expansion about the normal's 1.96, good to 1e-4 there.
func simT975(df int) float64 {
	table := [...]float64{12.7062, 4.3027, 3.1824, 2.7764, 2.5706, 2.4469, 2.3646, 2.3060, 2.2622, 2.2281,
		2.2010, 2.1788, 2.1604, 2.1448, 2.1314, 2.1199, 2.1098, 2.1009, 2.0930, 2.0860,
		2.0796, 2.0739, 2.0687, 2.0639, 2.0595, 2.0555, 2.0518, 2.0484, 2.0452, 2.0423}
	if df < 1 {
		return math.NaN()
	}
	if df <= len(table) {
		return table[df-1]
	}
	z, n := 1.959964, float64(df)
	z3, z5, z7 := z*z*z, z*z*z*z*z, z*z*z*z*z*z*z
	return z + (z3+z)/(4*n) + (5*z5+16*z3+3*z)/(96*n*n) + (3*z7+19*z5+17*z3-15*z)/(384*n*n*n)
}

// simCell is the mean over seeds ± the half-width of its 95% confidence
// interval; one seed has no interval. NaN marks a seed where it never
// happened: those are left out and counted, always for simCounted metrics.
func simCell(xs []float64, c simColumn) string {
	var ok []float64
	for _, x := range xs {
		if !math.IsNaN(x) {
			ok = append(ok, x)
		}
	}
	count := ""
	if simCounted[c.key] || len(ok) < len(xs) {
		count = fmt.Sprintf(" (%d/%d)", len(ok), len(xs))
	}
	if len(ok) == 0 {
		return "never" + count
	}
	m := simMean(ok)
	if len(ok) == 1 {
		return fmt.Sprintf("%.*f", c.prec, m) + count
	}
	var v float64
	for _, x := range ok {
		v += (x - m) * (x - m)
	}
	n := float64(len(ok))
	half := simT975(len(ok)-1) * math.Sqrt(v/(n-1)) / math.Sqrt(n)
	return fmt.Sprintf("%.*f±%.*f", c.prec, m, c.prec, half) + count
}

// simConfig is what the environment asks of a run of the simulation.
type simConfig struct {
	scen         *regexp.Regexp
	seeds, seed0 int  // seeds seed0+1 to seed0+seeds
	fixed        bool // every seed starts at epoch, not at its own phase
	epoch        time.Duration
}

func simEnv(t *testing.T) simConfig {
	t.Helper()
	if os.Getenv("KANIVET_AIMD_SIM") != "1" {
		t.Skip("limiter simulation: set KANIVET_AIMD_SIM=1")
	}
	pat := os.Getenv("KANIVET_AIMD_SCEN")
	if pat == "" {
		pat = ".*"
	}
	var cfg simConfig
	var err error
	if cfg.scen, err = regexp.Compile("^(?:" + pat + ")$"); err != nil {
		t.Fatalf("KANIVET_AIMD_SCEN: %v", err)
	}
	cfg.seeds = 15
	if v := os.Getenv("KANIVET_AIMD_SEEDS"); v != "" {
		if cfg.seeds, err = strconv.Atoi(v); err != nil || cfg.seeds < 1 {
			t.Fatalf("KANIVET_AIMD_SEEDS=%q", v)
		}
	}
	if v := os.Getenv("KANIVET_AIMD_SEED0"); v != "" {
		if cfg.seed0, err = strconv.Atoi(v); err != nil || cfg.seed0 < 0 {
			t.Fatalf("KANIVET_AIMD_SEED0=%q", v)
		}
	}
	if v := os.Getenv("KANIVET_AIMD_EPOCH"); v != "" && v != "rand" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("KANIVET_AIMD_EPOCH=%q: seconds after Unix 1,000,000, or rand", v)
		}
		cfg.fixed, cfg.epoch = true, time.Duration(n)*time.Second
	}
	return cfg
}

// simPhaseStream is the random stream a seed's start time is drawn from,
// apart from the users' (seed, 4i+k) streams.
const simPhaseStream = 1 << 40

// start is when a seed's run starts on the store's clock.
func (cfg simConfig) start(seed uint64) time.Time {
	t := time.Unix(1_000_000, 0)
	if cfg.fixed {
		return t.Add(cfg.epoch)
	}
	day := int64(24 * time.Hour)
	return t.Add(time.Duration(rand.New(rand.NewPCG(seed, simPhaseStream)).Int64N(day)))
}

// simTable runs the scenarios KANIVET_AIMD_SCEN selects, each over the
// configured seeds, and logs a table of mean±CI per metric, with a legend and
// the first seed's per-user detail.
func simTable(t *testing.T, title string, scenarios []simScenario, columns []simColumn) {
	cfg := simEnv(t)
	began := time.Now()
	type row struct {
		sc     simScenario
		vals   map[string][]float64
		detail string
	}
	var rows []row
	first, last := cfg.seed0+1, cfg.seed0+cfg.seeds
	for _, sc := range scenarios {
		if !cfg.scen.MatchString(sc.id) {
			continue
		}
		// Seeds share nothing, so they run GOMAXPROCS at a time; each is
		// deterministic and they are tallied in order.
		res := make([]simResult, cfg.seeds)
		var wg sync.WaitGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for i := range res {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				seed := uint64(first + i)
				res[i] = simRun(sc, seed, cfg.start(seed))
				<-sem
			}()
		}
		wg.Wait()
		r := row{sc: sc, vals: map[string][]float64{}, detail: res[0].detail}
		for _, x := range res {
			for k, v := range x.m {
				r.vals[k] = append(r.vals[k], v)
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Skip("no scenario matches KANIVET_AIMD_SCEN")
	}
	var cols []simColumn
	for _, c := range columns {
		if slices.ContainsFunc(rows, func(r row) bool { return r.vals[c.key] != nil }) {
			cols = append(cols, c)
		}
	}
	cells := [][]string{append([]string{"id"}, func() (h []string) {
		for _, c := range cols {
			h = append(h, c.head)
		}
		return h
	}()...)}
	for _, r := range rows {
		line := []string{r.sc.id}
		for _, c := range cols {
			cell := "-"
			if xs := r.vals[c.key]; xs != nil {
				cell = simCell(xs, c)
			}
			line = append(line, cell)
		}
		cells = append(cells, line)
	}
	widths := make([]int, len(cells[0]))
	for _, line := range cells {
		for i, cell := range line {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== %s: mean±95%% CI (Student t) over %d seeds, %d-%d\n", title, cfg.seeds, first, last)
	if cfg.fixed {
		fmt.Fprintf(&b, "every seed starts at %s UTC (KANIVET_AIMD_EPOCH)\n", cfg.start(0).UTC().Format(time.DateTime))
	} else {
		var tod []string
		for seed := first; seed <= last; seed++ {
			tod = append(tod, cfg.start(uint64(seed)).UTC().Format(time.TimeOnly))
		}
		fmt.Fprintf(&b, "seeds start at their own time of day (UTC): %s\n", strings.Join(tod, " "))
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "%-4s %s: %s\n", r.sc.id, r.sc.name, r.sc.shape())
	}
	b.WriteString("\n")
	for _, line := range cells {
		for i, cell := range line {
			fmt.Fprintf(&b, "%s%s  ", cell, strings.Repeat(" ", widths[i]-len([]rune(cell))))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nseed %d, per user: mean limit/share of store time\n", first)
	for _, r := range rows {
		fmt.Fprintf(&b, "%-4s %s\n", r.sc.id, r.detail)
	}
	fmt.Fprintf(&b, "(%d scenarios x %d seeds in %.1fs)\n", len(rows), cfg.seeds, time.Since(began).Seconds())
	t.Log(b.String())
}

// TestThrottleSim is the S set: users contending for one store.
func TestThrottleSim(t *testing.T) {
	simTable(t, "S: contention", simContention(), simContinuousColumns)
}

// TestThrottleSimUncontended is the U set: one user on a store with room.
func TestThrottleSimUncontended(t *testing.T) {
	simTable(t, "U: one user, store with room", simUncontended(), simContinuousColumns)
}

// TestThrottleSimReports is the R set: bursty reports.
func TestThrottleSimReports(t *testing.T) {
	simTable(t, "R: reports", simReports(), simReportColumns)
}

// TestThrottleSimEvidence is the E set: reports with evidence opened alongside.
func TestThrottleSimEvidence(t *testing.T) {
	simTable(t, "E: reports and evidence", simEvidence(), simReportColumns)
}
