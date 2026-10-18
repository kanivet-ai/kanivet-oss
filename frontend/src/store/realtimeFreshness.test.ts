import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { create } from 'zustand';

const ws = vi.hoisted(() => ({
  handlers: new Map<string, Set<(evt: any) => void>>(),
  subscribes: [] as string[],
  unsubscribes: [] as string[],
}));

vi.mock('../services/api', () => ({
  default: {
    isReady: () => true,
    get wsHandlers() { return ws.handlers; },
    subscribeToItems: (cluster: string, group: string, version: string, kind: string, _ns: string, onEvent: (evt: any) => void) => {
      const topic = `items:${cluster}:${group}:${version}:${kind}:`;
      if (!ws.handlers.has(topic)) ws.handlers.set(topic, new Set());
      ws.handlers.get(topic)!.add(onEvent);
      ws.subscribes.push(topic);
      return topic;
    },
    unsubscribe: (topic: string, onEvent: (evt: any) => void) => {
      ws.handlers.get(topic)?.delete(onEvent);
      if (!ws.handlers.get(topic)?.size) ws.handlers.delete(topic);
      ws.unsubscribes.push(topic);
    },
  },
}));
vi.mock('../services/islandNotifications', () => ({ notifyRolloutComplete: () => {} }));

const { createRealtimeSlice, liveItemsFor } = await import('./realtimeSlice');

const resource = (name: string) => ({ group: '', version: 'v1', name, kind: name, namespaced: true });
const topicIn = (cluster: string, name: string) => `items:${cluster}::v1:${name}:`;
const item = (name: string, rv = '1') => ({ name, namespace: 'ns', uid: `uid-${name}`, resourceVersion: rv });
const tabState = (extra: any = {}) => ({ selectedNode: null, listItems: [], resourceListTabs: [], detailTabs: [], selectedItem: null, ...extra });

// A store with cluster tabs A and B and just enough tab state for the slice.
const makeStore = () =>
  create<any>()((set, get, api) => ({
    currentTab: 'A',
    tabIndexMap: new Map([['A', 0], ['B', 1]]),
    activeTabs: [{ id: 'A', state: tabState() }, { id: 'B', state: tabState() }],
    getCurrentTabState: () => get().activeTabs[get().tabIndexMap.get(get().currentTab)].state,
    updateCurrentTabState: (patch: any) =>
      set((st: any) => {
        const i = st.tabIndexMap.get(st.currentTab);
        const tabs = [...st.activeTabs];
        tabs[i] = { ...tabs[i], state: { ...tabs[i].state, ...patch } };
        return { activeTabs: tabs };
      }),
    removeRolloutTracking: () => {},
    ...createRealtimeSlice(set, get, api),
  }));

const emitIn = (cluster: string, name: string, events: any[], bulk = false, epoch = 1) => {
  const topic = topicIn(cluster, name);
  for (const h of ws.handlers.get(topic) || []) h({ isBatch: true, topic, events, bulk, epoch: bulk ? epoch : undefined });
};
const emit = (name: string, events: any[], bulk = false, epoch = 1) => emitIn('A', name, events, bulk, epoch);
const snapshotIn = (cluster: string, name: string, items: any[], epoch = 1) =>
  emitIn(cluster, name, [...items.map((i) => ({ action: 'added', item: i })), { type: 'sync_complete', itemCount: items.length, epoch }], true, epoch);
const snapshot = (name: string, items: any[], epoch = 1) => snapshotIn('A', name, items, epoch);
const failedSync = (name: string, extra: any = {}) => emit(name, [{ type: 'sync_complete', itemCount: 0, epoch: 0, ...extra }]);
const topicStatus = (name: string, status: any) => emit(name, [{ type: 'topic_status', channel: 'items', topic: topicIn('A', name), ...status }]);

const select = (store: any, name: string, extra: any = {}) =>
  store.getState().updateCurrentTabState({ selectedNode: { id: name, type: 'resource', data: resource(name) }, listItems: [], ...extra });
const start = async (store: any) => {
  store.getState().startRealtime();
  await vi.advanceTimersByTimeAsync(60);
};
const open = async (store: any, name: string, items: any[]) => {
  select(store, name);
  await start(store);
  snapshot(name, items);
  await vi.advanceTimersByTimeAsync(700);
};
const shown = (store: any) => store.getState().getCurrentTabState();
const health = (store: any, name: string, cluster = 'A') => store.getState().topicHealth[topicIn(cluster, name)];
const names = (items: any[]) => items.map((i: any) => i.name);

