import React, { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, ExternalLinkIcon, MixIcon } from '@radix-ui/react-icons';
import {
  NamespaceCost,
  NodeCost,
  PodCost,
  WorkloadCost,
  formatBytes,
  formatCost,
  formatMilliCores,
  formatPercent,
  getUsageBadge,
} from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import { HealthBadge } from './HealthBadge';
import { HPABadge } from './HPABadge';
import { workloadResource } from './finopsView';

interface Props {
  namespaces: NamespaceCost[];
  totalCost: number;
  nodes: NodeCost[];
  emptyMessage: string;
  /** Omitted when rows can't be opened from here (e.g. host pods seen from a vcluster). */
  onOpenWorkload?: (kind: string, namespace: string, name: string) => void;
  onOpenVCluster?: (namespace: string, name: string) => void;
  /** Hide the share column (used for the vcluster control plane table). */
  hideShare?: boolean;
  /** Show the usage column; without metrics-server it would be all dashes. */
  showUsage?: boolean;
  nameHeader?: string;
  /** Monthly savings from the rightsizing engine, by workload and by namespace. */
  workloadSavings?: (wl: WorkloadCost) => number | undefined;
  namespaceSavings?: (ns: NamespaceCost) => number | undefined;
}

interface EfficiencyCellProps {
  item: Pick<WorkloadCost, 'overallEfficiency' | 'hasUsage' | 'cpuRequest' | 'memoryRequest' | 'cpuUsed' | 'memoryUsed'>;
}

const EfficiencyCell: React.FC<EfficiencyCellProps> = ({ item }) => {
  const hasRequests = item.cpuRequest > 0 || item.memoryRequest > 0;
  if (!item.hasUsage && hasRequests) {
    return <span className="pod-empty">—</span>;
  }
  const color = getUsageBadge(item.overallEfficiency, hasRequests, !!item.hasUsage).color;
  return (
    <span>
      <HealthBadge
        efficiency={item.overallEfficiency}
        hasUsage={item.hasUsage}
        showLabel={false}
        cpuRequest={item.cpuRequest}
        memoryRequest={item.memoryRequest}
        cpuUsed={item.cpuUsed}
        memoryUsed={item.memoryUsed}
      />
      {hasRequests && <span style={{ color }}>{formatPercent(item.overallEfficiency)}</span>}
    </span>
  );
};

const CostCell: React.FC<{ monthly: number; savings?: number; compact?: boolean }> = ({ monthly, savings, compact }) => (
  <div className="cost-with-insights">
    {savings !== undefined && savings >= 1 && (
      <Tooltip content={`Rightsizing its requests to what its usage history supports saves about ${formatCost(savings)}/mo. See Rightsizing for the evidence.`}>
        <span className="savings-hint">−{formatCost(savings)}</span>
      </Tooltip>
    )}
    <span className={compact ? 'pod-cost-compact' : 'cost-value'}>{formatCost(monthly)}</span>
  </div>
);

const PodNodeCell: React.FC<{ pod: PodCost; node?: NodeCost }> = ({ pod, node }) => {
  const short = pod.nodeName?.split('.')[0] || '—';
  if (!node) return <span className="node-text">{short}</span>;
  const cpu = node.cpuAllocatable > 0 ? (node.cpuRequested / node.cpuAllocatable) * 100 : 0;
  const mem = node.memoryAllocatable > 0 ? (node.memoryRequested / node.memoryAllocatable) * 100 : 0;
  return (
    <Tooltip
      side="left"
      content={
        <div className="node-tooltip">
          <div className="node-tooltip-title">
            <div className="node-tooltip-name">{short}</div>
            <div className="node-tooltip-instance">{node.instanceType}{node.priceMissing ? '' : ` · ${formatCost(node.monthlyCost)}/mo`}</div>
          </div>
          <div className="node-tooltip-section">
            <div className="node-tooltip-label">Requested</div>
            <div className="node-tooltip-metrics">
              <div className="metric-row">
                <span className="metric-label">CPU</span>
                <div className="metric-bar"><div className="metric-fill cpu" style={{ width: `${Math.min(cpu, 100)}%` }} /></div>
                <span className="metric-value">{formatPercent(cpu)}</span>
              </div>
              <div className="metric-row">
                <span className="metric-label">Memory</span>
                <div className="metric-bar"><div className="metric-fill mem" style={{ width: `${Math.min(mem, 100)}%` }} /></div>
                <span className="metric-value">{formatPercent(mem)}</span>
              </div>
            </div>
          </div>
          <div className="node-tooltip-footer">
            <div className="footer-item"><span className="footer-label">{node.podCount} pods</span></div>
            <div className="footer-item"><span className="footer-label">{node.region}</span></div>
            {node.isSpot && <div className="footer-item spot"><span className="footer-label">Spot</span></div>}
          </div>
        </div>
      }
    >
      <span className="node-link">{short}</span>
    </Tooltip>
  );
};

