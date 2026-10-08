import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import { sortItems } from '../utils/columnSorting';
import { formatStatus } from '../utils/formatters';
import { getResourceCategory } from '../utils/resourceUtils';

interface UseResourceListStateProps {
  paneId?: string;
}

const EMPTY_ROLLOUT_STATUSES = new Map<string, any>();
// One empty selection for every render, so the table can skip a render that
// changes nothing. Selections are replaced, never edited in place.
const NO_SELECTION: Set<string> = new Set();

// The lower-cased text a search looks in, built once per item. Items are
// replaced on change, never edited in place, so an entry stays valid for the
// life of its item. Fields are separated by a newline, which a typed query
// cannot contain, so a match never spans two fields; `key=value` covers a
// match on the key or the value alone.
const searchTexts = new WeakMap<object, string>();

function searchTextOf(item: any): string {
  let text = searchTexts.get(item);
  if (text !== undefined) return text;
  const fields: string[] = [
    item.name,
    item.namespace,
    item.kind,
    item.message,
    item.reason,
  ].filter((f) => typeof f === 'string');
  // Where the pod is scheduled, so a node name narrows the list to its pods.
  const nodeName = item.nodeName || item.spec?.nodeName;
  if (typeof nodeName === 'string') fields.push(nodeName);
  fields.push(formatStatus(item));
  const labels = item.labels || {};
  for (const key in labels) fields.push(`${key}=${String(labels[key])}`);
  const annotations = item.annotations || {};
  for (const key in annotations)
    fields.push(`${key}=${String(annotations[key])}`);
  text = fields.join('\n').toLowerCase();
  searchTexts.set(item, text);
  return text;
}

export function matchesSearch(item: any, query: string): boolean {
  return searchTextOf(item).includes(query);
}