beforeEach(() => {
  vi.useFakeTimers();
  const g = globalThis as any;
  g.window = g;
  g.addEventListener ??= () => {};
  g.removeEventListener ??= () => {};
  g.CustomEvent ??= class { constructor(public type: string, public init?: any) {} };
  g.dispatchEvent = () => true;
  g.requestAnimationFrame = (cb: () => void) => setTimeout(cb, 0);
  g.cancelAnimationFrame = (id: any) => clearTimeout(id);
  delete g.__kanivetRealtime;
  delete g.__kanivetParkedRealtime;
  delete g.__kanivetItemsCache;
  ws.handlers.clear();
  ws.subscribes.length = 0;
  ws.unsubscribes.length = 0;
});
afterEach(() => vi.useRealTimers());

describe('a list the backend could not read', () => {
  it('is reported as failed, keeping the rows of an earlier visit without showing them as live', async () => {
    const store = makeStore();
    // Rows from an earlier visit, as loadListItems puts them from the cache.
    select(store, 'pods', { listItems: [item('deleted-yesterday')] });
    await start(store);
    expect(shown(store).isLoadingListItems).toBe(true);
    expect(shown(store).hasReceivedInitialListData).toBe(false);

    // The backend's failure sync: epoch 0, no rows.
    failedSync('pods');
    await vi.advanceTimersByTimeAsync(31_000);

    const st = shown(store);
    expect(names(st.listItems)).toEqual(['deleted-yesterday']);
    expect(st.isLoadingListItems).toBe(false);
    expect(st.hasReceivedInitialListData).toBe(false);
    expect(st.loadError).toBeTruthy();
    expect(health(store, 'pods').live).toBe(false);
    expect(health(store, 'pods').failing.error).toBe(st.loadError);
    expect(liveItemsFor(topicIn('A', 'pods'))).toBeUndefined();
  });

  it('shows the error and the time of the last complete list the backend sent', async () => {
    const store = makeStore();
    select(store, 'pods');
    await start(store);
    failedSync('pods', { failed: true, error: 'pods is forbidden', errorCode: 'forbidden', lastSyncAt: 1_700_000_000_000 });
    await vi.advanceTimersByTimeAsync(20);
    expect(shown(store).loadError).toBe('pods is forbidden');
    expect(health(store, 'pods')).toMatchObject({
      live: false,
      lastSyncAt: 1_700_000_000_000,
      failing: { error: 'pods is forbidden', errorCode: 'forbidden' },
    });
  });

  it('becomes live once a list does arrive', async () => {
    const store = makeStore();
    select(store, 'pods', { listItems: [item('old')] });
    await start(store);
    failedSync('pods', { failed: true, error: 'timeout' });
    await vi.advanceTimersByTimeAsync(20);
    snapshot('pods', [item('new')], 2);
    await vi.advanceTimersByTimeAsync(20);
    const st = shown(store);
    expect(names(st.listItems)).toEqual(['new']);
    expect(st.loadError).toBeUndefined();
    expect(st.hasReceivedInitialListData).toBe(true);
    expect(health(store, 'pods').live).toBe(true);
    expect(health(store, 'pods').failing).toBeNull();
    expect(health(store, 'pods').lastSyncAt).toBeGreaterThan(0);
  });

  it('does not take a live event on cached rows for the list arriving', async () => {
    const store = makeStore();
    select(store, 'pods', { listItems: [item('a')] });
    await start(store);
    emit('pods', [{ action: 'modified', item: item('a', '2') }]);
    await vi.advanceTimersByTimeAsync(20);
    expect(shown(store).hasReceivedInitialListData).toBe(false);
    expect(health(store, 'pods').live).toBe(false);
  });

  it('reports a load that times out with rows on screen', async () => {
    const store = makeStore();
    select(store, 'pods', { listItems: [item('cached')] });
    await start(store);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(shown(store).loadError).toMatch(/longer than expected/);
    expect(names(shown(store).listItems)).toEqual(['cached']);
    expect(health(store, 'pods').failing.errorCode).toBe('timeout');
  });

  it('shows the rows of a reload as loading, not live', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    // What a reload does: the subscription goes, the rows stay on screen.
    store.getState().releaseRealtimeTopics((t: string) => t === topicIn('A', 'pods'));
    await start(store);
    expect(names(shown(store).listItems)).toEqual(['a']);
    expect(shown(store).hasReceivedInitialListData).toBe(false);
    expect(health(store, 'pods').live).toBe(false);
  });
});

