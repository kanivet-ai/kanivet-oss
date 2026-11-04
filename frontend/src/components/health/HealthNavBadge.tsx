import React from 'react';
import { useIssues } from './healthStore';
import { badgeFor } from './healthView';
import './Health.css';

/** The Issues sidebar row's live count: critical issues, else warnings. */
const HealthNavBadge: React.FC<{ cluster?: string }> = ({ cluster }) => {
  const { report } = useIssues(cluster);
  const b = badgeFor(report?.summary);
  if (!b) return null;
  return (
    <span className={`health-nav-badge health-nav-badge--${b.tone} ap-tnum`} aria-label={`${b.count} ${b.tone === 'danger' ? 'critical' : 'warning'} issues`}>
      {b.count > 99 ? '99+' : b.count}
    </span>
  );
};

export default HealthNavBadge;
