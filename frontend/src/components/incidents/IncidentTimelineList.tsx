import React from 'react';
import { IncidentChangeMarker, IncidentTimelineEntry } from '../../types/incidents';
import IncidentTimelineCard from './IncidentTimelineCard';

interface Props {
  entries: IncidentTimelineEntry[];
  onOpenResource?: (entry: IncidentTimelineEntry) => void;
  changes?: IncidentChangeMarker[];
  cluster?: string;
}

const IncidentTimelineList: React.FC<Props> = ({ entries, onOpenResource, changes, cluster }) => {
  const byId = React.useMemo(() => new Map((changes || []).map((c) => [c.id, c])), [changes]);
  if (!entries.length) {
    return <div className="incident-empty">No incidents match the current filters.</div>;
  }
  return (
    <div className="incident-list">
      {entries.map((e) => (
        <IncidentTimelineCard
          key={e.id}
          entry={e}
          onOpenResource={onOpenResource}
          related={(e.relatedChanges || []).map((id) => byId.get(id)).filter((c): c is IncidentChangeMarker => !!c)}
          cluster={cluster}
        />
      ))}
    </div>
  );
};

export default IncidentTimelineList;
