import { useEffect, useState, useCallback, useRef } from 'react';
import {
  CheckCircledIcon,
  CubeIcon,
  CodeIcon,
  Link2Icon,
  ExclamationTriangleIcon,
  CounterClockwiseClockIcon,
} from '@radix-ui/react-icons';
import Dialog from '../../common/Dialog';
import LiveAge from '../../common/LiveAge';
import api from '../../../services/api';
import { useStore } from '../../../store';
import useResourceNavigation from '../../../hooks/useResourceNavigation';
import { useVisibleInterval } from '../../../hooks/useVisibleInterval';
import { ArgoDestination, ArgoDestinationMap } from '../../../services/api/resources';
import PropertyGroup from '../shared/PropertyGroup';
import PropertyRow from '../../common/PropertyRow';
import MetadataSection from '../shared/MetadataSection';
import ApplicationTopology from './ApplicationTopology';
import ApplicationDiff from './ApplicationDiff';
import ArgoSyncDialog from '../../dialogs/ArgoSyncDialog';
import { ArgoSyncOptions } from '../../../services/api/resources';
import './ApplicationDetailView.css';

interface ResourceNode {
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
  status?: string;
  health?: string;
  syncStatus?: string;
  message?: string;
  exists?: boolean;
}

interface OperationResource {
  group?: string;
  version?: string;
  kind?: string;
  namespace?: string;
  name?: string;
  status?: string;
  message?: string;
  hookType?: string;
  hookPhase?: string;
  syncPhase?: string;
}

const opResourceLabel = (r: OperationResource): string => {
  if (r.hookType) return r.hookPhase || r.status || '—';
  return r.status || '—';
};

const opResourceClass = (r: OperationResource): string => {
  const raw = r.hookType ? (r.hookPhase || r.status || '') : (r.status || '');
  const s = raw.toLowerCase();
  if (s === 'synced' || s === 'succeeded' || s === 'pruned') return 'argo-op-status-success';
  if (s === 'syncfailed' || s === 'failed' || s === 'error') return 'argo-op-status-failed';
  if (s === 'running' || s === 'pending') return 'argo-op-status-running';
  return 'argo-op-status-unknown';
};

interface OperationState {
  phase?: string;
  message?: string;
  startedAt?: string;
  finishedAt?: string;
  resources?: OperationResource[];
}

interface HistoryEntry {
  id: number;
  revision?: string;
  deployedAt?: string;
  source?: string;
  initiator?: string;
}

interface AppSummary {
  name: string;
  namespace: string;
  project?: string;
  syncStatus?: string;
  health?: string;
  revision?: string;
  targetRevision?: string;
  repoUrl?: string;
  path?: string;
  destServer?: string;
  destName?: string;
  destNamespace?: string;
  resources?: ResourceNode[];
  conditions?: { type: string; message: string }[];
  operation?: OperationState;
  history?: HistoryEntry[];
  reconciledAt?: string;
  lastSyncedAt?: string;
  lastSyncPhase?: string;
  refreshRequested?: boolean;
}

const dispatchToast = (kind: 'success' | 'error', message: string) => {
  const event = new CustomEvent(kind === 'error' ? 'toast:error' : 'toast:success', { detail: { message } });
  window.dispatchEvent(event);
};

const healthBadgeClass = (h?: string) => {
  switch ((h || '').toLowerCase()) {
    case 'healthy':
      return 'status-badge success';
    case 'degraded':
    case 'missing':
      return 'status-badge danger';
    case 'progressing':
      return 'status-badge warning';
    default:
      return 'status-badge neutral';
  }
};

const normalizeServer = (s?: string): string => {
  if (!s) return '';
  try {
    const u = new URL(s);
    const port = u.port || (u.protocol === 'https:' ? '443' : '80');
    return `${u.protocol}//${u.hostname}:${port}${u.pathname.replace(/\/$/, '')}`;
  } catch {
    return s.replace(/\/$/, '');
  }
};

