package rightsizing

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	day = 24 * time.Hour

	bootstrapReps = 400
	// minDays is the least history a recommendation is made from at all.
	minDays = 3.0
	// minJobRuns is the same floor for Jobs and CronJobs, counted in runs.
	minJobRuns = 3
	// dayCoverageForStats is the share of a day's samples needed before that
	// day's P95 or peak joins the daily series used for shifts and trends.
	dayCoverageForStats = 0.5
	// trendHorizonDays is how far ahead a significant growth trend projects.
	trendHorizonDays = 7
	maxTrendFactor   = 1.5
	trendAlpha       = 0.01

	// Material-change floors: below these the difference is noise, not money.
	minCPUDelta = 0.025            // cores
	minMemDelta = 64 * 1024 * 1024 // bytes
	minCPU      = 0.010            // cores
	minMem      = 32 * 1024 * 1024 // bytes
	mib         = 1024 * 1024
	gib         = 1024 * mib

	// overFactor: even the pessimistic end of the need must sit below 80% of
	// the current request before calling it over-provisioned. Mirrored, a
	// request below 80% of the low end is under-provisioned by any amount.
	overFactor = 0.8
	// cpuLimitPressure: share of time at ≥95% of the CPU limit that means the
	// data is censored by throttling. throttleHigh: a replica throttled in
	// more than this share of its periods is throttled, and more than this
	// share of replica-time throttled censors the data.
	cpuLimitPressure = 0.01
	throttleHigh     = 0.05
	memLimitPressure = 0.9
	// oomStepUp is how far above the current limit memory goes after an OOM:
	// the observed peak is capped by the limit, so the true need is unknown.
	oomStepUp = 1.25
	// startupWindow is how long after a pod starts its CPU counts as startup.
	startupWindow = 10 * time.Minute
	// evtHorizonDays is the return period of the memory upper bound.
	evtHorizonDays = 30
	// peakHorizonDays is how long a recommendation is expected to stay in
	// place, for the chance its memory peak is beaten meanwhile.
	peakHorizonDays = 30
	// oomMinBump is the least an OOM kill raises memory by, whatever the
	// limit: 25% of a small limit can be less than one allocation burst, and
	// it would OOM again. The Kubernetes VPA uses the same floor.
	oomMinBump = 100 * mib
	// shrinkMinDays is the least history a recommendation goes down from: a
	// full week, so weekly peaks have been seen. Going up needs only minDays.
	shrinkMinDays = 7.0
	// newVersionDays: a rollout younger than this leaves most of the window
	// describing the version before.
	newVersionDays = 3
)

// Resources are a container's current requests and limits in cores and bytes.
type Resources struct {
	CPURequest, CPULimit, MemRequest, MemLimit float64
}

type hpaTarget struct {
	resource    string // cpu | memory
	utilization int32
	name        string
	// container is the one a ContainerResource metric scales on; empty for
	// a Resource metric, whose utilisation sums the pod's containers.
	container string
	// pod: the target sums several containers' requests, so it is set for
	// the workload once all of them are analysed.
	pod bool
}

// maxHPATarget is the highest utilisation target worth suggesting: above it
// an HPA has no room to react before pods saturate.
const maxHPATarget = 90

// containerInput is one workload container's aligned history. Every series
// has one entry per step from start; NaN marks a missing sample.
type containerInput struct {
	container string
	start     time.Time
	step      time.Duration
	// cpu is every replica's CPU at each step. mem is the busiest replica's
	// memory: memory has no slack, any replica over its limit is killed.
	cpu *pooled
	mem []float64
	// podMeans is each long-lived pod's mean CPU, for replica imbalance.
	podMeans []float64
	// cpuMax, cpuSum and replicas are derived from cpu by prepare.
	cpuMax, cpuSum, replicas []float64
	throttle                 []float64 // share of replicas throttled per step; nil when the cluster has no CFS series
	cpuBurst                 *pooled   // every replica's peak 2-minute rate per step; nil when not fetched
	throttleKind             string    // throttlePeriods or throttleSeconds
	memIsUsage               bool      // memory is total usage, page cache included
	oomTimes, restartTimes   []time.Time
	startupCPUPeak           float64 // NaN when unknown
	current                  Resources
	isJob                    bool
	hpa                      *hpaTarget
	profile                  Profile
	seed                     uint64
	jvm                      *JVMInfo
	versionSince             time.Time // zero when unknown
	// inPlaceResize: the cluster can resize running pods (Kubernetes 1.33+),
	// which a startup boost needs.
	inPlaceResize bool
	// memLimits is the memory limit each hour on the hourly grid, NaN where
	// unknown; nil without request history.
	memLimits []float64
	hourly    grid
	// vpa is the VerticalPodAutoscaler that sets this container's requests.
	vpa *vpaContainer
	// heap is a fixed heap ceiling of a runtime other than the JVM.
	heap *HeapCeiling
}

// dailyStat applies f to each day with enough steps covered and remembers
// which block each value came from.
func dailyStat(blocks []dayBlock, perDay int, f func(sorted []float64) float64) (vals []float64, idx []int) {
	for i, b := range blocks {
		if float64(b.present) < dayCoverageForStats*float64(perDay) || len(b.vals) == 0 {
			continue
		}
		v := b.vals
		if !b.sorted {
			v = sortedCopy(v)
		}
		vals = append(vals, f(v))
		idx = append(idx, i)
	}
	return vals, idx
}

// regime finds where the current behaviour starts. It returns the first block
// to use and, when a significant shift moved it later, the shift. A drop is
// only followed once the lower level has lasted dropMinDays; until then it is
// returned as held, with the block it starts at and how long it has lasted.
func regime(daily []float64, idx []int, dropMinDays int) (from int, shift *levelShift, held heldDrop) {
	s, ok := detectLevelShift(daily)
	if !ok {
		return 0, nil, heldDrop{}
	}
	if s.ratio < 1 && len(daily)-s.at < dropMinDays {
		return 0, nil, heldDrop{shift: &s, from: idx[s.at], days: len(daily) - s.at}
	}
	return idx[s.at], &s, heldDrop{}
}

// How long a lower level must hold before a recommendation follows it down.
// Rises count at once; a drop is where following noise costs throttling or an
// OOM kill. Two quiet days are often a weekend, and a drop right after a
// restart is usually a cold cache refilling.
//
// Both were set by replaying the engine on ~6,600 real containers, training
// on 7-21 days and scoring on the following unseen week. Following CPU drops
// younger than a week put that week over the request 4-8 times more often
// than ignoring them; at 14 days, following them is no longer worse than not.
// Memory drops that held two weeks still came back often enough that
// following any drop raised the miss rate at the same cost, so memory never
// follows one within the window: a shorter window is how to size for a new,
// lower level once it is trusted.
const (
	cpuDropMinDays = 14
	memDropMinDays = math.MaxInt
)

// heldDrop is a significant drop not acted on yet.
type heldDrop struct {
	shift *levelShift
	from  int // first block of the lower level
	days  int // how long it has lasted
}

// trendFactor projects a significant upward trend trendHorizonDays ahead and
// returns the growth multiplier (1 when there is no such trend).
func trendFactor(daily []float64) float64 {
	if len(daily) < 6 {
		return 1
	}
	s, p := mannKendall(daily)
	if s <= 0 || p >= trendAlpha {
		return 1
	}
	a, b := theilSen(daily)
	last := float64(len(daily) - 1)
	now := a + b*last
	if now <= 0 {
		return 1
	}
	return min(max((a+b*(last+trendHorizonDays))/now, 1), maxTrendFactor)
}

// cpuEstimate is the CPU need from a run of day blocks: the profile's
// quantile of pooled replica-time, projected forward when it is growing.
type cpuEstimate struct {
	est, trend float64
	shift      *levelShift
	from       int
	sorted     []float64
	held       heldDrop
}

func estimateCPU(blocks []dayBlock, perDay int, q float64) cpuEstimate {
	daily, idx := dailyStat(blocks, perDay, func(s []float64) float64 { return quantileSorted(s, 0.95) })
	from, shift, held := regime(daily, idx, cpuDropMinDays)
	used := blocks[from:]
	if shift != nil {
		daily = daily[shift.at:]
	}
	sorted := sortedValues(used)
	tf := trendFactor(daily)
	return cpuEstimate{est: quantileSorted(sorted, q) * tf, trend: tf, shift: shift, from: from, sorted: sorted, held: held}
}

type memEstimate struct {
	peak, est, upper, trend float64
	shift                   *levelShift
	from                    int
	sorted                  []float64
	dailyPeaks              []float64
	held                    heldDrop
	// earlierPeak: peak is from before the shift, higher than recent, the
	// peak since.
	earlierPeak bool
	recent      float64
}

