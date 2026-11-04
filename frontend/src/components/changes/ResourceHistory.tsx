import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { getResourceHistory } from '../../services/api/changes';
import type { ChangeEntry, ChangeHistory } from '../../types/changes';
import { SOURCE_LABELS, defaultBaseline, filterHistory, groupByDay, type HistoryView } from '../../utils/changes';
import ChangeEntryRow from './ChangeEntryRow';
import RevisionDiff from './RevisionDiff';
import './Changes.css';

interface Props {
  resource: any;
  cluster: string;
}

const VIEWS: Array<{ id: HistoryView; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'revisions', label: 'Revisions' },
  { id: 'edits', label: 'Edits' },
  { id: 'events', label: 'Events' },
];

const ResourceHistory: React.FC<Props> = ({ resource, cluster }) => {
  const apiVersion: string = resource?.apiVersion || 'v1';
  const [group, version] = apiVersion.includes('/') ? apiVersion.split('/') : ['', apiVersion];
  const kind = resource?.kind || '';
  const name = resource?.metadata?.name || resource?.name || '';
  const namespace = resource?.metadata?.namespace || resource?.namespace || '';
  const uid = resource?.metadata?.uid || '';
  // Only a new rollout or edit can change the history; the resourceVersion
  // moves on every status write, so it is not used as a reload key.
  const generation = resource?.metadata?.generation;

  const [data, setData] = useState<ChangeHistory | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [view, setView] = useState<HistoryView>('all');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [baseId, setBaseId] = useState<string | null | undefined>(undefined);

  const load = useCallback(async () => {
    if (!cluster || !kind || !name) return;
    setLoading(true);
    setError(null);
    try {
      setData(await getResourceHistory({ cluster, group, version, kind, namespace, name }));
    } catch (e: any) {
      setError(e?.message || 'Could not load history');
    } finally {
      setLoading(false);
    }
  }, [cluster, group, version, kind, namespace, name]);

  useEffect(() => { void load(); }, [load, uid, generation]);

  const entries = useMemo(() => filterHistory(data?.entries || [], view), [data, view]);
  const all = data?.entries || [];
  const selected = all.find((e) => e.id === selectedId) || null;
  const snapshots = all.filter((e) => e.hasSnapshot);
  const base: ChangeEntry | null = selected
    ? baseId === undefined ? defaultBaseline(all, selected) : all.find((e) => e.id === baseId) || null
    : null;

  const select = (e: ChangeEntry) => {
    if (!e.hasSnapshot) { setSelectedId(null); return; }
    setSelectedId(e.id === selectedId ? null : e.id);
    setBaseId(undefined);
  };

  if (error) {
    return (
      <div className="chg-empty">
        <div>{error}</div>
        <button type="button" className="ap-btn" onClick={() => { void load(); }}>Try again</button>
      </div>
    );
  }
  if (!data && loading) return <div className="chg-empty">Reading history from the cluster…</div>;
  if (!data) return null;

  return (
    <div className="chg-history">
      <div className="chg-history-bar">
        <div className="ap-segmented ap-segmented--sm" role="tablist" aria-label="History filter">
          {VIEWS.map((v) => (
            <button key={v.id} type="button" role="tab" aria-selected={view === v.id} onClick={() => setView(v.id)}>{v.label}</button>
          ))}
        </div>
        <span className="chg-sources" title="Where this history was read from">
          {data.sources.map((s) => SOURCE_LABELS[s] || s).join(' · ')}
        </span>
        <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={() => { void load(); }} disabled={loading}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {data.warnings && data.warnings.length > 0 && (
        <div className="chg-warning" role="status">Partial history: {data.warnings.join('; ')}</div>
      )}
      {selected && (
        <RevisionDiff
          cluster={cluster}
          base={base}
          target={selected}
          candidates={snapshots}
          onBaseChange={(id) => setBaseId(id)}
        />
      )}
      {entries.length === 0 ? (
        <div className="chg-empty">
          {view === 'all' ? 'The cluster keeps no history for this resource.' : 'Nothing of this kind in the history.'}
        </div>
      ) : (
        <div className="chg-timeline">
          {groupByDay(entries).map((g) => (
            <div key={g.label} className="chg-day">
              <div className="ap-group-label chg-day-label">{g.label}</div>
              {g.entries.map((e) => (
                <ChangeEntryRow
                  key={e.id}
                  entry={e}
                  cluster={cluster}
                  selected={e.id === selectedId}
                  baseline={!!base && e.id === base.id}
                  onSelect={e.hasSnapshot ? select : undefined}
                />
              ))}
            </div>
          ))}
        </div>
      )}
      {snapshots.length > 0 && !selected && (
        <div className="chg-hint">Select a revision to diff it against the one before, or pick any two.</div>
      )}
    </div>
  );
};

export default ResourceHistory;
