import type { CostRecommendation, NamespaceCost, NodeCost, WorkloadCost } from '../../types/finops';
import type { FinOpsFilterState } from './FinOpsFilters';
import { countActiveFilters } from './FinOpsFilters';

export interface NodeGroup {
  instanceType: string;
  nodes: NodeCost[];
  totalCost: number;
  allocatedCost: number;
  spotCount: number;
  onDemandCount: number;
  priceMissing: boolean;
  avgCpuAlloc: number;
  avgMemAlloc: number;
}

export const isFiltered = (filters: FinOpsFilterState) => countActiveFilters(filters) > 0;

const includes = (haystack: string | undefined, needle: string) =>
  !!haystack && haystack.toLowerCase().includes(needle);

function workloadMatchesSearch(w: WorkloadCost, q: string): boolean {
  return includes(w.name, q)
    || includes(w.kind, q)
    || includes(w.vclusterNamespace, q)
    || (w.pods ?? []).some((p) => includes(p.podName, q));
}

function workloadPassesFlags(w: WorkloadCost, f: FinOpsFilterState): boolean {
  if (f.showNoRequests && (w.cpuRequest > 0 || w.memoryRequest > 0)) return false;
  if (f.showOverprovisioned && !(w.hasUsage && w.overallEfficiency < 30)) return false;
  if (f.showOvercommitted && !(w.hasUsage && w.overallEfficiency > 100)) return false;
  if (w.hasUsage && (w.overallEfficiency < f.efficiencyMin || w.overallEfficiency > f.efficiencyMax)) return false;
  return true;
}

/**
 * Filters namespaces and, inside them, workloads. A namespace whose own name
 * matches the search keeps all its workloads; otherwise only matching ones.
 * Per-workload flags (no requests, over-provisioned, ...) narrow the
 * workloads, and a namespace stays when any of its workloads does.
 */
export function filterNamespaces(namespaces: NamespaceCost[], f: FinOpsFilterState): NamespaceCost[] {
  const q = f.search.trim().toLowerCase();
  const usesWorkloadFlags = f.showNoRequests || f.showOverprovisioned || f.showOvercommitted
    || f.efficiencyMin > 0 || f.efficiencyMax < 200;

  const out: NamespaceCost[] = [];
  for (const ns of namespaces) {
    if (f.selectedNamespaces.length > 0 && !f.selectedNamespaces.includes(ns.namespace)) continue;
    if (ns.monthlyCost < f.costMin || ns.monthlyCost > f.costMax) continue;

    const nsMatches = !q || includes(ns.namespace, q) || includes(ns.vcluster, q);
    let workloads = ns.topWorkloads ?? [];
    if (!nsMatches) workloads = workloads.filter((w) => workloadMatchesSearch(w, q));
    if (usesWorkloadFlags) workloads = workloads.filter((w) => workloadPassesFlags(w, f));

    if (!nsMatches && workloads.length === 0) continue;
    if (usesWorkloadFlags && workloads.length === 0) continue;
    out.push(workloads === ns.topWorkloads ? ns : { ...ns, topWorkloads: workloads });
  }
  return out;
}

/** Nodes only follow the search and cost filters; the rest describe workloads. */
export function filterNodes(nodes: NodeCost[], f: FinOpsFilterState): NodeCost[] {
  const q = f.search.trim().toLowerCase();
  return nodes.filter((n) => {
    if (q && !includes(n.nodeName, q) && !includes(n.instanceType, q)) return false;
    return n.monthlyCost >= f.costMin && n.monthlyCost <= f.costMax;
  });
}

export function groupNodesByType(nodes: NodeCost[]): NodeGroup[] {
  const groups = new Map<string, NodeGroup>();
  for (const node of nodes) {
    const key = node.instanceType || 'unknown';
    let g = groups.get(key);
    if (!g) {
      g = { instanceType: key, nodes: [], totalCost: 0, allocatedCost: 0, spotCount: 0, onDemandCount: 0, priceMissing: false, avgCpuAlloc: 0, avgMemAlloc: 0 };
      groups.set(key, g);
    }
    g.nodes.push(node);
    g.totalCost += node.monthlyCost;
    g.allocatedCost += node.allocatedMonthlyCost;
    g.priceMissing = g.priceMissing || !!node.priceMissing;
    if (node.isSpot) g.spotCount++;
    else g.onDemandCount++;
  }
  for (const g of groups.values()) {
    let cpu = 0, mem = 0;
    for (const n of g.nodes) {
      if (n.cpuAllocatable > 0) cpu += n.cpuRequested / n.cpuAllocatable;
      if (n.memoryAllocatable > 0) mem += n.memoryRequested / n.memoryAllocatable;
    }
    g.avgCpuAlloc = (cpu / g.nodes.length) * 100;
    g.avgMemAlloc = (mem / g.nodes.length) * 100;
  }
  return Array.from(groups.values()).sort((a, b) => b.totalCost - a.totalCost || a.instanceType.localeCompare(b.instanceType));
}

export function totalSavings(recs: CostRecommendation[]): number {
  return recs.reduce((sum, r) => sum + (r.projectedSavings || 0), 0);
}

/** Maps a workload kind to the resource descriptor the detail view opens. */
export function workloadResource(kind: string): { name: string; group: string; version: string; kind: string; namespaced: boolean } | null {
  switch (kind) {
    case 'Deployment':
    case 'StatefulSet':
    case 'DaemonSet':
    case 'ReplicaSet':
      return { name: `${kind.toLowerCase()}s`, group: 'apps', version: 'v1', kind, namespaced: true };
    case 'Job':
    case 'CronJob':
      return { name: `${kind.toLowerCase()}s`, group: 'batch', version: 'v1', kind, namespaced: true };
    case 'Pod':
      return { name: 'pods', group: '', version: 'v1', kind, namespaced: true };
    default:
      return null;
  }
}
