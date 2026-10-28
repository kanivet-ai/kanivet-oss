import { describe, expect, it } from 'vitest';
import {
  nodeCapacityType,
  nodeInstanceType,
  nodeLoadPercent,
  nodePool,
  nodeZone,
  withNodeLoad,
} from './nodeColumns';
import { formatStatus } from './formatters';
import { sortItems } from './columnSorting';

const node = (labels: Record<string, string>, extra: Record<string, any> = {}) => ({
  kind: 'Node',
  name: 'n1',
  labels,
  ...extra,
});

describe('node label columns', () => {
  it('prefer Karpenter labels', () => {
    const n = node({
      'karpenter.sh/nodepool': 'gpu',
      'eks.amazonaws.com/nodegroup': 'system',
      'karpenter.sh/capacity-type': 'spot',
      'node.kubernetes.io/instance-type': 'g5.xlarge',
      'topology.kubernetes.io/zone': 'eu-west-1a',
    });
    expect(nodePool(n)).toBe('gpu');
    expect(nodeCapacityType(n)).toBe('spot');
    expect(nodeInstanceType(n)).toBe('g5.xlarge');
    expect(nodeZone(n)).toBe('eu-west-1a');
  });

  it('fall back to managed node group labels', () => {
    expect(nodePool(node({ 'eks.amazonaws.com/nodegroup': 'system' }))).toBe('system');
    expect(nodePool(node({ 'kubernetes.azure.com/agentpool': 'sys' }))).toBe('sys');
    expect(nodeZone(node({ 'failure-domain.beta.kubernetes.io/zone': 'z1' }))).toBe('z1');
  });

  it('name capacity types the way Karpenter does', () => {
    expect(nodeCapacityType(node({ 'eks.amazonaws.com/capacityType': 'ON_DEMAND' }))).toBe('on-demand');
    expect(nodeCapacityType(node({ 'kubernetes.azure.com/scalesetpriority': 'regular' }))).toBe('on-demand');
    expect(nodeCapacityType(node({ 'kubernetes.azure.com/scalesetpriority': 'spot' }))).toBe('spot');
    expect(nodeCapacityType(node({ 'cloud.google.com/gke-spot': 'true' }))).toBe('spot');
    expect(nodeCapacityType(node({}))).toBeUndefined();
  });
});

describe('node status', () => {
  const ready = { type: 'Ready', status: 'True' };

  it('flags pressure, cordoning and removal behind Ready', () => {
    const n = node({}, {
      unschedulable: true,
      deletionTimestamp: '2026-10-28T10:00:00Z',
      conditions: [ready, { type: 'MemoryPressure', status: 'True' }, { type: 'DiskPressure', status: 'False' }],
    });
    expect(formatStatus(n)).toBe('Ready (Cordoned, MemoryPressure, Terminating)');
  });

  it('stays plain Ready for a healthy node', () => {
    expect(formatStatus(node({}, { conditions: [ready, { type: 'PIDPressure', status: 'False' }] }))).toBe('Ready');
  });
});

describe('node load', () => {
  const allocatable = { cpu: '3920m', memory: '15Gi', pods: '58' };
  const report = {
    nodes: {
      n1: { cpuRequests: 1960, memoryRequests: 3 * 1024 ** 3, pods: 29, cpuUsage: 392, memoryUsage: 12 * 1024 ** 3 },
    },
  };

  it('is a share of the node allocatable', () => {
    const [row] = withNodeLoad([node({}, { allocatable })], report);
    expect(nodeLoadPercent(row, 'cpuRequests')).toBe(50);
    expect(nodeLoadPercent(row, 'cpu')).toBe(10);
    expect(nodeLoadPercent(row, 'memory')).toBe(80);
    expect(nodeLoadPercent(row, 'pods')).toBe(50);
  });

  it('treats a node the report does not name as idle, without usage', () => {
    const [row] = withNodeLoad([node({}, { name: 'empty', allocatable })], report);
    expect(nodeLoadPercent(row, 'cpuRequests')).toBe(0);
    expect(nodeLoadPercent(row, 'cpu')).toBeUndefined();
  });

  it('carries why usage is missing to every node', () => {
    const [row] = withNodeLoad([node({}, { allocatable })], { nodes: {}, usageError: 'Unauthorized' });
    expect(row.load.usageError).toBe('Unauthorized');
    expect(nodeLoadPercent(row, 'memory')).toBeUndefined();
  });

  it('keeps a row while its item and figures are unchanged', () => {
    const items = [node({}, { allocatable })];
    const first = withNodeLoad(items, report)[0];
    const sameFigures = { nodes: { n1: { ...report.nodes.n1 } } };
    expect(withNodeLoad(items, sameFigures)[0]).toBe(first);
    const busier = { nodes: { n1: { ...report.nodes.n1, cpuUsage: 3000 } } };
    expect(withNodeLoad(items, busier)[0]).not.toBe(first);
  });

  it('sorts by load, with unknown figures last when descending', () => {
    const rows = withNodeLoad(
      [
        node({}, { name: 'low', allocatable }),
        node({}, { name: 'none' }),
        node({}, { name: 'n1', allocatable }),
      ],
      { nodes: { low: { cpuRequests: 100, memoryRequests: 0, pods: 1 }, n1: report.nodes.n1 } },
    );
    const sorted = sortItems(rows, { sortBy: 'cpu req', sortOrder: 'desc' }).map((r: any) => r.name);
    expect(sorted).toEqual(['n1', 'low', 'none']);
  });
});
