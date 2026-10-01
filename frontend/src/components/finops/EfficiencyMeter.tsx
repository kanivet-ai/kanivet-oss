import React from 'react';
import { getAllocationBadge, formatPercent } from '../../types/finops';
import { InfoCircledIcon, CheckCircledIcon, ExclamationTriangleIcon, CrossCircledIcon } from '@radix-ui/react-icons';
import './EfficiencyMeter.css';

interface EfficiencyMeterProps {
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  onExplainClick?: () => void;
}

/** Allocation meter: how much of the cluster's allocatable capacity pods request. */
export const EfficiencyMeter: React.FC<EfficiencyMeterProps> = ({
  cpuEfficiency,
  memoryEfficiency,
  overallEfficiency,
  onExplainClick,
}) => {
  const badge = getAllocationBadge(overallEfficiency);

  const getIcon = () => {
    if (overallEfficiency >= 85) return <ExclamationTriangleIcon />;
    if (overallEfficiency >= 50) return <CheckCircledIcon />;
    if (overallEfficiency >= 30) return <ExclamationTriangleIcon />;
    return <CrossCircledIcon />;
  };

  return (
    <div className="efficiency-meter-card">
      <div className="meter-header">
        <div className="meter-label">
          Capacity Allocated
          {onExplainClick && (
            <button className="info-button" onClick={onExplainClick} title="How is this calculated?">
              <InfoCircledIcon />
            </button>
          )}
        </div>
        <div className={`efficiency-badge ${badge.class}`}>
          {getIcon()}
          <span>{badge.label}</span>
        </div>
      </div>

      <div className="meter-visual">
        <div className="meter-track">
          <div className="meter-zones">
            <div className="zone zone-critical" style={{ width: '30%' }}>
              <span className="zone-label">Low</span>
            </div>
            <div className="zone zone-fair" style={{ width: '20%' }}>
              <span className="zone-label">Fair</span>
            </div>
            <div className="zone zone-good" style={{ width: '20%' }}>
              <span className="zone-label">Good</span>
            </div>
            <div className="zone zone-excellent" style={{ width: '15%' }}>
              <span className="zone-label">Excellent</span>
            </div>
            <div className="zone zone-risk" style={{ width: '15%' }}>
              <span className="zone-label">Tight</span>
            </div>
          </div>
          <div className="meter-indicator" style={{ left: `${Math.min(overallEfficiency, 100)}%` }}>
            <div className="indicator-line" style={{ borderColor: badge.color }} />
            <div className="indicator-value" style={{ backgroundColor: badge.color }}>
              {formatPercent(overallEfficiency)}
            </div>
          </div>
        </div>
      </div>

      <div className="efficiency-breakdown">
        <div className="breakdown-item">
          <span className="breakdown-label">CPU requested</span>
          <span className="breakdown-value" style={{ color: getAllocationBadge(cpuEfficiency).color }}>
            {formatPercent(cpuEfficiency)}
          </span>
        </div>
        <div className="breakdown-divider" />
        <div className="breakdown-item">
          <span className="breakdown-label">Memory requested</span>
          <span className="breakdown-value" style={{ color: getAllocationBadge(memoryEfficiency).color }}>
            {formatPercent(memoryEfficiency)}
          </span>
        </div>
      </div>

      <div className="meter-hint" style={{ color: badge.color }}>
        {badge.description}
      </div>
    </div>
  );
};
