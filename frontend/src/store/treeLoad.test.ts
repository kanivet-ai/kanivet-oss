import { describe, expect, it, vi } from 'vitest';

// A restored session: cluster c1 was open with its Workloads category expanded.
const storage = new Map<string, string>([
  ['kanivet.activeClusters', JSON.stringify(['c1'])],
  ['kanivet.currentTab', 'c1'],
  ['kanivet.tabstate.c1', JSON.stringify({ expandedNodes: ['workloads'] })],
]);
(globalThis as any).localStorage = {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => storage.set(key, value),
  removeItem: (key: string) => storage.delete(key),
};
(globalThis as any).window = new EventTarget();

const api = vi.hoisted(() => ({
  getCategories: vi.fn(async () => [
    { id: 'workloads', name: 'Workloads' },
    { id: 'cluster', name: 'Cluster' },
  ]),
  getResources: vi.fn(async () => []),
  listVClusters: vi.fn(async () => []),
  getClusterStatus: vi.fn(async () => ({ healthy: true })),
  registerActiveCluster: vi.fn(),
  indexCluster: vi.fn(async () => {}),
  invalidateCache: vi.fn(),
}));
vi.mock('../services/api', () => ({ default: api }));
vi.mock('../services/islandNotifications', () => ({}));
vi.mock('../services/cloudService', () => ({
  default: {},
  SSOLoginRequiredError: class extends Error {},
}));

const { useStore } = await import('./index');
const { forgetTreeLoad } = await import('./resourceSlice');

describe('loading a restored cluster tree', () => {
  it('shares one load between hydrate, the sidebar and the restore effect, and expands the saved nodes', async () => {
    useStore.getState().hydrateFromStorage();
    // TreeSidebar's and Layout's effects ask for the same tree right after.
    await Promise.all([
      useStore.getState().loadTreeData('c1'),
      useStore.getState().loadTreeData('c1'),
    ]);

    expect(api.getCategories).toHaveBeenCalledTimes(1);
    expect(api.listVClusters).toHaveBeenCalledTimes(1);
    const tree = useStore.getState().activeTabs[0].state.treeData;
    const workloads = tree.find((node) => node.id === 'workloads');
    expect(workloads?.expanded).toBe(true);
    expect(workloads?.children?.map((node) => node.label)).toContain('pods');
  });

  it('loads again once the shared load has settled', async () => {
    api.getCategories.mockClear();

    await useStore.getState().loadTreeData('c1');

    expect(api.getCategories).toHaveBeenCalledTimes(1);
  });

  it('rewrites a snapshot an earlier release saved with whole objects in it', () => {
    const secret = { kind: 'Secret', apiVersion: 'v1', metadata: { name: 'db', namespace: 'prod' }, data: { password: 'aHVudGVyMg==' } };
    const row = { name: 'db', namespace: 'prod', annotations: { 'kubectl.kubernetes.io/last-applied-configuration': '{"data":{"password":"aHVudGVyMg=="}}' } };
    storage.set('kanivet.tabstate.c1', JSON.stringify({
      expandedNodes: ['workloads'],
      detailTabs: [{ id: 'dt-1', item: secret }],
      resourceListTabs: [{ id: 'lt-1', resource: { name: 'secrets' }, items: [], selectedItem: row }],
    }));

    useStore.getState().hydrateFromStorage();

    const saved = storage.get('kanivet.tabstate.c1')!;
    expect(saved).not.toContain('aHVudGVyMg==');
    expect(JSON.parse(saved).detailTabs[0].item).toEqual({ name: 'db', namespace: 'prod', kind: 'Secret', apiVersion: 'v1' });
  });

  it('starts a load of its own after a retry rebuilt the cluster client', async () => {
    const categories = [{ id: 'workloads', name: 'Workloads' }];
    const answers: Array<() => void> = [];
    api.getCategories.mockClear();
    api.getCategories.mockImplementation(
      () => new Promise((resolve) => answers.push(() => resolve(categories))) as any,
    );
    try {
      // The startup load, hanging on the old client.
      const stale = useStore.getState().loadTreeData('c1');
      forgetTreeLoad('c1');
      const retried = useStore.getState().loadTreeData('c1');
      expect(api.getCategories).toHaveBeenCalledTimes(2);
      expect(api.invalidateCache).toHaveBeenCalledWith('/resources/categories:{"cluster":"c1"}');

      // The stale load settling first must not drop the retry's load, which
      // later callers still join.
      answers[0]();
      await stale;
      const joined = useStore.getState().loadTreeData('c1');
      expect(api.getCategories).toHaveBeenCalledTimes(2);
      answers[1]();
      await Promise.all([retried, joined]);
    } finally {
      api.getCategories.mockReset();
    }
  });
});