func estimateMem(blocks []dayBlock, perDay int) memEstimate {
	daily, idx := dailyStat(blocks, perDay, func(s []float64) float64 { return s[len(s)-1] })
	from, shift, held := regime(daily, idx, memDropMinDays)
	used := blocks[from:]
	if shift != nil {
		daily = daily[shift.at:]
	}
	sorted := sortedValues(used)
	peak := math.NaN()
	if len(sorted) > 0 {
		peak = sorted[len(sorted)-1]
	}
	tf := trendFactor(daily)
	est := peak * tf
	// A rise is followed for the trend and the tail fit, where a step would
	// otherwise read as growth. It never lowers the estimate below a higher
	// peak before it, though: for a peak, dropping history only forgets
	// memory the container has already used, and too little gets it killed.
	recent, earlier := peak, false
	for _, b := range blocks[:from] {
		if len(b.vals) == 0 {
			continue
		}
		top := b.vals[len(b.vals)-1]
		if !b.sorted {
			top = maxOf(b.vals)
		}
		if top > peak {
			peak, earlier = top, true
		}
	}
	est = max(est, peak)
	var upper float64
	if len(daily) >= 5 {
		upper = gumbelReturnLevel(daily, evtHorizonDays) * tf
	} else {
		// Too few days to fit the tail: assume a bad month is 20% worse.
		upper = est * 1.2
	}
	if math.IsNaN(upper) || upper < est {
		upper = est
	}
	return memEstimate{peak: peak, est: est, upper: upper, trend: tf, earlierPeak: earlier, recent: recent, shift: shift, from: from, sorted: sorted, dailyPeaks: daily, held: held}
}

// memHeadroom adds the profile's headroom to a memory estimate and says which
// term set it.
func memHeadroom(est float64, p profileParams) (float64, string) {
	if rel := est * p.memMargin; rel >= p.memFloor {
		return est + rel, fmt.Sprintf("%.0f%%", p.memMargin*100)
	}
	return est + p.memFloor, fmt.Sprintf("the %s minimum, more than %.0f%% of a small peak", fmtBytes(p.memFloor), p.memMargin*100)
}

// startupBoost works out what a container needs while it starts, when its
// steady-state request is far below its startup CPU.
//
// Production systems keep the steady-state request and add CPU only during
// startup, resized back in place once the pod is Ready: the VPA's CPU startup
// boost (AEP-7862), GKE's startup boost, Google's kube-startup-cpu-boost, and
// CAST AI's startup mode, which keeps the original request during startup.
// Kanivet doesn't act, so it recommends the same: during startup, today's
// request (startup was fine with it) or the observed startup rate if higher,
// never above the limit. Without in-place resize there is nothing to boost
// with, so the steady recommendation itself stays at the observed startup
// rate: at that share, startup on a busy node takes about as long as it has.
// The startup rate is a step-long average, so it is a floor for the burst.
func startupBoost(in containerInput, startupPeak float64, cpu *ResourceRec) *StartupBoost {
	req := roundCPU(max(in.current.CPURequest, startupPeak))
	if in.current.CPULimit > 0 {
		req = min(req, in.current.CPULimit)
	}
	sb := &StartupBoost{Request: req, StartupRate: startupPeak, InPlace: in.inPlaceResize}
	if !in.inPlaceResize {
		floor := min(roundCPU(startupPeak), in.current.CPURequest)
		if floor > cpu.Recommended {
			sb.Floor, sb.SteadyRecommended = true, cpu.Recommended
			cpu.Recommended = floor
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("Kept at the %s startup rate rather than %s: without in-place resize there's no way to give it more CPU only while it starts.", fmtCores(floor), fmtCores(sb.SteadyRecommended)))
			if cpu.Recommended >= in.current.CPURequest && cpu.Verdict == VerdictOver {
				cpu.Verdict = VerdictRight
			}
		}
	}
	return sb
}

// fmtAge says how long ago, in hours under two days and days after.
func fmtAge(d time.Duration) string {
	if h := d.Hours(); h < 48 {
		return fmt.Sprintf("%.0f hours", math.Max(h, 1))
	}
	return fmt.Sprintf("%.0f days", d.Hours()/24)
}

// weekendBlocks says which 24h blocks fall mostly on a Saturday or Sunday
// (UTC), for resampling weekdays and weekends separately.
func weekendBlocks(blocks []dayBlock, start time.Time, step time.Duration, perDay int) []bool {
	out := make([]bool, len(blocks))
	for i, b := range blocks {
		mid := start.Add(time.Duration(b.start+perDay/2) * step).UTC().Weekday()
		out[i] = mid == time.Saturday || mid == time.Sunday
	}
	return out
}

// roundCPU rounds cores up to a value people write: 5m steps below 100m, 10m
// below a core, 50m above.
func roundCPU(c float64) float64 {
	m := c * 1000
	var step float64
	switch {
	case m < 100:
		step = 5
	case m < 1000:
		step = 10
	default:
		step = 50
	}
	return math.Ceil(m/step-1e-9) * step / 1000
}

// roundMem rounds bytes up: 16Mi steps below 1Gi, 64Mi below 4Gi, 256Mi above.
func roundMem(b float64) float64 {
	var step float64
	switch {
	case b < gib:
		step = 16 * mib
	case b < 4*gib:
		step = 64 * mib
	default:
		step = 256 * mib
	}
	return math.Ceil(b/step-1e-9) * step
}

