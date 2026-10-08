package rightsizing

import (
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

const step5m = 5 * time.Minute

var t0 = time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)

// synth builds a container input over `days` days at 5-minute steps. cpuAt
// gives a replica's CPU at sample i (replica k runs 5% lighter per k), memAt
// the busiest replica's memory.
func synth(days int, replicas int, cpuAt, memAt func(i int) float64) containerInput {
	n := days * 288
	in := containerInput{
		container: "app", start: t0, step: step5m, profile: ProfileBalanced, seed: 42,
		mem: make([]float64, n), startupCPUPeak: math.NaN(),
	}
	var pts []pooledPoint
	for i := range n {
		c := cpuAt(i)
		for k := range replicas {
			pts = append(pts, pooledPoint{int32(i), c * (1 - 0.05*float64(k))})
		}
		in.mem[i] = memAt(i)
	}
	in.cpu = buildPooled(n, pts)
	return in
}

// drop removes every sample at the steps where gone is true.
func drop(in *containerInput, gone func(i int) bool) {
	n := len(in.mem)
	var pts []pooledPoint
	for i := range n {
		if gone(i) {
			in.mem[i] = math.NaN()
			continue
		}
		for _, v := range in.cpu.at(i) {
			pts = append(pts, pooledPoint{int32(i), v})
		}
	}
	in.cpu = buildPooled(n, pts)
	in.cpuMax, in.cpuSum, in.replicas = nil, nil, nil
}

// diurnal is AR(1) noise around a daily cycle, the shape real services have.
func diurnal(rng *rand.Rand, base, swing float64) func(int) float64 {
	e := 0.0
	return func(i int) float64 {
		e = 0.9*e + rng.NormFloat64()*0.15
		phase := 2 * math.Pi * float64(i%288) / 288
		return base * (1 + swing*math.Sin(phase)) * math.Exp(e)
	}
}

func flatMem(rng *rand.Rand, base float64) func(int) float64 {
	return func(int) float64 { return base * (1 + 0.05*rng.Float64()) }
}

func hasFinding(r ContainerReport, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestAnalyzeOverProvisioned(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	in := synth(14, 3, diurnal(rng, 0.15, 0.5), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, CPULimit: 2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	r := analyze(in)

	if r.Verdict != VerdictOver || r.CPU.Verdict != VerdictOver || r.Memory.Verdict != VerdictOver {
		t.Fatalf("verdicts: overall %s cpu %s mem %s", r.Verdict, r.CPU.Verdict, r.Memory.Verdict)
	}
	if r.CPU.Recommended >= 0.5 || r.CPU.Recommended < r.CPU.P95 {
		t.Fatalf("cpu rec %v should sit at P95 %v, far below 1 core", r.CPU.Recommended, r.CPU.P95)
	}
	if r.CPU.Low > r.CPU.Estimate || r.CPU.High < r.CPU.Estimate {
		t.Fatalf("interval [%v, %v] must contain estimate %v", r.CPU.Low, r.CPU.High, r.CPU.Estimate)
	}
	if r.Memory.Recommended < 315*mib*1.15 || r.Memory.Recommended > 450*mib {
		t.Fatalf("memory rec %v Mi", r.Memory.Recommended/mib)
	}
	if r.Memory.RecommendedLimit != r.Memory.Recommended {
		t.Fatal("memory limit should equal the request")
	}
	if r.CPU.LimitAction != "keep" {
		t.Fatalf("cpu limit action %s", r.CPU.LimitAction)
	}
	// Fourteen days give one full week of training and one scored week. On
	// this seed the second week runs hotter than the first (daily P95 from
	// 0.22 to 0.39 cores), so an honest backtest may call it a miss; it must
	// still score a whole week and stay within a sane range.
	if bt := r.Backtest; bt == nil || bt.Folds != 1 || bt.TestDays != 7 || bt.CPUExceedance > 3*bt.CPUTarget {
		t.Fatalf("backtest %+v", r.Backtest)
	}
	if r.Confidence == "low" {
		t.Fatalf("confidence %s, data %+v", r.Confidence, r.Data)
	}
	if len(r.CPU.Explain) < 3 || len(r.Memory.Explain) < 2 {
		t.Fatalf("explanations missing: %v %v", r.CPU.Explain, r.Memory.Explain)
	}
}

func TestAnalyzeDeterministic(t *testing.T) {
	mk := func() ContainerReport {
		rng := rand.New(rand.NewPCG(2, 2))
		in := synth(14, 2, diurnal(rng, 0.2, 0.3), flatMem(rng, 200*mib))
		in.current = Resources{CPURequest: 0.5, MemRequest: gib, MemLimit: gib}
		return analyze(in)
	}
	a, b := mk(), mk()
	if a.CPU.Low != b.CPU.Low || a.CPU.High != b.CPU.High || a.CPU.Recommended != b.CPU.Recommended {
		t.Fatal("same input must give the same numbers")
	}
}

func TestAnalyzeOOMKilled(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(int) float64 { return 500 * mib * (0.9 + 0.1*rng.Float64()) })
	in.current = Resources{CPURequest: 0.2, MemRequest: 512 * mib, MemLimit: 512 * mib}
	in.oomTimes = []time.Time{t0.Add(3 * day), t0.Add(9 * day)}
	in.restartTimes = in.oomTimes
	r := analyze(in)

	if r.Verdict != VerdictUnder || r.Memory.Verdict != VerdictUnder || !r.Memory.Censored {
		t.Fatalf("verdict %s mem %s censored %v", r.Verdict, r.Memory.Verdict, r.Memory.Censored)
	}
	if r.Memory.Recommended < 640*mib {
		t.Fatalf("after OOM memory must step above the limit, got %v Mi", r.Memory.Recommended/mib)
	}
	if !hasFinding(r, "oom-killed") || hasFinding(r, "restarts") {
		t.Fatalf("findings %+v", r.Findings)
	}
}

func TestAnalyzeCPUThrottledAtLimit(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 4))
	gen := diurnal(rng, 0.45, 0.4)
	in := synth(14, 2, func(i int) float64 { return min(gen(i), 0.5) }, flatMem(rng, 100*mib))
	in.current = Resources{CPURequest: 0.25, CPULimit: 0.5, MemRequest: 256 * mib, MemLimit: 256 * mib}
	r := analyze(in)

	if r.CPU.Verdict != VerdictUnder || !r.CPU.Censored || r.CPU.LimitAction != "raise" {
		t.Fatalf("cpu verdict %s censored %v action %s", r.CPU.Verdict, r.CPU.Censored, r.CPU.LimitAction)
	}
	if r.CPU.RecommendedLimit < 0.75 {
		t.Fatalf("limit should rise at least 1.5×, got %v", r.CPU.RecommendedLimit)
	}
	if !hasFinding(r, "cpu-limit-pressure") {
		t.Fatal("missing limit-pressure finding")
	}
}

