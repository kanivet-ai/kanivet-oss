import { useCallback, useEffect, useState } from 'react';
import { CheckCircledIcon, ReloadIcon, PauseIcon, PlayIcon, ArchiveIcon, CountdownTimerIcon } from '@radix-ui/react-icons';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import PropertyRow from '../common/PropertyRow';
import LiveAge from '../common/LiveAge';
import ResourceLink from '../ResourceLink';
import { getFluxObject, FluxObjectDetail } from '../../services/api/integrations';
import { StatusBadge, healthTone, shortRevision } from './statusParts';
import { useConfirmedAction } from './useConfirmedAction';
import { reconcileSpec, suspendSpec } from './fluxActions';
import { isSourceKind } from './gitopsFilter';
import './GitOpsPage.css';

interface Props {
  cluster: string;
  resource: any;
}

const INVENTORY_PAGE = 50;

/**
 * Flux section of the inspector for any *.toolkit.fluxcd.io object: status,
 * revisions, source, actions, and the managed inventory as resource links.
 */
const FluxDetailSection = ({ cluster, resource }: Props) => {
  const { kind, metadata = {} } = resource;
  const namespace = metadata.namespace || '';
  const name = metadata.name || '';
  const [detail, setDetail] = useState<FluxObjectDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(async () => {
    try {
      setDetail(await getFluxObject(cluster, kind, namespace, name));
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to load Flux status');
    }
  }, [cluster, kind, namespace, name]);

  // Reload whenever the inspector's object changes (watch-driven).
  useEffect(() => { load(); }, [load, metadata.resourceVersion]);

  const { ask, dialog, busy } = useConfirmedAction(load);

  if (error && !detail) return <div className="gitops-message gitops-message--danger">{error}</div>;
  if (!detail) return null;

  const target = { kind, namespace, name, hasSource: !isSourceKind(kind) };
  const inventory = showAll ? detail.inventory : detail.inventory.slice(0, INVENTORY_PAGE);
  const failing = detail.status === 'Failed';

  return (
    <div className="gitops-section">
      <div className="quick-actions gitops-section-actions">
        <button className="quick-action-btn" disabled={busy || detail.suspended} onClick={() => ask(reconcileSpec(cluster, target))}
          title={detail.suspended ? 'Resume first: Flux ignores reconcile requests while suspended' : 'Reconcile now'} aria-label="Reconcile now">
          <span className="ap-action-icon"><ReloadIcon /></span>
          <span className="ap-action-label">Reconcile</span>
        </button>
        <button className={`quick-action-btn${detail.suspended ? '' : ' ap-action--warn'}`} disabled={busy} onClick={() => ask(suspendSpec(cluster, target, !detail.suspended))}
          aria-label={detail.suspended ? 'Resume' : 'Suspend'}>
          <span className="ap-action-icon">{detail.suspended ? <PlayIcon /> : <PauseIcon />}</span>
          <span className="ap-action-label">{detail.suspended ? 'Resume' : 'Suspend'}</span>
        </button>
      </div>

      <PropertyGroup title="Flux" icon={<CheckCircledIcon />} defaultOpen persistKey="flux-status">
        <PropertyRow label="Status" value={<StatusBadge tone={healthTone(detail.status)} label={detail.status} title={detail.reason} />} />
        {detail.reason && <PropertyRow label="Reason" value={detail.reason} />}
        {detail.revision && <PropertyRow label={detail.category === 'source' ? 'Artifact' : 'Applied revision'} value={shortRevision(detail.revision)} copyText={detail.revision} mono />}
        {detail.attemptedRevision && detail.attemptedRevision !== detail.revision && (
          <PropertyRow label="Attempted revision" value={shortRevision(detail.attemptedRevision)} copyText={detail.attemptedRevision} mono />
        )}
        {detail.sourceRef && (
          <PropertyRow label="Source" value={
            <ResourceLink cluster={cluster} apiVersion={detail.sourceRef.apiVersion || 'source.toolkit.fluxcd.io/v1'} kind={detail.sourceRef.kind} name={detail.sourceRef.name} namespace={detail.sourceRef.namespace} openInDetailTab>
              {detail.sourceRef.kind}/{detail.sourceRef.name}
            </ResourceLink>
          } />
        )}
        {detail.url && <PropertyRow label="URL" value={detail.url} copyText={detail.url} mono />}
        {detail.path && <PropertyRow label="Path" value={detail.path} mono />}
        {detail.chart && <PropertyRow label="Chart" value={`${detail.chart}${detail.chartVersion ? ` ${detail.chartVersion}` : ''}`} />}
        {detail.targetNamespace && <PropertyRow label="Target namespace" value={detail.targetNamespace} mono />}
        {detail.interval && <PropertyRow label="Interval" value={detail.interval} />}
        <PropertyRow label="Suspended" value={detail.suspended ? 'Yes' : 'No'} />
        {detail.lastReconciledAt && <PropertyRow label="Ready since" value={<LiveAge timestamp={detail.lastReconciledAt} />} />}
        {detail.lastHandledReconcileAt && <PropertyRow label="Last manual reconcile" value={detail.lastHandledReconcileAt} muted mono />}
      </PropertyGroup>

      {detail.message && <div className={`gitops-message${failing ? ' gitops-message--danger' : ''}`}>{detail.message}</div>}

      {detail.category === 'kustomization' && (
        <PropertyGroup title="Managed resources" icon={<ArchiveIcon />} count={detail.inventory.length} defaultOpen={detail.inventory.length <= 30} persistKey="flux-inventory">
          {detail.inventory.length === 0 ? (
            <div className="gitops-inventory-row gitops-empty-hint">No inventory recorded yet.</div>
          ) : (
            <div className="gitops-inventory">
              {inventory.map((r) => (
                <div key={`${r.apiVersion}/${r.kind}/${r.namespace}/${r.name}`} className="gitops-inventory-row">
                  <span className="gitops-inventory-kind" title={r.apiVersion}>{r.kind}</span>
                  <ResourceLink cluster={cluster} apiVersion={r.apiVersion || 'v1'} kind={r.kind} name={r.name} namespace={r.namespace || undefined} openInDetailTab />
                  {r.namespace && <span className="gitops-inventory-ns">{r.namespace}</span>}
                </div>
              ))}
              {!showAll && detail.inventory.length > INVENTORY_PAGE && (
                <button type="button" className="gitops-inventory-more" onClick={() => setShowAll(true)}>Show all {detail.inventory.length}</button>
              )}
            </div>
          )}
        </PropertyGroup>
      )}

      {detail.history && detail.history.length > 0 && (
        <PropertyGroup title="Release history" icon={<CountdownTimerIcon />} count={detail.history.length} defaultOpen={false} persistKey="flux-history">
          <div className="gitops-inventory">
            {detail.history.map((h) => (
              <div key={h.version} className="gitops-inventory-row">
                <span className="gitops-inventory-kind">v{h.version} · {h.status || 'unknown'}</span>
                <span>{h.chartName} {h.chartVersion}{h.appVersion ? ` (app ${h.appVersion})` : ''}</span>
                {h.lastDeployed && <span className="gitops-inventory-ns"><LiveAge timestamp={h.lastDeployed} /></span>}
              </div>
            ))}
          </div>
        </PropertyGroup>
      )}
      <div className="section-divider" />
      {dialog}
    </div>
  );
};

export default FluxDetailSection;
