import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
// RightsizingParts reaches the API client through the monitoring settings.
vi.mock('../../services/api', () => ({ default: {} }));
vi.mock('../../services/api/websocket', () => ({ wsManager: {} }));
vi.mock('../../services/cloudService', () => ({ default: {} }));
vi.mock('../../utils/logger', () => ({ default: {}, logger: {} }));
import type { ContainerReport, WorkloadReport } from '../../types/rightsizing';
import { RightsizingTable } from './RightsizingTable';
import { groupWorkloads, sortWorkloads } from './rightsizingView';

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
  verdict: 'over-provisioned' as const,
  timeAboveRequest: 0,
  p50: 0,
  p90: 0,
  p95: 0,
  p99: 0,
  peak: 0,
  ...over,
});

const container = (name: string): ContainerReport => ({
  container: name,
  verdict: 'over-provisioned',
  confidence: 'high',
  cpu: rec({ request: 1, recommended: 0.2 }),
  memory: rec({ request: 1024 * MI, recommended: 512 * MI }),
  data: { days: 14, coverage: 1, samples: 4032, first: '2026-09-17T00:00:00Z' },
  findings: [
    {
      code: 'cpu-over-provisioned',
      severity: 'info',
      title: 'CPU 80% unused',
      message: 'CPU sits mostly idle.',
    },
    {
      code: 'memory-over-provisioned',
      severity: 'info',
      title: 'Memory 50% unused',
      message: 'Memory sits mostly idle.',
    },
  ],
  avgReplicas: 2,
  oomKills: 0,
  restarts: 0,
  cpuMonthly: 30,
  memMonthly: 4,
  monthlySavings: 20,
});

/** A large cluster: 3,000 workloads over 300 namespaces, ten rows each. */
const workloads = Array.from(
  { length: 3000 },
  (_, i): WorkloadReport => ({
    namespace: `ns-${i % 300}`,
    kind: 'Deployment',
    name: `app-${i}`,
    replicas: 2,
    verdict: 'over-provisioned',
    confidence: 'high',
    containers: [container('app'), container('sidecar')],
    priced: true,
    monthlyCost: 40,
    monthlySavings: 20,
    savingsLow: 10,
    savingsHigh: 30,
    riskScore: i,
  }),
);

// Nothing may be logged: no key or prop warnings.
const render = () => {
  const error = vi.spyOn(console, 'error').mockImplementation(() => {});
  try {
    const html = renderToStaticMarkup(
      createElement(RightsizingTable, {
        groups: groupWorkloads(sortWorkloads(workloads, 'risk'), 'namespace'),
        groupBy: 'namespace',
        selected: new Set<string>(),
        onToggle: () => {},
        onToggleMany: () => {},
        onOpen: () => {},
        scrollRef: { current: null },
        emptyText: '',
      }),
    );
    expect(error).not.toHaveBeenCalled();
    return html;
  } finally {
    error.mockRestore();
  }
};

describe('triage table on a large cluster', () => {
  it('draws the rows in view, not all 3,000', () => {
    const rows = render().match(/role="row"/g) ?? [];
    expect(rows.length).toBeGreaterThan(1);
    expect(rows.length).toBeLessThan(40);
  });

  it('mounts no tooltips until a row is hovered', () => {
    const html = render();
    expect(html).toContain('CPU 80% unused');
    // Radix marks each tooltip trigger with its state.
    expect(html).not.toContain('data-state=');
  });
});