func TestAnalyzeInsufficientData(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), flatMem(rng, 100*mib))
	drop(&in, func(i int) bool { return i < 12*288 }) // only the last two days exist
	in.current = Resources{CPURequest: 1, MemRequest: gib}
	r := analyze(in)
	if r.Verdict != VerdictInsufficient || r.Confidence != "low" || !hasFinding(r, "insufficient-data") {
		t.Fatalf("verdict %s confidence %s", r.Verdict, r.Confidence)
	}
	if math.Abs(r.Data.Days-2) > 0.01 {
		t.Fatalf("days %v", r.Data.Days)
	}
}

func TestAnalyzeRegimeChange(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 6))
	low, high := diurnal(rng, 0.1, 0.3), diurnal(rng, 0.3, 0.3)
	in := synth(14, 2, func(i int) float64 {
		if i >= 10*288 {
			return high(i)
		}
		return low(i)
	}, flatMem(rng, 200*mib))
	in.current = Resources{CPURequest: 0.5, MemRequest: 512 * mib, MemLimit: 512 * mib}
	r := analyze(in)

	if r.CPU.ShiftAt == nil || !r.CPU.ShiftAt.Equal(t0.Add(10*day)) {
		t.Fatalf("shift at %v", r.CPU.ShiftAt)
	}
	// Using all 14 days would understate the new level badly.
	if r.CPU.Estimate < 0.35 {
		t.Fatalf("estimate %v ignores the new regime", r.CPU.Estimate)
	}
	if !hasFinding(r, "regime-change") {
		t.Fatal("missing regime-change finding")
	}
}

func TestAnalyzeHPACoupled(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	in := synth(14, 4, diurnal(rng, 0.15, 0.3), flatMem(rng, 200*mib))
	in.current = Resources{CPURequest: 1, MemRequest: 300 * mib, MemLimit: 300 * mib}
	in.hpa = &hpaTarget{resource: "cpu", utilization: 70, name: "api"}
	r := analyze(in)

	if r.Verdict != VerdictHPACoupled || r.HPA == nil {
		t.Fatalf("verdict %s hpa %+v", r.Verdict, r.HPA)
	}
	// Usage supports ~200m, which would need a 350% target; capped at 90%
	// the paired request is 1 core × 70 / 90 ≈ 780m.
	if r.HPA.SuggestedTarget != maxHPATarget || math.Abs(r.HPA.PairedRequest-0.78) > 1e-9 || r.CPU.Recommended != r.HPA.PairedRequest {
		t.Fatalf("hpa %+v rec %v", r.HPA, r.CPU.Recommended)
	}
}

func TestAnalyzeNoRequests(t *testing.T) {
	rng := rand.New(rand.NewPCG(8, 8))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), flatMem(rng, 100*mib))
	r := analyze(in)
	if r.Verdict != VerdictNoRequests || r.CPU.Recommended == 0 || r.Memory.Recommended == 0 {
		t.Fatalf("verdict %s rec %v/%v", r.Verdict, r.CPU.Recommended, r.Memory.Recommended)
	}
	if r.Memory.LimitAction != "set" || r.CPU.LimitAction != "none" {
		t.Fatalf("limit actions %s %s", r.Memory.LimitAction, r.CPU.LimitAction)
	}
}

func TestAnalyzeMemoryLeak(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(i int) float64 {
		return (200 + 10*float64(i)/288) * mib * (1 + 0.02*rng.Float64()) // +10Mi a day
	})
	in.current = Resources{CPURequest: 0.2, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	if r.Memory.TrendFactor <= 1 || !hasFinding(r, "growth") {
		t.Fatalf("trend %v findings %+v", r.Memory.TrendFactor, r.Findings)
	}
	// Peak ≈ 340Mi; a week of growth adds ≈ 70Mi before headroom.
	if r.Memory.Estimate < 390*mib {
		t.Fatalf("estimate %v Mi ignores growth", r.Memory.Estimate/mib)
	}
}

func TestAnalyzeRightSized(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 10))
	in := synth(14, 2, diurnal(rng, 0.2, 0.3), flatMem(rng, 400*mib))
	probe := analyze(in)
	in.current = Resources{CPURequest: probe.CPU.Recommended, MemRequest: probe.Memory.Recommended, MemLimit: probe.Memory.Recommended}
	r := analyze(in)
	if r.Verdict != VerdictRight || !hasFinding(r, "right-sized") {
		t.Fatalf("verdict %s cpu %s mem %s", r.Verdict, r.CPU.Verdict, r.Memory.Verdict)
	}
}

