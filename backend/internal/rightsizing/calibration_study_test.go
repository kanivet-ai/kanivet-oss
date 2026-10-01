//go:build calibration

package rightsizing

// A calibration study on real history, run by hand against a copy of the
// rightsizing cache database. It replays the engine with rolling origins:
// train on some days, test on the following week the estimator never saw,
// and measures how often each rule would have been exceeded. It makes no
// network calls: every day it reads must already be cached.
//
//	RS_CACHE_DB=/path/copy-of-rightsizing.db RS_CLUSTER=arn:... \
//	  go test -tags calibration -run TestCalibrationStudy -v -timeout 30m .

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/metrics"
)

type cacheOnly struct{}

var errNotCached = errors.New("not cached")

func (cacheOnly) QueryRange(context.Context, string, string, time.Time, time.Time, time.Duration) ([]metrics.HistorySeries, error) {
	return nil, errNotCached
}

func (cacheOnly) QueryInstant(context.Context, string, string, time.Time) ([]metrics.HistorySeries, error) {
	return nil, errNotCached
}

// dayData is one 24h block of a container: pooled CPU samples and the
// busiest replica's memory samples.
type dayData struct {
	cpu, mem []float64
	cpuCov   float64
	memCov   float64
}

func splitDays(p *pooled, mem []float64, step time.Duration) []dayData {
	ranges, perDay := blockRanges(p.steps(), step)
	out := make([]dayData, len(ranges))
	for i, r := range ranges {
		present := 0
		for s := r[0]; s < r[1]; s++ {
			if p.off[s+1] > p.off[s] {
				present++
			}
		}
		d := dayData{cpu: slices.Clone(p.span(r[0], r[1])), cpuCov: float64(present) / float64(perDay)}
		if mem != nil {
			for _, v := range mem[r[0]:r[1]] {
				if !math.IsNaN(v) {
					d.mem = append(d.mem, v)
				}
			}
			d.memCov = float64(len(d.mem)) / float64(perDay)
		}
		out[i] = d
	}
	return out
}

func toBlocks(days []dayData, pick func(dayData) []float64, cov func(dayData) float64, perDay int) []dayBlock {
	out := make([]dayBlock, len(days))
	for i, d := range days {
		out[i] = dayBlock{vals: slices.Clone(pick(d)), present: int(cov(d) * float64(perDay)), start: i * perDay}
	}
	return sortBlocks(out)
}

type tally struct {
	n, breaches int
	ratios      []float64 // estimate / what the test period needed
}

func (t *tally) add(breach bool, ratio float64) {
	t.n++
	if breach {
		t.breaches++
	}
	if !math.IsNaN(ratio) && !math.IsInf(ratio, 0) {
		t.ratios = append(t.ratios, ratio)
	}
}

func (t *tally) String() string {
	slices.Sort(t.ratios)
	return fmt.Sprintf("n=%5d  breach %5.1f%%  over-provision median %.2fx  p90 %.2fx", t.n,
		100*float64(t.breaches)/math.Max(float64(t.n), 1), quantileSorted(t.ratios, 0.5), quantileSorted(t.ratios, 0.9))
}

type cpuTally struct {
	exceed []float64
	target float64
}

func (c *cpuTally) String() string {
	slices.Sort(c.exceed)
	bad := 0
	for _, e := range c.exceed {
		if e > 2*c.target {
			bad++
		}
	}
	return fmt.Sprintf("n=%5d  target %4.1f%%  exceedance median %5.2f%%  mean %5.2f%%  p90 %5.2f%%  over 2x target %5.1f%% of cases",
		len(c.exceed), 100*c.target, 100*quantileSorted(c.exceed, 0.5), 100*mean(c.exceed), 100*quantileSorted(c.exceed, 0.9),
		100*float64(bad)/math.Max(float64(len(c.exceed)), 1))
}

