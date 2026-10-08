import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { create } from 'zustand';
import { createInitialTabState } from './utils';
import { pageResources } from './navigationTargets';

vi.mock('../services/api', () => ({ default: {
  addNavigationEntry: vi.fn(), navigateBack: vi.fn(), navigateForward: vi.fn(),
  getNamespaces: vi.fn().mockResolvedValue(['a', 'b']),
  getResourceDetails: vi.fn(), getResourceEvents: vi.fn().mockResolvedValue([]),
} }));
vi.mock('../services/islandNotifications', () => ({ notifyDrainComplete: vi.fn(), notifyRolloutComplete: vi.fn() }));
vi.mock('./realtimeSlice', () => ({ itemsTopic: (cluster: string, r: any) => `items:${cluster}:${r.group || ''}:${r.version}:${r.name}:`, liveItemsFor: () => undefined }));

const { default: api } = await import('../services/api');
const { createNavigationSlice } = await import('./navigationSlice');
const { createResourceSlice } = await import('./resourceSlice');
const { createResourceListTabSlice } = await import('./resourceListTabSlice');
const { createDetailTabSlice } = await import('./detailTabSlice');
const { createTabSlice } = await import('./tabSlice');
const pod = { name: 'pods', group: '', version: 'v1', kind: 'Pod', namespaced: true };
const node = { name: 'nodes', group: '', version: 'v1', kind: 'Node', namespaced: false };
const entry = (name: string, resource = pod, namespace = 'a') => ({ type: 'item', path: `${namespace}/${name}`, resource, item: { name, namespace } });
let store: ReturnType<typeof create<any>>;
const state = () => store.getState().getCurrentTabState();
const activeList = () => state().resourceListTabs.find((t: any) => t.id === state().activeResourceListTab);
const activeDetail = () => state().detailTabs.find((t: any) => t.id === state().activeDetailTab);

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('window', {});
  vi.stubGlobal('localStorage', { setItem: vi.fn() });
  let entries: any[] = [];
  let index = -1;
  vi.mocked(api.addNavigationEntry).mockImplementation(async (_tab, _cluster, e) => {
    entries = [...entries.slice(0, index + 1), e]; index = entries.length - 1;
  });
  vi.mocked(api.navigateBack).mockImplementation(async () => index > 0 ? entries[--index] : null);
  vi.mocked(api.navigateForward).mockImplementation(async () => index < entries.length - 1 ? entries[++index] : null);
  vi.mocked(api.getResourceDetails).mockImplementation(async (_cluster, group, version, kind, namespace, name) => ({ kind, apiVersion: group ? `${group}/${version}` : version, metadata: { namespace, name }, spec: { containers: [{ name: 'app' }] } }));
  store = create<any>()((set, get, storeApi) => ({
    ...createTabSlice(set, get, storeApi),
    currentTab: 'test', activeTabs: ['test', 'other'].map((id) => ({ id, state: createInitialTabState() })), tabIndexMap: new Map([['test', 0], ['other', 1]]),
    getCurrentTabState: () => get().activeTabs.find((t: any) => t.id === get().currentTab)?.state,
    updateCurrentTabState: (updates: any) => set((s: any) => ({ activeTabs: s.activeTabs.map((t: any) => t.id === s.currentTab ? { ...t, state: { ...t.state, ...updates } } : t) })),
    startRealtime: vi.fn(), parkRealtime: vi.fn(), releaseRealtimeTopics: vi.fn(),
    ...createResourceSlice(set, get, storeApi), ...createResourceListTabSlice(set, get, storeApi),
    ...createDetailTabSlice(set, get, storeApi), ...createNavigationSlice(set, get, storeApi),
  }));
});
afterEach(() => vi.unstubAllGlobals());
async function visit(e: any) {
  await store.getState().restoreNavigationState(e);
  await store.getState().recordNavigation(e.type, e.path, e.resource, e.item);
}

