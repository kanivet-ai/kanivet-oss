import React from 'react';
import { CheckCircledIcon, CrossCircledIcon, ExclamationTriangleIcon, QuestionMarkCircledIcon } from '@radix-ui/react-icons';
import './StatusValue.css';

type StatusType = 'success' | 'warning' | 'danger' | 'neutral';

interface StatusValueProps {
  value: string | number | boolean;
  type?: StatusType | 'auto';
  showIcon?: boolean;
}

const SUCCESS_VALUES = ['running', 'succeeded', 'ready', 'true', 'active', 'bound', 'deployed', 'complete', 'completed', 'available', 'healthy'];
const DANGER_VALUES = ['failed', 'error', 'false', 'crashloopbackoff', 'oomkilled', 'imagepullbackoff', 'errimagepull', 'unhealthy'];
const WARNING_VALUES = ['pending', 'waiting', 'unknown', 'terminating', 'unschedulable', 'not ready', 'notready', 'progressing'];

const detectStatusType = (value: string | number | boolean): StatusType => {
  if (typeof value === 'boolean') return value ? 'success' : 'neutral';
  if (typeof value === 'number') {
    if (value > 0) return 'success';
    return 'neutral';
  }
  const str = String(value).toLowerCase().replace(/[^a-z]/g, '');
  if (SUCCESS_VALUES.some(s => str.includes(s))) return 'success';
  if (DANGER_VALUES.some(s => str.includes(s))) return 'danger';
  if (WARNING_VALUES.some(s => str.includes(s))) return 'warning';
  return 'neutral';
};

const StatusIcon: React.FC<{ type: StatusType }> = ({ type }) => {
  const iconProps = { className: 'status-value-icon' };
  switch (type) {
    case 'success': return <CheckCircledIcon {...iconProps} />;
    case 'danger': return <CrossCircledIcon {...iconProps} />;
    case 'warning': return <ExclamationTriangleIcon {...iconProps} />;
    default: return <QuestionMarkCircledIcon {...iconProps} />;
  }
};

export const StatusValue: React.FC<StatusValueProps> = ({
  value,
  type = 'auto',
  showIcon = true,
}) => {
  const resolvedType = type === 'auto' ? detectStatusType(value) : type;
  const displayValue = typeof value === 'boolean' ? (value ? 'True' : 'False') : String(value);

  return (
    <span className={`status-value status-value-${resolvedType}`}>
      {showIcon && <StatusIcon type={resolvedType} />}
      <span className="status-value-text">{displayValue}</span>
    </span>
  );
};

export default StatusValue;