func TestAnalyzeJobRuns(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 11))
	in := synth(14, 1, diurnal(rng, 0.5, 0.1), flatMem(rng, 300*mib))
	// Runs for an hour every two days: 7 runs.
	drop(&in, func(i int) bool { return (i/288)%2 != 0 || i%288 >= 12 })
	in.isJob = true
	in.current = Resources{CPURequest: 2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	r := analyze(in)
	if r.Data.Runs != 7 {
		t.Fatalf("runs %d", r.Data.Runs)
	}
	if r.Verdict == VerdictInsufficient {
		t.Fatal("seven runs is enough history for a job")
	}
}

func TestRounding(t *testing.T) {
	approx(t, "42m", roundCPU(0.0421), 0.045, 1e-12)
	approx(t, "213m", roundCPU(0.2131), 0.22, 1e-12)
	approx(t, "1.01", roundCPU(1.01), 1.05, 1e-12)
	approx(t, "exact", roundCPU(0.2), 0.2, 1e-12)
	approx(t, "300Mi", roundMem(300*mib), 304*mib, 0)
	approx(t, "1.1Gi", roundMem(1.1*gib), 1.125*gib, 0)
	approx(t, "5Gi", roundMem(5.1*gib), 5.25*gib, 0)
}

// With only total usage (page cache included) a cache-heavy app sits at its
// limit without being at risk; the engine must not call it under-provisioned
// or recommend more memory without an OOM kill.
func TestAnalyzeUsageMetricCacheAtLimit(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 12))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(int) float64 { return 1024 * mib })
	in.memIsUsage = true
	in.current = Resources{CPURequest: 0.2, MemRequest: 512 * mib, MemLimit: 1024 * mib}
	r := analyze(in)
	if r.Memory.Verdict == VerdictUnder || r.Memory.Recommended != 512*mib {
		t.Fatalf("mem verdict %s rec %v Mi", r.Memory.Verdict, r.Memory.Recommended/mib)
	}
	if !hasFinding(r, "memory-cache-ambiguous") {
		t.Fatalf("findings %+v", r.Findings)
	}

	in.oomTimes = []time.Time{t0.Add(5 * day)}
	r = analyze(in)
	if r.Memory.Verdict != VerdictUnder || r.Memory.Recommended < 1280*mib {
		t.Fatalf("an OOM kill is real pressure even on usage: %s %v Mi", r.Memory.Verdict, r.Memory.Recommended/mib)
	}
}

// fleet builds n interchangeable replicas, each with its own AR(1) noise
// around the same diurnal level; hot scales replica 0.
func fleet(seed uint64, days, n int, hot float64) containerInput {
	rng := rand.New(rand.NewPCG(seed, seed))
	steps := days * 288
	gens := make([]func(int) float64, n)
	for k := range gens {
		gens[k] = diurnal(rng, 0.2, 0.3)
	}
	in := containerInput{container: "app", start: t0, step: step5m, profile: ProfileBalanced, seed: 7, mem: make([]float64, steps), startupCPUPeak: math.NaN()}
	var pts []pooledPoint
	for i := range steps {
		for k, g := range gens {
			v := g(i)
			if k == 0 {
				v *= hot
			}
			pts = append(pts, pooledPoint{int32(i), v})
		}
		in.mem[i] = 200 * mib
	}
	in.cpu = buildPooled(steps, pts)
	in.current = Resources{CPURequest: 1, MemRequest: 512 * mib, MemLimit: 512 * mib}
	// Lifetime means per pod, as alignPooled computes them.
	sums := make([]float64, n)
	for i := range steps {
		for k, v := range in.cpu.at(i) {
			sums[k] += v
		}
	}
	for k := range sums {
		in.podMeans = append(in.podMeans, sums[k]/float64(steps))
	}
	return in
}

// The same per-pod behaviour must get the same request whether there are 2
// replicas or 60. A busiest-replica target fails this: it climbs with N.
func TestAnalyzeReplicaCountInvariance(t *testing.T) {
	small := analyze(fleet(21, 14, 2, 1)).CPU.Estimate
	large := analyze(fleet(22, 14, 60, 1)).CPU.Estimate
	if r := large / small; r < 0.9 || r > 1.1 {
		t.Fatalf("P95 with 2 replicas %.3f vs 60 replicas %.3f (ratio %.2f)", small, large, r)
	}
}

func TestAnalyzeHotReplica(t *testing.T) {
	r := analyze(fleet(23, 14, 4, 4))
	if !hasFinding(r, "replica-imbalance") || r.Imbalance < 2 {
		t.Fatalf("imbalance %.2f findings %+v", r.Imbalance, r.Findings)
	}
	if hasFinding(analyze(fleet(24, 14, 30, 1)), "replica-imbalance") {
		t.Fatal("interchangeable replicas flagged as imbalanced")
	}
}

func TestAnalyzeUsageMetricAtLimitIsAmbiguous(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 13))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(int) float64 { return 121 * mib })
	in.memIsUsage = true
	in.current = Resources{CPURequest: 0.05, MemRequest: 128 * mib, MemLimit: 128 * mib}
	r := analyze(in)
	if r.Memory.Verdict == VerdictUnder || hasFinding(r, "memory-near-limit") || !hasFinding(r, "memory-cache-ambiguous") {
		t.Fatalf("mem verdict %s findings %+v", r.Memory.Verdict, r.Findings)
	}
}

func TestAnalyzeNoRequestsAndSidecarFindings(t *testing.T) {
	rng := rand.New(rand.NewPCG(14, 14))
	in := synth(14, 1, diurnal(rng, 0.01, 0.2), flatMem(rng, 60*mib))
	in.container = "istio-proxy"
	r := analyze(in)
	if !hasFinding(r, "no-requests") || !hasFinding(r, "injected-sidecar") {
		t.Fatalf("findings %+v", r.Findings)
	}
}