// analyze turns one container's history into a recommendation with its
// evidence. It is deterministic for a given input.
func analyze(in containerInput) ContainerReport {
	p := in.profile.params()
	rng := rand.New(rand.NewPCG(in.seed, 0x9e3779b97f4a7c15))
	r := ContainerReport{Container: in.container, Findings: []Finding{}}

	in.prepare()
	n := len(in.mem)
	first, present := -1, 0
	for i := range n {
		if !math.IsNaN(in.mem[i]) || !math.IsNaN(in.cpuMax[i]) {
			if first < 0 {
				first = i
			}
			present++
		}
	}
	r.CPU = ResourceRec{Request: in.current.CPURequest, Limit: in.current.CPULimit, Explain: []string{}}
	r.Memory = ResourceRec{Request: in.current.MemRequest, Limit: in.current.MemLimit, Explain: []string{}}
	if first < 0 {
		// Nothing to go on: keep today's values everywhere.
		r.CPU.Recommended, r.CPU.RecommendedLimit, r.CPU.Verdict, r.CPU.LimitAction = in.current.CPURequest, in.current.CPULimit, VerdictInsufficient, "keep"
		r.Memory.Recommended, r.Memory.RecommendedLimit, r.Memory.Verdict, r.Memory.LimitAction = in.current.MemRequest, in.current.MemLimit, VerdictInsufficient, "keep"
		r.Verdict = VerdictInsufficient
		r.Confidence = "low"
		r.Findings = append(r.Findings, Finding{Code: "no-data", Severity: "info", Title: "No data", Message: "No usage history for this container in the window."})
		return r
	}
	span := float64(n-first) * float64(in.step) / float64(day)
	r.Data = DataQuality{
		Days:     span,
		Coverage: float64(present) / float64(n-first),
		Samples:  present,
		First:    in.start.Add(time.Duration(first) * in.step),
	}
	var repSum, repN float64
	for _, v := range in.replicas {
		if !math.IsNaN(v) {
			repSum += v
			repN++
		}
	}
	if repN > 0 {
		r.AvgReplicas = repSum / repN
	}
	if in.isJob {
		// CPU is a rate, which needs two samples inside its window: a run of a
		// minute or two has none, but its memory is still sampled.
		r.Data.Runs = max(countRuns(in.replicas), countRuns(in.mem))
		// A finished run's pod reserves nothing, so a Job pays for its request
		// only while a run is going. A service's gap is more likely lost
		// scrapes than no pods, so only Jobs get this.
		r.Data.DutyCycle = repN / float64(n-first)
	}
	r.OOMKills = len(in.oomTimes)
	r.Restarts = len(in.restartTimes)
	if !math.IsNaN(in.startupCPUPeak) && in.startupCPUPeak > 0 {
		r.StartupCPUPeak = in.startupCPUPeak
	}

	insufficient := span < minDays
	if in.isJob {
		// Few runs, but a long batch run can still have days of usage, which
		// is as much evidence as a service gets.
		active := float64(present) * float64(in.step) / float64(day)
		insufficient = r.Data.Runs < minJobRuns && active < minDays
	}
	// readyAt is when a young workload will have enough history: three days
	// to say anything, a week to recommend less.
	readyAt := r.Data.First.Add(time.Duration(minDays * float64(day)))
	if span >= minDays {
		readyAt = r.Data.First.Add(time.Duration(shrinkMinDays * float64(day)))
	}

	cpuBlocks, perDay := pooledBlocks(in.cpu, in.step)
	memBlocks, _ := seriesBlocks(in.mem, in.step)
	cpuBlocks, memBlocks = sortBlocks(cpuBlocks), sortBlocks(memBlocks)
	at := func(sample int) *time.Time {
		t := in.start.Add(time.Duration(sample) * in.step)
		return &t
	}

	// ---- CPU ----------------------------------------------------------------
	ce := estimateCPU(cpuBlocks, perDay, p.cpuQuantile)
	cpu := &r.CPU
	if len(ce.sorted) > 0 {
		cpu.P50 = quantileSorted(ce.sorted, 0.5)
		cpu.P90 = quantileSorted(ce.sorted, 0.9)
		cpu.P95 = quantileSorted(ce.sorted, 0.95)
		cpu.P99 = quantileSorted(ce.sorted, 0.99)
		cpu.Peak = ce.sorted[len(ce.sorted)-1]
		cpu.Estimate = ce.est
		if in.current.CPURequest > 0 {
			cpu.TimeAboveRequest = fractionAbove(ce.sorted, in.current.CPURequest)
		}
		used := cpuBlocks[ce.from:]
		ci := bootstrapQuantileDays(blockVals(used), weekendBlocks(used, in.start, in.step, perDay), p.cpuQuantile, ce.est/ce.trend, bootstrapReps, rng)
		cpu.Low, cpu.High = ci.lo*ce.trend, ci.hi*ce.trend
		if math.IsNaN(cpu.Low) {
			cpu.Low, cpu.High = ce.est, ce.est
		}
		cpu.Recommended = roundCPU(max(ce.est, minCPU))
		if ce.trend > 1 {
			cpu.TrendFactor = ce.trend
		}
		if ce.shift != nil {
			cpu.ShiftAt, cpu.ShiftRatio = at(cpuBlocks[ce.from].start), ce.shift.ratio
		}
		q := int(math.Round(p.cpuQuantile * 100))
		cpu.Explain = append(cpu.Explain, fmt.Sprintf("P%d of CPU across all replicas and every %s of the window: %s. The request covers %d%% of replica-time.", q, fmtStep(in.step), fmtCores(quantileSorted(ce.sorted, p.cpuQuantile)), q))
		if ce.shift != nil {
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("Only data since %s is used: CPU changed %.1f× then.", cpu.ShiftAt.Format("Jan 2"), ce.shift.ratio))
		}
		if h := ce.held; h.shift != nil {
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("CPU dropped %.1f× on %s, but the whole window is still used: a lower level only counts once it has held %d days (%d so far), so a quiet weekend isn't taken for the new normal.", 1/h.shift.ratio, at(cpuBlocks[h.from].start).Format("Jan 2"), cpuDropMinDays, h.days))
		}
		if ce.trend > 1 {
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("Growing steadily (Mann-Kendall p < %.2f): projected %d days ahead, +%.0f%% → %s.", trendAlpha, trendHorizonDays, (ce.trend-1)*100, fmtCores(ce.est)))
		}
		if !math.IsNaN(ci.lo) {
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("90%% interval from resampling whole days: %s – %s.", fmtCores(cpu.Low), fmtCores(cpu.High)))
		}
		cpu.Explain = append(cpu.Explain, fmt.Sprintf("Rounded up to %s.", fmtCores(cpu.Recommended)))
		applyBurst(cpu, ce.sorted)
	}

	// CPU limit: usage pressed against it means the data is censored.
	cpu.LimitAction = "none"
	if in.current.CPULimit > 0 {
		cpu.LimitAction = "keep"
		cpu.RecommendedLimit = in.current.CPULimit
		atLimit := fractionAbove(ce.sorted, 0.95*in.current.CPULimit)
		throttled, worst := false, 0.0
		if in.throttle != nil {
			// Each step holds the share of replicas throttled more than
			// throttleHigh of the time. Weighted by the replicas running then,
			// the mean is that share of replica-time: the same for 2 replicas
			// as for 60, as CPU is, and a peak an HPA scaled out for counts
			// for every replica it ran.
			var sum, weight float64
			for i, s := range in.throttle {
				if math.IsNaN(s) {
					continue
				}
				k := 1.0
				if i < len(in.replicas) && in.replicas[i] > 0 {
					k = in.replicas[i]
				}
				sum, weight, worst = sum+s*k, weight+k, max(worst, s)
			}
			if weight > 0 {
				share := min(sum/weight, 1)
				r.Throttling = &share
				switch {
				case in.throttleKind == throttlePeriods:
					throttled = share > throttleHigh
				case share > throttleHigh:
					// Throttled seconds per second is only roughly the share
					// of time throttled, so it supports a verdict, never makes one.
					r.Findings = append(r.Findings, Finding{Code: "cpu-throttled", Severity: "warning", Resource: "cpu",
						Message: fmt.Sprintf("Throttled more than roughly %.0f%% of the time in %.0f%% of replica-time. Its %s limit is holding it back.", throttleHigh*100, share*100, fmtCores(in.current.CPULimit))})
				}
			}
		}
		// Short bursts against the limit, when no CFS period counters say how
		// often it throttles: the share of replica-time within minutes of it.
		burstAtLimit := 0.0
		if !throttled && in.throttleKind != throttlePeriods && in.cpuBurst != nil {
			burstAtLimit = fractionAboveSeries(in.cpuBurst.vals, 0.9*in.current.CPULimit)
		}
		switch {
		case burstAtLimit > cpuLimitPressure && atLimit <= cpuLimitPressure:
			cpu.Censored = true
			cpu.LimitAction = "raise"
			cpu.RecommendedLimit = roundCPU(max(in.current.CPULimit*1.5, cpu.Recommended*1.5))
			r.Findings = append(r.Findings, Finding{Code: "cpu-burst-limit", Severity: "warning", Resource: "cpu",
				Message: fmt.Sprintf("Its 5-minute averages stay under the %s limit, but within minutes it reaches 90%% of it in %.1f%% of replica-time. The limit throttles those bursts, so its real demand is higher than the averages show.", fmtCores(in.current.CPULimit), burstAtLimit*100)})
		case atLimit > cpuLimitPressure || throttled:
			cpu.Censored = true
			cpu.LimitAction = "raise"
			cpu.RecommendedLimit = roundCPU(max(in.current.CPULimit*1.5, cpu.Recommended*1.5))
			msg := fmt.Sprintf("CPU sits at its %s limit %.1f%% of the time, so it is being throttled and its real demand is higher than what was measured.", fmtCores(in.current.CPULimit), atLimit*100)
			if throttled {
				msg = fmt.Sprintf("CPU is throttled in more than %.0f%% of scheduling periods for %.0f%% of replica-time (at worst, %.0f%% of replicas at once), so its real demand is higher than what was measured.", throttleHigh*100, *r.Throttling*100, worst*100)
			}
			r.Findings = append(r.Findings, Finding{Code: "cpu-limit-pressure", Severity: "critical", Resource: "cpu", Message: msg})
		case cpu.Recommended > in.current.CPULimit:
			cpu.LimitAction = "raise"
			ratio := 1.5
			if in.current.CPURequest > 0 {
				ratio = max(ratio, in.current.CPULimit/in.current.CPURequest)
			}
			cpu.RecommendedLimit = roundCPU(cpu.Recommended * ratio)
		}
	}

	// ---- Memory -------------------------------------------------------------
	me := estimateMem(memBlocks, perDay)
	mem := &r.Memory
	if len(me.sorted) > 0 {
		mem.P50 = quantileSorted(me.sorted, 0.5)
		mem.P90 = quantileSorted(me.sorted, 0.9)
		mem.P95 = quantileSorted(me.sorted, 0.95)
		mem.P99 = quantileSorted(me.sorted, 0.99)
		mem.Peak = me.peak
		mem.Estimate = me.est
		if in.current.MemRequest > 0 {
			mem.TimeAboveRequest = fractionAbove(me.sorted, in.current.MemRequest)
		}
		need, why := memHeadroom(me.est, p)
		mem.Recommended = roundMem(max(need, minMem))
		mem.Low, mem.High = need, need
		if me.est > 0 {
			mem.High = max(need, me.upper*need/me.est)
		}
		if me.trend > 1 {
			mem.TrendFactor = me.trend
		}
		if me.shift != nil {
			mem.ShiftAt, mem.ShiftRatio = at(memBlocks[me.from].start), me.shift.ratio
		}
		if nd := len(me.dailyPeaks); nd > 0 {
			r.Data.NewPeakChance = peakHorizonDays / float64(nd+peakHorizonDays)
		}
		if in.memIsUsage {
			mem.Explain = append(mem.Explain, fmt.Sprintf("Peak memory of the busiest replica: %s. This cluster only reports total usage, which includes page cache the kernel can reclaim, so the real need is likely lower and this errs high.", fmtBytes(me.peak)))
		} else {
			mem.Explain = append(mem.Explain, fmt.Sprintf("Peak working set of the busiest replica: %s.", fmtBytes(me.peak)))
		}
		switch {
		case me.earlierPeak:
			mem.Explain = append(mem.Explain, fmt.Sprintf("Memory changed %.1f× on %s, so its trend and worst day are read from data since then, which peaks at %s. The higher peak before still counts: memory it has used once, it can need again.", me.shift.ratio, mem.ShiftAt.Format("Jan 2"), fmtBytes(me.recent)))
		case me.shift != nil:
			mem.Explain = append(mem.Explain, fmt.Sprintf("Only data since %s is used: memory changed %.1f× then.", mem.ShiftAt.Format("Jan 2"), me.shift.ratio))
		}
		if h := me.held; h.shift != nil {
			mem.Explain = append(mem.Explain, fmt.Sprintf("Memory dropped %.1f× on %s (%d days ago), but the whole window's peak still counts: memory never follows a drop, because drops often come back (caches refill after a restart) and too little memory gets the pod OOM-killed. Once the lower level is the norm, a shorter window sizes for it.", 1/h.shift.ratio, at(memBlocks[h.from].start).Format("Jan 2"), h.days))
		}
		if me.trend > 1 {
			mem.Explain = append(mem.Explain, fmt.Sprintf("Growing steadily (Mann-Kendall p < %.2f): projected %d days ahead, +%.0f%% → %s.", trendAlpha, trendHorizonDays, (me.trend-1)*100, fmtBytes(me.recent*me.trend)))
		}
		mem.Explain = append(mem.Explain, fmt.Sprintf("Plus %s headroom (%s) → %s, rounded up to %s.", fmtBytes(need-me.est), why, fmtBytes(need), fmtBytes(mem.Recommended)))
	}
	// An OOM kill puts the true peak above the limit it was killed at, so
	// memory goes a step past that limit. Once today's limit is already past
	// the step, the kills are dealt with: stepping again from today's limit
	// would raise memory on every refresh until they leave the window. The
	// step still holds as a floor, the way the VPA keeps an OOM in its
	// history.
	oomActive := r.OOMKills > 0
	if r.OOMKills > 0 && in.current.MemLimit > 0 {
		r.OOMLimit = in.oomLimit()
		floor := roundMem(max(r.OOMLimit*oomStepUp, r.OOMLimit+oomMinBump))
		oomActive = in.current.MemLimit < floor
		mem.Censored = oomActive
		if floor > mem.Recommended {
			mem.Recommended = floor
			mem.Explain = append(mem.Explain, fmt.Sprintf("Raised to %s (%.0f%% or %s over the %s limit it was OOM-killed at, whichever is more): its true peak is above that limit and was never measured.", fmtBytes(floor), (oomStepUp-1)*100, fmtBytes(oomMinBump), fmtBytes(r.OOMLimit)))
		}
		mem.High = max(mem.High, mem.Recommended)
	}
	noOOM := "there were none"
	if r.OOMKills > 0 {
		noOOM = "there were none since its limit was raised"
	}
	// Memory request = limit: memory can't be throttled, only killed, so a
	// limit above the request just moves the OOM to a worse moment.
	mem.RecommendedLimit = mem.Recommended
	mem.LimitAction = "set"
	// Total usage counts page cache, which grows to fill whatever limit there
	// is. Usage below the request still proves the need is lower, but usage
	// above it proves nothing, so without an OOM kill the request stays.
	cacheBlind := in.memIsUsage && !oomActive && in.current.MemRequest > 0
	if cacheBlind && mem.Recommended > in.current.MemRequest {
		mem.Recommended = in.current.MemRequest
		mem.RecommendedLimit = max(in.current.MemLimit, in.current.MemRequest)
		mem.Low, mem.High = mem.Recommended, mem.Recommended
		mem.Explain = append(mem.Explain, fmt.Sprintf("Kept at the current %s: usage above it may be page cache, which this cluster's metrics can't separate from what the app needs. OOM kills would show real pressure, and %s.", fmtBytes(in.current.MemRequest), noOOM))
	}
	// A JVM with a fixed heap needs the heap plus its off-heap memory, whatever
	// usage showed so far: a heap that has not filled up yet will.
	var jvmFloor float64
	if in.jvm != nil {
		r.JVM = in.jvm
		if in.jvm.HeapMax > 0 {
			jvmFloor = roundMem(jvmMemoryFloor(in.jvm.HeapMax))
			if mem.Recommended < jvmFloor {
				mem.Recommended, mem.RecommendedLimit = jvmFloor, jvmFloor
				mem.High = max(mem.High, jvmFloor)
				mem.Explain = append(mem.Explain, fmt.Sprintf("Raised to %s: the JVM may grow its heap to %s (-Xmx), and the limit must also hold what it uses off-heap (metaspace, code cache, threads, buffers). A heap that hasn't filled up yet will.", fmtBytes(jvmFloor), fmtBytes(in.jvm.HeapMax)))
			}
		}
	}
	// So does any other runtime given a fixed heap ceiling.
	var heapFloor float64
	if h := in.heap; h != nil {
		r.Heap = h
		heapFloor = roundMem(jvmMemoryFloor(h.Max))
		if mem.Recommended < heapFloor {
			mem.Recommended, mem.RecommendedLimit = heapFloor, heapFloor
			mem.High = max(mem.High, heapFloor)
			mem.Explain = append(mem.Explain, fmt.Sprintf("Raised to %s: %s may grow its heap to %s (%s), and the limit must also hold what it uses outside the heap (buffers, native memory, compiled code). A heap that hasn't filled up yet will.", fmtBytes(heapFloor), h.runtimeName(), fmtBytes(h.Max), h.Setting))
		}
	}
	if in.current.MemLimit == mem.RecommendedLimit && in.current.MemRequest == mem.Recommended {
		mem.LimitAction = "keep"
	}

	// ---- Per-resource verdicts ---------------------------------------------
	cpu.Verdict = VerdictRight
	switch {
	case len(ce.sorted) == 0:
		cpu.Verdict = VerdictInsufficient
	case in.current.CPURequest == 0:
		cpu.Verdict = VerdictNoRequests
	case cpu.Censored:
		cpu.Verdict = VerdictUnder
	// Under needs no margin beyond the interval: a request exceeded more than
	// twice as often as the profile allows is the same failure the backtest
	// and the change check use. The material-change floor keeps requests near
	// the need from flipping on rounding, but a small one below 80% of even
	// the low end of it is under however few millicores short.
	case (in.current.CPURequest < cpu.Low || cpu.TimeAboveRequest > 2*(1-p.cpuQuantile)) &&
		(cpu.Recommended-in.current.CPURequest >= minCPUDelta || in.current.CPURequest < overFactor*cpu.Low):
		cpu.Verdict = VerdictUnder
		r.Findings = append(r.Findings, Finding{Code: "cpu-over-request", Severity: "warning", Resource: "cpu",
			Message: fmt.Sprintf("Uses more CPU than it requests %.0f%% of the time. It depends on spare node capacity and slows down when neighbours are busy.", cpu.TimeAboveRequest*100)})
	case cpu.High < overFactor*in.current.CPURequest && in.current.CPURequest-cpu.Recommended >= minCPUDelta:
		cpu.Verdict = VerdictOver
	}
	// Going down needs a full week of history: shorter windows missed the
	// next week's peak noticeably more often when replayed on real data.
	tooYoungToShrink := !in.isJob && span < shrinkMinDays
	if tooYoungToShrink && cpu.Verdict == VerdictOver {
		cpu.Verdict = VerdictInsufficient
	}

	if r.OOMKills > 0 && !oomActive {
		r.Findings = append(r.Findings, Finding{Code: "oom-resolved", Severity: "info", Resource: "memory",
			Message: fmt.Sprintf("OOM-killed %s at a %s limit, since raised to %s with no kill at it. Memory stays at least %s, a step over the limit it was killed at.", times(r.OOMKills), fmtBytes(r.OOMLimit), fmtBytes(in.current.MemLimit), fmtBytes(roundMem(max(r.OOMLimit*oomStepUp, r.OOMLimit+oomMinBump))))})
	}
	mem.Verdict = VerdictRight
	switch {
	case len(me.sorted) == 0:
		mem.Verdict = VerdictInsufficient
	case oomActive:
		mem.Verdict = VerdictUnder
		msg := fmt.Sprintf("OOM-killed %s in the window. Its real peak is above the %s limit and was never measured.", times(r.OOMKills), fmtBytes(r.OOMLimit))
		if in.current.MemLimit == 0 {
			msg = fmt.Sprintf("OOM-killed %s in the window with no memory limit set, so the node ran out of memory. A request that covers its real peak keeps the scheduler from overcommitting the node.", times(r.OOMKills))
		}
		r.Findings = append(r.Findings, Finding{Code: "oom-killed", Severity: "critical", Resource: "memory", Message: msg})
	case jvmFloor > 0 && in.current.MemLimit > 0 && in.current.MemLimit < jvmFloor:
		mem.Verdict = VerdictUnder
		r.Findings = append(r.Findings, Finding{Code: "jvm-heap-over-limit", Severity: "critical", Resource: "memory",
			Message: fmt.Sprintf("Its JVM may grow the heap to %s (-Xmx), which with off-heap memory needs about %s, more than the %s limit. Once the heap fills, the kernel kills it before the JVM's own limit is reached.", fmtBytes(in.jvm.HeapMax), fmtBytes(jvmFloor), fmtBytes(in.current.MemLimit))})
	case heapFloor > 0 && in.current.MemLimit > 0 && in.current.MemLimit < heapFloor:
		mem.Verdict = VerdictUnder
		r.Findings = append(r.Findings, Finding{Code: "heap-over-limit", Severity: "critical", Resource: "memory",
			Message: fmt.Sprintf("%s may grow its heap to %s (%s), which with what it uses outside the heap needs about %s, more than the %s limit. Once the heap fills, the kernel kills it before %s's own limit is reached.", in.heap.runtimeName(), fmtBytes(in.heap.Max), in.heap.Setting, fmtBytes(heapFloor), fmtBytes(in.current.MemLimit), in.heap.runtimeName())})
	case in.current.MemRequest == 0:
		mem.Verdict = VerdictNoRequests
	case cacheBlind && (me.peak > in.current.MemRequest || (in.current.MemLimit > 0 && me.peak > memLimitPressure*in.current.MemLimit)):
		// Page cache grows until it meets the limit, so usage at the limit is
		// what a healthy cache-heavy app looks like on this metric.
		against := fmt.Sprintf("a %s request", fmtBytes(in.current.MemRequest))
		if in.current.MemLimit > 0 && me.peak > memLimitPressure*in.current.MemLimit {
			against = fmt.Sprintf("%.0f%% of its %s limit", me.peak/in.current.MemLimit*100, fmtBytes(in.current.MemLimit))
		}
		r.Findings = append(r.Findings, Finding{Code: "memory-cache-ambiguous", Severity: "info", Resource: "memory",
			Message: fmt.Sprintf("Total memory usage, page cache included, reaches %s, %s. Cache is reclaimable and this cluster doesn't report working set, so this alone isn't a sign of pressure. OOM kills would be, and %s.", fmtBytes(me.peak), against, noOOM)})
	case in.current.MemLimit > 0 && me.peak > memLimitPressure*in.current.MemLimit:
		mem.Verdict = VerdictUnder
		r.Findings = append(r.Findings, Finding{Code: "memory-near-limit", Severity: "critical", Resource: "memory",
			Message: fmt.Sprintf("Peaked at %.0f%% of its %s limit. One bad day away from an OOM kill.", me.peak/in.current.MemLimit*100, fmtBytes(in.current.MemLimit))})
	case me.peak > in.current.MemRequest:
		mem.Verdict = VerdictUnder
		r.Findings = append(r.Findings, Finding{Code: "memory-over-request", Severity: "warning", Resource: "memory",
			Message: fmt.Sprintf("Peaks at %s, above its %s request. Under node memory pressure it is among the first to be evicted.", fmtBytes(me.peak), fmtBytes(in.current.MemRequest))})
	case mem.High < overFactor*in.current.MemRequest && in.current.MemRequest-mem.Recommended >= minMemDelta:
		mem.Verdict = VerdictOver
	}
	if tooYoungToShrink && mem.Verdict == VerdictOver {
		mem.Verdict = VerdictInsufficient
	}

	if cpu.Verdict == VerdictNoRequests || mem.Verdict == VerdictNoRequests {
		missing := "CPU or memory requests"
		switch {
		case in.current.CPURequest > 0:
			missing = "a memory request"
		case in.current.MemRequest > 0:
			missing = "a CPU request"
		}
		r.Findings = append(r.Findings, Finding{Code: "no-requests", Severity: "warning",
			Message: fmt.Sprintf("Sets no %s, so the scheduler places it blind and it is among the first evicted under pressure. Its usage supports %s CPU and %s memory.", missing, fmtCores(cpu.Recommended), fmtBytes(mem.Recommended))})
	}
	if s := injectedSidecar(in.container); s != "" {
		r.Findings = append(r.Findings, Finding{Code: "injected-sidecar", Severity: "info",
			Message: fmt.Sprintf("%s injects this container and rewrites its resources at admission, so change them with the pod template annotations in the output below, not the container spec.", s)})
	}
	if v := in.vpa; v != nil {
		msg := fmt.Sprintf("VerticalPodAutoscaler %s (updateMode %s) sets this container's %s requests itself, so today's requests are the VPA's and a change to the pod template won't last. Steer the VPA instead, with minAllowed and maxAllowed in its resourcePolicy. Its savings are left out of the totals.", v.name, v.mode, strings.Join(v.resources, " and "))
		var rec []string
		if v.target.CPURequest > 0 && v.sets("cpu") {
			rec = append(rec, fmtCores(v.target.CPURequest)+" CPU")
		}
		if v.target.MemRequest > 0 && v.sets("memory") {
			rec = append(rec, fmtBytes(v.target.MemRequest)+" memory")
		}
		if len(rec) > 0 {
			msg += fmt.Sprintf(" The VPA currently recommends %s.", strings.Join(rec, " and "))
		}
		r.Findings = append(r.Findings, Finding{Code: "vpa-managed", Severity: "warning", Message: msg})
	}
	if cpu.Verdict == VerdictOver {
		r.Findings = append(r.Findings, Finding{Code: "cpu-over-provisioned", Severity: "info", Resource: "cpu",
			Message: fmt.Sprintf("Requests %s but stays under %s for %.0f%% of replica-time. Even the top of the 90%% interval (%s) leaves %.0f%% of the request unused.",
				fmtCores(in.current.CPURequest), fmtCores(cpu.Estimate), p.cpuQuantile*100, fmtCores(cpu.High), (1-cpu.High/in.current.CPURequest)*100)})
	}
	if mem.Verdict == VerdictOver {
		r.Findings = append(r.Findings, Finding{Code: "memory-over-provisioned", Severity: "info", Resource: "memory",
			Message: fmt.Sprintf("Requests %s but never used more than %s. Even a worst-day-in-a-month estimate with headroom (%s) leaves %.0f%% of it unused.",
				fmtBytes(in.current.MemRequest), fmtBytes(me.peak), fmtBytes(mem.High), (1-mem.High/in.current.MemRequest)*100)})
	}

	// ---- What to change -----------------------------------------------------
	// Without data, or within the noise of today's value, the recommendation
	// is today's value: a number that isn't acted on must not move money or
	// end up in a patch.
	if cpu.Verdict == VerdictInsufficient || cpu.Verdict == VerdictRight {
		if cpu.Verdict == VerdictRight && len(cpu.Explain) > 0 && cpu.Recommended != in.current.CPURequest {
			cpu.Explain = append(cpu.Explain, fmt.Sprintf("Within the noise of the current %s request, so keep it.", fmtCores(in.current.CPURequest)))
		}
		cpu.Recommended = in.current.CPURequest
		cpu.RecommendedLimit = in.current.CPULimit
		cpu.LimitAction = "none"
		if in.current.CPULimit > 0 {
			cpu.LimitAction = "keep"
		}
	}
	if cpu.Censored {
		// Throttled at the limit: the measured CPU is a floor, never a reason
		// to go down.
		cpu.Recommended = max(cpu.Recommended, roundCPU(in.current.CPURequest))
	}
	if (mem.Verdict == VerdictInsufficient || mem.Verdict == VerdictRight) && in.current.MemRequest > 0 || mem.Verdict == VerdictInsufficient {
		if mem.Verdict == VerdictRight && len(mem.Explain) > 0 && mem.Recommended != in.current.MemRequest {
			mem.Explain = append(mem.Explain, fmt.Sprintf("Within the noise of the current %s request, so keep it.", fmtBytes(in.current.MemRequest)))
		}
		mem.Recommended = in.current.MemRequest
		mem.RecommendedLimit = in.current.MemLimit
		mem.LimitAction = "keep"
	}

	// ---- HPA coupling ------------------------------------------------------
	// The HPA scales on usage / request, so request and target move together:
	// target × old / new keeps today's replica counts. Going down, a request
	// below old × target / maxHPATarget would need a target past
	// maxHPATarget, so that is as far as it goes. The target itself is set
	// once the request is final, after the startup floor below.
	var hpaRes *ResourceRec
	var usageOnly float64
	if in.hpa != nil && in.hpa.utilization > 0 {
		hpaRes = &r.CPU
		if in.hpa.resource == "memory" {
			hpaRes = &r.Memory
		}
		T := float64(in.hpa.utilization)
		if res := hpaRes; res.Verdict == VerdictOver && res.Request > 0 && res.Recommended > 0 && T*res.Request/res.Recommended > maxHPATarget {
			usageOnly = res.Recommended
			if in.hpa.resource == "memory" {
				res.Recommended = roundMem(res.Request * T / maxHPATarget)
				if res.RecommendedLimit > 0 {
					res.RecommendedLimit = res.Recommended
				}
			} else {
				res.Recommended = roundCPU(res.Request * T / maxHPATarget)
			}
		}
	}

	// ---- Supporting evidence -----------------------------------------------
	// Imbalance: the hottest long-lived pod's mean CPU against the median
	// pod's. Interchangeable replicas converge to the same mean over a day or
	// more; a persistently hot one is a sharding or routing problem the
	// request can't fix.
	if len(in.podMeans) >= 2 {
		pm := sortedCopy(in.podMeans)
		if med := quantileSorted(pm, 0.5); med > 0 {
			r.Imbalance = pm[len(pm)-1] / med
			if r.Imbalance >= 2 && pm[len(pm)-1]-med >= minCPUDelta {
				r.Findings = append(r.Findings, Finding{Code: "replica-imbalance", Severity: "warning", Resource: "cpu",
					Message: fmt.Sprintf("Replicas are unevenly loaded: the busiest pod averages %.1f× the typical one over its lifetime. The request covers replica-time as a whole, so that pod will run hotter than the rest; fixing the load balancing or sharding matters more than the request.", r.Imbalance)})
			}
		}
	}
	if r.StartupCPUPeak > 0 && cpu.Recommended > 0 && r.StartupCPUPeak > 2*cpu.Recommended {
		msg := fmt.Sprintf("Peaks at %s in its first %d minutes after start, %.0f× the steady-state request.", fmtCores(r.StartupCPUPeak), int(startupWindow.Minutes()), r.StartupCPUPeak/cpu.Recommended)
		sev := "info"
		if !in.isJob && in.current.CPURequest > cpu.Recommended {
			sb := startupBoost(in, r.StartupCPUPeak, cpu)
			r.StartupBoost = sb
			switch {
			case sb.Floor:
				msg += fmt.Sprintf(" A CPU request is the share a container gets when its node is busy, so a request that low makes startup crawl there, long enough for probes to give up. This cluster can't resize pods in place (that needs Kubernetes 1.33 or newer), so the recommendation stays at the startup rate, %s, instead of the %s steady state alone supports.", fmtCores(cpu.Recommended), fmtCores(sb.SteadyRecommended))
			default:
				msg += fmt.Sprintf(" A CPU request is the share a container gets when its node is busy, so a request that low makes startup crawl there, long enough for probes to give up. Pair the change with a startup boost: %s while it starts, %s once it is Ready, resized in place, so the saving stays.", fmtCores(sb.Request), fmtCores(cpu.Recommended))
			}
		} else {
			msg += " Startup is left out of the steady-state numbers, so startup will be slower with a lower request."
		}
		if in.current.CPULimit > 0 && r.StartupCPUPeak >= 0.95*in.current.CPULimit {
			msg += fmt.Sprintf(" The %s limit already throttles it at startup.", fmtCores(in.current.CPULimit))
			sev = "warning"
		}
		r.Findings = append(r.Findings, Finding{Code: "startup-spike", Severity: sev, Resource: "cpu", Message: msg})
	}
	if hpaRes != nil {
		coupleHPA(&r, in.hpa, hpaRes, usageOnly)
	}
	if nonOOM := r.Restarts - r.OOMKills; nonOOM > 0 {
		r.Findings = append(r.Findings, Finding{Code: "restarts", Severity: "info",
			Message: fmt.Sprintf("Restarted %s for reasons other than memory. Crash loops distort usage; check the logs.", times(nonOOM))})
	}
	if cpu.ShiftAt != nil {
		r.Findings = append(r.Findings, Finding{Code: "regime-change", Severity: "info", Resource: "cpu",
			Message: fmt.Sprintf("CPU changed %.1f× on %s. Only data since then is used.", cpu.ShiftRatio, cpu.ShiftAt.Format("Jan 2"))})
	}
	if mem.ShiftAt != nil {
		msg := fmt.Sprintf("Memory changed %.1f× on %s. Only data since then is used.", mem.ShiftRatio, mem.ShiftAt.Format("Jan 2"))
		if me.earlierPeak {
			msg = fmt.Sprintf("Memory changed %.1f× on %s. Its trend is read from data since then, but the higher %s peak before still sets the floor.", mem.ShiftRatio, mem.ShiftAt.Format("Jan 2"), fmtBytes(me.peak))
		}
		r.Findings = append(r.Findings, Finding{Code: "regime-change", Severity: "info", Resource: "memory", Message: msg})
	}
	// On total usage, growth is usually page cache filling up, not a leak.
	if me.trend > 1 && !(in.memIsUsage && !oomActive) {
		r.Findings = append(r.Findings, Finding{Code: "growth", Severity: "warning", Resource: "memory",
			Message: fmt.Sprintf("Memory is climbing steadily, +%.0f%% projected over the next week. If it never levels off, look for a leak.", (me.trend-1)*100)})
	}

	// ---- Backtest ----------------------------------------------------------
	if !in.isJob {
		r.Backtest = backtest(cpuBlocks, memBlocks, perDay, p)
	}

	// ---- Overall -----------------------------------------------------------
	// ---- Things the history can't fully speak for ---------------------------
	cautious := false
	if in.jvm != nil && in.jvm.HeapMax == 0 {
		r.Findings = append(r.Findings, Finding{Code: "jvm-heap-follows-limit", Severity: "info", Resource: "memory",
			Message: fmt.Sprintf("A JVM with its heap sized from the memory limit (%.0f%% of it). It lets the heap grow towards that share of whatever limit it gets, so part of the memory seen is there because the limit allowed it, and a smaller limit lowers the heap ceiling too, which shows up as more garbage collection rather than an OOM kill. Set -Xmx to make its memory predictable, and watch GC after resizing.", in.jvm.RAMPercentage)})
		cautious = cautious || mem.Recommended != in.current.MemRequest
	}
	if !in.versionSince.IsZero() {
		end := in.start.Add(time.Duration(len(in.mem)) * in.step)
		since := in.versionSince.UTC()
		r.VersionSince = &since
		// Only when the window holds at least a day of the previous version:
		// for a brand-new workload, all of its history is this version.
		if age := end.Sub(since); age >= 0 && age < newVersionDays*day && r.Data.First.Add(day).Before(since) {
			r.Findings = append(r.Findings, Finding{Code: "new-version", Severity: "info",
				Message: fmt.Sprintf("The running version was rolled out %s ago, so most of this history comes from the one before it. New code can use resources differently; recheck in a few days.", fmtAge(age))})
			cautious = true
		}
	}
	r.Confidence = confidence(r, insufficient)
	if cautious && r.Confidence == "high" {
		r.Confidence = "medium"
	}
	r.Verdict = overallVerdict(r, insufficient)
	if r.Verdict == VerdictInsufficient {
		msg := fmt.Sprintf("Not enough history yet: %.0f of %.0f days. A recommendation appears around %s.", math.Floor(span), minDays, readyAt.Format("Jan 2, 15:04 MST"))
		if !insufficient {
			msg = fmt.Sprintf("Usage suggests less, but %.0f days is short of the full week needed to recommend going down: weekly peaks may not have happened yet. A recommendation appears around %s.", math.Floor(span), readyAt.Format("Jan 2, 15:04 MST"))
		}
		switch {
		case in.isJob && !insufficient:
			// Enough runs, yet one resource has nothing: every run ends before
			// a CPU rate can be taken. Waiting won't change that.
			msg = fmt.Sprintf("Each run finishes within a few minutes, too quickly to measure CPU at a %s step. Memory is measured, but a workload with unknown CPU isn't called right-sized.", fmtStep(in.step))
		case in.isJob:
			msg = fmt.Sprintf("Not enough history yet: %d of %d runs. Each run adds evidence; so would a run lasting %.0f days.", r.Data.Runs, minJobRuns, minDays)
		}
		r.Findings = append(r.Findings, Finding{Code: "insufficient-data", Severity: "info", Message: msg})
	}
	if r.Verdict == VerdictRight {
		r.Findings = append(r.Findings, Finding{Code: "right-sized", Severity: "good", Message: "Requests match observed usage. Nothing to change."})
	}
	if b := r.CPU.Burst; b != nil && r.CPU.Recommended > b.IdleRecommended {
		r.Findings = append(r.Findings, Finding{Code: "bursty", Severity: "info", Resource: "cpu",
			Message: fmt.Sprintf("Busy %.1f%% of the time, peaking at %s. The request covers the bursts; sized to idle time it would be %s.", b.ActiveShare*100, fmtCores(b.Peak), fmtCores(b.IdleRecommended))})
	}
	titleFindings(&r, in, readyAt)
	return r
}

