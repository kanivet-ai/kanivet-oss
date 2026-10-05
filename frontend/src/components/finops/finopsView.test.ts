import { describe, expect, it } from 'vitest';
import type { NamespaceCost, NodeCost, WorkloadCost } from '../../types/finops';
import { DEFAULT_FINOPS_FILTERS } from './FinOpsFilters';
import { filterNamespaces, filterNodes, groupNodesByType, workloadResource } from './finopsView';

const workload = (name: string, over: Partial<WorkloadCost> = {}): WorkloadCost => ({
  kind: 'Deployment', name, namespace: 'shop', replicas: 1,
  cpuRequest: 100, memoryRequest: 1024, hourlyCost: 1, dailyCost: 24, monthlyCost: 720,
  cpuEfficiency: 0, memoryEfficiency: 0, overallEfficiency: 0,
  pods: [{ podName: `${name}-abc`, namespace: 'shop', nodeName: 'n1' } as any],
  ...over,
});

const ns = (namespace: string, workloads: WorkloadCost[], over: Partial<NamespaceCost> = {}): NamespaceCost => ({
  namespace, podCount: workloads.length, cpuRequest: 100, cpuLimit: 0, memoryRequest: 1024, memoryLimit: 0,
  hourlyCost: 1, dailyCost: 24, monthlyCost: 720, cpuEfficiency: 0, memoryEfficiency: 0, overallEfficiency: 0,
  topWorkloads: workloads, ...over,
});

const node = (nodeName: string, instanceType: string, over: Partial<NodeCost> = {}): NodeCost => ({
  nodeName, instanceType, region: 'eu-north-1', provider: 'AWS EKS', hourlyCost: 1, dailyCost: 24, monthlyCost: 720,
  cpuCapacity: 4000, memoryCapacity: 1, cpuAllocatable: 4000, memoryAllocatable: 100, cpuRequested: 2000,
  memoryRequested: 50, podCount: 3, isSpot: false, allocatedMonthlyCost: 360, ...over,
});

describe('filterNamespaces', () => {
  const data = [
    ns('shop', [workload('web'), workload('api')]),
    ns('beige', [workload('mimir', { vclusterNamespace: 'observability' })], { vcluster: 'beige-vcluster' }),
  ];

  it('keeps every workload when the namespace name matches', () => {
    const out = filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, search: 'sho' });
    expect(out).toHaveLength(1);
    expect(out[0].topWorkloads).toHaveLength(2);
  });

  it('narrows to matching workloads, pods and vcluster namespaces', () => {
    expect(filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, search: 'api' })[0].topWorkloads!.map((w) => w.name)).toEqual(['api']);
    expect(filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, search: 'web-abc' })[0].topWorkloads!.map((w) => w.name)).toEqual(['web']);
    expect(filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, search: 'observab' })[0].namespace).toBe('beige');
    expect(filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, search: 'beige-vcl' })[0].namespace).toBe('beige');
  });

  it('usage flags ignore workloads without usage data', () => {
    const withUsage = [ns('shop', [
      workload('idle', { hasUsage: true, overallEfficiency: 10 }),
      workload('busy', { hasUsage: true, overallEfficiency: 90 }),
      workload('unknown', { hasUsage: false }),
    ])];
    const out = filterNamespaces(withUsage, { ...DEFAULT_FINOPS_FILTERS, showOverprovisioned: true });
    expect(out[0].topWorkloads!.map((w) => w.name)).toEqual(['idle']);
  });

  it('drops namespaces with no workload left after flag filters', () => {
    const out = filterNamespaces(data, { ...DEFAULT_FINOPS_FILTERS, showNoRequests: true });
    expect(out).toHaveLength(0);
  });
});

describe('filterNodes and groupNodesByType', () => {
  const nodes = [
    node('a', 'm5.xlarge'),
    node('b', 'm5.xlarge', { isSpot: true, monthlyCost: 300 }),
    node('c', 'c5.large', { priceMissing: true, monthlyCost: 0, allocatedMonthlyCost: 0 }),
  ];

  it('filters on search and cost only', () => {
    expect(filterNodes(nodes, { ...DEFAULT_FINOPS_FILTERS, search: 'c5' }).map((n) => n.nodeName)).toEqual(['c']);
    expect(filterNodes(nodes, { ...DEFAULT_FINOPS_FILTERS, showNoRequests: true })).toHaveLength(3);
  });

  it('groups by instance type and flags unpriced groups', () => {
    const groups = groupNodesByType(nodes);
    expect(groups.map((g) => g.instanceType)).toEqual(['m5.xlarge', 'c5.large']);
    expect(groups[0].spotCount).toBe(1);
    expect(groups[0].totalCost).toBe(1020);
    expect(groups[0].avgCpuAlloc).toBe(50);
    expect(groups[1].priceMissing).toBe(true);
  });
});

describe('workloadResource', () => {
  it('maps workload kinds to their API resources', () => {
    expect(workloadResource('StatefulSet')).toMatchObject({ name: 'statefulsets', group: 'apps' });
    expect(workloadResource('CronJob')).toMatchObject({ name: 'cronjobs', group: 'batch' });
    expect(workloadResource('Pod')).toMatchObject({ name: 'pods', group: '' });
    expect(workloadResource('Service')).toBeNull();
  });

  it('opens an Argo Rollout, which the backend now names as the owner of its pods', () => {
    expect(workloadResource('Rollout')).toMatchObject({ name: 'rollouts', group: 'argoproj.io', version: 'v1alpha1' });
  });
});
