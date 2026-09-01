import React, { useEffect, useState, useMemo, useRef } from 'react';
import { useStore } from '../../store';
import api from '../../services/api';
import { PricingSourceBadge } from './CostBadge';
import { LastCalculatedBadge } from './LastCalculatedBadge';
import { EfficiencyMeter } from './EfficiencyMeter';
import { IdleCostCard } from './IdleCostCard';
import { HealthBadge } from './HealthBadge';
import { HPABadge } from './HPABadge';
import { VPABadge } from './VPABadge';
import { EfficiencyExplainer } from './EfficiencyExplainer';
import { FinOpsFilters, FinOpsFilterState } from './FinOpsFilters';
import { FinOpsLoadingSkeleton } from './FinOpsLoadingSkeleton';
import {
  ClusterCostSummary,
  NodeCost,
  NamespaceCost,
  PodCost,
  formatCost,
  formatBytes,
  formatMilliCores,
  getEfficiencyColor,
} from '../../types/finops';
import {
  MixIcon,
  ExclamationTriangleIcon,
  ChevronDownIcon,
  ChevronRightIcon,
} from '@radix-ui/react-icons';
import { Tooltip } from '../common/Tooltip';
import './FinOpsDashboard.css';

interface Dashboard {
  summary: ClusterCostSummary;
  nodes: NodeCost[];
  namespaces: NamespaceCost[];
}

interface NodeGroup {
  instanceType: string;
  nodes: NodeCost[];
  totalCost: number;
  spotCount: number;
  onDemandCount: number;
  avgCpuUtil: number;
  avgMemUtil: number;
}