// The startup floor comes after the HPA's lower bound, so the request the
// HPA finding pairs with is the one recommended.
func TestAnalyzeHPAPairsTheFinalRequest(t *testing.T) {
	rng := rand.New(rand.NewPCG(25, 25))
	in := synth(14, 3, diurnal(rng, 0.02, 0.2), flatMem(rng, 700*mib))
	in.current = Resources{CPURequest: 0.5, CPULimit: 2, MemRequest: gib, MemLimit: 2 * gib}
	in.startupCPUPeak = 0.4
	in.hpa = &hpaTarget{resource: "cpu", utilization: 30, name: "api"}
	r := analyze(in)
	if r.StartupBoost == nil || !r.StartupBoost.Floor {
		t.Fatalf("setup: no startup floor: %+v", r.StartupBoost)
	}
	if r.HPA == nil || r.HPA.PairedRequest != r.CPU.Recommended || r.HPA.SuggestedTarget != int32(math.Round(30*0.5/r.CPU.Recommended)) {
		t.Fatalf("hpa %+v, but %v cores recommended", r.HPA, r.CPU.Recommended)
	}
}

// An HPA keeps usage per request near its target, so a larger request alone
// mostly brings fewer, busier pods. Under-provisioned CPU pairs with a lower
// target that keeps today's replica counts.
func TestAnalyzeHPAUnderLowersTheTarget(t *testing.T) {
	rng := rand.New(rand.NewPCG(26, 26))
	in := synth(14, 4, diurnal(rng, 0.3, 0.3), flatMem(rng, 200*mib))
	in.current = Resources{CPURequest: 0.25, MemRequest: 300 * mib, MemLimit: 300 * mib}
	in.hpa = &hpaTarget{resource: "cpu", utilization: 70, name: "api"}
	r := analyze(in)
	if r.CPU.Verdict != VerdictUnder || r.CPU.Recommended <= 0.25 {
		t.Fatalf("setup: cpu %s rec %v", r.CPU.Verdict, r.CPU.Recommended)
	}
	want := int32(math.Round(70 * 0.25 / r.CPU.Recommended))
	if r.HPA == nil || r.HPA.PairedRequest != r.CPU.Recommended || r.HPA.SuggestedTarget != want || want >= 70 || !hasFinding(r, "hpa-coupled") {
		t.Fatalf("hpa %+v, want target %d with %v cores", r.HPA, want, r.CPU.Recommended)
	}
}

// A worker idle at 5m that bursts to ~1.2 cores for an hour a day: the
// request must cover the bursts, with the idle-sized value as the option.
func TestAnalyzeBurstyCPU(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 15))
	in := synth(14, 1, func(i int) float64 {
		if i%288 < 12 {
			return 1.2 * (0.9 + 0.2*rng.Float64())
		}
		return 0.005 * (0.8 + 0.4*rng.Float64())
	}, flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, CPULimit: 2, MemRequest: 512 * mib, MemLimit: 512 * mib}
	r := analyze(in)
	if r.CPU.Burst == nil || r.CPU.Burst.IdleRecommended > 0.02 {
		t.Fatalf("burst %+v", r.CPU.Burst)
	}
	if r.CPU.Recommended < 1.1 || r.CPU.Verdict == VerdictOver {
		t.Fatalf("bursty CPU rec %v verdict %s: a 1-core request is what the bursts need", r.CPU.Recommended, r.CPU.Verdict)
	}
}

func TestAnalyzeHPAPairWithinRoom(t *testing.T) {
	rng := rand.New(rand.NewPCG(16, 16))
	in := synth(14, 4, diurnal(rng, 0.15, 0.3), flatMem(rng, 200*mib))
	in.current = Resources{CPURequest: 0.5, MemRequest: 300 * mib, MemLimit: 300 * mib}
	in.hpa = &hpaTarget{resource: "cpu", utilization: 30, name: "api"}
	r := analyze(in)
	if r.HPA == nil || r.HPA.SuggestedTarget >= maxHPATarget {
		t.Fatalf("hpa %+v", r.HPA)
	}
	want := int32(math.Round(30 * 0.5 / r.HPA.PairedRequest))
	if r.HPA.SuggestedTarget != want {
		t.Fatalf("target %d, want %d for request %v", r.HPA.SuggestedTarget, want, r.HPA.PairedRequest)
	}
}

// A resource without data keeps today's value, so it can neither save money
// nor end up as 0 in a patch.
func TestAnalyzeMissingMemoryKeepsRequest(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 17))
	in := synth(14, 2, diurnal(rng, 0.1, 0.2), func(int) float64 { return math.NaN() })
	in.current = Resources{CPURequest: 1, MemRequest: 2 * gib, MemLimit: 2 * gib}
	r := analyze(in)
	if r.Memory.Verdict != VerdictInsufficient || r.Memory.Recommended != 2*gib || r.Memory.RecommendedLimit != 2*gib {
		t.Fatalf("memory %s rec %v limit %v", r.Memory.Verdict, r.Memory.Recommended, r.Memory.RecommendedLimit)
	}
	if r.CPU.Verdict != VerdictOver {
		t.Fatalf("cpu verdict %s", r.CPU.Verdict)
	}
}

// CPU pinned at its limit is censored: the recommendation never goes down.
func TestAnalyzeCensoredCPUNeverShrinks(t *testing.T) {
	rng := rand.New(rand.NewPCG(18, 18))
	gen := diurnal(rng, 0.45, 0.4)
	in := synth(14, 2, func(i int) float64 { return min(gen(i), 0.5) }, flatMem(rng, 100*mib))
	in.current = Resources{CPURequest: 0.5, CPULimit: 0.5, MemRequest: 256 * mib, MemLimit: 256 * mib}
	r := analyze(in)
	if !r.CPU.Censored || r.CPU.Recommended < 0.5 {
		t.Fatalf("censored %v rec %v", r.CPU.Censored, r.CPU.Recommended)
	}
}

