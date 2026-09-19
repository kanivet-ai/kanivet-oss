import React from 'react';
import { IncidentSeverity, IncidentTimelineFilters } from '../../types/incidents';

interface Props {
  filters: IncidentTimelineFilters;
  namespaces: string[];
  kinds: string[];
  activeRangeMs: number | null;
  onChange: (patch: Partial<IncidentTimelineFilters>) => void;
  onRangeChange: (ms: number) => void;
  onRefresh: () => void;
}

const SEVERITIES: IncidentSeverity[] = ['critical', 'warning', 'info'];
const RANGES: { label: string; ms: number }[] = [
  { label: '15m', ms: 15 * 60 * 1000 },
  { label: '1h', ms: 60 * 60 * 1000 },
  { label: '6h', ms: 6 * 60 * 60 * 1000 },
  { label: '24h', ms: 24 * 60 * 60 * 1000 },
  { label: 'All', ms: 0 },
];

const IncidentTimelineToolbar: React.FC<Props> = ({ filters, namespaces, kinds, activeRangeMs, onChange, onRangeChange, onRefresh }) => {
  const isActive = (s: IncidentSeverity) => (filters.severities || []).includes(s);
  const toggleSeverity = (s: IncidentSeverity) => {
    const current = filters.severities || [];
    onChange({ severities: isActive(s) ? current.filter((x) => x !== s) : [...current, s] });
  };
  return (
    <div className="incident-toolbar">
      <div className="toolbar-group ap-segmented">
        {SEVERITIES.map((s) => (
          <button
            key={s}
            className={`severity-toggle severity-${s} ${isActive(s) ? 'active' : ''}`}
            onClick={() => toggleSeverity(s)}
            type="button"
          >
            {s}
          </button>
        ))}
      </div>
      <div className="toolbar-group ap-segmented">
        {RANGES.map((r) => (
          <button
            key={r.label}
            className={`range-toggle ${activeRangeMs === r.ms ? 'active' : ''}`}
            onClick={() => onRangeChange(r.ms)}
            type="button"
          >
            {r.label}
          </button>
        ))}
      </div>
      <select
        className="ap-select"
        value={(filters.namespaces || [])[0] || ''}
        onChange={(e) => onChange({ namespaces: e.target.value ? [e.target.value] : [] })}
      >
        <option value="">All namespaces</option>
        {namespaces.map((n) => (
          <option key={n} value={n}>{n}</option>
        ))}
      </select>
      <select
        className="ap-select"
        value={(filters.kinds || [])[0] || ''}
        onChange={(e) => onChange({ kinds: e.target.value ? [e.target.value] : [] })}
      >
        <option value="">All kinds</option>
        {kinds.map((k) => (
          <option key={k} value={k}>{k}</option>
        ))}
      </select>
      <input
        type="text"
        className="ap-input"
        placeholder="Search..."
        value={filters.search || ''}
        onChange={(e) => onChange({ search: e.target.value })}
      />
      <label className="include-routine">
        <input
          type="checkbox"
          checked={!!filters.includeRoutine}
          onChange={(e) => onChange({ includeRoutine: e.target.checked })}
        />
        Include routine
      </label>
      <button className="refresh-btn ap-btn" onClick={onRefresh} type="button">Refresh</button>
    </div>
  );
};

export default IncidentTimelineToolbar;