const FinOpsDashboard: React.FC = () => {
  const { currentTab } = useStore();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [expandedNs, setExpandedNs] = useState<Set<string>>(new Set());
  const [expandedWl, setExpandedWl] = useState<Set<string>>(new Set());
  const [expandedNodeGroup, setExpandedNodeGroup] = useState<Set<string>>(new Set());
  const [showExplainer, setShowExplainer] = useState(false);
  const [filters, setFilters] = useState<FinOpsFilterState>({
    search: '',
    selectedNamespaces: [],
    efficiencyMin: 0,
    efficiencyMax: 200,
    costMin: 0,
    costMax: 999999,
    showNoRequests: false,
    showOverprovisioned: false,
    showOvercommitted: false,
  });

  const cluster = currentTab;
  const abortRef = useRef<AbortController | null>(null);
  const fastPollTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const fastPollCount = useRef(0);

  useEffect(() => {
    if (!cluster) return;
    api.preloadFinOpsPricing(cluster).catch(() => {});
    setDashboard(null);
    setLoading(true);
    setError(null);
    fastPollCount.current = 0;
    loadData();
    const intervalId = setInterval(loadData, 60000);
    return () => {
      clearInterval(intervalId);
      if (fastPollTimer.current) clearTimeout(fastPollTimer.current);
      abortRef.current?.abort();
    };
  }, [cluster]);

  const loadData = async () => {
    if (!cluster) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const isCurrent = () => abortRef.current === ctrl;
    setError(null);
    const acc: Partial<Dashboard> = {};
    let firstChunk = true;
    try {
      await api.streamFinOpsDashboard(cluster, (type, data) => {
        if (!isCurrent()) return;
        if (type === 'error') {
          setError(typeof data === 'string' ? data : 'Failed to load cost data');
          return;
        }
        if (type === 'done') return;
        if (type === 'summary' || type === 'nodes' || type === 'namespaces') {
          (acc as any)[type] = data;
          setDashboard({
            summary: acc.summary as ClusterCostSummary,
            nodes: acc.nodes || [],
            namespaces: acc.namespaces || [],
          });
          if (firstChunk) {
            setLoading(false);
            firstChunk = false;
          }
        }
      }, ctrl.signal);
    } catch (err: any) {
      if (isCurrent() && err?.name !== 'AbortError') setError(err.message || 'Failed to load cost data');
    } finally {
      if (isCurrent()) {
        setLoading(false);
        const missing = (acc.summary as any)?.pricingInfo?.nodesMissingPrice ?? 0;
        if (fastPollTimer.current) clearTimeout(fastPollTimer.current);
        if (missing > 0 && fastPollCount.current < 30) {
          fastPollCount.current++;
          fastPollTimer.current = setTimeout(loadData, 10000);
        } else if (missing === 0) {
          fastPollCount.current = 0;
        }
      }
    }
  };

  const toggleNs = (ns: string) => {
    setExpandedNs(prev => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
    });
  };

  const toggleWl = (key: string) => {
    setExpandedWl(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const toggleNodeGroup = (key: string) => {
    setExpandedNodeGroup(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const groupNodesByType = (nodes: NodeCost[]): NodeGroup[] => {
    const groups = new Map<string, NodeGroup>();
    for (const node of nodes) {
      const key = node.instanceType || 'unknown';
      let group = groups.get(key);
      if (!group) {
        group = { instanceType: key, nodes: [], totalCost: 0, spotCount: 0, onDemandCount: 0, avgCpuUtil: 0, avgMemUtil: 0 };
        groups.set(key, group);
      }
      group.nodes.push(node);
      group.totalCost += node.monthlyCost;
      if (node.isSpot) group.spotCount++;
      else group.onDemandCount++;
    }
    for (const group of groups.values()) {
      let totalCpu = 0, totalMem = 0;
      for (const n of group.nodes) {
        if (n.cpuAllocatable > 0) totalCpu += n.cpuRequested / n.cpuAllocatable;
        if (n.memoryAllocatable > 0) totalMem += n.memoryRequested / n.memoryAllocatable;
      }
      group.avgCpuUtil = (totalCpu / group.nodes.length) * 100;
      group.avgMemUtil = (totalMem / group.nodes.length) * 100;
    }
    return Array.from(groups.values()).sort((a, b) => b.totalCost - a.totalCost);
  };

  if (!cluster) {
    return (
      <div className="finops-dashboard finops-empty">
        <MixIcon />
        <p>Select a cluster to view cost analysis</p>
      </div>
    );
  }

  const { summary, nodes, namespaces } = dashboard || {};

  const filteredNamespaces = useMemo(() => {
    if (!namespaces) return [];
    return namespaces.filter(ns => {
      if (filters.selectedNamespaces.length > 0 && !filters.selectedNamespaces.includes(ns.namespace)) {
        return false;
      }
      
      const searchLower = filters.search.toLowerCase();
      if (filters.search) {
        const nsMatches = ns.namespace.toLowerCase().includes(searchLower);
        const workloadMatches = ns.topWorkloads?.some(wl => 
          wl.name.toLowerCase().includes(searchLower) || 
          wl.kind.toLowerCase().includes(searchLower)
        );
        if (!nsMatches && !workloadMatches) return false;
      }
      
      if (ns.overallEfficiency < filters.efficiencyMin || ns.overallEfficiency > filters.efficiencyMax) {
        return false;
      }
      
      if (ns.monthlyCost < filters.costMin || ns.monthlyCost > filters.costMax) {
        return false;
      }
      
      if (filters.showNoRequests && (ns.cpuRequest > 0 || ns.memoryRequest > 0)) {
        return false;
      }
      
      if (filters.showOverprovisioned && ns.overallEfficiency >= 30) {
        return false;
      }
      
      if (filters.showOvercommitted && ns.overallEfficiency < 85) {
        return false;
      }
      
      return true;
    }).map(ns => ({
      ...ns,
      topWorkloads: ns.topWorkloads?.filter(wl => {
        if (!filters.search) return true;
        const searchLower = filters.search.toLowerCase();
        return wl.name.toLowerCase().includes(searchLower) || 
               wl.kind.toLowerCase().includes(searchLower);
      })
    }));
  }, [namespaces, filters]);

  const filteredNodes = useMemo(() => {
    if (!nodes) return [];
    return nodes.filter(node => {
      const searchLower = filters.search.toLowerCase();
      if (filters.search) {
        const nodeMatches = node.nodeName.toLowerCase().includes(searchLower);
        const instanceMatches = node.instanceType.toLowerCase().includes(searchLower);
        if (!nodeMatches && !instanceMatches) return false;
      }
      
      const eff = node.cpuAllocatable > 0 ? (node.cpuRequested / node.cpuAllocatable) * 100 : 0;
      if (eff < filters.efficiencyMin || eff > filters.efficiencyMax) {
        return false;
      }
      
      if (node.monthlyCost < filters.costMin || node.monthlyCost > filters.costMax) {
        return false;
      }
      
      if (filters.showOverprovisioned && eff >= 30) {
        return false;
      }
      
      if (filters.showOvercommitted && eff < 85) {
        return false;
      }
      
      return true;
    });
  }, [nodes, filters]);

  const isFiltered = filters.search !== '' || 
                     filters.selectedNamespaces.length > 0 ||
                     filters.efficiencyMin > 0 || 
                     filters.efficiencyMax < 200 ||
                     filters.costMin > 0 || 
                     filters.costMax < 999999 ||
                     filters.showNoRequests ||
                     filters.showOverprovisioned ||
                     filters.showOvercommitted;

  const filteredStats = useMemo(() => {
    if (!isFiltered) {
      return {
        monthlyCost: summary?.monthlyCost || 0,
        dailyCost: summary?.dailyCost || 0,
        totalCpu: summary?.totalCpu || 0,
        totalMemory: summary?.totalMemory || 0,
        requestedCpu: summary?.requestedCpu || 0,
        requestedMemory: summary?.requestedMemory || 0,
        cpuEfficiency: summary?.cpuEfficiency || 0,
        memoryEfficiency: summary?.memoryEfficiency || 0,
        overallEfficiency: summary?.overallEfficiency || 0,
        idleCost: summary?.idleCost || 0,
        idlePercentage: summary?.idlePercentage || 0,
      };
    }

    let totalMonthlyCost = 0;
    let totalCpu = 0;
    let totalMemory = 0;
    let requestedCpu = 0;
    let requestedMemory = 0;

    filteredNamespaces.forEach(ns => {
      totalMonthlyCost += ns.monthlyCost;
      requestedCpu += ns.cpuRequest;
      requestedMemory += ns.memoryRequest;
    });

    filteredNodes.forEach(node => {
      totalCpu += node.cpuAllocatable;
      totalMemory += node.memoryAllocatable;
    });

    const cpuEfficiency = totalCpu > 0 ? (requestedCpu / totalCpu) * 100 : 0;
    const memoryEfficiency = totalMemory > 0 ? (requestedMemory / totalMemory) * 100 : 0;
    const overallEfficiency = (cpuEfficiency + memoryEfficiency) / 2;

    const idleCost = totalMonthlyCost > 0 && totalCpu > 0 && totalMemory > 0
      ? totalMonthlyCost * ((1 - cpuEfficiency / 100) * 0.7 + (1 - memoryEfficiency / 100) * 0.3)
      : 0;
    const idlePercentage = totalMonthlyCost > 0 ? (idleCost / totalMonthlyCost) * 100 : 0;

    return {
      monthlyCost: totalMonthlyCost,
      dailyCost: totalMonthlyCost / 30,
      totalCpu,
      totalMemory,
      requestedCpu,
      requestedMemory,
      cpuEfficiency,
      memoryEfficiency,
      overallEfficiency,
      idleCost,
      idlePercentage,
    };
  }, [isFiltered, filteredNamespaces, filteredNodes, summary]);

  const totalCost = filteredStats.monthlyCost;
  const totalNamespaceCost = filteredNamespaces.reduce((sum, ns) => sum + ns.monthlyCost, 0);
  const controlPlaneCost = summary?.breakdown?.controlPlaneCost || 0;

  return (
    <div className="finops-dashboard">
      <div className="finops-header">
        <div className="finops-title">
          <MixIcon />
          <h2>FinOps</h2>
          {summary && (
            <span className="finops-total-cost">
              {formatCost(filteredStats.monthlyCost)}/mo
              {isFiltered && <span className="filtered-badge">filtered</span>}
            </span>
          )}
        </div>
        <div className="finops-actions">
          {summary?.lastUpdated && (
            <LastCalculatedBadge lastUpdated={summary.lastUpdated} cacheDuration={60} />
          )}
          {summary?.pricingInfo && <PricingSourceBadge pricingInfo={summary.pricingInfo} />}
        </div>
      </div>

      {error && (
        <div className={`finops-error ${error.includes('not yet supported') ? 'finops-info' : ''}`}>
          <ExclamationTriangleIcon /> {error}
        </div>
      )}

      <div className="finops-content">
        {loading && !dashboard ? (
          <FinOpsLoadingSkeleton />
        ) : dashboard ? (
          <div className="finops-body">
            <FinOpsFilters
              filters={filters}
              onFiltersChange={setFilters}
              stats={{
                totalNamespaces: namespaces?.length || 0,
                totalNodes: nodes?.length || 0,
                filteredNamespaces: filteredNamespaces.length,
                filteredNodes: filteredNodes.length,
              }}
              availableNamespaces={namespaces?.map(ns => ns.namespace) || []}
            />

            <div className="finops-summary">
              <div className="finops-stats">
                <div className="stat-card primary">
                  <div className="stat-label">
                    Monthly Cost
                    {isFiltered && <span className="filtered-indicator"> (filtered)</span>}
                </div>
                  <div className="stat-value">{formatCost(filteredStats.monthlyCost)}</div>
                </div>
                <div className="stat-card">
                  <div className="stat-label">
                    Yearly Cost
                    {isFiltered && <span className="filtered-indicator"> (filtered)</span>}
                  </div>
                  <div className="stat-value">{formatCost(filteredStats.monthlyCost * 12)}</div>
                </div>
                <div className="stat-card">
                  <div className="stat-label">
                    Daily Cost
                    {isFiltered && <span className="filtered-indicator"> (filtered)</span>}
                  </div>
                  <div className="stat-value">{formatCost(filteredStats.dailyCost)}</div>
                </div>
              </div>

              {filteredStats.idleCost > 0 && (
                <IdleCostCard idleCost={filteredStats.idleCost} idlePercentage={filteredStats.idlePercentage} />
              )}

              <EfficiencyMeter
                cpuEfficiency={filteredStats.cpuEfficiency}
                memoryEfficiency={filteredStats.memoryEfficiency}
                overallEfficiency={filteredStats.overallEfficiency}
                onExplainClick={() => setShowExplainer(true)}
              />

              <div className="cost-allocation">
                <div className="allocation-label">
                  Cost Distribution
                  {isFiltered && <span className="filtered-indicator"> (filtered)</span>}
                </div>
                <div className="allocation-bar">
                  {filteredNamespaces.slice(0, 5).map((ns, i) => {
                    const pct = totalCost > 0 ? (ns.monthlyCost / totalCost) * 100 : 0;
                    return pct > 0 ? (
                      <Tooltip key={ns.namespace} content={
                        <div className="alloc-tooltip">
                          <div className="alloc-tooltip-title">{ns.namespace}</div>
                          <div className="alloc-tooltip-row"><span>Cost</span><span>{formatCost(ns.monthlyCost)}/mo</span></div>
                          <div className="alloc-tooltip-row"><span>Share</span><span>{pct.toFixed(1)}%</span></div>
                          <div className="alloc-tooltip-row"><span>Pods</span><span>{ns.podCount}</span></div>
                          <div className="alloc-tooltip-row"><span>Efficiency</span><span style={{ color: getEfficiencyColor(ns.overallEfficiency) }}>{ns.overallEfficiency.toFixed(0)}%</span></div>
                        </div>
                      } side="top">
                        <div className={`allocation-segment seg-${i}`} style={{ width: `${pct}%` }} />
                      </Tooltip>
                    ) : null;
                  })}
                  {(() => {
                    const top5Cost = filteredNamespaces.slice(0, 5).reduce((s, ns) => s + ns.monthlyCost, 0);
                    const otherNsCost = totalNamespaceCost - top5Cost;
                    const otherPct = totalCost > 0 ? (otherNsCost / totalCost) * 100 : 0;
                    const otherCount = filteredNamespaces.length - 5;
                    return otherPct > 0 ? (
                      <Tooltip content={
                        <div className="alloc-tooltip">
                          <div className="alloc-tooltip-title">Other Namespaces</div>
                          <div className="alloc-tooltip-row"><span>Cost</span><span>{formatCost(otherNsCost)}/mo</span></div>
                          <div className="alloc-tooltip-row"><span>Share</span><span>{otherPct.toFixed(1)}%</span></div>
                          <div className="alloc-tooltip-row"><span>Count</span><span>{otherCount} namespaces</span></div>
                        </div>
                      } side="top">
                        <div className="allocation-segment seg-other" style={{ width: `${otherPct}%` }} />
                      </Tooltip>
                    ) : null;
                  })()}
                  {filteredStats.idleCost > 0 && !isFiltered && (
                    <Tooltip content={
                      <div className="alloc-tooltip">
                        <div className="alloc-tooltip-title">Idle / Unallocated</div>
                        <div className="alloc-tooltip-row"><span>Cost</span><span>{formatCost(filteredStats.idleCost)}/mo</span></div>
                        <div className="alloc-tooltip-row"><span>Share</span><span>{((filteredStats.idleCost / totalCost) * 100).toFixed(1)}%</span></div>
                        <div className="alloc-tooltip-desc">Resources reserved but not requested by pods</div>
                      </div>
                    } side="top">
                      <div className="allocation-segment seg-idle" style={{ width: `${(filteredStats.idleCost / totalCost) * 100}%` }} />
                    </Tooltip>
                  )}
                  {controlPlaneCost > 0 && !isFiltered && (
                    <Tooltip content={
                      <div className="alloc-tooltip">
                        <div className="alloc-tooltip-title">Control Plane</div>
                        <div className="alloc-tooltip-row"><span>Cost</span><span>{formatCost(controlPlaneCost)}/mo</span></div>
                        <div className="alloc-tooltip-row"><span>Share</span><span>{((controlPlaneCost / totalCost) * 100).toFixed(1)}%</span></div>
                        <div className="alloc-tooltip-desc">Managed Kubernetes control plane fee</div>
                      </div>
                    } side="top">
                      <div className="allocation-segment seg-cp" style={{ width: `${(controlPlaneCost / totalCost) * 100}%` }} />
                    </Tooltip>
                  )}
                </div>
                <div className="allocation-legend">
                  {filteredNamespaces.slice(0, 5).map((ns, i) => (
                    <span key={ns.namespace} className="legend-item">
                      <span className={`legend-dot seg-${i}`} />
                      <span className="legend-label">{ns.namespace}</span>
                    </span>
                  ))}
                  {filteredNamespaces.length > 5 && <span className="legend-item"><span className="legend-dot seg-other" /><span className="legend-label">Other</span></span>}
                  {filteredStats.idleCost > 0 && !isFiltered && <span className="legend-item"><span className="legend-dot seg-idle" /><span className="legend-label">Idle</span></span>}
                  {controlPlaneCost > 0 && !isFiltered && <span className="legend-item"><span className="legend-dot seg-cp" /><span className="legend-label">Control Plane</span></span>}
                </div>
              </div>
            </div>

            <div className="finops-section">
              <div className="section-header">
                <h3>Cost by Namespace</h3>
                <span className="section-count">
                  {filteredNamespaces.length}
                  {filteredNamespaces.length !== namespaces?.length && ` of ${namespaces?.length}`} namespaces
                </span>
              </div>
              <div className="section-content">
                <table className="cost-table">
                  <thead>
                    <tr>
                      <th className="col-expand"></th>
                      <th className="col-name">Namespace / Workload / Pod</th>
                      <th className="col-pods">Resources</th>
                      <th className="col-efficiency">Efficiency</th>
                      <th className="col-cost">Cost/mo</th>
                      <th className="col-share">Share / Node</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredNamespaces.length === 0 ? (
                      <tr>
                        <td colSpan={6} className="empty-state">
                          <div className="empty-content">
                            <MixIcon />
                            <span>No namespaces match your filters</span>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      filteredNamespaces.map(ns => {
                      const nsExpanded = expandedNs.has(ns.namespace);
                      const pct = totalCost > 0 ? (ns.monthlyCost / totalCost) * 100 : 0;
                      return (
                        <React.Fragment key={ns.namespace}>
                          <tr className="ns-row" onClick={() => toggleNs(ns.namespace)}>
                            <td className="col-expand">
                              {nsExpanded ? <ChevronDownIcon /> : <ChevronRightIcon />}
                            </td>
                            <td className="col-name">{ns.namespace}</td>
                            <td className="col-pods">
                              <span className="resource-count">{ns.podCount}</span>
                            </td>
                            <td className="col-efficiency">
                              <span>
                                <HealthBadge 
                                  efficiency={ns.overallEfficiency} 
                                  showLabel={false}
                                  cpuRequest={ns.cpuRequest}
                                  memoryRequest={ns.memoryRequest}
                                />
                              <span style={{ color: getEfficiencyColor(ns.overallEfficiency) }}>
                                  {ns.overallEfficiency > 0 ? `${ns.overallEfficiency.toFixed(0)}%` : '-'}
                                </span>
                              </span>
                            </td>
                            <td className="col-cost">{formatCost(ns.monthlyCost)}</td>
                            <td className="col-share">
                              <div className="share-cell">
                                <div className="share-bar">
                                  <div className="share-fill" style={{ width: `${Math.min(pct, 100)}%` }} />
                                </div>
                                <span className="share-pct">{pct < 1 && pct > 0 ? '<1' : pct.toFixed(0)}%</span>
                              </div>
                            </td>
                          </tr>
                          {nsExpanded && ns.topWorkloads && ns.topWorkloads.map(wl => {
                            const wlKey = `${ns.namespace}/${wl.kind}/${wl.name}`;
                            const wlExpanded = expandedWl.has(wlKey);
                            return (
                              <React.Fragment key={wlKey}>
                                <tr className="wl-row" onClick={() => toggleWl(wlKey)}>
                                  <td className="col-expand">
                                    {wl.pods && wl.pods.length > 0 ? (
                                      wlExpanded ? <ChevronDownIcon /> : <ChevronRightIcon />
                                    ) : null}
                                  </td>
                                  <td className="col-name">
                                    <span className="wl-kind-badge">{wl.kind}</span>
                                    {wl.name}
                                  </td>
                                  <td className="col-pods">
                                    <span className="resource-count">{wl.replicas}</span>
                                    {wl.hpa && (
                                      <HPABadge hpa={wl.hpa} />
                                    )}
                                  </td>
                                  <td className="col-efficiency">
                                    <span>
                                      <HealthBadge 
                                        efficiency={wl.overallEfficiency} 
                                        showLabel={false}
                                        cpuRequest={wl.cpuRequest}
                                        memoryRequest={wl.memoryRequest}
                                      />
                                    <span style={{ color: getEfficiencyColor(wl.overallEfficiency) }}>
                                        {wl.overallEfficiency > 0 ? `${wl.overallEfficiency.toFixed(0)}%` : '-'}
                                      </span>
                                    </span>
                                  </td>
                                  <td className="col-cost">
                                    <div className="cost-with-insights">
                                      <span className="cost-value">{formatCost(wl.monthlyCost)}</span>
                                      {wl.vpa && wl.vpa.hasRecommendation && wl.vpa.potentialSavings > 0 && (
                                        <VPABadge vpa={wl.vpa} />
                                      )}
                                    </div>
                                  </td>
                                  <td className="col-share"></td>
                                </tr>
                                {wlExpanded && wl.pods && wl.pods.map((pod: PodCost) => {
                                  const nodeInfo = nodes?.find(n => n.nodeName === pod.nodeName);
                                  const nodeName = pod.nodeName?.split('.')[0] || '-';
                                  const cpuUtil = nodeInfo && nodeInfo.cpuAllocatable > 0 
                                    ? ((nodeInfo.cpuRequested / nodeInfo.cpuAllocatable) * 100).toFixed(0)
                                    : '0';
                                  const memUtil = nodeInfo && nodeInfo.memoryAllocatable > 0
                                    ? ((nodeInfo.memoryRequested / nodeInfo.memoryAllocatable) * 100).toFixed(0)
                                    : '0';
                                  return (
                                  <tr key={pod.podName} className="pod-row">
                                    <td className="col-expand"></td>
                                      <td className="col-name">
                                        <span className="pod-name-text">{pod.podName}</span>
                                      </td>
                                    <td className="col-pods">
                                        <div className="pod-resources">
                                          <Tooltip content="CPU requests">
                                            <span className="resource-compact cpu">
                                              {formatMilliCores(pod.cpuRequest)}
                                            </span>
                                          </Tooltip>
                                          <Tooltip content="Memory requests">
                                            <span className="resource-compact mem">
                                              {formatBytes(pod.memoryRequest)}
                                            </span>
                                          </Tooltip>
                                        </div>
                                    </td>
                                    <td className="col-efficiency">
                                        <span className="pod-empty">-</span>
                                      </td>
                                      <td className="col-cost">
                                        <span className="pod-cost-compact">{formatCost(pod.monthlyCost)}</span>
                                      </td>
                                      <td className="col-share">
                                        {nodeInfo ? (
                                          <Tooltip content={
                                            <div className="node-tooltip">
                                              <div className="node-tooltip-title">
                                                <div className="node-tooltip-name">{nodeName}</div>
                                                <div className="node-tooltip-instance">{nodeInfo.instanceType}</div>
                                              </div>
                                              
                                              <div className="node-tooltip-section">
                                                <div className="node-tooltip-label">Utilization</div>
                                                <div className="node-tooltip-metrics">
                                                  <div className="metric-row">
                                                    <span className="metric-label">CPU</span>
                                                    <div className="metric-bar">
                                                      <div className="metric-fill cpu" style={{ width: `${cpuUtil}%` }} />
                                                    </div>
                                                    <span className="metric-value">{cpuUtil}%</span>
                                                  </div>
                                                  <div className="metric-row">
                                                    <span className="metric-label">MEM</span>
                                                    <div className="metric-bar">
                                                      <div className="metric-fill mem" style={{ width: `${memUtil}%` }} />
                                                    </div>
                                                    <span className="metric-value">{memUtil}%</span>
                                                  </div>
                                                </div>
                                              </div>

                                              <div className="node-tooltip-footer">
                                                <div className="footer-item">
                                                  <span className="footer-label">{nodeInfo.podCount} pods</span>
                                                </div>
                                                <div className="footer-item">
                                                  <span className="footer-label">{nodeInfo.region}</span>
                                                </div>
                                                {nodeInfo.isSpot && (
                                                  <div className="footer-item spot">
                                                    <span className="footer-label">spot</span>
                                                  </div>
                                                )}
                                              </div>
                                            </div>
                                          } side="left">
                                            <span className="node-link">{nodeName}</span>
                                          </Tooltip>
                                        ) : (
                                          <span className="node-text">{nodeName}</span>
                                        )}
                                    </td>
                                  </tr>
                                  );
                                })}
                              </React.Fragment>
                            );
                          })}
                        </React.Fragment>
                      );
                      })
                    )}
                  </tbody>
                </table>
              </div>
            </div>

            <div className="finops-section">
              <div className="section-header">
                <h3>Infrastructure</h3>
                <span className="section-count">
                  {filteredNodes.length}
                  {filteredNodes.length !== nodes?.length && ` of ${nodes?.length}`} nodes
                </span>
              </div>
              <div className="section-content">
                <table className="cost-table nodes-table">
                  <thead>
                    <tr>
                      <th className="col-expand"></th>
                      <th className="col-name">Instance Type / Node</th>
                      <th className="col-count">Nodes / Pods</th>
                      <th className="col-util">Utilization</th>
                      <th className="col-cost">Cost/mo</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredNodes.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="empty-state">
                          <div className="empty-content">
                            <MixIcon />
                            <span>No nodes match your filters</span>
                          </div>
                        </td>
                      </tr>
                    ) : (
                      filteredNodes && groupNodesByType(filteredNodes).map(group => {
                      const isExpanded = expandedNodeGroup.has(group.instanceType);
                      return (
                        <React.Fragment key={group.instanceType}>
                          <tr className="ng-row" onClick={() => toggleNodeGroup(group.instanceType)}>
                            <td className="col-expand">
                              {isExpanded ? <ChevronDownIcon /> : <ChevronRightIcon />}
                            </td>
                            <td className="col-name">
                              <span className="instance-type-name">{group.instanceType}</span>
                              {group.spotCount > 0 && (
                                <Tooltip content={`${group.spotCount} spot, ${group.onDemandCount} on-demand`}>
                                  <span className="spot-badge-compact">{group.spotCount} spot</span>
                                </Tooltip>
                              )}
                            </td>
                            <td className="col-count">
                              <span className="resource-count">{group.nodes.length}</span>
                            </td>
                            <td className="col-util">
                              <Tooltip content={`Average CPU utilization across ${group.nodes.length} nodes`}>
                                <span className="efficiency-compact" style={{ 
                                  color: group.avgCpuUtil < 30 ? 'var(--efficiency-critical)' : 
                                         group.avgCpuUtil < 50 ? 'var(--efficiency-fair)' :
                                         group.avgCpuUtil < 70 ? 'var(--efficiency-good)' :
                                         'var(--efficiency-excellent)'
                                }}>
                                  {group.avgCpuUtil.toFixed(0)}% avg
                                </span>
                              </Tooltip>
                            </td>
                            <td className="col-cost">
                              <span className="cost-value">{formatCost(group.totalCost)}</span>
                            </td>
                          </tr>
                          {isExpanded && group.nodes.map(node => {
                            const cpuPct = node.cpuAllocatable > 0 ? (node.cpuRequested / node.cpuAllocatable) * 100 : 0;
                            const memPct = node.memoryAllocatable > 0 ? (node.memoryRequested / node.memoryAllocatable) * 100 : 0;
                            return (
                              <tr key={node.nodeName} className="node-row">
                                <td className="col-expand"></td>
                                <td className="col-name">
                                  {node.nodeName.split('.')[0]}
                                  {node.isSpot && <span className="spot-badge-sm">spot</span>}
                                </td>
                                <td className="col-count">{node.podCount} pods</td>
                                <td className="col-util">
                                  <div className="util-cell">
                                    <div className="util-bars">
                                      <div className="util-bar">
                                        <div className="util-fill cpu" style={{ width: `${cpuPct}%` }} />
                                      </div>
                                      <div className="util-bar">
                                        <div className="util-fill mem" style={{ width: `${memPct}%` }} />
                                      </div>
                                    </div>
                                    <span className="util-pct">{cpuPct.toFixed(0)}% / {memPct.toFixed(0)}%</span>
                                  </div>
                                </td>
                                <td className="col-cost">{formatCost(node.monthlyCost)}</td>
                              </tr>
                            );
                          })}
                        </React.Fragment>
                      );
                      })
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        ) : null}
      </div>

      {showExplainer && summary && (
        <EfficiencyExplainer
          summary={summary}
          onClose={() => setShowExplainer(false)}
        />
      )}
    </div>
  );
};

export default FinOpsDashboard;
