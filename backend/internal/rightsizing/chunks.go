package rightsizing

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

// History queries are cut at UTC day boundaries. A finished day never changes,
// so it is stored compressed in SQLite and never asked for again; a refresh
// only fetches the day in progress. Day-aligned ranges also line up with
// Mimir's own results cache, which splits queries into 24h pieces, so users
// asking about the same day mostly hit it.

// finalLag is how long after midnight a day counts as finished: late samples
// and ingestion delay settle well within it.
const finalLag = 30 * time.Minute

// minSplitSpan is the smallest range a too-large query is split down to.
const minSplitSpan = 2 * time.Hour

type chunkStore interface {
	GetRightsizingChunk(key string) ([]byte, error)
	SaveRightsizingChunk(key, cluster string, day int64, data []byte) error
}

// chunker runs range queries as cached day chunks.
type chunker struct {
	q     historyQuerier
	store chunkStore // nil: no persistence, still day-aligned
	now   func() time.Time

	hits, misses atomic.Int64
}

func newChunker(q historyQuerier, st chunkStore) *chunker {
	return &chunker{q: q, store: st, now: time.Now}
}

type daySpan struct {
	day        time.Time // UTC midnight
	start, end time.Time // query range, aligned to the step
	final      bool
}

// daySpans cuts a grid into UTC days. Finished days are queried whole, so the
// cached chunk is the same whichever window asked for it; the day in progress
// is queried from the grid's start (or midnight) to its end.
func daySpans(g grid, now time.Time) []daySpan {
	var out []daySpan
	first := g.start.UTC().Truncate(day)
	for d := first; !d.After(g.end); d = d.Add(day) {
		final := !d.Add(day + finalLag).After(now)
		s := daySpan{day: d, start: d, end: d.Add(day - g.step), final: final}
		if !final {
			s.start = maxTime(d, g.start)
			s.end = g.end
		}
		if s.end.Before(s.start) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func chunkKey(cluster, query string, step time.Duration, d time.Time) string {
	h := sha256.Sum256([]byte(cluster + "\x00" + query + "\x00" + step.String() + "\x00" + strconv.FormatInt(d.Unix(), 10)))
	return hex.EncodeToString(h[:])
}

// batchDays is how many consecutive finished days one query fetches when
// they're missing from the cache. Fewer, larger queries for a first load; the
// result is still stored per day, and Mimir's query frontend still splits and
// caches it by day. A batch that fails falls back to single days.
const batchDays = 4

// seriesSet is a query's answer as it came, one part per day or split range.
// A series spanning several days appears once per part; consumers walk the
// parts instead of joining them, which would copy every sample again.
type seriesSet [][]metrics.HistorySeries

func (ss seriesSet) each(f func(s *metrics.HistorySeries)) {
	for _, part := range ss {
		for i := range part {
			f(&part[i])
		}
	}
}

// rangeQuery returns the query's series over the grid, one part per day.
// keep, if set, drops series nobody will look at: from cached days as they
// are decoded, and from today's range as it is parsed. Days fetched to be
// cached keep everything, so a workload created tomorrow still finds its
// history in them.
func (c *chunker) rangeQuery(ctx context.Context, cluster, query string, g grid, keep func(map[string]string) bool) (seriesSet, error) {
	spans := daySpans(g, c.now())
	parts := make([][]metrics.HistorySeries, len(spans))

	var missing, live []int
	for i, sp := range spans {
		if !sp.final {
			live = append(live, i)
			continue
		}
		if c.store != nil {
			if data, err := c.store.GetRightsizingChunk(chunkKey(cluster, query, g.step, sp.day)); err == nil && len(data) > 0 {
				if res, err := decodeSeries(data, keep); err == nil {
					parts[i] = res
					c.hits.Add(1)
					continue
				}
			}
		}
		missing = append(missing, i)
	}

	// Consecutive missing days, up to batchDays at a time.
	var batches [][]int
	for _, i := range missing {
		if n := len(batches); n > 0 {
			last := batches[n-1]
			if last[len(last)-1] == i-1 && len(last) < batchDays {
				batches[n-1] = append(last, i)
				continue
			}
		}
		batches = append(batches, []int{i})
	}

	errs := make([]error, len(batches)+len(live))
	var wg sync.WaitGroup
	for bi, b := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[bi] = c.fetchBatch(ctx, cluster, query, g.step, spans, b, parts)
		}()
	}
	for li, i := range live {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.misses.Add(1)
			lctx := ctx
			if keep != nil {
				lctx = metrics.WithSeriesFilter(ctx, keep)
			}
			parts[i], errs[len(batches)+li] = c.fetchSpan(lctx, cluster, query, spans[i].start, spans[i].end, g.step)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return seriesSet(parts), nil
}

// fetchBatch fetches consecutive finished days in one query, splits the
// answer back into days and stores each. If the batch fails, it retries the
// days one at a time before giving up.
func (c *chunker) fetchBatch(ctx context.Context, cluster, query string, step time.Duration, spans []daySpan, b []int, parts [][]metrics.HistorySeries) error {
	first, last := spans[b[0]], spans[b[len(b)-1]]
	res, err := c.fetchSpan(ctx, cluster, query, first.start, last.end, step)
	if err != nil && len(b) > 1 && ctx.Err() == nil {
		for _, i := range b {
			if err := c.fetchBatch(ctx, cluster, query, step, spans, []int{i}, parts); err != nil {
				return err
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	c.misses.Add(int64(len(b)))
	byDay := splitByDay(res, first.day, len(b))
	for k, i := range b {
		if c.store != nil {
			if data, err := encodeSeries(byDay[k]); err == nil {
				_ = c.store.SaveRightsizingChunk(chunkKey(cluster, query, step, spans[i].day), cluster, spans[i].day.Unix(), data)
			}
		}
		parts[i] = byDay[k]
	}
	return nil
}

// splitByDay cuts series spanning n days from firstDay into one slice per
// day, keeping each series' labels in every day it has samples. The pieces
// share the answer's arrays: samples come sorted by time, so each day is a
// sub-slice.
func splitByDay(res []metrics.HistorySeries, firstDay time.Time, n int) [][]metrics.HistorySeries {
	out := make([][]metrics.HistorySeries, n)
	base := firstDay.Unix()
	daySec := int64(day / time.Second)
	for _, s := range res {
		lo := 0
		for d := range n {
			end := base + int64(d+1)*daySec
			hi := lo + sort.Search(len(s.Times)-lo, func(j int) bool { return s.Times[lo+j] >= end })
			for lo < hi && s.Times[lo] < base+int64(d)*daySec {
				lo++
			}
			if hi > lo {
				out[d] = append(out[d], metrics.HistorySeries{Labels: s.Labels, Times: s.Times[lo:hi:hi], Values: s.Values[lo:hi:hi]})
			}
			lo = hi
		}
	}
	return out
}

// fetchSpan queries a range, halving it while the store says it's too large.
func (c *chunker) fetchSpan(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	res, err := c.q.QueryRange(ctx, cluster, query, start, end, step)
	var qe *metrics.QueryError
	if err == nil || !errors.As(err, &qe) || !qe.TooLarge() || end.Sub(start) < minSplitSpan {
		return res, err
	}
	half := end.Sub(start) / 2
	mid := start.Add(half - half%step)
	left, err := c.fetchSpan(ctx, cluster, query, start, mid, step)
	if err != nil {
		return nil, err
	}
	right, err := c.fetchSpan(ctx, cluster, query, mid.Add(step), end, step)
	if err != nil {
		return nil, err
	}
	// The halves stay separate pieces of the same series: splitByDay and every
	// consumer handle a series repeated within a part.
	return append(left, right...), nil
}

// ---- Compact encoding ---------------------------------------------------------
//
// A finished day for a busy namespace is ~100k samples, and a 28-day window
// keeps four weeks of them for every query. Each series is stored as:
//
//   - its labels, sorted name/value pairs;
//   - its timestamps as runs of a constant stride (a series sampled every
//     step with no gaps is a single run, whatever its length);
//   - its values quantised to 0.1% steps on a log scale, 16 bits each,
//     delta-coded and byte-split so the compressor sees long runs of small
//     high bytes.
//
// 0.1% is far below what the statistics resolve (the bootstrap histograms
// use 1.5% buckets, recommendations round to 5m CPU and 1Mi), and it covers
// 1e-9 to 1e19, so both cores and bytes fit.

const chunkFormat = 2

const (
	quantFloor = 1e-9
	quantStep  = 0.0009995003330835331 // ln(1.001)
)

func quantise(v float32) uint16 {
	if !(v > quantFloor) {
		return 0
	}
	q := math.Round(math.Log(float64(v)/quantFloor)/quantStep) + 1
	return uint16(min(q, math.MaxUint16))
}

func dequantise(q uint16) float32 {
	if q == 0 {
		return 0
	}
	return float32(quantFloor * math.Exp(float64(q-1)*quantStep))
}

var (
	bufPool  = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	gzwPool  = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression); return w }}
	gzrPool  sync.Pool // *gzip.Reader
	codePool = sync.Pool{New: func() any { return new([]byte) }}
)

func encodeSeries(ss []metrics.HistorySeries) ([]byte, error) {
	raw := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(raw)
	raw.Reset()
	var tmp [binary.MaxVarintLen64]byte
	putU := func(v uint64) { raw.Write(tmp[:binary.PutUvarint(tmp[:], v)]) }
	putI := func(v int64) { raw.Write(tmp[:binary.PutVarint(tmp[:], v)]) }
	putS := func(s string) { putU(uint64(len(s))); raw.WriteString(s) }
	codes := codePool.Get().(*[]byte)
	defer codePool.Put(codes)

	raw.WriteByte(chunkFormat)
	putU(uint64(len(ss)))
	for _, s := range ss {
		names := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			names = append(names, k)
		}
		sort.Strings(names)
		putU(uint64(len(names)))
		for _, k := range names {
			putS(k)
			putS(s.Labels[k])
		}

		n := len(s.Times)
		putU(uint64(n))
		// Runs: start (delta from the previous run's last sample), stride, length.
		var runs [][3]int64
		for i := 0; i < n; {
			j, stride := i+1, int64(0)
			if j < n {
				stride = s.Times[j] - s.Times[i]
				for j < n && s.Times[j]-s.Times[j-1] == stride {
					j++
				}
			}
			runs = append(runs, [3]int64{s.Times[i], stride, int64(j - i)})
			i = j
		}
		putU(uint64(len(runs)))
		prev := int64(0)
		for _, r := range runs {
			putI(r[0] - prev)
			putI(r[1])
			putU(uint64(r[2]))
			prev = r[0] + r[1]*(r[2]-1)
		}

		// Values: zigzag deltas of the quantised codes, high bytes then low.
		*codes = slices.Grow((*codes)[:0], 2*n)[:2*n]
		hi, lo := (*codes)[:n], (*codes)[n:]
		var last uint16
		for i, v := range s.Values {
			q := quantise(v)
			d := int16(q - last)
			z := uint16(d<<1) ^ uint16(d>>15)
			hi[i], lo[i] = byte(z>>8), byte(z)
			last = q
		}
		raw.Write(*codes)
	}

	out := new(bytes.Buffer)
	zw := gzwPool.Get().(*gzip.Writer)
	defer gzwPool.Put(zw)
	zw.Reset(out)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// decodeSeries reads a chunk back. keep, if set, skips series by label
// without materialising their samples.
func decodeSeries(data []byte, keep func(map[string]string) bool) ([]metrics.HistorySeries, error) {
	zr, _ := gzrPool.Get().(*gzip.Reader)
	var err error
	if zr == nil {
		zr, err = gzip.NewReader(bytes.NewReader(data))
	} else {
		err = zr.Reset(bytes.NewReader(data))
	}
	if err != nil {
		return nil, err
	}
	defer gzrPool.Put(zr)
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	if _, err := buf.ReadFrom(zr); err != nil {
		return nil, err
	}
	r := chunkReader{b: buf.Bytes()}
	if ver := r.byte(); ver != chunkFormat {
		return nil, fmt.Errorf("unknown chunk format %d", ver)
	}
	n := r.uvarint()
	out := make([]metrics.HistorySeries, 0, min(n, 1<<16))
	for range n {
		nl := r.uvarint()
		labels := make(map[string]string, min(nl, 64))
		for range nl {
			k := r.str()
			labels[k] = r.str()
		}
		cnt := int(r.uvarint())
		nruns := r.uvarint()
		if r.err != nil || cnt < 0 || cnt > len(r.b) {
			return nil, errBadChunk
		}
		wanted := keep == nil || keep(labels)
		var s metrics.HistorySeries
		if wanted {
			s = metrics.HistorySeries{Labels: labels, Times: make([]int64, 0, cnt), Values: make([]float32, cnt)}
		}
		prev := int64(0)
		for range nruns {
			start := prev + r.varint()
			stride := r.varint()
			length := int64(r.uvarint())
			if r.err != nil || length <= 0 {
				return nil, errBadChunk
			}
			if wanted {
				if int64(len(s.Times))+length > int64(cnt) {
					return nil, errBadChunk
				}
				for j := range length {
					s.Times = append(s.Times, start+stride*j)
				}
			}
			prev = start + stride*(length-1)
		}
		codes := r.bytes(2 * cnt)
		if r.err != nil {
			return nil, errBadChunk
		}
		if !wanted {
			continue
		}
		if len(s.Times) != cnt {
			return nil, errBadChunk
		}
		var last uint16
		for i := range cnt {
			z := uint16(codes[i])<<8 | uint16(codes[cnt+i])
			last += uint16(int16(z>>1) ^ -int16(z&1))
			s.Values[i] = dequantise(last)
		}
		out = append(out, s)
	}
	if r.err != nil {
		return nil, errBadChunk
	}
	return out, nil
}

var errBadChunk = errors.New("corrupt chunk")

// chunkReader walks an encoded chunk; the first error sticks.
type chunkReader struct {
	b   []byte
	err error
}

func (r *chunkReader) byte() byte {
	if len(r.b) == 0 {
		r.err = errBadChunk
		return 0
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c
}

func (r *chunkReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errBadChunk
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *chunkReader) varint() int64 {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.err = errBadChunk
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *chunkReader) bytes(n int) []byte {
	if n < 0 || n > len(r.b) {
		r.err = errBadChunk
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}

func (r *chunkReader) str() string { return string(r.bytes(int(r.uvarint()))) }
