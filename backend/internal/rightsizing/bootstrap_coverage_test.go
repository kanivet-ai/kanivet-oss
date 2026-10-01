package rightsizing

import (
	"math/rand/v2"
	"testing"
)

// bootstrapScenario measures how often the 90% interval for the q-quantile
// covers the truth with n days of AR(1) usage, optionally with weekends at
// half the weekday level.
func bootstrapScenario(q float64, n int, weekly bool, trials int, seed uint64) float64 {
	rng := rand.New(rand.NewPCG(seed, 99))
	level := func(d int) float64 {
		if weekly && d%7 >= 5 {
			return 0.1
		}
		return 0.2
	}
	var long []float64
	for d := range 7 * 400 {
		long = append(long, ar1Day(rng, level(d))...)
	}
	truth := quantileSorted(sortedCopy(long), q)
	covered := 0
	for range trials {
		offset := rng.IntN(7)
		days := make([][]float64, n)
		weekend := make([]bool, n)
		var all []float64
		for d := range days {
			days[d] = ar1Day(rng, level(d+offset))
			weekend[d] = weekly && (d+offset)%7 >= 5
			all = append(all, days[d]...)
		}
		est := quantileSorted(sortedCopy(all), q)
		ci := bootstrapQuantileDays(days, weekend, q, est, 300, rng)
		if ci.lo <= truth && truth <= ci.hi {
			covered++
		}
	}
	return float64(covered) / float64(trials)
}

// TestBootstrapCoverageTable checks the 90% interval across the cases that
// used to break it: few days, P99, and a weekly cycle. Before stratifying by
// weekday, correcting for few blocks and widening far tails, coverage ran
// 64-95%; it must now stay near nominal everywhere.
func TestBootstrapCoverageTable(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	for _, q := range []float64{0.95, 0.99} {
		for _, n := range []int{4, 7, 14} {
			for _, weekly := range []bool{false, true} {
				c := bootstrapScenario(q, n, weekly, 300, uint64(n))
				t.Logf("q=%.2f n=%2d weekly=%-5v coverage %.0f%%", q, n, weekly, 100*c)
				if c < 0.80 || c > 0.97 {
					t.Errorf("q=%.2f n=%d weekly=%v: 90%% interval covered %.0f%%", q, n, weekly, 100*c)
				}
			}
		}
	}
}
