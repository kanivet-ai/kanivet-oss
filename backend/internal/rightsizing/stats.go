package rightsizing

import (
	"math"
	"math/rand/v2"
	"slices"
)

// quantileSorted returns the q-quantile of an ascending slice by linear
// interpolation between order statistics (Hyndman-Fan type 7, the default in
// R and NumPy). It returns NaN for an empty slice.
func quantileSorted(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	h := q * float64(n-1)
	lo := int(math.Floor(h))
	hi := min(lo+1, n-1)
	return sorted[lo] + (h-float64(lo))*(sorted[hi]-sorted[lo])
}

// fractionAbove returns the share of samples strictly above x.
func fractionAbove(sorted []float64, x float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i, found := slices.BinarySearch(sorted, x)
	for found && i < len(sorted) && sorted[i] == x {
		i++
	}
	return float64(len(sorted)-i) / float64(len(sorted))
}

func sortedCopy(xs []float64) []float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func variance(xs []float64) float64 {
	n := len(xs)
	if n < 2 {
		return 0
	}
	m := mean(xs)
	var s float64
	for _, x := range xs {
		s += (x - m) * (x - m)
	}
	return s / float64(n-1)
}

func median(xs []float64) float64 { return quantileSorted(sortedCopy(xs), 0.5) }

func maxOf(xs []float64) float64 {
	m := math.Inf(-1)
	for _, x := range xs {
		m = max(m, x)
	}
	return m
}

// normalSF is the standard normal survival function P(Z > z).
func normalSF(z float64) float64 { return 0.5 * math.Erfc(z/math.Sqrt2) }

// ---- Day-block bootstrap ---------------------------------------------------

// bucketRatio is the width of one histogram bucket: values within 1.5% share
// a bucket. The bootstrap only uses buckets for the interval, never for the
// point estimate, so this bounds the interval's resolution, not its accuracy.
const bucketRatio = 1.015

// dayHistograms holds a cumulative histogram per day over a shared log-spaced
// bucket grid, so one bootstrap replicate is a weighted sum of a few counts
// per bucket instead of a sort.
type dayHistograms struct {
	lo    float64 // lower edge of bucket 0
	nb    int
	cum   [][]int32 // cum[d][b] = samples of day d in buckets 0..b
	sizes []int
}

func (h *dayHistograms) bucketOf(v float64) int {
	if v <= h.lo {
		return 0
	}
	b := int(math.Log(v/h.lo) / math.Log(bucketRatio))
	return min(max(b, 0), h.nb-1)
}

// upperEdge is the value at the top of bucket b.
func (h *dayHistograms) upperEdge(b int) float64 {
	return h.lo * math.Pow(bucketRatio, float64(b+1))
}

func newDayHistograms(days [][]float64) *dayHistograms {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, d := range days {
		for _, v := range d {
			if v > 0 {
				lo = min(lo, v)
			}
			hi = max(hi, v)
		}
	}
	if math.IsInf(lo, 1) {
		lo = 1e-9
	}
	if hi <= lo {
		hi = lo * bucketRatio
	}
	lo /= bucketRatio // keep the smallest positive value inside bucket 0
	nb := int(math.Log(hi/lo)/math.Log(bucketRatio)) + 2
	h := &dayHistograms{lo: lo, nb: nb, cum: make([][]int32, len(days)), sizes: make([]int, len(days))}
	for d, vals := range days {
		c := make([]int32, nb)
		for _, v := range vals {
			c[h.bucketOf(v)]++
		}
		for b := 1; b < nb; b++ {
			c[b] += c[b-1]
		}
		h.cum[d] = c
		h.sizes[d] = len(vals)
	}
	return h
}

