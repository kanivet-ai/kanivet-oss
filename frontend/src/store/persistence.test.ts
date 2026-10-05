import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { create } from 'zustand';

const stored = vi.hoisted(() => {
  const map = new Map<string, string>();
  const g = globalThis as any;
  g.window = g;
  g.addEventListener = () => {};
  g.removeEventListener = () => {};
  g.localStorage = {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => map.set(k, v),
    removeItem: (k: string) => map.delete(k),
    key: () => null,
    get length() {
      return map.size;
    },
  };
  return map;
});

vi.mock('../services/api', () => ({ default: {} }));

const persistence = await import('./persistence');
const { createInitialTabState, rebuildTabIndex } = await import('./utils');
const {
  buildTabSnapshot,
  parseTabSnapshot,
  snapshotToTabState,
  tabStateChanged,
  installPersistence,
  markHydrated,
  restorableBottomTabs,
  SNAPSHOT_VERSION,
} = persistence;

const pod = {
  kind: 'Pod',
  apiVersion: 'v1',
  metadata: { name: 'web-1', namespace: 'prod', uid: 'u1' },
  spec: { containers: [{ name: 'app' }] },
  status: { phase: 'Running' },
};
const secret = {
  kind: 'Secret',
  apiVersion: 'v1',
  metadata: { name: 'db', namespace: 'prod', uid: 's1' },
  data: { password: 'aHVudGVyMg==' },
};
const bottom = (over: any = {}) => ({
  id: 'logs-web-1-1',
  type: 'logs',
  title: 'web-1 (prod)',
  resource: pod,
  cluster: 'c1',
  location: 'bottom',
  ...over,
});

