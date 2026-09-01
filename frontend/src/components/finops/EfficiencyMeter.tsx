import React from 'react';
import { getEfficiencyBadge } from '../../types/finops';
import { InfoCircledIcon, CheckCircledIcon, ExclamationTriangleIcon, CrossCircledIcon } from '@radix-ui/react-icons';
import './EfficiencyMeter.css';

interface EfficiencyMeterProps {
  cpuEfficiency: number;
  memoryEfficiency: number;
  overallEfficiency: number;
  onExplainClick?: () => void;
}

export const EfficiencyMeter: React.FC<EfficiencyMeterProps> = ({
  cpuEfficiency,
  memoryEfficiency,
  overallEfficiency,
  onExplainClick,
}) => {
  const badge = getEfficiencyBadge(overallEfficiency, true);
  
  const getIcon = () => {
    if (overallEfficiency >= 85) return <ExclamationTriangleIcon />;
    if (overallEfficiency >= 70) return <CheckCircledIcon />;
    if (overallEfficiency >= 50) return <CheckCircledIcon />;
    if (overallEfficiency >= 30) return <ExclamationTriangleIcon />;
    return <CrossCircledIcon />;
  };

  return (
    <div className="efficiency-meter-card">
      <div className="meter-header">
        <div className="meter-label">
          Resource Efficiency
          {onExplainClick && (
            <button className="info-button" onClick={onExplainClick} title="What is efficiency?">
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
              <span className="zone-label">Critical</span>
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
              <span className="zone-label">Overcommit</span>
            </div>
          </div>
          <div className="meter-indicator" style={{ left: `${Math.min(overallEfficiency, 100)}%` }}>
            <div className="indicator-line" style={{ borderColor: badge.color }} />
            <div className="indicator-value" style={{ backgroundColor: badge.color }}>
              {overallEfficiency.toFixed(0)}%
            </div>
          </div>
        </div>
      </div>

      <div className="efficiency-breakdown">
        <div className="breakdown-item">
          <span className="breakdown-label">CPU</span>
          <span className="breakdown-value" style={{ color: getEfficiencyBadge(cpuEfficiency).color }}>
            {cpuEfficiency.toFixed(0)}%
          </span>
        </div>
        <div className="breakdown-divider" />
        <div className="breakdown-item">
          <span className="breakdown-label">Memory</span>
          <span className="breakdown-value" style={{ color: getEfficiencyBadge(memoryEfficiency).color }}>
            {memoryEfficiency.toFixed(0)}%
          </span>
        </div>
      </div>

      <div className="meter-hint" style={{ color: badge.color }}>
        {badge.description}
      </div>
    </div>
  );
};

