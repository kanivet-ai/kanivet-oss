package rightsizing

import (
	"math"
	"strconv"
	"time"
)

// Profile is the user's risk appetite. It moves the CPU quantile and the
// memory margin, nothing else.
type Profile string

const (
	ProfileConservative Profile = "conservative"
	ProfileBalanced     Profile = "balanced"
	ProfileAggressive   Profile = "aggressive"
)

type profileParams struct {
	cpuQuantile float64 // quantile of pooled replica CPU the request covers
	// Memory headroom over the peak is a share of it or a floor in bytes,
	// whichever is larger. The floor carries small containers, whose normal
	// jitter is a large share of their size: they were most of the misses.
	// Replayed on ~6,600 real containers (train 14 days, score the next
	// unseen week), balanced went from 4.8% of container-weeks over the
	// recommendation with 15% alone to 3.3% with the 32Mi floor, for 16% more
	// headroom in total. Headroom scaled to the day-to-day swing of the peak
	// did as well only at twice the memory. A flat 5% (the old aggressive)
	// missed 10-14%.
	memMargin float64
	memFloor  float64
}

func (p Profile) params() profileParams {
	switch p {
	case ProfileConservative:
		return profileParams{cpuQuantile: 0.99, memMargin: 0.30, memFloor: 64 * mib}
	case ProfileAggressive:
		return profileParams{cpuQuantile: 0.90, memMargin: 0.10, memFloor: 16 * mib}
	default:
		return profileParams{cpuQuantile: 0.95, memMargin: 0.15, memFloor: 32 * mib}
	}
}

// ParseProfile maps a query value to a profile, defaulting to balanced.
func ParseProfile(s string) Profile {
	switch Profile(s) {
	case ProfileConservative, ProfileAggressive:
		return Profile(s)
	}
	return ProfileBalanced
}

type Verdict string

// Verdicts, in the order the overall verdict picks them.
const (
	VerdictInsufficient Verdict = "insufficient-data"
	VerdictUnder        Verdict = "under-provisioned"
	VerdictNoRequests   Verdict = "no-requests"
	VerdictHPACoupled   Verdict = "hpa-coupled"
	VerdictOver         Verdict = "over-provisioned"
	VerdictRight        Verdict = "right-sized"
)

// Finding is one plain-language observation behind a verdict.
type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // critical | warning | info | good
	Resource string `json:"resource,omitempty"`
	// Title is a tag of a few words for scanning ("OOM ×5"); Message is the
	// full explanation shown on demand.
	Title   string `json:"title"`
	Message string `json:"message"`
}

// ResourceRec is the analysis of one resource of one container. CPU values are
// cores, memory values are bytes; 0 means unset.
type ResourceRec struct {
	Request          float64 `json:"request"`
	Limit            float64 `json:"limit"`
	Recommended      float64 `json:"recommended"`
	RecommendedLimit float64 `json:"recommendedLimit"`
	// LimitAction is keep | set | raise | none.
	LimitAction string `json:"limitAction"`
	// Low and High bound what the container plausibly needs: a 90% bootstrap
	// interval for CPU, the observed peak to the worst-day-in-a-month return
	// level for memory, both with the same margin as Recommended.
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
	Estimate float64 `json:"estimate"`
	Verdict  Verdict `json:"verdict"`
	// TimeAboveRequest is the share of samples above the current request.
	TimeAboveRequest float64 `json:"timeAboveRequest"`
	P50              float64 `json:"p50"`
	P90              float64 `json:"p90"`
	P95              float64 `json:"p95"`
	P99              float64 `json:"p99"`
	Peak             float64 `json:"peak"`
	// TrendFactor is the projected 7-day growth applied, when > 1.
	TrendFactor float64    `json:"trendFactor,omitempty"`
	ShiftAt     *time.Time `json:"shiftAt,omitempty"`
	ShiftRatio  float64    `json:"shiftRatio,omitempty"`
	// Censored means usage was capped (OOM kill, CPU at its limit), so the
	// true need is higher than anything observed.
	Censored bool `json:"censored,omitempty"`
	// Explain walks from the data to the number, one step per line. Only the
	// single-workload evidence carries it; reports leave it out.
	Explain []string `json:"explain,omitempty"`
	// Burst is set for CPU that idles and works in short bursts.
	Burst *Burst `json:"burst,omitempty"`
}

// Burst describes CPU that is idle most of the time and busy in bursts.
type Burst struct {
	ActiveShare float64 `json:"activeShare"` // share of time busy
	Need        float64 `json:"need"`        // P90 of CPU while busy
	Peak        float64 `json:"peak"`
	// IdleRecommended is what a replica-time quantile would ask for: cheaper,
	// with bursts that run on spare node CPU only.
	IdleRecommended float64 `json:"idleRecommended"`
}