func TestAnalyzeNoDataKeepsEverything(t *testing.T) {
	in := synth(14, 1, func(int) float64 { return 0.1 }, func(int) float64 { return 100 * mib })
	drop(&in, func(int) bool { return true })
	in.current = Resources{CPURequest: 0.3, CPULimit: 1, MemRequest: 512 * mib, MemLimit: 512 * mib}
	r := analyze(in)
	if r.Verdict != VerdictInsufficient || r.CPU.Recommended != 0.3 || r.Memory.Recommended != 512*mib || r.CPU.RecommendedLimit != 1 {
		t.Fatalf("%+v %+v", r.CPU, r.Memory)
	}
}

func TestEveryFindingHasATitle(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 19))
	inputs := []containerInput{}
	a := synth(14, 3, diurnal(rng, 0.15, 0.5), flatMem(rng, 300*mib))
	a.current = Resources{CPURequest: 1, CPULimit: 2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	b := synth(14, 1, diurnal(rng, 0.1, 0.2), func(int) float64 { return 500 * mib })
	b.current = Resources{CPURequest: 0.2, MemRequest: 512 * mib, MemLimit: 512 * mib}
	b.oomTimes = []time.Time{t0.Add(3 * day)}
	b.restartTimes = []time.Time{t0.Add(3 * day), t0.Add(5 * day)}
	c := synth(14, 1, diurnal(rng, 0.01, 0.2), flatMem(rng, 60*mib))
	c.container = "istio-proxy"
	d := fleet(31, 14, 4, 4)
	e := synth(14, 1, func(i int) float64 {
		if i%288 < 12 {
			return 1.2
		}
		return 0.005
	}, flatMem(rng, 300*mib))
	e.current = Resources{CPURequest: 1, MemRequest: 512 * mib, MemLimit: 512 * mib}
	inputs = append(inputs, a, b, c, d, e)
	seen := map[string]bool{}
	for _, in := range inputs {
		for _, f := range analyze(in).Findings {
			seen[f.Code] = true
			if f.Title == "" || f.Title == f.Code {
				t.Errorf("finding %s has no title (%q)", f.Code, f.Title)
			}
		}
	}
	if len(seen) < 8 {
		t.Fatalf("only exercised %v", seen)
	}
}

// One long batch run has days of usage: as much evidence as a service, even
// though it is a single run.
func TestAnalyzeLongSingleRunJob(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 13))
	in := synth(14, 1, diurnal(rng, 0.5, 0.1), flatMem(rng, 300*mib))
	drop(&in, func(i int) bool { return i < 288*8 }) // started 6 days ago, still running
	in.isJob = true
	in.current = Resources{CPURequest: 4, MemRequest: 4 * gib, MemLimit: 4 * gib}
	r := analyze(in)
	if r.Data.Runs != 1 || r.Verdict == VerdictInsufficient {
		t.Fatalf("runs %d verdict %s", r.Data.Runs, r.Verdict)
	}
}

// A young service says when it will have enough history, not just how much
// it has.
func TestAnalyzeYoungWorkloadSaysWhenReady(t *testing.T) {
	rng := rand.New(rand.NewPCG(14, 14))
	in := synth(14, 2, diurnal(rng, 0.2, 0.3), flatMem(rng, 300*mib))
	drop(&in, func(i int) bool { return i < 288*13 }) // one day old
	in.current = Resources{CPURequest: 1, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	want := "Ready " + t0.Add(16*day).Format("Jan 2")
	for _, f := range r.Findings {
		if f.Code == "insufficient-data" {
			if f.Title != want {
				t.Fatalf("title %q, want %q", f.Title, want)
			}
			return
		}
	}
	t.Fatalf("verdict %s, no insufficient-data finding", r.Verdict)
}

// Runs too short for a CPU rate still show in memory and count as runs.
func TestAnalyzeShortJobRunsCountFromMemory(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 15))
	in := synth(14, 1, diurnal(rng, 0.5, 0.1), flatMem(rng, 300*mib))
	n := len(in.mem)
	in.cpu = buildPooled(n, nil) // no CPU rate for any run
	for i := range n {
		if i%288 != 0 { // one memory sample a day
			in.mem[i] = math.NaN()
		}
	}
	in.isJob = true
	in.current = Resources{CPURequest: 1, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	if r.Data.Runs != 14 || r.Verdict == VerdictInsufficient {
		t.Fatalf("runs %d verdict %s", r.Data.Runs, r.Verdict)
	}
}

// Postgres after a restart: two weeks at its working level, then two days
// far lower while its caches refill. Following the drop would size it for a
// cold cache and OOM-kill it once warm; it must keep the established peak.
func TestAnalyzeMemoryDropAfterRestartIsHeld(t *testing.T) {
	rng := rand.New(rand.NewPCG(16, 16))
	n := 14 * 288
	in := synth(14, 1, diurnal(rng, 0.02, 0.1), func(i int) float64 {
		if i >= n-2*288 {
			return 19 * mib * (1 + 0.02*rng.Float64())
		}
		return 73 * mib * (1 + 0.02*rng.Float64())
	})
	r := analyze(in)
	if r.Memory.Recommended < 73*mib {
		t.Fatalf("recommended %v Mi under the 73Mi established peak", r.Memory.Recommended/mib)
	}
	if r.Memory.ShiftAt != nil {
		t.Fatal("a two-day drop must not be reported as the new regime")
	}

	// Even a drop that has held nine days is not followed: replayed on real
	// history, memory drops that lasted weeks still came back often enough
	// to make following them a net loss. The explanation says what happened.
	in = synth(18, 1, diurnal(rng, 0.02, 0.1), func(i int) float64 {
		if i >= 9*288 {
			return 19 * mib * (1 + 0.02*rng.Float64())
		}
		return 73 * mib * (1 + 0.02*rng.Float64())
	})
	r = analyze(in)
	if r.Memory.Recommended < 73*mib || r.Memory.ShiftAt != nil {
		t.Fatalf("a memory drop was followed: recommended %v Mi", r.Memory.Recommended/mib)
	}
}

