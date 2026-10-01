import React, { useEffect } from 'react';
import { Cross2Icon } from '@radix-ui/react-icons';
import {
  ClusterCostSummary,
  formatCost,
  formatBytes,
  formatMilliCores,
  formatPercent,
  getPricingSourceLabel,
} from '../../types/finops';
import './EfficiencyExplainer.css';

interface EfficiencyExplainerProps {
  summary: ClusterCostSummary;
  onClose: () => void;
}

interface BarsProps {
  label: string;
  total: number;
  requested: number;
  used?: number;
  format: (n: number) => string;
}

const ResourceBars: React.FC<BarsProps> = ({ label, total, requested, used, format }) => {
  const pct = (n: number) => (total > 0 ? Math.min((n / total) * 100, 100) : 0);
  return (
    <div className="comparison-row">
      <div className="comparison-label">{label}</div>
      <div className="comparison-bars">
        <div className="comparison-bar">
          <span className="bar-label">Allocatable</span>
          <div className="bar-track"><div className="bar-fill total" style={{ width: '100%' }} /></div>
          <span className="bar-value">{format(total)}</span>
        </div>
        <div className="comparison-bar">
          <span className="bar-label">Requested</span>
          <div className="bar-track"><div className="bar-fill used" style={{ width: `${pct(requested)}%` }} /></div>
          <span className="bar-value">{format(requested)}</span>
        </div>
        {used !== undefined && (
          <div className="comparison-bar">
            <span className="bar-label">Used</span>
            <div className="bar-track"><div className="bar-fill used" style={{ width: `${pct(used)}%`, opacity: 0.6 }} /></div>
            <span className="bar-value">{format(used)}</span>
          </div>
        )}
        <div className="comparison-bar waste">
          <span className="bar-label">Unrequested</span>
          <div className="bar-track"><div className="bar-fill waste-fill" style={{ width: `${100 - pct(requested)}%` }} /></div>
          <span className="bar-value">{format(Math.max(total - requested, 0))}</span>
        </div>
      </div>
      <div className="comparison-efficiency">{formatPercent(pct(requested))}</div>
    </div>
  );
};

export const EfficiencyExplainer: React.FC<EfficiencyExplainerProps> = ({ summary, onClose }) => {
  const isVCluster = summary.scope === 'vcluster';

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div className="efficiency-explainer-overlay" onClick={onClose}>
      <div className="efficiency-explainer-modal ap-popover" role="dialog" aria-label="How costs are calculated" onClick={(e) => e.stopPropagation()}>
        <div className="explainer-header">
          <h2>How these costs are calculated</h2>
          <button className="close-button" onClick={onClose} aria-label="Close">
            <Cross2Icon />
          </button>
        </div>

        <div className="explainer-content">
          <div className="explainer-section">
            <h3>Node prices</h3>
            <p>
              Each node is priced from its instance type and region using the{' '}
              <strong>{getPricingSourceLabel(summary.pricingInfo.source)}</strong>, at on-demand rates, over a 30-day month.
              {summary.spotAtOnDemandCount > 0 && (
                <> {summary.spotAtOnDemandCount} spot node{summary.spotAtOnDemandCount === 1 ? ' is' : 's are'} priced at
                  on-demand too, because the price list has no spot rates: treat those figures as an upper bound.</>
              )}
              {!isVCluster && summary.breakdown.controlPlaneCost > 0 && (
                <> The managed control plane fee ({formatCost(summary.breakdown.controlPlaneCost)}/mo) is added on top.</>
              )}
            </p>
          </div>

          <div className="explainer-section">
            <h3>Pod and workload costs</h3>
            <div className="formula-card">
              <div className="formula-label">Formula</div>
              <code className="formula">Pod cost = CPU × node CPU rate + memory × node memory rate</code>
              <div className="formula-breakdown">
                <div className="formula-item">
                  <span className="formula-key">CPU, memory:</span>
                  <span className="formula-value">what the pod requests, or what it uses when that is higher</span>
                </div>
                <div className="formula-item">
                  <span className="formula-key">Rates:</span>
                  <span className="formula-value">the node's price split between its vCPUs and memory</span>
                </div>
              </div>
            </div>
            <p>Workloads and namespaces add up their pods. Pods without requests cost nothing here, which is why they are flagged.</p>
          </div>

          {isVCluster ? (
            <div className="explainer-section">
              <h3>vcluster share</h3>
              <p>
                A vcluster has no nodes of its own. Its pods run on the host cluster's nodes, so its cost is what those
                pods claim on the host, plus its own control plane (the vcluster API server and etcd pods in the{' '}
                <strong>{summary.vcluster?.namespace}</strong> namespace).
                {summary.vcluster && summary.vcluster.hostMonthlyCost > 0 && (
                  <> That is <strong>{formatPercent(summary.vcluster.sharePercent)}</strong> of the host's{' '}
                    {formatCost(summary.vcluster.hostMonthlyCost)}/mo.</>
                )}
              </p>
            </div>
          ) : (
            <div className="explainer-section">
              <h3>Idle capacity and allocation</h3>
              <p>
                <strong>Idle</strong> is each node's price minus what the pods on it claim: capacity you pay for that no
                pod requested. <strong>Capacity allocated</strong> is requests as a share of allocatable capacity.
                Around 70–85% packs nodes well while leaving headroom for spikes and rollouts.
              </p>
              <div className="resource-comparison">
                <ResourceBars
                  label="CPU"
                  total={summary.totalCpu}
                  requested={summary.requestedCpu}
                  used={summary.usageAvailable ? summary.usedCpu : undefined}
                  format={formatMilliCores}
                />
                <ResourceBars
                  label="Memory"
                  total={summary.totalMemory}
                  requested={summary.requestedMemory}
                  used={summary.usageAvailable ? summary.usedMemory : undefined}
                  format={formatBytes}
                />
              </div>
            </div>
          )}

          <div className="explainer-section">
            <h3>Usage efficiency</h3>
            {summary.usageAvailable ? (
              <p>
                Usage efficiency compares what workloads use right now (from metrics-server) with what they request.
                <strong> Right-sizing savings</strong> price the requests above 1.3× current usage. This is a point-in-time
                reading, so check a workload's peaks before lowering its requests.
              </p>
            ) : (
              <div className="info-callout">
                metrics-server isn't reachable on this cluster, so costs are based on requests alone and right-sizing
                savings can't be estimated.
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
