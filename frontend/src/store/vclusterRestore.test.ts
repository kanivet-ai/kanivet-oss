import { describe, expect, it, vi } from 'vitest';

const HOST = 'arn:aws:eks:eu-north-1:1:cluster/host';
const VC = `vcluster:${HOST}:beige:beige-vcluster`;

const storage = new Map<string, string>([
  ['kanivet.activeClusters', JSON.stringify([HOST, VC])],
  ['kanivet.currentTab', VC],
]);
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

const order: string[] = [];
const connect = vi.hoisted(() => ({ release: null as null | (() => void), fail: false }));
const api = vi.hoisted(() => ({
  getCategories: vi.fn(async (cluster: string) => {
    order.push(`categories:${cluster}`);
    return [];
  }),
  getResources: vi.fn(async () => []),
  getNamespaces: vi.fn(async () => []),
  listVClusters: vi.fn(async () => []),
  getNatsDetection: vi.fn(async () => ({ installed: false })),
  getClusterStatus: vi.fn(async (cluster: string) => {
    order.push(`status:${cluster}`);
    return { healthy: true };
  }),
  registerActiveCluster: vi.fn(),
  indexCluster: vi.fn(async (cluster: string) => {
    order.push(`index:${cluster}`);
  }),
  invalidateCache: vi.fn(),
  connectVCluster: vi.fn(
    () =>
      new Promise((resolve, reject) => {
        order.push('connect');
        connect.release = () => (connect.fail ? reject(new Error('host down')) : resolve({ id: VC }));
      }),
  ),
}));
vi.mock('../services/api', () => ({ default: api }));
vi.mock('../services/islandNotifications', () => ({}));
vi.mock('../services/cloudService', () => ({ default: {}, SSOLoginRequiredError: class extends Error {} }));

const { useStore } = await import('./index');
const settle = () => new Promise((resolve) => setTimeout(resolve, 10));

describe('a vcluster tab restored after a restart', () => {
  it('reconnects once, and loads nothing from it before it is connected', async () => {
    useStore.getState().hydrateFromStorage();
    await settle();

    expect(api.connectVCluster).toHaveBeenCalledTimes(1);
    expect(api.connectVCluster).toHaveBeenCalledWith(HOST, 'beige', 'beige-vcluster');
    expect(order.some((o) => o.endsWith(VC))).toBe(false);
    // The host cluster does not wait for it.
    expect(order).toContain(`status:${HOST}`);

    connect.release!();
    await settle();
    expect(order).toContain(`status:${VC}`);
    expect(order).toContain(`index:${VC}`);
    expect(order).toContain(`categories:${VC}`);
    expect(order.indexOf('connect')).toBeLessThan(order.indexOf(`status:${VC}`));
  });

  it('holds the list load until the connection is made, and does not reconnect again', async () => {
    const loaded = await useStore.getState().loadListItems(VC, { name: 'deployments', group: 'apps', version: 'v1', namespaced: true });
    expect(loaded).toBe(true);
    expect(api.connectVCluster).toHaveBeenCalledTimes(1);
  });
});

describe('a vcluster that cannot be reconnected', () => {
  it('reports it and lets later loads through instead of blocking them', async () => {
    vi.resetModules();
    const toasts: string[] = [];
    (globalThis as any).window.addEventListener('toast:error', (e: any) => toasts.push(e.detail.message));
    connect.fail = true;
    const gateModule = await import('./vclusterRestore');
    gateModule.reconnectRestoredVCluster(VC);
    const gate = gateModule.vclusterGate(VC)!;
    connect.release!();
    expect(await gate).toBe(false);
    await settle();
    expect(toasts[0]).toContain('beige-vcluster');
    expect(gateModule.vclusterGate(VC)).toBeUndefined();
  });

  it('has nothing to wait for on an ordinary cluster', async () => {
    const gateModule = await import('./vclusterRestore');
    expect(gateModule.vclusterGate(HOST)).toBeUndefined();
    gateModule.reconnectRestoredVCluster(HOST);
    expect(gateModule.vclusterGate(HOST)).toBeUndefined();
  });
});
