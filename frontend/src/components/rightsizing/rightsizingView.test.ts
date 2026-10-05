import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { EvidenceSheet } from './EvidenceSheet';

vi.mock('../../services/api', () => ({ default: {} }));
vi.mock('../../services/api/rightsizing', () => ({}));
vi.mock('./useRightsizingReport', () => ({
  useRightsizingProvider: () => 'auto',
}));
vi.mock('react-dom', () => ({ createPortal: (node: unknown) => node }));
vi.mock('./EvidenceCharts', () => ({
  ChartLegend: () => null,
  CPUChart: () => null,
  MemoryChart: () => null,
}));
vi.mock('./RightsizingParts', () => ({
  ChangeSummary: () => null,
  ConfidenceMeter: () => null,
  VerdictBadge: () => null,
}));
import type {
  ContainerReport,
  HPACoupling,
  WorkloadReport,
} from '../../types/rightsizing';
import {
  PROFILE_META,
  bulkKubectl,
  bulkRepositoryPrompt,
  bulkYAML,
  evaluateCandidate,
  eventsOf,
  hasChange,
  potentialSavings,
  rightsizingRecommendations,
  startupBoostYAML,
  formatDayTime,
  hoursAbove,
  idleShare,
  emptyFilters,
  facetValues,
  groupWorkloads,
  patchObject,
  reasonTags,
  releaseOf,
  sortWorkloads,
  choiceFromRec,
  exceedance,
  filterWorkloads,
  formatCores,
  formatMem,
  groupByNamespace,
  kubectlCommands,
  pairedHPATarget,
  patchYAML,
  repositoryPrompt,
  quantileAt,
  quantity,
  snapCPU,
  snapMem,
  toCSV,
} from './rightsizingView';

const MI = 1024 * 1024;

const rec = (over: Partial<ContainerReport['cpu']> = {}) => ({
  request: 0,
  limit: 0,
  recommended: 0,
  recommendedLimit: 0,
  limitAction: 'none' as const,
  low: 0,
  high: 0,
  estimate: 0,
  verdict: 'right-sized' as const,
  timeAboveRequest: 0,
  p50: 0,
  p90: 0,
  p95: 0,
  p99: 0,
  peak: 0,
  explain: [],
  ...over,
});

const container = (over: Partial<ContainerReport> = {}): ContainerReport => ({
  container: 'app',
  verdict: 'over-provisioned',
  confidence: 'high',
  cpu: rec({
    request: 1,
    recommended: 0.22,
    limitAction: 'keep',
    limit: 2,
    recommendedLimit: 2,
  }),
  memory: rec({
    request: 2048 * MI,
    recommended: 640 * MI,
    recommendedLimit: 640 * MI,
    limitAction: 'set',
    peak: 550 * MI,
  }),
  data: {
    days: 14,
    coverage: 0.99,
    samples: 4032,
    first: '2026-09-17T00:00:00Z',
  },
  findings: [],
  avgReplicas: 3,
  oomKills: 0,
  restarts: 0,
  cpuMonthly: 30,
  memMonthly: 4,
  monthlySavings: 28.9,
  ...over,
});

const workload = (over: Partial<WorkloadReport> = {}): WorkloadReport => ({
  namespace: 'shop',
  kind: 'Deployment',
  name: 'api',
  replicas: 3,
  verdict: 'over-provisioned',
  confidence: 'high',
  containers: [container()],
  priced: true,
  monthlyCost: 40,
  monthlySavings: 28,
  savingsLow: 25,
  savingsHigh: 30,
  riskScore: 1e6 + 28,
  ...over,
});

const hpaCoupling = (over: Partial<HPACoupling> = {}): HPACoupling => ({
  name: 'api-hpa',
  resource: 'cpu',
  targetUtilization: 50,
  suggestedTarget: 86,
  pairedRequest: 0.7,
  ...over,
});

/** An HPA on a Resource metric over two containers, as the backend reports
 * it: one target over both requests (50% × 1.2 / 0.7). */
const podHPAWorkload = () =>
  workload({
    containers: [
      container({
        cpu: rec({ request: 1, recommended: 0.5 }),
        hpa: hpaCoupling({ pairedRequest: 0.5, pod: true }),
      }),
      container({
        container: 'sidecar',
        verdict: 'right-sized',
        cpu: rec({ request: 0.2, recommended: 0.2 }),
        memory: rec({ request: 64 * MI, recommended: 64 * MI }),
      }),
    ],
    hpa: hpaCoupling({ pod: true }),
  });

