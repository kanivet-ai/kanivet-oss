import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { create } from 'zustand';
import { createInitialTabState } from './utils';

vi.mock('./realtimeSlice', () => ({
  itemsTopic: (cluster: string, resource: any) => `${cluster}:${resource.kind}`,
  liveItemsFor: () => undefined,
}));

const { createResourceListTabSlice } = await import('./resourceListTabSlice');
const settings = { name: 'cluster-settings', kind: 'ClusterSettings', group: '', version: 'v1', namespaced: false };
const overview = { name: 'cluster-dashboard', kind: 'ClusterDashboard', group: '', version: 'v1', namespaced: false };

beforeEach(() => vi.stubGlobal('window', {}));
afterEach(() => vi.unstubAllGlobals());

it('opens cluster settings as a reusable page and restores sidebar selection without a resource watch', async () => {
  const startRealtime = vi.fn();
  const store = create<any>()((set, get, api) => ({
    currentTab: 'staging',
    activeTabs: ['staging', 'production'].map((id) => ({ id, state: createInitialTabState() })),
    selectNode: (node: any) => set((state: any) => ({
      activeTabs: state.activeTabs.map((tab: any) => tab.id === get().currentTab ? { ...tab, state: { ...tab.state, selectedNode: node } } : tab),
    })),
    startRealtime,
    releaseRealtimeTopics: vi.fn(),
    ...createResourceListTabSlice(set, get, api),
  }));

  await store.getState().openResourceListTab(settings, 'staging', true);
  const settingsTab = store.getState().activeTabs[0].state.resourceListTabs[0];
  expect(settingsTab.title).toBe('Cluster settings');
  expect(settingsTab.cluster).toBe('staging');
  await store.getState().openResourceListTab(settings, 'staging');
  expect(store.getState().activeTabs[0].state.resourceListTabs).toHaveLength(1);
  expect(store.getState().activeTabs[1].state.resourceListTabs).toHaveLength(0);

  await store.getState().openResourceListTab(overview, 'staging');
  const overviewTab = store.getState().activeTabs[0].state.resourceListTabs.find((tab: any) => tab.resource.kind === 'ClusterDashboard');
  store.getState().closeResourceListTab(overviewTab.id);
  expect(store.getState().activeTabs[0].state.selectedNode.type).toBe('cluster-settings');
  store.getState().setActiveResourceListTab(settingsTab.id);
  expect(startRealtime).not.toHaveBeenCalled();
});
