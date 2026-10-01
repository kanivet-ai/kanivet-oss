import React from 'react';
import { CheckCircledIcon, ExclamationTriangleIcon, CrossCircledIcon, InfoCircledIcon, QuestionMarkCircledIcon } from '@radix-ui/react-icons';
import { getUsageBadge, formatPercent, formatMilliCores, formatBytes } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './HealthBadge.css';

interface HealthBadgeProps {
  /** usage / request, in percent */
  efficiency: number;
  hasUsage?: boolean;
  size?: 'sm' | 'md' | 'lg';
  showLabel?: boolean;
  showIcon?: boolean;
  cpuRequest?: number;
  memoryRequest?: number;
  cpuUsed?: number;
  memoryUsed?: number;
}

/** Usage efficiency of a workload or namespace: what it uses against what it requests. */
export const HealthBadge: React.FC<HealthBadgeProps> = ({
  efficiency,
  hasUsage = false,
  size = 'sm',
  showLabel = true,
  showIcon = true,
  cpuRequest = 0,
  memoryRequest = 0,
  cpuUsed = 0,
  memoryUsed = 0,
}) => {
  const hasRequests = cpuRequest > 0 || memoryRequest > 0;
  const badge = getUsageBadge(efficiency, hasRequests, hasUsage);

  const getIcon = () => {
    if (badge.isSpecialCase) return <QuestionMarkCircledIcon />;
    if (efficiency > 100) return <ExclamationTriangleIcon />;
    if (efficiency >= 70) return <CheckCircledIcon />;
    if (efficiency >= 50) return <InfoCircledIcon />;
    if (efficiency >= 30) return <ExclamationTriangleIcon />;
    return <CrossCircledIcon />;
  };

  const tooltip = (
    <div className="health-tooltip">
      <strong>{badge.isSpecialCase ? badge.label : `${badge.label} (${formatPercent(efficiency)} of requests used)`}</strong>
      <p>{badge.description}</p>
      {hasUsage && hasRequests && (
        <p>
          CPU {formatMilliCores(cpuUsed)} of {formatMilliCores(cpuRequest)} · Memory {formatBytes(memoryUsed)} of {formatBytes(memoryRequest)}
        </p>
      )}
    </div>
  );

  return (
    <Tooltip content={tooltip}>
      <span className={`health-badge health-badge-${size} ${badge.class}`}>
        {showIcon && <span className="health-icon">{getIcon()}</span>}
        {showLabel && <span className="health-label">{badge.label}</span>}
      </span>
    </Tooltip>
  );
};
