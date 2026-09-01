import { StateCreator } from 'zustand';
import api from '../services/api';
import { TabSlice, StoreState, Tab, TabState } from './types';
import { createInitialTabState, rebuildTabIndex } from './utils';

export const createTabSlice: StateCreator<StoreState, [], [], TabSlice> = (set, get) => ({
  activeTabs: [],
  tabIndexMap: new Map(),
  currentTab: null,

  openTab: async (cluster: string) => {
    const { activeTabs } = get();
    const existingTab = activeTabs.find((t: Tab) => t.id === cluster);
    if (!existingTab) {
      const newTabState = createInitialTabState();
      newTabState.focusArea = 'list';

      const newTabs = [...activeTabs, { id: cluster, name: cluster, state: newTabState }];
      set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: cluster });
      api.registerActiveCluster(cluster);
      api.indexCluster(cluster).catch(() => {});
      try { localStorage.setItem('kanivet.currentTab', cluster); } catch {}
      try { localStorage.setItem('kanivet.activeClusters', JSON.stringify([...activeTabs.map((t) => t.id), cluster])); } catch {}
      get().loadClusterStatus(cluster);

      const openOverview = async () => {
        const tabIndex = get().tabIndexMap.get(cluster) ?? -1;
        if (tabIndex === -1) return;
        const overviewNode = { id: 'cluster-overview', label: 'Overview', type: 'overview' as const, data: { cluster } };
        get().selectNode(overviewNode);
        const dashboardResource = { name: 'cluster-dashboard', group: '', version: 'v1', kind: 'ClusterDashboard', namespaced: false };
        await get().openResourceListTab(dashboardResource, cluster, true, undefined);
      };

      const treeLoaded = get().loadTreeData(cluster).then(() => true, () => false);
      await openOverview();
      if (await treeLoaded) {
        const tabIndex = get().tabIndexMap.get(cluster) ?? -1;
        if (tabIndex !== -1) {
          const categories = get().activeTabs[tabIndex].state.treeData;
          const crossplaneCategory = categories.find((cat) => cat.id === 'crossplane');
          if (crossplaneCategory) api.getResources(cluster, 'crossplane', true).catch(() => {});
          const customCategory = categories.find((cat) => cat.id === 'custom');
          if (customCategory) api.getResources(cluster, 'custom', true).catch(() => {});
          const argoCategory = categories.find((cat) => cat.id === 'argocd');
          if (argoCategory) api.getResources(cluster, 'argocd', true).catch(() => {});
        }
      }
    } else {
      set({ currentTab: cluster });
      try { localStorage.setItem('kanivet.currentTab', cluster); } catch {}
    }
  },

  closeTab: (clusterId: string) => {
    const { activeTabs, currentTab, bottomTabs } = get();
    if (currentTab === clusterId) get().stopRealtime();
    const terminalTabs = bottomTabs.filter(
      (tab) => tab.cluster === clusterId && tab.type === 'shell' && tab.resource.kind === 'Terminal'
    );
    if (terminalTabs.length > 0) {
      import('../services/terminalManager').then(({ default: TerminalManager }) => {
        terminalTabs.forEach((tab) => TerminalManager.getInstance().closeSession(tab.id));
      });
    }
    api.unregisterActiveCluster(clusterId);
    api.releaseCluster(clusterId).catch(() => {});
    const cacheMap: Map<string, any> | undefined = (window as any).__kanivetItemsCache;
    if (cacheMap) {
      for (const topic of [...cacheMap.keys()]) {
        if (topic.startsWith(`items:${clusterId}:`)) cacheMap.delete(topic);
      }
    }
    if (clusterId.startsWith('vcluster:')) {
      api.disconnectVCluster(clusterId).catch(() => {});
    }
    const newTabs = activeTabs.filter((t: Tab) => t.id !== clusterId);
    const newCurrent = currentTab === clusterId ? newTabs[0]?.id || null : currentTab;
    set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: newCurrent });
    try {
      if (newCurrent) localStorage.setItem('kanivet.currentTab', newCurrent);
      else localStorage.removeItem('kanivet.currentTab');
      localStorage.setItem('kanivet.activeClusters', JSON.stringify(newTabs.map((t) => t.id)));
      localStorage.removeItem(`kanivet.tabstate.${clusterId}`);
      localStorage.removeItem(`kanivet.lastResource.${clusterId}`);
      localStorage.removeItem(`kanivet.lastItem.${clusterId}`);
      localStorage.removeItem(`kanivet.lastDetailTab.${clusterId}`);
      localStorage.removeItem(`kanivet.namespace.${clusterId}`);
      localStorage.removeItem(`kanivet.selectedNamespaces.${clusterId}`);
    } catch {}
  },

  setCurrentTab: (tabId: string | null) => {
    const prevTab = get().currentTab;
    if (prevTab && prevTab !== tabId) get().stopRealtime();
    set({ currentTab: tabId });
    try {
      if (tabId) localStorage.setItem('kanivet.currentTab', tabId);
      else localStorage.removeItem('kanivet.currentTab');
    } catch {}
    if (tabId && tabId !== prevTab) {
      const tabState = get().getCurrentTabState();
      if (tabState?.selectedNode?.type === 'resource') get().startRealtime();
    }
  },

  reorderTabs: (fromIndex: number, toIndex: number) => {
    const { activeTabs } = get();
    if (fromIndex === toIndex || fromIndex < 0 || toIndex < 0 || fromIndex >= activeTabs.length || toIndex >= activeTabs.length) return;
    const newTabs = [...activeTabs];
    const [movedTab] = newTabs.splice(fromIndex, 1);
    newTabs.splice(toIndex, 0, movedTab);
    set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs) });
    try { localStorage.setItem('kanivet.activeClusters', JSON.stringify(newTabs.map((t) => t.id))); } catch {}
  },

  getCurrentTabState: () => {
    const { activeTabs, currentTab, tabIndexMap } = get();
    const tabIndex = tabIndexMap.get(currentTab || '') ?? -1;
    return tabIndex !== -1 ? activeTabs[tabIndex]?.state || null : null;
  },

  updateCurrentTabState: (updates: Partial<TabState>) => {
    const { activeTabs, currentTab, tabIndexMap } = get();
    const tabIndex = tabIndexMap.get(currentTab || '') ?? -1;
    if (tabIndex === -1) return;
    const updatedTabs = [...activeTabs];
    const currentState = updatedTabs[tabIndex].state;
    const newState = { ...currentState, ...updates };
    if (currentState.rolloutStatuses && !updates.rolloutStatuses) {
      newState.rolloutStatuses = currentState.rolloutStatuses;
    }
    updatedTabs[tabIndex] = { ...updatedTabs[tabIndex], state: newState };
    set({ activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) });

    // Persist immediately for fields the user expects to survive a reload
    // (namespace selection), but defer the heavy tabstate snapshot.
    try {
      if (currentTab) {
        if (Object.prototype.hasOwnProperty.call(updates, 'selectedNamespace') && updates.selectedNamespace !== undefined) {
          localStorage.setItem(`kanivet.namespace.${currentTab}`, String(updates.selectedNamespace));
        }
        if (Object.prototype.hasOwnProperty.call(updates, 'selectedNamespaces') && updates.selectedNamespaces !== undefined) {
          localStorage.setItem(`kanivet.selectedNamespaces.${currentTab}`, JSON.stringify(updates.selectedNamespaces || []));
        }
        // Skip persistence work entirely when only transient fields changed.
        if (isTransientOnlyUpdate(updates)) return;
        scheduleTabStateSave(currentTab, newState);
      }
    } catch {}
  },
});