describe('evidence sheet apply section', () => {
  const render = (c: ContainerReport, w = workload({ containers: [c] })) => {
    vi.stubGlobal('document', { body: {} });
    try {
      return renderToStaticMarkup(
        createElement(EvidenceSheet, {
          cluster: 'staging',
          workload: w,
          profile: 'balanced',
          window: '14d',
          onClose: () => {},
        }),
      );
    } finally {
      vi.unstubAllGlobals();
    }
  };

  it.each(['no-requests', 'insufficient-data'] as const)(
    'shows available resource settings for %s workloads without requests',
    (verdict) => {
      const html = render(
        container({
          verdict,
          cpu: rec({ recommended: 0.055 }),
          memory: rec({ recommended: 320 * MI }),
        }),
      );
      expect(html).toContain('Apply it yourself');
      expect(html).toContain('Copy AI prompt');
      expect(html).toContain('cpu: 55m');
      expect(html).toContain('memory: 320Mi');
    },
  );

  it('shows an empty state instead of zero-valued output without usage data', () => {
    const html = render(
      container({ verdict: 'insufficient-data', cpu: rec(), memory: rec() }),
    );
    expect(html).toContain('Apply it yourself');
    expect(html).toContain('No resource values are available yet.');
    expect(html).not.toContain('Copy AI prompt');
  });

  it("pairs the HPA target with the whole pod's requests", () => {
    const w = podHPAWorkload();
    // On the sidecar's tab too: its request is in the pod's total.
    for (const c of w.containers) {
      const html = render(c, { ...w, verdict: c.verdict });
      expect(html).toContain('averageUtilization: 86');
      expect(html).toMatch(/HPA target of(<!-- -->)? <strong>86(<!-- -->)?%/);
    }
  });
});

describe('formatting', () => {
  it('formats cores and memory the way people write them', () => {
    expect(formatCores(0.22)).toBe('220m');
    expect(formatCores(1.5)).toBe('1.5 cores');
    expect(formatCores(1)).toBe('1 core');
    expect(formatMem(640 * MI)).toBe('640Mi');
    expect(formatMem(1.25 * 1024 * MI)).toBe('1.25Gi');
  });

  it('writes Kubernetes quantities', () => {
    expect(quantity('cpu', 0.22)).toBe('220m');
    expect(quantity('cpu', 2)).toBe('2');
    expect(quantity('memory', 640 * MI)).toBe('640Mi');
    expect(quantity('memory', 2048 * MI)).toBe('2Gi');
  });

  it('snaps CPU like the backend rounds', () => {
    expect(snapCPU(0.0421)).toBeCloseTo(0.045);
    expect(snapCPU(0.2131)).toBeCloseTo(0.22);
    expect(snapCPU(1.01)).toBeCloseTo(1.05);
    // Small workloads get fine steps, or a slider over them has no positions.
    expect(snapCPU(0.0032)).toBeCloseTo(0.004);
    expect(snapMem(30 * 1024 * 1024)).toBe(32 * 1024 * 1024);
    expect(snapMem(100 * 1024 * 1024)).toBe(100 * 1024 * 1024);
  });
});

describe('quantile grid', () => {
  const q = [0, 0.5, 0.9, 0.95, 1];
  const v = [0.01, 0.1, 0.2, 0.3, 0.6];

  it('reads exceedance by interpolation', () => {
    expect(exceedance(q, v, 0.2)).toBeCloseTo(0.1);
    expect(exceedance(q, v, 0.25)).toBeCloseTo(0.075);
    expect(exceedance(q, v, 0.6)).toBe(0);
    expect(exceedance(q, v, 0)).toBe(1);
  });

  it('inverts to the value at a quantile', () => {
    expect(quantileAt(q, v, 0.95)).toBeCloseTo(0.3);
    expect(quantileAt(q, v, 0.925)).toBeCloseTo(0.25);
  });

  it('skips missing grid points', () => {
    expect(exceedance([0, 0.5, 1], [null, 0.1, 0.2], 0.15)).toBeCloseTo(0.25);
  });
});

describe('copy-ready output', () => {
  it('builds a patch that keeps the CPU limit and sets memory request = limit', () => {
    const yaml = patchYAML('Deployment', [choiceFromRec(container())]);
    expect(yaml).toContain(
      'spec:\n  template:\n    spec:\n      containers:\n        - name: app',
    );
    expect(yaml).toContain('cpu: 220m');
    expect(yaml).toContain('cpu: 2\n');
    expect(yaml.match(/memory: 640Mi/g)).toHaveLength(2);
  });

  it('targets the job template for CronJobs and has no kubectl line for them', () => {
    expect(patchYAML('CronJob', [choiceFromRec(container())])).toContain(
      'jobTemplate:',
    );
    expect(
      kubectlCommands(workload({ kind: 'CronJob' }), [
        choiceFromRec(container()),
      ]),
    ).toBeNull();
  });

  it('writes kubectl set resources, in the virtual namespace for vcluster workloads', () => {
    const cmd = kubectlCommands(workload({ vclusterNamespace: 'apps' }), [
      choiceFromRec(container()),
    ])!;
    expect(cmd).toContain(
      'kubectl -n apps set resources deployment/api -c app',
    );
    expect(cmd).toContain('--requests=cpu=220m,memory=640Mi');
    expect(cmd).toContain('--limits=cpu=2,memory=640Mi');
  });

  it('omits the CPU limit when there was none', () => {
    const c = container({
      cpu: rec({ request: 1, recommended: 0.2, limitAction: 'none' }),
    });
    expect(patchYAML('Deployment', [choiceFromRec(c)])).not.toMatch(
      /limits:\n\s+cpu/,
    );
  });
});

