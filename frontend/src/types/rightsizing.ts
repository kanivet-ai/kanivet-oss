// Mirrors backend/internal/rightsizing/types.go. CPU values are cores,
// memory values are bytes; 0 means unset.

export type RightsizingProfile = 'conservative' | 'balanced' | 'aggressive';
export type RightsizingWindow = '7d' | '14d' | '28d';

export type Verdict =
  | 'insufficient-data'
  | 'under-provisioned'
  | 'no-requests'
  | 'hpa-coupled'
  | 'over-provisioned'
  | 'right-sized';

export type Severity = 'critical' | 'warning' | 'info' | 'good';

export interface Finding {
  code: string;
  severity: Severity;
  resource?: 'cpu' | 'memory';
  /** A tag of a few words for rows, e.g. "OOM ×5". */
  title: string;
  message: string;
}

export interface ResourceRec {
  request: number;
  limit: number;
  recommended: number;
  recommendedLimit: number;
  limitAction: 'keep' | 'set' | 'raise' | 'none';
  low: number;
  high: number;
  estimate: number;
  verdict: Verdict;
  timeAboveRequest: number;
  p50: number;
  p90: number;
  p95: number;
  p99: number;
  peak: number;
  trendFactor?: number;
  shiftAt?: string;
  shiftRatio?: number;
  censored?: boolean;
  /** Only in the evidence response; reports leave it out. */
  explain?: string[];
  /** Set for CPU that idles and works in short bursts. */
  burst?: {
    activeShare: number;
    need: number;
    peak: number;
    idleRecommended: number;
  };
}

export interface DataQuality {
  days: number;
  coverage: number;
  samples: number;
  first: string;
  runs?: number;
  /** Jobs: share of the time since the first run that a run was going. */
  dutyCycle?: number;
  newPeakChance?: number;
}

export interface StartupBoost {
  /** CPU request while the pod starts. */
  request: number;
  /** Observed CPU in the first minutes after start (a step-long average). */
  startupRate: number;
  /** The cluster can resize pods in place (Kubernetes 1.33+). */
  inPlace: boolean;
  /** No in-place resize: the steady recommendation was kept at the startup rate. */
  floor?: boolean;
  steadyRecommended?: number;
  /** A pod label that picks out the workload. */
  selector?: Record<string, string>;
}

export interface Backtest {
  trainDays: number;
  testDays: number;
  cpuRecommended: number;
  cpuExceedance: number;
  cpuTarget: number;
  memRecommended: number;
  memTestPeak: number;
  memBreached: boolean;
  /** Weeks scored, each against an estimate re-fitted on every day before it. */
  folds: number;
  memBreaches: number;
  calibrated: boolean;
  bursty?: boolean;
}

export interface HPACoupling {
  name: string;
  resource: 'cpu' | 'memory';
  targetUtilization: number;
  suggestedTarget: number;
  pairedRequest: number;
}

export interface ContainerReport {
  /** JVM heap sizing, when the container runs a JVM. */
  jvm?: { heapMax?: number; ramPercentage?: number };
  /** A fixed heap ceiling of another runtime (Node, .NET or Go). */
  heap?: { runtime: 'node' | 'dotnet' | 'go'; setting: string; max: number };
  /** Roughly when the running version was rolled out. */
  versionSince?: string;
  /** CPU for startup only, when the steady request is far below startup CPU. */
  startupBoost?: StartupBoost;
  container: string;
  verdict: Verdict;
  confidence: 'high' | 'medium' | 'low';
  cpu: ResourceRec;
  memory: ResourceRec;
  data: DataQuality;
  backtest?: Backtest;
  findings: Finding[];
  avgReplicas: number;
  imbalance?: number;
  oomKills: number;
  /** The highest memory limit it was OOM-killed at; below today's once raised. */
  oomLimit?: number;
  restarts: number;
  startupCpuPeak?: number;
  /** Share of replica-time throttled in over 5% of its CFS periods. */
  throttling?: number;
  hpa?: HPACoupling;
  /** $ per core-month and per GiB-month of request, across the average replica count. */
  cpuMonthly: number;
  memMonthly: number;
  /** What this container's recommendation is worth per month (negative = needs more). */
  monthlySavings: number;
}

