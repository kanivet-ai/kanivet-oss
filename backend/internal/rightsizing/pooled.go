package rightsizing

import (
	"math"
	"slices"
	"time"
)

// pooled holds every replica's CPU at each grid step: the samples at step i
// are vals[off[i]:off[i+1]]. CPU is judged on this pooled per-replica
// distribution, not on the busiest replica: for interchangeable replicas the
// busiest of N at P95 is the pooled 0.95^(1/N) quantile, so a busiest-replica
// target would grow with the replica count (P98 for 3 replicas, P99.95 for
// 100) while the per-pod behaviour stays the same. Replica-time is also what
// the request is paid for. Hot replicas are flagged separately as imbalance.
type pooled struct {
	off  []int32
	vals []float64
}

func (p *pooled) steps() int { return len(p.off) - 1 }

func (p *pooled) at(i int) []float64 { return p.vals[p.off[i]:p.off[i+1]] }

func (p *pooled) span(lo, hi int) []float64 { return p.vals[p.off[lo]:p.off[hi]] }

// pooledPoint is one replica's sample at a grid index.
type pooledPoint struct {
	idx int32
	v   float64
}

// buildPooled lays points out by grid index with a counting sort.
func buildPooled(n int, pts []pooledPoint) *pooled {
	p := &pooled{off: make([]int32, n+1), vals: make([]float64, len(pts))}
	for _, pt := range pts {
		p.off[pt.idx+1]++
	}
	for i := 1; i <= n; i++ {
		p.off[i] += p.off[i-1]
	}
	next := slices.Clone(p.off[:n])
	for _, pt := range pts {
		p.vals[next[pt.idx]] = pt.v
		next[pt.idx]++
	}
	return p
}

// mergePooled combines the pooled samples of several history keys that
// belong to one live container.
func mergePooled(n int, ps []*pooled) *pooled {
	switch len(ps) {
	case 0:
		return &pooled{off: make([]int32, n+1)}
	case 1:
		return ps[0]
	}
	var pts []pooledPoint
	for _, p := range ps {
		for i := range n {
			for _, v := range p.at(i) {
				pts = append(pts, pooledPoint{int32(i), v})
			}
		}
	}
	return buildPooled(n, pts)
}

// derive returns the busiest replica, the total and the replica count at
// each step, NaN where no replica reported.
func (p *pooled) derive() (busiest, total, count []float64) {
	n := p.steps()
	busiest, total, count = make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range n {
		vs := p.at(i)
		if len(vs) == 0 {
			busiest[i], total[i], count[i] = math.NaN(), math.NaN(), math.NaN()
			continue
		}
		m, s := math.Inf(-1), 0.0
		for _, v := range vs {
			m = max(m, v)
			s += v
		}
		busiest[i], total[i], count[i] = m, s, float64(len(vs))
	}
	return
}

// dayBlock is one 24h block of samples. present counts the grid steps with
// data, which for pooled CPU is fewer than the samples.
type dayBlock struct {
	vals    []float64
	present int
	start   int  // grid index of the block's first step
	sorted  bool // vals is in ascending order
}

// sortBlocks gives each block a sorted copy of its values, all in one
// buffer. Nothing downstream needs the order within a day, and the daily
// statistics, the window's quantiles and the backtest all want sorted days:
// sorting each day once and merging days is far cheaper than sorting the
// same samples again for each of them.
func sortBlocks(bs []dayBlock) []dayBlock {
	buf := flattenBlocks(bs)
	out := make([]dayBlock, len(bs))
	off := 0
	for i, b := range bs {
		v := buf[off : off+len(b.vals) : off+len(b.vals)]
		slices.Sort(v)
		out[i] = dayBlock{vals: v, present: b.present, start: b.start, sorted: true}
		off += len(b.vals)
	}
	return out
}

// sortedValues returns every value of the blocks in ascending order, merging
// sorted days pairwise instead of sorting them all again.
func sortedValues(bs []dayBlock) []float64 {
	for _, b := range bs {
		if !b.sorted {
			return sortedCopy(flattenBlocks(bs))
		}
	}
	src := flattenBlocks(bs)
	runs := make([]int, 0, len(bs)+1) // run boundaries in src
	runs = append(runs, 0)
	for _, b := range bs {
		if len(b.vals) > 0 {
			runs = append(runs, runs[len(runs)-1]+len(b.vals))
		}
	}
	dst := make([]float64, len(src))
	for len(runs) > 2 {
		next := runs[:1]
		for i := 0; i+1 < len(runs); i += 2 {
			lo, mid := runs[i], runs[i+1]
			hi := mid
			if i+2 < len(runs) {
				hi = runs[i+2]
			}
			mergeInto(dst[lo:hi], src[lo:mid], src[mid:hi])
			next = append(next, hi)
		}
		runs = next
		src, dst = dst, src
	}
	return src
}

func mergeInto(dst, a, b []float64) {
	i, j, k := 0, 0, 0
	for i < len(a) && j < len(b) {
		if b[j] < a[i] {
			dst[k] = b[j]
			j++
		} else {
			dst[k] = a[i]
			i++
		}
		k++
	}
	k += copy(dst[k:], a[i:])
	copy(dst[k:], b[j:])
}

// blockRanges splits n steps into 24h ranges ending at the window's end,
// oldest first.
func blockRanges(n int, step time.Duration) (ranges [][2]int, perDay int) {
	perDay = max(int(day/step), 1)
	nBlocks := (n + perDay - 1) / perDay
	for b := range nBlocks {
		hi := n - (nBlocks-1-b)*perDay
		ranges = append(ranges, [2]int{max(hi-perDay, 0), hi})
	}
	return ranges, perDay
}

// seriesBlocks splits an aligned series (NaN = missing) into day blocks,
// dropping the empty ones before the first sample.
func seriesBlocks(values []float64, step time.Duration) ([]dayBlock, int) {
	ranges, perDay := blockRanges(len(values), step)
	var out []dayBlock
	for _, r := range ranges {
		var vals []float64
		for _, v := range values[r[0]:r[1]] {
			if !math.IsNaN(v) {
				vals = append(vals, v)
			}
		}
		if len(out) == 0 && len(vals) == 0 {
			continue
		}
		out = append(out, dayBlock{vals: vals, present: len(vals), start: r[0]})
	}
	return out, perDay
}

// pooledBlocks splits pooled samples into day blocks without copying.
func pooledBlocks(p *pooled, step time.Duration) ([]dayBlock, int) {
	ranges, perDay := blockRanges(p.steps(), step)
	var out []dayBlock
	for _, r := range ranges {
		present := 0
		for i := r[0]; i < r[1]; i++ {
			if p.off[i+1] > p.off[i] {
				present++
			}
		}
		if len(out) == 0 && present == 0 {
			continue
		}
		out = append(out, dayBlock{vals: p.span(r[0], r[1]), present: present, start: r[0]})
	}
	return out, perDay
}

func blockVals(blocks []dayBlock) [][]float64 {
	out := make([][]float64, len(blocks))
	for i, b := range blocks {
		out[i] = b.vals
	}
	return out
}

func flattenBlocks(blocks []dayBlock) []float64 {
	n := 0
	for _, b := range blocks {
		n += len(b.vals)
	}
	out := make([]float64, 0, n)
	for _, b := range blocks {
		out = append(out, b.vals...)
	}
	return out
}