describe('repository AI prompt', () => {
  it('combines every selected workload and its container recommendations', () => {
    const workloads = [
      workload(),
      workload({
        name: 'nightly',
        kind: 'CronJob',
        vcluster: 'preview',
        vclusterNamespace: 'apps',
        labels: { 'app.kubernetes.io/instance': 'batch-release' },
        containers: [
          container({ container: 'scheduler' }),
          container({
            container: 'worker',
            cpu: rec({ recommended: 0.055 }),
            memory: rec({ recommended: 320 * MI }),
            hpa: {
              name: 'worker-hpa',
              resource: 'cpu',
              targetUtilization: 60,
              suggestedTarget: 80,
              pairedRequest: 0.055,
            },
            startupBoost: { request: 1, startupRate: 0.9, inPlace: true },
          }),
        ],
      }),
    ];
    const prompt = bulkRepositoryPrompt(workloads, 'staging');
    expect(prompt.match(/Cluster: staging/g)).toHaveLength(2);
    expect(prompt).toContain('Workload: Deployment api');
    expect(prompt).toContain('Workload: CronJob nightly');
    expect(prompt).toContain('Virtual cluster: preview (host namespace: shop)');
    expect(prompt).toContain('Helm release label: batch-release');
    expect(prompt).toContain('Container: scheduler');
    expect(prompt).toContain('Container: worker');
    expect(prompt).toContain('cpu: 220m');
    expect(prompt).toContain('cpu: 55m');
    expect(prompt).toContain('memory: 640Mi');
    expect(prompt).toContain('memory: 320Mi');
    expect(prompt).toContain('HorizontalPodAutoscaler worker-hpa');
    expect(prompt).toContain('kind: StartupCPUBoost');
  });

  it('omits unavailable values and keeps recommendations without request changes', () => {
    const unknown = container({
      container: 'unknown',
      cpu: rec(),
      memory: rec(),
    });
    const unchanged = container({
      cpu: rec({ request: 0.055, recommended: 0.055 }),
      memory: rec({ request: 320 * MI, recommended: 320 * MI }),
    });
    const prompt = bulkRepositoryPrompt(
      [
        workload({ containers: [unchanged, unknown] }),
        workload({ name: 'no-data', containers: [unknown] }),
      ],
      'staging',
    );
    expect(prompt).toContain('Workload: Deployment api');
    expect(prompt).toContain('cpu: 55m');
    expect(prompt).not.toContain('unknown');
    expect(prompt).not.toContain('no-data');
    expect(bulkRepositoryPrompt([], 'staging')).toBe('');
    expect(
      bulkRepositoryPrompt([workload({ containers: [unknown] })], 'staging'),
    ).toBe('');
  });

  it('includes the selected values and identifies a CronJob inside a vcluster', () => {
    const w = workload({
      kind: 'CronJob',
      vcluster: 'preview',
      vclusterNamespace: 'apps',
      labels: { 'app.kubernetes.io/instance': 'shop-release' },
    });
    const prompt = repositoryPrompt(
      w,
      [choiceFromRec(w.containers[0], 0.055, 320 * MI)],
      'staging',
    );
    expect(prompt).toContain('Cluster: staging');
    expect(prompt).toContain('Workload: CronJob api');
    expect(prompt).toContain('Namespace: apps');
    expect(prompt).toContain('Virtual cluster: preview (host namespace: shop)');
    expect(prompt).toContain('Helm release label: shop-release');
    expect(prompt).toContain('Recommended resources:\n\n```yaml\nresources:');
    expect(prompt).not.toContain('jobTemplate:');
    expect(prompt).toContain('cpu: 55m');
    expect(prompt.match(/memory: 320Mi/g)).toHaveLength(2);
    expect(prompt).not.toContain('cpu: 220m');
    expect(prompt).not.toContain('strategic-merge patch');
    expect(prompt).not.toContain('Run the relevant repository validation');
  });

  it('includes coupled changes for all containers, including startup prerequisites', () => {
    const w = workload({
      containers: [
        container(),
        container({
          container: 'worker',
          hpa: {
            name: 'worker-hpa',
            resource: 'cpu',
            targetUtilization: 60,
            suggestedTarget: 80,
            pairedRequest: 0.22,
          },
          startupBoost: { request: 1, startupRate: 0.9, inPlace: true },
        }),
      ],
    });
    const prompt = repositoryPrompt(
      w,
      w.containers.map((c) => choiceFromRec(c)),
      'staging',
    );
    expect(prompt).toContain('Container: app');
    expect(prompt).toContain('Container: worker');
    expect(prompt).toContain('HorizontalPodAutoscaler worker-hpa');
    expect(prompt).toContain('80% paired with a request of 220m');
    expect(prompt).toContain('Verify the target against the selected request');
    expect(prompt).toContain('kind: StartupCPUBoost');
    expect(prompt).toContain('keep the startup request');
    const raised = repositoryPrompt(
      w,
      w.containers.map((c) => choiceFromRec(c, 1)),
      'staging',
    );
    expect(raised).not.toContain('kind: StartupCPUBoost');
  });
});

