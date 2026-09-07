import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import {
  kindToResource,
  kindToResourceDef,
  getResourceCategory,
  parseApiVersion,
} from '../utils/resourceUtils';

interface ResourceInfo {
  cluster: string;
  apiVersion: string;
  kind: string;
  name: string;
  namespace?: string;
}

const useResourceNavigation = (cluster: string) => {
  const {
    selectNode,
    loadListItems,
    loadDetails,
    recordNavigation,
    setFocusArea,
    openDetailTab,
    openResourceListTab,
    updateCurrentTabState,
    loadTreeData,
    expandNode,
    getCurrentTabState,
  } = useStore(useShallow((s) => ({ selectNode: s.selectNode, loadListItems: s.loadListItems, loadDetails: s.loadDetails, recordNavigation: s.recordNavigation, setFocusArea: s.setFocusArea, openDetailTab: s.openDetailTab, openResourceListTab: s.openResourceListTab, updateCurrentTabState: s.updateCurrentTabState, loadTreeData: s.loadTreeData, expandNode: s.expandNode, getCurrentTabState: s.getCurrentTabState })));

  const navigateToResource = async (resourceInfo: ResourceInfo) => {
    const {
      cluster: targetCluster,
      apiVersion,
      kind,
      name,
      namespace,
    } = resourceInfo;

    // Parse the apiVersion to get group and version
    const { group: apiGroup, version } = parseApiVersion(apiVersion);

    // Convert kind to plural resource name
    const resourceName = kindToResource(kind);

    // Create resource object
    const resource = {
      name: resourceName,
      group: apiGroup,
      version: version,
      kind: resourceName,
      namespaced: !!namespace,
    };

    // Determine category
    const categoryId = getResourceCategory(apiGroup, resourceName);

    // Create tree node
    const treeNode = {
      id: `${categoryId}-${apiGroup || 'core'}-${version}-${resourceName}`,
      label: resourceName,
      type: 'resource' as const,
      data: resource,
    };

    try {
      // Ensure tree data is loaded
      await loadTreeData(targetCluster);
      await new Promise((resolve) => setTimeout(resolve, 50));

      // If the resource is namespaced and we have a specific namespace, ensure we can see it
      if (namespace && resource.namespaced) {
        updateCurrentTabState({
          selectedNamespace: 'all',
          selectedNamespaces: [],
        });
        await new Promise((resolve) => setTimeout(resolve, 50));
      }

      // Ensure the category is expanded
      const tabState = getCurrentTabState();
      if (tabState && !tabState.expandedNodes.has(categoryId)) {
        await expandNode(targetCluster, categoryId, 'category', { categoryId });
        await new Promise((resolve) => setTimeout(resolve, 100));
      }

      // Navigate to the resource
      selectNode(treeNode);

      // Open the resource list tab immediately
      await openResourceListTab(resource, targetCluster, false);
      setFocusArea('list');

      // Load data in parallel without blocking the UI
      const success = await loadListItems(targetCluster, resource);

      if (success) {
        const item = {
          name: name,
          namespace: namespace,
          uid: `${namespace || 'cluster'}-${name}`,
          kind: kind,
          apiVersion: apiVersion,
        };

        const applySelection = () => {
          const state = useStore.getState();
          if (state.currentTab === targetCluster) {
            state.selectItem(item);
          }
          const tab = state.activeTabs.find((t) => t.id === targetCluster);
          if (tab) {
            const activeListTabId = tab.state.activeResourceListTab;
            if (activeListTabId) {
              const listTab = tab.state.resourceListTabs.find((rt) => rt.id === activeListTabId);
              const matchKey = `${item.namespace || 'default'}/${item.name}`;
              const itemInList = listTab?.items?.some(
                (i: any) => `${i.namespace || 'default'}/${i.name}` === matchKey,
              );
              const currentSelKey = listTab?.selectedItem
                ? `${listTab.selectedItem.namespace || 'default'}/${listTab.selectedItem.name}`
                : null;
              if (currentSelKey !== matchKey && (itemInList || !listTab?.items?.length)) {
                state.updateResourceListTab(activeListTabId, { selectedItem: item });
              }
            }
          }
        };

        applySelection();
        const retryTimers: NodeJS.Timeout[] = [];
        [100, 350, 800, 1500, 3000].forEach((delay) => {
          retryTimers.push(setTimeout(applySelection, delay));
        });

        // Load details and record navigation in parallel
        Promise.all([
          loadDetails(targetCluster, resource, item),
          recordNavigation('resource', treeNode.id, resource, item),
        ]).catch((error) => {
          console.error('Error loading details or navigation:', error);
        });

        return true;
      }

      return false;
    } catch (error) {
      console.error('Failed to navigate to resource:', error);
      return false;
    }
  };

  const navigateToLink = async (
    kind: string,
    name: string,
    namespace?: string,
    options?: { openInDetailTab?: boolean; isPinned?: boolean; apiVersion?: string },
  ) => {
    let apiVersion = options?.apiVersion;
    let resourceDef = kindToResourceDef(kind);

    if (!apiVersion && !resourceDef) {
      console.error(`Unknown kind: ${kind}`);
      return;
    }

    if (!apiVersion) {
      apiVersion = resourceDef!.group
        ? `${resourceDef!.group}/${resourceDef!.version}`
        : resourceDef!.version;
    }

    const isNamespaced = resourceDef ? resourceDef.namespaced : !!namespace;

    // Always navigate in the main view first
    const navigationSuccessful = await navigateToResource({
      cluster,
      apiVersion,
      kind,
      name,
      namespace: isNamespaced ? namespace : undefined,
    });

    // If openInDetailTab is true, also open in detail tab
    if (options?.openInDetailTab && navigationSuccessful) {
      const { group: apiGroup, version } = parseApiVersion(apiVersion);
      const resourceName = kindToResource(kind);

      const resource = {
        name: resourceName,
        group: apiGroup,
        version: version,
        kind: resourceName,
        namespaced: !!namespace,
      };

      const item = {
        name: name,
        namespace: namespace,
        uid: `${namespace || 'cluster'}-${name}`,
        kind: kind,
        apiVersion: apiVersion,
      };

      // Immediately open detail tab, then ensure details are loaded
      openDetailTab(resource, item, cluster, options.isPinned || false);
      await loadDetails(cluster, resource, item);
    }
  };

  return { navigateToResource, navigateToLink };
};

export default useResourceNavigation;
