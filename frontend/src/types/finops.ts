export interface PricingInfo {
  source: 'cur' | 'aws-api' | 'azure-api' | 'unknown';
  lastUpdated: string;
  instanceCount: number;
  isAvailable: boolean;
  error?: string;
  nodesWithPricing: number;
  nodesMissingPrice: number;
  /** False when the cloud has no price source (GKE, on-prem, kind). */
  supported: boolean;
}

export type CostScope = 'cluster' | 'vcluster';

export interface VClusterCost {
  host: string;
  namespace: string;
  name: string;
  hostMonthlyCost: number;
  sharePercent: number;
  controlPlane?: WorkloadCost[];
}

/**
 * Totals for one dashboard. In cluster scope the efficiency fields are
 * allocation (requests / allocatable). In vcluster scope they are zero: the
 * nodes are shared, so `vcluster` carries the share of the host instead.
 */
export interface ClusterCostSummary {
  cluster: string;
  scope: CostScope;
  provider: string;
  region: string;
  nodeCount: number;
  spotNodeCount: number;
  spotAtOnDemandCount: number;
  spotAtOnDemandCost: number;
  podCount: number;
  namespaceCount: number;
  totalCpu: number;
  totalMemory: number;
  requestedCpu: number;
  requestedMemory: number;
  usedCpu: number;
  usedMemory: number;
  usageAvailable: boolean;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  allocatedCost: number;
  idleCost: number;
  idlePercentage: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  breakdown: CostBreakdown;
  pricingInfo: PricingInfo;
  vcluster?: VClusterCost;
  lastUpdated: string;
}

export interface CostBreakdown {
  computeCost: number;
  cpuCost: number;
  memoryCost: number;
  controlPlaneCost: number;
}

/** In vcluster scope, requests, pods and allocated cost count only the vcluster's pods. */
export interface NodeCost {
  nodeName: string;
  instanceType: string;
  region: string;
  provider: string;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  priceMissing?: boolean;
  cpuCapacity: number;
  memoryCapacity: number;
  cpuAllocatable: number;
  memoryAllocatable: number;
  cpuRequested: number;
  memoryRequested: number;
  podCount: number;
  isSpot: boolean;
  spotAtOnDemand?: boolean;
  allocatedMonthlyCost: number;
}

/** Efficiency on pods, workloads and namespaces is usage / request, set only when hasUsage. */
export interface PodCost {
  podName: string;
  namespace: string;
  nodeName: string;
  ownerKind?: string;
  ownerName?: string;
  cpuRequest: number;
  cpuLimit: number;
  memoryRequest: number;
  memoryLimit: number;
  cpuUsed?: number;
  memoryUsed?: number;
  hasUsage?: boolean;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  status: string;
  createdAt: string;
  vclusterNamespace?: string;
}

export interface NamespaceCost {
  namespace: string;
  podCount: number;
  cpuRequest: number;
  cpuLimit: number;
  memoryRequest: number;
  memoryLimit: number;
  cpuUsed?: number;
  memoryUsed?: number;
  hasUsage?: boolean;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  topWorkloads?: WorkloadCost[];
  /** The vcluster running in this host namespace, if any. */
  vcluster?: string;
}

export interface HPAInfo {
  minReplicas: number;
  maxReplicas: number;
  currentReplicas: number;
  minMonthlyCost: number;
  maxMonthlyCost: number;
}

export interface WorkloadCost {
  kind: string;
  name: string;
  namespace: string;
  vclusterNamespace?: string;
  replicas: number;
  cpuRequest: number;
  cpuLimit?: number;
  memoryRequest: number;
  memoryLimit?: number;
  cpuUsed?: number;
  memoryUsed?: number;
  hasUsage?: boolean;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  pods?: PodCost[];
  hpa?: HPAInfo;
}

export type RecommendationType = 'rightsize' | 'no-requests' | 'underutilized-node';

export interface CostRecommendation {
  type: RecommendationType;
  resource: string;
  kind?: string;
  namespace?: string;
  /** Set on host-side recommendations for workloads inside a vcluster. */
  vclusterNamespace?: string;
  currentCost: number;
  projectedSavings: number;
  recommendation: string;
  priority: 'high' | 'medium' | 'low';
  /** Rightsizing rows come from the rightsizing engine and open its evidence. */
  rightsizing?: import('./rightsizing').WorkloadReport;
}

export interface FinOpsDashboardData {
  summary: ClusterCostSummary;
  nodes: NodeCost[];
  namespaces: NamespaceCost[];
  recommendations: CostRecommendation[];
}

export function formatCost(cost: number, decimals = 2): string {
  if (cost >= 1000) {
    return `$${cost.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`;
  }
  if (cost >= 1) {
    return `$${cost.toFixed(decimals)}`;
  }
  if (cost === 0) {
    return '$0';
  }
  if (cost >= 0.01) {
    return `$${cost.toFixed(3)}`;
  }
  return `$${cost.toFixed(4)}`;
}