describe('patch with values that are not known', () => {
  it('omits unknown resources from kubectl commands', () => {
    const cmd = kubectlCommands(workload(), [
      { container: 'app', cpu: 0.2, memory: 0, cpuLimit: 0, memoryLimit: 0 },
      { container: 'unknown', cpu: 0, memory: 0, cpuLimit: 0, memoryLimit: 0 },
    ]);
    expect(cmd).toBe(
      'kubectl -n shop set resources deployment/api -c app \\\n  --requests=cpu=200m',
    );
  });

  it('omits unknown resources from injected sidecar annotations', () => {
    const cmd = kubectlCommands(workload(), [
      {
        container: 'istio-proxy',
        cpu: 0,
        memory: 320 * MI,
        cpuLimit: 0,
        memoryLimit: 320 * MI,
      },
    ]);
    expect(cmd).toContain('sidecar.istio.io/proxyMemory');
    expect(cmd).not.toContain('proxyCPU');
  });

  it('leaves out a resource with no request and no data instead of writing 0', () => {
    const yaml = patchYAML('Deployment', [
      { container: 'app', cpu: 0.2, memory: 0, cpuLimit: 0, memoryLimit: 0 },
    ]);
    expect(yaml).toContain('cpu: 200m');
    expect(yaml).not.toMatch(/memory|limits/);
  });
});

describe('triage', () => {
  it('filters by verdict, search and dismissal', () => {
    const ws = [
      workload(),
      workload({
        name: 'worker',
        verdict: 'under-provisioned',
        riskScore: 3e6,
      }),
      workload({
        name: 'cron',
        dismissed: [{ reason: 'batch', createdAt: '' }],
      }),
    ];
    const base = {
      ...emptyFilters(),
    };
    expect(filterWorkloads(ws, base).map((w) => w.name)).toEqual([
      'api',
      'worker',
    ]);
    expect(
      filterWorkloads(ws, {
        ...base,
        verdicts: new Set(['under-provisioned']),
      }).map((w) => w.name),
    ).toEqual(['worker']);
    expect(
      filterWorkloads(ws, { ...base, search: 'WORK' }).map((w) => w.name),
    ).toEqual(['worker']);
    expect(filterWorkloads(ws, { ...base, showDismissed: true })).toHaveLength(
      3,
    );
  });

  it('orders namespace groups by their most urgent workload', () => {
    const groups = groupByNamespace([
      workload({ namespace: 'a', riskScore: 1e6 }),
      workload({
        namespace: 'b',
        riskScore: 4e6,
        verdict: 'under-provisioned',
      }),
    ]);
    expect(groups.map((g) => g.namespace)).toEqual(['b', 'a']);
    expect(groups[0].atRisk).toBe(1);
  });

  it('exports one CSV row per container with savings', () => {
    const csv = toCSV([workload()]);
    const [header, row] = csv.trim().split('\n');
    expect(header.split(',')).toContain('monthly_savings_usd');
    // (1 - 0.22) × 30 + (2048 - 640)/1024 × 4 = 23.4 + 5.5
    expect(row).toContain(',28.9,');
  });
});

