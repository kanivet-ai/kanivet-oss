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

  const severity = getSeverity();

  return (
    <div className={`idle-cost-card severity-${severity}`}>
      <div className="idle-icon">
        <ExclamationTriangleIcon />
      </div>
      <div className="idle-content">
        <div className="idle-label">Idle Resources</div>
        <div className="idle-value">{formatCost(idleCost)}/mo</div>
        <div className="idle-percentage">{idlePercentage.toFixed(0)}% of total budget wasted</div>
        <div className="idle-description">
          Resources reserved but not requested by pods
        </div>
      </div>
      <div className="idle-visual">
        <svg viewBox="0 0 100 100" className="idle-chart">
          <circle
            cx="50"
            cy="50"
            r="40"
            fill="none"
            stroke="var(--bg-tertiary)"
            strokeWidth="8"
          />
          <circle
            cx="50"
            cy="50"
            r="40"
            fill="none"
            stroke="currentColor"
            strokeWidth="8"
            strokeDasharray={`${idlePercentage * 2.51} ${(100 - idlePercentage) * 2.51}`}
            strokeDashoffset="0"
            transform="rotate(-90 50 50)"
            strokeLinecap="round"
          />
          <text
            x="50"
            y="50"
            textAnchor="middle"
            dominantBaseline="middle"
            fontSize="20"
            fontWeight="700"
            fill="currentColor"
            fontFamily="var(--font-mono)"
          >
            {idlePercentage.toFixed(0)}%
          </text>
        </svg>
      </div>
    </div>
  );
};

