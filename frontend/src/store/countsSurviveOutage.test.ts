import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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

const workloads = (deployments: number | null) => [
  { name: 'pods', group: '', version: 'v1', kind: 'Pod', namespaced: true, count: 2279 },
  { name: 'deployments', group: 'apps', version: 'v1', kind: 'Deployment', namespaced: true, count: deployments, countPending: deployments === null },
];

const api = vi.hoisted(() => ({
  getCategories: vi.fn(async () => [
    { id: 'workloads', name: 'Workloads' },
    { id: 'cluster', name: 'Cluster' },
  ]),
  getResources: vi.fn(async (): Promise<any[]> => []),
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

const deploymentsCount = () =>
  useStore.getState().activeTabs[0].state.treeData
    .find((node) => node.id === 'workloads')
    ?.children?.find((node) => node.label === 'deployments')?.count;

const expandWorkloads = () =>
  useStore.getState().expandNode('c1', 'workloads', 'category', { categoryId: 'workloads' });

describe('sidebar counts through a credentials outage', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps the known number while the backend cannot count, and asks again until it can', async () => {
    api.getResources.mockResolvedValue(workloads(79));
    useStore.getState().hydrateFromStorage();
    await useStore.getState().loadTreeData('c1');
    await vi.advanceTimersByTimeAsync(0);
    expect(deploymentsCount()).toBe(79);

    // Credentials expire: the backend still answers, with the count unknown.
    api.getResources.mockResolvedValue(workloads(null));
    await expandWorkloads();
    await vi.advanceTimersByTimeAsync(0);
    expect(deploymentsCount()).toBe(79);

    // Still expired at the first retry; back by the second.
    const calls = api.getResources.mock.calls.length;
    await vi.advanceTimersByTimeAsync(2_000);
    expect(api.getResources.mock.calls.length).toBeGreaterThan(calls);
    expect(deploymentsCount()).toBe(79);

    api.getResources.mockResolvedValue(workloads(81));
    await vi.advanceTimersByTimeAsync(5_000);
    expect(deploymentsCount()).toBe(81);

    // Known again: nothing more is asked.
    const settled = api.getResources.mock.calls.length;
    await vi.advanceTimersByTimeAsync(600_000);
    expect(api.getResources.mock.calls.length).toBe(settled);
  });

  it('asks again at once when credentials change', async () => {
    api.getResources.mockResolvedValue(workloads(null));
    await expandWorkloads();
    await vi.advanceTimersByTimeAsync(0);

    api.getResources.mockResolvedValue(workloads(90));
    (globalThis as any).window.dispatchEvent(new Event('cloud:auth-changed'));
    await vi.advanceTimersByTimeAsync(0);
    expect(deploymentsCount()).toBe(90);
  });

  it('does not chase a count the cluster will never have', async () => {
    // Gateway API is not installed: no count, and not marked pending.
    api.getResources.mockResolvedValue([
      ...workloads(79),
      { name: 'gateways', group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespaced: true, count: null },
    ]);
    await expandWorkloads();
    await vi.advanceTimersByTimeAsync(0);
    const calls = api.getResources.mock.calls.length;
    await vi.advanceTimersByTimeAsync(600_000);
    expect(api.getResources.mock.calls.length).toBe(calls);
  });

  it('stops asking once the category is collapsed', async () => {
    api.getResources.mockResolvedValue(workloads(null));
    await expandWorkloads();
    await vi.advanceTimersByTimeAsync(0);
    useStore.setState((state) => {
      const tabs = [...state.activeTabs];
      tabs[0] = { ...tabs[0], state: { ...tabs[0].state, expandedNodes: new Set() } };
      return { activeTabs: tabs };
    });
    await vi.advanceTimersByTimeAsync(2_000);
    const calls = api.getResources.mock.calls.length;
    await vi.advanceTimersByTimeAsync(600_000);
    expect(api.getResources.mock.calls.length).toBe(calls);
  });
});
