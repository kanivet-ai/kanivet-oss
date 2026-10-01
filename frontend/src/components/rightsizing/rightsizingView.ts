import type {
  StartupBoost,
  ContainerReport,
  Finding,
  Distribution,
  RightsizingProfile,
  Verdict,
  WorkloadReport,
} from '../../types/rightsizing';

const MI = 1024 * 1024;
const GI = 1024 * MI;

export type Tone =
  | 'danger'
  | 'warning'
  | 'success'
  | 'info'
  | 'neutral'
  | 'purple';

export const VERDICT_META: Record<
  Verdict,
  { label: string; short: string; tone: Tone; order: number }
> = {
  'under-provisioned': {
    label: 'Under-provisioned',
    short: 'At risk',
    tone: 'danger',
    order: 0,
  },
  'no-requests': {
    label: 'No requests',
    short: 'Set requests',
    tone: 'warning',
    order: 1,
  },
  'over-provisioned': {
    label: 'Over-provisioned',
    short: 'Can shrink',
    tone: 'info',
    order: 2,
  },
  'hpa-coupled': {
    label: 'Scaled by HPA',
    short: 'Change with HPA',
    tone: 'purple',
    order: 3,
  },
  'right-sized': {
    label: 'Right-sized',
    short: 'Right-sized',
    tone: 'success',
    order: 4,
  },
  'insufficient-data': {
    label: 'Not enough data',
    short: 'Gathering data',
    tone: 'neutral',
    order: 5,
  },
};

export const PROFILE_META: Record<
  RightsizingProfile,
  { label: string; cpu: string; mem: string }
> = {
  conservative: { label: 'Conservative', cpu: 'P99', mem: '+30%' },
  balanced: { label: 'Balanced', cpu: 'P95', mem: '+15%' },
  aggressive: { label: 'Aggressive', cpu: 'P90', mem: '+5%' },
};

// ---- Formatting --------------------------------------------------------------

/** 0.22 → "220m", 1.5 → "1.5 cores". */
export function formatCores(cores: number): string {
  if (!cores) return '—';
  if (cores >= 1) {
    const v = Math.round(cores * 100) / 100;
    return `${v} core${v === 1 ? '' : 's'}`;
  }
  const m = cores * 1000;
  return m < 10 ? `${m.toFixed(1)}m` : `${Math.round(m)}m`;
}

/** Bytes in the units people write: "640Mi", "1.25Gi". */
export function formatMem(bytes: number): string {
  if (!bytes) return '—';
  if (bytes >= GI) {
    const v = bytes / GI;
    return `${v >= 10 ? v.toFixed(1) : (Math.round(v * 100) / 100).toString()}Gi`;
  }
  if (bytes >= MI) return `${Math.round(bytes / MI)}Mi`;
  return `${Math.round(bytes / 1024)}Ki`;
}

export function formatResource(resource: 'cpu' | 'memory', v: number): string {
  return resource === 'cpu' ? formatCores(v) : formatMem(v);
}

/** A Kubernetes quantity for YAML and kubectl: "220m", "2", "640Mi", "2Gi". */
export function quantity(resource: 'cpu' | 'memory', v: number): string {
  if (resource === 'cpu') {
    const m = Math.round(v * 1000);
    return m % 1000 === 0 ? String(m / 1000) : `${m}m`;
  }
  const mi = Math.round(v / MI);
  return mi % 1024 === 0 ? `${mi / 1024}Gi` : `${mi}Mi`;
}

export function formatPct(fraction: number, digits = 1): string {
  const pct = fraction * 100;
  if (pct > 0 && pct < 0.1) return '<0.1%';
  return `${pct.toFixed(pct >= 10 ? 0 : digits)}%`;
}

export function formatMoney(v: number): string {
  const a = Math.abs(v);
  const s =
    a >= 1000
      ? `$${Math.round(a).toLocaleString('en-US')}`
      : a >= 10
        ? `$${Math.round(a)}`
        : `$${a.toFixed(2)}`;
  return v < 0 ? `−${s}` : s;
}