describe('topic status', () => {
  it('marks a live list as not updating while the backend reports it failing, and clears it on ok', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    topicStatus('pods', { state: 'failing', error: 'connection refused', errorCode: 'list_failed', failingSince: 1000, lastSyncAt: 500 });
    expect(shown(store).loadError).toBe('connection refused');
    expect(names(shown(store).listItems)).toEqual(['a']);
    expect(health(store, 'pods')).toMatchObject({ live: false, lastSyncAt: 500, failing: { error: 'connection refused', since: 1000 } });

    topicStatus('pods', { state: 'ok', lastSyncAt: 2000 });
    expect(shown(store).loadError).toBeUndefined();
    expect(health(store, 'pods')).toMatchObject({ live: true, failing: null, lastSyncAt: 2000 });
  });

  it('asks again, with backoff, for a list the backend lost track of', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    ws.subscribes.length = 0;
    topicStatus('pods', { state: 'failing', error: 'lost sync', errorCode: 'desynced' });
    await vi.advanceTimersByTimeAsync(999);
    expect(ws.subscribes).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    expect(ws.subscribes).toEqual([topicIn('A', 'pods')]);
    expect(ws.unsubscribes).not.toContain(topicIn('A', 'pods'));

    // Still failing: the next attempt waits longer.
    topicStatus('pods', { state: 'failing', error: 'lost sync', errorCode: 'desynced' });
    await vi.advanceTimersByTimeAsync(1999);
    expect(ws.subscribes).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(ws.subscribes).toHaveLength(2);

    // A list arrives: the backoff starts over.
    snapshot('pods', [item('a'), item('b')], 2);
    await vi.advanceTimersByTimeAsync(200);
    expect(shown(store).loadError).toBeUndefined();
    topicStatus('pods', { state: 'failing', error: 'lost sync', errorCode: 'desynced' });
    await vi.advanceTimersByTimeAsync(1000);
    expect(ws.subscribes).toHaveLength(3);
  });

  it('restarts a stopped watch with a new subscription', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    ws.subscribes.length = 0;
    topicStatus('pods', { state: 'failing', error: 'watch ended', errorCode: 'watch_stopped' });
    await vi.advanceTimersByTimeAsync(1000);
    expect(ws.unsubscribes).toContain(topicIn('A', 'pods'));
    expect(ws.subscribes).toEqual([topicIn('A', 'pods')]);
    expect(ws.handlers.has(topicIn('A', 'pods'))).toBe(true);
  });

  it('does not resubscribe for failures a new subscription cannot fix', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    ws.subscribes.length = 0;
    topicStatus('pods', { state: 'failing', error: 'forbidden', errorCode: 'forbidden' });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(ws.subscribes).toEqual([]);
  });

  it('shows a parked list that went failing as not updating when it comes back', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a')]);
    await open(store, 'secrets', [item('s')]);
    topicStatus('pods', { state: 'failing', error: 'connection refused' });
    select(store, 'pods');
    await start(store);
    expect(names(shown(store).listItems)).toEqual(['a']);
    expect(shown(store).loadError).toBe('connection refused');
  });
});

