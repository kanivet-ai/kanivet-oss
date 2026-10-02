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
)

// MetricsCacheBytes bounds stored data, including keys and per-entry overhead.
// SQLite's pages/journal and temporary query responses are separate overhead.
const MetricsCacheBytes int64 = 128 << 20

const (
	queryTailLag     = 2 * time.Minute
	queryTailTTL     = 15 * time.Second
	queryBlockPoints = 256
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

type queryCache struct {
	store    QueryCacheStore
	maxBytes int64
	now      func() time.Time
	locks    [64]chan struct{}
}

func newQueryCache(store QueryCacheStore, maxBytes int64) *queryCache {
	if store == nil {
		store = &memoryQueryStore{items: make(map[string]*list.Element)}
	}
	c := &queryCache{store: store, maxBytes: maxBytes, now: time.Now}
	for i := range c.locks {
		c.locks[i] = make(chan struct{}, 1)
	}
	return c
}

func queryHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c *queryCache) lock(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(key))
	l := c.locks[int(h[0])%len(c.locks)]
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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

type cachedAnswer struct {
	Expires int64
	Body    []byte
}

func (c *queryCache) answer(ctx context.Context, key string, ttl time.Duration, fetch func() ([]byte, error)) ([]byte, error) {
	if CacheOnly(ctx) {
		var entry cachedAnswer
		if c != nil && c.read(key, &entry) && (allowStaleCache(ctx) || entry.Expires > c.now().UnixNano()) {
			return entry.Body, nil
		}
		return nil, ErrCacheMiss
	}
	if c == nil {
		return fetch()
	}
	unlock, err := c.lock(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var entry cachedAnswer
	if c.read(key, &entry) && entry.Expires > c.now().UnixNano() {
		return entry.Body, nil
	}
	b, err := fetch()
	if err == nil {
		c.write(key, cachedAnswer{Expires: c.now().Add(ttl).UnixNano(), Body: b})
	}
	return b, err
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
	get := func(p url.Values) ([]byte, error) {
		body, err := fetch(p)
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
			Warnings                 []string
		}
		if err := json.Unmarshal(b, &status, json.MatchCaseInsensitiveNames(true)); err != nil {
			return nil, err
		}
		if status.Status != "success" {
			return nil, &QueryError{Type: status.ErrorType, Message: status.Error}
		}
		return b, nil
	}
	var b []byte
	var err error
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
		b, err = c.rangeQuery(ctx, source, params, start, end, step, get)
	} else {
		b, err = c.answer(ctx, queryHash(source, path, params.Encode()), queryTailTTL, func() ([]byte, error) { return get(params) })
	}
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func copyParams(p url.Values) url.Values {
	out := make(url.Values, len(p))
	for k, v := range p {
		out[k] = slices.Clone(v)
	}
	return out
}

func (c *queryCache) rangeQuery(ctx context.Context, source string, params url.Values, start, end, step int64, fetch func(url.Values) ([]byte, error)) ([]byte, error) {
	end = start + (end-start)/step*step
	identity := copyParams(params)
	identity.Del("start")
	identity.Del("end")
	identity.Del("step")
	key := queryHash("range-v1", source, identity.Encode(), strconv.FormatInt(step, 10), strconv.FormatInt(start%step, 10))
	if !CacheOnly(ctx) {
		unlock, err := c.lock(ctx, key)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	now := c.now()
	width := step * queryBlockPoints
	phase := start % step
	blocks := make(map[int64]*queryBlock)
	var bases []int64
	var missing []querySpan
	hasCoverage := false
	for base := (start-phase)/width*width + phase; base <= end; base += width {
		block := &queryBlock{}
		if !c.read(queryHash(key, strconv.FormatInt(base, 10)), block) {
			block = &queryBlock{}
		}
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
	if CacheOnly(ctx) {
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
		b, err := fetch(p)
		if err != nil {
			return nil, err
		}
		var response queryEnvelope
		if err := json.Unmarshal(b, &response); err != nil {
			return nil, err
		}
		// Partial responses and native histograms must not be marked complete.
		if response.Data.ResultType != "matrix" || len(response.Warnings) > 0 || len(response.Infos) > 0 || slices.ContainsFunc(response.Data.Result, func(s querySeries) bool { return len(s.Histograms) > 0 }) {
			if span.Start == start && span.End == end {
				return b, nil
			}
			return fetch(params)
		}
		stableEnd := (now.Add(-queryTailLag).Unix()-phase)/step*step + phase
		for _, base := range bases {
			block := blocks[base]
			lo, hi := max(span.Start, base), min(span.End, base+width-step)
			if lo > hi {
				continue
			}
			block.Series = replaceQuerySeries(block.Series, response.Data.Result, lo, hi)
			if lo <= min(hi, stableEnd) {
				block.Coverage = append(block.Coverage, querySpan{Start: lo, End: min(hi, stableEnd)})
			}
			if hi > stableEnd {
				block.Coverage = append(block.Coverage, querySpan{Start: max(lo, stableEnd+step), End: hi, Expires: now.Add(queryTailTTL).UnixNano()})
			}
			block.Coverage = mergeQuerySpans(block.Coverage, step)
			c.write(queryHash(key, strconv.FormatInt(base, 10)), block)
		}
	}
	var out queryEnvelope
	out.Status, out.Data.ResultType = "success", "matrix"
	out.Data.Result = []querySeries{}
	for _, base := range bases {
		out.Data.Result = replaceQuerySeries(out.Data.Result, blocks[base].Series, max(start, base), min(end, base+width-step))
	}
	return json.Marshal(out)
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
