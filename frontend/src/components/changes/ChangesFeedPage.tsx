import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { getChangeFeed } from '../../services/api/changes';
import type { ChangeCategory, ChangeFeed, ChangeSource } from '../../types/changes';
import { CATEGORY_LABELS, SOURCE_LABELS, groupByDay } from '../../utils/changes';
import ChangeEntryRow from './ChangeEntryRow';
import './Changes.css';

interface Props {
  cluster: string;
}

const RANGES = [
  { label: '1h', ms: 3600e3 },
  { label: '6h', ms: 6 * 3600e3 },
  { label: '24h', ms: 24 * 3600e3 },
  { label: '7d', ms: 7 * 24 * 3600e3 },
  { label: '14d', ms: 14 * 24 * 3600e3 },
];
const TYPES: ChangeCategory[] = ['rollout', 'image', 'env', 'config', 'resources', 'restart', 'sync', 'created'];
const SOURCES: ChangeSource[] = ['replicaset', 'controllerrevision', 'helm', 'argocd', 'flux'];

const ChangesFeedPage: React.FC<Props> = ({ cluster }) => {
  const [rangeMs, setRangeMs] = useState(24 * 3600e3);
  const [namespace, setNamespace] = useState('');
  const [kind, setKind] = useState('');
  const [category, setCategory] = useState<ChangeCategory | ''>('');
  const [source, setSource] = useState<ChangeSource | ''>('');
  const [data, setData] = useState<ChangeFeed | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!cluster) return;
    setLoading(true);
    setError(null);
    try {
      // The feed is filtered by namespace/kind client side so the pickers
      // keep listing every option of the range.
      setData(await getChangeFeed(cluster, {
        since: new Date(Date.now() - rangeMs),
        categories: category ? [category] : undefined,
        sources: source ? [source] : undefined,
        limit: 1000,
      }));
    } catch (e: any) {
      setError(e?.message || 'Could not load changes');
    } finally {
      setLoading(false);
    }
  }, [cluster, rangeMs, category, source]);

  useEffect(() => {
    void load();
    const id = setInterval(() => { void load(); }, 60000);
    return () => clearInterval(id);
  }, [load]);

  const all = data?.entries || [];
  const namespaces = useMemo(() => Array.from(new Set(all.map((e) => e.subject.namespace).filter(Boolean))).sort(), [all]);
  const kinds = useMemo(() => Array.from(new Set(all.map((e) => e.subject.kind))).sort(), [all]);
  const entries = all.filter((e) => (!namespace || e.subject.namespace === namespace) && (!kind || e.subject.kind === kind));

  return (
    <div className="chg-page">
      <div className="chg-toolbar">
        <div className="chg-toolbar-title">
          <span className="chg-page-title">Changes</span>
          <span className="chg-page-sub">Rollouts, Helm upgrades and GitOps syncs, read from the cluster</span>
        </div>
        <div className="ap-segmented" role="group" aria-label="Time range">
          {RANGES.map((r) => (
            <button key={r.label} type="button" aria-pressed={rangeMs === r.ms} onClick={() => setRangeMs(r.ms)}>{r.label}</button>
          ))}
        </div>
        <select className="ap-select" value={namespace} onChange={(e) => setNamespace(e.target.value)} aria-label="Namespace">
          <option value="">All namespaces</option>
          {namespaces.map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <select className="ap-select" value={kind} onChange={(e) => setKind(e.target.value)} aria-label="Kind">
          <option value="">All kinds</option>
          {kinds.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <select className="ap-select" value={category} onChange={(e) => setCategory(e.target.value as ChangeCategory | '')} aria-label="Change type">
          <option value="">All change types</option>
          {TYPES.map((t) => <option key={t} value={t}>{CATEGORY_LABELS[t]}</option>)}
        </select>
        <select className="ap-select" value={source} onChange={(e) => setSource(e.target.value as ChangeSource | '')} aria-label="Source">
          <option value="">All sources</option>
          {SOURCES.map((s) => <option key={s} value={s}>{SOURCE_LABELS[s]}</option>)}
        </select>
        <button type="button" className="ap-btn" onClick={() => { void load(); }} disabled={loading}>Refresh</button>
      </div>
      {data?.warnings && data.warnings.length > 0 && (
        <div className="chg-warning" role="status">Some sources could not be read: {data.warnings.join('; ')}</div>
      )}
      <div className="chg-page-body">
        {error ? (
          <div className="chg-empty">
            <div>{error}</div>
            <button type="button" className="ap-btn" onClick={() => { void load(); }}>Try again</button>
          </div>
        ) : !data && loading ? (
          <div className="chg-empty">Reading rollouts and releases…</div>
        ) : entries.length === 0 ? (
          <div className="chg-empty">No changes in this range.</div>
        ) : (
          <div className="chg-timeline chg-timeline--feed">
            {groupByDay(entries).map((g) => (
              <div key={g.label} className="chg-day">
                <div className="ap-group-label chg-day-label">{g.label}</div>
                {g.entries.map((e) => <ChangeEntryRow key={`${e.subject.namespace}/${e.subject.name}/${e.id}`} entry={e} cluster={cluster} showSubject />)}
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="ap-statusbar">
        {entries.length} change{entries.length === 1 ? '' : 's'}{loading ? ' · refreshing' : ''}
      </div>
    </div>
  );
};

export default ChangesFeedPage;
