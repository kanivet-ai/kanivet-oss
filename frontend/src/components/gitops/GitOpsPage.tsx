import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getGitOpsStatus, GitOpsItem, GitOpsStatus } from '../../services/api/integrations';
import { useVisibleInterval } from '../../hooks/useVisibleInterval';
import { lazyView } from '../../utils/lazyView';
import LiveAge from '../common/LiveAge';
import ArgoIcon from '../icons/ArgoIcon';
import FluxIcon from '../icons/FluxIcon';
import { Pivot, StatusBadge, healthTone, shortRevision } from './statusParts';
import { filterGitOps, countByHealth, GitOpsFilter, KindFilter, ToolFilter, isSourceKind } from './gitopsFilter';
import { openInspector } from './openInspector';
import { useConfirmedAction } from './useConfirmedAction';
import { reconcileSpec, suspendSpec } from './fluxActions';
import './GitOpsPage.css';

// The Argo CD segment is the existing applications page, not a copy of it.
const ArgoApplicationsPage = lazyView(() => import('../ArgoApplicationsPage'));

interface Props {
  cluster: string;
  /** Segment to open on ("argocd" when reached from the Argo sidebar entry). */
  initialTool?: ToolFilter;
}

const HEALTHS = ['Healthy', 'Failing', 'Progressing', 'Suspended'] as const;

const storageKey = (cluster: string) => `kanivet.gitopsPage.${cluster}`;
const loadFilter = (cluster: string, initialTool?: ToolFilter): GitOpsFilter => {
  const base: GitOpsFilter = { tool: initialTool || 'all', kind: 'all', health: null, search: '' };
  try {
    const raw = localStorage.getItem(storageKey(cluster));
    if (raw) return { ...base, ...JSON.parse(raw), ...(initialTool ? { tool: initialTool } : {}) };
  } catch {}
  return base;
};

