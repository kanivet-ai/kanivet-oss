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
  store.getState().updateCurrentTabState({
    selectedNode: { id: name, type: 'resource', data: resource(name) },
    listItems: [],
  });

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

  it('resubscribes a parked topic that never received its list within the load timeout', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    await open(store, 'secrets', [item('s')]);
    await vi.advanceTimersByTimeAsync(31_000);
    ws.subscribes.length = 0;

    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.subscribes).toEqual([topicOf('configmaps')]);
    expect(store.getState().getCurrentTabState().isLoadingListItems).toBe(true);
  });

  it('keeps waiting on a parked subscription whose list is still on its way', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    await open(store, 'secrets', [item('s')]);
    ws.subscribes.length = 0;

    select(store, 'configmaps');
    // Forced, as activating a list tab does: asking again would only make the
    // backend send the whole list a second time once it has it.
    store.getState().startRealtime(true);
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.subscribes).toEqual([]);
    expect(store.getState().getCurrentTabState().isLoadingListItems).toBe(true);
    snapshot('configmaps', [item('a')]);
    await vi.advanceTimersByTimeAsync(20);
    const state = store.getState().getCurrentTabState();
    expect(state.listItems.map((i: any) => i.name)).toEqual(['a']);
    expect(state.isLoadingListItems).toBe(false);
  });

  it('times out a parked list that never arrives from its first subscribe', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    await vi.advanceTimersByTimeAsync(60);
    await open(store, 'secrets', [item('s')]);
    await vi.advanceTimersByTimeAsync(20_000);

    select(store, 'configmaps');
    store.getState().startRealtime(true);
    await vi.advanceTimersByTimeAsync(9_000);
    expect(store.getState().getCurrentTabState().loadError).toBeUndefined();
    await vi.advanceTimersByTimeAsync(1_000);
    expect(store.getState().getCurrentTabState().loadError).toBeTruthy();
  });

  it('subscribes on the first call and shows the first rows on the next frame', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    expect(ws.subscribes).toEqual([topicOf('configmaps')]);
    emit('configmaps', [{ action: 'added', item: item('a') }], true);
    await vi.advanceTimersByTimeAsync(1);
    expect(
      store
        .getState()
        .getCurrentTabState()
        .listItems.map((i: any) => i.name),
    ).toEqual(['a']);
    // Later updates still coalesce.
    emit('configmaps', [{ action: 'added', item: item('b') }]);
    await vi.advanceTimersByTimeAsync(20);
    expect(store.getState().getCurrentTabState().listItems).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(120);
    expect(store.getState().getCurrentTabState().listItems).toHaveLength(2);
  });

  it('collapses a burst of starts into the first one and one at the end', async () => {
    const store = makeStore();
    select(store, 'configmaps');
    store.getState().startRealtime();
    select(store, 'secrets');
    store.getState().startRealtime(true);
    store.getState().startRealtime(true);
    expect(ws.subscribes).toEqual([topicOf('configmaps')]);
    await vi.advanceTimersByTimeAsync(60);
    expect(ws.subscribes).toEqual([topicOf('configmaps'), topicOf('secrets')]);
  });

  it('keeps the selection on the live object of its row', async () => {
    const store = makeStore();
    await open(store, 'pods', [item('a'), item('b')]);
    const selected = store.getState().getCurrentTabState().listItems[0];
    store.getState().updateCurrentTabState({ selectedItem: selected });
    emit('pods', [
      { action: 'modified', item: { ...item('a', '2'), phase: 'Failed' } },
    ]);
    await vi.advanceTimersByTimeAsync(200);
    const state = store.getState().getCurrentTabState();
    expect(state.selectedItem.phase).toBe('Failed');
    expect(state.selectedItem).toBe(state.listItems[0]);
  });

  it('keeps a list shown in another split pane live', async () => {
    const store = makeStore();
    const listTab = (id: string, name: string, paneId: string) => ({
      id,
      title: name,
      resource: resource(name),
      items: [],
      selectedItem: null,
      cluster: CLUSTER,
      paneId,
    });
    store.getState().updateCurrentTabState({
      resourceListTabs: [
        listTab('t-pods', 'pods', 'root'),
        listTab('t-deploy', 'deployments', 'p2'),
      ],
      activeResourceListTabByPane: { root: 't-pods', p2: 't-deploy' },
    });
    await open(store, 'pods', [item('a')]);
    await open(store, 'deployments', [item('d')]);

    emit('pods', [
      { action: 'modified', item: { ...item('a', '2'), phase: 'Failed' } },
    ]);
    await vi.advanceTimersByTimeAsync(200);
    const state = store.getState().getCurrentTabState();
    const podsTab = state.resourceListTabs.find((t: any) => t.id === 't-pods');
    expect(podsTab.items[0].phase).toBe('Failed');
    // The on-screen list is left alone.
    expect(state.listItems.map((i: any) => i.name)).toEqual(['d']);
  });

  describe('a list shown in another split pane', () => {
    const splitPods = (store: any) => {
      const listTab = (id: string, name: string, paneId: string) => ({
        id, title: name, resource: resource(name), items: [], selectedItem: null, cluster: CLUSTER, paneId,
      });
      store.getState().updateCurrentTabState({
        resourceListTabs: [listTab('t-pods', 'pods', 'root'), listTab('t-other', 'r0', 'p2')],
        activeResourceListTabByPane: { root: 't-pods', p2: 't-other' },
      });
    };
    const podsPhase = (store: any) =>
      store.getState().getCurrentTabState().resourceListTabs.find((t: any) => t.id === 't-pods').items[0]?.phase;

    it('is never the parked subscription closed to make room', async () => {
      const store = makeStore();
      splitPods(store);
      await open(store, 'pods', [item('a')]);
      // The other pane browses more resource types than are kept parked.
      for (const n of ['r1', 'r2', 'r3', 'r4', 'r5', 'r6', 'r7', 'r8']) await open(store, n, [item(n)]);

      expect(ws.unsubscribes).not.toContain(topicOf('pods'));
      emit('pods', [{ action: 'modified', item: { ...item('a', '2'), phase: 'Failed' } }]);
      await vi.advanceTimersByTimeAsync(200);
      expect(podsPhase(store)).toBe('Failed');
    });

    it('stays live when a reconnect restarts the on-screen list', async () => {
      const store = makeStore();
      splitPods(store);
      await open(store, 'pods', [item('a')]);
      await open(store, 'r0', [item('d')]);
      ws.subscribes.length = 0;

      // What the reconnect, SSO refresh and cluster retry handlers do.
      store.getState().stopRealtime();
      store.getState().startRealtime(true);
      await vi.advanceTimersByTimeAsync(60);

      expect(ws.subscribes).toContain(topicOf('pods'));
      emit('pods', [{ action: 'modified', item: { ...item('a', '2'), phase: 'Failed' } }]);
      await vi.advanceTimersByTimeAsync(200);
      expect(podsPhase(store)).toBe('Failed');
    });
  });

  describe('open details', () => {
    const detailTab = (kind: string, apiVersion: string, res: any) => ({
      id: `detail-${kind}`,
      title: 'api',
      resource: res,
      cluster: CLUSTER,
      isPinned: true,
      location: 'detail',
      item: {
        kind,
        apiVersion,
        metadata: { name: 'api', namespace: 'ns' },
        status: { conditions: [] },
      },
    });
    const deployments = {
      group: 'apps',
      version: 'v1',
      name: 'deployments',
      kind: 'Deployment',
      namespaced: true,
    };
    const services = {
      group: '',
      version: 'v1',
      name: 'services',
      kind: 'services',
      namespaced: true,
    };
    const openDeployments = async (store: any, items: any[]) => {
      store.getState().updateCurrentTabState({
        selectedNode: {
          id: 'deployments',
          type: 'resource',
          data: deployments,
        },
        listItems: [],
      });
      store.getState().startRealtime();
      await vi.advanceTimersByTimeAsync(60);
      for (const h of ws.handlers.get(
        `items:${CLUSTER}:apps:v1:deployments:`,
      ) || []) {
        h({
          isBatch: true,
          events: [
            ...items.map((i) => ({ action: 'added', item: i })),
            { type: 'sync_complete', itemCount: items.length, epoch: 1 },
          ],
          bulk: true,
          epoch: 1,
        });
      }
      await vi.advanceTimersByTimeAsync(700);
    };
    const emitDeployments = (events: any[]) => {
      for (const h of ws.handlers.get(
        `items:${CLUSTER}:apps:v1:deployments:`,
      ) || [])
        h({ isBatch: true, events });
    };
    const deployment = (conditions: any[]) => ({
      ...item('api', '5'),
      kind: 'Deployment',
      apiVersion: 'apps/v1',
      conditions,
    });

    it('changes only details of the listed type', async () => {
      const store = makeStore();
      store.getState().updateCurrentTabState({
        detailTabs: [
          detailTab('Service', 'v1', services),
          detailTab('Deployment', 'apps/v1', deployments),
        ],
        detailData: detailTab('Service', 'v1', services).item,
      });
      await openDeployments(store, [deployment([])]);
      const failing = [{ type: 'Available', status: 'False' }];
      emitDeployments([{ action: 'modified', item: deployment(failing) }]);
      await vi.advanceTimersByTimeAsync(200);
      let state = store.getState().getCurrentTabState();
      expect(state.detailTabs[0].item.status.conditions).toEqual([]);
      expect(state.detailData.status.conditions).toEqual([]);
      expect(state.detailTabs[1].item.status.conditions).toEqual(failing);

      emitDeployments([{ action: 'deleted', item: deployment(failing) }]);
      await vi.advanceTimersByTimeAsync(200);
      state = store.getState().getCurrentTabState();
      expect(state.detailTabs[0].isDeleted).toBeFalsy();
      expect(state.detailTabs[1].isDeleted).toBe(true);
    });

    it('announces one change per open object and none for a snapshot', async () => {
      const store = makeStore();
      store.getState().updateCurrentTabState({
        detailTabs: [detailTab('Deployment', 'apps/v1', deployments)],
      });
      const dispatched: any[] = [];
      (globalThis as any).dispatchEvent = (e: any) => {
        dispatched.push(e.detail);
        return true;
      };
      await openDeployments(store, [
        ...Array.from({ length: 50 }, (_, i) => ({
          ...item(`d${i}`),
          kind: 'Deployment',
        })),
        deployment([]),
      ]);
      expect(dispatched).toEqual([]);
      emitDeployments([
        {
          action: 'modified',
          item: deployment([{ type: 'Progressing', status: 'True' }]),
        },
        {
          action: 'modified',
          item: deployment([{ type: 'Available', status: 'True' }]),
        },
        {
          action: 'modified',
          item: { ...item('other', '3'), kind: 'Deployment' },
        },
      ]);
      await vi.advanceTimersByTimeAsync(200);
      expect(dispatched).toEqual([{ name: 'api', namespace: 'ns' }]);
    });
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
