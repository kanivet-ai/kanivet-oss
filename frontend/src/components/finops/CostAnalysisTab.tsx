import React, { useEffect, useState } from 'react';
import api from '../../services/api';
import { CostBreakdownBar } from './CostBadge';
import {
  PodCost,
  WorkloadCost,
  NodeCost,
  NamespaceCost,
  formatCost,
  formatBytes,
  formatMilliCores,
  getEfficiencyColor,
} from '../../types/finops';
import { MixIcon, ReloadIcon } from '@radix-ui/react-icons';
import './CostAnalysisTab.css';

interface CostAnalysisTabProps {
  cluster: string;
  kind: string;
  namespace: string;
  name: string;
}

type CostData = PodCost | WorkloadCost | NodeCost | NamespaceCost | null;

const CostAnalysisTab: React.FC<CostAnalysisTabProps> = ({
  cluster,
  kind,
  namespace,
  name,
}) => {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [costData, setCostData] = useState<CostData>(null);

  useEffect(() => {
    loadCostData();
  }, [cluster, kind, namespace, name]);

  const loadCostData = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.getFinOpsResourceCost(cluster, kind, namespace, name);
      setCostData(data);
    } catch (err: any) {
      if (err.response?.status === 404) {
        setError('Cost data not available for this resource type');
      } else {
        setError(err.message || 'Failed to load cost data');
      }
    } finally {
      setLoading(false);
    }
  };

  if (loading) {
    return (
      <div className="cost-analysis-tab cost-loading">
        <div className="loading-spinner" />
        <span>Loading cost data...</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="cost-analysis-tab cost-error">
        <MixIcon />
        <p>{error}</p>
      </div>
    );
  }

  if (!costData) {
    return (
      <div className="cost-analysis-tab cost-empty">
        <MixIcon />
        <p>No cost data available</p>
      </div>
    );
  }

  const isPod = 'podName' in costData;
  const isWorkload = 'replicas' in costData && !isPod;
  const isNode = 'instanceType' in costData;
  const isNamespace = 'podCount' in costData && !isWorkload;

  return (
    <div className="cost-analysis-tab">
      <div className="cost-header">
        <h3>Cost Analysis</h3>
        <button className="refresh-btn" onClick={loadCostData}>
          <ReloadIcon />
        </button>
      </div>

      <div className="cost-summary-cards">
        <div className="cost-card primary">
          <div className="cost-card-label">Monthly Cost</div>
          <div className="cost-card-value">{formatCost(costData.monthlyCost)}</div>
          <div className="cost-card-sub">
            {formatCost(costData.dailyCost)}/day · {formatCost(costData.hourlyCost)}/hr
          </div>
        </div>

        {(isPod || isWorkload || isNamespace) && 'overallEfficiency' in costData && (
          <div className="cost-card">
            <div className="cost-card-label">Efficiency</div>
            <div className="cost-card-value">
              <span style={{ color: getEfficiencyColor(costData.overallEfficiency) }}>
                {costData.overallEfficiency.toFixed(0)}%
              </span>
            </div>
            {('cpuEfficiency' in costData) && (
              <div className="cost-card-sub">
                CPU: {costData.cpuEfficiency?.toFixed(0) || 0}% · Mem: {costData.memoryEfficiency?.toFixed(0) || 0}%
              </div>
            )}
          </div>
        )}

        {isNode && (
          <div className="cost-card">
            <div className="cost-card-label">Instance</div>
            <div className="cost-card-value instance-type">{(costData as NodeCost).instanceType}</div>
            <div className="cost-card-sub">
              {(costData as NodeCost).isSpot ? (
                <span className="spot-indicator">Spot Instance</span>
              ) : (
                <span>On-Demand</span>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="cost-details">
        <h4>Resource Details</h4>

        {isPod && (
          <PodCostDetails cost={costData as PodCost} />
        )}

        {isWorkload && (
          <WorkloadCostDetails cost={costData as WorkloadCost} />
        )}

        {isNode && (
          <NodeCostDetails cost={costData as NodeCost} />
        )}

        {isNamespace && (
          <NamespaceCostDetails cost={costData as NamespaceCost} />
        )}
      </div>

      {(isPod || isWorkload) && (
        <div className="cost-breakdown-section">
          <h4>Cost Breakdown (Estimated)</h4>
          <div className="breakdown-visual">
            <div className="breakdown-item">
              <span className="breakdown-label">
                <span className="dot cpu" />
                CPU Cost
              </span>
              <span className="breakdown-value">
                ~{formatCost(costData.monthlyCost * 0.7)}
              </span>
            </div>
            <div className="breakdown-item">
              <span className="breakdown-label">
                <span className="dot memory" />
                Memory Cost
              </span>
              <span className="breakdown-value">
                ~{formatCost(costData.monthlyCost * 0.3)}
              </span>
            </div>
          </div>
          <CostBreakdownBar
            cpuCost={costData.monthlyCost * 0.7}
            memoryCost={costData.monthlyCost * 0.3}
            width={200}
            height={12}
          />
        </div>
      )}
    </div>
  );
};

const PodCostDetails: React.FC<{ cost: PodCost }> = ({ cost }) => (
  <div className="details-grid">
    <div className="detail-row">
      <span className="detail-label">Node</span>
      <span className="detail-value mono">{cost.nodeName || 'N/A'}</span>
    </div>
    {cost.ownerKind && (
      <div className="detail-row">
        <span className="detail-label">Owner</span>
        <span className="detail-value">{cost.ownerKind}/{cost.ownerName}</span>
      </div>
    )}
    <div className="detail-row">
      <span className="detail-label">CPU Request</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">CPU Limit</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuLimit)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Memory Request</span>
      <span className="detail-value mono">{formatBytes(cost.memoryRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Memory Limit</span>
      <span className="detail-value mono">{formatBytes(cost.memoryLimit)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Status</span>
      <span className="detail-value">{cost.status}</span>
    </div>
  </div>
);

const WorkloadCostDetails: React.FC<{ cost: WorkloadCost }> = ({ cost }) => (
  <div className="details-grid">
    <div className="detail-row">
      <span className="detail-label">Kind</span>
      <span className="detail-value">{cost.kind}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Replicas</span>
      <span className="detail-value">{cost.replicas}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total CPU Request</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total Memory Request</span>
      <span className="detail-value mono">{formatBytes(cost.memoryRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Cost per Replica</span>
      <span className="detail-value mono">
        {formatCost(cost.monthlyCost / Math.max(cost.replicas, 1))}/mo
      </span>
    </div>
  </div>
);

const NodeCostDetails: React.FC<{ cost: NodeCost }> = ({ cost }) => (
  <div className="details-grid">
    <div className="detail-row">
      <span className="detail-label">Instance Type</span>
      <span className="detail-value mono">{cost.instanceType}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Region</span>
      <span className="detail-value">{cost.region}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Provider</span>
      <span className="detail-value">{cost.provider}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Pod Count</span>
      <span className="detail-value">{cost.podCount}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">CPU Capacity</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuCapacity)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">CPU Allocatable</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuAllocatable)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">CPU Requested</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuRequested)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Memory Capacity</span>
      <span className="detail-value mono">{formatBytes(cost.memoryCapacity)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Memory Allocatable</span>
      <span className="detail-value mono">{formatBytes(cost.memoryAllocatable)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Memory Requested</span>
      <span className="detail-value mono">{formatBytes(cost.memoryRequested)}</span>
    </div>
  </div>
);

const NamespaceCostDetails: React.FC<{ cost: NamespaceCost }> = ({ cost }) => (
  <div className="details-grid">
    <div className="detail-row">
      <span className="detail-label">Pod Count</span>
      <span className="detail-value">{cost.podCount}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total CPU Request</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total CPU Limit</span>
      <span className="detail-value mono">{formatMilliCores(cost.cpuLimit)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total Memory Request</span>
      <span className="detail-value mono">{formatBytes(cost.memoryRequest)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Total Memory Limit</span>
      <span className="detail-value mono">{formatBytes(cost.memoryLimit)}</span>
    </div>
    <div className="detail-row">
      <span className="detail-label">Cost per Pod (avg)</span>
      <span className="detail-value mono">
        {formatCost(cost.monthlyCost / Math.max(cost.podCount, 1))}/mo
      </span>
    </div>
  </div>
);

export default CostAnalysisTab;
