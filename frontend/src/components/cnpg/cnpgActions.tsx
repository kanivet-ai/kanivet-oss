import { cnpgBackupNow, cnpgPromote, cnpgRestart, CNPGClusterSummary } from '../../services/api/integrations';
import type { ConfirmSpec } from '../gitops/useConfirmedAction';
import { backupMethodLabel } from './cnpgFormat';

type C = Pick<CNPGClusterSummary, 'namespace' | 'name' | 'backupMethod' | 'backupPlugin' | 'currentPrimary' | 'instances'>;

export const backupSpec = (cluster: string, c: C): ConfirmSpec => ({
  title: 'Back up now?',
  body: (
    <p className="ap-dialog-description">
      Creates a <code className="ap-mono">Backup</code> of <strong>{c.namespace}/{c.name}</strong> using
      {' '}{backupMethodLabel(c.backupMethod, c.backupPlugin).toLowerCase()}. The operator takes it from the
      cluster's configured backup target.
    </p>
  ),
  confirmText: 'Back up',
  done: `Backup of ${c.name} requested`,
  failed: 'Backup failed',
  run: () => cnpgBackupNow(cluster, c.namespace, c.name),
});

export const promoteSpec = (cluster: string, c: C, instance: string): ConfirmSpec => ({
  title: `Promote ${instance}?`,
  body: (
    <p className="ap-dialog-description">
      Switches the primary of <strong>{c.namespace}/{c.name}</strong> from <strong>{c.currentPrimary || 'the current primary'}</strong> to
      {' '}<strong>{instance}</strong>. The current primary is demoted and restarted as a replica; clients on the
      read-write service reconnect.
    </p>
  ),
  confirmText: 'Promote',
  variant: 'warning',
  done: `Switchover to ${instance} requested`,
  failed: 'Promote failed',
  run: () => cnpgPromote(cluster, c.namespace, c.name, instance),
});

export const restartSpec = (cluster: string, c: C): ConfirmSpec => ({
  title: 'Restart the cluster?',
  body: (
    <p className="ap-dialog-description">
      Rolling restart of all {c.instances} instance{c.instances === 1 ? '' : 's'} of <strong>{c.namespace}/{c.name}</strong>:
      replicas first, then a switchover or in-place restart of the primary. Expect a brief write interruption.
    </p>
  ),
  confirmText: 'Restart',
  variant: 'warning',
  done: `Rolling restart of ${c.name} requested`,
  failed: 'Restart failed',
  run: () => cnpgRestart(cluster, c.namespace, c.name),
});
