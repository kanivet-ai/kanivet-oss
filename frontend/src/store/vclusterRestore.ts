import api from '../services/api';
import { parseVClusterId } from '../utils/clusterUtils';

/**
 * A vcluster is reached through a connection the backend holds in memory, so a
 * vcluster tab restored after a restart has none: everything it loads fails
 * until the connection is made again. Reconnecting is idempotent and waits for
 * the vcluster to be healthy.
 *
 * Only ids registered here (by the restore) wait; every other load, vcluster
 * tabs the user opened included, is untouched.
 */
const pending = new Map<string, Promise<boolean>>();

export const reconnectRestoredVCluster = (id: string): void => {
  if (pending.has(id)) return;
  const vcluster = parseVClusterId(id);
  if (!vcluster) return;
  const attempt = (async () => {
    try {
      await api.connectVCluster(vcluster.host, vcluster.namespace, vcluster.name);
      return true;
    } catch (err: any) {
      const reason = err?.response?.data?.error || err?.message || 'unknown error';
      window.dispatchEvent(
        new CustomEvent('toast:error', { detail: { message: `Could not reconnect to vcluster ${vcluster.name}: ${reason}` } }),
      );
      return false;
    }
  })();
  pending.set(id, attempt);
  // A failed reconnect must not block later loads (the user may connect it by
  // hand); they fail or succeed on their own.
  attempt.then((ok) => {
    if (!ok && pending.get(id) === attempt) pending.delete(id);
  });
};

/** A promise to wait on before loading from `id`, or undefined when there is
 * nothing to wait for. Resolves false when the reconnect failed. */
export const vclusterGate = (id: string): Promise<boolean> | undefined => pending.get(id);

export const forgetRestoredVCluster = (id: string): void => {
  pending.delete(id);
};
