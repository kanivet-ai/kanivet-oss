import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { create } from 'zustand';
import { createInitialTabState } from './utils';
import { pageResources } from './navigationTargets';

vi.mock('./realtimeSlice', () => ({
  itemsTopic: (cluster: string, resource: any) => `${cluster}:${resource.kind}`,
  liveItemsFor: () => undefined,
}));

const { createResourceListTabSlice } = await import('./resourceListTabSlice');

beforeEach(() => vi.stubGlobal('window', {}));
afterEach(() => vi.unstubAllGlobals());

const makeStore = () =>
  create<any>()((set, get, api) => ({
    currentTab: 'c1',
    activeTabs: [{ id: 'c1', state: createInitialTabState() }],
    selectNode: (node: any) => set((state: any) => ({
      activeTabs: state.activeTabs.map((tab: any) => tab.id === get().currentTab ? { ...tab, state: { ...tab.state, selectedNode: node } } : tab),
    })),
    startRealtime: vi.fn(),
    releaseRealtimeTopics: vi.fn(),
    ...createResourceListTabSlice(set, get, api),
  }));

// Each new page, with the sidebar node type its tab must select.
const pages: [string, string][] = [
  ['issues', 'issues'], ['audit', 'audit'], ['changes', 'changes'], ['topology', 'topology'],
  ['network', 'network'], ['access', 'access'], ['certificates', 'certificates'],
  ['upgrade-readiness', 'upgrade-readiness'], ['gitops', 'gitops'], ['cnpg-overview', 'cnpg-overview'],
  ['compare', 'compare'],
];

it.each(pages)('opening the %s page from anywhere selects its sidebar node', async (page, type) => {
  const store = makeStore();
  // The user was on Issues; a button (not the tree) opens the other page.
  store.getState().selectNode({ id: 'cluster-issues', type: 'issues' });
  await store.getState().openResourceListTab(pageResources[page], 'c1');
  expect(store.getState().activeTabs[0].state.selectedNode.type).toBe(type);
});

it('switching back to an open page tab re-selects its node', async () => {
  const store = makeStore();
  await store.getState().openResourceListTab(pageResources.issues, 'c1', true);
  await store.getState().openResourceListTab(pageResources.audit, 'c1', true);
  expect(store.getState().activeTabs[0].state.selectedNode.id).toBe('cluster-audit');
  const issuesTab = store.getState().activeTabs[0].state.resourceListTabs.find((t: any) => t.resource.kind === 'HealthIssues');
  store.getState().setActiveResourceListTab(issuesTab.id);
  expect(store.getState().activeTabs[0].state.selectedNode.id).toBe('cluster-issues');
});