describe('finding your workloads', () => {
  const ws = [
    workload({
      name: 'api',
      labels: { 'app.kubernetes.io/instance': 'shop', team: 'payments' },
      monthlySavings: 28,
    }),
    workload({
      name: 'worker',
      labels: { 'app.kubernetes.io/instance': 'shop' },
      monthlySavings: 3,
      riskScore: 9e6,
      verdict: 'under-provisioned',
    }),
    workload({
      name: 'ingest',
      namespace: 'data',
      kind: 'StatefulSet',
      monthlySavings: 120,
    }),
  ];

  it('counts facet values, most common first', () => {
    expect(facetValues(ws, releaseOf)).toEqual([{ value: 'shop', count: 2 }]);
    expect(facetValues(ws, (w) => w.namespace)).toEqual([
      { value: 'shop', count: 2 },
      { value: 'data', count: 1 },
    ]);
  });

  it('filters by release, team, kind and minimum savings, and searches labels', () => {
    const f = emptyFilters();
    expect(
      filterWorkloads(ws, { ...f, releases: new Set(['shop']) }).map(
        (w) => w.name,
      ),
    ).toEqual(['api', 'worker']);
    expect(
      filterWorkloads(ws, { ...f, teams: new Set(['payments']) }).map(
        (w) => w.name,
      ),
    ).toEqual(['api']);
    expect(
      filterWorkloads(ws, { ...f, kinds: new Set(['StatefulSet']) }).map(
        (w) => w.name,
      ),
    ).toEqual(['ingest']);
    expect(
      filterWorkloads(ws, { ...f, minSavings: 20 }).map((w) => w.name),
    ).toEqual(['api', 'ingest']);
    expect(
      filterWorkloads(ws, { ...f, search: 'payments' }).map((w) => w.name),
    ).toEqual(['api']);
  });

  it('sorts by risk, savings or name and groups in that order', () => {
    expect(sortWorkloads(ws, 'risk')[0].name).toBe('worker');
    expect(sortWorkloads(ws, 'savings').map((w) => w.name)).toEqual([
      'ingest',
      'api',
      'worker',
    ]);
    expect(sortWorkloads(ws, 'name').map((w) => w.name)).toEqual([
      'api',
      'ingest',
      'worker',
    ]);
    const groups = groupWorkloads(sortWorkloads(ws, 'savings'), 'release');
    expect(groups.map((g) => g.key)).toEqual(['No release label', 'shop']);
    expect(groups[1].savings).toBe(31);
  });
});

describe('row reasons', () => {
  it('shows short titles, most severe first, and hides quiet ones', () => {
    const c = container({
      verdict: 'under-provisioned',
      findings: [
        {
          code: 'memory-cache-ambiguous',
          severity: 'info',
          title: 'Cache-heavy',
          message: 'x',
        },
        {
          code: 'cpu-over-provisioned',
          severity: 'info',
          title: 'CPU 96% unused',
          message: 'y',
        },
        {
          code: 'oom-killed',
          severity: 'critical',
          title: 'OOM ×5',
          message: 'z',
        },
      ],
    });
    const { tags } = reasonTags(
      workload({ verdict: 'under-provisioned', containers: [c] }),
    );
    expect(tags.map((t) => t.title)).toEqual(['OOM ×5', 'CPU 96% unused']);
  });
});

describe('bulk patches', () => {
  it('writes one strategic patch per changed workload, CronJobs through the job template', () => {
    const script = bulkKubectl([
      workload(),
      workload({ name: 'nightly', kind: 'CronJob' }),
    ]);
    expect(script).toContain(
      "kubectl -n shop patch deployment/api --type strategic -p '",
    );
    expect(script).toContain('kubectl -n shop patch cronjob/nightly');
    const cron = patchObject('CronJob', [choiceFromRec(container())]) as any;
    expect(
      cron.spec.jobTemplate.spec.template.spec.containers[0].resources.requests,
    ).toEqual({ cpu: '220m', memory: '640Mi' });
  });

  it('leaves custom resources such as Rollouts out of the script instead of stopping it there', () => {
    const script = bulkKubectl([
      workload({ name: 'a' }),
      workload({ name: 'web', kind: 'Rollout' }),
      workload({ name: 'b' }),
    ]);
    // A strategic patch on a custom resource fails (415), and `set -e`
    // would then leave every workload after it unpatched.
    expect(script).not.toContain('patch rollout/web');
    expect(script).toContain('# Rollout shop/web');
    expect(script).toContain('kubectl -n shop patch deployment/a');
    expect(script).toContain('kubectl -n shop patch deployment/b');
  });

  it('turns mesh sidecars into pod template annotations', () => {
    const sidecar = container({ container: 'istio-proxy' });
    const obj = patchObject('Deployment', [choiceFromRec(sidecar)]) as any;
    expect(
      obj.spec.template.metadata.annotations['sidecar.istio.io/proxyCPU'],
    ).toBe('220m');
    expect(obj.spec.template.spec).toBeUndefined();
  });

  it('skips workloads with nothing to change', () => {
    // As the engine reports a container it leaves alone: memory kept.
    const same = container({
      cpu: { ...container().cpu, recommended: 1 },
      memory: {
        ...container().memory,
        recommended: 2048 * MI,
        recommendedLimit: 0,
        limitAction: 'keep',
      },
    });
    expect(bulkKubectl([workload({ containers: [same] })])).not.toContain(
      'kubectl -n',
    );
  });
});