// A quiet weekend at the end of the window is not a new normal: CPU must
// still be sized for the weekdays.
func TestAnalyzeWeekendDipDoesNotShrinkCPU(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 17))
	n := 14 * 288
	in := synth(14, 2, func(i int) float64 {
		base := 0.4
		if i >= n-2*288 {
			base = 0.15 // the last two days run at under half
		}
		return base * (1 + 0.05*rng.NormFloat64())
	}, flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 0.5, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	if r.CPU.ShiftAt != nil {
		t.Fatal("a two-day dip was taken as the new regime")
	}
	if r.CPU.Estimate < 0.35 {
		t.Fatalf("CPU sized for the weekend: %v cores", r.CPU.Estimate)
	}
}

// Five days of a clearly over-provisioned service: enough to see it, not
// enough to cut it, since a weekly peak may not have happened yet.
func TestAnalyzeNoShrinkBeforeAFullWeek(t *testing.T) {
	rng := rand.New(rand.NewPCG(18, 18))
	in := synth(14, 2, diurnal(rng, 0.1, 0.3), flatMem(rng, 200*mib))
	drop(&in, func(i int) bool { return i < 288*9 }) // five days old
	in.current = Resources{CPURequest: 2, MemRequest: 4 * gib, MemLimit: 4 * gib}
	r := analyze(in)
	if r.Verdict != VerdictInsufficient || r.CPU.Recommended != 2 || r.Memory.Recommended != 4*gib {
		t.Fatalf("verdict %s, cpu %v, mem %v Mi", r.Verdict, r.CPU.Recommended, r.Memory.Recommended/mib)
	}
	want := "Ready " + t0.Add(16*day).Format("Jan 2")
	for _, f := range r.Findings {
		if f.Code == "insufficient-data" && f.Title != want {
			t.Fatalf("title %q, want %q", f.Title, want)
		}
	}
}

// limitHistory is an hourly memory limit over the input's window: before
// until the given day, after from then on.
func limitHistory(in *containerInput, raisedOn int, before, after float64) {
	in.hourly = newGrid(in.start.Add(time.Duration(len(in.mem)-1)*in.step), time.Duration(len(in.mem))*in.step, time.Hour)
	in.memLimits = make([]float64, in.hourly.n)
	for i := range in.memLimits {
		in.memLimits[i] = before
		if in.hourly.start.Add(time.Duration(i) * time.Hour).After(t0.Add(time.Duration(raisedOn) * day)) {
			in.memLimits[i] = after
		}
	}
}

// OOM-killed at 512Mi twice, then raised to what was recommended, with no
// kill since: following the advice must settle, not step memory up again
// from each new limit until the kills leave the window.
func TestAnalyzeOOMFollowedAdviceSettles(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	base := synth(14, 1, diurnal(rng, 0.1, 0.2), func(i int) float64 {
		if i < 7*288 {
			return 500 * mib * (0.9 + 0.1*rng.Float64())
		}
		return 530 * mib * (0.95 + 0.05*rng.Float64())
	})
	base.oomTimes = []time.Time{t0.Add(3 * day), t0.Add(5 * day)}
	base.restartTimes = base.oomTimes
	limit := 512.0 * mib
	var r ContainerReport
	for refresh := range 4 {
		in := base
		in.cpuMax, in.cpuSum, in.replicas = nil, nil, nil
		in.current = Resources{CPURequest: 0.2, MemRequest: limit, MemLimit: limit}
		limitHistory(&in, 7, 512*mib, limit)
		r = analyze(in)
		if refresh > 0 && r.Memory.Recommended > limit {
			t.Fatalf("refresh %d: %vMi raised again to %vMi with no OOM kill at it", refresh, limit/mib, r.Memory.Recommended/mib)
		}
		limit = r.Memory.Recommended
	}
	if limit != 640*mib {
		t.Fatalf("settled at %vMi, want the 640Mi step over the 512Mi it was killed at", limit/mib)
	}
	if r.Memory.Verdict == VerdictUnder || r.Memory.Censored || hasFinding(r, "oom-killed") || !hasFinding(r, "oom-resolved") || r.OOMLimit != 512*mib {
		t.Fatalf("verdict %s censored %v oom limit %vMi findings %+v", r.Memory.Verdict, r.Memory.Censored, r.OOMLimit/mib, r.Findings)
	}

	// Raised once more by hand, to 2Gi: the floor still holds below it.
	in := base
	in.cpuMax, in.cpuSum, in.replicas = nil, nil, nil
	in.current = Resources{CPURequest: 0.2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	limitHistory(&in, 7, 512*mib, 2*gib)
	if r := analyze(in); r.Memory.Recommended < 640*mib {
		t.Fatalf("recommended %vMi, under the step over the limit it was OOM-killed at", r.Memory.Recommended/mib)
	}

	// Where only total usage is reported, page cache fills whatever room a
	// raise gives: that must not read as the next shortage either.
	limit = 640 * mib
	for refresh := range 3 {
		in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(i int) float64 {
			if i < 7*288 {
				return 500 * mib
			}
			return 0.97 * limit
		})
		in.memIsUsage = true
		in.oomTimes = base.oomTimes
		in.current = Resources{CPURequest: 0.2, MemRequest: limit, MemLimit: limit}
		limitHistory(&in, 7, 512*mib, limit)
		r := analyze(in)
		if r.Memory.Recommended > limit || r.Memory.Verdict == VerdictUnder {
			t.Fatalf("usage metric, refresh %d: %vMi → %vMi, verdict %s", refresh, limit/mib, r.Memory.Recommended/mib, r.Memory.Verdict)
		}
	}
}