// coupleHPA pairs a container's final request with its HPA. Going down,
// lowering the request alone makes the HPA add replicas, so total cost
// barely moves. Going up, while the HPA scales between its minimum and
// maximum a larger request alone mostly brings fewer, busier pods, so the
// usage per request that was the problem stays. Either way the target has
// to move with the request. Memory per pod rarely scales down with more
// pods, so a memory HPA is only paired going down.
func coupleHPA(r *ContainerReport, h *hpaTarget, res *ResourceRec, usageOnly float64) {
	if res.Request <= 0 || res.Recommended <= 0 || res.Recommended == res.Request {
		return
	}
	switch {
	case res.Verdict == VerdictOver:
		res.Verdict = VerdictHPACoupled
	case res.Verdict == VerdictUnder && h.resource == "cpu" && res.Recommended > res.Request:
	default:
		return
	}
	r.HPA = &HPACoupling{Name: h.name, Resource: h.resource, TargetUtilization: h.utilization, PairedRequest: res.Recommended, usageOnly: usageOnly}
	// A target over the whole pod's requests is set once every container's
	// is known.
	if !h.pod {
		describeHPA(r, res, res.Request, res.Recommended, false)
	}
}

// describeHPA sets the target that keeps today's replica counts when the
// requests the HPA sums, this container's or the whole pod's, go from old to
// paired, and explains it.
func describeHPA(r *ContainerReport, res *ResourceRec, old, paired float64, pod bool) {
	h := r.HPA
	// A request raised far past today's could round the target to 0%, which
	// no HPA accepts.
	h.SuggestedTarget = max(1, int32(math.Round(float64(h.TargetUtilization)*old/paired)))
	fmtV := fmtCores
	if h.Resource == "memory" {
		fmtV = fmtBytes
	}
	var msg string
	if res.Verdict == VerdictUnder {
		msg = fmt.Sprintf("HPA %s scales this workload at %d%% %s utilisation, so while it scales between its minimum and maximum, a larger request alone mostly brings fewer, busier pods. Change both together: request %s and target %d%% keep today's replica counts, with a request that covers what each pod uses.", h.Name, h.TargetUtilization, h.Resource, fmtV(res.Recommended), h.SuggestedTarget)
	} else {
		msg = fmt.Sprintf("HPA %s scales this workload at %d%% %s utilisation, so lowering the request alone makes it add replicas and total cost barely moves. Change both together: request %s and target %d%% keep today's scaling with smaller pods.", h.Name, h.TargetUtilization, h.Resource, fmtV(res.Recommended), h.SuggestedTarget)
		switch {
		case h.usageOnly > 0 && pod:
			msg += fmt.Sprintf(" Usage alone supports %s, but it goes no lower than %s, so the target stays within %d%% however the pod's other containers are set.", fmtV(h.usageOnly), fmtV(res.Recommended), maxHPATarget)
		case h.usageOnly > 0:
			msg += fmt.Sprintf(" Usage alone supports %s, but that would need a target above %d%%, which leaves the HPA no room to react.", fmtV(h.usageOnly), maxHPATarget)
		}
	}
	if pod {
		msg += fmt.Sprintf(" Its target counts every container in the pod, so it is worked out over all of their requests: %s today, %s recommended.", fmtV(old), fmtV(paired))
		res.Explain = append(res.Explain, fmt.Sprintf("Paired with the HPA, which counts the whole pod: requests totalling %s at a %d%% target scale like today's %s at %d%%.", fmtV(paired), h.SuggestedTarget, fmtV(old), h.TargetUtilization))
	} else {
		res.Explain = append(res.Explain, fmt.Sprintf("Paired with the HPA: %s at a %d%% target scales like %s does at %d%% today.", fmtV(paired), h.SuggestedTarget, fmtV(old), h.TargetUtilization))
	}
	r.Findings = append(r.Findings, Finding{Code: "hpa-coupled", Severity: "info", Resource: h.Resource, Title: "HPA", Message: msg})
}