describe('duration curve helpers', () => {
  // Uniform usage from 0 to 100m across the day.
  const q = Array.from({ length: 101 }, (_, i) => i / 100);
  const dist = {
    q,
    cpu: q.map((x) => x * 0.1),
    mem: [],
    dailyMemPeaks: [],
    dailyCpuP95: [],
  };

  it('reads hours a day above a request', () => {
    expect(hoursAbove(dist, 0.09)).toBeCloseTo(2.4, 5);
    expect(hoursAbove(dist, 0.2)).toBe(0);
  });

  it('measures the idle share of a request', () => {
    // Mean of min(U, 100m) for U ~ uniform(0, 100m) is 50m: half idle.
    expect(idleShare(dist, 0.1)).toBeCloseTo(0.5, 2);
    expect(idleShare(dist, 0.2)).toBeCloseTo(0.75, 2);
  });

  it('formats time of day', () => {
    expect(formatDayTime(0)).toBe('never');
    expect(formatDayTime(0.25)).toBe('15 min');
    expect(formatDayTime(2)).toBe('2h');
    expect(formatDayTime(5.4)).toBe('5h 24m');
  });
});

describe('startupBoostYAML', () => {
  it('boosts the request only, until the pod is Ready', () => {
    const y = startupBoostYAML(
      {
        name: 'platform-auth',
        namespace: 'beige',
        vclusterNamespace: 'applications',
      },
      'keycloak',
      {
        request: 0.5,
        startupRate: 0.116,
        inPlace: true,
        selector: { 'app.kubernetes.io/instance': 'platform-auth' },
      },
      2,
    );
    expect(y).toContain('kind: StartupCPUBoost');
    expect(y).toContain('namespace: applications');
    expect(y).toContain('- key: app.kubernetes.io/instance');
    expect(y).toContain('value: keycloak');
    expect(y).toContain('requests: "500m"');
    expect(y).toContain('limits: "2" # unchanged');
    expect(y).toContain('type: Ready');
  });

  it('asks for a selector when the workload has no usable label', () => {
    const y = startupBoostYAML(
      { name: 'x', namespace: 'n' },
      'c',
      { request: 0.2, startupRate: 0.2, inPlace: true },
      0,
    );
    expect(y).toContain("# Add a label that selects only this workload's pods");
    expect(y).not.toContain('limits:');
  });
});

describe('memory limits the engine keeps', () => {
  const kept = (limit: number, over: Partial<ContainerReport> = {}) =>
    container({
      verdict: 'right-sized',
      cpu: rec({ request: 0.25, recommended: 0.25 }),
      memory: rec({
        request: 512 * MI,
        limit,
        recommended: 512 * MI,
        recommendedLimit: limit,
        limitAction: 'keep',
      }),
      ...over,
    });

  it('leaves a limit above the request where it is', () => {
    const choice = choiceFromRec(kept(1024 * MI));
    expect(choice.memoryLimit).toBe(1024 * MI);
    const yaml = patchYAML('Deployment', [choice]);
    expect(yaml).toMatch(/limits:\n\s+memory: 1Gi/);
    expect(yaml.match(/memory: 512Mi/g)).toHaveLength(1);
    expect(kubectlCommands(workload(), [choice])).toContain(
      '--limits=memory=1Gi',
    );
  });

  it('adds no limit where there was none', () => {
    const choice = choiceFromRec(kept(0));
    expect(choice.memoryLimit).toBe(0);
    expect(kubectlCommands(workload(), [choice])).not.toContain('--limits');
    expect(patchYAML('Deployment', [choice])).not.toContain('limits:');
  });

  it('still sets request = limit for a value picked on the slider', () => {
    const choice = choiceFromRec(kept(1024 * MI), 0.25, 768 * MI);
    expect(choice.memoryLimit).toBe(768 * MI);
  });

  it("keeps an injected mesh proxy's memory limit annotation", () => {
    const proxy = kept(1024 * MI, { container: 'istio-proxy' });
    proxy.memory.request = proxy.memory.recommended = 128 * MI;
    const obj = patchObject('Deployment', [choiceFromRec(proxy)]) as any;
    expect(
      obj.spec.template.metadata.annotations[
        'sidecar.istio.io/proxyMemoryLimit'
      ],
    ).toBe('1Gi');
  });

  it('leaves untouched containers out of bulk patches', () => {
    const proxy = kept(1024 * MI, { container: 'istio-proxy' });
    proxy.memory.request = proxy.memory.recommended = 128 * MI;
    const w = workload({
      containers: [
        container(),
        kept(256 * MI, { container: 'log-shipper' }),
        proxy,
      ],
    });
    for (const out of [bulkKubectl([w]), bulkYAML([w])]) {
      expect(out).toContain('app');
      expect(out).not.toContain('log-shipper');
      expect(out).not.toContain('sidecar.istio.io');
    }
  });
});