// A kill at the raised limit is a kill at that limit: memory steps up from
// it. So is one whose limit history is unknown.
func TestAnalyzeOOMAfterRaiseStepsFromThatLimit(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	in := synth(14, 1, diurnal(rng, 0.1, 0.2), func(int) float64 { return 600 * mib * (0.9 + 0.1*rng.Float64()) })
	in.oomTimes = []time.Time{t0.Add(3 * day), t0.Add(10 * day)}
	in.current = Resources{CPURequest: 0.2, MemRequest: 640 * mib, MemLimit: 640 * mib}
	limitHistory(&in, 7, 512*mib, 640*mib)
	r := analyze(in)
	if r.Memory.Verdict != VerdictUnder || !r.Memory.Censored || r.OOMLimit != 640*mib || r.Memory.Recommended < 800*mib {
		t.Fatalf("verdict %s censored %v oom limit %vMi recommended %vMi", r.Memory.Verdict, r.Memory.Censored, r.OOMLimit/mib, r.Memory.Recommended/mib)
	}

	in.cpuMax, in.cpuSum, in.replicas = nil, nil, nil
	in.oomTimes = in.oomTimes[:1]
	in.memLimits = nil
	if r := analyze(in); r.Memory.Verdict != VerdictUnder || r.Memory.Recommended < 800*mib {
		t.Fatalf("without limit history: verdict %s recommended %vMi", r.Memory.Verdict, r.Memory.Recommended/mib)
	}
}

// An OOM kill on a small limit raises it by at least 100Mi: 25% of 64Mi is
// 16Mi, less than one allocation burst.
func TestAnalyzeOOMBumpHasAnAbsoluteFloor(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 19))
	in := synth(14, 1, diurnal(rng, 0.05, 0.2), flatMem(rng, 60*mib))
	in.current = Resources{CPURequest: 0.1, MemRequest: 64 * mib, MemLimit: 64 * mib}
	in.oomTimes = []time.Time{t0.Add(5 * day)}
	r := analyze(in)
	if r.Memory.Recommended < 164*mib {
		t.Fatalf("recommended %v Mi after an OOM at 64Mi", r.Memory.Recommended/mib)
	}
}

// A one-off 900Mi batch spike, then daily peaks rising from 300Mi to 500Mi:
// following the rise must not forget memory the container already used.
func TestAnalyzeMemoryRiseKeepsEarlierPeak(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 13))
	in := synth(28, 2, diurnal(rng, 0.2, 0.3), func(i int) float64 {
		d, h := i/288, i%288
		base := 300.0
		if d >= 20 {
			base = 500
		}
		if d == 4 && h >= 100 && h < 112 {
			return 900 * mib * (1 + 0.02*rng.Float64())
		}
		return base * mib * (0.97 + 0.03*rng.Float64())
	})
	in.current = Resources{CPURequest: 0.5, MemRequest: 2 * gib, MemLimit: 2 * gib}
	r := analyze(in)
	if r.Memory.ShiftAt == nil {
		t.Fatal("setup: the rise to 500Mi was not detected")
	}
	if r.Memory.Peak < 900*mib || r.Memory.Estimate < 900*mib || r.Memory.Recommended < 900*mib {
		t.Fatalf("peak %.0fMi, estimate %.0fMi, recommended %.0fMi: below the 900Mi already used", r.Memory.Peak/mib, r.Memory.Estimate/mib, r.Memory.Recommended/mib)
	}
}

