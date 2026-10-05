import { describe, expect, it, vi } from 'vitest';

const storage = new Map<string, string>();
(globalThis as any).localStorage = {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => storage.set(key, value),
  removeItem: (key: string) => storage.delete(key),
  key: (index: number) => [...storage.keys()][index] ?? null,
  get length() {
    return storage.size;
  },
};
(globalThis as any).window = new EventTarget();

const pod = {
  kind: 'Pod',
  apiVersion: 'v1',
  metadata: { name: 'web-1', namespace: 'prod', uid: 'u1' },
  spec: { containers: [{ name: 'app' }] },
};
const secretRef = { name: 'db', namespace: 'prod', uid: 's1', kind: 'Secret', apiVersion: 'v1' };

const api = vi.hoisted(() => ({
  getCategories: vi.fn(async () => []),
  getResources: vi.fn(async () => []),
  listVClusters: vi.fn(async () => []),
  getNatsDetection: vi.fn(async () => ({ installed: false })),
  getClusterStatus: vi.fn(async () => ({ healthy: true })),
  registerActiveCluster: vi.fn(),
  indexCluster: vi.fn(async () => {}),
  invalidateCache: vi.fn(),
  getResourceDetails: vi.fn(async (_c: string, _g: string, _v: string, kind: string, ns: string, name: string) => {
    if (name === 'gone') throw Object.assign(new Error('not found'), { response: { status: 404 } });
    if (kind === 'Secret') return { kind: 'Secret', apiVersion: 'v1', metadata: { name, namespace: ns }, data: { k: 'v' } };
    return { ...pod, metadata: { ...pod.metadata, name, namespace: ns } };
  }),
}));
vi.mock('../services/api', () => ({ default: api }));
vi.mock('../services/islandNotifications', () => ({}));
vi.mock('../services/cloudService', () => ({ default: {}, SSOLoginRequiredError: class extends Error {} }));

const snapshot = {
  v: 2,
  resourceListTabs: [{ id: 'l1', title: 'pods', resource: { name: 'pods', group: '', version: 'v1', kind: 'Pod', namespaced: true }, items: [], selectedItem: { name: 'web-1', namespace: 'prod' }, cluster: 'c1', selectedNamespaces: ['prod'], sortBy: 'name', sortOrder: 'asc', isPinned: true }],
  activeResourceListTab: 'l1',
  activeResourceListTabByPane: { root: 'l1', right: 'logs-gone-1' },
  detailTabs: [
    { id: 'd1', title: 'web-1', resource: { name: 'pods', group: '', version: 'v1', kind: 'Pod' }, item: { name: 'web-1', namespace: 'prod', kind: 'Pod' }, cluster: 'c1', isPinned: true, location: 'detail' },
    { id: 'd2', title: 'db', resource: { name: 'secrets', group: '', version: 'v1', kind: 'Secret' }, item: secretRef, cluster: 'c1', isPinned: true, location: 'detail' },
    { id: 'd3', title: 'gone', resource: { name: 'pods', group: '', version: 'v1', kind: 'Pod' }, item: { name: 'gone', namespace: 'prod', kind: 'Pod' }, cluster: 'c1', isPinned: true, location: 'detail' },
  ],
  activeDetailTab: 'd1',
  focusedCenterPaneId: 'root',
  expandedNodes: ['workloads'],
  selectedNode: { id: 'workloads-core-v1-pods', label: 'pods', type: 'resource', data: { name: 'pods', group: '', version: 'v1', kind: 'Pod', namespaced: true } },
  searchQuery: 'we',
  listFilter: 'web',
  isDetailsPanelCollapsed: true,
  bottomTabs: [
    { id: 'logs-web-1-1', type: 'logs', title: 'web-1 (prod)', cluster: 'c1', location: 'bottom', ref: { apiVersion: 'v1', kind: 'Pod', name: 'web-1', namespace: 'prod' } },
    { id: 'logs-gone-1', type: 'logs', title: 'gone', cluster: 'c1', location: 'center', paneId: 'right', ref: { apiVersion: 'v1', kind: 'Pod', name: 'gone', namespace: 'prod' } },
    { id: 'trace-1', type: 'trace', title: 'trace', cluster: 'c1', location: 'bottom', ref: { apiVersion: 'pkg.crossplane.io/v1', kind: 'Provider', name: 'p', namespace: '' } },
  ],
  activeBottomTab: 'logs-web-1-1',
};
storage.set('kanivet.activeClusters', JSON.stringify(['c1']));
storage.set('kanivet.currentTab', 'c1');
storage.set('kanivet.tabstate.c1', JSON.stringify(snapshot));
storage.set('kanivet.tabstate.closed', JSON.stringify(snapshot));
storage.set('kanivet.lastResource.closed', '{}');

