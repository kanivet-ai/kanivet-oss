package rightsizing

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/metrics"
)

func TestDaySpansAlignToUTCDays(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 7, 0, 0, time.UTC)
	g := newGrid(now, 28*day, 10*time.Minute)
	spans := daySpans(g, now)
	if len(spans) != 29 {
		t.Fatalf("%d spans for a 28-day window", len(spans))
	}
	for i, sp := range spans[:len(spans)-1] {
		if !sp.final || sp.start != sp.day || sp.end != sp.day.Add(day-g.step) || sp.day.Hour() != 0 {
			t.Fatalf("span %d %+v should be a whole finished UTC day", i, sp)
		}
	}
	last := spans[len(spans)-1]
	if last.final || !last.end.Equal(g.end) || !last.start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("today's span %+v", last)
	}
	// Just after midnight, yesterday is not final yet: late samples may land.
	early := time.Date(2026, 10, 1, 0, 10, 0, 0, time.UTC)
	sp := daySpans(newGrid(early, 2*day, 10*time.Minute), early)
	if sp[len(sp)-2].final {
		t.Fatal("yesterday counted as final 10 minutes after midnight")
	}
	// Both days are in progress, and neither may fetch the other's samples.
	if y, today := sp[len(sp)-2], sp[len(sp)-1]; !y.end.Equal(today.day.Add(-10*time.Minute)) || !today.start.Equal(today.day) {
		t.Fatalf("yesterday %v-%v overlaps today %v-%v", y.start, y.end, today.start, today.end)
	}
}

// Read from chunks alone, a day the report did not store for the reader's
// grid fails the read instead of leaving the day empty.
func TestChunksOnlyMissIsNotCached(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	cache := &memChunks{data: map[string][]byte{}}
	report := newChunker(&countingStore{}, cache)
	report.now = func() time.Time { return now }
	report.tails, report.run = true, "balanced"
	g := newGrid(now, 3*day, 10*time.Minute)
	if _, err := report.rangeQuery(context.Background(), "c", "q", g, nil); err != nil {
		t.Fatal(err)
	}

	read := func(g grid, run string) error {
		c := newChunker(nil, cache)
		c.now = func() time.Time { return now }
		c.run = run
		_, err := c.rangeQuery(withChunksOnly(context.Background()), "c", "q", g, nil)
		return err
	}
	if err := read(g, "balanced"); err != nil {
		t.Fatalf("the report's own grid: %v", err)
	}
	// Another run's report stored no tail of its own.
	if err := read(g, "conservative"); err != errNotCached {
		t.Fatalf("another run's tail: %v", err)
	}
	// A grid ten minutes on needs a day in progress nobody stored.
	if err := read(newGrid(now.Add(10*time.Minute), 3*day, 10*time.Minute), "balanced"); err != errNotCached {
		t.Fatalf("a later grid: %v", err)
	}
	// A finished day evicted from the cache.
	cache.mu.Lock()
	delete(cache.data, chunkKey("c", "q", g.step, now.Truncate(day).Add(-day), ""))
	cache.mu.Unlock()
	if err := read(g, "balanced"); err != errNotCached {
		t.Fatalf("an evicted day: %v", err)
	}
}