export const NamespaceCostTable: React.FC<Props> = ({
  namespaces,
  totalCost,
  nodes,
  emptyMessage,
  onOpenWorkload,
  onOpenVCluster,
  hideShare,
  showUsage = true,
  nameHeader = 'Namespace / Workload / Pod',
  workloadSavings,
  namespaceSavings,
}) => {
  const [expandedNs, setExpandedNs] = useState<Set<string>>(new Set());
  const [expandedWl, setExpandedWl] = useState<Set<string>>(new Set());
  const nodeByName = React.useMemo(() => new Map(nodes.map((n) => [n.nodeName, n])), [nodes]);
  const toggle = (set: Set<string>, key: string, update: (s: Set<string>) => void) => {
    const next = new Set(set);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    update(next);
  };
  const colCount = 4 + (hideShare ? 0 : 1) + (showUsage ? 1 : 0);

  const renderPod = (pod: PodCost) => (
    <tr key={`${pod.namespace}/${pod.podName}`} className="pod-row">
      <td className="col-expand"></td>
      <td className="col-name">
        <span className="pod-name-text">{pod.podName}</span>
        {onOpenWorkload && <button
          className="row-open-btn"
          onClick={() => onOpenWorkload('Pod', pod.namespace, pod.podName)}
          title="Open pod"
          aria-label={`Open pod ${pod.podName}`}
        >
          <ExternalLinkIcon />
        </button>}
      </td>
      <td className="col-pods">
        <div className="pod-resources">
          <Tooltip content={pod.hasUsage ? `CPU used ${formatMilliCores(pod.cpuUsed || 0)} of ${formatMilliCores(pod.cpuRequest)} requested` : 'CPU requested'}>
            <span className="resource-compact cpu">{formatMilliCores(pod.cpuRequest)}</span>
          </Tooltip>
          <Tooltip content={pod.hasUsage ? `Memory used ${formatBytes(pod.memoryUsed || 0)} of ${formatBytes(pod.memoryRequest)} requested` : 'Memory requested'}>
            <span className="resource-compact mem">{formatBytes(pod.memoryRequest)}</span>
          </Tooltip>
        </div>
      </td>
      {showUsage && <td className="col-efficiency"><EfficiencyCell item={pod} /></td>}
      <td className="col-cost"><CostCell monthly={pod.monthlyCost} compact /></td>
      {!hideShare && <td className="col-share"><PodNodeCell pod={pod} node={nodeByName.get(pod.nodeName)} /></td>}
    </tr>
  );

  const renderWorkload = (ns: NamespaceCost, wl: WorkloadCost) => {
    const key = `${ns.namespace}/${wl.vclusterNamespace ?? ''}/${wl.kind}/${wl.name}`;
    const isBarePod = wl.kind === 'Pod';
    const expandable = !isBarePod && (wl.pods?.length ?? 0) > 0;
    const expanded = expandable && expandedWl.has(key);
    // Workloads synced from a vcluster live in the vcluster, not in this cluster.
    const openable = !!onOpenWorkload && !wl.vclusterNamespace && !!workloadResource(wl.kind);
    return (
      <React.Fragment key={key}>
        <tr className={`wl-row${expandable ? ' is-expandable' : ''}`} onClick={() => expandable && toggle(expandedWl, key, setExpandedWl)}>
          <td className="col-expand">{expandable ? (expanded ? <ChevronDownIcon /> : <ChevronRightIcon />) : null}</td>
          <td className="col-name">
            <span className="wl-kind-badge">{wl.kind}</span>
            <span className="wl-name">{wl.name}</span>
            {wl.vclusterNamespace && (
              <Tooltip content={`Runs in namespace ${wl.vclusterNamespace} inside vcluster ${ns.vcluster ?? ''}`}>
                <span className="vns-chip">{wl.vclusterNamespace}</span>
              </Tooltip>
            )}
            {openable && (
              <button
                className="row-open-btn"
                onClick={(e) => { e.stopPropagation(); onOpenWorkload!(wl.kind, wl.namespace, wl.name); }}
                title={`Open ${wl.kind}`}
                aria-label={`Open ${wl.kind} ${wl.name}`}
              >
                <ExternalLinkIcon />
              </button>
            )}
          </td>
          <td className="col-pods">
            <span className="resource-count">{isBarePod ? '1 pod' : `${wl.replicas} ${wl.replicas === 1 ? 'pod' : 'pods'}`}</span>
            {wl.hpa && <HPABadge hpa={wl.hpa} />}
          </td>
          {showUsage && <td className="col-efficiency"><EfficiencyCell item={wl} /></td>}
          <td className="col-cost"><CostCell monthly={wl.monthlyCost} savings={workloadSavings?.(wl)} /></td>
          {!hideShare && (
            <td className="col-share">
              {isBarePod && wl.pods?.[0] ? <PodNodeCell pod={wl.pods[0]} node={nodeByName.get(wl.pods[0].nodeName)} /> : null}
            </td>
          )}
        </tr>
        {expanded && wl.pods!.map(renderPod)}
      </React.Fragment>
    );
  };

  return (
    <table className="cost-table">
      <thead>
        <tr>
          <th className="col-expand"></th>
          <th className="col-name">{nameHeader}</th>
          <th className="col-pods">Pods</th>
          {showUsage && (
            <th className="col-efficiency">
              <Tooltip content="Share of requested CPU and memory actually used, from metrics-server">
                <span>Requests used</span>
              </Tooltip>
            </th>
          )}
          <th className="col-cost">Cost/mo</th>
          {!hideShare && <th className="col-share">Share / Node</th>}
        </tr>
      </thead>
      <tbody>
        {namespaces.length === 0 ? (
          <tr>
            <td colSpan={colCount} className="empty-state">
              <div className="empty-content">
                <MixIcon />
                <span>{emptyMessage}</span>
              </div>
            </td>
          </tr>
        ) : namespaces.map((ns) => {
          const expanded = expandedNs.has(ns.namespace);
          const pct = totalCost > 0 ? (ns.monthlyCost / totalCost) * 100 : 0;
          return (
            <React.Fragment key={ns.namespace}>
              <tr className="ns-row is-expandable" onClick={() => toggle(expandedNs, ns.namespace, setExpandedNs)}>
                <td className="col-expand">{expanded ? <ChevronDownIcon /> : <ChevronRightIcon />}</td>
                <td className="col-name">
                  <span className="ns-name">{ns.namespace}</span>
                  {ns.vcluster && (
                    onOpenVCluster ? (
                      <button
                        className="vcluster-chip"
                        onClick={(e) => { e.stopPropagation(); onOpenVCluster(ns.namespace, ns.vcluster!); }}
                        title={`Open costs for vcluster ${ns.vcluster}`}
                      >
                        vcluster {ns.vcluster}
                        <ExternalLinkIcon />
                      </button>
                    ) : (
                      <span className="vcluster-chip static">vcluster {ns.vcluster}</span>
                    )
                  )}
                </td>
                <td className="col-pods"><span className="resource-count">{ns.podCount}</span></td>
                {showUsage && <td className="col-efficiency"><EfficiencyCell item={ns} /></td>}
                <td className="col-cost"><CostCell monthly={ns.monthlyCost} savings={namespaceSavings?.(ns)} /></td>
                {!hideShare && (
                  <td className="col-share">
                    <div className="share-cell">
                      <div className="share-bar"><div className="share-fill" style={{ width: `${Math.min(pct, 100)}%` }} /></div>
                      <span className="share-pct">{formatPercent(pct)}</span>
                    </div>
                  </td>
                )}
              </tr>
              {expanded && (ns.topWorkloads ?? []).map((wl) => renderWorkload(ns, wl))}
            </React.Fragment>
          );
        })}
      </tbody>
    </table>
  );
};
