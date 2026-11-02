import React from 'react';
import { parseCPUToMillicores, parseMemoryToBytes } from './detailViewFormatters';

// Node list columns read from well-known labels, falling back across the
// provisioners and providers that set them: Karpenter first, then managed
// node groups (EKS, AKS, GKE).
const firstLabel = (item: any, keys: string[]): string | undefined => {
  const labels = item?.labels;
  if (!labels) return undefined;
  for (const key of keys) {
    const v = labels[key];
    if (v) return String(v);
  }
  return undefined;
};

export const nodePool = (item: any): string | undefined =>
  firstLabel(item, [
    'karpenter.sh/nodepool',
    'karpenter.sh/provisioner-name',
    'eks.amazonaws.com/nodegroup',
    'kubernetes.azure.com/agentpool',
    'cloud.google.com/gke-nodepool',
  ]);

export const nodeInstanceType = (item: any): string | undefined =>
  firstLabel(item, ['node.kubernetes.io/instance-type', 'beta.kubernetes.io/instance-type']);

export const nodeZone = (item: any): string | undefined =>
  firstLabel(item, ['topology.kubernetes.io/zone', 'failure-domain.beta.kubernetes.io/zone']);

// Spot or on-demand, in Karpenter's words whichever label says it.
export const nodeCapacityType = (item: any): string | undefined => {
  const karpenter = firstLabel(item, ['karpenter.sh/capacity-type']);
  if (karpenter) return karpenter;
  const eks = firstLabel(item, ['eks.amazonaws.com/capacityType']);
  if (eks) return eks.toLowerCase().replace(/_/g, '-');
  const aks = firstLabel(item, ['kubernetes.azure.com/scalesetpriority']);
  if (aks) return aks === 'regular' ? 'on-demand' : aks;
  if (firstLabel(item, ['cloud.google.com/gke-spot', 'cloud.google.com/gke-preemptible']) === 'true') return 'spot';
  return undefined;
};

export const formatNodeCapacityType = (item: any): string | React.ReactElement => {
  const type = nodeCapacityType(item);
  if (!type) return '-';
  return <span className={`capacity-type capacity-${type}`}>{type}</span>;
};

// Load figures come from /cluster/nodes/load (see useNodeLoad), merged into
// each node row as `load`; capacity comes from the node's own allocatable.
export interface NodeLoad {
  cpuRequests: number;
  memoryRequests: number;
  pods: number;
  cpuUsage?: number;
  memoryUsage?: number;
  // Set on every node when the cluster's metrics-server could not be read.
  usageError?: string;
}

export interface NodeLoadReport {
  nodes: Record<string, NodeLoad>;
  // Why usage is missing, typically no metrics-server in the cluster.
  usageError?: string;
}

// A node the report does not name runs no pods.
const IDLE: NodeLoad = { cpuRequests: 0, memoryRequests: 0, pods: 0 };

const sameLoad = (a: NodeLoad, b: NodeLoad): boolean =>
  a.cpuRequests === b.cpuRequests &&
  a.memoryRequests === b.memoryRequests &&
  a.pods === b.pods &&
  a.cpuUsage === b.cpuUsage &&
  a.memoryUsage === b.memoryUsage &&
  a.usageError === b.usageError;

// Node rows with their load, by list item. A row is replaced only when its
// item or its figures change, so unchanged rows keep their sort keys and
// skip re-rendering.
const loadedRows = new WeakMap<object, any>();

export const withNodeLoad = (items: any[], report: NodeLoadReport | null): any[] => {
  if (!report) return items;
  return items.map((item) => {
    const reported = report.nodes[item.name] || IDLE;
    const load = report.usageError ? { ...reported, usageError: report.usageError } : reported;
    const prev = loadedRows.get(item);
    if (prev && sameLoad(prev.load, load)) return prev;
    const row = { ...item, load };
    loadedRows.set(item, row);
    return row;
  });
};

