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
}));
vi.mock('../services/api', () => ({ default: api }));
vi.mock('../services/islandNotifications', () => ({}));
vi.mock('../services/cloudService', () => ({
  default: {},
  SSOLoginRequiredError: class extends Error {},
}));

const { useStore } = await import('./index');

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
});
