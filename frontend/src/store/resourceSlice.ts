import { StateCreator } from 'zustand';
import api from '../services/api';
import { notifyDrainComplete, notifyRolloutComplete } from '../services/islandNotifications';
import { ResourceSlice, StoreState, TreeNode, PinnedDetail, RolloutStatusData } from './types';
import { rebuildTabIndex, updateTreeNode, findNodeById, predefinedCategories } from './utils';

const makeArgoOverviewNode = (cluster: string): TreeNode => ({ id: 'argo-overview', label: 'Apps Overview', type: 'argo-overview', data: { cluster } });

export const createResourceSlice: StateCreator<StoreState, [], [], ResourceSlice> = (set, get) => ({
  rolloutPollingInterval: null,

  loadTreeData: async (cluster: string, data?: TreeNode[]) => {
    if (data) {
      set((state) => {
        const tabIndex = state.tabIndexMap.get(cluster) ?? -1;
        if (tabIndex === -1) return state;
        const updatedTabs = [...state.activeTabs];
        updatedTabs[tabIndex] = { ...updatedTabs[tabIndex], state: { ...updatedTabs[tabIndex].state, treeData: data } };
        return { activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) };
      });
    } else {
      const initialState = get();
      const initialTabIndex = initialState.tabIndexMap.get(cluster) ?? -1;
      if (initialTabIndex === -1) return;
      const initialExpandedNodes = initialState.activeTabs[initialTabIndex]?.state.expandedNodes || new Set<string>();
      const isVCluster = cluster.startsWith('vcluster:');
      const rawCategories = await api.getCategories(cluster);
      const hasCount = (resources: any[]) => resources.some((res: any) => (res.count ?? 0) > 0);
      const disabledActionables: Record<string, boolean> = { crossplane: false, argocd: false, vclusters: isVCluster };
      const setNodeDisabled = (nodeId: string, disabled: boolean) => {
        set((state) => {
          const tabIndex = state.tabIndexMap.get(cluster) ?? -1;
          if (tabIndex === -1) return state;
          const currentState = state.activeTabs[tabIndex].state;
          let changed = false;
          const treeData = currentState.treeData.map((node) => {
            if (node.id !== nodeId || !!node.disabled === disabled) return node;
            changed = true;
            return { ...node, disabled };
          });
          if (!changed) return state;
          const updatedTabs = [...state.activeTabs];
          updatedTabs[tabIndex] = { ...updatedTabs[tabIndex], state: { ...currentState, treeData } };
          return { activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) };
        });
      };
      api.getResources(cluster, 'crossplane', false).catch(() => [])
        .then((resources) => setNodeDisabled('crossplane', !hasCount(resources)));
      api.getResources(cluster, 'argocd', false).catch(() => [])
        .then((resources) => setNodeDisabled('argocd', !hasCount(resources)));
      if (!isVCluster) {
        api.listVClusters(cluster).catch(() => [])
          .then((vcs) => setNodeDisabled('virtual-clusters', vcs.length === 0));
      }
      const categoryMap = new Map<string, any>(rawCategories.map((cat: any) => [cat.id, cat]));
      [{ id: 'crossplane', name: 'Crossplane' }, { id: 'argocd', name: 'Argo CD' }].forEach((cat) => {
        if (!categoryMap.has(cat.id)) categoryMap.set(cat.id, cat);
      });
      const categoryOrder = ['workloads', 'networking', 'config', 'storage', 'rbac', 'cluster', 'crossplane', 'argocd', 'custom'];
      const categories: any[] = [
        ...categoryOrder.filter((id) => categoryMap.has(id)).map((id) => categoryMap.get(id)),
        ...rawCategories.filter((cat: any) => !categoryOrder.includes(cat.id)),
      ];
      const overviewNode: TreeNode = { id: 'cluster-overview', label: 'Overview', type: 'overview', data: { cluster } };
      const eventsNode: TreeNode = { id: 'cluster-events', label: 'Events', type: 'resource', hideCount: true, data: { name: 'events', group: '', version: 'v1', kind: 'Event', namespaced: true, cluster } };
      const helmNode: TreeNode = { id: 'helm-releases', label: 'Helm Releases', type: 'helm', data: { cluster } };
      const incidentsNode: TreeNode = { id: 'incident-timeline', label: 'Incident Timeline', type: 'incident-timeline', data: { cluster } };
      const finopsNode: TreeNode = { id: 'finops-dashboard', label: 'FinOps', type: 'finops', data: { cluster } };
      let vclustersNode: TreeNode = { id: 'virtual-clusters', label: 'Virtual Clusters', type: 'vclusters', data: { cluster }, disabled: disabledActionables.vclusters };
      if (initialExpandedNodes.has(vclustersNode.id)) {
        const existing = findNodeById(initialState.activeTabs[initialTabIndex]?.state.treeData || [], vclustersNode.id);
        vclustersNode = { ...vclustersNode, expanded: true, children: existing?.children };
      }

      set((state) => {
        const tabIndex = state.tabIndexMap.get(cluster) ?? -1;
        if (tabIndex === -1) return state;
        const currentState = state.activeTabs[tabIndex].state;
        const expandedNodes = currentState.expandedNodes;
        const formattedCategories = categories.map((cat: any) => {
          const disabled = !!disabledActionables[cat.id];
          const node: TreeNode = { id: cat.id, label: cat.name, type: 'category', data: { categoryId: cat.id }, disabled };
          if (!disabled && expandedNodes.has(cat.id)) {
            node.expanded = true;
            const existingNode = findNodeById(currentState.treeData, cat.id);
            if (existingNode?.children) node.children = existingNode.children;
          }
          return node;
        });
        const clusterIdx = formattedCategories.findIndex(cat => cat.id === 'cluster');
        const crossplaneIdx = formattedCategories.findIndex(cat => cat.id === 'crossplane');
        const treeDataWithOverview: TreeNode[] = [
          overviewNode, finopsNode, ...formattedCategories.slice(0, clusterIdx + 1), eventsNode, incidentsNode, helmNode, vclustersNode, ...formattedCategories.slice(crossplaneIdx),
        ];
        const updatedTabs = [...state.activeTabs];
        updatedTabs[tabIndex] = { ...updatedTabs[tabIndex], state: { ...updatedTabs[tabIndex].state, treeData: treeDataWithOverview } };
        return { activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) };
      });

      const nodesToExpand = Array.from(initialExpandedNodes);
      for (const nodeId of nodesToExpand) {
        const freshState = get();
        const freshTabIndex = freshState.tabIndexMap.get(cluster) ?? -1;
        if (freshTabIndex === -1) return;
        const treeData = freshState.activeTabs[freshTabIndex].state.treeData;
        const node = findNodeById(treeData, nodeId);
        if (!node || node.disabled) continue;
        if (!node.children) {
          await get().expandNode(cluster, nodeId, node.type, node.data);
        } else if (node.type === 'category') {
          // Re-fetch counts in background for already-expanded categories
          get().expandNode(cluster, nodeId, node.type, node.data).catch(() => {});
        }
      }
    }
  },

  expandNode: async (cluster: string, nodeId: string, nodeType: string, metadata: any) => {
    if (nodeType === 'vclusters') {
      let vcs: Awaited<ReturnType<typeof api.listVClusters>> = [];
      try {
        vcs = await api.listVClusters(cluster);
      } catch (err: any) {
        const message = err?.response?.data?.error || err?.message || 'Failed to list virtual clusters';
        window.dispatchEvent(new CustomEvent('toast:error', { detail: { message } }));
      }
      const children = vcs.map((vc) => ({
        id: `vcluster-${vc.namespace}-${vc.name}`,
        label: vc.name,
        type: 'vcluster' as const,
        data: { host: cluster, namespace: vc.namespace, name: vc.name, ready: vc.ready, phase: vc.phase },
      }));
      const { activeTabs, tabIndexMap } = get();
      const tabIndex = tabIndexMap.get(cluster) ?? -1;
      if (tabIndex === -1) return;
      const updatedTabs = [...activeTabs];
      const updatedExpandedNodes = new Set(updatedTabs[tabIndex].state.expandedNodes);
      updatedExpandedNodes.add(nodeId);
      updatedTabs[tabIndex] = {
        ...updatedTabs[tabIndex],
        state: { ...updatedTabs[tabIndex].state, treeData: updateTreeNode(updatedTabs[tabIndex].state.treeData, nodeId, children, updatedExpandedNodes), expandedNodes: updatedExpandedNodes },
      };
      set({ activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) });
      return;
    }
    if (nodeType === 'category') {
      const predefinedResources = predefinedCategories[metadata.categoryId as keyof typeof predefinedCategories];
      if (predefinedResources) {
        const formattedResources = predefinedResources.map((res: any) => ({
          id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
          label: res.name, type: 'resource', data: res, count: undefined,
        }));
        const { activeTabs, tabIndexMap } = get();
        const tabIndex = tabIndexMap.get(cluster) ?? -1;
        if (tabIndex !== -1) {
          const updatedTabs = [...activeTabs];
          const updatedExpandedNodes = new Set(updatedTabs[tabIndex].state.expandedNodes);
          updatedExpandedNodes.add(nodeId);
          updatedTabs[tabIndex] = {
            ...updatedTabs[tabIndex],
            state: { ...updatedTabs[tabIndex].state, treeData: updateTreeNode(updatedTabs[tabIndex].state.treeData, nodeId, formattedResources, updatedExpandedNodes), expandedNodes: updatedExpandedNodes },
          };
          set({ activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) });
        }
        api.getResources(cluster, metadata.categoryId, false)
          .then((resourcesWithCounts) => {
            const backendResourceMap = new Map(resourcesWithCounts.map((res: any) => [res.name, res]));
            const mergedResources = [...predefinedResources];
            resourcesWithCounts.forEach((backendRes: any) => {
              const exists = predefinedResources.some((predefined: any) => predefined.name === backendRes.name);
              if (!exists) mergedResources.push(backendRes);
            });
            const formattedResourcesWithCounts = mergedResources.map((res: any) => {
              const backendData = backendResourceMap.get(res.name);
              return {
                id: `${metadata.categoryId}-${(backendData as any)?.group || res.group || 'core'}-${(backendData as any)?.version || res.version}-${res.name}`,
                label: res.name, type: 'resource', data: backendData || res, count: (backendData as any)?.count ?? undefined,
              };
            });
            const { activeTabs, tabIndexMap } = get();
            const tabIdx = tabIndexMap.get(cluster) ?? -1;
            if (tabIdx !== -1) {
              const updatedTabs2 = [...activeTabs];
              updatedTabs2[tabIdx] = {
                ...updatedTabs2[tabIdx],
                state: { ...updatedTabs2[tabIdx].state, treeData: updateTreeNode(updatedTabs2[tabIdx].state.treeData, nodeId, formattedResourcesWithCounts, updatedTabs2[tabIdx].state.expandedNodes) },
              };
              set({ activeTabs: updatedTabs2, tabIndexMap: rebuildTabIndex(updatedTabs2) });
            }
          }).catch(() => {});
        return;
      }

      const resources = await api.getResources(cluster, metadata.categoryId, true);
      let formattedResources: TreeNode[];
      if (metadata.categoryId === 'argocd') {
        formattedResources = [makeArgoOverviewNode(cluster), ...resources.map((res: any) => ({
          id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
          label: res.kind || res.name, type: 'resource', data: res, count: undefined,
        }))];
      } else if (metadata.categoryId === 'crossplane' || metadata.categoryId === 'custom') {
        const groupedResources = new Map<string, any[]>();
        resources.forEach((res: any) => {
          const apiVersion = `${res.group}/${res.version}`;
          if (!groupedResources.has(apiVersion)) groupedResources.set(apiVersion, []);
          groupedResources.get(apiVersion)!.push(res);
        });
        const { activeTabs: currentActiveTabs } = get();
        const currentTabData = currentActiveTabs.find((t) => t.id === cluster);
        const currentExpandedNodes = currentTabData?.state.expandedNodes || new Set();
        formattedResources = Array.from(groupedResources.entries()).map(([apiVersion, versionResources]) => {
          const apiVersionNodeId = `${metadata.categoryId}-${apiVersion.replace('/', '-')}`;
          return {
            id: apiVersionNodeId, label: apiVersion, type: 'apiVersion', data: { apiVersion, categoryId: metadata.categoryId },
            count: undefined, expanded: currentExpandedNodes.has(apiVersionNodeId),
            children: versionResources.map((res: any) => ({
              id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
              label: res.kind || res.name, type: 'resource', data: res, count: undefined,
            })),
          };
        });
      } else {
        formattedResources = resources.map((res: any) => ({
          id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
          label: res.name, type: 'resource', data: res, count: undefined,
        }));
      }

      const { activeTabs, tabIndexMap } = get();
      const tabIndex = tabIndexMap.get(cluster) ?? -1;
      if (tabIndex === -1) return;
      const updatedTabs = [...activeTabs];
      const updatedExpandedNodes = new Set(updatedTabs[tabIndex].state.expandedNodes);
      updatedExpandedNodes.add(nodeId);
      updatedTabs[tabIndex] = {
        ...updatedTabs[tabIndex],
        state: { ...updatedTabs[tabIndex].state, treeData: updateTreeNode(updatedTabs[tabIndex].state.treeData, nodeId, formattedResources, updatedExpandedNodes), expandedNodes: updatedExpandedNodes },
      };
      set({ activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) });

      if (metadata.categoryId !== 'custom') {
        api.getResources(cluster, metadata.categoryId, false)
          .then((resourcesWithCounts) => {
            const filteredResourcesWithCounts = resourcesWithCounts.filter((res: any) => res.name !== 'replicasets');
            let formattedResourcesWithCounts: TreeNode[];
            if (metadata.categoryId === 'argocd') {
              formattedResourcesWithCounts = [makeArgoOverviewNode(cluster), ...filteredResourcesWithCounts.map((res: any) => ({
                id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
                label: res.kind || res.name, type: 'resource', data: res, count: res.count,
              }))];
            } else if (metadata.categoryId === 'crossplane') {
              const groupedResources = new Map<string, any[]>();
              filteredResourcesWithCounts.forEach((res: any) => {
                const apiVersion = `${res.group}/${res.version}`;
                if (!groupedResources.has(apiVersion)) groupedResources.set(apiVersion, []);
                groupedResources.get(apiVersion)!.push(res);
              });
              const { activeTabs: currentTabs } = get();
              const currentTab = currentTabs.find((t) => t.id === cluster);
              const currentExpandedNodes = currentTab?.state.expandedNodes || new Set();
              formattedResourcesWithCounts = Array.from(groupedResources.entries()).map(([apiVersion, versionResources]) => {
                const allKnown = versionResources.every((res: any) => typeof res.count === 'number');
              const totalCount = allKnown ? versionResources.reduce((sum: number, res: any) => sum + res.count, 0) : undefined;
                const apiVersionNodeId = `${metadata.categoryId}-${apiVersion.replace('/', '-')}`;
                return {
                  id: apiVersionNodeId, label: apiVersion, type: 'apiVersion', data: { apiVersion, categoryId: metadata.categoryId },
                  count: totalCount, expanded: currentExpandedNodes.has(apiVersionNodeId),
                  children: versionResources.map((res: any) => ({
                    id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
                    label: res.kind || res.name, type: 'resource', data: res, count: res.count,
                  })),
                };
              });
            } else {
              formattedResourcesWithCounts = filteredResourcesWithCounts.map((res: any) => ({
                id: `${metadata.categoryId}-${res.group || 'core'}-${res.version}-${res.name}`,
                label: res.name, type: 'resource', data: res, count: res.count,
              }));
            }
            const { activeTabs, tabIndexMap } = get();
            const tabIdx = tabIndexMap.get(cluster) ?? -1;
            if (tabIdx === -1) return;
            const updatedTabs2 = [...activeTabs];
            updatedTabs2[tabIdx] = {
              ...updatedTabs2[tabIdx],
              state: { ...updatedTabs2[tabIdx].state, treeData: updateTreeNode(updatedTabs2[tabIdx].state.treeData, nodeId, formattedResourcesWithCounts, updatedTabs2[tabIdx].state.expandedNodes) },
            };
            set({ activeTabs: updatedTabs2, tabIndexMap: rebuildTabIndex(updatedTabs2) });
          }).catch(() => {});
      }
    }
  },

  selectNode: (node: TreeNode) => {
    get().updateCurrentTabState({ selectedNode: node });
    const { currentTab } = get();
    try {
      if (currentTab && node?.type === 'resource' && node.data) {
        localStorage.setItem(`kanivet.lastResource.${currentTab}`, JSON.stringify(node.data));
      }
    } catch {}
  },

  loadListItems: async (cluster: string, resource: any) => {
    const topic = `items:${cluster}:${resource.group || ''}:${resource.version}:${resource.name}:`;
    const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
    (window as any).__kanivetItemsCache = cacheMap;
    const topicCache = cacheMap.get(topic);
    const cachedItems = topicCache ? Array.from(topicCache.values()) : [];
    const nsPatch = resource.namespaced ? {} : { namespaces: [] };
    if (cachedItems.length > 0) {
      get().updateCurrentTabState({ listItems: cachedItems, hasReceivedInitialListData: false, ...nsPatch });
    } else {
      get().updateCurrentTabState({ listItems: [], selectedItem: null, hasReceivedInitialListData: false, ...nsPatch });
    }
    if (resource.namespaced) {
      api.getNamespaces(cluster).then((namespaces) => {
        if (get().currentTab === cluster) get().updateCurrentTabState({ namespaces });
      }).catch(() => {});
    }
    return true;
  },

  reloadListItems: async () => {
    const { currentTab, activeTabs } = get();
    const tabState = get().getCurrentTabState();
    if (!currentTab || !tabState?.selectedNode || tabState.selectedNode.type !== 'resource') return;
    const resource = tabState.selectedNode.data;
    const topic = `items:${currentTab}:${resource.group || ''}:${resource.version}:${resource.name}:`;
    const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
    if (cacheMap.has(topic)) cacheMap.delete(topic);
    get().updateCurrentTabState({ loadError: undefined, isLoadingListItems: true, hasReceivedInitialListData: false, listItems: [], selectedItem: null });
    await get().loadListItems(currentTab, resource);
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (tab && tab.state.activeResourceListTab) {
      const activeListTab = tab.state.resourceListTabs.find((rt) => rt.id === tab.state.activeResourceListTab);
      if (activeListTab) get().updateResourceListTab(tab.state.activeResourceListTab, { items: tabState.listItems });
    }
  },

  selectItem: (item: any) => {
    get().updateCurrentTabState({ selectedItem: item });
    const { currentTab } = get();
    try {
      if (currentTab && item) {
        localStorage.setItem(`kanivet.lastItem.${currentTab}`, JSON.stringify({ name: item.name, namespace: item.namespace || '' }));
      }
    } catch {}
  },

  loadDetails: async (cluster: string, resource: any, item: any, signal?: AbortSignal) => {
    const isHelmRelease = item?.kind === 'HelmRelease' || resource?.kind === 'HelmRelease';
    if (isHelmRelease) {
      const tabState = get().getCurrentTabState();
      if (tabState?.activeDetailTab) {
        set((state) => ({
          activeTabs: state.activeTabs.map((t) =>
            t.id === cluster ? { ...t, state: { ...t.state, detailData: item, isDetailsPanelCollapsed: false } } : t
          ),
        }));
      } else {
        get().updateCurrentTabState({ detailData: item, isDetailsPanelCollapsed: false });
      }
      return item;
    }
    let details;
    try {
      details = await api.getResourceDetails(cluster, resource.group, resource.version, resource.kind, item.namespace || '', item.name, signal);
    } catch (error: any) {
      if (error?.response?.status === 404) return;
      throw error;
    }
    if (signal?.aborted) return;
    const detailsName = details?.metadata?.name || details?.name;
    const detailsNs = details?.metadata?.namespace || details?.namespace || '';
    const tabState = get().getCurrentTabState();
    if (tabState?.activeDetailTab) {
      const activeDt = tabState.detailTabs.find((dt) => dt.id === tabState.activeDetailTab);
      const dtName = activeDt?.item?.metadata?.name || activeDt?.item?.name;
      const dtNs = activeDt?.item?.metadata?.namespace || activeDt?.item?.namespace || '';
      if (detailsName && dtName && (dtName !== detailsName || dtNs !== detailsNs)) return details;
      set((state) => ({
        activeTabs: state.activeTabs.map((t) =>
          t.id === cluster ? {
            ...t, state: {
              ...t.state, detailData: details, isDetailsPanelCollapsed: false,
              detailTabs: t.state.detailTabs.map((dt) => dt.id === tabState.activeDetailTab ? { ...dt, item: details } : dt),
            },
          } : t
        ),
      }));
    } else {
      get().updateCurrentTabState({ detailData: details, isDetailsPanelCollapsed: false });
    }
    get().loadResourceEvents(cluster, resource, details, signal);
    return details;
  },

  loadResourceEvents: async (cluster: string, resource: any, details: any, signal?: AbortSignal) => {
    const name = details?.metadata?.name || details?.name;
    const namespace = details?.metadata?.namespace || details?.namespace || '';
    if (!name) return;
    let events: any[] = [];
    try {
      events = await api.getResourceEvents(cluster, resource.group, resource.version, resource.kind, namespace, name, signal);
    } catch {
      return;
    }
    if (signal?.aborted || !events.length) return;
    const matches = (item: any) =>
      item && (item.metadata?.name || item.name) === name &&
      (item.metadata?.namespace || item.namespace || '') === namespace;
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => {
        if (t.id !== cluster) return t;
        const dd = t.state.detailData;
        return {
          ...t, state: {
            ...t.state,
            detailData: matches(dd) ? { ...dd, events } : dd,
            detailTabs: t.state.detailTabs.map((dt) => matches(dt.item) ? { ...dt, item: { ...dt.item, events } } : dt),
          },
        };
      }),
    }));
  },

  updateDetailData: (data: any) => { get().updateCurrentTabState({ detailData: data }); },
  setSearchQuery: (query: string) => { get().updateCurrentTabState({ searchQuery: query }); },
  performSearch: (query: string) => { get().updateCurrentTabState({ searchQuery: query }); },

  toggleNodeExpansion: (nodeId: string) => {
    const { activeTabs, currentTab, tabIndexMap } = get();
    const tabIndex = tabIndexMap.get(currentTab || '') ?? -1;
    if (tabIndex === -1) return;
    const currentState = activeTabs[tabIndex].state;
    const expandedNodes = new Set(currentState.expandedNodes);
    if (expandedNodes.has(nodeId)) expandedNodes.delete(nodeId);
    else expandedNodes.add(nodeId);
    const toggleExpanded = (nodes: TreeNode[]): TreeNode[] => nodes.map((node) => {
      if (node.id === nodeId) return { ...node, expanded: !node.expanded };
      if (node.children) return { ...node, children: toggleExpanded(node.children) };
      return node;
    });
    const updatedTabs = [...activeTabs];
    updatedTabs[tabIndex] = { ...updatedTabs[tabIndex], state: { ...currentState, treeData: toggleExpanded(currentState.treeData), expandedNodes } };
    set({ activeTabs: updatedTabs, tabIndexMap: rebuildTabIndex(updatedTabs) });
    try {
      if (currentTab) {
        const snapshot = updatedTabs.find((t) => t.id === currentTab)?.state;
        if (snapshot) {
          const toSave = {
            activeResourceListTab: snapshot.activeResourceListTab, activeResourceListTabByPane: snapshot.activeResourceListTabByPane,
            resourceListTabs: snapshot.resourceListTabs.map((rt) => ({
              id: rt.id, title: rt.title, resource: rt.resource, cluster: rt.cluster, selectedNamespaces: rt.selectedNamespaces,
              sortBy: rt.sortBy, sortOrder: rt.sortOrder, isPinned: rt.isPinned, paneId: rt.paneId, selectedItem: rt.selectedItem,
              items: [],
            })),
            detailTabs: snapshot.detailTabs.map((dt) => ({
              id: dt.id, title: dt.title, resource: dt.resource, cluster: dt.cluster, isPinned: dt.isPinned, location: dt.location, paneId: dt.paneId,
              item: dt.item ? { name: dt.item.name, namespace: dt.item.namespace, uid: dt.item.uid, kind: dt.item.kind, apiVersion: dt.item.apiVersion } : dt.item,
            })),
            activeDetailTab: snapshot.activeDetailTab, bottomTabs: get().bottomTabs.map((bt) => ({
              id: bt.id, type: bt.type, title: bt.title, customTitle: bt.customTitle, resource: bt.resource,
              cluster: bt.cluster, selectedContainer: bt.selectedContainer, location: bt.location, paneId: bt.paneId,
            })),
            activeBottomTab: get().activeBottomTab, focusedCenterPaneId: snapshot.focusedCenterPaneId, centerPaneLayout: snapshot.centerPaneLayout,
            expandedNodes: Array.from(snapshot.expandedNodes || []),
            selectedNode: snapshot.selectedNode ? { id: snapshot.selectedNode.id, label: snapshot.selectedNode.label, type: snapshot.selectedNode.type, data: snapshot.selectedNode.data } : null,
          };
          localStorage.setItem(`kanivet.tabstate.${currentTab}`, JSON.stringify(toSave));
        }
      }
    } catch {}
  },

  pinDetail: (detail: any) => {
    const { currentTab } = get();
    const tabState = get().getCurrentTabState();
    if (!tabState || !currentTab || !detail) return;
    const pinnedDetail: PinnedDetail = {
      id: `${detail.metadata?.namespace || 'default'}-${detail.metadata?.name}-${Date.now()}`,
      name: detail.metadata?.name || 'Unknown', namespace: detail.metadata?.namespace, kind: detail.kind || 'Unknown', data: detail, cluster: currentTab,
    };
    const existingPin = tabState.pinnedDetails.find((p) => p.name === pinnedDetail.name && p.namespace === pinnedDetail.namespace && p.kind === pinnedDetail.kind);
    if (!existingPin) get().updateCurrentTabState({ pinnedDetails: [...tabState.pinnedDetails, pinnedDetail] });
  },

  unpinDetail: (detailId: string) => {
    const tabState = get().getCurrentTabState();
    if (!tabState) return;
    get().updateCurrentTabState({ pinnedDetails: tabState.pinnedDetails.filter((p) => p.id !== detailId) });
  },

  selectPinnedDetail: (detail: PinnedDetail) => { get().updateCurrentTabState({ detailData: detail.data, isDetailsPanelCollapsed: false }); },

  deleteResources: async (cluster: string, resource: any, items: any[]) => {
    const result = await api.deleteResources(cluster, resource.group, resource.version, resource.kind, items);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
    return result;
  },

  removeFinalizers: async (cluster: string, resource: any, items: any[]) => {
    const result = await api.removeFinalizers(cluster, resource.group, resource.version, resource.kind, items);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
    return result;
  },

  forceRefreshResources: async (cluster: string, resource: any, items: any[]) => {
    await api.forceRefreshResources(cluster, resource.group, resource.version, resource.kind, items);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
  },

  restartResource: async (cluster: string, resource: any, item: any) => {
    await api.restartResource(cluster, resource.group, resource.version, resource.kind, item.namespace, item.name);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
    const kind = resource.kind?.toLowerCase();
    if (kind?.includes('deployment') || kind?.includes('statefulset') || kind?.includes('daemonset') || kind?.includes('replicaset')) {
      const key = `${resource.group || '_'}/${resource.version}/${resource.name}/${item.namespace || '_'}/${item.name}`;
      get().addRolloutTracking(key, resource.group || '', resource.version, resource.name, item.namespace || '', item.name);
    }
  },

  triggerCronJob: async (cluster: string, item: any) => {
    const result = await api.triggerCronJob(cluster, item.namespace, item.name);
    api.invalidateCache(`items:${cluster}:batch:v1:jobs`);
    return result;
  },

  bulkRestartResources: async (cluster: string, resource: any, items: any[]) => {
    const resources = items.map((item) => ({ group: resource.group, version: resource.version, kind: resource.kind, namespace: item.namespace, name: item.name }));
    await api.bulkRestartResources(cluster, resources);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
    const kind = resource.kind?.toLowerCase();
    if (kind?.includes('deployment') || kind?.includes('statefulset') || kind?.includes('daemonset') || kind?.includes('replicaset')) {
      items.forEach((item) => {
        const key = `${resource.group || '_'}/${resource.version}/${resource.name}/${item.namespace || '_'}/${item.name}`;
        get().addRolloutTracking(key, resource.group || '', resource.version, resource.name, item.namespace || '', item.name);
      });
    }
  },

  scaleResource: async (cluster: string, resource: any, item: any, replicas: number) => {
    await api.scaleResource(cluster, resource.group, resource.version, resource.kind, item.namespace, item.name, replicas);
    api.invalidateCache(`items:${cluster}:${resource.group}:${resource.version}:${resource.name}`);
  },

  taintNode: async (cluster: string, nodeName: string, key: string, value: string, effect: string) => {
    await api.taintNode(cluster, nodeName, key, value, effect);
    api.invalidateCache(`items:${cluster}:_:v1:nodes`);
  },

  removeTaint: async (cluster: string, nodeName: string, key: string) => {
    await api.removeTaint(cluster, nodeName, key);
    api.invalidateCache(`items:${cluster}:_:v1:nodes`);
  },

  drainNode: async (cluster: string, nodeName: string, options?: any) => {
    try {
      const result = await api.drainNode(cluster, nodeName, options);
      api.invalidateCache(`items:${cluster}:_:v1:nodes`);
      notifyDrainComplete(nodeName, true);
      return result;
    } catch (e) {
      notifyDrainComplete(nodeName, false);
      throw e;
    }
  },

  cordonNode: async (cluster: string, nodeName: string, unschedulable: boolean) => {
    await api.cordonNode(cluster, nodeName, unschedulable);
    api.invalidateCache(`items:${cluster}:_:v1:nodes`);
  },

  startRolloutPolling: () => {
    const state = get();
    if (state.rolloutPollingInterval) return;
    const pollRollouts = async () => {
      const { activeTabs, currentTab } = get();
      const tab = activeTabs.find((t) => t.id === currentTab);
      if (!tab || !currentTab || !tab.state.rolloutRequests || tab.state.rolloutRequests.size === 0) return;
      const requests = Array.from(tab.state.rolloutRequests.values());
      try {
        const response = await api.getBatchRolloutStatus(currentTab, requests);
        const rolloutStatuses = new Map<string, RolloutStatusData>();
        response.results.forEach((result: any) => {
          if (result.status && !result.error) {
            rolloutStatuses.set(result.key, result.status);
            if (result.status.status === 'Complete') {
              const req = tab.state.rolloutRequests?.get(result.key);
              if (req?.name) notifyRolloutComplete(req.name);
              setTimeout(() => get().removeRolloutTracking(result.key), 5000);
            }
          } else if (result.error) {
            setTimeout(() => get().removeRolloutTracking(result.key), 2000);
          }
        });
        get().updateCurrentTabState({ rolloutStatuses });
      } catch (error) { console.error('Failed to fetch rollout statuses:', error); }
    };
    pollRollouts();
    const interval = setInterval(pollRollouts, 2000);
    set({ rolloutPollingInterval: interval });
  },

  stopRolloutPolling: () => {
    const state = get();
    if (state.rolloutPollingInterval) {
      clearInterval(state.rolloutPollingInterval);
      set({ rolloutPollingInterval: null });
    }
  },

  addRolloutTracking: (key: string, group: string, version: string, kind: string, namespace: string, name: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const rolloutRequests = new Map(tab.state.rolloutRequests || []);
    rolloutRequests.set(key, { group, version, kind, namespace, name });
    get().updateCurrentTabState({ rolloutRequests });
    if (!get().rolloutPollingInterval) get().startRolloutPolling();
  },

  removeRolloutTracking: (key: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const rolloutRequests = new Map(tab.state.rolloutRequests || []);
    const rolloutStatuses = new Map(tab.state.rolloutStatuses || []);
    rolloutRequests.delete(key);
    rolloutStatuses.delete(key);
    get().updateCurrentTabState({ rolloutRequests, rolloutStatuses });
    if (rolloutRequests.size === 0) get().stopRolloutPolling();
  },
});
