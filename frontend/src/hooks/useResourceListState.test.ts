import { describe, it, expect, vi } from 'vitest';
import { formatStatus } from '../utils/formatters';

vi.mock('../store', () => ({ useStore: () => ({}) }));

const { matchesSearch } = await import('./useResourceListState');

// The matcher as it was: every field lower-cased again on each call.
const matchesSearchBefore = (item: any, query: string): boolean => {
  if (item.name?.toLowerCase().includes(query)) return true;
  if (item.namespace?.toLowerCase().includes(query)) return true;
  if (item.kind?.toLowerCase().includes(query)) return true;
  if (item.message?.toLowerCase().includes(query)) return true;
  if (item.reason?.toLowerCase().includes(query)) return true;
  const nodeName = item.nodeName || item.spec?.nodeName;
  if (typeof nodeName === 'string' && nodeName.toLowerCase().includes(query))
    return true;
  if (formatStatus(item).toLowerCase().includes(query)) return true;
  for (const [key, value] of Object.entries(item.labels || {})) {
    const val = String(value);
    if (
      key.toLowerCase().includes(query) ||
      val.toLowerCase().includes(query) ||
      `${key}=${val}`.toLowerCase().includes(query)
    )
      return true;
  }
  for (const [key, value] of Object.entries(item.annotations || {})) {
    const val = String(value);
    if (
      key.toLowerCase().includes(query) ||
      val.toLowerCase().includes(query) ||
      `${key}=${val}`.toLowerCase().includes(query)
    )
      return true;
  }
  return false;
};

const items = [
  {
    name: 'api-7c9f',
    namespace: 'Team-A',
    kind: 'Pod',
    phase: 'Running',
    nodeName: 'ip-10-0-1-2.ec2.internal',
    labels: { app: 'API', tier: 'backend' },
  },
  {
    name: 'worker',
    namespace: 'jobs',
    kind: 'Pod',
    phase: 'Pending',
    reason: 'Unschedulable',
    message: 'No nodes available',
    spec: { nodeName: 'gpu-1' },
  },
  {
    name: 'db',
    namespace: 'data',
    kind: 'StatefulSet',
    conditions: [{ type: 'Ready', status: 'True' }],
    annotations: {
      'sidecar.istio.io/status': '{"initContainers":["istio-init"]}',
    },
  },
  {
    name: 'ingress',
    namespace: 'edge',
    kind: 'Service',
    labels: { 'app.kubernetes.io/name': 'nginx' },
  },
];
const queries = [
  'api',
  'team-a',
  'pod',
  'running',
  'ec2',
  'gpu',
  'unschedulable',
  'no nodes',
  'ready',
  'app=api',
  'tier',
  'backend',
  'istio-init',
  'sidecar.istio.io/status={',
  'name=nginx',
  'nginx',
  'kubernetes',
  'x',
  'db',
  'e',
  'running\nteam',
  'pending',
];

describe('matchesSearch', () => {
  it('matches exactly what the per-field matcher matched', () => {
    for (const item of items) {
      for (const query of queries) {
        expect(
          matchesSearch(item, query),
          `${item.name} / ${JSON.stringify(query)}`,
        ).toBe(matchesSearchBefore(item, query));
      }
    }
  });

  it('reads an item once for any number of searches', () => {
    let reads = 0;
    const item: any = { name: 'api', namespace: 'ns' };
    Object.defineProperty(item, 'labels', {
      get: () => {
        reads++;
        return { app: 'api' };
      },
    });
    for (const query of ['a', 'ap', 'api', 'app=', 'zzz'])
      matchesSearch(item, query);
    expect(reads).toBe(1);
  });
});
