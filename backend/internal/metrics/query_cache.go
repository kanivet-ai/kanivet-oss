package metrics

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// MetricsCacheBytes bounds stored data, including keys and per-entry overhead.
// SQLite's pages/journal and temporary query responses are separate overhead.
const MetricsCacheBytes int64 = 128 << 20

const (
	// Remote-write samples keep arriving for minutes after their timestamp
	// (agent queues, retries, out-of-order ingestion), so a span only counts as
	// final once it is this old. Anything newer is refetched when it expires.
	queryTailLag     = 10 * time.Minute
	queryTailTTL     = 15 * time.Second
	queryBlockPoints = 256
	// hotBytes bounds the in-memory copy of live blocks and short-lived answers.
	hotBytes int64 = 16 << 20
)

// Cache-only reads never wait for an upstream refresh or fetch missing samples.
type cacheOnlyKey struct{}
type cacheOnlyMode struct{ allowStale bool }

var ErrCacheMiss = errors.New("metrics are not cached")

func WithCacheOnly(ctx context.Context, allowStale bool) context.Context {
	return context.WithValue(ctx, cacheOnlyKey{}, cacheOnlyMode{allowStale})
}

func CacheOnly(ctx context.Context) bool {
	_, ok := ctx.Value(cacheOnlyKey{}).(cacheOnlyMode)
	return ok
}

func allowStaleCache(ctx context.Context) bool {
	mode, _ := ctx.Value(cacheOnlyKey{}).(cacheOnlyMode)
	return mode.allowStale
}

type QueryCacheStore interface {
	GetMetricsQuery(key string) ([]byte, error)
	SaveMetricsQuery(key string, data []byte, maxBytes int64) error
}

// queryCache keeps finished chart data on disk and everything that will
// change soon (the live tail, short-lived answers) in memory, so a chart that
// refreshes every few seconds doesn't rewrite SQLite each time.
type queryCache struct {
	store    QueryCacheStore
	hot      *memoryQueryStore
	maxBytes int64
	now      func() time.Time
	// flight shares one fetch between identical requests. Different ranges of
	// the same query run side by side; only the merge into a block is serial,
	// and it never waits on the network.
	flight singleflight.Group
	blocks [64]sync.Mutex
}

func newQueryCache(store QueryCacheStore, maxBytes int64) *queryCache {
	if store == nil {
		store = newMemoryQueryStore()
	}
	return &queryCache{store: store, hot: newMemoryQueryStore(), maxBytes: maxBytes, now: time.Now}
}

func newMemoryQueryStore() *memoryQueryStore {
	return &memoryQueryStore{items: make(map[string]*list.Element)}
}

func queryHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// shared runs fn once for every concurrent caller with the same key. A caller
// whose own context ends stops waiting; the others still get the result. A
// result that failed only because the caller that ran it gave up is retried
// for the callers still waiting.
func (c *queryCache) shared(ctx context.Context, key string, fn func() (any, error)) (any, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ch := c.flight.DoChan(key, fn)
		select {
		case r := <-ch:
			if r.Err != nil && ctx.Err() == nil && (errors.Is(r.Err, context.Canceled) || errors.Is(r.Err, context.DeadlineExceeded)) && r.Shared {
				continue
			}
			return r.Val, r.Err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *queryCache) blockLock(key string) *sync.Mutex {
	h := sha256.Sum256([]byte(key))
	return &c.blocks[int(h[0])%len(c.blocks)]
}

// Samples retain the provider's original numeric strings and precision.
type querySample [2]jsontext.Value

func (s querySample) timestamp() int64 {
	v, _ := strconv.ParseFloat(string(s[0]), 64)
	return int64(v)
}

type querySeries struct {
	Metric     map[string]string `json:"metric"`
	Values     []querySample     `json:"values"`
	Histograms jsontext.Value    `json:"histograms,omitempty"`
}

type queryEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string        `json:"resultType"`
		Result     []querySeries `json:"result"`
	} `json:"data"`
	ErrorType string   `json:"errorType,omitempty"`
	Error     string   `json:"error,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Infos     []string `json:"infos,omitempty"`
}

type querySpan struct {
	Start, End int64
	// Zero means finalized; live coverage expires and is queried once more
	// after the ingestion allowance, even if its original response was empty.
	Expires int64
}

type queryBlock struct {
	Coverage []querySpan
	Series   []querySeries
}

// read and write go to disk, compressed.
func (c *queryCache) read(key string, dst any) bool {
	b, err := c.store.GetMetricsQuery(key)
	if err != nil {
		return false
	}
	z, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return false
	}
	defer z.Close()
	return json.UnmarshalRead(z, dst) == nil
}

func (c *queryCache) write(key string, value any) {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if err := json.MarshalWrite(z, value); err != nil {
		_ = z.Close()
		return
	}
	if z.Close() == nil {
		_ = c.store.SaveMetricsQuery(key, b.Bytes(), c.maxBytes)
	}
}

// readHot and writeHot keep a value in memory only.
func (c *queryCache) readHot(key string, dst any) bool {
	b, err := c.hot.GetMetricsQuery(key)
	return err == nil && json.Unmarshal(b, dst) == nil
}

func (c *queryCache) writeHot(key string, value any) {
	if b, err := json.Marshal(value); err == nil {
		_ = c.hot.SaveMetricsQuery(key, b, hotBytes)
	}
}

// readBlock prefers the in-memory copy, which is never older than the disk one.
func (c *queryCache) readBlock(key string) *queryBlock {
	block := &queryBlock{}
	if c.readHot(key, block) {
		return block
	}
	block = &queryBlock{}
	if c.read(key, block) {
		return block
	}
	return &queryBlock{}
}

// writeBlock always refreshes memory, and touches disk only when the block's
// finished coverage grew. The disk copy drops live coverage, so a restart
// refetches the tail instead of trusting it.
func (c *queryCache) writeBlock(key string, block *queryBlock, finalBefore []querySpan) {
	c.writeHot(key, block)
	final := finalSpans(block.Coverage)
	if slices.Equal(final, finalBefore) {
		return
	}
	c.write(key, &queryBlock{Coverage: final, Series: block.Series})
}

func finalSpans(spans []querySpan) []querySpan {
	var out []querySpan
	for _, s := range spans {
		if s.Expires == 0 {
			out = append(out, s)
		}
	}
	return out
}

type cachedAnswer struct {
	Expires int64
	Body    []byte
}

// answer caches a short-lived answer in memory only: it is stale within
// seconds, so writing it to disk would cost more than refetching it.
func (c *queryCache) answer(ctx context.Context, key string, ttl time.Duration, fetch func() ([]byte, error)) ([]byte, error) {
	if CacheOnly(ctx) {
		var entry cachedAnswer
		if c != nil && c.readHot(key, &entry) && (allowStaleCache(ctx) || entry.Expires > c.now().UnixNano()) {
			return entry.Body, nil
		}
		return nil, ErrCacheMiss
	}
	if c == nil {
		return fetch()
	}
	v, err := c.shared(ctx, "answer|"+key, func() (any, error) {
		var entry cachedAnswer
		if c.readHot(key, &entry) && entry.Expires > c.now().UnixNano() {
			return entry.Body, nil
		}
		b, err := fetch()
		if err == nil {
			c.writeHot(key, cachedAnswer{Expires: c.now().Add(ttl).UnixNano(), Body: b})
		}
		return b, err
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// Expressions whose values depend on the enclosing range cannot be split.
var rangeDependentQuery = regexp.MustCompile(`\b(start|end|range)\s*\(`)

func (c *queryCache) prom(ctx context.Context, source, path string, params url.Values, fetch func(url.Values) (io.ReadCloser, error)) (io.ReadCloser, error) {
	if c == nil {
		if CacheOnly(ctx) {
			return nil, ErrCacheMiss
		}
		return fetch(params)
	}
	start, e1 := strconv.ParseInt(params.Get("start"), 10, 64)
	end, e2 := strconv.ParseInt(params.Get("end"), 10, 64)
	stepDuration, e3 := time.ParseDuration(params.Get("step"))
	if e3 != nil {
		secs, e := strconv.ParseInt(params.Get("step"), 10, 64)
		e3 = e
		stepDuration = time.Duration(secs) * time.Second
	}
	step := int64(stepDuration / time.Second)
	if path == "/api/v1/query_range" && e1 == nil && e2 == nil && e3 == nil && step > 0 && stepDuration%time.Second == 0 && start >= 0 && end >= start && !rangeDependentQuery.MatchString(params.Get("query")) {
		out, err := c.rangeQuery(ctx, source, params, start, end, step, func(p url.Values) (*queryEnvelope, error) {
			body, err := fetch(p)
			if err != nil {
				return nil, err
			}
			defer body.Close()
			return decodeEnvelope(body)
		})
		if err != nil {
			return nil, err
		}
		return streamJSON(out), nil
	}
	// A query that can't be split into blocks is kept whole, briefly, in
	// memory. These are rare (instant queries, start()/end()/range()).
	b, err := c.answer(ctx, queryHash(source, path, params.Encode()), queryTailTTL, func() ([]byte, error) {
		body, err := fetch(params)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		var status struct {
			Status, ErrorType, Error string
		}
		if err := json.Unmarshal(b, &status, json.MatchCaseInsensitiveNames(true)); err != nil {
			return nil, err
		}
		if status.Status != "success" {
			return nil, &QueryError{Type: status.ErrorType, Message: status.Error}
		}
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// decodeEnvelope reads a range answer in one pass, straight off the wire.
func decodeEnvelope(r io.Reader) (*queryEnvelope, error) {
	var out queryEnvelope
	if err := json.UnmarshalRead(r, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		return nil, &QueryError{Type: out.ErrorType, Message: out.Error}
	}
	return &out, nil
}

// streamJSON encodes v as the caller reads it, so the answer is never held
// as one more buffer next to the decoded series.
func streamJSON(v any) io.ReadCloser {
	r, w := io.Pipe()
	go func() { w.CloseWithError(json.MarshalWrite(w, v)) }()
	return r
}

func copyParams(p url.Values) url.Values {
	out := make(url.Values, len(p))
	for k, v := range p {
		out[k] = slices.Clone(v)
	}
	return out
}

func (c *queryCache) rangeQuery(ctx context.Context, source string, params url.Values, start, end, step int64, fetch func(url.Values) (*queryEnvelope, error)) (*queryEnvelope, error) {
	end = start + (end-start)/step*step
	identity := copyParams(params)
	identity.Del("start")
	identity.Del("end")
	identity.Del("step")
	key := queryHash("range-v1", source, identity.Encode(), strconv.FormatInt(step, 10), strconv.FormatInt(start%step, 10))
	if CacheOnly(ctx) {
		return c.assembleRange(ctx, key, params, start, end, step, nil)
	}
	v, err := c.shared(ctx, key+"|"+strconv.FormatInt(start, 10)+"|"+strconv.FormatInt(end, 10), func() (any, error) {
		return c.assembleRange(ctx, key, params, start, end, step, fetch)
	})
	if err != nil {
		return nil, err
	}
	return v.(*queryEnvelope), nil
}

func (c *queryCache) assembleRange(ctx context.Context, key string, params url.Values, start, end, step int64, fetch func(url.Values) (*queryEnvelope, error)) (*queryEnvelope, error) {
	now := c.now()
	width := step * queryBlockPoints
	phase := start % step
	blocks := make(map[int64]*queryBlock)
	var bases []int64
	var missing []querySpan
	hasCoverage := false
	for base := (start-phase)/width*width + phase; base <= end; base += width {
		block := c.readBlock(queryHash(key, strconv.FormatInt(base, 10)))
		if !allowStaleCache(ctx) {
			block.Coverage = slices.DeleteFunc(block.Coverage, func(s querySpan) bool { return s.Expires != 0 && s.Expires <= now.UnixNano() })
		}
		blocks[base] = block
		bases = append(bases, base)
		lo, hi := max(start, base), min(end, base+width-step)
		cursor := lo
		for _, covered := range block.Coverage {
			if covered.End < cursor || covered.Start > hi {
				continue
			}
			hasCoverage = true
			if covered.Start > cursor {
				missing = append(missing, querySpan{Start: cursor, End: min(hi, covered.Start-step)})
			}
			cursor = max(cursor, covered.End+step)
		}
		if cursor <= hi {
			missing = append(missing, querySpan{Start: cursor, End: hi})
		}
	}
	if fetch == nil {
		if !hasCoverage || (len(missing) > 0 && !allowStaleCache(ctx)) {
			return nil, ErrCacheMiss
		}
		// A preview can contain gaps, but never claims those gaps were fetched.
		missing = nil
	}
	missing = mergeQuerySpans(missing, step)
	for _, span := range missing {
		p := copyParams(params)
		p.Set("start", strconv.FormatInt(span.Start, 10))
		p.Set("end", strconv.FormatInt(span.End, 10))
		response, err := fetch(p)
		if err != nil {
			return nil, err
		}
		// Partial responses and native histograms must not be marked complete.
		if response.Data.ResultType != "matrix" || len(response.Warnings) > 0 || len(response.Infos) > 0 || slices.ContainsFunc(response.Data.Result, func(s querySeries) bool { return len(s.Histograms) > 0 }) {
			if span.Start == start && span.End == end {
				return response, nil
			}
			return fetch(params)
		}
		stableEnd := (now.Add(-queryTailLag).Unix()-phase)/step*step + phase
		for _, base := range bases {
			lo, hi := max(span.Start, base), min(span.End, base+width-step)
			if lo > hi {
				continue
			}
			// Merge into the latest copy: another range of this query may have
			// written the block while this one was fetching.
			blockKey := queryHash(key, strconv.FormatInt(base, 10))
			mu := c.blockLock(blockKey)
			mu.Lock()
			block := c.readBlock(blockKey)
			block.Coverage = slices.DeleteFunc(block.Coverage, func(s querySpan) bool { return s.Expires != 0 && s.Expires <= now.UnixNano() })
			finalBefore := finalSpans(block.Coverage)
			block.Series = replaceQuerySeries(block.Series, response.Data.Result, lo, hi)
			if lo <= min(hi, stableEnd) {
				block.Coverage = append(block.Coverage, querySpan{Start: lo, End: min(hi, stableEnd)})
			}
			if hi > stableEnd {
				block.Coverage = append(block.Coverage, querySpan{Start: max(lo, stableEnd+step), End: hi, Expires: now.Add(queryTailTTL).UnixNano()})
			}
			block.Coverage = mergeQuerySpans(block.Coverage, step)
			c.writeBlock(blockKey, block, finalBefore)
			mu.Unlock()
			blocks[base] = block
		}
	}
	out := &queryEnvelope{Status: "success"}
	out.Data.ResultType = "matrix"
	out.Data.Result = []querySeries{}
	for _, base := range bases {
		out.Data.Result = replaceQuerySeries(out.Data.Result, blocks[base].Series, max(start, base), min(end, base+width-step))
	}
	return out, nil
}

func mergeQuerySpans(spans []querySpan, step int64) []querySpan {
	slices.SortFunc(spans, func(a, b querySpan) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	out := make([]querySpan, 0, len(spans))
	for _, s := range spans {
		if n := len(out); n > 0 && out[n-1].Expires == s.Expires && s.Start <= out[n-1].End+step {
			out[n-1].End = max(out[n-1].End, s.End)
		} else {
			out = append(out, s)
		}
	}
	return out
}

// Replace the fetched interval, including absent/stale series, rather than
// appending over old samples. Label identity is independent of map order.
func replaceQuerySeries(old, fresh []querySeries, start, end int64) []querySeries {
	out := make([]querySeries, 0, len(old)+len(fresh))
	index := make(map[string]int)
	for _, s := range old {
		values := make([]querySample, 0, len(s.Values))
		for _, v := range s.Values {
			if t := v.timestamp(); t < start || t > end {
				values = append(values, v)
			}
		}
		s.Values = values
		index[labelKey(s.Metric)] = len(out)
		out = append(out, s)
	}
	for _, s := range fresh {
		key := labelKey(s.Metric)
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, querySeries{Metric: s.Metric, Values: []querySample{}})
		}
		for _, v := range s.Values {
			if t := v.timestamp(); t >= start && t <= end {
				out[i].Values = append(out[i].Values, v)
			}
		}
	}
	for i := range out {
		slices.SortFunc(out[i].Values, func(a, b querySample) int {
			if a.timestamp() < b.timestamp() {
				return -1
			}
			if a.timestamp() > b.timestamp() {
				return 1
			}
			return 0
		})
	}
	return slices.DeleteFunc(out, func(s querySeries) bool { return len(s.Values) == 0 })
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		parts = append(parts, k, labels[k])
	}
	return queryHash(parts...)
}

type memoryQueryEntry struct {
	key  string
	data []byte
	size int64
}
type memoryQueryStore struct {
	mu    sync.Mutex
	items map[string]*list.Element
	lru   list.List
	bytes int64
}

func (s *memoryQueryStore) GetMetricsQuery(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.items[key]
	if e == nil {
		return nil, fmt.Errorf("cache miss")
	}
	s.lru.MoveToFront(e)
	return slices.Clone(e.Value.(memoryQueryEntry).data), nil
}

func (s *memoryQueryStore) SaveMetricsQuery(key string, data []byte, maxBytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	size := int64(len(key) + len(data) + 128)
	if size > maxBytes {
		return nil
	}
	if e := s.items[key]; e != nil {
		s.bytes -= e.Value.(memoryQueryEntry).size
		s.lru.Remove(e)
		delete(s.items, key)
	}
	for s.bytes+size > maxBytes {
		e := s.lru.Back()
		v := e.Value.(memoryQueryEntry)
		s.bytes -= v.size
		delete(s.items, v.key)
		s.lru.Remove(e)
	}
	e := s.lru.PushFront(memoryQueryEntry{key, slices.Clone(data), size})
	s.items[key] = e
	s.bytes += size
	return nil
}