export function useResourceListState({ paneId }: UseResourceListStateProps) {
  const {
    currentTab,
    bottomTabs,
    updateCurrentTabState,
    updateResourceListTab,
    setActiveDetailTab,
    setActiveBottomTab,
    setActiveResourceListTab,
  } = useStore(useShallow((s) => ({ currentTab: s.currentTab, bottomTabs: s.bottomTabs, updateCurrentTabState: s.updateCurrentTabState, updateResourceListTab: s.updateResourceListTab, setActiveDetailTab: s.setActiveDetailTab, setActiveBottomTab: s.setActiveBottomTab, setActiveResourceListTab: s.setActiveResourceListTab })));

  // Only the fields the list renders from, so writes it does not show (tree
  // counts, detail refreshes) leave it alone.
  const tabState = useStore(
    useShallow((s) => {
      const t = s.getCurrentTabState();
      return {
        resourceListTabs: t?.resourceListTabs,
        detailTabs: t?.detailTabs,
        activeResourceListTab: t?.activeResourceListTab,
        activeDetailTab: t?.activeDetailTab,
        activeResourceListTabByPane: t?.activeResourceListTabByPane,
        selectedNode: t?.selectedNode,
        focusArea: t?.focusArea,
        namespaces: t?.namespaces,
        selectedNamespaces: t?.selectedNamespaces,
        rolloutStatuses: t?.rolloutStatuses,
        sortBy: t?.sortBy,
        sortOrder: t?.sortOrder,
        isLoadingListItems: t?.isLoadingListItems,
        hasReceivedInitialListData: t?.hasReceivedInitialListData,
        listSync: t?.listSync,
        loadError: t?.loadError,
      };
    }),
  );

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
  // The tree selection names the list of the pane activated last. A split
  // pane showing another resource type keys and formats its rows as its own.
  const tabNode = tabState?.selectedNode || null;
  const hasActiveTab = !!activeTab;
  const listResource = isResourceListTab ? activeTab?.resource : null;
  const selectedNode = useMemo(() => {
    if (!hasActiveTab) return null;
    if (
      !listResource?.version ||
      !listResource?.name ||
      tabNode?.type !== 'resource'
    )
      return tabNode;
    const shown = tabNode.data || {};
    if (
      (shown.group || '') === (listResource.group || '') &&
      shown.version === listResource.version &&
      shown.name === listResource.name
    )
      return tabNode;
    const id = `${getResourceCategory(listResource.group || '', listResource.name)}-${listResource.group || 'core'}-${listResource.version}-${listResource.name}`;
    return {
      id,
      label: listResource.name,
      type: 'resource' as const,
      data: listResource,
    };
  }, [hasActiveTab, listResource, tabNode]);
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

  const selectedResources =
    selectedResourcesByTab[activeTabIdRef.current] || NO_SELECTION;
  const setSelectedResources = useCallback((newSet: Set<string>) => {
    const tabId = activeTabIdRef.current;
    setSelectedResourcesByTab((prev) => ({ ...prev, [tabId]: newSet }));
  }, []);

  const rolloutStatuses = tabState?.rolloutStatuses || EMPTY_ROLLOUT_STATUSES;
  const sortBy = (isResourceListTab && activeTab?.sortBy) || tabState?.sortBy || 'name';
  const sortOrder = (isResourceListTab && activeTab?.sortOrder) || tabState?.sortOrder || 'asc';

  const isNamespaced = !!selectedNode?.data?.namespaced;
  const namespaceFilteredItems = useMemo(() => {
    const hasNamespaceFilter = isNamespaced && selectedNamespaces && selectedNamespaces.length > 0;
    if (!hasNamespaceFilter) return listItems;
    return listItems.filter((item: any) => item.namespace && selectedNamespaces.includes(item.namespace));
  }, [listItems, selectedNamespaces, isNamespaced]);

  // Filtered, sorted lists keyed by the list they came from. Switching back to a
  // tab whose list has not changed reuses its result instead of sorting again,
  // and the unchanged array lets the table skip its per-item work.
  const filteredCacheRef = useRef(new WeakMap<any[], Map<string, any[]>>());

  const getFilteredItems = useCallback(
    (searchQuery: string) => {
      const query = searchQuery?.toLowerCase() || '';
      const cacheKey = `${sortBy}\u0000${sortOrder}\u0000${query}`;
      let perList = filteredCacheRef.current.get(namespaceFilteredItems);
      const cached = perList?.get(cacheKey);
      if (cached) return cached;
      // A query containing an earlier one (the empty query included) matches a
      // subset of that one's rows, which are already sorted: narrow those.
      const sortPrefix = `${sortBy}\u0000${sortOrder}\u0000`;
      let narrower: any[] | undefined;
      let narrowerLength = -1;
      if (query && perList) {
        for (const [key, rows] of perList) {
          if (!key.startsWith(sortPrefix)) continue;
          const earlier = key.slice(sortPrefix.length);
          if (earlier.length > narrowerLength && query.includes(earlier)) {
            narrower = rows;
            narrowerLength = earlier.length;
          }
        }
      }
      const result = narrower
        ? narrower.filter((item: any) => matchesSearch(item, query))
        : sortItems(
            query
              ? namespaceFilteredItems.filter((item: any) =>
                  matchesSearch(item, query),
                )
              : namespaceFilteredItems,
            { sortBy, sortOrder },
          );
      if (!perList) {
        perList = new Map();
        filteredCacheRef.current.set(namespaceFilteredItems, perList);
      }
      // A list is typically viewed with a handful of sorts and searches; keep the latest few.
      if (perList.size >= 8)
        perList.delete(perList.keys().next().value as string);
      perList.set(cacheKey, result);
      return result;
    },
    [namespaceFilteredItems, sortBy, sortOrder],
  );

  const handleNamespaceChange = useCallback(
    async (namespace: string) => {
      const newNamespaces = namespace === 'all' ? [] : [namespace];
      updateCurrentTabState({
        selectedNamespace: namespace,
        selectedNamespaces: newNamespaces,
      });
      if (activeTabId) {
        updateResourceListTab(activeTabId, {
          selectedNamespaces: newNamespaces,
        });
      }
    },
    [updateCurrentTabState, activeTabId, updateResourceListTab],
  );

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
