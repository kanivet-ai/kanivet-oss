export interface PricingInfo {
  source: 'cur' | 'aws-api' | 'azure-api' | 'unknown';
  lastUpdated: string;
  instanceCount: number;
  isAvailable: boolean;
  error?: string;
  nodesWithPricing: number;
  nodesMissingPrice: number;
}

export interface ClusterCostSummary {
  cluster: string;
  provider: string;
  region: string;
  nodeCount: number;
  podCount: number;
  namespaceCount: number;
  totalCpu: number;
  totalMemory: number;
  usedCpu: number;
  usedMemory: number;
  requestedCpu: number;
  requestedMemory: number;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  projectedMonthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  idleCost: number;
  idlePercentage: number;
  spotSavings?: number;
  breakdown: CostBreakdown;
  trend?: CostTrend;
  pricingInfo: PricingInfo;
  lastUpdated: string;
}

export interface CostBreakdown {
  computeCost: number;
  cpuCost: number;
  memoryCost: number;
  storageCost: number;
  networkCost: number;
  controlPlaneCost: number;
}

export interface CostTrend {
  direction: 'up' | 'down' | 'stable';
  changePercent: number;
  previous: number;
  current: number;
  datapoints?: CostDatapoint[];
}

export interface CostDatapoint {
  timestamp: string;
  cost: number;
}

export interface NodeCost {
  nodeName: string;
  instanceType: string;
  region: string;
  provider: string;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuCapacity: number;
  memoryCapacity: number;
  cpuAllocatable: number;
  memoryAllocatable: number;
  cpuRequested: number;
  memoryRequested: number;
  cpuUsed?: number;
  memoryUsed?: number;
  podCount: number;
  isSpot: boolean;
  labels?: Record<string, string>;
}

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
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  status: string;
  createdAt: string;
  labels?: Record<string, string>;
}

export interface NamespaceCost {
  namespace: string;
  podCount: number;
  cpuRequest: number;
  cpuLimit: number;
  memoryRequest: number;
  memoryLimit: number;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  topWorkloads?: WorkloadCost[];
}

export interface HPAInfo {
  minReplicas: number;
  maxReplicas: number;
  currentReplicas: number;
  minMonthlyCost: number;
  maxMonthlyCost: number;
}

export interface VPAInfo {
  targetCpu?: number;
  targetMemory?: number;
  hasRecommendation: boolean;
  potentialSavings: number;
  savingsPercent: number;
}

export interface WorkloadCost {
  kind: string;
  name: string;
  namespace: string;
  replicas: number;
  cpuRequest: number;
  memoryRequest: number;
  hourlyCost: number;
  dailyCost: number;
  monthlyCost: number;
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  pods?: PodCost[];
  hpa?: HPAInfo;
  vpa?: VPAInfo;
}

export interface CostRecommendation {
  type: string;
  resource: string;
  namespace?: string;
  currentCost: number;
  projectedSavings: number;
  recommendation: string;
  priority: 'critical' | 'high' | 'medium' | 'low';
  details?: Record<string, unknown>;
}

export type CostTimeRange = '24h' | '7d' | '30d' | 'mtd';

export type CostAggregation = 'namespace' | 'deployment' | 'node' | 'label' | 'pod';

export interface CostFilter {
  namespaces?: string[];
  labels?: Record<string, string>;
  minCost?: number;
  maxCost?: number;
  minEfficiency?: number;
  maxEfficiency?: number;
  ownerKinds?: string[];
  timeRange?: CostTimeRange;
}

export function formatCost(cost: number, decimals = 2): string {
  if (cost >= 1000) {
    return `$${cost.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  }
  if (cost >= 1) {
    return `$${cost.toFixed(decimals)}`;
  }
  if (cost >= 0.01) {
    return `$${cost.toFixed(3)}`;
  }
  return `$${cost.toFixed(4)}`;
}

export function formatCostPerDay(hourlyCost: number): string {
  return formatCost(hourlyCost * 24);
}

export function formatCostPerMonth(hourlyCost: number): string {
  return formatCost(hourlyCost * 24 * 30);
}

export function getEfficiencyColor(efficiency: number): string {
  if (efficiency >= 85) return 'var(--efficiency-risk)';
  if (efficiency >= 70) return 'var(--efficiency-excellent)';
  if (efficiency >= 50) return 'var(--efficiency-good)';
  if (efficiency >= 30) return 'var(--efficiency-fair)';
  return 'var(--efficiency-critical)';
}

export function getEfficiencyLabel(efficiency: number): string {
  if (efficiency >= 85) return 'Risk';
  if (efficiency >= 70) return 'Excellent';
  if (efficiency >= 50) return 'Good';
  if (efficiency >= 30) return 'Fair';
  return 'Critical';
}

export interface EfficiencyBadgeInfo {
  label: string;
  color: string;
  class: string;
  description: string;
  isSpecialCase?: boolean;
}

export function getEfficiencyBadge(efficiency: number, hasRequests: boolean = true): EfficiencyBadgeInfo {
  if (!hasRequests || efficiency === 0) return {
    label: 'No Requests',
    color: 'var(--text-muted)',
    class: 'efficiency-none',
    description: 'Resource requests not defined',
    isSpecialCase: true
  };
  
  if (efficiency >= 85) return {
    label: 'Overcommit',
    color: 'var(--efficiency-risk)',
    class: 'efficiency-risk',
    description: 'Requesting more than available - risky!'
  };
  if (efficiency >= 70) return {
    label: 'Excellent',
    color: 'var(--efficiency-excellent)',
    class: 'efficiency-excellent',
    description: 'Well optimized'
  };
  if (efficiency >= 50) return {
    label: 'Good',
    color: 'var(--efficiency-good)',
    class: 'efficiency-good',
    description: 'Room for improvement'
  };
  if (efficiency >= 30) return {
    label: 'Fair',
    color: 'var(--efficiency-fair)',
    class: 'efficiency-fair',
    description: 'Underutilized'
  };
  return {
    label: 'Critical',
    color: 'var(--efficiency-critical)',
    class: 'efficiency-critical',
    description: 'Massive waste'
  };
}

export function getCostTrendIcon(trend?: CostTrend): string {
  if (!trend) return '→';
  if (trend.direction === 'up') return '↑';
  if (trend.direction === 'down') return '↓';
  return '→';
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) {
    return 'none';
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
    return cores % 1 === 0 ? `${cores.toFixed(0)}` : `${cores.toFixed(1)}`;
  }
  return `${milliCores}m`;
}

export function getPricingSourceLabel(source: PricingInfo['source']): string {
  switch (source) {
    case 'cur': return 'AWS CUR';
    case 'aws-api': return 'AWS API';
    case 'azure-api': return 'Azure API';
    default: return 'Unknown';
  }
}

export function getPricingSourceDescription(source: PricingInfo['source']): string {
  switch (source) {
    case 'cur': return 'Pricing from AWS Cost and Usage Report';
    case 'aws-api': return 'Pricing from AWS Pricing API';
    case 'azure-api': return 'Pricing from Azure Retail Prices API';
    default: return 'Pricing source unavailable';
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