export function getEfficiencyColor(efficiency: number): string {
  if (efficiency >= 85) return 'var(--efficiency-risk)';
  if (efficiency >= 70) return 'var(--efficiency-excellent)';
  if (efficiency >= 50) return 'var(--efficiency-good)';
  if (efficiency >= 30) return 'var(--efficiency-fair)';
  return 'var(--efficiency-critical)';
}

export interface EfficiencyBadgeInfo {
  label: string;
  color: string;
  class: string;
  description: string;
  isSpecialCase?: boolean;
}

/**
 * Allocation: how much of the cluster's allocatable capacity pods request.
 * High is good, until there is no headroom left for new pods.
 */
export function getAllocationBadge(allocation: number): EfficiencyBadgeInfo {
  if (allocation >= 85) return {
    label: 'Tight',
    color: 'var(--efficiency-risk)',
    class: 'efficiency-risk',
    description: 'Little headroom left: new pods may wait for nodes to scale up',
  };
  if (allocation >= 70) return {
    label: 'Excellent',
    color: 'var(--efficiency-excellent)',
    class: 'efficiency-excellent',
    description: 'Nodes are well packed with room to absorb spikes',
  };
  if (allocation >= 50) return {
    label: 'Good',
    color: 'var(--efficiency-good)',
    class: 'efficiency-good',
    description: 'Some capacity is paid for but unclaimed',
  };
  if (allocation >= 30) return {
    label: 'Fair',
    color: 'var(--efficiency-fair)',
    class: 'efficiency-fair',
    description: 'A large share of node capacity is idle',
  };
  return {
    label: 'Low',
    color: 'var(--efficiency-critical)',
    class: 'efficiency-critical',
    description: 'Most node capacity is idle: fewer or smaller nodes would do',
  };
}

/**
 * Usage efficiency: how much of what a workload requests it actually uses.
 * Low means over-provisioned; above 100% means it runs beyond its requests.
 */
export function getUsageBadge(efficiency: number, hasRequests = true, hasUsage = true): EfficiencyBadgeInfo {
  if (!hasRequests) return {
    label: 'No requests',
    color: 'var(--text3)',
    class: 'efficiency-none',
    description: 'No CPU or memory requests: the scheduler can’t place it well and its cost can’t be attributed',
    isSpecialCase: true,
  };
  if (!hasUsage) return {
    label: 'No usage data',
    color: 'var(--text3)',
    class: 'efficiency-none',
    description: 'Install metrics-server to compare usage with requests',
    isSpecialCase: true,
  };
  if (efficiency > 100) return {
    label: 'Under-requested',
    color: 'var(--efficiency-risk)',
    class: 'efficiency-risk',
    description: 'Uses more than it requests: it can be throttled or evicted under pressure',
  };
  if (efficiency >= 70) return {
    label: 'Right-sized',
    color: 'var(--efficiency-excellent)',
    class: 'efficiency-excellent',
    description: 'Requests closely match usage',
  };
  if (efficiency >= 50) return {
    label: 'Good',
    color: 'var(--efficiency-good)',
    class: 'efficiency-good',
    description: 'Requests are a little above usage',
  };
  if (efficiency >= 30) return {
    label: 'Over-provisioned',
    color: 'var(--efficiency-fair)',
    class: 'efficiency-fair',
    description: 'Requests are well above usage',
  };
  return {
    label: 'Idle',
    color: 'var(--efficiency-critical)',
    class: 'efficiency-critical',
    description: 'Uses a small fraction of what it requests',
  };
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) {
    return 'none';
  }
  if (bytes >= 1024 * 1024 * 1024 * 1024) {
    return `${(bytes / (1024 * 1024 * 1024 * 1024)).toFixed(1)} Ti`;
  }
  if (bytes >= 1024 * 1024 * 1024) {
    const gib = bytes / (1024 * 1024 * 1024);
    return gib % 1 === 0 ? `${gib.toFixed(0)} Gi` : `${gib.toFixed(1)} Gi`;
  }
  if (bytes >= 1024 * 1024) {
    const mib = bytes / (1024 * 1024);
    return mib % 1 === 0 ? `${mib.toFixed(0)} Mi` : `${mib.toFixed(1)} Mi`;
  }
  return `${(bytes / 1024).toFixed(0)} Ki`;
}

export function formatMilliCores(milliCores: number): string {
  if (milliCores === 0) {
    return 'none';
  }
  if (milliCores >= 1000) {
    const cores = milliCores / 1000;
    return cores % 1 === 0 ? `${cores.toFixed(0)} cores` : `${cores.toFixed(1)} cores`;
  }
  return `${milliCores}m`;
}

export function formatPercent(pct: number): string {
  if (pct > 0 && pct < 1) return '<1%';
  return `${pct.toFixed(0)}%`;
}

export function getPricingSourceLabel(source: PricingInfo['source']): string {
  switch (source) {
    case 'cur': return 'AWS CUR';
    case 'aws-api': return 'AWS price list';
    case 'azure-api': return 'Azure retail prices';
    default: return 'No price source';
  }
}

export function formatTimeAgo(dateString: string): string {
  if (!dateString) return 'Never';
  const date = new Date(dateString);
  if (isNaN(date.getTime()) || date.getFullYear() < 2000) return 'Never';
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${diffDays}d ago`;
}
