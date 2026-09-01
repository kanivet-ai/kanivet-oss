import React from 'react';
import { ClockIcon } from '@radix-ui/react-icons';
import { formatTimeAgo } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './LastCalculatedBadge.css';

interface LastCalculatedBadgeProps {
  lastUpdated: string;
  cacheDuration?: number;
}

export const LastCalculatedBadge: React.FC<LastCalculatedBadgeProps> = ({
  lastUpdated,
  cacheDuration = 60,
}) => {
  const timeAgo = formatTimeAgo(lastUpdated);
  const lastCalcDate = new Date(lastUpdated);
  const getStatusClass = () => {
    const ageSeconds = (Date.now() - lastCalcDate.getTime()) / 1000;
    if (ageSeconds > cacheDuration * 2) return 'stale';
    if (ageSeconds > cacheDuration) return 'aging';
    return 'fresh';
  };

  const tooltipContent = (
    <div>
      <div><strong>Cost Calculation</strong></div>
      <div style={{ fontSize: '11px', marginTop: '4px' }}>
        Last calculated: {timeAgo}
      </div>
      <div style={{ fontSize: '11px', marginTop: '4px' }}>
        Cache duration: {cacheDuration}s
      </div>
      <div style={{ fontSize: '11px', marginTop: '4px', color: 'var(--text-muted)' }}>
        Costs refresh automatically every {cacheDuration} seconds
      </div>
    </div>
  );

  return (
    <Tooltip content={tooltipContent}>
      <span className={`last-calculated-badge ${getStatusClass()}`}>
        <ClockIcon />
        <span className="calc-time">{timeAgo}</span>
        <span className="calc-cache">cached {cacheDuration}s</span>
      </span>
    </Tooltip>
  );
};