export type NodeLoadMetric = 'cpu' | 'memory' | 'cpuRequests' | 'memoryRequests' | 'pods';

const allocatable = (item: any, metric: NodeLoadMetric): number => {
  const a = item?.allocatable;
  if (!a) return 0;
  if (metric === 'cpu' || metric === 'cpuRequests') return parseCPUToMillicores(a.cpu);
  if (metric === 'memory' || metric === 'memoryRequests') return parseMemoryToBytes(a.memory);
  return parseInt(a.pods, 10) || 0;
};

const used = (load: NodeLoad, metric: NodeLoadMetric): number | undefined => {
  switch (metric) {
    case 'cpu':
      return load.cpuUsage;
    case 'memory':
      return load.memoryUsage;
    case 'cpuRequests':
      return load.cpuRequests;
    case 'memoryRequests':
      return load.memoryRequests;
    case 'pods':
      return load.pods;
  }
};

// The share of the node's allocatable in use, as a percentage, or undefined
// when the load or the node's allocatable is not known.
export const nodeLoadPercent = (item: any, metric: NodeLoadMetric): number | undefined => {
  const load: NodeLoad | undefined = item?.load;
  if (!load) return undefined;
  const value = used(load, metric);
  const total = allocatable(item, metric);
  if (value === undefined || total <= 0) return undefined;
  return (value / total) * 100;
};

const formatCores = (milli: number): string => String(Number((milli / 1000).toFixed(2)));

const formatGiB = (bytes: number): string => String(Number((bytes / 1024 ** 3).toFixed(1)));

const METRIC_LABELS: Record<NodeLoadMetric, string> = {
  cpu: 'CPU used',
  memory: 'Memory used',
  cpuRequests: 'CPU requested',
  memoryRequests: 'Memory requested',
  pods: 'Pods',
};

const meterTitle = (item: any, metric: NodeLoadMetric, value: number): string => {
  const total = allocatable(item, metric);
  const label = METRIC_LABELS[metric];
  if (metric === 'cpu' || metric === 'cpuRequests') {
    return `${label}: ${formatCores(value)} of ${formatCores(total)} cores allocatable`;
  }
  if (metric === 'memory' || metric === 'memoryRequests') {
    return `${label}: ${formatGiB(value)} of ${formatGiB(total)} GiB allocatable`;
  }
  return `${label}: ${value} of ${total} allocatable`;
};

// Usage is coloured by how close the node is to running out; requests are
// not, since a well-packed node is meant to be near fully requested.
const meterTone = (metric: NodeLoadMetric, percent: number): string => {
  if (metric !== 'cpu' && metric !== 'memory') return 'requested';
  if (percent >= 90) return 'danger';
  if (percent >= 75) return 'warning';
  return 'normal';
};

export const NodeLoadMeter = ({ item, metric }: { item: any; metric: NodeLoadMetric }): React.ReactElement => {
  const percent = nodeLoadPercent(item, metric);
  const load: NodeLoad | undefined = item?.load;
  if (percent === undefined || !load) {
    let title: string | undefined;
    if (load && (metric === 'cpu' || metric === 'memory')) {
      title = load.usageError
        ? `No usage: metrics-server returned "${load.usageError}"`
        : 'No usage from metrics-server for this node';
    }
    return (
      <span className="node-load-empty" title={title}>
        -
      </span>
    );
  }
  const value = used(load, metric)!;
  const tone = meterTone(metric, percent);
  const text = metric === 'pods' ? `${value}/${allocatable(item, metric)}` : `${Math.round(percent)}%`;
  return (
    <span className={`node-load-meter ${tone}`} title={meterTitle(item, metric, value)}>
      <span className="node-load-bar">
        <span className="node-load-fill" style={{ width: `${Math.min(percent, 100)}%` }} />
      </span>
      <span className="node-load-value">{text}</span>
    </span>
  );
};
