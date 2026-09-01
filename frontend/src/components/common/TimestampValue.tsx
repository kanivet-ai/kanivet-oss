import React from 'react';
import { ClockIcon } from '@radix-ui/react-icons';
import { Tooltip } from './Tooltip';
import { formatAge } from '../../utils/formatters';
import './TimestampValue.css';

interface TimestampValueProps {
  timestamp: string;
  showIcon?: boolean;
  suffix?: string;
}

const formatFullDate = (timestamp: string): string => {
  if (!timestamp) return '';
  const date = new Date(timestamp);
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
};

export const TimestampValue: React.FC<TimestampValueProps> = ({
  timestamp,
  showIcon = true,
  suffix,
}) => {
  if (!timestamp) return <span className="timestamp-value">-</span>;

  const relativeTime = formatAge(timestamp);
  const fullDate = formatFullDate(timestamp);
  const displayText = suffix ? `${relativeTime} ${suffix}` : relativeTime;

  return (
    <Tooltip content={fullDate}>
      <span className="timestamp-value">
        {showIcon && <ClockIcon className="timestamp-icon" />}
        <span className="timestamp-text">{displayText}</span>
      </span>
    </Tooltip>
  );
};

export default TimestampValue;
