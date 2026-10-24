import { beforeEach, describe, it, expect, vi } from 'vitest';
import { create } from 'zustand';

const api = vi.hoisted(() => ({
  calls: [] as string[],
  details: null as null | ((value: any) => void),
  getResourceDetails: null as any,
  getResourceEvents: null as any,
}));

vi.mock('../services/api', () => ({
  default: {
    getResourceDetails: (...args: any[]) => api.getResourceDetails(...args),
    getResourceEvents: (...args: any[]) => api.getResourceEvents(...args),
  },
}));
vi.mock('../services/islandNotifications', () => ({
  notifyDrainComplete: () => {},
  notifyRolloutComplete: () => {},
}));

const { createResourceSlice } = await import('./resourceSlice');

const podResource = { group: '', version: 'v1', kind: 'Pod', name: 'pods' };
const notFound = () => Object.assign(new Error('not found'), { response: { status: 404 } });

const makeStore = (state: any) =>
  create<any>()((set, get, storeApi) => ({
    currentTab: 'c1',
    activeTabs: [{ id: 'c1', name: 'c1', state }],
    getCurrentTabState: () => get().activeTabs.find((t: any) => t.id === get().currentTab)?.state,
    updateCurrentTabState: (updates: any) =>
      set((s: any) => ({ activeTabs: s.activeTabs.map((t: any) => (t.id === s.currentTab ? { ...t, state: { ...t.state, ...updates } } : t)) })),
    ...createResourceSlice(set, get, storeApi),
  }));

beforeEach(() => {
  api.calls = [];
  api.getResourceDetails = vi.fn(() => {
    api.calls.push('details');
    return new Promise((resolve) => {
      api.details = resolve;
    });
  });
  api.getResourceEvents = vi.fn(async () => {
    api.calls.push('events');
    return [{ reason: 'Started' }];
  });
});

describe('loadDetails', () => {
  it('asks for the events together with the details and shows both', async () => {
    const pod = { kind: 'Pod', metadata: { name: 'api-0', namespace: 'ns' } };
    const store = makeStore({ detailData: pod, detailTabs: [{ id: 'dt-1', item: pod }], activeDetailTab: 'dt-1' });
    const loading = store.getState().loadDetails('c1', podResource, { name: 'api-0', namespace: 'ns' });
    // The events request is out before the details have answered.
    expect([...api.calls].sort()).toEqual(['details', 'events']);
    api.details!({ ...pod, spec: { containers: [] } });
    await loading;
    await Promise.resolve();
    const state = store.getState().activeTabs[0].state;
    expect(state.detailData.spec).toEqual({ containers: [] });
    expect(state.detailData.events).toEqual([{ reason: 'Started' }]);
    expect(state.detailTabs[0].item.events).toEqual([{ reason: 'Started' }]);
  });

  it('asks for the events of this object only: its uid, Kind and apiVersion', async () => {
    const pod = { kind: 'Pod', metadata: { name: 'api-0', namespace: 'ns', uid: 'u1' } };
    const store = makeStore({ detailTabs: [{ id: 'dt', item: pod }], activeDetailTab: 'dt' });
    const loading = store.getState().loadDetails('c1', podResource, { name: 'api-0', namespace: 'ns', uid: 'u1' });
    expect(api.getResourceEvents).toHaveBeenCalledWith('c1', '', 'v1', 'Pod', 'ns', 'api-0', undefined, { uid: 'u1', kind: 'Pod', apiVersion: 'v1' });
    api.details!({ ...pod, spec: {} });
    await loading;
  });

  it('marks the tab deleted when the object is gone, instead of keeping the list-row placeholder', async () => {
    const row = { kind: 'Pod', metadata: { name: 'api-0', namespace: 'ns' } };
    api.getResourceDetails = vi.fn(async () => { throw notFound(); });
    const store = makeStore({ detailData: row, detailTabs: [{ id: 'dt', item: row }], activeDetailTab: 'dt' });
    await expect(store.getState().loadDetails('c1', podResource, { name: 'api-0', namespace: 'ns' })).resolves.toBeUndefined();
    expect(store.getState().activeTabs[0].state.detailTabs[0].isDeleted).toBe(true);
  });
});

