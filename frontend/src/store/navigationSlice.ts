import { StateCreator } from 'zustand';
import api from '../services/api';
import { FocusArea } from '../types';
import { getResourceCategory } from '../utils/resourceUtils';
import { findTreeResourceNode } from '../utils/searchResults';
import { findNodeById } from './utils';
import { pageNode, pageResources } from './navigationTargets';
import { NavigationSlice, StoreState, NavigationEntry } from './types';

const HISTORY_ID = 'workspace';

const entryKey = (entry: NavigationEntry) => JSON.stringify([
  entry.clusterId, entry.paneId || 'root',
  (pageResources[entry.type] || entry.resource)?.group || '', (pageResources[entry.type] || entry.resource)?.version, (pageResources[entry.type] || entry.resource)?.name,
  entry.type === 'item' ? 'resource' : entry.type,
  entry.item?.metadata?.namespace || entry.item?.namespace || '',
  entry.item?.metadata?.name || entry.item?.name || '',
]);

export const createNavigationSlice: StateCreator<StoreState, [], [], NavigationSlice> = (_set, get) => {
  // Preserve click order even when HTTP requests finish out of order.
  let pending = Promise.resolve();
  let currentEntry: string | undefined;
  let reveal = 0;
  let revision = 0;
  const enqueue = (action: () => Promise<void>) => {
    const result = pending.then(action);
    pending = result.catch(() => {});
    return result;
  };
  const navigate = (direction: 'back' | 'forward') => {
    return enqueue(async () => {
      const cluster = get().currentTab;
      if (!cluster) return;
      const startedAt = revision;
      try {
        const entry = await (direction === 'back' ? api.navigateBack(HISTORY_ID) : api.navigateForward(HISTORY_ID));
        if (entry) {
          currentEntry = entryKey(entry);
          if (get().currentTab === cluster && revision === startedAt) await get().restoreNavigationState(entry);
        }
      } catch (error) { console.error(`Failed to navigate ${direction}:`, error); }
    });
  };

  return {
    recordCurrentNavigation: () => {
      const state = get().getCurrentTabState();
      if (!state) return Promise.resolve();
      const tabId = state.activeResourceListTabByPane?.[state.focusedCenterPaneId || 'root'] || state.activeResourceListTab;
      const tab = state.resourceListTabs.find((t) => t.id === tabId);
      const node = tab ? pageNode(tab.resource.kind, get().currentTab) || { type: 'resource', id: state.selectedNode?.id || '', data: tab.resource } : null;
      const selected = node || state.selectedNode;
      if (!selected) return Promise.resolve();
      const resource = tab?.resource || selected.data;
      return get().recordNavigation(selected.type, selected.id, resource, tab?.selectedItem || null);
    },
    recordNavigation: (type, path, resource = null, item = null) => {
      const cluster = get().currentTab;
      if (!cluster) return Promise.resolve();
      revision++;
      const entry = { type, path, resource, item, clusterId: cluster, paneId: get().getCurrentTabState()?.focusedCenterPaneId || 'root' };
      return enqueue(async () => {
        const key = entryKey(entry);
        if (currentEntry === key) return;
        await api.addNavigationEntry(HISTORY_ID, cluster, entry);
        currentEntry = key;
      });
    },
    navigateBack: () => navigate('back'),
    navigateForward: () => navigate('forward'),

    restoreNavigationState: async (entry) => {
      const cluster = entry.clusterId || get().currentTab;
      if (!cluster) return;
      const startedAt = ++revision;
      if (!get().activeTabs.some((tab) => tab.id === cluster)) {
        await get().openTab(cluster, false);
        if (revision !== startedAt) return;
      } else if (get().currentTab !== cluster) {
        get().setCurrentTab(cluster, false);
      }
      const isCurrent = () => get().currentTab === cluster && revision === startedAt;
      const page = pageResources[entry.type] || Object.values(pageResources).find((r) => r.kind === entry.resource?.kind);
      const resource = page || entry.resource;
      if (!resource) return;
      if (!page && entry.type !== 'resource' && entry.type !== 'item') return;

      if (entry.paneId) get().updateCurrentTabState({ focusedCenterPaneId: entry.paneId });
      await get().openResourceListTab(resource, cluster, false, entry.paneId);
      if (!isCurrent()) return;
      const tabId = get().getCurrentTabState()?.activeResourceListTab;
      if (!tabId) return;
      if (page) {
        get().selectNode(pageNode(page.kind, cluster)!);
        get().updateCurrentTabState({ selectedItem: null, detailData: null, activeDetailTab: null, isDetailsPanelCollapsed: true, focusArea: 'list' });
        get().updateResourceListTab(tabId, { selectedItem: entry.item || null, navigationReveal: ++reveal });
        if (page.kind === 'ArgoApplicationsOverview') {
          const category = findNodeById(get().getCurrentTabState()?.treeData || [], 'argocd');
          if (category && !category.expanded) {
            if (category.children) get().toggleNodeExpansion('argocd');
            else await get().expandNode(cluster, 'argocd', 'category', { categoryId: 'argocd' });
          }
        }
        if (page.kind === 'HelmReleases' && entry.item) {
          get().openDetailTab({ kind: 'HelmRelease' }, entry.item, cluster);
        }
        return;
      }

      const tree = get().getCurrentTabState()?.treeData || [];
      const discoveredNode = findTreeResourceNode(tree, resource.group, resource.version, resource.name);
      const category = tree.find((root) => root.type === 'category' && (
        (discoveredNode && findNodeById([root], discoveredNode.id)) || entry.path.startsWith(`${root.id}-`)
      ))?.id || getResourceCategory(resource.group || '', resource.name);
      const node = discoveredNode
        || { id: resource.name === 'events' && !resource.group ? 'cluster-events' : `${category}-${resource.group || 'core'}-${resource.version}-${resource.name}`, label: resource.name, type: 'resource', data: resource };
      get().selectNode(node);
      await get().loadListItems(cluster, resource);
      if (!isCurrent()) return;
      const state = get().getCurrentTabState()!;
      const item = entry.item ? { ...entry.item, name: entry.item.metadata?.name || entry.item.name, namespace: entry.item.metadata?.namespace || entry.item.namespace } : null;
      const list = state.resourceListTabs.find((tab) => tab.id === tabId)!;
      const selectedNamespaces = item?.namespace && list.selectedNamespaces.length && !list.selectedNamespaces.includes(item.namespace) ? [] : list.selectedNamespaces;
      get().updateResourceListTab(tabId, { items: state.listItems, selectedItem: item, selectedNamespaces, navigationReveal: ++reveal });
      get().updateCurrentTabState({ selectedItem: item, selectedNamespaces, selectedNamespace: selectedNamespaces[0] || 'all', focusArea: 'list', detailData: null, activeDetailTab: null, isDetailsPanelCollapsed: !item });
      if (item) {
        get().openDetailTab(resource, {
          ...item, kind: resource.kind, apiVersion: resource.group ? `${resource.group}/${resource.version}` : resource.version,
          metadata: { ...item.metadata, name: item.name, namespace: item.namespace },
        }, cluster);
        // The detail loader checks identity before applying its response.
        void get().loadDetails(cluster, resource, item).catch((error) => console.error('Failed to restore details:', error));
      }
      get().startRealtime(true);

      // Reveal both the resource category and nested API-version groups.
      const categoryNode = findNodeById(get().getCurrentTabState()?.treeData || [], category);
      if (categoryNode && !categoryNode.expanded) {
        if (categoryNode.children) get().toggleNodeExpansion(category);
        else await get().expandNode(cluster, category, 'category', { categoryId: category });
      }
      if (!isCurrent()) return;
      if (category === 'crossplane' || category === 'custom') {
        const groupId = `${category}-${resource.group}-${resource.version}`;
        if (!get().getCurrentTabState()?.expandedNodes.has(groupId)) get().toggleNodeExpansion(groupId);
      }
    },

    setFocusArea: (area: FocusArea) => { get().updateCurrentTabState({ focusArea: area }); },
    toggleDetailsPanel: () => {
      const tabState = get().getCurrentTabState();
      if (tabState) get().updateCurrentTabState({ isDetailsPanelCollapsed: !tabState.isDetailsPanelCollapsed });
    },
    setDetailsPanelCollapsed: (collapsed) => { get().updateCurrentTabState({ isDetailsPanelCollapsed: collapsed }); },
  };
};