func TestChunkEncodingRoundTrip(t *testing.T) {
	in := []metrics.HistorySeries{
		// A gap splits the timestamps into runs; values span cores to bytes.
		{Labels: map[string]string{"pod": "a", "namespace": "n"}, Times: []int64{1000, 1300, 1600, 3400, 3700, 3701}, Values: []float32{0.25, 1.5e9, 3e-4, 0, 7.3e12, 0.001}},
		{Labels: map[string]string{"pod": "b"}, Times: []int64{}, Values: []float32{}},
		{Labels: map[string]string{"pod": "c"}, Times: []int64{5}, Values: []float32{2}},
	}
	data, err := encodeSeries(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeSeries(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Labels["pod"] != "a" || out[0].Labels["namespace"] != "n" {
		t.Fatalf("decoded %+v", out)
	}
	for k := range in {
		if len(out[k].Times) != len(in[k].Times) {
			t.Fatalf("series %d: %d samples, want %d", k, len(out[k].Times), len(in[k].Times))
		}
		for i, v := range in[k].Values {
			got := out[k].Values[i]
			if out[k].Times[i] != in[k].Times[i] {
				t.Fatalf("series %d time %d: %d vs %d", k, i, out[k].Times[i], in[k].Times[i])
			}
			if (v == 0) != (got == 0) || (v != 0 && math.Abs(float64(got/v)-1) > 5.1e-4) {
				t.Fatalf("series %d value %d: %v vs %v", k, i, got, v)
			}
		}
	}

	// keep skips series without materialising them.
	only, err := decodeSeries(data, func(l map[string]string) bool { return l["pod"] == "c" })
	if err != nil || len(only) != 1 || only[0].Values[0] < 1.99 {
		t.Fatalf("filtered decode: %+v, %v", only, err)
	}
	if _, err := decodeSeries(data[:len(data)/2], nil); err == nil {
		t.Fatal("a truncated chunk decoded")
	}
}

func TestQuantisationIsMonotoneAndTight(t *testing.T) {
	prev := uint16(0)
	for v := float32(1e-8); v < 1e18; v *= 1.37 {
		q := quantise(v)
		if q < prev {
			t.Fatalf("quantise not monotone at %v", v)
		}
		prev = q
		if got := dequantise(q); math.Abs(float64(got/v)-1) > 5.1e-4 {
			t.Fatalf("%v round-trips to %v", v, got)
		}
	}
}

func TestSplitByDaySharesArraysAndKeepsAllSamples(t *testing.T) {
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var ts []int64
	var vs []float32
	for tm := first.Add(-time.Hour); tm.Before(first.Add(3*day + time.Hour)); tm = tm.Add(time.Hour) {
		ts = append(ts, tm.Unix())
		vs = append(vs, 1)
	}
	byDay := splitByDay([]metrics.HistorySeries{{Labels: map[string]string{"pod": "a"}, Times: ts, Values: vs}}, first, 3)
	for d, part := range byDay {
		if len(part) != 1 || len(part[0].Times) != 24 {
			t.Fatalf("day %d: %+v", d, part)
		}
		if part[0].Times[0] != first.Add(time.Duration(d)*day).Unix() {
			t.Fatalf("day %d starts at %d", d, part[0].Times[0])
		}
	}
	if &byDay[0][0].Times[0] != &ts[1] {
		t.Fatal("day pieces should be sub-slices of the answer")
	}
}

type memChunks struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memChunks) GetRightsizingChunk(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.data[key]; ok {
		return d, nil
	}
	return nil, errNoStore
}

func (m *memChunks) SaveRightsizingChunk(key, cluster string, day int64, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = data
	return nil
}

// countingStore answers every range query with one sample per step and
// counts what it was asked.
type countingStore struct {
	mu     sync.Mutex
	calls  int
	hours  float64
	maxLen time.Duration // refuse ranges longer than this as too large (0 = never)
}

func (s *countingStore) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	s.mu.Lock()
	s.calls++
	s.hours += end.Sub(start).Hours()
	s.mu.Unlock()
	if s.maxLen > 0 && end.Sub(start) > s.maxLen {
		return nil, &metrics.QueryError{Type: "execution", Message: "the query exceeded the maximum number of samples (limit: 50000000)"}
	}
	var ts []int64
	var vs []float32
	for t := start; !t.After(end); t = t.Add(step) {
		ts = append(ts, t.Unix())
		vs = append(vs, 1)
	}
	return []metrics.HistorySeries{{Labels: map[string]string{"pod": "a"}, Times: ts, Values: vs}}, nil
}

func (s *countingStore) QueryInstant(ctx context.Context, cluster, query string, at time.Time) ([]metrics.HistorySeries, error) {
	return nil, nil
}

