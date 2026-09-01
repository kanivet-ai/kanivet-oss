import React from 'react';
import { formatCost, getEfficiencyColor, PricingInfo, getPricingSourceLabel, formatTimeAgo } from '../../types/finops';
import { InfoCircledIcon, ExclamationTriangleIcon, CheckCircledIcon } from '@radix-ui/react-icons';
import './CostBadge.css';

interface CostBadgeProps {
  cost: number;
  period?: 'hourly' | 'daily' | 'monthly';
  size?: 'sm' | 'md' | 'lg';
  showTrend?: boolean;
  trendDirection?: 'up' | 'down' | 'stable';
  trendPercent?: number;
  efficiency?: number;
  showEfficiency?: boolean;
  compact?: boolean;
  className?: string;
}

export const CostBadge: React.FC<CostBadgeProps> = ({
  cost,
  period = 'monthly',
  size = 'sm',
  showTrend = false,
  trendDirection,
  trendPercent,
  efficiency,
  showEfficiency = false,
  compact = false,
  className = '',
}) => {
  const displayCost = period === 'hourly' ? cost : period === 'daily' ? cost * 24 : cost * 24 * 30;
  const periodLabel = period === 'hourly' ? '/hr' : period === 'daily' ? '/day' : '/mo';

  const getTrendIcon = () => {
    if (!showTrend || !trendDirection) return null;
    if (trendDirection === 'up') return '↑';
    if (trendDirection === 'down') return '↓';
    return '→';
  };

  const getTrendClass = () => {
    if (!trendDirection) return '';
    if (trendDirection === 'up') return 'trend-up';
    if (trendDirection === 'down') return 'trend-down';
    return 'trend-stable';
  };

  return (
    <span className={`cost-badge cost-badge-${size} ${compact ? 'cost-badge-compact' : ''} ${className}`}>
      <span className="cost-value">{formatCost(displayCost)}</span>
      {!compact && <span className="cost-period">{periodLabel}</span>}
      {showTrend && trendDirection && (
        <span className={`cost-trend ${getTrendClass()}`}>
          {getTrendIcon()}
          {trendPercent !== undefined && <span className="trend-percent">{trendPercent.toFixed(0)}%</span>}
        </span>
      )}
      {showEfficiency && efficiency !== undefined && (
        <span
          className="cost-efficiency"
          style={{ color: getEfficiencyColor(efficiency) }}
          title={`${efficiency.toFixed(0)}% efficiency`}
        >
          {efficiency.toFixed(0)}%
        </span>
      )}
    </span>
  );
};

interface EfficiencyBadgeProps {
  efficiency: number;
  size?: 'sm' | 'md' | 'lg';
  showLabel?: boolean;
  className?: string;
}

export const EfficiencyBadge: React.FC<EfficiencyBadgeProps> = ({
  efficiency,
  size = 'sm',
  className = '',
}) => {
  return (
    <span className={`efficiency-badge efficiency-badge-${size} ${className}`}>
      <span className="efficiency-value" style={{ color: getEfficiencyColor(efficiency) }}>
        {efficiency.toFixed(0)}%
      </span>
    </span>
  );
};

interface CostSparklineProps {
  datapoints: number[];
  width?: number;
  height?: number;
  className?: string;
}

export const CostSparkline: React.FC<CostSparklineProps> = ({
  datapoints,
  width = 60,
  height = 20,
  className = '',
}) => {
  if (!datapoints || datapoints.length < 2) return null;

  const min = Math.min(...datapoints);
  const max = Math.max(...datapoints);
  const range = max - min || 1;

  const points = datapoints.map((value, i) => {
    const x = (i / (datapoints.length - 1)) * width;
    const y = height - ((value - min) / range) * height;
    return `${x},${y}`;
  }).join(' ');

  const isUp = datapoints[datapoints.length - 1] > datapoints[0];
  const strokeColor = isUp ? 'var(--sparkline-up)' : 'var(--sparkline-down)';

  return (
    <svg
      className={`cost-sparkline ${className}`}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
    >
      <polyline
        fill="none"
        stroke={strokeColor}
        strokeWidth="1.5"
        points={points}
      />
    </svg>
  );
};