const resolveDestination = (
  summary: AppSummary,
  map: ArgoDestinationMap | null,
): ArgoDestination => {
  if (summary.destName === 'in-cluster' || (!summary.destName && !summary.destServer)) {
    return { kind: 'local' };
  }
  if (summary.destServer === 'https://kubernetes.default.svc' || summary.destServer === 'https://kubernetes.default.svc:443') {
    return { kind: 'local' };
  }
  if (map) {
    if (summary.destName && map.byName[summary.destName]) return map.byName[summary.destName];
    if (summary.destServer && map.byServer[normalizeServer(summary.destServer)]) {
      return map.byServer[normalizeServer(summary.destServer)];
    }
  }
  return { kind: 'external', server: summary.destServer, name: summary.destName };
};

const operationPhaseClass = (phase?: string): string => {
  switch ((phase || '').toLowerCase()) {
    case 'succeeded':
      return 'status-badge success';
    case 'failed':
    case 'error':
      return 'status-badge danger';
    case 'running':
    case 'terminating':
      return 'status-badge warning';
    default:
      return 'status-badge neutral';
  }
};

const syncBadgeClass = (s?: string) => {
  switch ((s || '').toLowerCase()) {
    case 'synced':
      return 'status-badge success';
    case 'outofsync':
      return 'status-badge warning';
    default:
      return 'status-badge neutral';
  }
};

interface Props {
  cluster: string;
  resource: any;
  handleResourceClick?: (kind: string, name: string, namespace?: string, e?: React.MouseEvent, apiVersion?: string) => void;
}

