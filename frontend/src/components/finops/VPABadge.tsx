import React from 'react';
import { LightningBoltIcon } from '@radix-ui/react-icons';
import { VPAInfo, formatCost } from '../../types/finops';
import { Tooltip } from '../common/Tooltip';
import './VPABadge.css';

interface VPABadgeProps {
  vpa: VPAInfo;
}

export const VPABadge: React.FC<VPABadgeProps> = ({ vpa }) => {
  if (!vpa.hasRecommendation || vpa.potentialSavings <= 0) {
    return null;
  }

  const tooltipContent = (
    <div className="vpa-tooltip">
      <div className="vpa-tooltip-title">
        <LightningBoltIcon />
        VPA Optimization
      </div>
      <div className="vpa-insight">
        VPA recommends reducing resource requests
      </div>
      <div className="vpa-savings">
        <div className="savings-row">
          <span>Potential savings</span>
          <span className="savings-value">{formatCost(vpa.potentialSavings)}/mo</span>
        </div>
        <div className="savings-row">
          <span>Reduction</span>
          <span className="savings-percent">{Math.abs(vpa.savingsPercent).toFixed(0)}%</span>
        </div>
      </div>
    </div>
  );

  return (
    <Tooltip content={tooltipContent}>
      <span className="vpa-badge">
        <LightningBoltIcon />
        {vpa.savingsPercent > 0 ? '-' : '+'}{Math.abs(vpa.savingsPercent).toFixed(0)}%
      </span>
    </Tooltip>
  );
};

