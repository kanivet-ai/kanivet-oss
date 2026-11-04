import React from 'react';
import { IncidentChangeMarker, IncidentTimelineEntry } from '../../types/incidents';
import IncidentRelatedChanges from '../changes/IncidentRelatedChanges';
import { formatAge } from '../../utils/formatters';

interface Props {
  entry: IncidentTimelineEntry;
  onOpenResource?: (entry: IncidentTimelineEntry) => void;
  related?: IncidentChangeMarker[];
  cluster?: string;
}

const IncidentTimelineCard: React.FC<Props> = ({ entry, onOpenResource, related, cluster }) => {
  const handleClick = () => onOpenResource?.(entry);
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onOpenResource?.(entry);
    }
  };
  return (
    <div
      className={`incident-card incident-${entry.severity}`}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      role="button"
      tabIndex={0}
    >
      <div className="incident-card-header">
        <span className={`incident-severity-badge severity-${entry.severity}`}>{entry.severity}</span>
        <span className="incident-reason">{entry.reason}</span>
        {entry.count > 1 && <span className="incident-count">×{entry.count}</span>}
        <span className="incident-time">{formatAge(entry.lastTimestamp)}</span>
      </div>
      <div className="incident-target">
        <span className="incident-kind">{entry.kind}</span>
        {entry.namespace && <span className="incident-ns">{entry.namespace}/</span>}
        <span className="incident-name">{entry.name}</span>
      </div>
      <div className="incident-message">{entry.message}</div>
      {related && related.length > 0 && cluster && <IncidentRelatedChanges changes={related} cluster={cluster} />}
    </div>
  );
};

export default IncidentTimelineCard;
