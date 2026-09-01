import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { useStore } from '../store';
import { sortItems } from '../utils/columnSorting';
import { formatStatus } from '../utils/formatters';

interface UseResourceListStateProps {
  paneId?: string;
}

const EMPTY_ROLLOUT_STATUSES = new Map<string, any>();

export function useResourceListState({ paneId }: UseResourceListStateProps) {
  const {
    currentTab,
    getCurrentTabState,
    bottomTabs,
    updateCurrentTabState,
    updateResourceListTab,
    setActiveDetailTab,
    setActiveBottomTab,
    setActiveResourceListTab,
  } = useStore();

  const tabState = getCurrentTabState();

  const resourceListTabs = useMemo(() => {
    const tabs = tabState?.resourceListTabs || [];
    if (!paneId) return tabs;
    return tabs.filter((t) => t.paneId === paneId);
  }, [tabState?.resourceListTabs, paneId]);

  const activeResourceListTab = tabState?.activeResourceListTab;
  const activeDetailTab = tabState?.activeDetailTab;
  const activeBottomTab = useStore((s) => s.activeBottomTab);

  const centerDetailTabs = useMemo(() => {
    const tabs = (tabState?.detailTabs || []).filter((tab) => tab.location === 'center');
    if (!paneId) return tabs;
    return tabs.filter((t) => t.paneId === paneId);
  }, [tabState?.detailTabs, paneId]);

  const centerBottomTabs = useMemo(() => {
    const tabs = bottomTabs.filter((tab) => tab.location === 'center' && tab.cluster === currentTab);
    if (!paneId) return tabs;
    return tabs.filter((t) => t.paneId === paneId);
  }, [bottomTabs, paneId, currentTab]);

  const allCenterTabs = useMemo(() => {
    return [...resourceListTabs, ...centerDetailTabs, ...centerBottomTabs];
  }, [resourceListTabs, centerDetailTabs, centerBottomTabs]);

  const [localActiveTab, setLocalActiveTab] = useState<string | null>(null);
  const activateTabTimeoutRef = useRef<NodeJS.Timeout | null>(null);

  useEffect(() => {
    setLocalActiveTab(null);
  }, [currentTab]);

  useEffect(() => {
    if (activeResourceListTab && resourceListTabs.some((tab) => tab.id === activeResourceListTab)) {
      setLocalActiveTab(activeResourceListTab);
    } else if (activeDetailTab && centerDetailTabs.some((tab) => tab.id === activeDetailTab)) {
      setLocalActiveTab(activeDetailTab);
    } else if (activeBottomTab && centerBottomTabs.some((tab) => tab.id === activeBottomTab)) {
      setLocalActiveTab(activeBottomTab);
    }
  }, [activeResourceListTab, activeDetailTab, activeBottomTab, resourceListTabs, centerDetailTabs, centerBottomTabs]);

  const activateTabDelayed = useCallback((tabId: string) => {
    if (activateTabTimeoutRef.current) {
      clearTimeout(activateTabTimeoutRef.current);
    }
    activateTabTimeoutRef.current = setTimeout(() => {
      setLocalActiveTab(tabId);
      if (paneId) {
        try {
          useStore.getState().setActiveResourceListTabForPane(paneId, tabId);
        } catch { }
      } else {
        const tab = allCenterTabs.find((t) => t.id === tabId);
        if (tab) {
          if ('item' in tab) {
            setActiveDetailTab(tabId);
          } else if ('type' in tab && (tab.type === 'logs' || tab.type === 'shell' || tab.type === 'edit')) {
            setActiveBottomTab(tabId);
          } else {
            setActiveResourceListTab(tabId);
          }
        }
      }
      activateTabTimeoutRef.current = null;
    }, 0);
  }, [paneId, allCenterTabs, setActiveDetailTab, setActiveBottomTab, setActiveResourceListTab]);

  const activeTabId = useMemo(() => {
    if (allCenterTabs.length === 0) return null;
    const tabExists = (tabId: string | null) => tabId && allCenterTabs.some((tab) => tab.id === tabId);

    if (paneId) {
      const perPane = (tabState?.activeResourceListTabByPane || {}) as Record<string, string | null>;
      const paneActiveTab = perPane[paneId];
      if (tabExists(paneActiveTab)) return paneActiveTab;
      if (tabExists(localActiveTab)) return localActiveTab;
      return allCenterTabs[0].id;
    } else {
      if (tabExists(localActiveTab)) return localActiveTab;
      if (tabExists(activeResourceListTab || null)) return activeResourceListTab!;
      if (tabExists(activeDetailTab || null)) return activeDetailTab!;
      if (tabExists(activeBottomTab)) return activeBottomTab;
      return allCenterTabs[0].id;
    }
  }, [allCenterTabs, paneId, localActiveTab, tabState?.activeResourceListTabByPane, activeResourceListTab, activeDetailTab, activeBottomTab]);

  const activeTab = allCenterTabs.find((tab) => tab.id === activeTabId);
  const isResourceListTab = activeTab && 'items' in activeTab;

  useEffect(() => {
    if (allCenterTabs.length > 0 && !activeTabId) {
      const firstTab = allCenterTabs[0];
      setLocalActiveTab(firstTab.id);
      if (paneId) {
        try {
          useStore.getState().setActiveResourceListTabForPane(paneId, firstTab.id);
        } catch { }
      } else {
        if ('item' in firstTab) {
          setActiveDetailTab(firstTab.id);
        } else if ('type' in firstTab && (firstTab.type === 'logs' || firstTab.type === 'shell' || firstTab.type === 'edit')) {
          setActiveBottomTab(firstTab.id);
        } else {
          setActiveResourceListTab(firstTab.id);
        }
      }
    } else if (allCenterTabs.length === 0) {
      setLocalActiveTab(null);
      if (paneId) {
        try {
          useStore.getState().setActiveResourceListTabForPane(paneId, null);
        } catch { }
      }
    }
  }, [allCenterTabs.length, activeTabId, paneId, setActiveDetailTab, setActiveBottomTab, setActiveResourceListTab]);

  const listItems = useMemo(() => {
    if (!activeTab) return [];
    return (isResourceListTab && activeTab?.items) || [];
  }, [isResourceListTab, activeTab]);

  const selectedItem = (isResourceListTab && activeTab?.selectedItem) || null;
  const selectedNode = activeTab ? tabState?.selectedNode || null : null;
  const focusArea = tabState?.focusArea || 'tree';
  const namespaces = activeTab ? tabState?.namespaces || [] : [];

  const selectedNamespaces = useMemo(() => {
    if (!activeTab) return [];
    return (isResourceListTab && activeTab?.selectedNamespaces) || tabState?.selectedNamespaces || [];
  }, [isResourceListTab, activeTab, tabState?.selectedNamespaces]);

  const [selectedResourcesByTab, setSelectedResourcesByTab] = useState<Record<string, Set<string>>>({});

  useEffect(() => {
    const activeTabIds = new Set(allCenterTabs.map((t) => t.id));
    setSelectedResourcesByTab((prev) => {
      const keys = Object.keys(prev);
      const staleKeys = keys.filter((k) => !activeTabIds.has(k));
      if (staleKeys.length === 0) return prev;
      const newMap: Record<string, Set<string>> = {};
      for (const key of keys) {
        if (activeTabIds.has(key)) newMap[key] = prev[key];
      }
      return newMap;
    });
  }, [allCenterTabs]);

  const activeTabIdRef = useRef<string>('default');
  activeTabIdRef.current = activeTab?.id || 'default';

  const selectedResources = selectedResourcesByTab[activeTabIdRef.current] || new Set<string>();
  const setSelectedResources = useCallback((newSet: Set<string>) => {
    const tabId = activeTabIdRef.current;
    setSelectedResourcesByTab((prev) => ({ ...prev, [tabId]: newSet }));
  }, []);

  const rolloutStatuses = tabState?.rolloutStatuses || EMPTY_ROLLOUT_STATUSES;
  const sortBy = (isResourceListTab && activeTab?.sortBy) || tabState?.sortBy || 'name';
  const sortOrder = (isResourceListTab && activeTab?.sortOrder) || tabState?.sortOrder || 'asc';

  const namespaceFilteredItems = useMemo(() => {
    const isClusterScoped = !selectedNode?.data?.namespaced;
    const hasNamespaceFilter = !isClusterScoped && selectedNamespaces && selectedNamespaces.length > 0;
    if (!hasNamespaceFilter) return listItems;
    return listItems.filter((item: any) => item.namespace && selectedNamespaces.includes(item.namespace));
  }, [listItems, selectedNamespaces, selectedNode]);

  const getFilteredItems = useCallback((searchQuery: string) => {
    const query = searchQuery?.toLowerCase() || '';
    if (!query) return sortItems(namespaceFilteredItems, { sortBy, sortOrder });

    const filtered = namespaceFilteredItems.filter((item: any) => {
      if (item.name?.toLowerCase().includes(query)) return true;
      if (item.namespace?.toLowerCase().includes(query)) return true;
      if (item.kind?.toLowerCase().includes(query)) return true;
      if (item.message?.toLowerCase().includes(query)) return true;
      if (item.reason?.toLowerCase().includes(query)) return true;
      if (formatStatus(item).toLowerCase().includes(query)) return true;
      const labels = item.labels || {};
      for (const key in labels) {
        const val = String(labels[key]);
        if (key.toLowerCase().includes(query) || val.toLowerCase().includes(query) || `${key}=${val}`.toLowerCase().includes(query)) return true;
      }
      const annotations = item.annotations || {};
      for (const key in annotations) {
        const val = String(annotations[key]);
        if (key.toLowerCase().includes(query) || val.toLowerCase().includes(query) || `${key}=${val}`.toLowerCase().includes(query)) return true;
      }
      return false;
    });

    return sortItems(filtered, { sortBy, sortOrder });
  }, [namespaceFilteredItems, sortBy, sortOrder]);

  const handleNamespaceChange = useCallback(async (namespace: string) => {
    const newNamespaces = namespace === 'all' ? [] : [namespace];
    updateCurrentTabState({
      selectedNamespace: namespace,
      selectedNamespaces: newNamespaces,
    });
    if (activeTabId) {
      updateResourceListTab(activeTabId, { selectedNamespaces: newNamespaces });
    }
  }, [updateCurrentTabState, activeTabId, updateResourceListTab]);

  useEffect(() => {
    return () => {
      if (activateTabTimeoutRef.current) {
        clearTimeout(activateTabTimeoutRef.current);
        activateTabTimeoutRef.current = null;
      }
    };
  }, []);

  return {
    tabState,
    currentTab,
    resourceListTabs,
    centerDetailTabs,
    centerBottomTabs,
    allCenterTabs,
    activeTabId,
    activeTab,
    isResourceListTab,
    localActiveTab,
    setLocalActiveTab,
    activateTabDelayed,
    listItems,
    selectedItem,
    selectedNode,
    focusArea,
    namespaces,
    selectedNamespaces,
    selectedResources,
    setSelectedResources,
    selectedResourcesByTab,
    setSelectedResourcesByTab,
    activeTabIdRef,
    rolloutStatuses,
    sortBy,
    sortOrder,
    namespaceFilteredItems,
    getFilteredItems,
    handleNamespaceChange,
  };
}
