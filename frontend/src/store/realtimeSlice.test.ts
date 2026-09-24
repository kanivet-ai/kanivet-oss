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

const CLUSTER = 'c1';
const resource = (name: string) => ({ group: '', version: 'v1', name, kind: name, namespaced: true });
const topicOf = (name: string) => `items:${CLUSTER}::v1:${name}:`;
const item = (name: string, rv = '1') => ({ name, namespace: 'ns', uid: `uid-${name}`, resourceVersion: rv });

// A store with just enough of the app's tab state for the realtime slice.
const makeStore = () => create<any>()((set, get, api) => ({
  currentTab: CLUSTER,
  tabIndexMap: new Map([[CLUSTER, 0]]),
  activeTabs: [{ id: CLUSTER, state: { selectedNode: null, listItems: [], resourceListTabs: [], detailTabs: [], selectedItem: null } }],
  getCurrentTabState: () => get().activeTabs[0].state,
  updateCurrentTabState: (patch: any) => set((st: any) => ({ activeTabs: [{ ...st.activeTabs[0], state: { ...st.activeTabs[0].state, ...patch } }] })),
  removeRolloutTracking: () => {},
  ...createRealtimeSlice(set, get, api),
}));

const select = (store: any, name: string) =>
  store.getState().updateCurrentTabState({ selectedNode: { id: name, type: 'resource', data: resource(name) }, listItems: [] });

const emit = (name: string, events: any[], bulk = false) => {
  for (const h of ws.handlers.get(topicOf(name)) || []) h({ isBatch: true, topic: topicOf(name), events, bulk, epoch: bulk ? 1 : undefined });
};
const snapshot = (name: string, items: any[]) =>
  emit(name, [...items.map((i) => ({ action: 'added', item: i })), { type: 'sync_complete', itemCount: items.length, epoch: 1 }], true);

// Selects a resource and lets its subscription start and deliver a snapshot.
const open = async (store: any, name: string, items: any[]) => {
  select(store, name);
  store.getState().startRealtime();
  await vi.advanceTimersByTimeAsync(60);
  snapshot(name, items);
  await vi.advanceTimersByTimeAsync(700);
};

describe('realtime subscriptions across tab switches', () => {
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

  it('keeps the previous tab subscribed and current while another tab is shown', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a'), item('b')]);
    await open(store, 'secrets', [item('s')]);

    expect(ws.unsubscribes).not.toContain(topicOf('configmaps'));
    emit('configmaps', [{ action: 'added', item: item('c') }, { action: 'deleted', item: item('a') }]);
    await vi.advanceTimersByTimeAsync(700);

    expect(liveItemsFor(topicOf('configmaps'))!.map((i) => i.name).sort()).toEqual(['b', 'c']);
    // The on-screen list is untouched by the background topic.
    expect(store.getState().getCurrentTabState().listItems.map((i: any) => i.name)).toEqual(['s']);
  });

  it('resumes a parked tab with its live list and no new subscription', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a')]);
    await open(store, 'secrets', [item('s')]);
    emit('configmaps', [{ action: 'added', item: item('b') }]);
    await vi.advanceTimersByTimeAsync(700);
    ws.subscribes.length = 0;

    select(store, 'configmaps');
    store.getState().startRealtime(true);
    await vi.advanceTimersByTimeAsync(60);

    const state = store.getState().getCurrentTabState();
    expect(ws.subscribes).toEqual([]);
    expect(state.listItems.map((i: any) => i.name).sort()).toEqual(['a', 'b']);
    expect(state.isLoadingListItems).toBe(false);
    expect(state.hasReceivedInitialListData).toBe(true);
  });

  it('applies events that arrive during the switch-away debounce to the parked list', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a')]);
    emit('configmaps', [{ action: 'added', item: item('late') }]);
    // The flush lands after the switch but before the old subscription is parked.
    await vi.advanceTimersByTimeAsync(100);
    select(store, 'secrets');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(800);

    expect(liveItemsFor(topicOf('configmaps'))!.map((i) => i.name).sort()).toEqual(['a', 'late']);
  });

  it('does not resubscribe a live topic on refresh', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a')]);
    ws.subscribes.length = 0;
    store.getState().startRealtime(true);
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.subscribes).toEqual([]);
  });

  it('resubscribes a parked topic that never received its list', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    await open(store, 'secrets', [item('s')]);
    ws.subscribes.length = 0;

    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.subscribes).toEqual([topicOf('configmaps')]);
    expect(store.getState().getCurrentTabState().isLoadingListItems).toBe(true);
  });

  it('caps the parked subscriptions and closes the oldest', async () => {
    const store = makeStore();
    const names = ['r1', 'r2', 'r3', 'r4', 'r5', 'r6', 'r7', 'r8'];
    for (const n of names) await open(store, n, [item(n)]);
    // r8 is on screen; r2..r7 stay parked; r1 was closed.
    expect(ws.unsubscribes).toEqual([topicOf('r1')]);
    expect(liveItemsFor(topicOf('r2'))).toBeDefined();
    expect(liveItemsFor(topicOf('r1'))).toBeUndefined();
  });

  it('releases only the matching topics', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a')]);
    await open(store, 'secrets', [item('s')]);
    store.getState().releaseRealtimeTopics((t: string) => t === topicOf('configmaps'));
    expect(ws.unsubscribes).toEqual([topicOf('configmaps')]);
    expect(liveItemsFor(topicOf('secrets'))).toBeDefined();
  });

  it('stopRealtime closes the on-screen and every parked subscription', async () => {
    const store = makeStore();
    await open(store, 'configmaps', [item('a')]);
    await open(store, 'secrets', [item('s')]);
    store.getState().stopRealtime();
    expect(ws.unsubscribes.sort()).toEqual([topicOf('configmaps'), topicOf('secrets')].sort());
    expect(ws.handlers.size).toBe(0);
  });
});