describe('split panes', () => {
  const listTab = (id: string, cluster: string, name: string, paneId: string) => ({
    id, title: name, cluster, resource: resource(name), items: [], selectedItem: null, paneId, selectedNamespaces: [],
  });
  const paneItems = (store: any, tabId: string, cluster = 'A') => {
    const st = store.getState();
    const t = st.activeTabs[st.tabIndexMap.get(cluster)].state.resourceListTabs.find((r: any) => r.id === tabId);
    return names(t.items);
  };
  // Cluster A shows pods (focused) and deployments side by side.
  const splitA = async (store: any) => {
    store.getState().updateCurrentTabState({
      resourceListTabs: [listTab('lt', 'A', 'pods', 'root'), listTab('rt', 'A', 'deployments', 'p2')],
      activeResourceListTabByPane: { root: 'lt', p2: 'rt' },
      activeResourceListTab: 'rt',
    });
    await open(store, 'deployments', [item('web')]);
    store.getState().updateCurrentTabState({ activeResourceListTab: 'lt' });
    await open(store, 'pods', [item('p1')]);
  };

  it('keeps the pane list of a cluster tab in the background through browsing elsewhere', async () => {
    const store = makeStore();
    await splitA(store);
    expect(paneItems(store, 'rt')).toEqual(['web']);

    store.getState().parkRealtime();
    store.setState({ currentTab: 'B' });
    for (const n of ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h']) {
      select(store, n);
      await start(store);
    }
    expect(ws.handlers.has(topicIn('A', 'deployments'))).toBe(true);
    // Changes while A is in the background reach the subscription only.
    emit('deployments', [{ action: 'added', item: item('api') }]);
    await vi.advanceTimersByTimeAsync(700);

    store.getState().parkRealtime();
    store.setState({ currentTab: 'A' });
    await start(store);
    expect(paneItems(store, 'rt').sort()).toEqual(['api', 'web']);
    emit('deployments', [{ action: 'deleted', item: item('web') }]);
    await vi.advanceTimersByTimeAsync(700);
    expect(paneItems(store, 'rt')).toEqual(['api']);
  });

  it('subscribes again every pane list of a cluster tab shown after its subscriptions closed', async () => {
    const store = makeStore();
    await splitA(store);
    store.getState().parkRealtime();
    store.setState({ currentTab: 'B' });
    select(store, 'a');
    await start(store);
    // A reconnect while B is shown: only B's lists are restarted.
    store.getState().stopRealtime();
    store.getState().startRealtime(true);
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.handlers.has(topicIn('A', 'deployments'))).toBe(false);

    store.getState().parkRealtime();
    store.setState({ currentTab: 'A' });
    ws.subscribes.length = 0;
    await start(store);
    expect(ws.subscribes.sort()).toEqual([topicIn('A', 'deployments'), topicIn('A', 'pods')].sort());
    expect(health(store, 'deployments').live).toBe(false);
    snapshot('deployments', [item('web'), item('new')], 2);
    await vi.advanceTimersByTimeAsync(700);
    expect(paneItems(store, 'rt')).toEqual(['web', 'new']);
    expect(health(store, 'deployments').live).toBe(true);
  });

  it('starts every pane list of a restored workspace, not only the focused one', async () => {
    const store = makeStore();
    store.getState().updateCurrentTabState({
      resourceListTabs: [listTab('lt', 'A', 'pods', 'root'), listTab('rt', 'A', 'deployments', 'p2'), listTab('dash', 'A', 'cluster-dashboard', 'p3')],
      activeResourceListTabByPane: { root: 'lt', p2: 'rt', p3: null },
      activeResourceListTab: 'lt',
    });
    select(store, 'pods');
    await start(store);
    expect(ws.subscribes.sort()).toEqual([topicIn('A', 'deployments'), topicIn('A', 'pods')].sort());
    snapshot('deployments', [item('web')]);
    await vi.advanceTimersByTimeAsync(700);
    expect(paneItems(store, 'rt')).toEqual(['web']);
    // The unfocused pane has its own state: its list arrived, the focused one has not.
    expect(health(store, 'deployments').live).toBe(true);
    expect(health(store, 'pods').live).toBe(false);
  });

  it('never subscribes a page shown in a pane', async () => {
    const store = makeStore();
    store.getState().updateCurrentTabState({
      resourceListTabs: [
        listTab('lt', 'A', 'pods', 'root'),
        { ...listTab('dash', 'A', 'cluster-dashboard', 'p2'), resource: { name: 'cluster-dashboard', group: '', version: 'v1', kind: 'ClusterDashboard' } },
      ],
      activeResourceListTabByPane: { root: 'lt', p2: 'dash' },
      activeResourceListTab: 'lt',
    });
    select(store, 'pods');
    await start(store);
    expect(ws.subscribes).toEqual([topicIn('A', 'pods')]);
  });
});

describe('open details of objects a resync drops', () => {
  const detailTab = (name: string, uid?: string) => ({
    id: `detail-${name}`,
    title: name,
    resource: resource('pods'),
    cluster: 'A',
    isPinned: true,
    location: 'detail',
    item: { kind: 'pods', apiVersion: 'v1', metadata: { name, namespace: 'ns', ...(uid && { uid }) } },
  });

  it('are marked deleted when a completed listing no longer has them', async () => {
    const store = makeStore();
    store.getState().updateCurrentTabState({ detailTabs: [detailTab('gone', 'uid-gone'), detailTab('kept'), detailTab('other-uid', 'uid-old')] });
    await open(store, 'pods', [item('gone'), item('kept'), item('other-uid')]);
    // After a reconnect the backend lists again; two objects went meanwhile.
    snapshot('pods', [item('kept')], 2);
    await vi.advanceTimersByTimeAsync(200);
    const tabs = shown(store).detailTabs;
    expect(tabs.find((t: any) => t.id === 'detail-gone').isDeleted).toBe(true);
    expect(tabs.find((t: any) => t.id === 'detail-kept').isDeleted).toBeFalsy();
    // A tab of an older object of the same name is not this one.
    expect(tabs.find((t: any) => t.id === 'detail-other-uid').isDeleted).toBeFalsy();
  });

  it('are marked deleted when the list was parked', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('gone'), item('kept')]);
    await open(store, 'secrets', [item('s')]);
    store.getState().updateCurrentTabState({ detailTabs: [detailTab('gone')] });
    snapshot('pods', [item('kept')], 2);
    await vi.advanceTimersByTimeAsync(700);
    expect(shown(store).detailTabs[0].isDeleted).toBe(true);
  });

  it('are left alone by a failed list', async () => {
    const store = makeStore();
    store.getState().updateCurrentTabState({ detailTabs: [detailTab('a')] });
    await open(store, 'pods', [item('a')]);
    failedSync('pods', { failed: true, error: 'x' });
    await vi.advanceTimersByTimeAsync(200);
    expect(shown(store).detailTabs[0].isDeleted).toBeFalsy();
    expect(names(shown(store).listItems)).toEqual(['a']);
  });
});