describe('changes worth patching', () => {
  // Throttled at its limit with a request that stays: only the limit moves.
  const throttled = () =>
    container({
      verdict: 'under-provisioned',
      cpu: rec({
        request: 1,
        limit: 1,
        recommended: 1,
        recommendedLimit: 1.5,
        limitAction: 'raise',
      }),
      memory: rec({
        request: 512 * MI,
        limit: 512 * MI,
        recommended: 512 * MI,
        recommendedLimit: 512 * MI,
        limitAction: 'keep',
      }),
    });

  it('counts a raised CPU limit with an unchanged request', () => {
    const w = workload({ containers: [throttled()] });
    expect(hasChange(w)).toBe(true);
    expect(bulkKubectl([w])).toContain('"limits":{"cpu":"1500m"');
    expect(bulkYAML([w])).toContain('cpu: 1500m');
  });

  it('adds no CPU limit to a container without data or a limit', () => {
    // The engine's answer without data: today's values, every limit kept.
    const quiet = container({
      verdict: 'insufficient-data',
      cpu: rec({ request: 0.5, recommended: 0.5, limitAction: 'keep' }),
      memory: rec({
        request: 256 * MI,
        recommended: 256 * MI,
        limitAction: 'keep',
      }),
    });
    expect(choiceFromRec(quiet)).toMatchObject({ cpuLimit: 0, memoryLimit: 0 });
    const w = workload({ containers: [quiet] });
    expect(hasChange(w)).toBe(false);
    expect(bulkKubectl([w])).not.toContain('kubectl -n');
  });

  it('gives each HPA one target, the lowest its containers suggest', () => {
    const hpa = (suggestedTarget: number) => ({
      name: 'api-hpa',
      resource: 'cpu' as const,
      targetUtilization: 60,
      suggestedTarget,
      pairedRequest: 0.22,
    });
    const script = bulkKubectl([
      workload({
        containers: [
          container({ hpa: hpa(80) }),
          container({ container: 'worker', hpa: hpa(72) }),
        ],
      }),
    ]);
    expect(script.match(/set HPA api-hpa/g)).toHaveLength(1);
    expect(script).toContain('set HPA api-hpa cpu target to 72%');
  });

  it("prefers the workload's one target, worked out over the pod", () => {
    const w = workload({
      containers: [
        container({ hpa: hpaCoupling({ suggestedTarget: 80 }) }),
        container({
          container: 'worker',
          hpa: hpaCoupling({ suggestedTarget: 72 }),
        }),
      ],
      hpa: hpaCoupling({ suggestedTarget: 75, pod: true }),
    });
    const script = bulkKubectl([w]);
    expect(script.match(/set HPA api-hpa/g)).toHaveLength(1);
    expect(script).toContain('set HPA api-hpa cpu target to 75%');
  });
});

describe('HPA targets over the requests they count', () => {
  it("pairs a pod-level target with every container's requests", () => {
    const w = podHPAWorkload();
    const recs = w.containers.map((c) => choiceFromRec(c));
    expect(pairedHPATarget(w, w.hpa!, recs)).toBe(86);
    // 600m picked for the app: 50% × 1.2 / 0.8.
    const picked = [choiceFromRec(w.containers[0], 0.6), recs[1]];
    expect(pairedHPATarget(w, w.hpa!, picked)).toBe(75);
    // A ContainerResource metric counts the container it names alone.
    const app = {
      ...w.containers[0],
      hpa: hpaCoupling({ pairedRequest: 0.5 }),
    };
    const one = { ...w, containers: [app, w.containers[1]], hpa: app.hpa };
    expect(pairedHPATarget(one, one.hpa, recs)).toBe(100);
    expect(pairedHPATarget(one, one.hpa, picked)).toBe(83);
  });

  it('describes a pod-level target once, against the total', () => {
    const w = podHPAWorkload();
    const prompt = repositoryPrompt(
      w,
      w.containers.map((c) => choiceFromRec(c)),
      'staging',
    );
    expect(prompt.match(/HorizontalPodAutoscaler api-hpa/g)).toHaveLength(1);
    expect(prompt).toContain('86% paired with requests totalling 700m per pod');
    expect(prompt).not.toContain('paired with a request of');
  });
});