describe('refreshDetails', () => {
  const pending = { kind: 'Pod', apiVersion: 'v1', metadata: { name: 'api-0', namespace: 'ns', uid: 'u1' }, spec: { containers: [{ name: 'app' }] }, status: { phase: 'Pending' } };
  const running = { ...pending, status: { phase: 'Running', containerStatuses: [{ name: 'app', restartCount: 2 }] } };

  it('reads past the backend cache and puts the new state in the tab the panel renders', async () => {
    api.getResourceDetails = vi.fn(async () => running);
    api.getResourceEvents = vi.fn(async () => [{ reason: 'Pulled' }]);
    const store = makeStore({ detailData: pending, detailTabs: [{ id: 'dt', item: pending, cluster: 'c1' }], activeDetailTab: 'dt', isDetailsPanelCollapsed: false });
    await store.getState().refreshDetails('c1', podResource, pending);
    expect(api.getResourceDetails).toHaveBeenCalledWith('c1', '', 'v1', 'Pod', 'ns', 'api-0', undefined, { fresh: true });
    const state = store.getState().activeTabs[0].state;
    expect(state.detailTabs[0].item.status.phase).toBe('Running');
    expect(state.detailTabs[0].item.status.containerStatuses[0].restartCount).toBe(2);
    expect(state.detailData.status.phase).toBe('Running');
    // The events are loaded again on the same tick, for this uid.
    expect(api.getResourceEvents).toHaveBeenCalledWith('c1', '', 'v1', 'Pod', 'ns', 'api-0', undefined, { uid: 'u1', kind: 'Pod', apiVersion: 'v1' });
    expect(state.detailTabs[0].item.events).toEqual([{ reason: 'Pulled' }]);
  });

  it('marks the tab deleted on a 404', async () => {
    api.getResourceDetails = vi.fn(async () => { throw notFound(); });
    const store = makeStore({ detailData: pending, detailTabs: [{ id: 'dt', item: pending, cluster: 'c1' }], activeDetailTab: 'dt' });
    await store.getState().refreshDetails('c1', podResource, pending);
    const dt = store.getState().activeTabs[0].state.detailTabs[0];
    expect(dt.isDeleted).toBe(true);
    // The last copy stays readable under the banner.
    expect(dt.item).toBe(pending);
  });
});

describe('updateDetailData', () => {
  it('writes into the tab showing that object, by identity', () => {
    const service = { kind: 'Service', apiVersion: 'v1', metadata: { name: 'api', namespace: 'ns' } };
    const deployment = { kind: 'Deployment', apiVersion: 'apps/v1', metadata: { name: 'api', namespace: 'ns' } };
    const store = makeStore({ detailData: service, detailTabs: [{ id: 'svc', item: service }, { id: 'dep', item: deployment }], activeDetailTab: 'svc' });
    const saved = { ...deployment, spec: { replicas: 3 } };
    store.getState().updateDetailData(saved, 'c1');
    const state = store.getState().activeTabs[0].state;
    expect(state.detailTabs[1].item).toBe(saved);
    expect(state.detailTabs[0].item).toBe(service);
    expect(state.detailData).toBe(service);
  });
});

describe('reloadListItems', () => {
  it('does not put the rows being reloaded back into the list tab as if they were current', async () => {
    vi.stubGlobal('window', {});
    const updateResourceListTab = vi.fn();
    const store = create<any>()((set, get, storeApi) => ({
      currentTab: 'c1',
      activeTabs: [{ id: 'c1', state: {
        selectedNode: { type: 'resource', data: { ...podResource, namespaced: false } },
        listItems: [{ name: 'old-row' }],
        activeResourceListTab: 'list-1',
        resourceListTabs: [{ id: 'list-1', items: [{ name: 'old-row' }] }],
        detailTabs: [],
      } }],
      getCurrentTabState: () => get().activeTabs[0].state,
      updateCurrentTabState: (updates: any) => set((s: any) => ({ activeTabs: [{ ...s.activeTabs[0], state: { ...s.activeTabs[0].state, ...updates } }] })),
      releaseRealtimeTopics: vi.fn(),
      startRealtime: vi.fn(),
      updateResourceListTab,
      ...createResourceSlice(set, get, storeApi),
    }));
    await store.getState().reloadListItems();
    expect(updateResourceListTab).toHaveBeenCalledWith('list-1', { items: [] });
    expect(updateResourceListTab).not.toHaveBeenCalledWith('list-1', { items: [{ name: 'old-row' }] });
    expect(store.getState().activeTabs[0].state.listItems).toEqual([]);
    vi.unstubAllGlobals();
  });
});
