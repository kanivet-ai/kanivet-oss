package metrics

import (
	"container/list"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rangeRecorder struct {
	calls [][2]int64
	fail  bool
	empty bool
	value string
}

func (r *rangeRecorder) fetch(p url.Values) (io.ReadCloser, error) {
	start, _ := strconv.ParseInt(p.Get("start"), 10, 64)
	end, _ := strconv.ParseInt(p.Get("end"), 10, 64)
	step, _ := strconv.ParseInt(p.Get("step"), 10, 64)
	r.calls = append(r.calls, [2]int64{start, end})
	if r.fail {
		return nil, errors.New("offline")
	}
	var response queryEnvelope
	response.Status, response.Data.ResultType = "success", "matrix"
	response.Data.Result = []querySeries{}
	if !r.empty {
		s := querySeries{Metric: map[string]string{"pod": "a", "namespace": "apps"}, Values: []querySample{}}
		for at := start; at <= end; at += step {
			v := r.value
			if v == "" {
				v = strconv.FormatInt(at, 10)
			}
			s.Values = append(s.Values, querySample{jsontext.Value(strconv.FormatInt(at, 10)), jsontext.Value(strconv.Quote(v))})
		}
		response.Data.Result = append(response.Data.Result, s)
	}
	b, _ := json.Marshal(response)
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func cachedRange(t *testing.T, c *queryCache, r *rangeRecorder, source, query string, start, end, step int64) queryEnvelope {
	t.Helper()
	p := url.Values{"query": {query}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}, "step": {strconv.FormatInt(step, 10)}}
	body, err := c.prom(context.Background(), source, "/api/v1/query_range", p, r.fetch)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	var out queryEnvelope
	if err := json.UnmarshalRead(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestQueryCacheDeltaAndExpandedRange(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	r := &rangeRecorder{}
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	got := cachedRange(t, c, r, "prom", "up", 1010, 1230, 10)
	if len(got.Data.Result) != 1 || len(got.Data.Result[0].Values) != 23 {
		t.Fatalf("merged result = %+v", got)
	}
	cachedRange(t, c, r, "prom", "up", 900, 1230, 10)
	cachedRange(t, c, r, "prom", "up", 1000, 1100, 10)
	want := [][2]int64{{1000, 1200}, {1210, 1230}, {900, 990}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("queries = %v, want %v", r.calls, want)
	}
}

func TestQueryCacheMissingInteriorAndBlockBoundaries(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	r := &rangeRecorder{}
	cachedRange(t, c, r, "prom", "up", 100, 200, 1)
	cachedRange(t, c, r, "prom", "up", 500, 600, 1)
	got := cachedRange(t, c, r, "prom", "up", 100, 600, 1)
	if len(got.Data.Result[0].Values) != 501 {
		t.Fatalf("samples = %d", len(got.Data.Result[0].Values))
	}
	if !reflect.DeepEqual(r.calls, [][2]int64{{100, 200}, {500, 600}, {201, 499}}) {
		t.Fatalf("queries = %v", r.calls)
	}
	for i, v := range got.Data.Result[0].Values {
		if v.timestamp() != int64(i+100) {
			t.Fatalf("unordered/duplicate sample %d: %s", i, v[0])
		}
	}
}

func TestQueryCacheSourceQueryStepAndPhaseIsolation(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	r := &rangeRecorder{}
	info := &ProviderInfo{Type: "mimir", URL: "http://one", Service: "one", Namespace: "monitoring"}
	first := querySource("cluster", info, "tenant-a")
	for _, source := range []string{first, querySource("other", info, "tenant-a"), querySource("cluster", info, "tenant-b")} {
		cachedRange(t, c, r, source, "up", 1000, 1100, 10)
	}
	info.Service, info.URL = "two", "http://two"
	cachedRange(t, c, r, querySource("cluster", info, "tenant-a"), "up", 1000, 1100, 10)
	cachedRange(t, c, r, first, "different", 1000, 1100, 10)
	cachedRange(t, c, r, first, "up", 1000, 1100, 20)
	cachedRange(t, c, r, first, "up", 1001, 1101, 10)
	cachedRange(t, c, r, first, "up", 1000, 1100, 10)
	if len(r.calls) != 7 {
		t.Fatalf("source isolation: %v", r.calls)
	}
}

func TestQueryCacheLateSamplesAndDisappearingSeries(t *testing.T) {
	// Samples up to 1080 are final at the start, and the tail after it is live.
	now := time.Unix(1080, 0).Add(queryTailLag)
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return now }
	r := &rangeRecorder{value: "1.23456789012345"}
	got := cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	if string(got.Data.Result[0].Values[0][1]) != `"1.23456789012345"` {
		t.Fatal("lost sample precision")
	}
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	if len(r.calls) != 1 {
		t.Fatal("live coverage did not deduplicate requests")
	}
	now = now.Add(20 * time.Second)
	r.empty = true
	got = cachedRange(t, c, r, "prom", "up", 1000, 1220, 10)
	if r.calls[1] != [2]int64{1090, 1220} {
		t.Fatalf("tail range = %v", r.calls[1])
	}
	if len(got.Data.Result[0].Values) != 9 {
		t.Fatal("stale tail survived empty replacement")
	}
	now = now.Add(20 * time.Second)
	r.empty = false
	r.value = "2"
	got = cachedRange(t, c, r, "prom", "up", 1000, 1240, 10)
	if r.calls[2] != [2]int64{1110, 1240} {
		t.Fatalf("late-data retry = %v", r.calls)
	}
	if len(got.Data.Result[0].Values) != 23 {
		t.Fatalf("empty finalized gap was filled: %d samples", len(got.Data.Result[0].Values))
	}
}

func TestQueryCacheEmptyHistoryAndFailedDelta(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	r := &rangeRecorder{empty: true}
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	if len(r.calls) != 1 {
		t.Fatal("empty successful history not cached")
	}
	r.fail = true
	p := url.Values{"query": {"up"}, "start": {"1000"}, "end": {"1300"}, "step": {"10"}}
	if _, err := c.prom(context.Background(), "prom", "/api/v1/query_range", p, r.fetch); err == nil {
		t.Fatal("failed delta must not return partial success")
	}
	r.fail = false
	r.empty = false
	cachedRange(t, c, r, "prom", "up", 1000, 1300, 10)
	if !reflect.DeepEqual(r.calls, [][2]int64{{1000, 1200}, {1210, 1300}, {1210, 1300}}) {
		t.Fatalf("failed query poisoned coverage: %v", r.calls)
	}
}

func TestQueryCacheConcurrentRequestsAndCancellation(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	p := url.Values{"query": {"up"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(p url.Values) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return (&rangeRecorder{}).fetch(p)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := c.prom(context.Background(), "prom", "/api/v1/query_range", p, fetch)
			if err != nil {
				t.Error(err)
			} else {
				b.Close()
			}
		}()
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.prom(ctx, "prom", "/api/v1/query_range", p, fetch); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting cancellation = %v", err)
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate fetches: %d", calls.Load())
	}
}

func TestQueryCacheDoesNotFinalizeWarningsOrErrors(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","warnings":["partial response"],"data":{"resultType":"matrix","result":[]}}`,
		`{"status":"success","isPartial":true,"data":{"resultType":"matrix","result":[]}}`,
		`{"status":"error","errorType":"execution","error":"overloaded"}`,
		`{"status":"success","data":{"resultType":"matrix","result":[`,
	} {
		t.Run(body, func(t *testing.T) {
			c := newQueryCache(nil, MetricsCacheBytes)
			p := url.Values{"query": {"up"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}
			calls := 0
			fetch := func(url.Values) (io.ReadCloser, error) { calls++; return io.NopCloser(strings.NewReader(body)), nil }
			for i := 0; i < 2; i++ {
				b, _ := c.prom(context.Background(), "prom", "/api/v1/query_range", p, fetch)
				if b != nil {
					b.Close()
				}
			}
			if calls != 2 {
				t.Fatalf("cached incomplete response: %d", calls)
			}
		})
	}
}

func TestQueryCacheInstantAndRangeDependentQueries(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	now := time.Unix(100000, 0)
	c.now = func() time.Time { return now }
	calls := 0
	fetch := func(url.Values) (io.ReadCloser, error) {
		calls++
		return io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[]}}`)), nil
	}
	for _, path := range []string{"/api/v1/query", "/api/v1/query_range"} {
		p := url.Values{"query": {"up @ start()"}, "time": {"1000"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}
		for i := 0; i < 2; i++ {
			b, err := c.prom(context.Background(), "prom", path, p, fetch)
			if err != nil {
				t.Fatal(err)
			}
			b.Close()
		}
	}
	if calls != 2 {
		t.Fatalf("exact responses not cached: %d", calls)
	}
	now = now.Add(queryTailTTL + time.Second)
	b, err := c.prom(context.Background(), "prom", "/api/v1/query", url.Values{"query": {"up @ start()"}, "time": {"1000"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}, fetch)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if calls != 3 {
		t.Fatal("instant response did not expire")
	}
}

func TestMemoryQueryCacheLRUAndBudget(t *testing.T) {
	s := &memoryQueryStore{items: make(map[string]*list.Element)}
	_ = s.SaveMetricsQuery("a", []byte("1"), 260)
	_ = s.SaveMetricsQuery("b", []byte("2"), 260)
	_, _ = s.GetMetricsQuery("a")
	_ = s.SaveMetricsQuery("c", []byte("3"), 260)
	if _, err := s.GetMetricsQuery("b"); err == nil {
		t.Fatal("least recently used entry survived")
	}
	if _, err := s.GetMetricsQuery("a"); err != nil {
		t.Fatal("recently used entry evicted")
	}
	_ = s.SaveMetricsQuery("huge", make([]byte, 1000), 260)
	if s.bytes > 260 || len(s.items) != 2 {
		t.Fatalf("size=%d count=%d", s.bytes, len(s.items))
	}
}

func TestCacheOnlyReturnsStaleWhileUpstreamIsBlocked(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	now := time.Unix(1200, 0)
	c.now = func() time.Time { return now }
	r := &rangeRecorder{value: "1"}
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	now = now.Add(20 * time.Second)
	params := url.Values{"query": {"up"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		body, err := c.prom(context.Background(), "prom", "/api/v1/query_range", params, func(p url.Values) (io.ReadCloser, error) {
			close(entered)
			<-release
			return (&rangeRecorder{value: "2"}).fetch(p)
		})
		if body != nil {
			body.Close()
		}
		finished <- err
	}()
	var once sync.Once
	releaseRefresh := func() { once.Do(func() { close(release) }) }
	defer releaseRefresh()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not fetch upstream")
	}
	ctx, cancel := context.WithTimeout(WithCacheOnly(context.Background(), true), time.Second)
	defer cancel()
	neverFetch := func(url.Values) (io.ReadCloser, error) {
		t.Error("cache-only fetched upstream")
		return nil, errors.New("unexpected fetch")
	}
	body, err := c.prom(ctx, "prom", "/api/v1/query_range", params, neverFetch)
	if err != nil {
		t.Fatal(err)
	}
	var result queryEnvelope
	if err := json.UnmarshalRead(body, &result); err != nil {
		t.Fatal(err)
	}
	body.Close()
	if len(result.Data.Result) != 1 || len(result.Data.Result[0].Values) != 21 || string(result.Data.Result[0].Values[20][1]) != `"1"` {
		t.Fatalf("cached result=%+v", result)
	}
	if _, err := c.prom(WithCacheOnly(ctx, false), "prom", "/api/v1/query_range", params, neverFetch); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expired tail should miss a fresh cache read: %v", err)
	}
	params.Set("start", "900")
	body, err = c.prom(ctx, "prom", "/api/v1/query_range", params, neverFetch)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.UnmarshalRead(body, &result); err != nil {
		t.Fatal(err)
	}
	body.Close()
	if len(result.Data.Result[0].Values) != 21 {
		t.Fatal("preview invented samples for an uncached range")
	}
	params.Set("query", "missing")
	if _, err := c.prom(ctx, "prom", "/api/v1/query_range", params, neverFetch); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("missing query err=%v", err)
	}
	params.Set("query", "up")

	releaseRefresh()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	params.Set("start", "1000")
	body, err = c.prom(ctx, "prom", "/api/v1/query_range", params, neverFetch)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if err := json.UnmarshalRead(body, &result); err != nil {
		t.Fatal(err)
	}
	if string(result.Data.Result[0].Values[20][1]) != `"2"` {
		t.Fatal("upstream refresh was not cached")
	}
}

func TestCacheOnlyInstantAndMiss(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	now := time.Unix(1200, 0)
	c.now = func() time.Time { return now }
	fetch := func() ([]byte, error) { return []byte("cached"), nil }
	if _, err := c.answer(context.Background(), "instant", time.Second, fetch); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	neverFetch := func() ([]byte, error) { t.Error("cache-only fetched upstream"); return nil, nil }
	got, err := c.answer(WithCacheOnly(context.Background(), true), "instant", time.Second, neverFetch)
	if err != nil || string(got) != "cached" {
		t.Fatalf("result=%q err=%v", got, err)
	}
	if _, err := c.answer(WithCacheOnly(context.Background(), false), "instant", time.Second, neverFetch); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("err=%v", err)
	}
	if _, err := c.answer(WithCacheOnly(context.Background(), true), "missing", time.Second, neverFetch); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("err=%v", err)
	}
}

// countingStore records disk writes.
type countingStore struct {
	*memoryQueryStore
	writes atomic.Int32
}

func (s *countingStore) SaveMetricsQuery(key string, data []byte, maxBytes int64) error {
	s.writes.Add(1)
	return s.memoryQueryStore.SaveMetricsQuery(key, data, maxBytes)
}

func TestQueryCacheLiveTailStaysOffDisk(t *testing.T) {
	disk := &countingStore{memoryQueryStore: newMemoryQueryStore()}
	c := newQueryCache(disk, MetricsCacheBytes)
	now := time.Unix(1200, 0)
	c.now = func() time.Time { return now }
	r := &rangeRecorder{}
	for range 4 {
		cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
		now = now.Add(queryTailTTL + time.Second)
	}
	if len(r.calls) != 4 {
		t.Fatalf("live tail was not refreshed: %v", r.calls)
	}
	if n := disk.writes.Load(); n != 0 {
		t.Fatalf("refreshing a live chart wrote to disk %d times", n)
	}
	now = time.Unix(1200, 0).Add(queryTailLag + time.Minute)
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	if n := disk.writes.Load(); n != 1 {
		t.Fatalf("finished block written %d times, want once", n)
	}
	// A restart keeps finished data and nothing else.
	fresh := newQueryCache(disk, MetricsCacheBytes)
	fresh.now = c.now
	before := len(r.calls)
	cachedRange(t, fresh, r, "prom", "up", 1000, 1200, 10)
	if len(r.calls) != before {
		t.Fatalf("finished data was refetched after restart: %v", r.calls[before:])
	}
}

func TestQueryCacheRecentSamplesAreNotFinal(t *testing.T) {
	now := time.Unix(10000, 0)
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return now }
	r := &rangeRecorder{}
	// Ends five minutes ago: remote write can still deliver samples for it.
	end := now.Add(-5 * time.Minute).Unix()
	cachedRange(t, c, r, "prom", "up", end-200, end, 10)
	now = now.Add(queryTailTTL + time.Second)
	cachedRange(t, c, r, "prom", "up", end-200, end, 10)
	if len(r.calls) != 2 {
		t.Fatalf("a range five minutes old was treated as final: %v", r.calls)
	}
}

func TestQueryCacheRangesOfOneQueryDoNotWaitOnEachOther(t *testing.T) {
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	slow := func(p url.Values) (io.ReadCloser, error) {
		close(entered)
		<-release
		return (&rangeRecorder{}).fetch(p)
	}
	go func() {
		p := url.Values{"query": {"up"}, "start": {"1000"}, "end": {"1200"}, "step": {"10"}}
		if b, err := c.prom(context.Background(), "prom", "/api/v1/query_range", p, slow); err == nil {
			b.Close()
		}
	}()
	<-entered
	done := make(chan error, 1)
	go func() {
		p := url.Values{"query": {"up"}, "start": {"5000"}, "end": {"5200"}, "step": {"10"}}
		b, err := c.prom(context.Background(), "prom", "/api/v1/query_range", p, (&rangeRecorder{}).fetch)
		if err == nil {
			b.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second range waited for another range's fetch")
	}
}

func TestShortLivedAnswersStayInMemory(t *testing.T) {
	disk := &countingStore{memoryQueryStore: newMemoryQueryStore()}
	c := newQueryCache(disk, MetricsCacheBytes)
	calls := 0
	fetch := func() ([]byte, error) { calls++; return []byte("snapshot"), nil }
	for range 3 {
		if _, err := c.answer(context.Background(), "k", time.Minute, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("answer fetched %d times within its TTL", calls)
	}
	if n := disk.writes.Load(); n != 0 {
		t.Fatalf("short-lived answer written to disk %d times", n)
	}
}
