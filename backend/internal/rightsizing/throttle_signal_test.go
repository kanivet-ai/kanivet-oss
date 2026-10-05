package rightsizing

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

// signalled is an answer the store accounts for: its own time, its work, and
// its clock (the Date header, to the second) when it was stamped.
func signalled(clock *fakeClock, skew time.Duration, latency, server time.Duration) outcome {
	// The answer was stamped as it left the store, half the time beyond the
	// store's own that the round trip held before it reached us.
	stamped := clock.t.Add(skew).Add(-(latency - server) / 2)
	return outcome{
		latency: latency, serverLatency: server, storeBytes: 40_000, work: 24,
		storeDate: stamped.Truncate(time.Second), receivedAt: clock.t,
	}
}

func TestLimiterLearnsTheStoresClock(t *testing.T) {
	for _, skew := range []time.Duration{7 * time.Second, -10 * time.Second, 0} {
		l, clock := testLimiter()
		for i := range 10 {
			clock.t = clock.t.Add(1700 * time.Millisecond) // the truncation falls differently each time
			oneQuery(l, signalled(clock, skew, 400*time.Millisecond, 100*time.Millisecond))
			if i+1 < clockMinSamples && l.offset != 0 {
				t.Fatalf("skew %v: offset %v used after %d readings", skew, l.offset, i+1)
			}
		}
		if diff := (l.offset - skew).Abs(); diff > time.Second {
			t.Errorf("skew %v: learnt offset %v, off by %v", skew, l.offset, diff)
		}
	}
}

func TestLimiterNeedsTheStoresTimingToTrustItsClock(t *testing.T) {
	l, clock := testLimiter()
	for range 10 {
		clock.t = clock.t.Add(time.Second)
		o := signalled(clock, 9*time.Second, 400*time.Millisecond, 100*time.Millisecond)
		o.serverLatency = 0 // Date alone says nothing of how long it took to arrive
		oneQuery(l, o)
	}
	if l.offset != 0 {
		t.Fatalf("offset %v learnt from answers without the store's time", l.offset)
	}
}

// With the store's clock a user whose own is 13 s ahead still has its lull
// when everyone does, and not at the moments its own clock says.
func TestLullsFollowTheStoresClock(t *testing.T) {
	l, clock := testLimiter()
	skew := 13 * time.Second
	for range 5 {
		clock.t = clock.t.Truncate(time.Minute).Add(time.Minute + 30*time.Second)
		oneQuery(l, signalled(clock, -skew, 400*time.Millisecond, 100*time.Millisecond))
	}
	if (l.offset + skew).Abs() > time.Second {
		t.Fatalf("offset %v, want %v", l.offset, -skew)
	}
	// 50 s into the minute here, but the store reads 37 s: no lull.
	clock.t = clock.t.Truncate(time.Minute).Add(time.Minute + 50*time.Second)
	oneQuery(l, signalled(clock, -skew, 400*time.Millisecond, 100*time.Millisecond))
	if l.inLull {
		t.Fatal("lull by the user's own clock, not the store's")
	}
	// 15 s into the minute here, and the store reads 2 s: in the lull.
	clock.t = clock.t.Truncate(time.Minute).Add(time.Minute + 15*time.Second)
	oneQuery(l, signalled(clock, -skew, 400*time.Millisecond, 100*time.Millisecond))
	if !l.inLull {
		t.Fatal("no lull when the store's clock is in one")
	}
}

// A tunnel that slows down says nothing of the store: judged on the round
// trip the limit is cut for it, judged on the store's own time it holds.
func TestLimiterJudgesTheStoreNotTheNetwork(t *testing.T) {
	run := func(signal bool) float64 {
		l, clock := testLimiter()
		l.limit = 4
		step := func(latency time.Duration) {
			clock.t = clock.t.Truncate(time.Minute).Add(time.Minute + 30*time.Second) // out of the lulls
			o := signalled(clock, 0, latency, 100*time.Millisecond)
			if !signal {
				o = outcome{latency: latency, work: 24}
			}
			oneQuery(l, o)
		}
		for range 12 {
			step(200 * time.Millisecond)
		}
		before := l.limit
		for range 40 {
			step(1500 * time.Millisecond)
		}
		return l.limit / before
	}
	if with, without := run(true), run(false); with < 1 || without >= 1 {
		t.Fatalf("limit kept %.2f with the store's time, %.2f without; want the first to hold and the second to cut", with, without)
	}
}

