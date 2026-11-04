import { reconcileFlux, setFluxSuspended } from '../../services/api/integrations';
import type { ConfirmSpec } from './useConfirmedAction';

interface Target {
  kind: string;
  namespace: string;
  name: string;
  /** Kustomizations and HelmReleases have a source to refresh first. */
  hasSource?: boolean;
}

const label = (t: Target) => `${t.kind} ${t.namespace ? `${t.namespace}/` : ''}${t.name}`;

export const reconcileSpec = (cluster: string, t: Target): ConfirmSpec => ({
  title: 'Reconcile now?',
  body: (
    <p className="ap-dialog-description">
      Flux will reconcile <strong>{label(t)}</strong> immediately instead of waiting for its interval
      {t.hasSource ? ', after fetching the latest revision from its source' : ''}. This sets the
      <code className="ap-mono"> reconcile.fluxcd.io/requestedAt</code> annotation.
    </p>
  ),
  confirmText: 'Reconcile',
  done: `Reconcile requested for ${t.name}`,
  failed: 'Reconcile failed',
  run: () => reconcileFlux(cluster, t.kind, t.namespace, t.name, !!t.hasSource),
});

export const suspendSpec = (cluster: string, t: Target, suspend: boolean): ConfirmSpec => ({
  title: suspend ? 'Suspend reconciliation?' : 'Resume reconciliation?',
  body: (
    <p className="ap-dialog-description">
      {suspend
        ? <>Flux stops applying changes to <strong>{label(t)}</strong> until it is resumed. Drift from Git is not corrected meanwhile.</>
        : <>Flux resumes reconciling <strong>{label(t)}</strong> and applies the latest revision right away.</>}
    </p>
  ),
  confirmText: suspend ? 'Suspend' : 'Resume',
  variant: suspend ? 'warning' : 'default',
  done: `${suspend ? 'Suspended' : 'Resumed'} ${t.name}`,
  failed: suspend ? 'Suspend failed' : 'Resume failed',
  run: () => setFluxSuspended(cluster, t.kind, t.namespace, t.name, suspend),
});
