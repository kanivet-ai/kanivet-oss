import { StateCreator } from 'zustand';
import { getResourceCategory } from '../utils/resourceUtils';
import { resolvePaneId } from '../utils/centerPaneLayout';
import { ResourceListTabSlice, StoreState, ResourceListTab } from './types';
import { itemsTopic, liveItemsFor } from './realtimeSlice';

// A tab's items taken from its still-open subscription when there is one, so an
// activated tab shows current rows at once instead of its last snapshot.
const withLiveItems = (rt: ResourceListTab): ResourceListTab => {
  if (rt.resource.kind === 'ClusterDashboard') return rt;
  const live = liveItemsFor(itemsTopic(rt.cluster, rt.resource));
  return live && live !== rt.items ? { ...rt, items: live } : rt;
};

export const createResourceListTabSlice: StateCreator<StoreState, [], [], ResourceListTabSlice> = (set, get) => ({
  openResourceListTab: async (resource: any, cluster: string, isPinned: boolean = false, paneId?: string) => {
    const { activeTabs, currentTab } = get();
    if (cluster !== currentTab) get().openTab(cluster);
    const tab = activeTabs.find((t) => t.id === cluster);
    if (!tab) return;

    const resourceKind = resource.kind || resource.name || 'Unknown';
    const resourceGroup = resource.group || '';
    const resourceVersion = resource.version || '';
    const title = resourceKind === 'ClusterDashboard' ? 'Overview'
      : resourceKind === 'FinOpsDashboard' ? 'FinOps'
      : resourceKind === 'IncidentTimeline' ? 'Incidents'
      : resourceKind;

    const wantedPaneId = paneId || tab.state.focusedCenterPaneId || undefined;
    const paneKey = resolvePaneId(tab.state.centerPaneLayout, wantedPaneId);
    if (wantedPaneId && paneKey !== wantedPaneId) {
      set((state) => ({
        activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, focusedCenterPaneId: paneKey } } : t),
      }));
    }

    const matches = tab.state.resourceListTabs.filter((rt) => {
      const samePane = (rt.paneId || 'root') === paneKey;
      const sameCluster = rt.cluster === cluster;
      const sameGV = (rt.resource.group || '') === resourceGroup && (rt.resource.version || '') === resourceVersion;
      const sameName = (rt.resource.name || '').toLowerCase() === (resource.name || '').toLowerCase();
      const sameKind = (rt.resource.kind || '').toLowerCase() === (resource.kind || '').toLowerCase();
      return samePane && sameCluster && sameGV && (sameName || sameKind);
    });

    const preferredId = (tab.state.activeResourceListTabByPane || {})[paneKey];
    const existingTab = matches.find((rt) => rt.id === preferredId) || matches[0];

    if (existingTab) {
      const ns = existingTab.selectedNamespaces;
      const current = withLiveItems(existingTab);
      const live = current !== existingTab;
      set((state) => ({
        activeTabs: state.activeTabs.map((t) =>
          t.id === cluster ? {
            ...t, state: { ...t.state, activeResourceListTab: existingTab.id, listItems: current.items, selectedItem: existingTab.selectedItem,
              ...(live ? { isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined } : {}),
              resourceListTabs: t.state.resourceListTabs.map((rt) => rt.id === existingTab.id ? { ...rt, items: current.items, isPinned: isPinned || rt.isPinned } : rt),
              activeResourceListTabByPane: { ...(t.state.activeResourceListTabByPane || {}), [paneKey]: existingTab.id },
              selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all',
            },
          } : t
        ),
      }));
      let node;
      if (existingTab.resource.kind === 'ClusterDashboard') {
        node = { id: 'cluster-overview', label: 'Overview', type: 'overview' as const, data: { cluster } };
      } else {
        const category = getResourceCategory(existingTab.resource.group, existingTab.resource.name);
        const nodeId = `${category}-${existingTab.resource.group || 'core'}-${existingTab.resource.version}-${existingTab.resource.name}`;
        node = { id: nodeId, label: existingTab.resource.name, type: 'resource' as const, data: existingTab.resource };
      }
      get().selectNode(node);
      // Pinning an open preview tab moves it among the pinned tabs, as a newly
      // opened pinned tab would be placed.
      if (isPinned && !existingTab.isPinned) get().pinResourceListTab(existingTab.id);
      return;
    }

    const topic = itemsTopic(cluster, resource);
    const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
    const topicCache = cacheMap.get(topic);
    const items = liveItemsFor(topic) ?? (topicCache ? Array.from(topicCache.values()) : []);

    let newResourceListTabs = tab.state.resourceListTabs;
    if (!isPinned) {
      const existingPreviewIndex = tab.state.resourceListTabs.findIndex((rt) => !rt.isPinned && (rt.paneId || 'root') === paneKey);
      if (existingPreviewIndex >= 0) { newResourceListTabs = [...tab.state.resourceListTabs]; newResourceListTabs.splice(existingPreviewIndex, 1); }
    }

    const newTab: ResourceListTab = {
      id: `list-${resourceKind}-${resourceGroup}-${resourceVersion}-${Date.now()}`, title, resource, items, selectedItem: null, cluster,
      selectedNamespaces: [], sortBy: 'age', sortOrder: 'desc', isPinned, paneId: paneKey,
    };
    const updatedTabs = isPinned
      ? [...newResourceListTabs.filter((t) => !t.isPinned), newTab, ...newResourceListTabs.filter((t) => t.isPinned)]
      : [newTab, ...newResourceListTabs];

    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === cluster ? { ...t, state: { ...t.state, resourceListTabs: updatedTabs, activeResourceListTab: newTab.id, listItems: items, activeResourceListTabByPane: { ...(t.state.activeResourceListTabByPane || {}), [paneKey]: newTab.id } } } : t
      ),
    }));
  },

  closeResourceListTab: (tabId: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const closingActiveTab = tab.state.activeResourceListTab === tabId;
    const newTabs = tab.state.resourceListTabs.filter((rt) => rt.id !== tabId);
    const closing = tab.state.resourceListTabs.find((rt) => rt.id === tabId);
    if (closing) {
      const topic = itemsTopic(closing.cluster, closing.resource);
      if (!newTabs.some((rt) => itemsTopic(rt.cluster, rt.resource) === topic)) {
        get().releaseRealtimeTopics((t) => t === topic);
      }
    }
    let newActiveTab = tab.state.activeResourceListTab;
    const perPaneMap = { ...(tab.state.activeResourceListTabByPane || {}) };
    Object.keys(perPaneMap).forEach((paneId) => {
      if (perPaneMap[paneId] === tabId) {
        const replacementTab = newTabs.find((rt) => (rt.paneId || 'root') === paneId);
        perPaneMap[paneId] = replacementTab ? replacementTab.id : null;
      }
    });
    let selectedNode = tab.state.selectedNode;
    if (closingActiveTab) {
      const currentIndex = tab.state.resourceListTabs.findIndex((rt) => rt.id === tabId);
      if (newTabs.length > 0) {
        const newIndex = Math.min(currentIndex, newTabs.length - 1);
        newActiveTab = newTabs[newIndex].id;
        const newTab = newTabs[newIndex];
        if (newTab.resource.kind === 'ClusterDashboard') {
          selectedNode = { id: 'cluster-overview', label: 'Overview', type: 'overview' as const, data: { cluster: currentTab } };
        } else {
          const category = getResourceCategory(newTab.resource.group || '', newTab.resource.name);
          const nodeId = `${category}-${newTab.resource.group || 'core'}-${newTab.resource.version}-${newTab.resource.name}`;
          selectedNode = { id: nodeId, label: newTab.resource.name, type: 'resource' as const, data: newTab.resource };
        }
      } else {
        newActiveTab = null;
        selectedNode = null;
        Object.keys(perPaneMap).forEach((k) => { perPaneMap[k] = null; });
      }
    }
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? { ...t, state: { ...t.state, resourceListTabs: newTabs, activeResourceListTab: newActiveTab, activeResourceListTabByPane: perPaneMap } } : t
      ),
    }));
    if (closingActiveTab && selectedNode) {
      get().selectNode(selectedNode);
    }
  },

  setActiveResourceListTab: (tabId: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const storedTab = tab.state.resourceListTabs.find((rt) => rt.id === tabId);
    if (!storedTab) return;
    const resourceListTab = withLiveItems(storedTab);
    const live = resourceListTab !== storedTab;

    let selectedNode;
    const resource = resourceListTab.resource;
    if (resource.kind === 'ClusterDashboard') {
      selectedNode = { id: 'cluster-overview', label: 'Overview', type: 'overview' as const, data: { cluster: currentTab } };
    } else {
      const category = getResourceCategory(resource.group || '', resource.name);
      const nodeId = `${category}-${resource.group || 'core'}-${resource.version}-${resource.name}`;
      selectedNode = { id: nodeId, label: resource.name, type: 'resource' as const, data: resource };
    }

    const ns = resourceListTab.selectedNamespaces;
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: {
            ...t.state, activeResourceListTab: tabId, listItems: resourceListTab.items, selectedItem: resourceListTab.selectedItem, selectedNode, selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all',
            ...(live ? {
              resourceListTabs: t.state.resourceListTabs.map((rt) => rt.id === tabId ? { ...rt, items: resourceListTab.items } : rt),
              isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined,
            } : {}),
          },
        } : t
      ),
    }));
    if (resourceListTab.resource.kind !== 'ClusterDashboard') get().startRealtime(true);
  },

  setActiveResourceListTabForPane: (paneId: string, tabId: string | null) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab || !paneId) return;
    const current = tab.state.activeResourceListTabByPane || {};
    const next = { ...current, [paneId]: tabId };
    const storedTab = tab.state.resourceListTabs.find((rt) => rt.id === tabId);
    const resourceListTab = storedTab && withLiveItems(storedTab);

    if (resourceListTab) {
      let selectedNode;
      if (resourceListTab.resource.kind === 'ClusterDashboard') {
        selectedNode = { id: 'cluster-overview', label: 'Overview', type: 'overview' as const, data: { cluster: currentTab } };
      } else {
        const category = getResourceCategory(resourceListTab.resource.group || '', resourceListTab.resource.name);
        const nodeId = `${category}-${resourceListTab.resource.group || 'core'}-${resourceListTab.resource.version}-${resourceListTab.resource.name}`;
        selectedNode = { id: nodeId, label: resourceListTab.resource.name, type: 'resource' as const, data: resourceListTab.resource };
      }
      const ns = resourceListTab.selectedNamespaces;
      const live = resourceListTab !== storedTab;
      get().updateCurrentTabState({
        activeResourceListTabByPane: next, selectedNode, selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all',
        ...(live ? {
          resourceListTabs: tab.state.resourceListTabs.map((rt) => rt.id === tabId ? { ...rt, items: resourceListTab.items } : rt),
          isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined,
        } : {}),
      });
      if (resourceListTab.resource.kind !== 'ClusterDashboard') get().startRealtime(true);
    } else {
      get().updateCurrentTabState({ activeResourceListTabByPane: next });
    }
  },

  updateResourceListTab: (tabId: string, updates: Partial<ResourceListTab>) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const isActiveTab = tab.state.activeResourceListTab === tabId;
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: {
            ...t.state, resourceListTabs: t.state.resourceListTabs.map((rt) => rt.id === tabId ? { ...rt, ...updates } : rt),
            ...(isActiveTab && updates.items ? { listItems: updates.items } : {}),
            ...(isActiveTab && updates.selectedItem !== undefined ? { selectedItem: updates.selectedItem } : {}),
          },
        } : t
      ),
    }));
  },

  pinResourceListTab: (tabId: string) => {
    const { currentTab } = get();
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: {
            ...t.state, resourceListTabs: (() => {
              const tabToPin = t.state.resourceListTabs.find((rt) => rt.id === tabId);
              if (!tabToPin) return t.state.resourceListTabs;
              const pinnedTab = { ...tabToPin, isPinned: true };
              const otherTabs = t.state.resourceListTabs.filter((rt) => rt.id !== tabId);
              const previewTabs = otherTabs.filter((rt) => !rt.isPinned);
              const pinnedTabs = otherTabs.filter((rt) => rt.isPinned);
              return [...previewTabs, pinnedTab, ...pinnedTabs];
            })(),
          },
        } : t
      ),
    }));
  },

  reorderResourceListTabs: (fromIndex: number, toIndex: number) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const newTabs = [...tab.state.resourceListTabs];
    const [removed] = newTabs.splice(fromIndex, 1);
    newTabs.splice(toIndex, 0, removed);
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === currentTab ? { ...t, state: { ...t.state, resourceListTabs: newTabs } } : t),
    }));
  },

  moveResourceListTabToPane: (tabId: string, targetPaneId: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const rlTab = tab.state.resourceListTabs.find((rt) => rt.id === tabId);
    if (!rlTab) return;
    const normalizedTarget = targetPaneId || 'root';
    const dup = tab.state.resourceListTabs.find((rt) =>
      (rt.paneId || 'root') === normalizedTarget && rt.cluster === rlTab.cluster &&
      (rt.resource.group || '') === (rlTab.resource.group || '') && (rt.resource.version || '') === (rlTab.resource.version || '') &&
      ((rt.resource.name || '').toLowerCase() === (rlTab.resource.name || '').toLowerCase() || (rt.resource.kind || '').toLowerCase() === (rlTab.resource.kind || '').toLowerCase()) &&
      rt.id !== rlTab.id
    );
    if (dup) {
      get().closeResourceListTab(tabId);
      const perPane = tab.state.activeResourceListTabByPane || {};
      const next = { ...perPane, [normalizedTarget]: dup.id };
      const ns = dup.selectedNamespaces;
      get().updateCurrentTabState({ activeResourceListTabByPane: next, activeResourceListTab: dup.id, listItems: dup.items, selectedItem: dup.selectedItem, selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all' });
      return;
    }
    get().updateCurrentTabState({
      resourceListTabs: tab.state.resourceListTabs.map((rt) => rt.id === tabId ? { ...rt, paneId: normalizedTarget } : rt),
      activeResourceListTabByPane: { ...(tab.state.activeResourceListTabByPane || {}), [normalizedTarget]: tabId },
    });
  },
});
