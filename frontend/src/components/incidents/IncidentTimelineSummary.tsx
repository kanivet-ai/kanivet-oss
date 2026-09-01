import React from 'react';
import { IncidentTimelineSummary as Summary } from '../../types/incidents';

interface Props {
  summary: Summary;
}

const IncidentTimelineSummary: React.FC<Props> = ({ summary }) => (
  <div className="incident-summary">
    <div className="summary-stat stat-critical">
      <span className="stat-value">{summary.critical}</span>
      <span className="stat-label">Critical</span>
    </div>
    <div className="summary-stat stat-warning">
      <span className="stat-value">{summary.warning}</span>
      <span className="stat-label">Warning</span>
    </div>
    <div className="summary-stat stat-info">
      <span className="stat-value">{summary.info}</span>
      <span className="stat-label">Info</span>
    </div>
    <div className="summary-stat stat-routine">
      <span className="stat-value">{summary.routine}</span>
      <span className="stat-label">Routine</span>
    </div>
    <div className="summary-stat stat-total">
      <span className="stat-value">{summary.total}</span>
      <span className="stat-label">Total events</span>
    </div>
  </div>
);

export default IncidentTimelineSummary;