export interface Change {
  at: string;
  resource: 'cpu' | 'memory';
  from: number;
  to: number;
  timeAboveAfter: number;
  oomKillsAfter: number;
  peakAfter: number;
  healthy: boolean;
  summary: string;
  daysObserved: number;
  followedAdvice?: boolean;
}

export interface Dismissal {
  container?: string;
  reason: string;
  until?: string;
  createdAt: string;
}

export interface WorkloadReport {
  namespace: string;
  kind: string;
  name: string;
  /** Pod labels teams filter by: app, Helm release, team. */
  labels?: Record<string, string>;
  vcluster?: string;
  vclusterNamespace?: string;
  replicas: number;
  verdict: Verdict;
  confidence: 'high' | 'medium' | 'low';
  containers: ContainerReport[];
  priced: boolean;
  monthlyCost: number;
  monthlySavings: number;
  savingsLow: number;
  savingsHigh: number;
  change?: Change;
  dismissed?: Dismissal[];
  /** The one HPA target change the workload pairs with; pairedRequest is the pod's total. */
  hpa?: HPACoupling;
  riskScore: number;
}

export interface Signals {
  oomKills: boolean;
  throttling: boolean;
  startupExclusion: boolean;
  requestHistory: boolean;
  memoryMetric: 'working-set' | 'usage';
  throttleKind?: 'periods' | 'seconds';
}

export interface Calibration {
  containers: number;
  calibrated: number;
  medianCpuExceedance: number;
  cpuTarget: number;
  memBreaches: number;
}

export interface RightsizingSummary {
  workloads: number;
  over: number;
  under: number;
  right: number;
  insufficient: number;
  hpaCoupled: number;
  noRequests: number;
  monthlySavings: number;
  savingsLow: number;
  savingsHigh: number;
  addedCost: number;
  dismissed: number;
  /** Workloads that make up monthlySavings. */
  shrinking: number;
}

export type ReportStatus =
  | 'ready'
  | 'computing'
  | 'no-history-source'
  | 'needs-tenant'
  | 'no-container-data'
  | 'error';

export interface RightsizingReport {
  cluster: string;
  status: ReportStatus;
  source: {
    type?: string;
    flavor?: string;
    namespace?: string;
    service?: string;
    reason?: string;
    metricsServer?: boolean;
  };
  window: RightsizingWindow;
  step: string;
  profile: RightsizingProfile;
  asOf: string;
  computedAt: string;
  durationMs: number;
  progress?: { done: number; total: number; stage?: string; paused?: boolean };
  signals: Signals;
  calibration: Calibration;
  summary: RightsizingSummary;
  workloads: WorkloadReport[];
  error?: string;
  stale?: boolean;
  refreshError?: string;
  /** Identifies the workloads as returned; send it back as `known`. */
  version?: string;
  /** The workloads match `known`: none are sent, keep the ones held. */
  unchanged?: boolean;
}

export interface Hourly {
  start: string;
  cpuP50: (number | null)[];
  cpuP95: (number | null)[];
  cpuMax: (number | null)[];
  memMax: (number | null)[];
  replicas: (number | null)[];
  requests?: { at: string; cpu: number; mem: number }[];
}

export interface Distribution {
  q: number[];
  cpu: (number | null)[];
  mem: (number | null)[];
  dailyMemPeaks: number[];
  dailyCpuP95: number[];
}

export interface EvidenceEvent {
  at: string;
  kind: 'oom' | 'restart' | 'shift-cpu' | 'shift-memory' | 'request-change';
  text?: string;
}

export interface Evidence {
  workload: WorkloadReport;
  step: string;
  series: Record<string, Hourly>;
  distributions: Record<string, Distribution>;
  events: Record<string, EvidenceEvent[]>;
  profiles: Record<
    string,
    Record<RightsizingProfile, { cpu: number; memory: number }>
  >;
  asOf: string;
}