// podHPA is the coupling of an HPA that scales on the pod's summed
// utilisation (a Resource metric, with several containers requesting it):
// one target from every container's requests, written to each container
// it pairs. None went below today's × target / maxHPATarget, so neither did
// the sum, and the target stays within maxHPATarget.
func podHPA(crs []ContainerReport, h *hpaTarget) *HPACoupling {
	res := func(c *ContainerReport) *ResourceRec {
		if h.resource == "memory" {
			return &c.Memory
		}
		return &c.CPU
	}
	var old, paired float64
	var first *HPACoupling
	for i := range crs {
		if rr := res(&crs[i]); rr.Request > 0 {
			old += rr.Request
			paired += rr.Recommended
		}
		if first == nil {
			first = crs[i].HPA
		}
	}
	if first == nil || paired <= 0 {
		return nil
	}
	for i := range crs {
		if crs[i].HPA != nil {
			describeHPA(&crs[i], res(&crs[i]), old, paired, true)
		}
	}
	// Each pairing container now holds the pod's target.
	out := *first
	out.PairedRequest = paired
	return &out
}

// titleFindings gives every finding a tag of a few words, the form a row
// shows; the sentence stays for the evidence view.
func titleFindings(r *ContainerReport, in containerInput, readyAt time.Time) {
	pctUnused := func(used, req float64) int {
		if req <= 0 {
			return 0
		}
		return int(math.Round(max(0, 1-used/req) * 100))
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		switch f.Code {
		case "oom-killed":
			f.Title = fmt.Sprintf("OOM ×%d", r.OOMKills)
		case "oom-resolved":
			f.Title = "Raised after OOM"
		case "memory-near-limit":
			f.Title = "Near memory limit"
		case "memory-over-request":
			f.Title = "Memory over request"
		case "memory-cache-ambiguous":
			f.Title = "Cache-heavy"
		case "cpu-limit-pressure":
			f.Title = "CPU at limit"
		case "cpu-throttled":
			f.Title = "Throttled"
		case "cpu-burst-limit":
			f.Title = "Bursts hit limit"
		case "cpu-over-request":
			f.Title = "CPU over request"
		case "no-requests":
			f.Title = "No requests"
		case "cpu-over-provisioned":
			f.Title = fmt.Sprintf("CPU %d%% unused", pctUnused(r.CPU.Estimate, in.current.CPURequest))
		case "memory-over-provisioned":
			f.Title = fmt.Sprintf("Memory %d%% unused", pctUnused(r.Memory.Peak, in.current.MemRequest))
		case "hpa-coupled":
			f.Title = "HPA"
		case "replica-imbalance":
			f.Title = "Uneven replicas"
		case "startup-spike":
			if r.StartupBoost != nil && !r.StartupBoost.Floor {
				f.Title = "Startup boost"
				continue
			}
			f.Title = "Startup spike"
		case "restarts":
			f.Title = fmt.Sprintf("Restarts ×%d", r.Restarts-r.OOMKills)
		case "regime-change":
			at := r.CPU.ShiftAt
			if f.Resource == "memory" {
				at = r.Memory.ShiftAt
			}
			if at != nil {
				f.Title = "Changed " + at.Format("Jan 2")
			} else {
				f.Title = "Changed"
			}
		case "growth":
			f.Title = "Memory growing"
		case "injected-sidecar":
			f.Title = "Mesh sidecar"
		case "vpa-managed":
			f.Title = "VPA-managed"
		case "jvm-heap-over-limit":
			f.Title = "JVM heap over limit"
		case "heap-over-limit":
			f.Title = "Heap over limit"
		case "jvm-heap-follows-limit":
			f.Title = "JVM heap follows limit"
		case "new-version":
			f.Title = "New version"
		case "bursty":
			f.Title = "Bursty"
		case "insufficient-data":
			switch {
			case in.isJob && r.Data.Runs >= minJobRuns:
				f.Title = "Runs too short for CPU"
			case in.isJob:
				f.Title = fmt.Sprintf("%d of %d runs", r.Data.Runs, minJobRuns)
			default:
				f.Title = "Ready " + readyAt.Format("Jan 2")
			}
		case "right-sized":
			f.Title = "Right-sized"
		case "no-data":
			f.Title = "No data"
		default:
			f.Title = f.Code
		}
	}
}

