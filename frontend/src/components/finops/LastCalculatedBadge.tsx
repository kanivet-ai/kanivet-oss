import React from 'react';
import { ClockIcon, ExclamationTriangleIcon } from '@radix-ui/react-icons';
import { formatTimeAgo } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './LastCalculatedBadge.css';

interface LastCalculatedBadgeProps {
  lastUpdated: string;
  cacheDuration?: number;
  /** Set when the costs were computed from pods the pod watch stopped keeping current. */
  podsStale?: boolean;
  podsStaleSince?: string;
}

export const LastCalculatedBadge: React.FC<LastCalculatedBadgeProps> = ({
  lastUpdated,
  cacheDuration = 60,
  podsStale = false,
  podsStaleSince,
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
    <div className="calc-tooltip">
      <div><strong>Cost Calculation</strong></div>
      <div className="calc-tooltip-row">
        Last calculated: {timeAgo}
      </div>
      <div className="calc-tooltip-row">
        Cache duration: {cacheDuration}s
      </div>
      <div className="calc-tooltip-row calc-tooltip-muted">
        Costs refresh automatically every {cacheDuration} seconds
      </div>
    </div>
  );

  const podsTooltip = (
    <div className="calc-tooltip">
      <div><strong>Pods not live</strong></div>
      <div className="calc-tooltip-row">
        {podsStaleSince
          ? `The cluster's pod watch started failing ${formatTimeAgo(podsStaleSince).toLowerCase()}.`
          : "The cluster's pod watch is failing."}
      </div>
      <div className="calc-tooltip-row calc-tooltip-muted">
        Costs use the pods as last seen: ones created, scaled or deleted since are not reflected.
      </div>
    </div>
  );

  return (
    <>
      <Tooltip content={tooltipContent}>
        <span className={`last-calculated-badge ${getStatusClass()}`}>
          <ClockIcon />
          <span className="calc-time">{timeAgo}</span>
          <span className="calc-cache">cached {cacheDuration}s</span>
        </span>
      </Tooltip>
      {podsStale && (
        <Tooltip content={podsTooltip}>
          <span className="last-calculated-badge stale" data-testid="pods-stale-badge">
            <ExclamationTriangleIcon />
            <span className="calc-time">pods not live</span>
          </span>
        </Tooltip>
      )}
    </>
  );
};