type DataQuality struct {
	Days     float64   `json:"days"`
	Coverage float64   `json:"coverage"`
	Samples  int       `json:"samples"`
	First    time.Time `json:"first"`
	Runs     int       `json:"runs,omitempty"`
	// DutyCycle is, for Jobs and CronJobs, the share of the time since the
	// first sample that a run was going: AvgReplicas counts only those steps.
	DutyCycle float64 `json:"dutyCycle,omitempty"`
	// NewPeakChance is the chance that at least one of the next 30 days beats
	// every past daily memory peak: k/(n+k) for n exchangeable past days and
	// k = 30. For a single day it would be 1/(n+1), which understates how
	// likely the peak is to be beaten while a recommendation is in place;
	// the headroom is what covers it.
	NewPeakChance float64 `json:"newPeakChance,omitempty"`
}

// Backtest scores the procedure on days it did not see.
type Backtest struct {
	TrainDays      int     `json:"trainDays"`
	TestDays       int     `json:"testDays"`
	CPURecommended float64 `json:"cpuRecommended"`
	CPUExceedance  float64 `json:"cpuExceedance"`
	CPUTarget      float64 `json:"cpuTarget"`
	MemRecommended float64 `json:"memRecommended"`
	MemTestPeak    float64 `json:"memTestPeak"`
	MemBreached    bool    `json:"memBreached"`
	Calibrated     bool    `json:"calibrated"`
	// Folds is how many weeks were scored, each against an estimate re-fitted
	// on every day before it; MemBreaches counts the weeks whose memory peak
	// went over. With several folds, the memory figures are the worst week's
	// and the CPU recommendation is the latest one.
	Folds       int `json:"folds"`
	MemBreaches int `json:"memBreaches"`
	// Bursty means CPU was scored on busy time only, as it is recommended.
	Bursty bool `json:"bursty,omitempty"`
}

// StartupBoost recommends CPU for startup only: Request while the pod starts,
// back to the steady recommendation once it is Ready.
type StartupBoost struct {
	Request     float64 `json:"request"`     // CPU request during startup
	StartupRate float64 `json:"startupRate"` // observed CPU in the first minutes, a step-long average
	// InPlace: the cluster can resize running pods (Kubernetes 1.33+), which
	// a boost needs. Without it, Floor is set: the steady recommendation was
	// kept at the startup rate instead of SteadyRecommended.
	InPlace           bool    `json:"inPlace"`
	Floor             bool    `json:"floor,omitempty"`
	SteadyRecommended float64 `json:"steadyRecommended,omitempty"`
	// Selector is a pod label that picks out the workload, for the config.
	Selector map[string]string `json:"selector,omitempty"`
}

type HPACoupling struct {
	Name              string `json:"name"`
	Resource          string `json:"resource"`
	TargetUtilization int32  `json:"targetUtilization"`
	// SuggestedTarget with PairedRequest keeps today's scaling behaviour.
	// On a workload, PairedRequest is the pod's total over the containers
	// the HPA counts.
	SuggestedTarget int32   `json:"suggestedTarget"`
	PairedRequest   float64 `json:"pairedRequest"`
	// Pod is set when the target counts the summed requests of every
	// container requesting the resource (a Resource metric), not one's.
	Pod bool `json:"pod,omitempty"`
	// usageOnly is what usage alone supported when the HPA held the request
	// up; zero otherwise.
	usageOnly float64
}

// VPAInfo is the VerticalPodAutoscaler that sets some of a container's
// requests at admission, so a patch to them would not last.
type VPAInfo struct {
	Name      string   `json:"name"`
	Mode      string   `json:"mode"`
	Resources []string `json:"resources"` // cpu, memory: the requests it sets
}

