import React, { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon, ExternalLinkIcon, MixIcon } from '@radix-ui/react-icons';
import { NodeCost, formatCost, formatPercent } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import { groupNodesByType } from './finopsView';

const pct = (n: number, d: number) => (d > 0 ? (n / d) * 100 : 0);

const allocColor = (p: number) =>
  p < 30 ? 'var(--efficiency-critical)' : p < 50 ? 'var(--efficiency-fair)' : p < 70 ? 'var(--efficiency-good)' : 'var(--efficiency-excellent)';

const Price: React.FC<{ node: Pick<NodeCost, 'priceMissing' | 'spotAtOnDemand'>; value: number; muted?: boolean }> = ({ node, value, muted }) => {
  if (node.priceMissing) {
    return (
      <Tooltip content="No price found for this instance type yet">
        <span className="price-pending">unpriced</span>
      </Tooltip>
    );
  }
  const text = <span className={muted ? 'pod-cost-compact' : 'cost-value'}>{formatCost(value)}</span>;
  if (!node.spotAtOnDemand) return text;
  return (
    <Tooltip content="Spot node priced at on-demand list price: the real rate is usually lower">
      <span className="upper-bound">≤ {text}</span>
    </Tooltip>
  );
};

const AllocationBars: React.FC<{ node: NodeCost }> = ({ node }) => {
  const cpu = pct(node.cpuRequested, node.cpuAllocatable);
  const mem = pct(node.memoryRequested, node.memoryAllocatable);
  return (
    <div className="util-cell">
      <div className="util-bars">
        <div className="util-bar"><div className="util-fill cpu" style={{ width: `${Math.min(cpu, 100)}%` }} /></div>
        <div className="util-bar"><div className="util-fill mem" style={{ width: `${Math.min(mem, 100)}%` }} /></div>
      </div>
      <span className="util-pct">{formatPercent(cpu)} / {formatPercent(mem)}</span>
    </div>
  );
};

const EmptyRow: React.FC<{ colSpan: number; message: string }> = ({ colSpan, message }) => (
  <tr>
    <td colSpan={colSpan} className="empty-state">
      <div className="empty-content">
        <MixIcon />
        <span>{message}</span>
      </div>
    </td>
  </tr>
);

interface ClusterNodeTableProps {
  nodes: NodeCost[];
  onOpenNode: (name: string) => void;
}

