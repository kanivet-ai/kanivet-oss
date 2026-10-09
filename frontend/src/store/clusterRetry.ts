import api from '../services/api';
import { forgetTreeLoad } from './resourceSlice';
import type { StoreState } from './types';

interface Store {
  getState: () => StoreState;
}

// The resource whose list is on screen, when it is one of this cluster's.
const shownList = (state: StoreState, cluster: string): any => {
  if (state.currentTab !== cluster) return null;
  const node = state.getCurrentTabState()?.selectedNode;
  return node?.type === 'resource' && node.data ? node.data : null;
};

const dropCachedLists = (cluster: string) => {
  const cacheMap: Map<string, any[]> | undefined = (window as any).__kanivetItemsCache;
  if (!cacheMap) return;
  for (const key of [...cacheMap.keys()]) {
    if (key.startsWith(`items:${cluster}:`)) cacheMap.delete(key);
  }
};

/**
 * Checks a failing cluster again and reports whether it answers now. When it
 * does, its tree and the list on screen are loaded again through the client
 * the backend has just rebuilt.
 *
 * When it does not, nothing on screen is touched. The list keeps its
 * subscription, and the backend, which goes on retrying, sends it the rows as
 * soon as the cluster is back. Stopping the list before the check left it
 * without a subscription for good whenever the check failed, and the check
 * that follows a sign-in often does: the list then showed whatever rows it
 * held, with the error gone, until the cluster tab was reopened.
 */
export const retryCluster = async (store: Store, cluster: string): Promise<boolean> => {
  await api.refreshClusters();
  await store.getState().loadClusterStatus(cluster, true);
  if (!store.getState().clusterStatuses[cluster]?.healthy) return false;

  // Not the load still hanging on the client the refresh replaced.
  forgetTreeLoad(cluster);
  // The list does not need the tree: one that cannot be loaded must not keep
  // the list from restarting.
  await store.getState().loadTreeData(cluster).catch(() => {});

  dropCachedLists(cluster);
  // The user may have moved to another cluster while the check ran; its list
  // is not this cluster's to restart.
  const state = store.getState();
  if (state.currentTab !== cluster) return true;
  const resource = shownList(state, cluster);
  state.stopRealtime();
  if (resource) {
    try {
      await state.loadListItems(cluster, resource);
    } finally {
      store.getState().startRealtime();
    }
  }
  return true;
};

/**
 * Starts the list on screen when it has no subscription, once its cluster
 * answers again. A list that is already subscribed is left as it is.
 */
export const resumeShownList = (store: Store, cluster: string): void => {
  const state = store.getState();
  if (shownList(state, cluster)) state.startRealtime(true);
};