describe('tab snapshot', () => {
  it('round-trips the whole workspace through a restart', () => {
    const state = {
      ...createInitialTabState(),
      resourceListTabs: [
        { id: 'l1', title: 'pods', resource: { name: 'pods', version: 'v1', kind: 'Pod' }, items: [pod], selectedItem: pod, cluster: 'c1', selectedNamespaces: ['prod'], sortBy: 'name', sortOrder: 'asc' as const, isPinned: true },
      ],
      activeResourceListTab: 'l1',
      detailTabs: [{ id: 'd1', title: 'web-1', resource: { name: 'pods' }, item: pod, cluster: 'c1', isPinned: true, location: 'detail' as const }],
      activeDetailTab: 'd1',
      expandedNodes: new Set(['workloads', 'config']),
      selectedNode: { id: 'workloads-core-v1-pods', label: 'pods', type: 'resource' as const, data: { name: 'pods' } },
      searchQuery: 'dep',
      listFilter: 'web',
      isDetailsPanelCollapsed: true,
      selectedNamespace: 'prod',
      selectedNamespaces: ['prod'],
    };
    const json = JSON.stringify(buildTabSnapshot(state, [bottom() as any], 'logs-web-1-1', 'c1'));
    const restored = snapshotToTabState(parseTabSnapshot(json)!);

    expect(restored.activeResourceListTab).toBe('l1');
    expect(restored.resourceListTabs![0]).toMatchObject({ id: 'l1', isPinned: true, sortBy: 'name', items: [] });
    expect(restored.resourceListTabs![0].selectedItem).toEqual({ name: 'web-1', namespace: 'prod', uid: 'u1', kind: 'Pod', apiVersion: 'v1' });
    expect(restored.activeDetailTab).toBe('d1');
    expect([...restored.expandedNodes!]).toEqual(['workloads', 'config']);
    expect(restored.selectedNode!.id).toBe('workloads-core-v1-pods');
    expect(restored.searchQuery).toBe('dep');
    expect(restored.listFilter).toBe('web');
    expect(restored.isDetailsPanelCollapsed).toBe(true);
    expect(restored.selectedNamespaces).toEqual(['prod']);
  });

  it('never writes an object, such as a Secret with its data', () => {
    const state = {
      ...createInitialTabState(),
      detailTabs: [{ id: 'd', title: 'db', resource: {}, item: secret, cluster: 'c1', isPinned: false }],
    };
    const json = JSON.stringify(buildTabSnapshot(state, [bottom({ type: 'edit', resource: secret })] as any, null, 'c1'));
    expect(json).not.toContain('aHVudGVyMg==');
  });

  it('keeps log, edit and trace tabs of the cluster, as references', () => {
    const tabs = [
      bottom(),
      bottom({ id: 'e', type: 'edit' }),
      bottom({ id: 't', type: 'trace' }),
      bottom({ id: 'sh', type: 'shell' }),
      bottom({ id: 'term', type: 'shell', resource: { kind: 'Terminal', metadata: { name: 'Terminal' } } }),
      bottom({ id: 'new', type: 'create', resource: { kind: 'New', metadata: { name: 'new-resource' } } }),
      bottom({ id: 'helm', type: 'edit', resource: { ...pod, _isHelmValues: true } }),
      bottom({ id: 'other', cluster: 'c2' }),
    ] as any;
    const kept = restorableBottomTabs(tabs, 'c1');
    expect(kept.map((b) => b.id)).toEqual(['logs-web-1-1', 'e', 't']);
    expect(kept[0].ref).toEqual({ apiVersion: 'v1', kind: 'Pod', name: 'web-1', namespace: 'prod' });
    expect(JSON.stringify(kept)).not.toContain('containers');
  });

  it('drops the active bottom tab when it is not one that is saved', () => {
    const snap = buildTabSnapshot(createInitialTabState(), [bottom()] as any, 'a-shell', 'c1');
    expect(snap.activeBottomTab).toBeNull();
  });

  it('reads a version 1 snapshot, keeping only references to objects', () => {
    const v1 = JSON.stringify({
      resourceListTabs: [{ id: 'l1', title: 'pods', resource: {}, items: [pod], selectedItem: pod }],
      activeResourceListTab: 'l1',
      detailTabs: [{ id: 'd1', item: secret }],
      expandedNodes: ['a'],
      selectedNode: null,
    });
    const snap = parseTabSnapshot(v1)!;
    expect(snap.v).toBe(SNAPSHOT_VERSION);
    expect(snap.resourceListTabs[0].items).toEqual([]);
    expect(JSON.stringify(snap)).not.toContain('aHVudGVyMg==');
    expect(snap.bottomTabs).toEqual([]);
    expect(snap.listFilter).toBe('');
  });

  it('degrades a corrupt or newer snapshot to nothing instead of throwing', () => {
    expect(parseTabSnapshot(null)).toBeNull();
    expect(parseTabSnapshot('{nope')).toBeNull();
    expect(parseTabSnapshot('[]')).toBeNull();
    expect(parseTabSnapshot(JSON.stringify({ v: SNAPSHOT_VERSION + 1 }))).toBeNull();
    const odd = parseTabSnapshot(JSON.stringify({ resourceListTabs: 'x', detailTabs: [null, 3], bottomTabs: [{ id: 1 }], expandedNodes: [1, 'a'] }))!;
    expect(odd.resourceListTabs).toEqual([]);
    expect(odd.detailTabs).toEqual([]);
    expect(odd.bottomTabs).toEqual([]);
    expect(odd.expandedNodes).toEqual(['a']);
  });
});

describe('change detection', () => {
  const base = () => ({
    ...createInitialTabState(),
    resourceListTabs: [
      { id: 'l1', title: 'pods', resource: {}, items: [pod], selectedItem: pod, cluster: 'c1', selectedNamespaces: [], sortBy: 'age', sortOrder: 'desc' as const, isPinned: false },
    ],
    detailTabs: [{ id: 'd1', title: 'web-1', resource: {}, item: pod, cluster: 'c1', isPinned: false }],
  });

  it('ignores a list or detail refresh that changes no saved field', () => {
    const a = base();
    const b = {
      ...a,
      listItems: [pod, pod],
      detailData: { ...pod },
      resourceListTabs: [{ ...a.resourceListTabs[0], items: [pod, pod], selectedItem: { ...pod, status: { phase: 'Pending' } } }],
      detailTabs: [{ ...a.detailTabs[0], item: { ...pod, status: { phase: 'Pending' } } }],
    };
    expect(tabStateChanged(a, b)).toBe(false);
  });

  it('sees a change to what is saved', () => {
    const a = base();
    expect(tabStateChanged(a, { ...a, listFilter: 'x' })).toBe(true);
    expect(tabStateChanged(a, { ...a, isDetailsPanelCollapsed: true })).toBe(true);
    expect(tabStateChanged(a, { ...a, detailTabs: [] })).toBe(true);
    expect(tabStateChanged(a, { ...a, resourceListTabs: [{ ...a.resourceListTabs[0], sortBy: 'name' }] })).toBe(true);
    expect(tabStateChanged(a, { ...a, resourceListTabs: [{ ...a.resourceListTabs[0], selectedItem: { ...pod, metadata: { ...pod.metadata, name: 'web-2' } } }] })).toBe(true);
  });
});