export function relativeTime(iso: string, now = Date.now()): string {
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

// ---- The slider: reading a quantile grid -----------------------------------

/** Share of samples above x, read off a quantile grid by interpolation. */
export function exceedance(
  q: number[],
  values: (number | null)[],
  x: number,
): number {
  const pts: [number, number][] = [];
  q.forEach((qi, i) => {
    const v = values[i];
    if (v !== null && v !== undefined && Number.isFinite(v)) pts.push([qi, v]);
  });
  if (pts.length === 0) return 0;
  if (x < pts[0][1]) return 1 - pts[0][0];
  if (x >= pts[pts.length - 1][1]) return 0;
  for (let i = 1; i < pts.length; i++) {
    const [q0, v0] = pts[i - 1];
    const [q1, v1] = pts[i];
    if (x < v1) {
      const t = v1 === v0 ? 1 : (x - v0) / (v1 - v0);
      return 1 - (q0 + t * (q1 - q0));
    }
  }
  return 0;
}

/** The value at quantile p, interpolated on the grid. */
export function quantileAt(
  q: number[],
  values: (number | null)[],
  p: number,
): number {
  let prev: [number, number] | null = null;
  for (let i = 0; i < q.length; i++) {
    const v = values[i];
    if (v === null || v === undefined) continue;
    if (q[i] >= p) {
      if (!prev || q[i] === prev[0]) return v;
      const t = (p - prev[0]) / (q[i] - prev[0]);
      return prev[1] + t * (v - prev[1]);
    }
    prev = [q[i], v];
  }
  return prev ? prev[1] : 0;
}

export interface CandidateEval {
  cpuTimeAbove: number;
  memPeakHeadroom: number; // candidate / observed peak - 1
  memDaysOver: number; // daily peaks above the candidate
  monthlyDelta: number; // positive = saves money
}

/** What a candidate request would have meant over the window. */
export function evaluateCandidate(
  c: ContainerReport,
  dist: Distribution | undefined,
  cpu: number,
  mem: number,
): CandidateEval {
  const cpuTimeAbove = dist
    ? exceedance(dist.q, dist.cpu, cpu)
    : c.cpu.timeAboveRequest;
  const peak = c.memory.peak || 0;
  const memDaysOver = dist
    ? dist.dailyMemPeaks.filter((p) => p > mem).length
    : 0;
  const monthlyDelta =
    (c.cpu.request - cpu) * c.cpuMonthly +
    ((c.memory.request - mem) / GI) * c.memMonthly;
  return {
    cpuTimeAbove,
    memPeakHeadroom: peak > 0 ? mem / peak - 1 : 0,
    memDaysOver,
    monthlyDelta,
  };
}

/** Snap a CPU value to the steps the backend rounds to. */
export function snapCPU(cores: number): number {
  const m = cores * 1000;
  // Fine steps where small workloads live: a slider over 2m-15m with 5m
  // steps had three positions.
  const step = m < 10 ? 1 : m < 100 ? 5 : m < 1000 ? 10 : 50;
  return (Math.ceil(m / step - 1e-9) * step) / 1000;
}

export function snapMem(bytes: number): number {
  const step =
    bytes < 128 * MI
      ? 4 * MI
      : bytes < GI
        ? 16 * MI
        : bytes < 4 * GI
          ? 64 * MI
          : 256 * MI;
  return Math.ceil(bytes / step - 1e-9) * step;
}

// ---- Copy-ready output ------------------------------------------------------

export interface ContainerChoice {
  container: string;
  cpu: number;
  memory: number;
  cpuLimit: number; // 0 = no CPU limit
  memoryLimit: number;
}

/** The recommendation as a choice, honouring the limit actions. */
export function choiceFromRec(
  c: ContainerReport,
  cpu = c.cpu.recommended,
  memory = c.memory.recommended,
): ContainerChoice {
  let cpuLimit = 0;
  if (c.cpu.limitAction === 'keep' || c.cpu.limitAction === 'raise') {
    cpuLimit = Math.max(c.cpu.recommendedLimit, cpu);
  }
  return { container: c.container, cpu, memory, cpuLimit, memoryLimit: memory };
}

const TEMPLATE_PATH: Record<string, string[]> = {
  CronJob: ['spec', 'jobTemplate', 'spec', 'template'],
};

/** Meshes inject these containers and rewrite their resources at admission;
 * pod template annotations are how their resources are set. */
const SIDECAR_ANNOTATIONS: Record<
  string,
  { cpu: string; cpuLimit: string; mem: string; memLimit: string }
> = {
  'istio-proxy': {
    cpu: 'sidecar.istio.io/proxyCPU',
    cpuLimit: 'sidecar.istio.io/proxyCPULimit',
    mem: 'sidecar.istio.io/proxyMemory',
    memLimit: 'sidecar.istio.io/proxyMemoryLimit',
  },
  'linkerd-proxy': {
    cpu: 'config.linkerd.io/proxy-cpu-request',
    cpuLimit: 'config.linkerd.io/proxy-cpu-limit',
    mem: 'config.linkerd.io/proxy-memory-request',
    memLimit: 'config.linkerd.io/proxy-memory-limit',
  },
};

export const isInjectedSidecar = (container: string) =>
  container in SIDECAR_ANNOTATIONS;

function sidecarAnnotations(choices: ContainerChoice[]): [string, string][] {
  const out: [string, string][] = [];
  for (const c of choices) {
    const a = SIDECAR_ANNOTATIONS[c.container];
    if (!a) continue;
    out.push([a.cpu, quantity('cpu', c.cpu)]);
    if (c.cpuLimit > 0) out.push([a.cpuLimit, quantity('cpu', c.cpuLimit)]);
    out.push(
      [a.mem, quantity('memory', c.memory)],
      [a.memLimit, quantity('memory', c.memoryLimit)],
    );
  }
  return out;
}

/** A strategic-merge patch for the workload's pod template. Injected mesh
 * sidecars get annotations; everything else gets container resources. */
export function patchYAML(kind: string, choices: ContainerChoice[]): string {
  const path = TEMPLATE_PATH[kind] ?? ['spec', 'template'];
  const lines: string[] = [];
  path.forEach((key, depth) => lines.push(`${'  '.repeat(depth)}${key}:`));
  const base = '  '.repeat(path.length);
  const annotations = sidecarAnnotations(choices);
  if (annotations.length > 0) {
    lines.push(`${base}metadata:`, `${base}  annotations:`);
    for (const [k, v] of annotations) lines.push(`${base}    ${k}: "${v}"`);
  }
  const regular = choices.filter((c) => !isInjectedSidecar(c.container));
  if (regular.length === 0) return lines.join('\n');
  lines.push(`${base}spec:`);
  const ind = `${base}  `;
  lines.push(`${ind}containers:`);
  // A value of 0 means "not set, and no data to set it from": leave it out.
  for (const c of regular.filter((x) => x.cpu > 0 || x.memory > 0)) {
    lines.push(`${ind}  - name: ${c.container}`);
    lines.push(`${ind}    resources:`);
    lines.push(`${ind}      requests:`);
    if (c.cpu > 0) lines.push(`${ind}        cpu: ${quantity('cpu', c.cpu)}`);
    if (c.memory > 0)
      lines.push(`${ind}        memory: ${quantity('memory', c.memory)}`);
    if (c.cpuLimit > 0 || c.memoryLimit > 0) {
      lines.push(`${ind}      limits:`);
      if (c.cpuLimit > 0)
        lines.push(`${ind}        cpu: ${quantity('cpu', c.cpuLimit)}`);
      if (c.memoryLimit > 0)
        lines.push(
          `${ind}        memory: ${quantity('memory', c.memoryLimit)}`,
        );
    }
  }
  return lines.join('\n');
}

const SET_RESOURCES_KINDS = new Set([
  'Deployment',
  'StatefulSet',
  'DaemonSet',
  'ReplicaSet',
  'Job',
  'ReplicationController',
]);

/** `kubectl set resources` lines (and a patch for mesh sidecar annotations),
 * or null for kinds `set resources` can't change. */
export function kubectlCommands(
  w: Pick<WorkloadReport, 'kind' | 'name' | 'namespace' | 'vclusterNamespace'>,
  choices: ContainerChoice[],
): string | null {
  if (!SET_RESOURCES_KINDS.has(w.kind)) return null;
  const ns = w.vclusterNamespace || w.namespace;
  const annotations = sidecarAnnotations(choices);
  const patch = annotations.length
    ? [
        `kubectl -n ${ns} patch ${w.kind.toLowerCase()}/${w.name} --type merge \\\n  -p '${JSON.stringify({ spec: { template: { metadata: { annotations: Object.fromEntries(annotations) } } } })}'`,
      ]
    : [];
  return [
    ...choices
      .filter((c) => !isInjectedSidecar(c.container))
      .map((c) => {
        const limits = [
          c.cpuLimit > 0 ? `cpu=${quantity('cpu', c.cpuLimit)}` : '',
          `memory=${quantity('memory', c.memoryLimit)}`,
        ]
          .filter(Boolean)
          .join(',');
        return `kubectl -n ${ns} set resources ${w.kind.toLowerCase()}/${w.name} -c ${c.container} \\\n  --requests=cpu=${quantity('cpu', c.cpu)},memory=${quantity('memory', c.memory)} \\\n  --limits=${limits}`;
      }),
    ...patch,
  ].join('\n');
}

// ---- Triage table -----------------------------------------------------------

export type SortKey = 'risk' | 'savings' | 'name' | 'cpu' | 'memory';
export type GroupKey = 'namespace' | 'release' | 'none';

export interface TriageFilters {
  verdicts: Set<Verdict>;
  search: string;
  showDismissed: boolean;
  namespaces: Set<string>;
  releases: Set<string>;
  teams: Set<string>;
  kinds: Set<string>;
  /** Only workloads saving at least this much per month (0 = any). */
  minSavings: number;
}

export const emptyFilters = (): TriageFilters => ({
  verdicts: new Set(),
  search: '',
  showDismissed: false,
  namespaces: new Set(),
  releases: new Set(),
  teams: new Set(),
  kinds: new Set(),
  minSavings: 0,
});

export const isDismissed = (w: WorkloadReport): boolean => {
  const d = w.dismissed ?? [];
  if (d.some((x) => !x.container)) return true;
  return (
    w.containers.length > 0 &&
    w.containers.every((c) => d.some((x) => x.container === c.container))
  );
};

/** "host › virtual" for vcluster workloads seen from the host. */
export const namespaceOf = (w: WorkloadReport) =>
  w.vclusterNamespace ? `${w.namespace} › ${w.vclusterNamespace}` : w.namespace;

/** The Helm release (or app instance) a workload belongs to. */
export const releaseOf = (w: WorkloadReport) =>
  w.labels?.['app.kubernetes.io/instance'] ?? '';

/** The team a workload belongs to, from the labels teams commonly set. */
export const teamOf = (w: WorkloadReport) =>
  w.labels?.team ??
  w.labels?.owner ??
  w.labels?.['app.kubernetes.io/part-of'] ??
  '';

export interface FacetValue {
  value: string;
  count: number;
}

/** The values of one facet with how many workloads carry each, most first. */
export function facetValues(
  ws: WorkloadReport[],
  of: (w: WorkloadReport) => string,
): FacetValue[] {
  const counts = new Map<string, number>();
  for (const w of ws) {
    const v = of(w);
    if (v) counts.set(v, (counts.get(v) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value));
}

export function filterWorkloads(
  ws: WorkloadReport[],
  f: TriageFilters,
): WorkloadReport[] {
  const q = f.search.trim().toLowerCase();
  return ws.filter((w) => {
    if (!f.showDismissed && isDismissed(w)) return false;
    if (f.verdicts.size > 0 && !f.verdicts.has(w.verdict)) return false;
    if (f.namespaces.size > 0 && !f.namespaces.has(namespaceOf(w)))
      return false;
    if (f.releases.size > 0 && !f.releases.has(releaseOf(w))) return false;
    if (f.teams.size > 0 && !f.teams.has(teamOf(w))) return false;
    if (f.kinds.size > 0 && !f.kinds.has(w.kind)) return false;
    if (f.minSavings > 0 && w.monthlySavings < f.minSavings) return false;
    if (!q) return true;
    return [
      w.name,
      w.namespace,
      w.vclusterNamespace ?? '',
      w.kind,
      ...Object.values(w.labels ?? {}),
      ...w.containers.map((c) => c.container),
    ].some((s) => s.toLowerCase().includes(q));
  });
}

/** Relative change from one value to another, e.g. -0.78 for 1 → 0.22. */
export const pctChange = (from: number, to: number) =>
  from > 0 ? to / from - 1 : 0;

export function sortWorkloads(
  ws: WorkloadReport[],
  by: SortKey,
): WorkloadReport[] {
  const out = [...ws];
  const change = (w: WorkloadReport, r: 'cpu' | 'memory') => {
    const p = primaryContainer(w);
    return p ? pctChange(p[r].request, p[r].recommended) : 0;
  };
  switch (by) {
    case 'savings':
      return out.sort((a, b) => b.monthlySavings - a.monthlySavings);
    case 'name':
      return out.sort((a, b) => a.name.localeCompare(b.name));
    case 'cpu':
      return out.sort((a, b) => change(a, 'cpu') - change(b, 'cpu'));
    case 'memory':
      return out.sort((a, b) => change(a, 'memory') - change(b, 'memory'));
    default:
      return out.sort((a, b) => b.riskScore - a.riskScore);
  }
}

export interface WorkloadGroup {
  key: string;
  workloads: WorkloadReport[];
  savings: number;
  atRisk: number;
}

/** Groups (already sorted) workloads, keeping their order inside each group;
 * groups follow their first member, so the sort decides group order too. */
export function groupWorkloads(
  ws: WorkloadReport[],
  by: GroupKey,
): WorkloadGroup[] {
  const keyOf = (w: WorkloadReport) =>
    by === 'namespace'
      ? namespaceOf(w)
      : by === 'release'
        ? releaseOf(w) || 'No release label'
        : '';
  const map = new Map<string, WorkloadGroup>();
  for (const w of ws) {
    const key = keyOf(w);
    let g = map.get(key);
    if (!g) {
      g = { key, workloads: [], savings: 0, atRisk: 0 };
      map.set(key, g);
    }
    g.workloads.push(w);
    if (w.monthlySavings > 0) g.savings += w.monthlySavings;
    if (w.verdict === 'under-provisioned') g.atRisk++;
  }
  return [...map.values()];
}

/** Kept for callers grouping by namespace in risk order. */
export function groupByNamespace(ws: WorkloadReport[]) {
  return groupWorkloads(sortWorkloads(ws, 'risk'), 'namespace').map((g) => ({
    namespace: g.key,
    workloads: g.workloads,
    savings: g.savings,
    atRisk: g.atRisk,
  }));
}

const TAG_RANK = { critical: 0, warning: 1, info: 2, good: 3 } as const;
/** Tags that explain nothing on a row by themselves. */
const QUIET_TAGS = new Set([
  'memory-cache-ambiguous',
  'injected-sidecar',
  'startup-spike',
  'regime-change',
]);

export interface ReasonTag {
  title: string;
  severity: Finding['severity'];
  message: string;
}

/** The short reasons a row shows: the deciding container's findings first,
 * then urgent ones from other containers, most severe first. */
export function reasonTags(
  w: WorkloadReport,
  max = 3,
): { tags: ReasonTag[]; more: number } {
  const p = primaryContainer(w);
  const fromPrimary = p?.findings ?? [];
  const urgentElsewhere = w.containers
    .filter((c) => c !== p)
    .flatMap((c) =>
      c.findings
        .filter((f) => f.severity === 'critical' || f.severity === 'warning')
        .map((f) => ({ ...f, title: `${c.container}: ${f.title}` })),
    );
  const seen = new Set<string>();
  // A tag that only repeats the verdict chip says nothing new.
  const restatesVerdict = (code: string) =>
    (code === 'no-requests' && w.verdict === 'no-requests') ||
    (code === 'right-sized' && w.verdict === 'right-sized');
  const all = [...fromPrimary, ...urgentElsewhere]
    .filter((f) => !QUIET_TAGS.has(f.code) && !restatesVerdict(f.code))
    .sort((a, b) => TAG_RANK[a.severity] - TAG_RANK[b.severity])
    .filter((f) => (seen.has(f.title) ? false : (seen.add(f.title), true)))
    .map((f) => ({
      title: f.title || f.code,
      severity: f.severity,
      message: f.message,
    }));
  return { tags: all.slice(0, max), more: Math.max(0, all.length - max) };
}

/** The container a row speaks for: one that set the workload's verdict,
 * preferring the app over injected sidecars, then the largest request. */
export function primaryContainer(
  w: WorkloadReport,
): ContainerReport | undefined {
  const deciding = w.containers.filter((c) => c.verdict === w.verdict);
  const pool = deciding.length ? deciding : w.containers;
  return [...pool].sort((a, b) => {
    const sa = isInjectedSidecar(a.container) ? 1 : 0;
    const sb = isInjectedSidecar(b.container) ? 1 : 0;
    if (sa !== sb) return sa - sb;
    return (
      b.cpu.request +
      b.memory.request / GI -
      (a.cpu.request + a.memory.request / GI)
    );
  })[0];
}

/** The one line a row says about why: the most severe finding of a
 * container that set the workload's verdict. */
export function headline(w: WorkloadReport): string {
  const deciding = w.containers.filter((c) => c.verdict === w.verdict);
  const pool = (deciding.length ? deciding : w.containers).flatMap((c) =>
    c.findings
      .filter((f) => f.code !== 'injected-sidecar')
      .map((f) => ({
        f,
        multi: w.containers.length > 1,
        container: c.container,
      })),
  );
  const rank = { critical: 0, warning: 1, info: 2, good: 3 } as const;
  const top = [...pool].sort(
    (a, b) => rank[a.f.severity] - rank[b.f.severity],
  )[0];
  if (!top) return '';
  return top.multi ? `${top.container}: ${top.f.message}` : top.f.message;
}

// ---- Export -----------------------------------------------------------------

const csvCell = (v: string | number) => {
  const s = String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};

export function toCSV(ws: WorkloadReport[]): string {
  const header = [
    'namespace',
    'vcluster_namespace',
    'kind',
    'name',
    'container',
    'verdict',
    'confidence',
    'cpu_request',
    'cpu_recommended',
    'memory_request_mi',
    'memory_recommended_mi',
    'monthly_savings_usd',
    'oom_kills',
    'days_of_data',
  ];
  const rows = [header.join(',')];
  for (const w of ws) {
    for (const c of w.containers) {
      rows.push(
        [
          w.namespace,
          w.vclusterNamespace ?? '',
          w.kind,
          w.name,
          c.container,
          c.verdict,
          c.confidence,
          quantity('cpu', c.cpu.request),
          quantity('cpu', c.cpu.recommended),
          Math.round(c.memory.request / MI),
          Math.round(c.memory.recommended / MI),
          c.monthlySavings,
          c.oomKills,
          c.data.days.toFixed(1),
        ]
          .map((v) =>
            csvCell(typeof v === 'number' ? Math.round(v * 100) / 100 : v),
          )
          .join(','),
      );
    }
  }
  return rows.join('\n') + '\n';
}

export function toMarkdown(ws: WorkloadReport[], cluster: string): string {
  const lines = [
    `## Rightsizing: ${cluster}`,
    '',
    '| Workload | Container | Verdict | CPU | Memory | $/mo |',
    '| --- | --- | --- | --- | --- | ---: |',
  ];
  for (const w of ws) {
    for (const c of w.containers) {
      const delta = c.monthlySavings;
      lines.push(
        `| ${w.namespace}/${w.name} | ${c.container} | ${VERDICT_META[c.verdict].label} | ${formatCores(c.cpu.request)} → ${formatCores(c.cpu.recommended)} | ${formatMem(c.memory.request)} → ${formatMem(c.memory.recommended)} | ${delta ? formatMoney(delta) : ''} |`,
      );
    }
  }
  return lines.join('\n') + '\n';
}

// ---- FinOps ------------------------------------------------------------------

const workloadKey = (
  namespace: string,
  vns: string | undefined,
  kind: string,
  name: string,
) => `${namespace}|${vns ?? ''}|${kind}|${name}`;

/** Lookups FinOps uses to show engine savings in its cost tables. */
export function rightsizingSavingsIndex(ws: WorkloadReport[]) {
  const byWorkload = new Map<string, number>();
  const byNamespace = new Map<string, number>();
  for (const w of ws) {
    // Same set as the report's savings total: every workload whose
    // recommendation costs less than today.
    if (w.monthlySavings <= 0 || isDismissed(w)) continue;
    byWorkload.set(
      workloadKey(w.namespace, w.vclusterNamespace, w.kind, w.name),
      w.monthlySavings,
    );
    byNamespace.set(
      w.namespace,
      (byNamespace.get(w.namespace) ?? 0) + w.monthlySavings,
    );
  }
  return {
    workload: (wl: {
      namespace: string;
      vclusterNamespace?: string;
      kind: string;
      name: string;
    }) =>
      byWorkload.get(
        workloadKey(wl.namespace, wl.vclusterNamespace, wl.kind, wl.name),
      ),
    namespace: (ns: { namespace: string }) => byNamespace.get(ns.namespace),
  };
}

const MIN_LISTED_SAVINGS = 5;

/** The engine's findings as FinOps savings rows: what can shrink, priced. */
export function rightsizingRecommendations(
  ws: WorkloadReport[],
): import('../../types/finops').CostRecommendation[] {
  return ws
    .filter(
      (w) =>
        w.priced && w.monthlySavings >= MIN_LISTED_SAVINGS && !isDismissed(w),
    )
    .sort((a, b) => b.monthlySavings - a.monthlySavings)
    .map((w) => {
      const t = w.containers.reduce(
        (acc, c) => ({
          cpu: acc.cpu + c.cpu.request,
          cpuRec: acc.cpuRec + c.cpu.recommended,
          mem: acc.mem + c.memory.request,
          memRec: acc.memRec + c.memory.recommended,
        }),
        { cpu: 0, cpuRec: 0, mem: 0, memRec: 0 },
      );
      return {
        type: 'rightsize' as const,
        resource: w.name,
        kind: w.kind,
        namespace: w.namespace,
        vclusterNamespace: w.vclusterNamespace,
        currentCost: w.monthlyCost,
        projectedSavings: w.monthlySavings,
        recommendation: `Per pod: CPU ${formatCores(t.cpu)} → ${formatCores(t.cpuRec)}, memory ${formatMem(t.mem)} → ${formatMem(t.memRec)}. ${w.confidence[0].toUpperCase() + w.confidence.slice(1)} confidence, from usage history.`,
        priority:
          w.monthlySavings >= 200
            ? 'high'
            : w.monthlySavings >= 50
              ? 'medium'
              : 'low',
        rightsizing: w,
      };
    });
}

// ---- Bulk -------------------------------------------------------------------

/** The patch as an object, the same content as patchYAML. */
export function patchObject(
  kind: string,
  choices: ContainerChoice[],
): Record<string, unknown> {
  const template: Record<string, unknown> = {};
  const annotations = sidecarAnnotations(choices);
  if (annotations.length > 0)
    template.metadata = { annotations: Object.fromEntries(annotations) };
  const containers = choices
    .filter(
      (c) => !isInjectedSidecar(c.container) && (c.cpu > 0 || c.memory > 0),
    )
    .map((c) => {
      const requests: Record<string, string> = {};
      const limits: Record<string, string> = {};
      if (c.cpu > 0) requests.cpu = quantity('cpu', c.cpu);
      if (c.memory > 0) requests.memory = quantity('memory', c.memory);
      if (c.cpuLimit > 0) limits.cpu = quantity('cpu', c.cpuLimit);
      if (c.memoryLimit > 0) limits.memory = quantity('memory', c.memoryLimit);
      return {
        name: c.container,
        resources: Object.keys(limits).length
          ? { requests, limits }
          : { requests },
      };
    });
  if (containers.length > 0) template.spec = { containers };
  return kind === 'CronJob'
    ? { spec: { jobTemplate: { spec: { template } } } }
    : { spec: { template } };
}

/** Whether a workload's recommendation changes anything worth patching. */
export const hasChange = (w: WorkloadReport) =>
  w.containers.some(
    (c) =>
      Math.abs(c.cpu.recommended - c.cpu.request) > 1e-9 ||
      Math.abs(c.memory.recommended - c.memory.request) > 0.5,
  );

/** One `kubectl patch` per workload, for every kind, as a runnable script. */
export function bulkKubectl(ws: WorkloadReport[]): string {
  const lines = [
    '#!/bin/sh',
    '# Rightsizing patches generated by Kanivet. Review before running.',
    'set -e',
    '',
  ];
  for (const w of ws.filter(hasChange)) {
    const ns = w.vclusterNamespace || w.namespace;
    const body = JSON.stringify(
      patchObject(
        w.kind,
        w.containers.map((c) => choiceFromRec(c)),
      ),
    );
    lines.push(
      `# ${w.kind} ${ns}/${w.name}${w.vclusterNamespace ? ' (inside its vcluster)' : ''}`,
    );
    lines.push(
      `kubectl -n ${ns} patch ${w.kind.toLowerCase()}/${w.name} --type strategic -p '${body}'`,
    );
    for (const c of w.containers) {
      if (c.hpa)
        lines.push(
          `# and set HPA ${c.hpa.name} ${c.hpa.resource} target to ${c.hpa.suggestedTarget}%`,
        );
    }
    lines.push('');
  }
  return lines.join('\n');
}

/** The same patches as YAML documents, one per workload. */
export function bulkYAML(ws: WorkloadReport[]): string {
  return ws
    .filter(hasChange)
    .map((w) => {
      const ns = w.vclusterNamespace || w.namespace;
      return `# ${w.kind} ${ns}/${w.name}\n${patchYAML(
        w.kind,
        w.containers.map((c) => choiceFromRec(c)),
      )}`;
    })
    .join('\n---\n');
}

/** Minutes or hours of a day, the unit the duration curve speaks in. */
export function formatDayTime(hours: number): string {
  const m = Math.round(hours * 60);
  if (m <= 0) return 'never';
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  return rest ? `${h}h ${rest}m` : `${h}h`;
}

/** Hours a day CPU sits above a request, from the pooled distribution. */
export const hoursAbove = (dist: Distribution, request: number) =>
  24 * exceedance(dist.q, dist.cpu, request);

/**
 * A load-duration curve: every sample of the window sorted from busiest to
 * quietest and laid across a typical 24-hour day, so the x axis reads as
 * "hours a day" and a request is a horizontal line. Where the line crosses
 * the curve is how long a day the app needs more than it requests; the area
 * under the line and over the curve is capacity paid for and idle.
 */
/** Share of a request left idle on average: 1 - E[min(usage, request)] /
 * request, integrated over the quantile grid. */
export function idleShare(dist: Distribution, request: number): number {
  if (request <= 0) return 0;
  let used = 0;
  let prevQ: number | null = null;
  let prevV = 0;
  dist.q.forEach((q, i) => {
    const v = dist.cpu[i];
    if (v === null || v === undefined || !Number.isFinite(v)) return;
    const m = Math.min(Math.max(v, 0), request);
    if (prevQ !== null) used += ((m + prevV) / 2) * (q - prevQ);
    prevQ = q;
    prevV = m;
  });
  return Math.min(1, Math.max(0, 1 - used / request));
}

/** Google's startup CPU boost controller, the config's target. */
export const STARTUP_BOOST_INSTALL =
  'kubectl apply -f https://github.com/google/kube-startup-cpu-boost/releases/download/v0.21.1/manifests.yaml';

/**
 * A StartupCPUBoost (github.com/google/kube-startup-cpu-boost) that gives a
 * container `boost.request` of CPU while it starts and resizes it back in
 * place once the pod is Ready. Only the request is raised: runtimes size
 * their thread pools from the CPU limit at startup, so a boosted limit would
 * leave oversized pools behind.
 */
export function startupBoostYAML(
  w: { name: string; namespace: string; vclusterNamespace?: string },
  container: string,
  boost: StartupBoost,
  cpuLimit: number,
): string {
  const ns = w.vclusterNamespace || w.namespace;
  const [key, value] = Object.entries(boost.selector ?? {})[0] ?? [];
  const lines = [
    'apiVersion: autoscaling.x-k8s.io/v1alpha1',
    'kind: StartupCPUBoost',
    'metadata:',
    `  name: ${w.name}-${container}-startup`,
    `  namespace: ${ns}`,
    'selector:',
    '  matchExpressions:',
    key
      ? `    - key: ${key}\n      operator: In\n      values: ["${value}"]`
      : '    # Add a label that selects only this workload\'s pods, e.g.\n    # - key: app.kubernetes.io/name\n    #   operator: In\n    #   values: ["my-app"]',
    'spec:',
    '  resourcePolicy:',
    '    containerPolicies:',
    '      - matchContainers:',
    '          type: ExactName',
    `          value: ${container}`,
    '        fixedResources:',
    `          requests: "${quantity('cpu', boost.request)}"`,
  ];
  if (cpuLimit > 0)
    lines.push(`          limits: "${quantity('cpu', cpuLimit)}" # unchanged`);
  lines.push(
    '  durationPolicy:',
    '    podCondition:',
    '      type: Ready',
    '      status: "True"',
  );
  return lines.join('\n');
}
