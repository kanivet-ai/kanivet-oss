import React, { useCallback, useState } from 'react';
import Dialog from '../common/Dialog';
import { useStore } from '../../store';
import { failureMessage } from '../../utils/errorMessage';

export interface ConfirmSpec {
  title: string;
  body: React.ReactNode;
  confirmText: string;
  variant?: 'default' | 'warning' | 'danger';
  /** Toast on success. */
  done: string;
  /** Prefix of the failure toast ("Reconcile failed"). */
  failed: string;
  run: () => Promise<unknown>;
}

/**
 * Every write the integrations make goes through one confirmation sheet and
 * reports through the app's toasts. Returns the trigger and the dialog to
 * render.
 */
export function useConfirmedAction(onDone?: () => void) {
  const [spec, setSpec] = useState<ConfirmSpec | null>(null);
  const [busy, setBusy] = useState(false);

  const confirm = useCallback(async () => {
    if (!spec) return;
    setBusy(true);
    const { addToast } = useStore.getState();
    try {
      await spec.run();
      addToast({ type: 'success', message: spec.done });
      setSpec(null);
      onDone?.();
    } catch (e) {
      addToast({ type: 'error', message: failureMessage(spec.failed, e) });
    } finally {
      setBusy(false);
    }
  }, [spec, onDone]);

  const dialog = (
    <Dialog
      isOpen={!!spec}
      onClose={() => !busy && setSpec(null)}
      onConfirm={confirm}
      title={spec?.title || ''}
      confirmText={spec?.confirmText}
      variant={spec?.variant}
      isLoading={busy}
      loadingText="Working..."
    >
      {spec?.body}
    </Dialog>
  );

  return { ask: setSpec, dialog, busy };
}