describe('requests a VerticalPodAutoscaler sets', () => {
  const vpa = (...resources: ('cpu' | 'memory')[]) => ({
    name: 'api-vpa',
    mode: 'Auto',
    resources,
  });

  it('are left out of bulk patches, limits too', () => {
    const w = workload({ containers: [container({ vpa: vpa('memory') })] });
    expect(hasChange(w)).toBe(true);
    expect(bulkKubectl([w])).toContain(
      `-p '{"spec":{"template":{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"220m"},"limits":{"cpu":"2"}}}]}}}}'`,
    );
    const yaml = bulkYAML([w]);
    expect(yaml).toContain('cpu: 220m');
    expect(yaml).not.toContain('memory');
  });

  it('leave a workload out when nothing else changes, and out of the count', () => {
    const allHeld = workload({
      name: 'vpa-all',
      containers: [container({ vpa: vpa('cpu', 'memory') })],
    });
    // CPU and its limit stay; only memory, which the VPA sets, would move.
    const memHeld = workload({
      name: 'vpa-memory',
      containers: [
        container({
          cpu: rec({
            request: 1,
            recommended: 1,
            limit: 2,
            recommendedLimit: 2,
            limitAction: 'keep',
          }),
          vpa: vpa('memory'),
        }),
      ],
    });
    const mixed = workload({
      name: 'mixed',
      containers: [
        container({ container: 'held', vpa: vpa('cpu', 'memory') }),
        container(),
      ],
    });
    const ws = [allHeld, memHeld, mixed];
    expect(ws.filter(hasChange).map((w) => w.name)).toEqual(['mixed']);
    for (const out of [bulkKubectl(ws), bulkYAML(ws)]) {
      expect(out).not.toContain('vpa-');
      expect(out).not.toContain('held');
      expect(out).toContain('shop/mixed');
    }
  });

  it('keep the HPA target that counts them', () => {
    const hpa = hpaCoupling({ suggestedTarget: 80, pairedRequest: 0.22 });
    const w = workload({
      containers: [container({ hpa, vpa: vpa('cpu') })],
      hpa,
    });
    const script = bulkKubectl([w]);
    // Memory still changes; CPU, request and limit, is left to the VPA.
    expect(script).toContain(
      `-p '{"spec":{"template":{"spec":{"containers":[{"name":"app","resources":{"requests":{"memory":"640Mi"},"limits":{"memory":"640Mi"}}}]}}}}'`,
    );
    expect(script).not.toContain('set HPA');
  });

  it('still show the recommendation for one workload, naming the VPA', () => {
    const c = container({ vpa: vpa('memory') });
    expect(patchYAML('Deployment', [choiceFromRec(c)])).toContain(
      'memory: 640Mi',
    );
    const prompt = repositoryPrompt(
      workload({ containers: [c] }),
      [choiceFromRec(c)],
      'staging',
    );
    expect(prompt).toContain('memory: 640Mi');
    expect(prompt).toContain(
      'VerticalPodAutoscaler api-vpa (updateMode Auto) sets the memory requests of container app',
    );
  });
});

describe('FinOps potential savings', () => {
  it('counts the long tail the listed rows leave out', () => {
    const ws = Array.from({ length: 400 }, (_, i) =>
      workload({ name: `w${i}`, monthlySavings: 4.5 }),
    );
    const listed = [
      ...rightsizingRecommendations(ws),
      {
        type: 'underutilized-node' as const,
        resource: 'node-a',
        kind: 'Node',
        currentCost: 300,
        projectedSavings: 120,
        recommendation: '',
        priority: 'medium' as const,
      },
    ];
    expect(listed).toHaveLength(1);
    const summary = { monthlySavings: 1800, shrinking: 400 } as any;
    expect(potentialSavings(summary, listed)).toEqual({
      total: 1920,
      smaller: 400,
    });
    expect(potentialSavings(undefined, listed)).toEqual({
      total: 120,
      smaller: 0,
    });
  });
});

describe('the slider before the evidence arrives', () => {
  it("reads the candidate off the report's quantiles", () => {
    const c = container({
      cpu: rec({ p50: 0.1, p90: 0.2, p95: 0.25, p99: 0.4, peak: 0.6 }),
    });
    const at = (cpu: number) => evaluateCandidate(c, undefined, cpu, 0);
    expect(at(0.05).cpuTimeAbove).toBeCloseTo(0.75);
    expect(at(0.2).cpuTimeAbove).toBeCloseTo(0.1);
    expect(at(1).cpuTimeAbove).toBe(0);
    expect(at(0.2).cpuApproximate).toBe(true);
  });
});

describe('evidence events', () => {
  it('stay the same empty list when the backend sends null', () => {
    const ev = JSON.parse('{"events":{"app":null}}');
    expect(eventsOf(ev, 'app')).toEqual([]);
    expect(eventsOf(ev, 'app')).toBe(eventsOf(ev, 'app'));
    expect(eventsOf(null, 'app')).toBe(eventsOf(ev, 'app'));
  });
});

describe('profiles', () => {
  it("state the backend's memory headroom (types.go params)", () => {
    expect(PROFILE_META.conservative).toMatchObject({
      mem: '30%',
      memFloor: '64Mi',
    });
    expect(PROFILE_META.balanced).toMatchObject({
      mem: '15%',
      memFloor: '32Mi',
    });
    expect(PROFILE_META.aggressive).toMatchObject({
      mem: '10%',
      memFloor: '16Mi',
    });
  });
});