const GitOpsPage = ({ cluster, initialTool }: Props) => {
  const [status, setStatus] = useState<GitOpsStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<GitOpsFilter>(() => loadFilter(cluster, initialTool));
  const [focused, setFocused] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    try { localStorage.setItem(storageKey(cluster), JSON.stringify(filter)); } catch {}
  }, [cluster, filter]);

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      setStatus(await getGitOpsStatus(cluster));
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to load GitOps status');
    } finally {
      if (!silent) setLoading(false);
    }
  }, [cluster]);

  useEffect(() => { setStatus(null); load(); }, [load]);
  useVisibleInterval(() => load(true), 8000, { targetRef: scrollRef, enabled: filter.tool !== 'argocd' });

  const { ask, dialog } = useConfirmedAction(() => load(true));
  const update = (patch: Partial<GitOpsFilter>) => setFilter((f) => ({ ...f, ...patch }));

  const items = status?.items || [];
  const rows = useMemo(() => filterGitOps(items, filter), [items, filter]);
  const counts = useMemo(() => countByHealth(items, filter), [items, filter]);
  const scopeTotal = Object.values(counts).reduce((a, b) => a + b, 0);
  const argoCount = items.filter((i) => i.tool === 'argocd').length;
  const fluxCount = items.length - argoCount;
  const key = (it: GitOpsItem) => `${it.tool}/${it.kind}/${it.namespace}/${it.name}`;

  const open = useCallback((it: GitOpsItem, pinned = false) => {
    openInspector(cluster, { apiVersion: it.apiVersion, kind: it.kind, namespace: it.namespace, name: it.name }, pinned);
  }, [cluster]);

  // "/" focuses search, ↑/↓ move, Enter opens, R reconciles the focused Flux row.
  useEffect(() => {
    if (filter.tool === 'argocd') return;
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
      if (!scrollRef.current?.offsetParent) return; // page hidden in a background tab
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const idx = rows.findIndex((r) => key(r) === focused);
      if (e.key === '/') { e.preventDefault(); searchRef.current?.focus(); }
      else if (e.key === 'ArrowDown' && rows.length) { e.preventDefault(); setFocused(key(rows[Math.min(idx + 1, rows.length - 1)])); }
      else if (e.key === 'ArrowUp' && rows.length) { e.preventDefault(); setFocused(key(rows[Math.max(idx - 1, 0)])); }
      else if (e.key === 'Enter' && idx >= 0) { e.preventDefault(); open(rows[idx]); }
      else if (e.key.toLowerCase() === 'r' && idx >= 0 && rows[idx].tool === 'flux' && !rows[idx].suspended) {
        e.preventDefault();
        const r = rows[idx];
        ask(reconcileSpec(cluster, { kind: r.kind, namespace: r.namespace, name: r.name, hasSource: !isSourceKind(r.kind) }));
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [rows, focused, filter.tool, open, ask, cluster]);

  const neither = status && !status.argoInstalled && !status.fluxInstalled;
  const kindOptions: { id: KindFilter; label: string }[] = filter.tool === 'argocd'
    ? []
    : [
        { id: 'all', label: 'All kinds' },
        ...(filter.tool !== 'flux' && status?.argoInstalled ? [{ id: 'Application' as KindFilter, label: 'Applications' }] : []),
        ...(status?.fluxInstalled ? [
          { id: 'Kustomization' as KindFilter, label: 'Kustomizations' },
          { id: 'HelmRelease' as KindFilter, label: 'Helm releases' },
          { id: 'sources' as KindFilter, label: 'Sources' },
        ] : []),
      ];

  return (
    <div className="argo-apps-page gitops-page">
      <div className="gitops-header">
        <div className="gitops-title">
          <span className="gitops-title-text">GitOps</span>
          <span className="gitops-subtitle">
            {status ? [status.argoInstalled && `${argoCount} Argo CD`, status.fluxInstalled && `${fluxCount} Flux`].filter(Boolean).join(' · ') || 'No GitOps controllers' : 'Loading'}
          </span>
        </div>
        <div className="ap-segmented" role="tablist" aria-label="GitOps tool">
          <button type="button" aria-pressed={filter.tool === 'all'} onClick={() => update({ tool: 'all', kind: 'all' })}>All</button>
          {status?.argoInstalled !== false && (
            <button type="button" aria-pressed={filter.tool === 'argocd'} onClick={() => update({ tool: 'argocd', kind: 'all' })}><ArgoIcon width={13} height={13} />Argo CD</button>
          )}
          {status?.fluxInstalled !== false && (
            <button type="button" aria-pressed={filter.tool === 'flux'} onClick={() => update({ tool: 'flux', kind: filter.kind === 'Application' ? 'all' : filter.kind })}><FluxIcon width={13} height={13} />Flux</button>
          )}
        </div>
      </div>

      {filter.tool === 'argocd' ? (
        <div className="gitops-embedded"><ArgoApplicationsPage cluster={cluster} /></div>
      ) : (
        <>
          <div className="argo-apps-pivots">
            <Pivot value={scopeTotal} label="Total" active={!filter.health} onClick={() => update({ health: null })} />
            {HEALTHS.map((h) => (
              <Pivot key={h} value={counts[h] || 0} label={h} tone={healthTone(h)} active={filter.health === h} onClick={() => update({ health: filter.health === h ? null : h })} />
            ))}
          </div>

          <div className="argo-apps-toolbar">
            <label className="ap-search argo-apps-search">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
                <circle cx="7" cy="7" r="4.5" />
                <path d="M10.5 10.5L14 14" />
              </svg>
              <input ref={searchRef} placeholder="Search by name, namespace, source or revision" value={filter.search} onChange={(e) => update({ search: e.target.value })} aria-label="Search GitOps objects" />
              <kbd aria-hidden="true">/</kbd>
            </label>
            {kindOptions.length > 2 && (
              <div className="ap-segmented ap-segmented--sm" aria-label="Kind">
                {kindOptions.map((k) => (
                  <button key={k.id} type="button" aria-pressed={filter.kind === k.id} onClick={() => update({ kind: k.id })}>{k.label}</button>
                ))}
              </div>
            )}
          </div>

          {error && <div className="argo-apps-error">{error} <button className="ap-btn ap-btn--sm" onClick={() => load()}>Retry</button></div>}
          {status?.errors?.length ? <div className="argo-apps-stale-banner"><span className="argo-apps-stale-dot" /><span>Partial data: {status.errors.join('; ')}</span></div> : null}

          {neither ? (
            <div className="gitops-empty">
              <p>Neither Argo CD nor Flux is installed on this cluster.</p>
              <p className="gitops-empty-hint">Kanivet shows this page when the Argo CD Application or Flux toolkit CRDs exist.</p>
            </div>
          ) : (
            <div className="argo-apps-table-wrap" ref={scrollRef}>
              <table className="argo-apps-table gitops-table">
                <thead>
                  <tr>
                    <th className="is-static">Name</th>
                    <th className="is-static gitops-col-kind">Kind</th>
                    <th className="is-static argo-apps-col-status">Health</th>
                    <th className="is-static gitops-col-status">Status</th>
                    <th className="is-static argo-apps-col-rev">Revision</th>
                    <th className="is-static gitops-col-source">Source</th>
                    <th className="is-static argo-apps-col-time">Updated</th>
                    <th className="is-static gitops-col-actions" aria-label="Actions" />
                  </tr>
                </thead>
                <tbody>
                  {loading && !status ? (
                    Array.from({ length: 8 }).map((_, i) => (
                      <tr key={`sk-${i}`} className="argo-apps-skeleton-row"><td colSpan={8}><span className="argo-apps-skeleton" /></td></tr>
                    ))
                  ) : rows.length === 0 ? (
                    <tr><td colSpan={8} className="argo-apps-empty">{items.length ? 'Nothing matches the current filters.' : 'No GitOps objects yet.'}</td></tr>
                  ) : rows.map((it) => {
                    const k = key(it);
                    return (
                      <tr key={k} className={focused === k ? 'focused' : ''} onClick={() => setFocused(k)} onDoubleClick={() => open(it, true)}>
                        <td className="argo-apps-name-cell">
                          <span className="gitops-tool" title={it.tool === 'flux' ? 'Flux' : 'Argo CD'}>{it.tool === 'flux' ? <FluxIcon width={13} height={13} /> : <ArgoIcon width={13} height={13} />}</span>
                          <button type="button" className="gitops-name-link argo-apps-name" onClick={(e) => { e.stopPropagation(); setFocused(k); open(it); }}>{it.name}</button>
                          <span className="argo-apps-ns gitops-ns">{it.namespace}</span>
                        </td>
                        <td className="gitops-kind">{it.kind}</td>
                        <td><StatusBadge tone={healthTone(it.health)} label={it.health} /></td>
                        <td className="gitops-detail" title={it.message || it.detail}>{it.detail || <span className="argo-apps-empty-value">–</span>}</td>
                        <td className="argo-apps-mono argo-apps-rev" title={it.revision}>{shortRevision(it.revision) || <span className="argo-apps-empty-value">–</span>}</td>
                        <td className="argo-apps-mono gitops-source" title={it.source}>{it.source || <span className="argo-apps-empty-value">–</span>}</td>
                        <td className="argo-apps-time">{it.updatedAt ? <LiveAge timestamp={it.updatedAt} /> : <span className="argo-apps-empty-value">–</span>}</td>
                        <td className="gitops-actions" onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
                          {it.tool === 'flux' && (
                            <>
                              <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={it.suspended} title={it.suspended ? 'Resume first: Flux ignores reconcile requests while suspended' : 'Reconcile now (R)'}
                                onClick={() => ask(reconcileSpec(cluster, { kind: it.kind, namespace: it.namespace, name: it.name, hasSource: !isSourceKind(it.kind) }))}>Reconcile</button>
                              <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost"
                                onClick={() => ask(suspendSpec(cluster, { kind: it.kind, namespace: it.namespace, name: it.name }, !it.suspended))}>{it.suspended ? 'Resume' : 'Suspend'}</button>
                            </>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          <div className="ap-statusbar">{rows.length} shown · {items.length} total{status?.byHealth?.Failing ? ` · ${status.byHealth.Failing} failing` : ''}</div>
        </>
      )}
      {dialog}
    </div>
  );
};

export default GitOpsPage;
