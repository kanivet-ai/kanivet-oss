package rightsizing

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v ± %v", name, got, want, tol)
	}
}

func TestQuantileSorted(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5}
	approx(t, "p0", quantileSorted(xs, 0), 1, 0)
	approx(t, "p50", quantileSorted(xs, 0.5), 3, 0)
	approx(t, "p90", quantileSorted(xs, 0.9), 4.6, 1e-12)
	approx(t, "p100", quantileSorted(xs, 1), 5, 0)
	if !math.IsNaN(quantileSorted(nil, 0.5)) {
		t.Fatal("empty should be NaN")
	}
}

func TestFractionAbove(t *testing.T) {
	xs := []float64{1, 2, 2, 3, 4}
	approx(t, "above 2", fractionAbove(xs, 2), 0.4, 1e-12)
	approx(t, "above 0", fractionAbove(xs, 0), 1, 0)
	approx(t, "above 4", fractionAbove(xs, 4), 0, 0)
	approx(t, "above 2.5", fractionAbove(xs, 2.5), 0.4, 1e-12)
}

// ar1Day simulates one day of 5-minute CPU samples: a lognormal level with
// AR(1) noise (φ = 0.9), the kind of autocorrelation real usage has.
func ar1Day(rng *rand.Rand, level float64) []float64 {
	out := make([]float64, 288)
	e := 0.0
	for i := range out {
		e = 0.9*e + rng.NormFloat64()*0.2
		out[i] = level * math.Exp(e)
	}
	return out
}

// TestBootstrapCoverage checks the interval does what it claims: across many
// simulated two-week histories, the 90% interval should contain the true
// population P95 roughly 90% of the time. Treating samples as independent
// instead of resampling days gives intervals that cover far less often.
func TestBootstrapCoverage(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	// True P95 of the stationary process, from one very long run.
	var long []float64
	for range 2000 {
		long = append(long, ar1Day(rng, 0.2)...)
	}
	truth := quantileSorted(sortedCopy(long), 0.95)

	const trials = 200
	covered := 0
	for range trials {
		days := make([][]float64, 14)
		var all []float64
		for d := range days {
			days[d] = ar1Day(rng, 0.2)
			all = append(all, days[d]...)
		}
		est := quantileSorted(sortedCopy(all), 0.95)
		ci := bootstrapQuantile(days, 0.95, est, 300, rng)
		if ci.lo <= est && est <= ci.hi && ci.lo <= truth && truth <= ci.hi {
			covered++
		}
	}
	rate := float64(covered) / trials
	// The percentile bootstrap with few blocks is mildly anti-conservative;
	// anything from 78% up is a well-behaved interval, below 70% is broken.
	if rate < 0.78 || rate > 0.99 {
		t.Fatalf("90%% interval covered the truth in %.0f%% of trials", rate*100)
	}
}

func TestBootstrapNeedsTwoDays(t *testing.T) {
	ci := bootstrapQuantile([][]float64{{1, 2, 3}}, 0.95, 3, 100, rand.New(rand.NewPCG(1, 1)))
	if !math.IsNaN(ci.lo) {
		t.Fatal("one day cannot give an interval")
	}
}

func TestDetectLevelShift(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	daily := make([]float64, 14)
	for i := range daily {
		level := 0.2
		if i >= 9 {
			level = 0.5 // a deploy on day 9 made it 2.5× hungrier
		}
		daily[i] = level * math.Exp(rng.NormFloat64()*0.05)
	}
	shift, ok := detectLevelShift(daily)
	if !ok || shift.at != 9 {
		t.Fatalf("want shift at day 9, got %+v ok=%v", shift, ok)
	}
	approx(t, "ratio", shift.ratio, 2.5, 0.3)
}

func TestDetectLevelShiftIgnoresNoise(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	falsePositives := 0
	for range 500 {
		daily := make([]float64, 14)
		for i := range daily {
			daily[i] = 0.3 * math.Exp(rng.NormFloat64()*0.15)
		}
		if _, ok := detectLevelShift(daily); ok {
			falsePositives++
		}
	}
	if falsePositives > 5 { // ≤ 1%
		t.Fatalf("%d of 500 stationary series flagged as shifted", falsePositives)
	}
}

func TestMannKendall(t *testing.T) {
	up := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	s, p := mannKendall(up)
	if s != 45 || p > 0.001 {
		t.Fatalf("increasing: S=%d p=%v", s, p)
	}
	flat := []float64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3}
	if _, p := mannKendall(flat); p < 0.05 {
		t.Fatalf("no trend but p=%v", p)
	}
	if _, p := mannKendall([]float64{2, 2, 2, 2, 2}); p != 1 {
		t.Fatalf("constant series p=%v", p)
	}
}

func TestTheilSenIgnoresOutlier(t *testing.T) {
	ys := []float64{10, 12, 14, 16, 100, 20, 22} // slope 2, one wild point
	a, b := theilSen(ys)
	approx(t, "slope", b, 2, 1e-9)
	approx(t, "intercept", a, 10, 1e-9)
}

func TestGumbelReturnLevel(t *testing.T) {
	// Draw maxima from Gumbel(μ=100, β=10) and check the 30-block return level
	// against the closed form μ - β ln(-ln(1 - 1/30)) ≈ 133.8.
	rng := rand.New(rand.NewPCG(7, 8))
	xs := make([]float64, 5000)
	for i := range xs {
		u := rng.Float64()
		xs[i] = 100 - 10*math.Log(-math.Log(u))
	}
	approx(t, "x30", gumbelReturnLevel(xs, 30), 133.8, 1.5)
}

func TestStudentTwoSided(t *testing.T) {
	approx(t, "t(10)=2.228", studentTwoSided(2.228, 10), 0.05, 1e-3)
	approx(t, "t(3)=3.182", studentTwoSided(3.182, 3), 0.05, 1e-3)
	approx(t, "t(1e6)=1.96", studentTwoSided(1.96, 1e6), 0.05, 1e-3)
	approx(t, "t=0", studentTwoSided(0, 5), 1, 1e-12)
}

func TestSortedValuesMergesSortedDays(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, days := range []int{0, 1, 2, 3, 7, 28} {
		var bs []dayBlock
		var all []float64
		for d := range days {
			n := rng.IntN(50) // some days empty
			if d == 2 {
				n = 0
			}
			v := make([]float64, n)
			for i := range v {
				v[i] = rng.Float64()
			}
			all = append(all, v...)
			bs = append(bs, dayBlock{vals: v, present: n})
		}
		got := sortedValues(sortBlocks(bs))
		if want := sortedCopy(all); !slices.Equal(got, want) {
			t.Fatalf("%d days: merged %v, want %v", days, got, want)
		}
	}
}
