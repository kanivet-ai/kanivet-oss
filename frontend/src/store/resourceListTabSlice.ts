import { StateCreator } from 'zustand';
import { getResourceCategory } from '../utils/resourceUtils';
import { ResourceListTabSlice, StoreState, ResourceListTab } from './types';

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

    let targetPaneId = paneId || tab.state.focusedCenterPaneId || undefined;
    if (targetPaneId && targetPaneId !== 'root' && tab.state.centerPaneLayout) {
      const findNode = (node: any, nodeId: string): any => {
        if (node.id === nodeId) return node;
        if (node.children) { for (const child of node.children) { const found = findNode(child, nodeId); if (found) return found; } }
        return null;
      };
      const foundNode = findNode(tab.state.centerPaneLayout, targetPaneId);
      if (!foundNode) {
        targetPaneId = 'root';
        set((state) => ({
          activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, focusedCenterPaneId: 'root' } } : t),
        }));
      }
    }
    const paneKey = targetPaneId || 'root';

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
      set((state) => ({
        activeTabs: state.activeTabs.map((t) =>
          t.id === cluster ? {
            ...t, state: { ...t.state, activeResourceListTab: existingTab.id, listItems: existingTab.items, selectedItem: existingTab.selectedItem,
              resourceListTabs: t.state.resourceListTabs.map((rt) => rt.id === existingTab.id ? { ...rt, isPinned: isPinned || rt.isPinned } : rt),
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
      return;
    }

    const topic = `items:${cluster}:${resource.group || ''}:${resource.version}:${resource.name}:`;
    const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
    const topicCache = cacheMap.get(topic);
    const items = topicCache ? Array.from(topicCache.values()) : [];

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
    if (closingActiveTab) get().stopRealtime();
    const newTabs = tab.state.resourceListTabs.filter((rt) => rt.id !== tabId);
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
    const resourceListTab = tab.state.resourceListTabs.find((rt) => rt.id === tabId);
    if (!resourceListTab) return;

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
        t.id === currentTab ? { ...t, state: { ...t.state, activeResourceListTab: tabId, listItems: resourceListTab.items, selectedItem: resourceListTab.selectedItem, selectedNode, selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all' } } : t
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
    const resourceListTab = tab.state.resourceListTabs.find((rt) => rt.id === tabId);

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
      get().updateCurrentTabState({ activeResourceListTabByPane: next, selectedNode, selectedNamespaces: ns, selectedNamespace: ns.length > 0 ? ns[0] : 'all' });
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