// The same queueing at the store costs a user whose queries spend most of their
// time in the network less of its limit: they hold less of the store's queue.
func TestLimiterQueueIsTheShareOfTimeAtTheStore(t *testing.T) {
	cut := func(network time.Duration) float64 {
		l, clock := testLimiter()
		l.limit = 5
		step := func(server time.Duration) {
			clock.t = clock.t.Truncate(time.Minute).Add(time.Minute + 30*time.Second)
			oneQuery(l, signalled(clock, 0, network+server, server))
		}
		for range 12 {
			step(50 * time.Millisecond)
		}
		before := l.limit
		for range 6 {
			step(100 * time.Millisecond) // the store's own time doubled: queueing
		}
		return before - l.limit
	}
	near, far := cut(20*time.Millisecond), cut(900*time.Millisecond)
	if !(near > far && far > 0) {
		t.Fatalf("limit cut %.3f for a user near the store and %.3f for one far from it; want more for the first, both positive", near, far)
	}
}

func TestSignalledAnswersAreAClassOfTheirOwn(t *testing.T) {
	l, clock := testLimiter()
	oneQuery(l, outcome{latency: time.Second, work: 24, class: "q"})
	oneQuery(l, signalled(clock, 0, 400*time.Millisecond, 100*time.Millisecond))
	if len(l.classes) != 2 {
		t.Fatalf("%d classes, want the plain and the store-timed answers apart", len(l.classes))
	}
}

func TestByteBucketsSpanAFactorOfTwo(t *testing.T) {
	if byteBucket(4096) != 0 || byteBucket(8191) != 0 || byteBucket(8192) != 1 || byteBucket(2048) != -1 {
		t.Fatalf("buckets %d %d %d %d", byteBucket(4096), byteBucket(8191), byteBucket(8192), byteBucket(2048))
	}
	if byteBucket(0) != byteBucket(1) {
		t.Fatal("no work and one byte must share a bucket, not fail")
	}
}

// headerQuerier answers like a store that sends timing headers, in as many
// responses as it is told.
type headerQuerier struct {
	headers   http.Header
	responses int
}

func (q headerQuerier) QueryRange(ctx context.Context, _, _ string, _, _ time.Time, _ time.Duration) ([]metrics.HistorySeries, error) {
	for range q.responses {
		metrics.RecordResponse(ctx, &http.Response{Header: q.headers})
	}
	return nil, nil
}

func (q headerQuerier) QueryInstant(ctx context.Context, cluster, query string, _ time.Time) ([]metrics.HistorySeries, error) {
	return q.QueryRange(ctx, cluster, query, time.Time{}, time.Time{}, 0)
}

// controlled hands the store's account of a query to the limiter, but only
// when one response answered it: with several, no one time describes it.
func TestControlledPassesTheStoresAccountToTheLimiter(t *testing.T) {
	headers := http.Header{"Server-Timing": {"response_time;dur=100, bytes_processed;val=40000"}}
	for _, tc := range []struct {
		name      string
		responses int
		timed     bool
	}{{"none", 0, false}, {"one", 1, true}, {"several", 2, false}} {
		l, _ := testLimiter()
		c := newControlled(headerQuerier{headers: headers, responses: tc.responses})
		c.lim = func(string) *limiter { return l }
		if _, err := c.QueryRange(context.Background(), "c", "up", time.Now(), time.Now(), time.Minute); err != nil {
			t.Fatal(err)
		}
		if len(l.classes) != 1 {
			t.Fatalf("%s: %d classes, want the one answer to have reached the limiter", tc.name, len(l.classes))
		}
		timed := false
		for key := range l.classes {
			timed = strings.Contains(key, "#s")
		}
		if timed != tc.timed {
			t.Errorf("%s: judged on the store's time = %v, want %v", tc.name, timed, tc.timed)
		}
	}
}