describe('writer', () => {
  const makeStore = () => {
    const store = create<any>()(() => ({ activeTabs: [], bottomTabs: [], activeBottomTab: null }));
    const tabs = [{ id: 'c1', name: 'c1', state: createInitialTabState() }];
    store.setState({ activeTabs: tabs, tabIndexMap: rebuildTabIndex(tabs) });
    return store;
  };
  const setState = (store: any, patch: any) =>
    store.setState({ activeTabs: [{ id: 'c1', name: 'c1', state: { ...store.getState().activeTabs[0].state, ...patch } }] });

  beforeEach(() => {
    vi.useFakeTimers();
    stored.clear();
  });
  afterEach(() => vi.useRealTimers());

  it('does not write before the stored workspace has been read back', () => {
    const store = makeStore();
    const off = installPersistence(store);
    setState(store, { listFilter: 'x' });
    vi.advanceTimersByTime(5000);
    expect(stored.has('kanivet.tabstate.c1')).toBe(false);
    markHydrated();
    setState(store, { listFilter: 'y' });
    vi.advanceTimersByTime(1500);
    expect(JSON.parse(stored.get('kanivet.tabstate.c1')!).listFilter).toBe('y');
    off();
  });

  it('writes while state keeps changing, instead of waiting for quiet', () => {
    const store = makeStore();
    const off = installPersistence(store, { alreadyHydrated: true });
    for (let i = 0; i < 30; i++) {
      setState(store, { listFilter: `f${i}` });
      vi.advanceTimersByTime(100);
    }
    expect(stored.has('kanivet.tabstate.c1')).toBe(true);
    off();
  });

  it('does not write for list traffic that changes nothing saved', () => {
    const store = makeStore();
    const off = installPersistence(store, { alreadyHydrated: true });
    setState(store, { listItems: [pod], isLoadingListItems: false });
    vi.advanceTimersByTime(3000);
    expect(stored.has('kanivet.tabstate.c1')).toBe(false);
    off();
  });

  it('saves the dock when a tab opens or closes', () => {
    const store = makeStore();
    const off = installPersistence(store, { alreadyHydrated: true });
    store.setState({ bottomTabs: [bottom()], activeBottomTab: 'logs-web-1-1' });
    vi.advanceTimersByTime(1500);
    expect(JSON.parse(stored.get('kanivet.tabstate.c1')!).bottomTabs).toHaveLength(1);
    store.setState({ bottomTabs: [], activeBottomTab: null });
    vi.advanceTimersByTime(1500);
    expect(JSON.parse(stored.get('kanivet.tabstate.c1')!).bottomTabs).toHaveLength(0);
    off();
  });

  it('does not bring a closed tab back', () => {
    const store = makeStore();
    const off = installPersistence(store, { alreadyHydrated: true });
    setState(store, { listFilter: 'x' });
    store.setState({ activeTabs: [] });
    vi.advanceTimersByTime(3000);
    expect(stored.has('kanivet.tabstate.c1')).toBe(false);
    off();
  });

  it('keeps working when storage refuses the write', () => {
    const store = makeStore();
    const off = installPersistence(store, { alreadyHydrated: true });
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const original = (globalThis as any).localStorage.setItem;
    (globalThis as any).localStorage.setItem = () => {
      throw new Error('quota');
    };
    setState(store, { listFilter: 'x' });
    expect(() => vi.advanceTimersByTime(1500)).not.toThrow();
    (globalThis as any).localStorage.setItem = original;
    warn.mockRestore();
    off();
  });
});
