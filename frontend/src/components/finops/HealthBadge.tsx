import React from 'react';
import { CheckCircledIcon, ExclamationTriangleIcon, CrossCircledIcon, InfoCircledIcon, QuestionMarkCircledIcon } from '@radix-ui/react-icons';
import { getEfficiencyBadge } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './HealthBadge.css';

interface HealthBadgeProps {
  efficiency: number;
  size?: 'sm' | 'md' | 'lg';
  showLabel?: boolean;
  showIcon?: boolean;
  cpuRequest?: number;
  memoryRequest?: number;
}

export const HealthBadge: React.FC<HealthBadgeProps> = ({
  efficiency,
  size = 'sm',
  showLabel = true,
  showIcon = true,
  cpuRequest = 1,
  memoryRequest = 1,
}) => {
  const hasRequests = cpuRequest > 0 || memoryRequest > 0;
  const badge = getEfficiencyBadge(efficiency, hasRequests);
  
  const getIcon = () => {
    if (badge.isSpecialCase) return <QuestionMarkCircledIcon />;
    if (efficiency >= 85) return <ExclamationTriangleIcon />;
    if (efficiency >= 70) return <CheckCircledIcon />;
    if (efficiency >= 50) return <InfoCircledIcon />;
    if (efficiency >= 30) return <ExclamationTriangleIcon />;
    return <CrossCircledIcon />;
  };

  const getTooltipContent = () => {
    if (badge.isSpecialCase) {
      return (
        <div className="health-tooltip">
          <strong>No Resource Requests</strong>
          <p>
            This workload has no CPU/memory requests defined. Set requests for proper scheduling and cost allocation.
          </p>
        </div>
      );
    }
    if (efficiency >= 85) {
      return (
        <div className="health-tooltip">
          <strong>Overcommitted ({efficiency.toFixed(0)}%)</strong>
          <p>
            Pods are requesting more than available capacity. This works due to overcommitment, but leaves no headroom for traffic spikes.
          </p>
        </div>
      );
    }
    return badge.description;
  };

  return (
    <Tooltip content={getTooltipContent()}>
      <span className={`health-badge health-badge-${size} ${badge.class}`}>
        {showIcon && <span className="health-icon">{getIcon()}</span>}
        {showLabel && <span className="health-label">{badge.label}</span>}
      </span>
    </Tooltip>
  );
};