// backtest replays the engine week by week: at each weekly cut-off it
// re-fits on every day before and scores the following week (rolling origin,
// expanding window), the way the offline calibration study does. Windows too
// short for a full week of training fall back to one split, scoring the last
// quarter of the days.
func backtest(cpuBlocks, memBlocks []dayBlock, perDay int, p profileParams) *Backtest {
	n := min(len(cpuBlocks), len(memBlocks))
	if n < 5 {
		return nil
	}
	cb := cpuBlocks[len(cpuBlocks)-n:]
	mb := memBlocks[len(memBlocks)-n:]
	var origins []int
	for o := 7; o <= n-3; o += 7 {
		origins = append(origins, o)
	}
	if len(origins) == 0 {
		origins = []int{n - max(2, n/4)}
	}
	bt := &Backtest{TrainDays: origins[0]}
	var above, total float64
	worst := -1.0
	for _, o := range origins {
		end := min(o+7, n)
		if len(origins) == 1 {
			end = n
		}
		ce := estimateCPU(cb[:o], perDay, p.cpuQuantile)
		me := estimateMem(mb[:o], perDay)
		testCPU := sortedValues(cb[o:end])
		testMem := flattenBlocks(mb[o:end])
		if len(ce.sorted) == 0 || len(me.sorted) == 0 || len(testCPU) == 0 || len(testMem) == 0 {
			continue
		}
		cpuRec := roundCPU(max(ce.est, minCPU))
		need, _ := memHeadroom(me.est, p)
		memRec := roundMem(max(need, minMem))
		scored, target := testCPU, 1-p.cpuQuantile
		// Bursty CPU is judged the way it is recommended: on busy time only.
		probe := ResourceRec{P50: quantileSorted(ce.sorted, 0.5), Recommended: cpuRec}
		applyBurst(&probe, ce.sorted)
		if probe.Burst != nil && probe.Recommended > cpuRec {
			threshold := 0.25 * quantileSorted(ce.sorted, 0.995)
			scored = testCPU[sort.SearchFloat64s(testCPU, threshold):]
			cpuRec, target = probe.Recommended, 1-burstNeedQ
			bt.Bursty = true
		}
		above += fractionAbove(scored, cpuRec) * float64(len(scored))
		total += float64(len(scored))
		bt.CPURecommended, bt.CPUTarget = cpuRec, target
		peak := maxOf(testMem)
		if peak > memRec {
			bt.MemBreaches++
		}
		if r := peak / memRec; r > worst {
			worst, bt.MemTestPeak, bt.MemRecommended = r, peak, memRec
		}
		bt.Folds++
		bt.TestDays += end - o
	}
	if bt.Folds == 0 || total == 0 {
		return nil
	}
	bt.CPUExceedance = above / total
	bt.MemBreached = bt.MemBreaches > 0
	bt.Calibrated = bt.CPUExceedance <= 2*bt.CPUTarget && !bt.MemBreached
	return bt
}

