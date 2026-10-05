import { describe, expect, it, vi } from 'vitest';

const storage = new Map<string, string>([
  ['kanivet.activeClusters', JSON.stringify(['plain', 'withnats'])],
  ['kanivet.currentTab', 'plain'],
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
  getNatsDetection: vi.fn(async (cluster: string) => ({ installed: cluster === 'withnats' })),
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

const treeOf = (cluster: string) =>
  useStore.getState().activeTabs.find((t) => t.id === cluster)!.state.treeData;
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('the NATS entry in the sidebar', () => {
  it('is never listed for a cluster without NATS, not even while detection is running', async () => {
    useStore.getState().hydrateFromStorage();
    const load = useStore.getState().loadTreeData('plain');
    await load;
    expect(treeOf('plain').some((n) => n.id === 'nats-monitoring')).toBe(false);
    await settle();
    expect(treeOf('plain').some((n) => n.id === 'nats-monitoring')).toBe(false);
  });

  it('appears right after Helm Releases once NATS is detected', async () => {
    await useStore.getState().loadTreeData('withnats');
    await settle();
    const ids = treeOf('withnats').map((n) => n.id);
    expect(ids.indexOf('nats-monitoring')).toBe(ids.indexOf('helm-releases') + 1);
  });

  it('stays in a tree that is rebuilt, instead of dropping out until detection answers again', async () => {
    api.getNatsDetection.mockImplementationOnce(() => new Promise(() => {}));
    forgetTreeLoad('withnats');
    await useStore.getState().loadTreeData('withnats');
    expect(treeOf('withnats').some((n) => n.id === 'nats-monitoring')).toBe(true);
  });

  it('keeps what was known when a check fails', async () => {
    api.getNatsDetection.mockRejectedValueOnce(new Error('proxy timeout'));
    forgetTreeLoad('withnats');
    await useStore.getState().loadTreeData('withnats');
    await settle();
    expect(treeOf('withnats').some((n) => n.id === 'nats-monitoring')).toBe(true);
  });

  it('is left out on a host that has virtual clusters', async () => {
    api.listVClusters.mockResolvedValueOnce([{ name: 'v1' }] as any);
    forgetTreeLoad('withnats');
    await useStore.getState().loadTreeData('withnats');
    await settle();
    expect(treeOf('withnats').some((n) => n.id === 'nats-monitoring')).toBe(false);
  });
});