/** Nodes of a cluster, grouped by instance type, with the idle share of each. */
export const ClusterNodeTable: React.FC<ClusterNodeTableProps> = ({ nodes, onOpenNode }) => {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const groups = React.useMemo(() => groupNodesByType(nodes), [nodes]);
  const toggle = (key: string) => {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setExpanded(next);
  };

  return (
    <table className="cost-table nodes-table">
      <thead>
        <tr>
          <th className="col-expand"></th>
          <th className="col-name">Instance Type / Node</th>
          <th className="col-count">Nodes / Pods</th>
          <th className="col-util">
            <Tooltip content="CPU / memory requested, as a share of allocatable">
              <span>Requested</span>
            </Tooltip>
          </th>
          <th className="col-cost">
            <Tooltip content="Node cost not claimed by any pod request">
              <span>Idle/mo</span>
            </Tooltip>
          </th>
          <th className="col-cost">Cost/mo</th>
        </tr>
      </thead>
      <tbody>
        {groups.length === 0 ? <EmptyRow colSpan={6} message="No nodes match your filters" /> : groups.map((g) => {
          const isOpen = expanded.has(g.instanceType);
          const avg = (g.avgCpuAlloc + g.avgMemAlloc) / 2;
          const anySpotAtOnDemand = g.nodes.some((n) => n.spotAtOnDemand);
          return (
            <React.Fragment key={g.instanceType}>
              <tr className="ng-row is-expandable" onClick={() => toggle(g.instanceType)}>
                <td className="col-expand">{isOpen ? <ChevronDownIcon /> : <ChevronRightIcon />}</td>
                <td className="col-name">
                  <span className="instance-type-name">{g.instanceType}</span>
                  {g.spotCount > 0 && (
                    <Tooltip content={`${g.spotCount} spot, ${g.onDemandCount} on-demand`}>
                      <span className="spot-badge-compact">{g.spotCount} spot</span>
                    </Tooltip>
                  )}
                </td>
                <td className="col-count"><span className="resource-count">{g.nodes.length}</span></td>
                <td className="col-util">
                  <Tooltip content={`Average requested: CPU ${formatPercent(g.avgCpuAlloc)}, memory ${formatPercent(g.avgMemAlloc)}`}>
                    <span className="efficiency-compact" style={{ color: allocColor(avg) }}>
                      {formatPercent(g.avgCpuAlloc)} / {formatPercent(g.avgMemAlloc)}
                    </span>
                  </Tooltip>
                </td>
                <td className="col-cost">
                  {g.priceMissing ? null : <span className="pod-cost-compact">{formatCost(Math.max(g.totalCost - g.allocatedCost, 0))}</span>}
                </td>
                <td className="col-cost">
                  <Price node={{ priceMissing: g.priceMissing && g.totalCost === 0, spotAtOnDemand: anySpotAtOnDemand }} value={g.totalCost} />
                </td>
              </tr>
              {isOpen && g.nodes.map((node) => (
                <tr key={node.nodeName} className="node-row">
                  <td className="col-expand"></td>
                  <td className="col-name">
                    {node.nodeName.split('.')[0]}
                    {node.isSpot && <span className="spot-badge-sm">Spot</span>}
                    <button
                      className="row-open-btn"
                      onClick={() => onOpenNode(node.nodeName)}
                      title="Open node"
                      aria-label={`Open node ${node.nodeName}`}
                    >
                      <ExternalLinkIcon />
                    </button>
                  </td>
                  <td className="col-count">{node.podCount} pods</td>
                  <td className="col-util"><AllocationBars node={node} /></td>
                  <td className="col-cost">
                    {node.priceMissing ? null : <span className="pod-cost-compact">{formatCost(Math.max(node.monthlyCost - node.allocatedMonthlyCost, 0))}</span>}
                  </td>
                  <td className="col-cost"><Price node={node} value={node.monthlyCost} muted /></td>
                </tr>
              ))}
            </React.Fragment>
          );
        })}
      </tbody>
    </table>
  );
};

/** Host nodes a vcluster's pods run on, with the vcluster's share of each. */
export const VClusterHostNodeTable: React.FC<{ nodes: NodeCost[] }> = ({ nodes }) => (
  <table className="cost-table nodes-table">
    <thead>
      <tr>
        <th className="col-expand"></th>
        <th className="col-name">Host Node</th>
        <th className="col-count">vcluster Pods</th>
        <th className="col-share">Share of Node</th>
        <th className="col-cost">Node Cost/mo</th>
        <th className="col-cost">vcluster Cost/mo</th>
      </tr>
    </thead>
    <tbody>
      {nodes.length === 0 ? <EmptyRow colSpan={6} message="No host nodes match your filters" /> : nodes.map((node) => {
        const share = pct(node.allocatedMonthlyCost, node.monthlyCost);
        return (
          <tr key={node.nodeName} className="node-row">
            <td className="col-expand"></td>
            <td className="col-name">
              {node.nodeName.split('.')[0]}
              <span className="node-type-chip">{node.instanceType || 'unknown'}</span>
              {node.isSpot && <span className="spot-badge-sm">Spot</span>}
            </td>
            <td className="col-count">{node.podCount}</td>
            <td className="col-share">
              {node.priceMissing ? null : (
                <div className="share-cell">
                  <div className="share-bar"><div className="share-fill" style={{ width: `${Math.min(share, 100)}%` }} /></div>
                  <span className="share-pct">{formatPercent(share)}</span>
                </div>
              )}
            </td>
            <td className="col-cost"><Price node={node} value={node.monthlyCost} muted /></td>
            <td className="col-cost"><Price node={node} value={node.allocatedMonthlyCost} /></td>
          </tr>
        );
      })}
    </tbody>
  </table>
);