it('restores the visible row and detail sidebar for pod-to-pod Back and Forward, including a cold list', async () => {
  await visit(entry('first')); await visit(entry('second'));
  await store.getState().navigateBack();
  expect(activeList().selectedItem.name).toBe('first');
  expect(state().selectedItem.name).toBe('first');
  expect(state().selectedNode.id).toBe('workloads-core-v1-pods');
  expect(state().activeResourceListTabByPane.root).toBe(activeList().id);
  expect(activeDetail().item.metadata.name).toBe('first');
  expect(activeDetail().item.spec.containers).toHaveLength(1);
  expect(state().isDetailsPanelCollapsed).toBe(false);
  expect(state().focusArea).toBe('list');
  expect(activeList().navigationReveal).toBeGreaterThan(0);
  await store.getState().navigateForward();
  expect(activeList().selectedItem.name).toBe('second');
  expect(activeDetail().item.metadata.name).toBe('second');
  expect(api.addNavigationEntry).toHaveBeenCalledTimes(2);
});

it('restores across resource kinds and reopens a preview list in the focused pane', async () => {
  store.getState().updateCurrentTabState({ centerPaneLayout: { id: 'split', type: 'split', children: [{ id: 'left', type: 'resourceList' }, { id: 'right', type: 'resourceList' }] }, focusedCenterPaneId: 'right' });
  await visit(entry('pod')); await visit(entry('node', node, ''));
  await store.getState().navigateBack();
  expect(activeList().resource.kind).toBe('Pod');
  expect(activeList().paneId).toBe('right');
  expect(state().activeResourceListTabByPane.right).toBe(activeList().id);
  expect(activeDetail().resource.kind).toBe('Pod');
  await store.getState().navigateForward();
  expect(activeList().resource.kind).toBe('Node');
  expect(activeDetail().item.metadata.name).toBe('node');
});

describe.each(Object.entries(pageResources))('%s history', (type, resource) => {
  it('restores the page and returns to the pod without watching a pseudo-resource', async () => {
    await visit(entry('pod')); await visit({ type, path: type, resource: { cluster: 'test' } });
    await store.getState().navigateBack();
    expect(activeList().selectedItem.name).toBe('pod');
    store.getState().startRealtime.mockClear();
    await store.getState().navigateForward();
    expect(activeList().resource.kind).toBe(resource.kind);
    expect(state().selectedNode.type).toBe(type);
    expect(state().activeDetailTab).toBeNull();
    expect(state().isDetailsPanelCollapsed).toBe(true);
    expect(store.getState().startRealtime).not.toHaveBeenCalled();
  });
});

it('reveals an item hidden by a namespace filter and preserves pinned details', async () => {
  await visit(entry('first')); store.getState().pinDetailTab(state().activeDetailTab);
  const pinned = activeDetail();
  store.getState().updateResourceListTab(activeList().id, { selectedNamespaces: ['b'] });
  await visit(entry('second', pod, 'b')); await store.getState().navigateBack();
  expect(activeList().selectedNamespaces).toEqual([]);
  expect(state().selectedNamespaces).toEqual([]);
  expect(state().detailTabs).toContainEqual(pinned);
  expect(activeDetail().item.metadata.name).toBe('first');
});
it('restores a list-only entry without leaving the newer item selected', async () => {
  await visit({ type: 'resource', path: 'pods', resource: pod }); await visit(entry('pod'));
  await store.getState().navigateBack();
  expect(activeList().selectedItem).toBeNull(); expect(state().detailData).toBeNull();
});
it('deduplicates destinations and truncates forward history after a new visit', async () => {
  await visit(entry('first'));
  await store.getState().recordNavigation('resource', 'different-path', pod, { name: 'first', namespace: 'a' });
  expect(api.addNavigationEntry).toHaveBeenCalledTimes(1);
  await visit(entry('second')); await store.getState().navigateBack(); await visit(entry('third'));
  await store.getState().navigateForward(); expect(activeList().selectedItem.name).toBe('third');
});
it('serializes recording and rapid Back/Forward requests', async () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const original = vi.mocked(api.addNavigationEntry).getMockImplementation()!;
  vi.mocked(api.addNavigationEntry).mockImplementationOnce(async (...args) => { await gate; return original(...args); });
  const first = store.getState().recordNavigation('item', 'a/first', pod, { name: 'first', namespace: 'a' });
  const second = store.getState().recordNavigation('item', 'a/second', pod, { name: 'second', namespace: 'a' });
  const back = store.getState().navigateBack(); const forward = store.getState().navigateForward();
  await Promise.resolve(); expect(api.navigateBack).not.toHaveBeenCalled(); release();
  await Promise.all([first, second, back, forward]); expect(activeList().selectedItem.name).toBe('second');
});
it('does not restore an in-flight response into a different cluster', async () => {
  let release!: (value: any) => void;
  vi.mocked(api.navigateBack).mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  const back = store.getState().navigateBack(); await Promise.resolve(); store.setState({ currentTab: 'other' });
  release(entry('first')); await back; expect(state().resourceListTabs).toEqual([]);
});