// quantileWeighted finds the bucket holding the q-quantile when day d counts
// w[d] times, by binary search over the cumulative counts.
func (h *dayHistograms) quantileWeighted(w []int, q float64) float64 {
	var total float64
	for d, wd := range w {
		total += float64(wd * h.sizes[d])
	}
	if total == 0 {
		return math.NaN()
	}
	target := q * total
	lo, hi := 0, h.nb-1
	for lo < hi {
		mid := (lo + hi) / 2
		var c float64
		for d, wd := range w {
			if wd != 0 {
				c += float64(wd) * float64(h.cum[d][mid])
			}
		}
		if c >= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return h.upperEdge(lo)
}

// interval is a two-sided bootstrap interval around an estimate.
type interval struct{ lo, hi float64 }

// bootstrapQuantile returns a 90% interval for the q-quantile of the pooled
// samples by resampling whole days with replacement (a moving-block bootstrap
// with one-day blocks). Samples five minutes apart are strongly correlated;
// treating them as independent would make the interval far too narrow. Days
// keep that correlation inside each block.
//
// Bucketing biases each replicate slightly upwards, so the interval is scaled
// by est / (the same statistic on the full data via buckets): only the
// bootstrap's spread is used, centred on the exact estimate.
func bootstrapQuantile(days [][]float64, q, est float64, reps int, rng *rand.Rand) interval {
	return bootstrapQuantileDays(days, nil, q, est, reps, rng)
}

func bootstrapQuantileDays(days [][]float64, weekend []bool, q, est float64, reps int, rng *rand.Rand) interval {
	n := len(days)
	if n < 2 || math.IsNaN(est) {
		return interval{math.NaN(), math.NaN()}
	}
	h := newDayHistograms(days)
	ones := make([]int, n)
	for i := range ones {
		ones[i] = 1
	}
	ref := h.quantileWeighted(ones, q)
	if ref <= 0 || math.IsNaN(ref) {
		return interval{est, est}
	}
	// Resample weekdays from weekdays and weekend days from weekend days, so
	// every replicate keeps the window's mix: drawing freely, a replicate of
	// two weeks could hold four weekends or none, which inflates the spread
	// of anything with a weekly cycle.
	strata := [][]int{make([]int, 0, n)}
	if len(weekend) == n {
		var wd, we []int
		for i, w := range weekend {
			if w {
				we = append(we, i)
			} else {
				wd = append(wd, i)
			}
		}
		// A stratum of one or two days would be resampled from almost
		// nothing and understate its own spread; drawing freely instead errs
		// wide, which is the safe side.
		if len(wd) >= 3 && len(we) >= 3 {
			strata = [][]int{wd, we}
		}
	}
	if len(strata) == 1 {
		for i := range n {
			strata[0] = append(strata[0], i)
		}
	}
	w := make([]int, n)
	reps = max(reps, 50)
	stats := make([]float64, 0, reps)
	for range reps {
		clear(w)
		for _, st := range strata {
			for range st {
				w[st[rng.IntN(len(st))]]++
			}
		}
		if v := h.quantileWeighted(w, q); !math.IsNaN(v) {
			stats = append(stats, v)
		}
	}
	slices.Sort(stats)
	scale := est / ref
	lo, hi := quantileSorted(stats, 0.05)*scale, quantileSorted(stats, 0.95)*scale
	// With few days the bootstrap spread is itself estimated from a handful
	// of blocks and runs narrow; widen it as a Student-t interval would.
	f := studentT95(n-len(strata)) / 1.6448536269514722
	// Far tails move more between days than resampling whole days shows (the
	// bootstrap is inconsistent for extremes); calibrated by simulation.
	if q >= 0.99 {
		f *= tailWiden
	}
	return interval{est - (est-lo)*f, est + (hi-est)*f}
}

// tailWiden widens intervals for P99 and beyond: with it, 90% intervals
// covered the true P99 in 84-93% of simulated 4-14 day histories, with and
// without a weekly cycle (TestBootstrapCoverageTable), against 78-87%
// without.
const tailWiden = 1.3

// studentT95 is the one-sided 95% quantile of Student's t with df degrees of
// freedom (the two-sided 90% critical value).
func studentT95(df int) float64 {
	table := []float64{6.314, 2.920, 2.353, 2.132, 2.015, 1.943, 1.895, 1.860, 1.833, 1.812,
		1.796, 1.782, 1.771, 1.761, 1.753, 1.746, 1.740, 1.734, 1.729, 1.725}
	switch {
	case df < 1:
		return math.Inf(1)
	case df <= len(table):
		return table[df-1]
	case df <= 30:
		return 1.697 + (1.725-1.697)*float64(30-df)/10
	default:
		return 1.6448536269514722 + 1.6/float64(df)
	}
}

// ---- Regime change ---------------------------------------------------------

// levelShift is the most significant single change in level of a daily series.
type levelShift struct {
	at    int     // first day of the new regime
	t     float64 // Welch's t statistic on log values (right minus left)
	ratio float64 // geometric mean after / before
}

// Thresholds for accepting a level shift. The family-wise false-positive rate
// over all scanned splits is held at 1% (Bonferroni), and the 30% floor keeps
// statistically clear but practically trivial shifts from discarding data.
const (
	shiftAlpha    = 0.01
	shiftMinRatio = 1.3
	shiftMinSide  = 2
)

// detectLevelShift scans every split of a daily series for the largest
// two-sample t on log values. It returns ok=false unless that split is
// significant after correcting for the number of splits scanned and the
// change is at least 30%.
func detectLevelShift(daily []float64) (levelShift, bool) {
	n := len(daily)
	if n < 2*shiftMinSide {
		return levelShift{}, false
	}
	logs := make([]float64, n)
	for i, v := range daily {
		logs[i] = math.Log(max(v, 1e-9))
	}
	best := levelShift{}
	for k := shiftMinSide; k <= n-shiftMinSide; k++ {
		left, right := logs[:k], logs[k:]
		nl, nr := float64(len(left)), float64(len(right))
		ml, mr := mean(left), mean(right)
		// Pooled variance: with two or three days on one side a per-side
		// variance is too unstable to trust, and that side would decide the
		// test. The floor keeps two perfectly flat segments from producing an
		// infinite t: 2% day-to-day noise is the least any real workload shows.
		pooled := ((nl-1)*variance(left) + (nr-1)*variance(right)) / (nl + nr - 2)
		se := math.Sqrt(max(pooled, 4e-4) * (1/nl + 1/nr))
		t := (mr - ml) / se
		if math.Abs(t) > math.Abs(best.t) {
			best = levelShift{at: k, t: t, ratio: math.Exp(mr - ml)}
		}
	}
	ratioOK := best.ratio >= shiftMinRatio || best.ratio <= 1/shiftMinRatio
	splits := float64(n - 2*shiftMinSide + 1)
	p := studentTwoSided(best.t, float64(n-2)) * splits
	return best, p < shiftAlpha && ratioOK
}

// studentTwoSided is P(|T| > |t|) for Student's t with df degrees of freedom.
func studentTwoSided(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) {
		return 1
	}
	x := df / (df + t*t)
	return regIncBeta(df/2, 0.5, x)
}

