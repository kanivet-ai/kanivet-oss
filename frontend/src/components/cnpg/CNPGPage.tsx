import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getCNPGClusters, CNPGClusterSummary } from '../../services/api/integrations';
import { useVisibleInterval } from '../../hooks/useVisibleInterval';
import LiveAge from '../common/LiveAge';
import { Pivot, StatusBadge } from '../gitops/statusParts';
import { openInspector } from '../gitops/openInspector';
import { useConfirmedAction } from '../gitops/useConfirmedAction';
import { backupSpec } from './cnpgActions';
import { backupMethodLabel, cnpgHealthTone } from './cnpgFormat';
import '../gitops/GitOpsPage.css';
import './CNPG.css';

const HEALTHS = ['Healthy', 'Degraded', 'Progressing', 'Failing'] as const;
const DAY = 24 * 3600 * 1000;

/** Cluster-level overview of every CloudNativePG Cluster. */
const CNPGPage = ({ cluster }: { cluster: string }) => {
  const [items, setItems] = useState<CNPGClusterSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [health, setHealth] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const load = useCallback(async () => {
    try {
      setItems(await getCNPGClusters(cluster));
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to load PostgreSQL clusters');
    }
  }, [cluster]);
  useEffect(() => { setItems(null); load(); }, [load]);
  useVisibleInterval(load, 10000, { targetRef: scrollRef });
  const { ask, dialog } = useConfirmedAction(load);

  const all = items || [];
  const counts = useMemo(() => {
    const m: Record<string, number> = {};
    all.forEach((c) => { m[c.health] = (m[c.health] || 0) + 1; });
    return m;
  }, [all]);
  const staleBackups = all.filter((c) => c.backupMethod && (!c.lastSuccessfulBackup || Date.now() - Date.parse(c.lastSuccessfulBackup) > DAY)).length;
  const q = search.trim().toLowerCase();
  const rows = all.filter((c) => (!health || c.health === health) && (!q || `${c.namespace}/${c.name}`.toLowerCase().includes(q)));
  const open = (c: CNPGClusterSummary, pinned = false) =>
    openInspector(cluster, { apiVersion: 'postgresql.cnpg.io/v1', kind: 'Cluster', namespace: c.namespace, name: c.name }, pinned);

  return (
    <div className="argo-apps-page gitops-page">
      <div className="gitops-header">
        <div className="gitops-title">
          <span className="gitops-title-text">PostgreSQL clusters</span>
          <span className="gitops-subtitle">CloudNativePG{items ? ` · ${all.length} cluster${all.length === 1 ? '' : 's'}` : ''}</span>
        </div>
      </div>
      <div className="argo-apps-pivots">
        <Pivot value={all.length} label="Total" active={!health} onClick={() => setHealth(null)} />
        {HEALTHS.map((h) => (
          <Pivot key={h} value={counts[h] || 0} label={h} tone={cnpgHealthTone(h)} active={health === h} onClick={() => setHealth(health === h ? null : h)} />
        ))}
        <span className="argo-apps-pivot-sep" />
        <Pivot value={staleBackups} label="No backup in 24h" tone={staleBackups ? 'warning' : undefined} active={false} />
      </div>
      <div className="argo-apps-toolbar">
        <label className="ap-search argo-apps-search">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" />
          </svg>
          <input placeholder="Search clusters" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Search PostgreSQL clusters" />
        </label>
      </div>
      {error && <div className="argo-apps-error">{error} <button className="ap-btn ap-btn--sm" onClick={load}>Retry</button></div>}
      <div className="argo-apps-table-wrap" ref={scrollRef}>
        <table className="argo-apps-table gitops-table">
          <thead>
            <tr>
              <th className="is-static">Cluster</th>
              <th className="is-static argo-apps-col-status">Health</th>
              <th className="is-static">Instances</th>
              <th className="is-static">Primary</th>
              <th className="is-static">Timeline</th>
              <th className="is-static">Backup</th>
              <th className="is-static argo-apps-col-time">Last backup</th>
              <th className="is-static gitops-col-actions" aria-label="Actions" />
            </tr>
          </thead>
          <tbody>
            {!items && !error ? (
              Array.from({ length: 5 }).map((_, i) => <tr key={i} className="argo-apps-skeleton-row"><td colSpan={8}><span className="argo-apps-skeleton" /></td></tr>)
            ) : rows.length === 0 ? (
              <tr><td colSpan={8} className="argo-apps-empty">{all.length ? 'Nothing matches the current filters.' : 'No CloudNativePG clusters on this cluster.'}</td></tr>
            ) : rows.map((c) => (
              <tr key={`${c.namespace}/${c.name}`} onDoubleClick={() => open(c, true)}>
                <td className="argo-apps-name-cell">
                  <button type="button" className="gitops-name-link argo-apps-name" onClick={() => open(c)}>{c.name}</button>
                  <span className="argo-apps-ns gitops-ns">{c.namespace}</span>
                </td>
                <td><StatusBadge tone={cnpgHealthTone(c.health)} label={c.health} title={c.phase} /></td>
                <td className="cnpg-num">{c.readyInstances}/{c.instances}</td>
                <td className="argo-apps-mono">{c.currentPrimary || '–'}{c.targetPrimary && c.targetPrimary !== c.currentPrimary ? ` → ${c.targetPrimary}` : ''}</td>
                <td className="cnpg-num">{c.timeline ?? '–'}</td>
                <td className="gitops-detail">{backupMethodLabel(c.backupMethod, c.backupPlugin)}</td>
                <td className="argo-apps-time">{c.lastSuccessfulBackup ? <LiveAge timestamp={c.lastSuccessfulBackup} /> : <span className="argo-apps-empty-value">never</span>}</td>
                <td className="gitops-actions">
                  <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={!c.backupMethod} title={c.backupMethod ? 'Back up now' : 'No backup method configured'} onClick={() => ask(backupSpec(cluster, c))}>Back up</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {dialog}
    </div>
  );
};

export default CNPGPage;
