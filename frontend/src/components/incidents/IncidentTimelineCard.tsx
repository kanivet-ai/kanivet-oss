import React from 'react';
import { IncidentTimelineEntry } from '../../types/incidents';
import { formatAge } from '../../utils/formatters';
import { useStore } from '../../store';
import InvestigateButton from '../ai/InvestigateButton';

interface Props {
  entry: IncidentTimelineEntry;
  onOpenResource?: (entry: IncidentTimelineEntry) => void;
}

const IncidentTimelineCard: React.FC<Props> = ({ entry, onOpenResource }) => {
  const cluster = useStore((s) => s.currentTab) || '';
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
        {cluster && (
          <InvestigateButton
            variant="inline"
            target={{
              cluster,
              kind: entry.kind,
              apiVersion: entry.apiVersion,
              namespace: entry.namespace,
              name: entry.name,
              context: `Incident (${entry.severity}): ${entry.reason} x${entry.count}, last ${entry.lastTimestamp}: ${entry.message}`,
              question: `Explain this ${entry.severity} incident (${entry.reason}) on the resource, its root cause and the fix.`,
            }}
          />
        )}
      </div>
      <div className="incident-target">
        <span className="incident-kind">{entry.kind}</span>
        {entry.namespace && <span className="incident-ns">{entry.namespace}/</span>}
        <span className="incident-name">{entry.name}</span>
      </div>
      <div className="incident-message">{entry.message}</div>
    </div>
  );
};

export default IncidentTimelineCard;
