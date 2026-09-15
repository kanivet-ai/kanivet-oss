import React from 'react';
import {
  AWSAccessDecision,
  AWSIdentityMechanism,
  AWSIdentityStatus,
  AWSPolicyType,
} from '../../types/awsIdentity';
import {
  decisionLabel,
  decisionStatus,
  mechanismLabel,
  statusLabel,
} from './awsIdentityUtils';
import './awsIdentity.css';

interface StatusPillProps {
  status: AWSIdentityStatus | 'pending';
  label?: string;
  title?: string;
  className?: string;
}

export const AWSStatusPill: React.FC<StatusPillProps> = ({
  status,
  label,
  title,
  className = '',
}) => (
  <span className={`awsid-pill status-${status} ${className}`} title={title}>
    {status === 'pending' ? (
      <span className="awsid-spinner" />
    ) : (
      <span className="awsid-pill-dot" />
    )}
    {label ?? (status === 'pending' ? 'Running' : statusLabel(status))}
  </span>
);

export const AWSDecisionPill: React.FC<{
  decision: AWSAccessDecision;
  title?: string;
}> = ({ decision, title }) => (
  <AWSStatusPill
    status={decisionStatus(decision)}
    label={decisionLabel(decision)}
    title={title}
  />
);

export const AWSMechanismBadge: React.FC<{
  mechanism: AWSIdentityMechanism;
  className?: string;
}> = ({ mechanism, className = '' }) => (
  <span className={`awsid-badge mechanism-${mechanism} ${className}`}>
    {mechanismLabel(mechanism)}
  </span>
);

export const AWSPolicyTypeBadge: React.FC<{
  type: AWSPolicyType;
  awsManaged?: boolean;
}> = ({ type, awsManaged }) => (
  <span className={`awsid-badge type-${type}`}>
    {type === 'managed'
      ? awsManaged
        ? 'AWS managed'
        : 'Customer managed'
      : type === 'inline'
        ? 'Inline'
        : 'Permissions boundary'}
  </span>
);
