import React from 'react';
import { IncidentTimelineEntry } from '../../types/incidents';
import IncidentTimelineCard from './IncidentTimelineCard';

interface Props {
  entries: IncidentTimelineEntry[];
  onOpenResource?: (entry: IncidentTimelineEntry) => void;
}

const IncidentTimelineList: React.FC<Props> = ({ entries, onOpenResource }) => {
  if (!entries.length) {
    return <div className="incident-empty">No incidents match the current filters.</div>;
  }
  return (
    <div className="incident-list">
      {entries.map((e) => (
        <IncidentTimelineCard key={e.id} entry={e} onOpenResource={onOpenResource} />
      ))}
    </div>
  );
};

export default IncidentTimelineList;