// Twenty-eight days are replayed as three weeks, each scored against an
// estimate re-fitted on every day before it.
func TestBacktestRollsWeekByWeek(t *testing.T) {
	rng := rand.New(rand.NewPCG(20, 20))
	in := synth(28, 2, diurnal(rng, 0.2, 0.3), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	bt := r.Backtest
	if bt == nil || bt.Folds != 3 || bt.TrainDays != 7 || bt.TestDays != 21 {
		t.Fatalf("backtest %+v", bt)
	}
}

// Averages well under the limit, but 2-minute peaks pressed against it:
// throttled bursts, so CPU must not be cut and the limit should rise.
func TestAnalyzeBurstsAgainstLimitCensorCPU(t *testing.T) {
	rng := rand.New(rand.NewPCG(24, 24))
	in := synth(14, 1, diurnal(rng, 0.2, 0.2), flatMem(rng, 300*mib))
	n := len(in.mem)
	var bursts []pooledPoint
	for i := range n {
		v := 0.3
		if i%20 == 0 {
			v = 0.98 // a burst to the 1-core limit every 100 minutes
		}
		bursts = append(bursts, pooledPoint{int32(i), v})
	}
	in.cpuBurst = buildPooled(n, bursts)
	in.throttleKind = throttleSeconds
	in.current = Resources{CPURequest: 2, CPULimit: 1, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	if !hasFinding(r, "cpu-burst-limit") || !r.CPU.Censored || r.CPU.LimitAction != "raise" || r.CPU.Recommended < 2 {
		t.Fatalf("cpu %+v findings %+v", r.CPU, r.Findings)
	}
}

// An HPA runs 10 replicas at the daily peak, half of them throttled, and 2
// the rest of the time, none throttled. Per step that is throttling 4.9% of
// the time; per replica-time, which is what the request pays for, 18%.
func TestAnalyzeThrottlingWeighsReplicaTime(t *testing.T) {
	rng := rand.New(rand.NewPCG(28, 28))
	in := synth(14, 1, diurnal(rng, 0.2, 0.2), flatMem(rng, 300*mib))
	n := len(in.mem)
	var pts []pooledPoint
	in.throttle = make([]float64, n)
	for i := range n {
		reps := 2
		if i%288 < 28 {
			reps, in.throttle[i] = 10, 0.5
		}
		for range reps {
			pts = append(pts, pooledPoint{int32(i), 0.2 * (1 + 0.1*rng.Float64())})
		}
	}
	in.cpu = buildPooled(n, pts)
	in.throttleKind = throttlePeriods
	in.current = Resources{CPURequest: 0.5, CPULimit: 1, MemRequest: gib, MemLimit: gib}
	r := analyze(in)
	if r.Throttling == nil {
		t.Fatal("no throttling share")
	}
	if *r.Throttling < 0.15 || !r.CPU.Censored || !hasFinding(r, "cpu-limit-pressure") {
		t.Fatalf("throttling %.3f, censored %v, findings %+v", *r.Throttling, r.CPU.Censored, r.Findings)
	}
}

// Keycloak-like: idles at a few millicores, starts at ~100m on a 500m
// request. With in-place resize the steady recommendation stays low and a
// startup boost carries startup; without it, the request stays at the
// startup rate.
func TestAnalyzeStartupBoost(t *testing.T) {
	rng := rand.New(rand.NewPCG(25, 25))
	base := func() containerInput {
		in := synth(14, 3, diurnal(rng, 0.005, 0.2), flatMem(rng, 700*mib))
		in.current = Resources{CPURequest: 0.5, CPULimit: 2, MemRequest: gib, MemLimit: 2 * gib}
		in.startupCPUPeak = 0.116
		return in
	}

	in := base()
	in.inPlaceResize = true
	r := analyze(in)
	sb := r.StartupBoost
	if sb == nil || sb.Floor || sb.Request != 0.5 || r.CPU.Recommended > 0.02 || r.CPU.Verdict != VerdictOver {
		t.Fatalf("in place: boost %+v, cpu rec %v verdict %s", sb, r.CPU.Recommended, r.CPU.Verdict)
	}

	in = base()
	r = analyze(in)
	sb = r.StartupBoost
	if sb == nil || !sb.Floor || r.CPU.Recommended != 0.12 || sb.SteadyRecommended > 0.02 {
		t.Fatalf("no in-place resize: boost %+v, cpu rec %v", sb, r.CPU.Recommended)
	}
}

func TestInPlaceResizeVersions(t *testing.T) {
	for _, c := range []struct {
		major, minor string
		want         bool
	}{{"1", "32", false}, {"1", "33", true}, {"1", "33+", true}, {"1", "35", true}, {"1", "28+", false}} {
		cs := fake.NewSimpleClientset()
		cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{Major: c.major, Minor: c.minor}
		if got := supportsInPlaceResize(cs); got != c.want {
			t.Errorf("%s.%s: %v", c.major, c.minor, got)
		}
	}
}

// A request the usage exceeds more than twice as often as the profile allows
// is under-provisioned, the same line the backtest and the change check draw.
// One at the recommendation stays right-sized, so following it can't flap.
func TestAnalyzeUnderNeedsNoExtraMargin(t *testing.T) {
	for _, p := range []Profile{ProfileConservative, ProfileBalanced, ProfileAggressive} {
		mk := func(req float64) ContainerReport {
			rng := rand.New(rand.NewPCG(31, 31))
			in := synth(14, 2, diurnal(rng, 0.6, 0.3), flatMem(rng, 300*mib))
			in.profile = p
			in.current = Resources{CPURequest: req, MemRequest: gib, MemLimit: gib}
			return analyze(in)
		}
		est := mk(1).CPU.Estimate
		target := 1 - p.params().cpuQuantile
		low := mk(roundCPU(0.8 * est))
		if low.CPU.TimeAboveRequest <= 2*target {
			t.Fatalf("%s: setup: only %.1f%% above a request at 80%% of the need", p, low.CPU.TimeAboveRequest*100)
		}
		if low.CPU.Verdict != VerdictUnder || !hasFinding(low, "cpu-over-request") {
			t.Errorf("%s: over the request %.1f%% of the time against a %.0f%% target, verdict %s", p, low.CPU.TimeAboveRequest*100, target*100, low.CPU.Verdict)
		}
		if at := mk(roundCPU(est)); at.CPU.Verdict != VerdictRight {
			t.Errorf("%s: a request at the %.3f recommendation is %s (%.1f%% above)", p, roundCPU(est), at.CPU.Verdict, at.CPU.TimeAboveRequest*100)
		}
	}
}

// The material-change floor is for requests near the need. A 5m request on a
// container using ~23m is only millicores short, but under its usage nearly
// all the time: under-provisioned, not right-sized.
func TestAnalyzeSmallRequestFarBelowNeedIsUnder(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 41))
	in := synth(14, 2, diurnal(rng, 0.012, 0.3), flatMem(rng, 100*mib))
	in.current = Resources{CPURequest: 0.005, MemRequest: 200 * mib, MemLimit: 200 * mib}
	r := analyze(in)
	if raise := roundCPU(r.CPU.Estimate) - 0.005; raise >= minCPUDelta || r.CPU.TimeAboveRequest < 0.5 {
		t.Fatalf("setup: raise %.3f, above the request %.0f%% of the time", raise, r.CPU.TimeAboveRequest*100)
	}
	if r.CPU.Verdict != VerdictUnder || !hasFinding(r, "cpu-over-request") || r.CPU.Recommended <= 0.005 {
		t.Fatalf("verdict %s, %v cores recommended, findings %+v", r.CPU.Verdict, r.CPU.Recommended, r.Findings)
	}
}