type ContainerReport struct {
	Container string   `json:"container"`
	JVM       *JVMInfo `json:"jvm,omitempty"`
	// Heap is a fixed heap ceiling of another runtime: Node, .NET or Go.
	Heap *HeapCeiling `json:"heap,omitempty"`
	// VPA is set when a VerticalPodAutoscaler sets this container's requests.
	VPA *VPAInfo `json:"vpa,omitempty"`
	// VersionSince is roughly when the running version was rolled out.
	VersionSince *time.Time `json:"versionSince,omitempty"`
	// StartupBoost is what the container needs while it starts, when its
	// steady-state request is far below its startup CPU.
	StartupBoost   *StartupBoost `json:"startupBoost,omitempty"`
	Verdict        Verdict       `json:"verdict"`
	Confidence     string        `json:"confidence"` // high | medium | low
	CPU            ResourceRec   `json:"cpu"`
	Memory         ResourceRec   `json:"memory"`
	Data           DataQuality   `json:"data"`
	Backtest       *Backtest     `json:"backtest,omitempty"`
	Findings       []Finding     `json:"findings"`
	AvgReplicas    float64       `json:"avgReplicas"`
	Imbalance      float64       `json:"imbalance,omitempty"`
	OOMKills       int           `json:"oomKills"`
	Restarts       int           `json:"restarts"`
	StartupCPUPeak float64       `json:"startupCpuPeak,omitempty"`
	Throttling     *float64      `json:"throttling,omitempty"` // share of replica-time throttled in over 5% of its periods
	HPA            *HPACoupling  `json:"hpa,omitempty"`
	// OOMLimit is the highest memory limit it was OOM-killed at, which can
	// be below today's when the limit was raised since.
	OOMLimit float64 `json:"oomLimit,omitempty"`
	// CPUMonthly and MemMonthly price one core and one GiB of request for a
	// month across the average replica count, so the UI can price any
	// candidate request; for a Job, only for the share of the month a run is
	// going. Zero when the nodes have no price.
	CPUMonthly float64 `json:"cpuMonthly"`
	MemMonthly float64 `json:"memMonthly"`
	// MonthlySavings is what this container's recommendation is worth
	// (negative when it needs more); zero when nothing is to change.
	MonthlySavings float64 `json:"monthlySavings"`
}

// Change describes the most recent request change seen in the window and how
// the container has behaved since.
type Change struct {
	At             time.Time `json:"at"`
	Resource       string    `json:"resource"`
	From           float64   `json:"from"`
	To             float64   `json:"to"`
	TimeAboveAfter float64   `json:"timeAboveAfter"`
	OOMKillsAfter  int       `json:"oomKillsAfter"`
	PeakAfter      float64   `json:"peakAfter"`
	Healthy        bool      `json:"healthy"`
	Summary        string    `json:"summary"`
	DaysObserved   float64   `json:"daysObserved"`
	FollowedAdvice bool      `json:"followedAdvice,omitempty"`
}

type Dismissal struct {
	Container string     `json:"container,omitempty"`
	Reason    string     `json:"reason"`
	Until     *time.Time `json:"until,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type WorkloadReport struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	// Labels are the pod labels teams filter by: app, Helm release, team.
	Labels            map[string]string `json:"labels,omitempty"`
	VCluster          string            `json:"vcluster,omitempty"`
	VClusterNamespace string            `json:"vclusterNamespace,omitempty"`
	Replicas          int               `json:"replicas"`
	Verdict           Verdict           `json:"verdict"`
	Confidence        string            `json:"confidence"`
	Containers        []ContainerReport `json:"containers"`
	Priced            bool              `json:"priced"`
	MonthlyCost       float64           `json:"monthlyCost"`
	MonthlySavings    float64           `json:"monthlySavings"`
	SavingsLow        float64           `json:"savingsLow"`
	SavingsHigh       float64           `json:"savingsHigh"`
	Change            *Change           `json:"change,omitempty"`
	Dismissed         []Dismissal       `json:"dismissed,omitempty"`
	// HPA is the one target change for the workload's HPA, when the
	// recommendation pairs with it: what the containers' couplings share.
	HPA *HPACoupling `json:"hpa,omitempty"`
	// RiskScore orders the triage table: higher is more urgent.
	RiskScore float64 `json:"riskScore"`
}

type Signals struct {
	OOMKills         bool `json:"oomKills"`
	Throttling       bool `json:"throttling"`
	StartupExclusion bool `json:"startupExclusion"`
	RequestHistory   bool `json:"requestHistory"`
	// MemoryMetric is "working-set", or "usage" when only total usage
	// (including page cache) exists, which overstates the need.
	MemoryMetric string `json:"memoryMetric"`
	// ThrottleKind is "periods" (drives verdicts), "seconds" (a hint) or "".
	ThrottleKind string `json:"throttleKind,omitempty"`
}

type Calibration struct {
	Containers          int     `json:"containers"`
	Calibrated          int     `json:"calibrated"`
	MedianCPUExceedance float64 `json:"medianCpuExceedance"`
	CPUTarget           float64 `json:"cpuTarget"`
	MemBreaches         int     `json:"memBreaches"`
}

type Summary struct {
	Workloads      int     `json:"workloads"`
	Over           int     `json:"over"`
	Under          int     `json:"under"`
	Right          int     `json:"right"`
	Insufficient   int     `json:"insufficient"`
	HPACoupled     int     `json:"hpaCoupled"`
	NoRequests     int     `json:"noRequests"`
	MonthlySavings float64 `json:"monthlySavings"`
	SavingsLow     float64 `json:"savingsLow"`
	SavingsHigh    float64 `json:"savingsHigh"`
	AddedCost      float64 `json:"addedCost"`
	Dismissed      int     `json:"dismissed"`
	// Shrinking counts the workloads that make up MonthlySavings.
	Shrinking int `json:"shrinking"`
}

type SourceInfo struct {
	Type      string `json:"type,omitempty"`
	Flavor    string `json:"flavor,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Service   string `json:"service,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// MetricsServer is true when point-in-time usage exists but no history.
	MetricsServer bool `json:"metricsServer,omitempty"`
}