// regIncBeta is the regularized incomplete beta function I_x(a, b), by the
// continued fraction in Numerical Recipes §6.4.
func regIncBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaCF(a, b, x) / a
	}
	return 1 - front*betaCF(b, a, 1-x)/b
}

func betaCF(a, b, x float64) float64 {
	const eps, tiny = 1e-14, 1e-300
	qab, qap, qam := a+b, a+1, a-1
	c, d := 1.0, 1-qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		m2 := 2 * fm
		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// ---- Trend -----------------------------------------------------------------

// mannKendall tests a series for a monotonic trend. It returns the S
// statistic and the two-sided p-value from the normal approximation with
// continuity correction and the tie-corrected variance.
func mannKendall(xs []float64) (s int, p float64) {
	n := len(xs)
	if n < 4 {
		return 0, 1
	}
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			switch {
			case xs[j] > xs[i]:
				s++
			case xs[j] < xs[i]:
				s--
			}
		}
	}
	ties := map[float64]int{}
	for _, x := range xs {
		ties[x]++
	}
	v := float64(n*(n-1)*(2*n+5)) / 18
	for _, t := range ties {
		if t > 1 {
			v -= float64(t*(t-1)*(2*t+5)) / 18
		}
	}
	if v <= 0 {
		return s, 1
	}
	var z float64
	switch {
	case s > 0:
		z = float64(s-1) / math.Sqrt(v)
	case s < 0:
		z = float64(s+1) / math.Sqrt(v)
	}
	return s, 2 * normalSF(math.Abs(z))
}

// theilSen fits y = a + b·x for x = 0..n-1 robustly: b is the median of all
// pairwise slopes and a the median of y - b·x.
func theilSen(ys []float64) (a, b float64) {
	n := len(ys)
	if n < 2 {
		if n == 1 {
			return ys[0], 0
		}
		return math.NaN(), 0
	}
	slopes := make([]float64, 0, n*(n-1)/2)
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			slopes = append(slopes, (ys[j]-ys[i])/float64(j-i))
		}
	}
	b = median(slopes)
	res := make([]float64, n)
	for i, y := range ys {
		res[i] = y - b*float64(i)
	}
	return median(res), b
}

// ---- Extremes --------------------------------------------------------------

// eulerGamma is the Euler-Mascheroni constant, the mean of a standard Gumbel.
const eulerGamma = 0.5772156649015329

// gumbelReturnLevel fits a Gumbel distribution to block maxima by the method
// of moments and returns the level exceeded on average once every T blocks.
// With daily peaks and T = 30 it is the "worst day in a month" estimate.
func gumbelReturnLevel(maxima []float64, T float64) float64 {
	if len(maxima) < 2 || T <= 1 {
		return math.NaN()
	}
	beta := math.Sqrt(variance(maxima)) * math.Sqrt(6) / math.Pi
	mu := mean(maxima) - eulerGamma*beta
	return mu - beta*math.Log(-math.Log(1-1/T))
}