func loadNamespaces(t *testing.T, d *db.DB, cluster string) []string {
	data, err := d.GetRightsizingReport(runKey(cluster, ProfileBalanced, 28*day))
	if err != nil {
		t.Fatalf("no 28d report cached: %v", err)
	}
	rep := unpackReport(data)
	seen := map[string]bool{}
	for _, w := range rep.Workloads {
		seen[w.Namespace] = true
	}
	var out []string
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

func TestCalibrationStudy(t *testing.T) {
	path, cluster := os.Getenv("RS_CACHE_DB"), os.Getenv("RS_CLUSTER")
	if path == "" || cluster == "" {
		t.Skip("set RS_CACHE_DB and RS_CLUSTER")
	}
	store := &db.DB{}
	if err := store.OpenRightsizingCache(path); err != nil {
		t.Fatal(err)
	}
	namespaces := loadNamespaces(t, store, cluster)
	ch := newChunker(cacheOnly{}, store)
	// Every day must be finished, so the grid ends just before today.
	asOf := time.Now().UTC().Truncate(day).Add(-10 * time.Minute)
	pr := probe{memMetric: memUsage, throttle: throttleSeconds}
	ctx := context.Background()

	mem := map[string]*tally{}
	memKeys := []string{}
	addMem := func(name string, breach bool, ratio float64) {
		if mem[name] == nil {
			mem[name] = &tally{}
			memKeys = append(memKeys, name)
		}
		mem[name].add(breach, ratio)
	}
	cpu := map[string]*cpuTally{}
	cpuKeys := []string{}
	addCPU := func(name string, target, exceed float64) {
		if cpu[name] == nil {
			cpu[name] = &cpuTally{target: target}
			cpuKeys = append(cpuKeys, name)
		}
		cpu[name].exceed = append(cpu[name].exceed, exceed)
	}
	var resRatio95, resRatio99 []float64
	breachFirstDay := map[int]int{}
	var breachOver []float64
	var breachExamples []string
	breachSmall := 0
	var bytesNew, bytesOld, bytesNeed float64
	frontier := map[string]*[3]float64{}
	var frontierKeys []string
	containers, shortDown, shortDownMem := 0, 0, 0

	for _, ns := range namespaces {
		g28 := newGrid(asOf, 28*day, 10*time.Minute)
		h28, err := fetchHistory(ctx, ch, cluster, scope{namespace: ns}, g28, pr, 28*day)
		if err != nil {
			t.Logf("%s: 28d not cached: %v", ns, err)
			continue
		}
		g14 := newGrid(asOf, 13*day, 5*time.Minute)
		h14, err14 := fetchHistory(ctx, ch, cluster, scope{namespace: ns}, g14, pr, 13*day)

		for k, p := range h28.cpu {
			if strings.HasSuffix(k.container, "POD") {
				continue
			}
			days := splitDays(p, h28.mem[k], g28.step)
			perDay := int(day / g28.step)
			containers++

			// ---- Step resolution: the same last 14 days at 10m and 5m ----
			if err14 != nil {
				t.Logf("%s: 14d fetch: %v", ns, err14)
			}
			if err14 == nil {
				p5, ok := h14.cpu[k]
				if !ok && containers%500 == 0 {
					t.Logf("no 5m series for %+v (%d keys at 5m)", k, len(h14.cpu))
				}
				if ok {
					d5 := splitDays(p5, nil, g14.step)
					var a, b []float64
					for _, d := range days[len(days)-13:] {
						a = append(a, d.cpu...)
					}
					for _, d := range d5 {
						b = append(b, d.cpu...)
					}
					slices.Sort(a)
					slices.Sort(b)
					if len(a) > 500 && len(b) > 1000 {
						if q5 := quantileSorted(b, 0.95); q5 >= 0.005 {
							resRatio95 = append(resRatio95, quantileSorted(a, 0.95)/q5)
						}
						if q5 := quantileSorted(b, 0.99); q5 >= 0.005 {
							resRatio99 = append(resRatio99, quantileSorted(a, 0.99)/q5)
						}
					} else if containers%500 == 0 {
						t.Logf("sizes %d %d", len(a), len(b))
					}
				}
			}

			for _, lh := range [][2]int{{3, 7}, {5, 7}, {7, 7}, {14, 7}, {21, 7}} {
				L, H := lh[0], lh[1]
				for o := 0; o+L+H <= len(days); o += 7 {
					train, test := days[o:o+L], days[o+L:o+L+H]
					ok := true
					for _, d := range append(slices.Clone(train), test...) {
						if d.cpuCov < 0.5 || d.memCov < 0.5 {
							ok = false
							break
						}
					}
					if !ok {
						continue
					}
					tag := fmt.Sprintf("train %2dd", L)

					// ---- Memory ----
					mb := toBlocks(train, func(d dayData) []float64 { return d.mem }, func(d dayData) float64 { return d.memCov }, perDay)
					var testMax float64
					for _, d := range test {
						for _, v := range d.mem {
							testMax = max(testMax, v)
						}
					}
					var trainMax float64
					var peaks []float64
					for _, d := range train {
						dm := 0.0
						for _, v := range d.mem {
							dm = max(dm, v)
						}
						peaks = append(peaks, dm)
						trainMax = max(trainMax, dm)
					}
					if trainMax >= 16*mib && testMax > 0 {
						for _, m := range []float64{0, 0.05, 0.10, 0.15, 0.30} {
							est := trainMax * (1 + m)
							addMem(fmt.Sprintf("%s  max x %.2f        ", tag, 1+m), testMax > est, est/testMax)
						}
						me := estimateMem(mb, perDay)
						if me.held.shift != nil {
							shortDownMem++
						}
						for _, v := range []struct {
							name          string
							margin, floor float64
						}{
							{"10%", 0.10, 0}, {"10%|16Mi", 0.10, 16 * mib},
							{"15%", 0.15, 0}, {"15%|16Mi", 0.15, 16 * mib}, {"15%|32Mi", 0.15, 32 * mib},
							{"20%|16Mi", 0.20, 16 * mib}, {"25%", 0.25, 0},
							{"30%", 0.30, 0}, {"30%|64Mi", 0.30, 64 * mib},
						} {
							if L != 14 {
								break
							}
							need, _ := memHeadroom(me.est, profileParams{memMargin: v.margin, memFloor: v.floor})
							est := roundMem(max(need, minMem))
							f := frontier[v.name]
							if f == nil {
								f = &[3]float64{}
								frontier[v.name] = f
								frontierKeys = append(frontierKeys, v.name)
							}
							f[0]++
							if testMax > est {
								f[1]++
							}
							f[2] += est - testMax
						}
						if L == 14 {
							need, _ := memHeadroom(me.est, ProfileBalanced.params())
							bytesNew += roundMem(max(need, minMem))
							bytesOld += roundMem(max(me.est*1.15, minMem))
							bytesNeed += testMax
						}
						for _, pp := range []Profile{ProfileAggressive, ProfileBalanced, ProfileConservative} {
							need, _ := memHeadroom(me.est, pp.params())
							est := roundMem(max(need, minMem))
							addMem(fmt.Sprintf("%s  engine %-12s ", tag, pp), testMax > est, est/testMax)
						}
						bal := roundMem(max(me.est*1.15, minMem)) // the old balanced rule
						// Variant: never follow a memory drop, however long.
						noDrop := me.est
						if me.shift != nil && me.shift.ratio < 1 {
							noDrop = trainMax * trendFactor(peaks)
						}
						nd := roundMem(max(noDrop*1.15, minMem))
						addMem(fmt.Sprintf("%s  cand: never drop    ", tag), testMax > nd, nd/testMax)
						abs := roundMem(max(bal, me.est+32*mib))
						addMem(fmt.Sprintf("%s  cand: bal, >=+32Mi  ", tag), testMax > abs, abs/testMax)
						if L == 14 && testMax > bal {
							// Why did it miss? First day over, and how far over.
							first := -1
							for i, d := range test {
								for _, v := range d.mem {
									if v > bal {
										first = i
										break
									}
								}
								if first >= 0 {
									break
								}
							}
							breachFirstDay[first]++
							breachOver = append(breachOver, testMax/bal)
							if trainMax < 128*mib {
								breachSmall++
							}
							if len(breachExamples) < 12 {
								breachExamples = append(breachExamples, fmt.Sprintf("%s/%s/%s/%s train max %s rec %s test max %s (first over day %d)",
									k.namespace, k.vns, k.wk, k.container, fmtBytes(trainMax), fmtBytes(bal), fmtBytes(testMax), first+1))
							}
						}
						sp := sortedCopy(peaks)
						vpa := quantileSorted(sp, 0.9) * 1.15
						addMem(fmt.Sprintf("%s  P90 daily peak x1.15", tag), testMax > vpa, vpa/testMax)
						if len(peaks) >= 5 {
							gl := gumbelReturnLevel(peaks, 30)
							addMem(fmt.Sprintf("%s  Gumbel 30-day level ", tag), testMax > gl, gl/testMax)
							mix := max(trainMax*1.15, gl)
							addMem(fmt.Sprintf("%s  max(maxx1.15,Gumbel)", tag), testMax > mix, mix/testMax)
						}
					}

					// ---- CPU ----
					cb := toBlocks(train, func(d dayData) []float64 { return d.cpu }, func(d dayData) float64 { return d.cpuCov }, perDay)
					var testCPU []float64
					for _, d := range test {
						testCPU = append(testCPU, d.cpu...)
					}
					slices.Sort(testCPU)
					all := sortedValues(cb)
					if len(all) == 0 || quantileSorted(all, 0.95) < 0.005 {
						continue
					}
					for _, q := range []float64{0.90, 0.95, 0.99} {
						ce := estimateCPU(cb, perDay, q)
						rec := roundCPU(max(ce.est, minCPU))
						addCPU(fmt.Sprintf("%s  engine P%.0f      ", tag, q*100), 1-q, fractionAbove(testCPU, rec))
						plain := roundCPU(max(quantileSorted(all, q), minCPU))
						addCPU(fmt.Sprintf("%s  plain  P%.0f      ", tag, q*100), 1-q, fractionAbove(testCPU, plain))
						if q == 0.95 && ce.shift != nil && ce.shift.ratio < 1 && len(train)-ce.from < 7 {
							shortDown++
							addCPU(fmt.Sprintf("%s  short drop: engine", tag), 1-q, fractionAbove(testCPU, rec))
							addCPU(fmt.Sprintf("%s  short drop: plain ", tag), 1-q, fractionAbove(testCPU, plain))
						}
					}
				}
			}
		}
	}

	t.Logf("containers: %d (namespaces %d)", containers, len(namespaces))
	t.Logf("")
	t.Logf("MEMORY: does the estimate cover the peak of the next 7 unseen days?")
	sort.Strings(memKeys)
	for _, k := range memKeys {
		t.Logf("  %s  %s", k, mem[k])
	}
	t.Logf("  engine held a short memory drop in %d cases", shortDownMem)
	for _, k := range frontierKeys {
		f := frontier[k]
		t.Logf("  frontier %-18s miss %4.1f%%  memory above next week's peak: %6.1f GiB", k, 100*f[1]/f[0], f[2]/gib)
	}
	t.Logf("  balanced, 14d train, summed over container-weeks: next-week peak %.1f GiB; old rule %.1f GiB (%.2fx); new rule %.1f GiB (%.2fx)",
		bytesNeed/gib, bytesOld/gib, bytesOld/bytesNeed, bytesNew/gib, bytesNew/bytesNeed)
	slices.Sort(breachOver)
	t.Logf("  balanced misses, 14d train: %d; first day over the recommendation %v; test peak / rec median %.2f p90 %.2f; train peak under 128Mi in %d",
		len(breachOver), breachFirstDay, quantileSorted(breachOver, 0.5), quantileSorted(breachOver, 0.9), breachSmall)
	for _, e := range breachExamples {
		t.Logf("    %s", e)
	}
	t.Logf("")
	t.Logf("CPU: share of the next 7 days' replica-time above the recommendation")
	sort.Strings(cpuKeys)
	for _, k := range cpuKeys {
		t.Logf("  %s  %s", k, cpu[k])
	}
	t.Logf("  engine followed a downward CPU shift with under 7 days behind it in %d cases", shortDown)
	t.Logf("")
	slices.Sort(resRatio95)
	slices.Sort(resRatio99)
	t.Logf("STEP: P95 from 10-minute samples / P95 from 5-minute samples, same 14 days: n=%d p10 %.3f median %.3f p90 %.3f",
		len(resRatio95), quantileSorted(resRatio95, 0.1), quantileSorted(resRatio95, 0.5), quantileSorted(resRatio95, 0.9))
	t.Logf("STEP: same for P99: n=%d p10 %.3f median %.3f p90 %.3f",
		len(resRatio99), quantileSorted(resRatio99, 0.1), quantileSorted(resRatio99, 0.5), quantileSorted(resRatio99, 0.9))
}