func TestChunkCacheFetchesOnlyTodayOnRefresh(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	store := &countingStore{}
	c := newChunker(store, &memChunks{data: map[string][]byte{}})
	c.now = func() time.Time { return now }
	g := newGrid(now, 28*day, 10*time.Minute)

	first, err := c.rangeQuery(context.Background(), "c", "q", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstCalls := store.calls
	// 28 finished days in batches of 4, plus today.
	if firstCalls != 28/batchDays+1 {
		t.Fatalf("first load: %d queries, want %d", firstCalls, 28/batchDays+1)
	}
	// Ten minutes later: only today is asked for again.
	now = now.Add(10 * time.Minute)
	second, err := c.rangeQuery(context.Background(), "c", "q", newGrid(now, 28*day, 10*time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.calls-firstCalls != 1 || c.hits.Load() != 28 {
		t.Fatalf("refresh sent %d queries (%d cache hits)", store.calls-firstCalls, c.hits.Load())
	}
	// Whole cached days cover the grid; out-of-grid samples are dropped later by align.
	count := func(ss seriesSet) (n int) {
		ss.each(func(s *metrics.HistorySeries) { n += len(s.Times) })
		return n
	}
	if count(first) < g.n || count(second) < g.n {
		t.Fatalf("%d and %d samples for a %d-point grid", count(first), count(second), g.n)
	}
	agg := align(second, newGrid(now, 28*day, 10*time.Minute), nil)
	present := 0
	for _, v := range agg[seriesKey{}] {
		if !math.IsNaN(v) {
			present++
		}
	}
	if present != g.n {
		t.Fatalf("aligned %d of %d grid points", present, g.n)
	}
}

func TestChunkSplitsQueriesTheStoreCallsTooLarge(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	store := &countingStore{maxLen: 7 * time.Hour}
	c := newChunker(store, nil)
	c.now = func() time.Time { return now }
	g := newGrid(now, 2*day, 5*time.Minute)
	res, err := c.rangeQuery(context.Background(), "c", "q", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	res.each(func(s *metrics.HistorySeries) {
		for _, ts := range s.Times {
			if seen[ts] {
				t.Fatalf("sample %d fetched twice across split halves", ts)
			}
			seen[ts] = true
		}
	})
	if store.calls <= 3 {
		t.Fatalf("expected the day queries to be split, got %d calls", store.calls)
	}
	if len(seen) < g.n {
		t.Fatalf("%d samples, the grid has %d", len(seen), g.n)
	}
}

// failingBatchStore refuses multi-day ranges with an error that isn't about
// size, so the chunker must fall back to single days.
type failingBatchStore struct{ countingStore }

func (s *failingBatchStore) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if end.Sub(start) > day {
		s.mu.Lock()
		s.calls++
		s.mu.Unlock()
		return nil, &metrics.QueryError{Type: "internal", Message: "boom"}
	}
	return s.countingStore.QueryRange(ctx, cluster, query, start, end, step)
}

func TestChunkBatchFallsBackToSingleDays(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	store := &failingBatchStore{}
	cache := &memChunks{data: map[string][]byte{}}
	c := newChunker(store, cache)
	c.now = func() time.Time { return now }
	if _, err := c.rangeQuery(context.Background(), "c", "q", newGrid(now, 8*day, 10*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(cache.data) != 8 {
		t.Fatalf("%d day chunks stored after falling back, want 8", len(cache.data))
	}
}

// partialStore answers like Thanos with a store gateway down: whatever it
// could reach, flagged as partial, until healed.
type partialStore struct {
	countingStore
	healed bool
}

func (s *partialStore) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if !s.healed {
		metrics.MarkPartial(ctx)
	}
	return s.countingStore.QueryRange(ctx, cluster, query, start, end, step)
}

// A partial answer serves the report that asked, but is not stored as the
// finished days (or the day in progress) it would otherwise stand for.
func TestPartialAnswersAreNotCached(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	store := &partialStore{}
	cache := &memChunks{data: map[string][]byte{}}
	c := newChunker(store, cache)
	c.now = func() time.Time { return now }
	c.tails = true
	g := newGrid(now, 8*day, 10*time.Minute)
	res, err := c.rangeQuery(context.Background(), "c", "q", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	samples := 0
	res.each(func(s *metrics.HistorySeries) { samples += len(s.Times) })
	if samples < g.n {
		t.Fatalf("the partial answer was not used: %d samples", samples)
	}
	if len(cache.data) != 0 || c.partials.Load() != 9 {
		t.Fatalf("%d chunks stored from partial answers (%d partial days)", len(cache.data), c.partials.Load())
	}
	store.healed = true
	if _, err := c.rangeQuery(context.Background(), "c", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	if len(cache.data) != 9 {
		t.Fatalf("%d chunks stored once the store answered whole, want 8 days and a tail", len(cache.data))
	}
}

func TestQueryClassIgnoresNamespaceAndPods(t *testing.T) {
	a := queryClass(`rate(x{namespace="beige",container!=""}[300s])`)
	b := queryClass(`rate(x{namespace="cyan",container!=""}[300s])`)
	c := queryClass(`max(y{namespace="beige",pod=~"(api).*"})`)
	if a != b || a == c {
		t.Fatalf("classes: %q %q %q", a, b, c)
	}
}

func TestPackedReportRoundTripsAndReadsOldPlainJSON(t *testing.T) {
	rep := &Report{Cluster: "c", Status: StatusReady, Workloads: []WorkloadReport{{Name: "api"}}}
	data, err := packReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	if got := unpackReport(data); got == nil || got.Workloads[0].Name != "api" {
		t.Fatalf("unpacked %+v", got)
	}
	if got := unpackReport([]byte(`{"cluster":"old","workloads":[]}`)); got == nil || got.Cluster != "old" {
		t.Fatalf("plain JSON report: %+v", got)
	}
}

func TestReportVersionFollowsComputationAndDismissals(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rep := &Report{ComputedAt: at}
	v0 := reportVersion(rep, nil)
	d := []db.RightsizingDismissal{{ID: 3}}
	if reportVersion(rep, d) == v0 {
		t.Fatal("a dismissal must change the version")
	}
	if reportVersion(&Report{ComputedAt: at.Add(time.Second)}, nil) == v0 {
		t.Fatal("a new computation must change the version")
	}
	if reportVersion(&Report{ComputedAt: at}, nil) != v0 {
		t.Fatal("the version must be stable")
	}
}