interface CostBreakdownBarProps {
  cpuCost: number;
  memoryCost: number;
  storageCost?: number;
  width?: number;
  height?: number;
  showLabels?: boolean;
  className?: string;
}

export const CostBreakdownBar: React.FC<CostBreakdownBarProps> = ({
  cpuCost,
  memoryCost,
  storageCost = 0,
  width = 80,
  height = 8,
  showLabels = false,
  className = '',
}) => {
  const total = cpuCost + memoryCost + storageCost;
  if (total === 0) return null;

  const cpuPercent = (cpuCost / total) * 100;
  const memPercent = (memoryCost / total) * 100;
  const storagePercent = (storageCost / total) * 100;

  return (
    <div className={`cost-breakdown-bar ${className}`} style={{ width }}>
      <div className="breakdown-bar" style={{ height }}>
        <div
          className="breakdown-segment cpu"
          style={{ width: `${cpuPercent}%`, backgroundColor: 'var(--chart-cpu)' }}
          title={`CPU: ${formatCost(cpuCost)}`}
        />
        <div
          className="breakdown-segment memory"
          style={{ width: `${memPercent}%`, backgroundColor: 'var(--chart-memory)' }}
          title={`Memory: ${formatCost(memoryCost)}`}
        />
        {storageCost > 0 && (
          <div
            className="breakdown-segment storage"
            style={{ width: `${storagePercent}%`, backgroundColor: 'var(--chart-storage)' }}
            title={`Storage: ${formatCost(storageCost)}`}
          />
        )}
      </div>
      {showLabels && (
        <div className="breakdown-labels">
          <span style={{ color: 'var(--chart-cpu)' }}>CPU</span>
          <span style={{ color: 'var(--chart-memory)' }}>Mem</span>
          {storageCost > 0 && <span style={{ color: 'var(--chart-storage)' }}>Stor</span>}
        </div>
      )}
    </div>
  );
};

interface PricingSourceBadgeProps {
  pricingInfo: PricingInfo;
  className?: string;
}

export const PricingSourceBadge: React.FC<PricingSourceBadgeProps> = ({
  pricingInfo,
  className = '',
}) => {
  const hasError = !!pricingInfo.error;
  const hasMissingPricing = pricingInfo.nodesMissingPrice > 0;
  const sourceLabel = getPricingSourceLabel(pricingInfo.source);
  const lastUpdated = formatTimeAgo(pricingInfo.lastUpdated);

  const getStatusClass = () => {
    if (hasError || !pricingInfo.isAvailable) return 'pricing-error';
    if (hasMissingPricing) return 'pricing-warning';
    return 'pricing-ok';
  };

  const getIcon = () => {
    if (hasError || !pricingInfo.isAvailable) return <ExclamationTriangleIcon />;
    if (hasMissingPricing) return <InfoCircledIcon />;
    return <CheckCircledIcon />;
  };

  const getTooltip = () => {
    const lines = [];
    lines.push(`Source: ${sourceLabel}`);
    lines.push(`Last updated: ${lastUpdated}`);
    lines.push(`Cached instances: ${pricingInfo.instanceCount}`);
    lines.push(`Nodes with pricing: ${pricingInfo.nodesWithPricing}/${pricingInfo.nodesWithPricing + pricingInfo.nodesMissingPrice}`);
    if (pricingInfo.error) {
      lines.push(`Error: ${pricingInfo.error}`);
    }
    return lines.join('\n');
  };

  return (
    <span
      className={`pricing-source-badge ${getStatusClass()} ${className}`}
      title={getTooltip()}
    >
      <span className="pricing-icon">{getIcon()}</span>
      <span className="pricing-source">{sourceLabel}</span>
      <span className="pricing-updated">{lastUpdated}</span>
      {hasMissingPricing && !hasError && (
        <span className="pricing-missing">
          {pricingInfo.nodesMissingPrice} nodes missing
        </span>
      )}
    </span>
  );
};

export default CostBadge;