it('restores Helm release selection and details without fetching a Kubernetes resource', async () => {
  const release = (name: string) => ({ type: 'helm', path: 'helm-releases', resource: pageResources.helm, item: { name, namespace: 'a', kind: 'HelmRelease', apiVersion: 'helm.sh/v1' } });
  await visit(release('first')); await visit(release('second'));
  await store.getState().navigateBack();
  expect(activeList().selectedItem.name).toBe('first');
  expect(activeDetail().item.name).toBe('first');
  await store.getState().navigateForward();
  expect(activeList().selectedItem.name).toBe('second');
  expect(activeDetail().item.name).toBe('second');
  expect(api.getResourceDetails).not.toHaveBeenCalled();
});

it('records cluster switches in one history and restores the original pod and cluster', async () => {
  await store.getState().restoreNavigationState({ type: 'overview', path: 'cluster-overview', clusterId: 'other' });
  store.getState().setCurrentTab('test', false);
  await visit(entry('original'));
  store.getState().setCurrentTab('other');
  await store.getState().navigateBack();
  expect(store.getState().currentTab).toBe('test');
  expect(activeList().selectedItem.name).toBe('original');
  expect(activeDetail().item.metadata.name).toBe('original');
  await store.getState().navigateForward();
  expect(store.getState().currentTab).toBe('other');
  expect(state().selectedNode.type).toBe('overview');
  expect(api.addNavigationEntry).toHaveBeenCalledTimes(2);
  expect(api.addNavigationEntry).toHaveBeenLastCalledWith('workspace', 'other', expect.objectContaining({ clusterId: 'other' }));
});

it('does not deduplicate matching resource names in different clusters', async () => {
  await visit(entry('same'));
  await store.getState().restoreNavigationState({ ...entry('same'), clusterId: 'other' });
  await store.getState().recordCurrentNavigation();
  await store.getState().navigateBack();
  expect(store.getState().currentTab).toBe('test');
  await store.getState().navigateForward();
  expect(store.getState().currentTab).toBe('other');
  expect(activeList().selectedItem.name).toBe('same');
});

it('handles rapid Back and Forward across clusters without recording restoration as a new visit', async () => {
  await visit(entry('first'));
  await store.getState().restoreNavigationState({ ...entry('second'), clusterId: 'other' });
  await store.getState().recordCurrentNavigation();
  store.getState().setCurrentTab('test');
  await visit(entry('third'));
  await Promise.all([store.getState().navigateBack(), store.getState().navigateBack()]);
  expect(store.getState().currentTab).toBe('other');
  expect(activeList().selectedItem.name).toBe('second');
  await Promise.all([store.getState().navigateForward(), store.getState().navigateForward()]);
  expect(store.getState().currentTab).toBe('test');
  expect(activeList().selectedItem.name).toBe('third');
  expect(api.addNavigationEntry).toHaveBeenCalledTimes(4);
});

it('reveals the category and nested API-version group for custom resources', async () => {
  const resource = { name: 'widgets', group: 'example.io', version: 'v1', kind: 'Widget', namespaced: true };
  const resourceNode = { id: 'custom-example.io-v1-widgets', type: 'resource', data: resource };
  store.getState().updateCurrentTabState({ treeData: [{ id: 'custom', type: 'category', expanded: false, children: [{ id: 'custom-example.io-v1', type: 'apiVersion', expanded: false, children: [resourceNode] }] }] });
  await store.getState().restoreNavigationState(entry('widget', resource));
  expect(state().selectedNode.id).toBe(resourceNode.id);
  expect(state().expandedNodes.has('custom')).toBe(true);
  expect(state().expandedNodes.has('custom-example.io-v1')).toBe(true);
});