type Progress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Stage string `json:"stage,omitempty"`
	// Paused is set while the metrics store's circuit breaker holds queries.
	Paused bool `json:"paused,omitempty"`
}

// Report statuses.
const (
	StatusReady           = "ready"
	StatusComputing       = "computing"
	StatusNoHistorySource = "no-history-source"
	StatusNeedsTenant     = "needs-tenant"
	// StatusNoContainerData: the store answers but has no container metrics
	// for this cluster, typically the wrong tenant or service.
	StatusNoContainerData = "no-container-data"
	StatusError           = "error"
)

type Report struct {
	evidenceInputs *evidenceInputs

	Cluster     string           `json:"cluster"`
	Status      string           `json:"status"`
	Source      SourceInfo       `json:"source"`
	Window      string           `json:"window"`
	Step        string           `json:"step"`
	Profile     Profile          `json:"profile"`
	AsOf        time.Time        `json:"asOf"`
	ComputedAt  time.Time        `json:"computedAt"`
	DurationMs  int64            `json:"durationMs"`
	Progress    *Progress        `json:"progress,omitempty"`
	Signals     Signals          `json:"signals"`
	Calibration Calibration      `json:"calibration"`
	Summary     Summary          `json:"summary"`
	Workloads   []WorkloadReport `json:"workloads"`
	Error       string           `json:"error,omitempty"`
	// Stale is set when this is a persisted report from an earlier run being
	// shown while a fresh one computes.
	Stale bool `json:"stale,omitempty"`
	// Engine is the reportEngine that computed the report; persisted reports
	// of an earlier release have none.
	Engine int `json:"engine,omitempty"`
	// RefreshError is why the latest refresh failed while this older report
	// is still being shown.
	RefreshError string `json:"refreshError,omitempty"`
	// Version identifies the workloads as returned, dismissals included. A
	// poll that names the version it already has gets Unchanged and no
	// workloads back, instead of the whole report again.
	Version   string `json:"version,omitempty"`
	Unchanged bool   `json:"unchanged,omitempty"`
}

// Evidence is everything the drawer needs to show one workload's analysis.
type Evidence struct {
	Workload WorkloadReport       `json:"workload"`
	Step     string               `json:"step"`
	Series   map[string]Hourly    `json:"series"` // by container
	Dist     map[string]Dist      `json:"distributions"`
	Events   map[string][]Event   `json:"events"`
	Profiles map[string]Snapshots `json:"profiles"` // container -> profile -> rec
	AsOf     time.Time            `json:"asOf"`
}

// Hourly is a downsampled view of usage: CPU across every replica's samples
// in each hour, memory as the busiest replica's peak.
type Hourly struct {
	Start    time.Time  `json:"start"`
	CPUP50   Floats     `json:"cpuP50"` // across replicas, within the hour
	CPUP95   Floats     `json:"cpuP95"`
	CPUMax   Floats     `json:"cpuMax"` // busiest replica
	MemMax   Floats     `json:"memMax"`
	Replicas Floats     `json:"replicas"`
	Requests []ReqPoint `json:"requests,omitempty"`
}

// ReqPoint is a request value in effect from At on.
type ReqPoint struct {
	At  time.Time `json:"at"`
	CPU float64   `json:"cpu"`
	Mem float64   `json:"mem"`
}

// Dist is a quantile grid: Values[i] is the Q[i] quantile. The slider reads
// exceedance off it for any candidate request.
type Dist struct {
	Q          Floats `json:"q"`
	CPU        Floats `json:"cpu"`
	Mem        Floats `json:"mem"`
	DailyPeaks Floats `json:"dailyMemPeaks"`
	DailyCPU   Floats `json:"dailyCpuP95"`
}

// Floats encodes NaN (a missing sample) as null, which JSON can carry and
// charts render as a gap.
type Floats []float64

func (f Floats) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(f)*8+2)
	b = append(b, '[')
	for i, v := range f {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b = append(b, "null"...)
		} else {
			b = strconv.AppendFloat(b, v, 'g', 6, 64)
		}
	}
	return append(b, ']'), nil
}

type Event struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // oom | restart | shift-cpu | shift-memory | request-change
	Text string    `json:"text,omitempty"`
}

type Snapshots map[Profile]ProfileRec

type ProfileRec struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}