func confidence(r ContainerReport, insufficient bool) string {
	if insufficient {
		return "low"
	}
	width := 0.0
	if r.CPU.Recommended > 0 && !math.IsNaN(r.CPU.High) {
		width = max(width, (r.CPU.High-r.CPU.Low)/r.CPU.Recommended)
	}
	if r.Memory.Recommended > 0 {
		width = max(width, (r.Memory.High-r.Memory.Low)/r.Memory.Recommended)
	}
	calibrated := r.Backtest == nil || r.Backtest.Calibrated
	switch {
	case r.Data.Days >= 7 && r.Data.Coverage >= 0.8 && width <= 0.25 && calibrated && r.Backtest != nil:
		return "high"
	// Under a week the weekly cycle hasn't been seen: replayed on real
	// history, 3-5 days of training missed the next week's memory peak
	// 5.5-6.1% of the time against 4.2% for a full week.
	case r.Data.Days >= 7 && width <= 0.5:
		return "medium"
	}
	return "low"
}

// overallVerdict picks the most important resource verdict. A container is
// only right-sized when both resources are; one resource without data leaves
// it insufficient unless the other one has something to act on.
func overallVerdict(r ContainerReport, insufficient bool) Verdict {
	vs := []Verdict{r.CPU.Verdict, r.Memory.Verdict}
	if insufficient {
		return VerdictInsufficient
	}
	for _, v := range []Verdict{VerdictUnder, VerdictNoRequests, VerdictOver, VerdictHPACoupled, VerdictInsufficient} {
		if slices.Contains(vs, v) {
			return v
		}
	}
	return VerdictRight
}