const ApplicationDetailView = ({ cluster, resource, handleResourceClick }: Props) => {
  const namespace = resource?.metadata?.namespace || '';
  const name = resource?.metadata?.name || '';
  const [summary, setSummary] = useState<AppSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | 'sync' | 'sync-prune' | 'refresh' | 'refresh-hard'>(null);
  const [destinations, setDestinations] = useState<ArgoDestinationMap | null>(null);
  const [navigatingTo, setNavigatingTo] = useState<string | null>(null);
  const [resourceView, setResourceView] = useState<'topology' | 'list' | 'diff'>('topology');
  const [syncDialogOpen, setSyncDialogOpen] = useState(false);
  const [rollbackTarget, setRollbackTarget] = useState<HistoryEntry | null>(null);
  const [rollbackPrune, setRollbackPrune] = useState(false);
  const [rollingBack, setRollingBack] = useState(false);
  const [pendingOp, setPendingOp] = useState<null | { kind: 'sync' | 'rollback' | 'refresh'; startedAt: number; label: string }>(null);
  const { openTab } = useStore();
  const { navigateToResource } = useResourceNavigation(cluster);

  const activeKeyRef = useRef('');
  useEffect(() => {
    const key = `${cluster}|${namespace}|${name}`;
    activeKeyRef.current = key;
    setSummary(null);
    setError(null);
    setLoading(true);
    setBusy(null);
    setPendingOp(null);
  }, [cluster, namespace, name]);

  const load = useCallback(
    async (silent = false) => {
      if (!namespace || !name) return;
      const key = `${cluster}|${namespace}|${name}`;
      if (!silent) setLoading(true);
      try {
        const data = await api.getArgoApplicationTree(cluster, namespace, name);
        if (activeKeyRef.current !== key) return;
        setSummary(data);
        setError(null);
      } catch (e: any) {
        if (activeKeyRef.current !== key) return;
        setError(e?.message || 'Failed to load Argo application');
      } finally {
        if (!silent && activeKeyRef.current === key) setLoading(false);
      }
    },
    [cluster, namespace, name],
  );

  const opPhase = summary?.operation?.phase || '';
  const opRunning = opPhase === 'Running' || opPhase === 'Terminating';

  useEffect(() => {
    if (!pendingOp) return;
    const startedAtIso = summary?.operation?.startedAt;
    if (!startedAtIso) return;
    const startedAtMs = new Date(startedAtIso).getTime();
    if (Number.isFinite(startedAtMs) && startedAtMs >= pendingOp.startedAt - 5000) {
      setPendingOp(null);
    }
  }, [summary?.operation?.startedAt, pendingOp]);

  useEffect(() => {
    if (!pendingOp) return;
    const tooOld = Date.now() - pendingOp.startedAt > 60000;
    if (tooOld) setPendingOp(null);
  }, [pendingOp, summary]);

  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => { load(); }, [load]);
  useVisibleInterval(() => load(true), opRunning || !!pendingOp ? 2000 : 10000, { targetRef: rootRef });

  useEffect(() => {
    let cancelled = false;
    api.getArgoDestinations(cluster).then((m) => {
      if (!cancelled) setDestinations(m);
    });
    return () => {
      cancelled = true;
    };
  }, [cluster]);

  const runSync = useCallback(
    async (opts: ArgoSyncOptions = {}) => {
      if (busy) return;
      setBusy(opts.prune ? 'sync-prune' : 'sync');
      setPendingOp({ kind: 'sync', startedAt: Date.now(), label: opts.dryRun ? 'Dry-run requested' : opts.prune ? 'Sync + Prune requested' : 'Sync requested' });
      try {
        await api.syncArgoApplication(cluster, namespace, name, opts);
        dispatchToast('success', opts.dryRun ? `Dry-run triggered for ${name}` : `Sync triggered for ${name}`);
        setSyncDialogOpen(false);
        load(true);
      } catch (e: any) {
        setPendingOp(null);
        dispatchToast('error', e?.response?.data?.error || e?.message || 'Sync failed');
      } finally {
        setBusy(null);
      }
    },
    [busy, cluster, namespace, name, load],
  );

  const runRefresh = useCallback(
    async (hard: boolean) => {
      if (busy) return;
      setBusy(hard ? 'refresh-hard' : 'refresh');
      try {
        await api.refreshArgoApplication(cluster, namespace, name, hard);
        dispatchToast('success', `${hard ? 'Hard ' : ''}Refresh triggered for ${name}`);
        load(true);
      } catch (e: any) {
        dispatchToast('error', e?.response?.data?.error || e?.message || 'Refresh failed');
      } finally {
        setBusy(null);
      }
    },
    [busy, cluster, namespace, name, load],
  );

  const navigateToManagedResource = useCallback(
    async (r: ResourceNode) => {
      if (!summary) return;
      const apiVersion = r.group ? `${r.group}/${r.version}` : r.version;
      const dest = resolveDestination(summary, destinations);
      const rowKey = `${r.kind}/${r.namespace || ''}/${r.name}`;
      try {
        let targetCluster = cluster;
        if (dest.kind === 'vcluster' && dest.vcluster) {
          setNavigatingTo(rowKey);
          const conn = await api.connectVCluster(cluster, dest.vcluster.namespace, dest.vcluster.name);
          targetCluster = conn.id;
          await openTab(conn.id);
          await new Promise((r) => setTimeout(r, 200));
        } else if (dest.kind !== 'local') {
          dispatchToast('error', `Resource lives on external destination${summary.destServer ? `: ${summary.destServer}` : ''}`);
          return;
        }

        const { openDetailTab, loadDetails } = useStore.getState();
        const resourceObj = {
          name: r.kind.toLowerCase() + 's',
          group: r.group,
          version: r.version,
          kind: r.kind,
          namespaced: !!r.namespace,
        };
        const itemObj = {
          name: r.name,
          namespace: r.namespace,
          uid: `${r.namespace || 'cluster'}-${r.name}`,
          kind: r.kind,
          apiVersion,
        };

        openDetailTab(resourceObj, itemObj, targetCluster, false);

        await navigateToResource({
          cluster: targetCluster,
          apiVersion,
          kind: r.kind,
          name: r.name,
          namespace: r.namespace,
        });

        loadDetails(targetCluster, resourceObj, itemObj).catch((e) => console.error('loadDetails failed', e));
      } catch (e: any) {
        dispatchToast('error', e?.response?.data?.error || e?.message || 'Failed to navigate');
      } finally {
        setNavigatingTo(null);
      }
    },
    [summary, destinations, cluster, openTab, navigateToResource],
  );

  const confirmRollback = useCallback(async () => {
    if (!rollbackTarget || rollingBack) return;
    setRollingBack(true);
    setPendingOp({ kind: 'rollback', startedAt: Date.now(), label: `Rollback to ${rollbackTarget.revision?.slice(0, 12) || rollbackTarget.id}` });
    try {
      await api.rollbackArgoApplication(cluster, namespace, name, rollbackTarget.id, rollbackPrune);
      dispatchToast('success', `Rollback to revision ${rollbackTarget.revision || rollbackTarget.id} triggered`);
      setRollbackTarget(null);
      setRollbackPrune(false);
      load(true);
    } catch (e: any) {
      setPendingOp(null);
      dispatchToast('error', e?.response?.data?.error || e?.message || 'Rollback failed');
    } finally {
      setRollingBack(false);
    }
  }, [rollbackTarget, rollbackPrune, rollingBack, cluster, namespace, name, load]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName?.toLowerCase();
      if (tag === 'input' || tag === 'textarea') return;
      const mod = e.metaKey || e.ctrlKey;
      if (!mod) return;
      const key = e.key.toLowerCase();
      if (key === 's' && !e.shiftKey) {
        e.preventDefault();
        runSync({});
      } else if (key === 's' && e.shiftKey) {
        e.preventDefault();
        setSyncDialogOpen(true);
      } else if (key === 'r' && !e.shiftKey) {
        e.preventDefault();
        runRefresh(false);
      } else if (key === 'r' && e.shiftKey) {
        e.preventDefault();
        runRefresh(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [runSync, runRefresh]);

  if (loading && !summary) {
    return <div className="argo-app-empty">Loading Argo application…</div>;
  }
  if (error) {
    return (
      <div className="argo-app-empty argo-error">
        {error}
        <button className="argo-retry" onClick={() => load()}>Retry</button>
      </div>
    );
  }
  if (!summary) {
    return <div className="argo-app-empty">No data</div>;
  }

  return (
    <>
      <div ref={rootRef} style={{ width: 1, height: 1, position: 'absolute' }} aria-hidden />
      <MetadataSection metadata={resource.metadata || {}} handleResourceClick={handleResourceClick} />
      <div className="section-divider" />

      <PropertyGroup title="Status" icon={<CheckCircledIcon />} defaultOpen>
        <PropertyRow
          label="Sync"
          value={
            <span className="argo-status-row-value">
              <span className={syncBadgeClass(summary.syncStatus)}>{summary.syncStatus || 'Unknown'}</span>
              {summary.lastSyncedAt && (
                <span className="argo-status-meta">
                  last sync <LiveAge timestamp={summary.lastSyncedAt} />
                  {summary.lastSyncPhase && summary.lastSyncPhase !== 'Succeeded' && (
                    <span className={`argo-status-phase-tag ${summary.lastSyncPhase.toLowerCase()}`}>
                      {summary.lastSyncPhase}
                    </span>
                  )}
                </span>
              )}
            </span>
          }
        />
        <PropertyRow
          label="Health"
          value={
            <span className="argo-status-row-value">
              <span className={healthBadgeClass(summary.health)}>{summary.health || 'Unknown'}</span>
              {summary.reconciledAt && (
                <span className="argo-status-meta">
                  refreshed <LiveAge timestamp={summary.reconciledAt} />
                  {summary.refreshRequested && <span className="argo-status-pending-tag">refresh requested</span>}
                </span>
              )}
            </span>
          }
        />
        {summary.project && <PropertyRow label="Project" value={summary.project} />}
        <div className="argo-actions-row">
          <button className="argo-action argo-primary" disabled={!!busy || opRunning} onClick={() => runSync({})} title="Sync (Cmd+S)">
            {(busy === 'sync' || opRunning) && <span className="argo-action-spinner" />}
            {busy === 'sync' ? 'Syncing…' : opRunning ? 'Syncing…' : 'Sync'}
          </button>
          <button className="argo-action" disabled={!!busy || opRunning} onClick={() => setSyncDialogOpen(true)} title="Sync with options (Cmd+Shift+S)">
            Sync Options…
          </button>
          <button className="argo-action" disabled={!!busy} onClick={() => runRefresh(false)} title="Refresh (Cmd+R)">
            {busy === 'refresh' && <span className="argo-action-spinner" />}
            {busy === 'refresh' ? 'Refreshing…' : 'Refresh'}
          </button>
          <button className="argo-action" disabled={!!busy} onClick={() => runRefresh(true)} title="Hard refresh (Cmd+Shift+R)">
            {busy === 'refresh-hard' && <span className="argo-action-spinner" />}
            {busy === 'refresh-hard' ? 'Refreshing…' : 'Hard Refresh'}
          </button>
        </div>
      </PropertyGroup>
      <div className="section-divider" />

      {pendingOp && !summary.operation && (
        <>
          <div className="argo-pending-banner">
            <span className="argo-pending-spinner" />
            <span className="argo-pending-label">{pendingOp.label}</span>
            <span className="argo-pending-detail">Waiting for Argo controller…</span>
          </div>
          <div className="section-divider" />
        </>
      )}

      {summary.operation && (
        <>
          <PropertyGroup
            title="Operation"
            icon={<CheckCircledIcon />}
            defaultOpen={opRunning}
            count={summary.operation.resources?.length}
          >
            <PropertyRow
              label="Phase"
              value={<span className={operationPhaseClass(summary.operation.phase)}>{summary.operation.phase || 'Unknown'}</span>}
            />
            {summary.operation.message && (
              <PropertyRow label="Message" value={summary.operation.message} />
            )}
            {summary.operation.startedAt && (
              <PropertyRow label="Started" value={new Date(summary.operation.startedAt).toLocaleString()} />
            )}
            {summary.operation.finishedAt && (
              <PropertyRow label="Finished" value={new Date(summary.operation.finishedAt).toLocaleString()} />
            )}
            {summary.operation.resources && summary.operation.resources.length > 0 && (
              <div className="argo-op-resources">
                {summary.operation.resources.map((r, i) => (
                  <div key={i} className="argo-op-row">
                    <span className="argo-op-kind">{r.kind}</span>
                    <span className="argo-op-name">{r.namespace ? `${r.namespace}/${r.name}` : r.name}</span>
                    <span className={`argo-op-status ${opResourceClass(r)}`}>
                      {opResourceLabel(r)}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <PropertyGroup title="Source" icon={<CodeIcon />} defaultOpen>
        <PropertyRow label="Repo" value={summary.repoUrl || '—'} copyText={summary.repoUrl || undefined} mono />
        <PropertyRow label="Path" value={summary.path || '—'} mono />
        <PropertyRow label="Target Revision" value={summary.targetRevision || '—'} mono />
        <PropertyRow label="Synced Revision" value={summary.revision || '—'} copyText={summary.revision || undefined} mono />
      </PropertyGroup>
      <div className="section-divider" />

      <PropertyGroup title="Destination" icon={<Link2Icon />} defaultOpen>
        {(() => {
          const dest = resolveDestination(summary, destinations);
          const kindLabel = dest.kind === 'vcluster' && dest.vcluster
            ? `vcluster: ${dest.vcluster.namespace}/${dest.vcluster.name}`
            : dest.kind === 'local'
              ? 'this cluster'
              : 'external';
          const kindBadgeClass = dest.kind === 'external' ? 'status-badge neutral' : 'status-badge success';
          return <PropertyRow label="Type" value={<span className={kindBadgeClass}>{kindLabel}</span>} />;
        })()}
        {summary.destName && <PropertyRow label="Name" value={summary.destName} mono />}
        <PropertyRow label="Server" value={summary.destServer || '—'} mono />
        <PropertyRow
          label="Namespace"
          value={
            summary.destNamespace && handleResourceClick ? (
              <button
                className="link-button"
                onClick={(e) => handleResourceClick('Namespace', summary.destNamespace!, undefined, e)}
              >
                {summary.destNamespace}
              </button>
            ) : (
              summary.destNamespace || '—'
            )
          }
        />
      </PropertyGroup>
      <div className="section-divider" />

      {summary.conditions && summary.conditions.length > 0 && (
        <>
          <PropertyGroup title="Conditions" icon={<ExclamationTriangleIcon />} count={summary.conditions.length} defaultOpen>
            {summary.conditions.map((c, i) => (
              <PropertyRow key={i} label={c.type} value={c.message || '—'} />
            ))}
          </PropertyGroup>
          <div className="section-divider" />
        </>
      )}

      <PropertyGroup
        title="Resources"
        icon={<CubeIcon />}
        count={summary.resources?.length ?? 0}
        defaultOpen
        actions={
          <div className="argo-view-toggle">
            <button
              type="button"
              className={`argo-view-toggle-btn ${resourceView === 'topology' ? 'active' : ''}`}
              onClick={() => setResourceView('topology')}
            >
              Topology
            </button>
            <button
              type="button"
              className={`argo-view-toggle-btn ${resourceView === 'list' ? 'active' : ''}`}
              onClick={() => setResourceView('list')}
            >
              List
            </button>
            <button
              type="button"
              className={`argo-view-toggle-btn ${resourceView === 'diff' ? 'active' : ''}`}
              onClick={() => setResourceView('diff')}
              title={summary.syncStatus === 'OutOfSync' ? 'Live vs Target diff' : 'No drift detected'}
            >
              Diff
              {summary.syncStatus === 'OutOfSync' && <span className="argo-diff-indicator" />}
            </button>
          </div>
        }
      >
        {resourceView === 'diff' ? (
          <ApplicationDiff cluster={cluster} namespace={namespace} name={name} />
        ) : resourceView === 'topology' ? (
          <ApplicationTopology
            cluster={cluster}
            namespace={namespace}
            name={name}
            destinations={destinations}
          />
        ) : (
          <div className="argo-resources">
            {(summary.resources || []).map((r, i) => {
              const dest = resolveDestination(summary, destinations);
              const label = r.namespace ? `${r.namespace}/${r.name}` : r.name;
              const rowKey = `${r.kind}/${r.namespace || ''}/${r.name}`;
              const clickable = dest.kind !== 'external';
              const titleHint = dest.kind === 'external'
                ? `Lives on external destination${summary.destServer ? `: ${summary.destServer}` : ''}`
                : dest.kind === 'vcluster' && dest.vcluster
                  ? `Open in vcluster ${dest.vcluster.namespace}/${dest.vcluster.name}`
                  : (r.message || 'Open in this cluster');
              return (
                <div
                  key={`${r.kind}-${r.namespace}-${r.name}-${i}`}
                  className="argo-resource-row"
                  title={r.message || ''}
                >
                  <span className="argo-resource-kind">{r.kind}</span>
                  {clickable ? (
                    <button
                      type="button"
                      className="argo-resource-name argo-resource-link"
                      title={titleHint}
                      onClick={() => navigateToManagedResource(r)}
                      disabled={navigatingTo === rowKey}
                    >
                      {navigatingTo === rowKey ? `Connecting… ${label}` : label}
                    </button>
                  ) : (
                    <span
                      className="argo-resource-name argo-resource-name-static"
                      title={titleHint}
                    >
                      {label}
                    </span>
                  )}
                  <span className="argo-resource-status">
                    {r.syncStatus && <span className={syncBadgeClass(r.syncStatus)}>{r.syncStatus}</span>}
                    {r.health && <span className={healthBadgeClass(r.health)}>{r.health}</span>}
                  </span>
                </div>
              );
            })}
            {(!summary.resources || summary.resources.length === 0) && (
              <div className="argo-app-empty argo-app-empty-inline">No managed resources reported.</div>
            )}
          </div>
        )}
      </PropertyGroup>

      {summary.history && summary.history.length > 0 && (
        <>
          <div className="section-divider" />
          <PropertyGroup
            title="History"
            icon={<CounterClockwiseClockIcon />}
            count={summary.history.length}
            defaultOpen={false}
          >
            <div className="argo-history">
              {(() => {
                const sorted = summary.history.slice().sort((a, b) => b.id - a.id);
                const currentId = sorted[0]?.id;
                return sorted.map((h) => {
                  const isCurrent = h.id === currentId;
                  return (
                  <div key={h.id} className={`argo-history-row ${isCurrent ? 'is-current' : ''}`}>
                    <div className="argo-history-info">
                      <span className="argo-history-id">#{h.id}</span>
                      <span className="argo-history-revision">{h.revision ? h.revision.slice(0, 12) : '—'}</span>
                      {h.deployedAt && (
                        <span className="argo-history-time">
                          {new Date(h.deployedAt).toLocaleString()}
                        </span>
                      )}
                      {h.initiator && <span className="argo-history-by">by {h.initiator}</span>}
                      {isCurrent && <span className="argo-history-current">current</span>}
                    </div>
                    <button
                      type="button"
                      className="argo-history-rollback"
                      disabled={!!busy || !!isCurrent}
                      onClick={() => setRollbackTarget(h)}
                      title={isCurrent ? 'This is the current revision' : 'Roll back to this revision'}
                    >
                      Rollback
                    </button>
                  </div>
                );
              });
              })()}
            </div>
          </PropertyGroup>
        </>
      )}

      <Dialog
        isOpen={!!rollbackTarget}
        title={rollbackTarget ? `Rollback to revision ${rollbackTarget.revision?.slice(0, 12) || rollbackTarget.id}` : ''}
        onClose={() => {
          setRollbackTarget(null);
          setRollbackPrune(false);
        }}
        onConfirm={confirmRollback}
        confirmText={rollingBack ? 'Rolling back…' : 'Rollback'}
        cancelText="Cancel"
        isLoading={rollingBack}
      >
        {rollbackTarget && (
          <div className="argo-rollback-dialog">
            <p>
              This will roll <strong>{name}</strong> back to revision{' '}
              <code>{rollbackTarget.revision?.slice(0, 12) || rollbackTarget.id}</code>
              {rollbackTarget.deployedAt && (
                <> (deployed {new Date(rollbackTarget.deployedAt).toLocaleString()})</>
              )}
              .
            </p>
            <label className="argo-rollback-prune">
              <input
                type="checkbox"
                checked={rollbackPrune}
                onChange={(e) => setRollbackPrune(e.target.checked)}
              />
              Prune resources that no longer exist in the target revision
            </label>
          </div>
        )}
      </Dialog>

      <ArgoSyncDialog
        isOpen={syncDialogOpen}
        appName={name}
        resources={(summary.resources || []).map((r) => ({
          group: r.group,
          kind: r.kind,
          namespace: r.namespace,
          name: r.name,
          syncStatus: r.syncStatus,
        }))}
        isRunning={busy === 'sync' || busy === 'sync-prune'}
        defaultRevision={summary.targetRevision}
        onCancel={() => setSyncDialogOpen(false)}
        onConfirm={(opts) => runSync(opts)}
      />
    </>
  );
};

export default ApplicationDetailView;
