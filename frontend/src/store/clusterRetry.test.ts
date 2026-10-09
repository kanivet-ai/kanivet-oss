import { describe, it, expect, vi, beforeEach } from 'vitest';

const api = vi.hoisted(() => ({ refreshClusters: vi.fn(async () => []) }));
vi.mock('../services/api', () => ({ default: api }));
const forgetTreeLoad = vi.hoisted(() => vi.fn());
vi.mock('./resourceSlice', () => ({ forgetTreeLoad }));

const { resumeShownList, retryCluster } = await import('./clusterRetry');

const CLUSTER = 'c1';
const pods = { group: '', version: 'v1', name: 'pods', kind: 'Pod', namespaced: true };

// A store holding only what a retry reads and calls. status is what the
// cluster's next status check answers.
const makeStore = (status: { healthy: boolean }, selectedNode: any = { id: 'pods', type: 'resource', data: pods }) => {
  const calls: string[] = [];
  const state: any = {
    currentTab: CLUSTER,
    clusterStatuses: {},
    getCurrentTabState: () => ({ selectedNode }),
    loadClusterStatus: vi.fn(async (cluster: string) => {
      calls.push('status');
      state.clusterStatuses = { [cluster]: status };
    }),
    loadTreeData: vi.fn(async () => { calls.push('tree'); }),
    loadListItems: vi.fn(async () => { calls.push('list'); return true; }),
    stopRealtime: vi.fn(() => { calls.push('stop'); }),
    startRealtime: vi.fn(() => { calls.push('start'); }),
  };
  return { store: { getState: () => state }, state, calls };
};

describe('retrying a failing cluster', () => {
  beforeEach(() => {
    (globalThis as any).window = globalThis;
    delete (globalThis as any).__kanivetItemsCache;
    forgetTreeLoad.mockClear();
  });

  it('leaves the list subscribed when the cluster still does not answer', async () => {
    const cache = new Map([[`items:${CLUSTER}::v1:pods:`, [{ name: 'a' }]]]);
    (globalThis as any).__kanivetItemsCache = cache;
    const { store, state } = makeStore({ healthy: false });

    expect(await retryCluster(store, CLUSTER)).toBe(false);

    // The backend goes on retrying and sends the rows to whoever is still
    // subscribed; a list stopped here would never hear of them.
    expect(state.stopRealtime).not.toHaveBeenCalled();
    expect(state.loadListItems).not.toHaveBeenCalled();
    expect(state.loadTreeData).not.toHaveBeenCalled();
    expect(cache.size).toBe(1);
  });

  it('reloads the tree and restarts the list once the cluster answers', async () => {
    const cache = new Map([
      [`items:${CLUSTER}::v1:pods:`, [{ name: 'a' }]],
      ['items:other::v1:pods:', [{ name: 'b' }]],
    ]);
    (globalThis as any).__kanivetItemsCache = cache;
    const { store, state, calls } = makeStore({ healthy: true });

    expect(await retryCluster(store, CLUSTER)).toBe(true);

    expect(calls).toEqual(['status', 'tree', 'stop', 'list', 'start']);
    expect(forgetTreeLoad).toHaveBeenCalledWith(CLUSTER);
    expect(state.loadListItems).toHaveBeenCalledWith(CLUSTER, pods);
    expect([...cache.keys()]).toEqual(['items:other::v1:pods:']);
  });

  it('restarts the list even when the tree cannot be loaded', async () => {
    const { store, state, calls } = makeStore({ healthy: true });
    state.loadTreeData = vi.fn(async () => { throw new Error('categories unavailable'); });

    expect(await retryCluster(store, CLUSTER)).toBe(true);

    expect(calls).toEqual(['status', 'stop', 'list', 'start']);
  });

  it('never leaves a stopped list without a subscription', async () => {
    const { store, state } = makeStore({ healthy: true });
    state.loadListItems = vi.fn(async () => { throw new Error('gate failed'); });

    await expect(retryCluster(store, CLUSTER)).rejects.toThrow('gate failed');

    expect(state.stopRealtime).toHaveBeenCalledTimes(1);
    expect(state.startRealtime).toHaveBeenCalledTimes(1);
  });

  it('does not touch the list of a cluster the user moved to meanwhile', async () => {
    const { store, state } = makeStore({ healthy: true });
    state.loadTreeData = vi.fn(async () => { state.currentTab = 'other'; });

    expect(await retryCluster(store, CLUSTER)).toBe(true);

    expect(state.stopRealtime).not.toHaveBeenCalled();
    expect(state.loadListItems).not.toHaveBeenCalled();
    expect(state.startRealtime).not.toHaveBeenCalled();
  });

  it('starts nothing when a page, not a list, is on screen', async () => {
    const { store, state } = makeStore({ healthy: true }, { id: 'cluster-overview', type: 'overview', data: {} });

    expect(await retryCluster(store, CLUSTER)).toBe(true);

    expect(state.loadListItems).not.toHaveBeenCalled();
    expect(state.startRealtime).not.toHaveBeenCalled();
  });
});

describe('a cluster that answers again', () => {
  it('starts the list on screen, which a live subscription ignores', () => {
    const { store, state } = makeStore({ healthy: true });

    resumeShownList(store, CLUSTER);

    expect(state.startRealtime).toHaveBeenCalledWith(true);
  });

  it('leaves another cluster and pages alone', () => {
    const other = makeStore({ healthy: true });
    other.state.currentTab = 'other';
    resumeShownList(other.store, CLUSTER);
    expect(other.state.startRealtime).not.toHaveBeenCalled();

    const page = makeStore({ healthy: true }, { id: 'cluster-overview', type: 'overview', data: {} });
    resumeShownList(page.store, CLUSTER);
    expect(page.state.startRealtime).not.toHaveBeenCalled();
  });
});
