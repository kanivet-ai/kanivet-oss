import { useCallback, useEffect, useState } from 'react';
import { ArchiveIcon, CheckCircledIcon, ClockIcon, LayersIcon, MixerHorizontalIcon, ReloadIcon, UploadIcon } from '@radix-ui/react-icons';
import PropertyGroup from '../detailView/shared/PropertyGroup';
import PropertyRow from '../common/PropertyRow';
import LiveAge from '../common/LiveAge';
import ResourceLink from '../ResourceLink';
import { getCNPGClusterStatus, CNPGClusterStatus } from '../../services/api/integrations';
import { StatusBadge } from '../gitops/statusParts';
import { useConfirmedAction } from '../gitops/useConfirmedAction';
import { backupSpec, promoteSpec, restartSpec } from './cnpgActions';
import { backupMethodLabel, backupPhaseTone, cnpgHealthTone, formatLag, lagTone, promotable } from './cnpgFormat';
import './CNPG.css';

interface Props {
  cluster: string;
  resource: any;
}

const BACKUPS_SHOWN = 8;

/** Inspector section for postgresql.cnpg.io/Cluster. */
const CNPGClusterSection = ({ cluster, resource }: Props) => {
  const { metadata = {} } = resource;
  const namespace = metadata.namespace || '';
  const name = metadata.name || '';
  const apiVersion = resource.apiVersion || 'postgresql.cnpg.io/v1';
  const [st, setSt] = useState<CNPGClusterStatus | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setSt(await getCNPGClusterStatus(cluster, namespace, name));
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || 'Failed to load cluster status');
    }
  }, [cluster, namespace, name]);

  useEffect(() => { load(); }, [load, metadata.resourceVersion]);
  // Instances and lag move without the Cluster object changing.
  useEffect(() => {
    const t = window.setInterval(() => { if (document.visibilityState === 'visible') load(); }, 10000);
    return () => window.clearInterval(t);
  }, [load]);

  const { ask, dialog, busy } = useConfirmedAction(load);

  if (error && !st) return <div className="cnpg-message cnpg-message--danger">{error}</div>;
  if (!st) return <div className="cnpg-loading"><span className="ap-spinner" /> Loading PostgreSQL status</div>;

  const c = st.cluster;
  const switching = !!c.targetPrimary && c.targetPrimary !== c.currentPrimary;
  const candidates = promotable(st.instances);
  const podRef = (pod: string) => <ResourceLink cluster={cluster} apiVersion="v1" kind="Pod" name={pod} namespace={namespace} openInDetailTab />;

  return (
    <div className="cnpg-section">
      <div className="quick-actions cnpg-actions">
        <button className="quick-action-btn" disabled={busy || !st.backupConfigured} onClick={() => ask(backupSpec(cluster, c))}
          title={st.backupConfigured ? 'Back up now' : 'This cluster declares no backup method'} aria-label="Back up now">
          <span className="ap-action-icon"><UploadIcon /></span>
          <span className="ap-action-label">Back up</span>
        </button>
        <button className="quick-action-btn ap-action--warn" disabled={busy || switching} onClick={() => ask(restartSpec(cluster, c))} aria-label="Rolling restart">
          <span className="ap-action-icon"><ReloadIcon /></span>
          <span className="ap-action-label">Restart</span>
        </button>
      </div>

      <div className="cnpg-tiles">
        <div className="cnpg-tile"><span className="cnpg-tile-label">Health</span><StatusBadge tone={cnpgHealthTone(c.health)} label={c.health} title={c.phase} /></div>
        <div className="cnpg-tile"><span className="cnpg-tile-label">Instances</span><span className="cnpg-tile-value">{c.readyInstances}/{c.instances}</span></div>
        <div className="cnpg-tile"><span className="cnpg-tile-label">Primary</span><span className="cnpg-tile-value cnpg-mono">{c.currentPrimary ? podRef(c.currentPrimary) : '–'}</span></div>
        <div className="cnpg-tile"><span className="cnpg-tile-label">Timeline</span><span className="cnpg-tile-value">{c.timeline ?? '–'}</span></div>
        <div className="cnpg-tile"><span className="cnpg-tile-label">Last backup</span><span className="cnpg-tile-value">{c.lastSuccessfulBackup ? <LiveAge timestamp={c.lastSuccessfulBackup} /> : 'Never'}</span></div>
      </div>

      {(c.phase || switching) && (
        <div className={`cnpg-message${c.health === 'Failing' ? ' cnpg-message--danger' : ''}`}>
          {c.phase}{c.phaseReason ? ` — ${c.phaseReason}` : ''}{switching ? ` (switching to ${c.targetPrimary})` : ''}
        </div>
      )}

      <PropertyGroup title="Instances" icon={<LayersIcon />} count={st.instances.length} defaultOpen persistKey="cnpg-instances">
        <table className="cnpg-table">
          <thead>
            <tr><th>Instance</th><th>Role</th><th>Status</th><th>Lag</th><th>Node</th><th className="cnpg-num">Restarts</th><th aria-label="Actions" /></tr>
          </thead>
          <tbody>
            {st.instances.map((i) => (
              <tr key={i.name}>
                <td className="cnpg-mono">{podRef(i.name)}</td>
                <td>{i.role === 'primary' ? <StatusBadge tone="info" label="Primary" /> : <span className="cnpg-muted">Replica</span>}</td>
                <td><span className={`ap-dot ap-dot--${i.status === 'failed' ? 'danger' : i.ready ? 'success' : 'warning'}`} aria-hidden="true" /> {i.ready ? i.status : (i.podPhase || 'not ready')}</td>
                <td>{i.role === 'primary' ? <span className="cnpg-muted">–</span> : i.lagSeconds !== undefined
                  ? <span className={`cnpg-lag cnpg-lag--${lagTone(i.lagSeconds)}`}>{formatLag(i.lagSeconds)}</span>
                  : <span className="cnpg-muted" title="Needs pods/proxy access to the instance metrics endpoint">unknown</span>}
                </td>
                <td className="cnpg-mono">{i.node ? <ResourceLink cluster={cluster} apiVersion="v1" kind="Node" name={i.node} openInDetailTab /> : '–'}</td>
                <td className="cnpg-num">{i.restarts}</td>
                <td className="cnpg-row-actions">
                  {candidates.some((p) => p.name === i.name) && !switching && (
                    <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" disabled={busy} onClick={() => ask(promoteSpec(cluster, c, i.name))}>Promote</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </PropertyGroup>

      <PropertyGroup title="Backups" icon={<ArchiveIcon />} count={st.backups.length} defaultOpen persistKey="cnpg-backups">
        <PropertyRow label="Method" value={backupMethodLabel(c.backupMethod, c.backupPlugin)} />
        {c.firstRecoverabilityPoint && <PropertyRow label="Recoverable from" value={<LiveAge timestamp={c.firstRecoverabilityPoint} />} />}
        {c.lastFailedBackup && <PropertyRow label="Last failure" value={<LiveAge timestamp={c.lastFailedBackup} />} />}
        {st.backups.length === 0 ? (
          <div className="cnpg-empty">{st.backupConfigured ? 'No backups yet.' : 'Backups are not configured for this cluster.'}</div>
        ) : (
          <table className="cnpg-table">
            <thead><tr><th>Backup</th><th>Phase</th><th>Method</th><th>Started</th><th>Finished</th></tr></thead>
            <tbody>
              {st.backups.slice(0, BACKUPS_SHOWN).map((b) => (
                <tr key={b.name} title={b.error}>
                  <td className="cnpg-mono"><ResourceLink cluster={cluster} apiVersion={apiVersion} kind="Backup" name={b.name} namespace={namespace} openInDetailTab /></td>
                  <td><StatusBadge tone={backupPhaseTone(b.phase)} label={b.phase || 'pending'} /></td>
                  <td className="cnpg-muted">{b.method || '–'}</td>
                  <td>{b.startedAt ? <LiveAge timestamp={b.startedAt} /> : '–'}</td>
                  <td>{b.stoppedAt ? <LiveAge timestamp={b.stoppedAt} /> : '–'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </PropertyGroup>

      {st.scheduledBackups.length > 0 && (
        <PropertyGroup title="Scheduled backups" icon={<ClockIcon />} count={st.scheduledBackups.length} defaultOpen persistKey="cnpg-scheduled">
          <table className="cnpg-table">
            <thead><tr><th>Schedule</th><th>Cron</th><th>State</th><th>Last run</th><th>Next run</th></tr></thead>
            <tbody>
              {st.scheduledBackups.map((s) => (
                <tr key={s.name}>
                  <td className="cnpg-mono"><ResourceLink cluster={cluster} apiVersion={apiVersion} kind="ScheduledBackup" name={s.name} namespace={namespace} openInDetailTab /></td>
                  <td className="cnpg-mono">{s.schedule}</td>
                  <td>{s.suspended ? <StatusBadge tone="warning" label="Suspended" /> : <StatusBadge tone="success" label="Active" />}</td>
                  <td>{s.lastScheduleTime ? <LiveAge timestamp={s.lastScheduleTime} /> : '–'}</td>
                  <td className="cnpg-muted">{s.nextScheduleTime || '–'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </PropertyGroup>
      )}

      {st.poolers.length > 0 && (
        <PropertyGroup title="Poolers" icon={<MixerHorizontalIcon />} count={st.poolers.length} defaultOpen persistKey="cnpg-poolers">
          {st.poolers.map((p) => (
            <PropertyRow key={p.name}
              label={<ResourceLink cluster={cluster} apiVersion={apiVersion} kind="Pooler" name={p.name} namespace={namespace} openInDetailTab />}
              value={`${p.type === 'ro' ? 'Read-only' : p.type === 'r' ? 'Read' : 'Read-write'} ·${p.instances} instance${p.instances === 1 ? '' : 's'}${p.poolMode ? ` · ${p.poolMode}` : ''}`} />
          ))}
        </PropertyGroup>
      )}

      <PropertyGroup title="Storage" icon={<CheckCircledIcon />} defaultOpen={false} persistKey="cnpg-storage">
        {c.storageSize && <PropertyRow label="Volume size" value={c.storageSize} />}
        {c.image && <PropertyRow label="Image" value={c.image} copyText={c.image} mono />}
      </PropertyGroup>
      <div className="section-divider" />
      {dialog}
    </div>
  );
};

export default CNPGClusterSection;
