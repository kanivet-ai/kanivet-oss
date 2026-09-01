import React from 'react';
import { DoubleArrowUpIcon, DoubleArrowDownIcon } from '@radix-ui/react-icons';
import { HPAInfo, formatCost } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './HPABadge.css';

interface HPABadgeProps {
  hpa: HPAInfo;
}

export const HPABadge: React.FC<HPABadgeProps> = ({ hpa }) => {
  const isAtMax = hpa.currentReplicas >= hpa.maxReplicas;
  const isAtMin = hpa.currentReplicas <= hpa.minReplicas;

  const tooltipContent = (
    <div className="hpa-tooltip">
      <div className="hpa-tooltip-title">Cost Range (HPA)</div>
      <div className="hpa-range-visual">
        <div className="range-bar">
          <div className="range-marker min">
            <DoubleArrowDownIcon />
          </div>
          <div className="range-track">
            <div 
              className="range-current" 
              style={{ left: `${((hpa.currentReplicas - hpa.minReplicas) / (hpa.maxReplicas - hpa.minReplicas)) * 100}%` }}
            />
          </div>
          <div className="range-marker max">
            <DoubleArrowUpIcon />
          </div>
        </div>
        <div className="range-labels">
          <span className="range-label">{formatCost(hpa.minMonthlyCost)}</span>
          <span className="range-label">{formatCost(hpa.maxMonthlyCost)}</span>
        </div>
      </div>
      <div className="hpa-stats">
        <div className="hpa-stat-row">
          <span>Min replicas</span>
          <span>{hpa.minReplicas}</span>
        </div>
        <div className="hpa-stat-row">
          <span>Current</span>
          <span>{hpa.currentReplicas}</span>
        </div>
        <div className="hpa-stat-row">
          <span>Max replicas</span>
          <span>{hpa.maxReplicas}</span>
        </div>
      </div>
      <div className="hpa-insight">
        {isAtMax ? 'At maximum - cannot scale up!' : isAtMin ? 'At minimum capacity' : `Can scale ${hpa.maxReplicas - hpa.currentReplicas}x more`}
      </div>
    </div>
  );

  return (
    <Tooltip content={tooltipContent}>
      <span className={`hpa-badge ${isAtMax ? 'at-max' : isAtMin ? 'at-min' : ''}`}>
        {formatCost(hpa.minMonthlyCost)}-{formatCost(hpa.maxMonthlyCost)}
      </span>
    </Tooltip>
  );
};