const { useStore } = await import('./index');
const tabState = () => useStore.getState().activeTabs.find((t) => t.id === 'c1')!.state;
const settle = () => new Promise((resolve) => setTimeout(resolve, 10));

describe('reopening the app', () => {
  it('brings the workspace back where it was left', async () => {
    useStore.getState().hydrateFromStorage();
    const s = tabState();

    expect(useStore.getState().currentTab).toBe('c1');
    expect(s.activeResourceListTab).toBe('l1');
    expect(s.resourceListTabs[0]).toMatchObject({ isPinned: true, sortBy: 'name', selectedNamespaces: ['prod'] });
    expect(s.detailTabs.map((d) => d.id)).toEqual(['d1', 'd2', 'd3']);
    expect(s.activeDetailTab).toBe('d1');
    expect([...s.expandedNodes]).toEqual(['workloads']);
    expect(s.selectedNode?.id).toBe('workloads-core-v1-pods');
    expect(s.searchQuery).toBe('we');
    expect(s.listFilter).toBe('web');
    expect(s.isDetailsPanelCollapsed).toBe(true);
  });

  it('forgets saved state of clusters that are not open', () => {
    expect(storage.has('kanivet.tabstate.closed')).toBe(false);
    expect(storage.has('kanivet.lastResource.closed')).toBe(false);
    expect(storage.has('kanivet.tabstate.c1')).toBe(true);
  });

  it('loads the objects behind every restored detail tab, not only the open one', async () => {
    useStore.getState().restoreWorkspaceContent('c1');
    await settle();
    const tabs = tabState().detailTabs;
    expect(tabs.find((d) => d.id === 'd1')!.item.spec).toBeDefined();
    expect(tabs.find((d) => d.id === 'd2')!.item.data).toEqual({ k: 'v' });
    expect(tabState().detailData?.metadata?.name).toBe('web-1');
    // A panel that was collapsed stays collapsed.
    expect(tabState().isDetailsPanelCollapsed).toBe(true);
  });

  it('marks a tab whose object is gone instead of leaving it blank', () => {
    expect(tabState().detailTabs.find((d) => d.id === 'd3')!.isDeleted).toBe(true);
  });

  it('reopens the dock and drops what no longer exists, with the pane that pointed at it', () => {
    const dock = useStore.getState().bottomTabs;
    expect(dock.map((b) => b.id)).toEqual(['logs-web-1-1', 'trace-1']);
    expect(dock[0].resource.spec.containers).toHaveLength(1);
    expect(useStore.getState().activeBottomTab).toBe('logs-web-1-1');
    expect(tabState().activeResourceListTabByPane).toEqual({ root: 'l1', right: null });
  });

  it('restores a cluster only once', async () => {
    const calls = api.getResourceDetails.mock.calls.length;
    useStore.getState().restoreWorkspaceContent('c1');
    await settle();
    expect(api.getResourceDetails.mock.calls.length).toBe(calls);
  });

  it('saves the workspace again in the current format', () => {
    const saved = JSON.parse(storage.get('kanivet.tabstate.c1')!);
    expect(saved.v).toBe(2);
    expect(JSON.stringify(saved)).not.toContain('"k":"v"');
  });
});