// countRuns counts separate stretches with at least one replica running.
func countRuns(replicas []float64) int {
	runs, in, gap := 0, false, 0
	for _, v := range replicas {
		if !math.IsNaN(v) && v > 0 {
			if !in && (runs == 0 || gap >= 2) {
				runs++
			}
			in, gap = true, 0
		} else {
			in = false
			gap++
		}
	}
	return runs
}

// Bursty workloads idle most of the time and do their real work in short
// bursts. A quantile of replica-time then describes the idling, and a request
// sized to it starves the bursts whenever the node is busy. For these the
// request covers the work: a quantile of CPU while active.
const (
	burstActiveShare = 0.10 // active less than this share of the time
	burstRatio       = 10.0 // high quantile at least this many times the median
	burstMinCores    = 0.1  // bursts smaller than this aren't worth a request
	burstNeedQ       = 0.9  // quantile of active CPU the request covers
)

// applyBurst switches a CPU recommendation to the active-time need when the
// usage is bursty, keeping the idle-sized value as the cheaper alternative.
func applyBurst(cpu *ResourceRec, sorted []float64) {
	if len(sorted) < 100 {
		return
	}
	hi := quantileSorted(sorted, 0.995)
	if hi < burstMinCores || hi < burstRatio*cpu.P50 {
		return
	}
	threshold := 0.25 * hi
	share := fractionAbove(sorted, threshold)
	if share >= burstActiveShare || share == 0 {
		return
	}
	i := sort.SearchFloat64s(sorted, threshold)
	active := sorted[i:]
	need := quantileSorted(active, burstNeedQ)
	cpu.Burst = &Burst{ActiveShare: share, Need: need, Peak: sorted[len(sorted)-1], IdleRecommended: cpu.Recommended}
	if roundCPU(need) <= cpu.Recommended {
		return
	}
	cpu.Recommended = roundCPU(need)
	cpu.Estimate = need
	cpu.Low = quantileSorted(active, 0.75)
	cpu.High = quantileSorted(active, 0.97)
	cpu.Explain = append(cpu.Explain,
		fmt.Sprintf("But it is bursty: busy only %.1f%% of the time, peaking at %s. Sized to idle time, bursts would get only spare node CPU.", share*100, fmtCores(cpu.Burst.Peak)),
		fmt.Sprintf("So the request covers the work instead: P%.0f of CPU while busy is %s, rounded to %s. The idle-sized %s is the cheaper option if slower bursts are fine.", burstNeedQ*100, fmtCores(need), fmtCores(cpu.Recommended), fmtCores(cpu.Burst.IdleRecommended)))
}

// injectedSidecar names the mesh that injects a container, if any.
func injectedSidecar(container string) string {
	switch container {
	case "istio-proxy":
		return "Istio"
	case "linkerd-proxy":
		return "Linkerd"
	}
	return ""
}

// fractionAboveSeries is the share of present samples above x.
func fractionAboveSeries(xs []float64, x float64) float64 {
	var n, above float64
	for _, v := range xs {
		if math.IsNaN(v) {
			continue
		}
		n++
		if v > x {
			above++
		}
	}
	if n == 0 {
		return 0
	}
	return above / n
}

// oomLimit is the highest memory limit an OOM kill happened at, from the
// hourly limit history, or today's limit where that is unknown. A kill is
// timed to the step it was seen in, and the limit could have changed within
// the hour around it, so the higher hourly value on either side counts.
func (in containerInput) oomLimit() float64 {
	var lim float64
	for _, t := range in.oomTimes {
		at := math.NaN()
		if g := in.hourly; len(in.memLimits) > 0 && g.step > 0 {
			lo := int(math.Floor(float64(t.Add(-in.step).Sub(g.start)) / float64(g.step)))
			hi := int(math.Ceil(float64(t.Sub(g.start)) / float64(g.step)))
			for i := max(lo, 0); i <= min(hi, len(in.memLimits)-1); i++ {
				if v := in.memLimits[i]; !math.IsNaN(v) && (math.IsNaN(at) || v > at) {
					at = v
				}
			}
		}
		if math.IsNaN(at) {
			at = in.current.MemLimit
		}
		lim = max(lim, at)
	}
	return lim
}

// prepare derives the busiest-replica, total and count series from pooled
// CPU, and gives empty inputs a pooled value so the rest never checks nil.
func (in *containerInput) prepare() {
	if in.cpu == nil {
		in.cpu = &pooled{off: make([]int32, len(in.mem)+1)}
	}
	if in.cpuMax == nil {
		in.cpuMax, in.cpuSum, in.replicas = in.cpu.derive()
	}
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

func fmtStep(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()))
}

func fmtCores(c float64) string {
	if c >= 1 {
		return fmt.Sprintf("%.2f cores", c)
	}
	return fmt.Sprintf("%.0fm", c*1000)
}

func fmtBytes(b float64) string {
	switch {
	case b >= gib:
		return fmt.Sprintf("%.2fGi", b/gib)
	case b >= mib:
		return fmt.Sprintf("%.0fMi", b/mib)
	default:
		return fmt.Sprintf("%.0fKi", b/1024)
	}
}
