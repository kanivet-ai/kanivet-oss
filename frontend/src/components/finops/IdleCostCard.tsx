import React from 'react';
import { formatCost } from '../../types/finops';
import { ExclamationTriangleIcon } from '@radix-ui/react-icons';
import './IdleCostCard.css';

interface IdleCostCardProps {
  idleCost: number;
  idlePercentage: number;
}

export const IdleCostCard: React.FC<IdleCostCardProps> = ({ idleCost, idlePercentage }) => {
  const getSeverity = () => {
    if (idlePercentage >= 40) return 'critical';
    if (idlePercentage >= 25) return 'high';
    if (idlePercentage >= 15) return 'medium';
    return 'low';
  };
  const pct = Math.min(Math.max(idlePercentage, 0), 100);

  return (
    <div className={`idle-cost-card severity-${getSeverity()}`}>
      <div className="idle-icon">
        <ExclamationTriangleIcon />
      </div>
      <div className="idle-content">
        <div className="idle-label">Idle Capacity</div>
        <div className="idle-value">{formatCost(idleCost)}/mo</div>
        <div className="idle-percentage">{idlePercentage.toFixed(0)}% of spend</div>
        <div className="idle-description">
          Node capacity you pay for that no pod requests
        </div>
      </div>
      <div className="idle-visual">
        <svg viewBox="0 0 100 100" className="idle-chart" role="img" aria-label={`${pct.toFixed(0)}% idle`}>
          <circle cx="50" cy="50" r="40" fill="none" stroke="var(--ctrl)" strokeWidth="8" />
          <circle
            cx="50"
            cy="50"
            r="40"
            fill="none"
            stroke="currentColor"
            strokeWidth="8"
            strokeDasharray={`${pct * 2.51} ${(100 - pct) * 2.51}`}
            strokeDashoffset="0"
            transform="rotate(-90 50 50)"
            strokeLinecap="round"
          />
          <text x="50" y="50" textAnchor="middle" dominantBaseline="middle" fontSize="20" fill="currentColor">
            {pct.toFixed(0)}%
          </text>
        </svg>
      </div>
    </div>
  );
};