// Persistence helpers ---------------------------------------------------------

const TRANSIENT_KEYS = new Set([
  'listItems',
  'selectedItem',
  'detailData',
  'isLoadingListItems',
  'hasReceivedInitialListData',
  'loadError',
  'namespaces',
  'rolloutStatuses',
  'scrollPositions',
  'focusArea',
]);

const isTransientOnlyUpdate = (updates: Partial<TabState>): boolean => {
  for (const key of Object.keys(updates)) {
    if (!TRANSIENT_KEYS.has(key)) return false;
  }
  return true;
};

const pendingSaves = new Map<string, ReturnType<typeof setTimeout>>();
const PERSIST_DEBOUNCE_MS = 750;

const stripItems = (rt: any) => {
  if (!rt) return rt;
  // Drop items + selectedItem to keep the snapshot small and fast to JSON-encode.
  // They are re-fetched on tab open.
  const { items: _items, selectedItem: _sel, ...rest } = rt;
  return rest;
};

const scheduleTabStateSave = (cluster: string, newState: TabState) => {
  const existing = pendingSaves.get(cluster);
  if (existing) clearTimeout(existing);
  const handle = setTimeout(() => {
    pendingSaves.delete(cluster);
    try {
      const toSave = {
        resourceListTabs: (newState.resourceListTabs || []).map(stripItems),
        activeResourceListTab: newState.activeResourceListTab,
        activeResourceListTabByPane: newState.activeResourceListTabByPane || {},
        detailTabs: newState.detailTabs || [],
        activeDetailTab: newState.activeDetailTab,
        centerPaneLayout: newState.centerPaneLayout,
        focusedCenterPaneId: newState.focusedCenterPaneId,
        expandedNodes: Array.from(newState.expandedNodes || []),
        selectedNode: newState.selectedNode ? {
          id: newState.selectedNode.id,
          label: newState.selectedNode.label,
          type: newState.selectedNode.type,
          data: newState.selectedNode.data,
        } : null,
      };
      localStorage.setItem(`kanivet.tabstate.${cluster}`, JSON.stringify(toSave));
    } catch {}
  }, PERSIST_DEBOUNCE_MS);
  pendingSaves.set(cluster, handle);
};

// Flush any pending writes on tab close / app exit
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', () => {
    for (const handle of pendingSaves.values()) clearTimeout(handle);
    pendingSaves.clear();
  });
}
