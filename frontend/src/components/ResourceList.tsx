import { useRef, useEffect, useState, useMemo, useCallback } from 'react';
import * as Tabs from '@radix-ui/react-tabs';
import {
  Cross2Icon,
  FileTextIcon,
  DesktopIcon,
  ReaderIcon,
  CheckCircledIcon,
  CrossCircledIcon,
} from '@radix-ui/react-icons';
import CrossplaneIcon from './icons/CrossplaneIcon';
import ResourceTable from './ResourceTable';
import ClusterDashboard from './ClusterDashboard';
import ArgoApplicationsPage from './ArgoApplicationsPage';
import HelmPage from './HelmPage';
import FinOpsDashboard from './finops/FinOpsDashboard';
import IncidentTimelinePage from './incidents/IncidentTimelinePage';
import ResourceControlsBar from './common/ResourceControlsBar';
import ResourceListDialogs from './ResourceListDialogs';
import { BottomTabContent, DetailTabContent } from './ResourceListTabContent';
import DisconnectedOverlay from './DisconnectedOverlay';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import { useRegisteredKeyboard } from '../hooks/useRegisteredKeyboard';
import { useTabManagement } from '../hooks/useTabManagement';
import { useResourceListState } from '../hooks/useResourceListState';
import { useResourceListActions } from '../hooks/useResourceListActions';
import { getColumnValue } from '../utils/resourceColumnValues';
import { printerColumnsFromItems } from '../utils/resourceListColumns';
import { createNavigationHandlers } from '../utils/keyboardShortcuts';
import { getResourceIcon } from '../utils/resourceIcons';
import { getNextSortOrder, getSortIndicator } from '../utils/columnSorting';
import './ResourceList.css';

interface ResourceListProps {
  paneId?: string;
  isFocusedPane?: boolean;
  onRequestPaneClose?: () => void;
}

const ResourceList = ({ paneId, isFocusedPane, onRequestPaneClose }: ResourceListProps) => {
  const {
    selectItem,
    loadDetails,
    openDetailTab,
    currentTab,
    recordNavigation,
    setFocusArea,
    reloadListItems,
    getCurrentTabState,
    updateCurrentTabState,
    updateResourceListTab,
    startRealtime,
    getDefaultColumns,
    closeResourceListTab,
    reorderResourceListTabs,
    moveDetailTab,
    closeDetailTab,
    setActiveDetailTab,
    setActiveResourceListTab,
    bottomTabs,
    closeBottomTab,
    moveBottomTab,
    setActiveBottomTab,
    pinResourceListTab,
    pinDetailTab,
    toasts,
    removeToast,
  } = useStore(useShallow((s) => ({ selectItem: s.selectItem, loadDetails: s.loadDetails, openDetailTab: s.openDetailTab, currentTab: s.currentTab, recordNavigation: s.recordNavigation, setFocusArea: s.setFocusArea, reloadListItems: s.reloadListItems, getCurrentTabState: s.getCurrentTabState, updateCurrentTabState: s.updateCurrentTabState, updateResourceListTab: s.updateResourceListTab, startRealtime: s.startRealtime, getDefaultColumns: s.getDefaultColumns, closeResourceListTab: s.closeResourceListTab, reorderResourceListTabs: s.reorderResourceListTabs, moveDetailTab: s.moveDetailTab, closeDetailTab: s.closeDetailTab, setActiveDetailTab: s.setActiveDetailTab, setActiveResourceListTab: s.setActiveResourceListTab, bottomTabs: s.bottomTabs, closeBottomTab: s.closeBottomTab, moveBottomTab: s.moveBottomTab, setActiveBottomTab: s.setActiveBottomTab, pinResourceListTab: s.pinResourceListTab, pinDetailTab: s.pinDetailTab, toasts: s.toasts, removeToast: s.removeToast })));

  const state = useResourceListState({ paneId });
  const {
    tabState,
    allCenterTabs,
    activeTabId,
    activeTab,
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
    setSelectedResourcesByTab,
    activeTabIdRef,
    rolloutStatuses,
    sortBy,
    sortOrder,
    namespaceFilteredItems,
    getFilteredItems,
    handleNamespaceChange,
  } = state;

  const currentLoadingRequestRef = useRef<string | null>(null);
  const currentAbortControllerRef = useRef<AbortController | null>(null);
  const tabsListRef = useRef<HTMLDivElement>(null);
  const [dropInsertIndex, setDropInsertIndex] = useState<number | null>(null);
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const lastSelectedKeyRef = useRef<string | null>(null);
  const lastTabIdRef = useRef<string | null>(null);
  const lastNodeIdRef = useRef<string | null>(null);
  const filteredItemsRef = useRef<any[]>([]);
  const pollingIntervalRef = useRef<NodeJS.Timeout | null>(null);

  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [showRemoveFinalizersConfirm, setShowRemoveFinalizersConfirm] = useState(false);
  const [actionMenu, setActionMenu] = useState<{ item: any; x: number; y: number } | null>(null);
  const [restartingItems, setRestartingItems] = useState<Set<string>>(new Set());
  const [searchQuery, setSearchQuery] = useState('');
  const [scaleDialog, setScaleDialog] = useState<{ item: any; currentReplicas: number } | null>(null);
  const [isScaling, setIsScaling] = useState(false);
  const [taintDialog, setTaintDialog] = useState<{ item: any } | null>(null);
  const [isTainting, setIsTainting] = useState(false);
  const [drainDialog, setDrainDialog] = useState<{ item: any } | null>(null);
  const [drainOptions, setDrainOptions] = useState({ ignoreDaemonsets: true, deleteEmptyDir: false, gracePeriod: 30 });
  const [isDraining, setIsDraining] = useState(false);
  const [drainResults, setDrainResults] = useState<any>(null);
  const isKeyboardNavigationRef = useRef(false);

  const getResourceKey = useCallback((item: any) => {
    const group = selectedNode?.data?.group || '_';
    const version = selectedNode?.data?.version || 'v1';
    const kind = selectedNode?.data?.kind || '';
    const namespace = item.namespace || '_';
    return `${group}/${version}/${kind}/${namespace}/${item.name}`;
  }, [selectedNode]);

  const actions = useResourceListActions({
    selectedNode,
    listItems,
    selectedResources,
    setSelectedResources,
    getResourceKey,
    setRestartingItems,
    setScaleDialog,
    setShowDeleteConfirm,
    setShowRemoveFinalizersConfirm,
    setDrainDialog,
    setTaintDialog,
    setActionMenu,
    scaleDialog,
    setIsScaling,
    taintDialog,
    setIsTainting,
    drainDialog,
    drainOptions,
    setIsDraining,
    setDrainResults,
  });

  const {
    tabContextMenu,
    setTabContextMenu,
    handleTabContextMenu,
    handleTabContextAction,
    handleSplitPane,
  } = useTabManagement(paneId, allCenterTabs, closeDetailTab, closeResourceListTab, closeBottomTab);

  useEffect(() => {
    return () => {
      currentLoadingRequestRef.current = null;
      if (currentAbortControllerRef.current) {
        currentAbortControllerRef.current.abort();
        currentAbortControllerRef.current = null;
      }
    };
  }, []);

  const checkAndClosePaneIfEmpty = useCallback(() => {
    if (!paneId || paneId === 'root') return;
    setTimeout(() => {
      const updatedTabState = getCurrentTabState();
      if (!updatedTabState) return;
      const resourceListTabsInPane = (updatedTabState.resourceListTabs || []).filter((t) => t.paneId === paneId).length;
      const detailTabsInPane = (updatedTabState.detailTabs || []).filter((t) => t.location === 'center' && t.paneId === paneId).length;
      const bottomTabsInPane = bottomTabs.filter((t) => t.location === 'center' && t.paneId === paneId).length;
      if (resourceListTabsInPane + detailTabsInPane + bottomTabsInPane === 0 && onRequestPaneClose) {
        onRequestPaneClose();
      }
    }, 50);
  }, [paneId, getCurrentTabState, bottomTabs, onRequestPaneClose]);

  const createEnhancedItemFromListData = (item: any, resource: any) => {
    return {
      kind: resource.kind || selectedNode?.data?.name || 'Resource',
      apiVersion: resource.group ? `${resource.group}/${resource.version}` : resource.version,
      metadata: {
        name: item.name,
        namespace: item.namespace,
        creationTimestamp: item.creationTimestamp,
        labels: item.labels || {},
        annotations: item.annotations || {},
        uid: item.uid,
        resourceVersion: item.resourceVersion,
        ownerReferences: item.ownerReferences || [],
        ...(item.metadata || {}),
      },
      status: {
        phase: item.phase || item.status,
        conditions: item.conditions || [],
        ...(item.readyReplicas !== undefined && { readyReplicas: item.readyReplicas }),
        ...(item.replicas !== undefined && { replicas: item.replicas }),
        ...(item.containerStatuses && { containerStatuses: item.containerStatuses }),
        ...(item.initContainerStatuses && { initContainerStatuses: item.initContainerStatuses }),
        ...(item.restarts !== undefined && !item.containerStatuses && { containerStatuses: [{ restartCount: item.restarts }] }),
        ...(item.nodeName && { nodeName: item.nodeName }),
        ...(item.status && typeof item.status === 'object' ? item.status : {}),
      },
      spec: {
        ...(item.replicas !== undefined && { replicas: item.replicas }),
        ...(item.nodeName && { nodeName: item.nodeName }),
        ...(item.serviceAccountName && { serviceAccountName: item.serviceAccountName }),
        ...(item.priorityClassName && { priorityClassName: item.priorityClassName }),
        ...(item.containers && { containers: item.containers }),
        ...(item.spec && typeof item.spec === 'object' ? item.spec : {}),
      },
      ...(item.type && { type: item.type }),
      ...(item.clusterIP && { clusterIP: item.clusterIP }),
      ...(item.ports && { ports: item.ports }),
      ...(item.selector && { selector: item.selector }),
      ...(item.images && { images: item.images }),
      ...(item.qosClass && { qosClass: item.qosClass }),
      ...Object.fromEntries(Object.entries(item).filter(([key]) => !['metadata', 'status', 'spec'].includes(key))),
    };
  };

  const handleItemSelect = (item: any, fromKeyboard: boolean = false) => {
    isKeyboardNavigationRef.current = fromKeyboard;
    selectItem(item);
    if (activeTab) {
      updateResourceListTab(activeTab.id, { selectedItem: item });
    }

    const tabStateNow = getCurrentTabState();
    if (tabStateNow?.activeDetailTab && tabStateNow.detailTabs.length > 0 && item && (item.name || item.metadata?.name)) {
      const resource = activeTab?.resource || selectedNode?.data;
      if (resource && currentTab) {
        const activeDetailTabId = tabStateNow.activeDetailTab;
        const activeDetailTabData = tabStateNow.detailTabs.find((dt) => dt.id === activeDetailTabId);

        if (activeDetailTabData) {
          const enhancedItem = createEnhancedItemFromListData(item, resource);
          currentLoadingRequestRef.current = null;
          if (currentAbortControllerRef.current) {
            currentAbortControllerRef.current.abort();
          }

          useStore.setState((s) => ({
            activeTabs: s.activeTabs.map((t) =>
              t.id === currentTab
                ? {
                  ...t,
                  state: {
                    ...t.state,
                    detailData: enhancedItem,
                    detailTabs: t.state.detailTabs.map((dt) =>
                      dt.id === activeDetailTabId ? { ...dt, item: enhancedItem } : dt
                    ),
                  },
                }
                : t
            ),
          }));

          const requestId = `${item.name}-${item.namespace || 'default'}-${Date.now()}`;
          currentLoadingRequestRef.current = requestId;
          const abortController = new AbortController();
          currentAbortControllerRef.current = abortController;

          loadDetails(currentTab, resource, item, abortController.signal)
            .then(() => {
              if (currentLoadingRequestRef.current !== requestId) {
                console.log(`Ignoring stale response for ${item.name}`);
              }
            })
            .catch((error) => {
              if (currentLoadingRequestRef.current === requestId) {
                currentLoadingRequestRef.current = null;
                currentAbortControllerRef.current = null;
              }
              if (error.name !== 'AbortError') {
                console.error('Failed to load details:', error);
              }
            });
        }
      }
    }
  };

  const handleItemOpen = async (item: any, isPinned: boolean = false) => {
    const resource = activeTab?.resource || selectedNode?.data;
    if (resource && currentTab) {
      const enhancedItem = createEnhancedItemFromListData(item, resource);
      currentLoadingRequestRef.current = null;
      if (currentAbortControllerRef.current) {
        currentAbortControllerRef.current.abort();
      }

      selectItem(item);
      if (activeTab) {
        updateResourceListTab(activeTab.id, { selectedItem: item });
      }
      openDetailTab(resource, enhancedItem, currentTab, isPinned);

      const requestId = `${item.name}-${item.namespace || 'default'}-${Date.now()}`;
      currentLoadingRequestRef.current = requestId;
      const abortController = new AbortController();
      currentAbortControllerRef.current = abortController;

      try {
        await loadDetails(currentTab, resource, item, abortController.signal);
        if (currentLoadingRequestRef.current === requestId) {
          const updatedTabState = getCurrentTabState();
          if (updatedTabState?.activeDetailTab && updatedTabState.detailData) {
            const activeDetailTabId = updatedTabState.activeDetailTab;
            useStore.setState((s) => ({
              activeTabs: s.activeTabs.map((t) =>
                t.id === currentTab
                  ? {
                    ...t,
                    state: {
                      ...t.state,
                      detailTabs: t.state.detailTabs.map((dt) =>
                        dt.id === activeDetailTabId ? { ...dt, item: updatedTabState.detailData } : dt
                      ),
                    },
                  }
                  : t
              ),
            }));
          }
        }
        await recordNavigation('item', `${item.namespace}/${item.name}`, resource, item);
      } catch (error: any) {
        if (currentLoadingRequestRef.current === requestId) {
          currentLoadingRequestRef.current = null;
          currentAbortControllerRef.current = null;
        }
        const isCancel = error?.name === 'AbortError' || error?.name === 'CanceledError' || error?.code === 'ERR_CANCELED' || error?.message === 'canceled';
        if (!isCancel) {
          console.error('Failed to load details in handleItemOpen:', error);
        }
      }
    }
  };

  useEffect(() => {
    const openScale = (e: any) => {
      const name = e?.detail?.name;
      const namespace = e?.detail?.namespace;
      const target = listItems.find((i: any) => i.name === name && i.namespace === namespace);
      if (target) {
        const currentReplicas = target.replicas || target.spec?.replicas || 1;
        setScaleDialog({ item: target, currentReplicas });
        setFocusArea('list');
      }
    };
    window.addEventListener('resourcelist:openScaleDialog', openScale as EventListener);
    return () => window.removeEventListener('resourcelist:openScaleDialog', openScale as EventListener);
  }, [listItems, setFocusArea]);

  useEffect(() => {
    const currentTabIdNow = activeTab?.id || null;
    const currentNodeId = selectedNode?.id || null;
    if (lastTabIdRef.current !== currentTabIdNow || lastNodeIdRef.current !== currentNodeId) {
      lastSelectedKeyRef.current = null;
      if (lastNodeIdRef.current !== currentNodeId) {
        setSelectedResources(new Set());
      }
      lastTabIdRef.current = currentTabIdNow;
      lastNodeIdRef.current = currentNodeId;
    }
  }, [activeTab?.id, selectedNode?.id, setSelectedResources]);

  useEffect(() => {
    if (pollingIntervalRef.current) {
      clearInterval(pollingIntervalRef.current);
      pollingIntervalRef.current = null;
    }
    if (selectedNode && selectedNode.type === 'resource' && currentTab) {
      startRealtime();
    }
  }, [selectedNode, currentTab, startRealtime]);

  const filteredItems = useMemo(() => getFilteredItems(searchQuery), [getFilteredItems, searchQuery]);
  filteredItemsRef.current = filteredItems;

  useEffect(() => {
    lastSelectedKeyRef.current = null;
  }, [sortBy, sortOrder]);

  const handleCheckboxChange = useCallback((item: any, event?: React.MouseEvent) => {
    const key = getResourceKey(item);
    const items = filteredItemsRef.current;
    const itemIndex = items.findIndex((i: any) => getResourceKey(i) === key);
    if (itemIndex === -1) return;
    const tabId = activeTabIdRef.current;

    setSelectedResourcesByTab((prev) => {
      const currentSelected = prev[tabId] || new Set<string>();
      if (event?.shiftKey && lastSelectedKeyRef.current) {
        const lastIndex = items.findIndex((i: any) => getResourceKey(i) === lastSelectedKeyRef.current);
        if (lastIndex === -1) {
          lastSelectedKeyRef.current = key;
          const newSelected = new Set(currentSelected);
          newSelected.add(key);
          return { ...prev, [tabId]: newSelected };
        }
        const start = Math.min(lastIndex, itemIndex);
        const end = Math.max(lastIndex, itemIndex);
        const newSelected = new Set(currentSelected);
        for (let i = start; i <= end; i++) {
          const rangeItem = items[i];
          if (rangeItem) newSelected.add(getResourceKey(rangeItem));
        }
        lastSelectedKeyRef.current = key;
        return { ...prev, [tabId]: newSelected };
      } else {
        const newSelected = new Set(currentSelected);
        if (newSelected.has(key)) {
          newSelected.delete(key);
        } else {
          newSelected.add(key);
        }
        lastSelectedKeyRef.current = key;
        return { ...prev, [tabId]: newSelected };
      }
    });
  }, [getResourceKey, activeTabIdRef, setSelectedResourcesByTab]);

  const handleSelectAll = useCallback(() => {
    const tabId = activeTabIdRef.current;
    setSelectedResourcesByTab((prev) => {
      const currentSelected = prev[tabId] || new Set<string>();
      if (currentSelected.size === filteredItems.length) {
        return { ...prev, [tabId]: new Set() };
      } else {
        const allKeys = new Set<string>(filteredItems.map((item: any) => getResourceKey(item)));
        return { ...prev, [tabId]: allKeys };
      }
    });
  }, [filteredItems, getResourceKey, activeTabIdRef, setSelectedResourcesByTab]);

  const handleClearSelection = useCallback(() => {
    setSelectedResources(new Set());
    lastSelectedKeyRef.current = null;
  }, [setSelectedResources]);

  const navHandlers = createNavigationHandlers(focusArea, filteredItems, selectedItem, handleItemSelect);

  useRegisteredKeyboard({
    ...Object.fromEntries(
      Object.entries(navHandlers).map(([key, handler]) => [
        key,
        { category: 'list' as const, description: `Navigate list (${key})`, handler },
      ])
    ),
    enter: {
      category: 'list',
      description: 'Open resource details',
      handler: async () => {
        if (focusArea !== 'list') return;
        if (selectedItem) await handleItemOpen(selectedItem);
      },
    },
    h: {
      category: 'list',
      description: 'Back to tree',
      handler: () => {
        if (focusArea === 'list') setFocusArea('tree');
      },
    },
    l: {
      category: 'list',
      description: 'Open details',
      handler: async () => {
        if (focusArea === 'list' && selectedItem) await handleItemOpen(selectedItem);
      },
    },
    escape: {
      category: 'list',
      description: 'Clear search / back to tree',
      handler: () => {
        if (searchQuery) {
          setSearchQuery('');
          searchInputRef.current?.blur();
        } else if (focusArea === 'list') {
          setFocusArea('tree');
        }
      },
    },
    '/': {
      category: 'list',
      description: 'Focus search',
      handler: (e) => {
        if (focusArea === 'list') {
          e.preventDefault();
          searchInputRef.current?.focus();
        }
      },
    },
    r: {
      category: 'list',
      description: 'Reload list',
      handler: async () => {
        if (focusArea === 'list') await reloadListItems();
      },
    },
  }, `resourceList-${paneId}`);

  const getColumnsForResourceKind = useCallback((kind: string): string[] => {
    if (!kind) return ['NAME', 'STATUS', 'AGE'];
    const isNamespaced = !!selectedNode?.data?.namespaced;
    return getDefaultColumns(kind, isNamespaced, printerColumnsFromItems(listItems));
  }, [getDefaultColumns, selectedNode?.data?.namespaced, listItems]);

  const resourceKindForColumns = activeTab?.resource?.kind || selectedNode?.data?.kind || listItems[0]?.kind || 'pods';
  const displayColumns = useMemo(() => getColumnsForResourceKind(resourceKindForColumns), [resourceKindForColumns, getColumnsForResourceKind]);

  const scrollPositionKey = `${currentTab}-${selectedNode?.id || 'none'}`;
  const scrollPosition = tabState?.scrollPositions?.[scrollPositionKey];

  // Persist scroll position outside React state to keep scroll on the
  // main thread cheap (60Hz events would otherwise trigger setState storms).
  // We commit to tab state at most once per animation frame.
  const scrollRafRef = useRef<number | null>(null);
  const latestScrollRef = useRef<number>(0);
  const handleScrollChange = useCallback((position: number) => {
    latestScrollRef.current = position;
    if (scrollRafRef.current !== null) return;
    scrollRafRef.current = requestAnimationFrame(() => {
      scrollRafRef.current = null;
      const current = useStore.getState().getCurrentTabState();
      updateCurrentTabState({
        scrollPositions: { ...(current?.scrollPositions || {}), [scrollPositionKey]: latestScrollRef.current },
      });
    });
  }, [scrollPositionKey, updateCurrentTabState]);
  useEffect(() => () => {
    if (scrollRafRef.current !== null) {
      cancelAnimationFrame(scrollRafRef.current);
      scrollRafRef.current = null;
    }
  }, []);

  const handleSort = useCallback((column: string) => {
    const columnKey = column.toLowerCase();
    const newSortOrder = getNextSortOrder(column, sortBy, sortOrder);
    updateCurrentTabState({ sortBy: columnKey, sortOrder: newSortOrder });
    if (activeTab) {
      updateResourceListTab(activeTab.id, { sortBy: columnKey, sortOrder: newSortOrder });
    }
  }, [sortBy, sortOrder, updateCurrentTabState, activeTab, updateResourceListTab]);

  const getColumnValueMemoized = useCallback(
    (item: any, column: string) => getColumnValue(item, column, selectedNode, rolloutStatuses, handleNamespaceChange, currentTab ?? undefined),
    [selectedNode, rolloutStatuses, handleNamespaceChange, currentTab]
  );

  const getSortIndicatorMemoized = useCallback(
    (column: string) => getSortIndicator(column, sortBy, sortOrder),
    [sortBy, sortOrder]
  );

  const renderResourceList = () => {
    const { getOverallState, clusterErrors } = useStore.getState();
    const isDisconnected = getOverallState() !== 'connected' || (currentTab && clusterErrors[currentTab]);
    return (
      <div className={`resource-list-content ${isDisconnected ? 'disconnected' : ''}`} style={{ position: 'relative' }}>
        <DisconnectedOverlay cluster={currentTab || undefined} lastUpdate={tabState?.hasReceivedInitialListData ? Date.now() : undefined} />
        {toasts.length > 0 && (
          <div className="resource-list-toasts">
            {toasts.map((toast) => (
              <div key={toast.id} className={`resource-list-toast resource-list-toast-${toast.type}`}>
                <div className="resource-list-toast-icon">
                  {toast.type === 'success' && <CheckCircledIcon width={14} height={14} />}
                  {toast.type === 'error' && <CrossCircledIcon width={14} height={14} />}
                </div>
                <span className="resource-list-toast-message">{toast.message}</span>
                <button className="resource-list-toast-close" onClick={() => removeToast(toast.id)}>
                  <Cross2Icon width={10} height={10} />
                </button>
              </div>
            ))}
          </div>
        )}
        {selectedNode && (
          <ResourceControlsBar
            namespaces={namespaces}
            selectedNamespaces={selectedNamespaces}
            onNamespaceChange={handleNamespaceChange}
            selectedCount={selectedResources.size}
            isActionsDisabled={false}
            resourceKind={selectedNode?.data?.kind}
            onBulkAction={actions.handleBulkAction}
            onClearSelection={handleClearSelection}
            searchQuery={searchQuery}
            onSearchChange={setSearchQuery}
            filteredCount={filteredItems.length}
            totalCount={namespaceFilteredItems.length}
          />
        )}
        <ResourceTable
          key={activeTabId}
          listItems={filteredItems}
          selectedItem={selectedItem}
          selectedResources={selectedResources}
          displayColumns={displayColumns}
          onItemOpen={handleItemOpen}
          onCheckboxChange={handleCheckboxChange}
          onSelectAll={handleSelectAll}
          onSort={handleSort}
          getColumnValue={getColumnValueMemoized}
          isKeyboardNavigationRef={isKeyboardNavigationRef}
          getSortIndicator={getSortIndicatorMemoized}
          getResourceKey={getResourceKey}
          selectedNode={selectedNode}
          onActionClick={actions.handleActionClick}
          restartingItems={restartingItems}
          resourceKind={resourceKindForColumns}
          isLoading={tabState?.isLoadingListItems || false}
          hasReceivedData={tabState?.hasReceivedInitialListData || false}
          rolloutStatuses={rolloutStatuses}
          handleNamespaceChange={handleNamespaceChange}
          loadError={tabState?.loadError ?? null}
          cluster={currentTab}
          onRetry={reloadListItems}
          scrollPosition={scrollPosition}
          onScrollChange={handleScrollChange}
          totalBeforeNamespaceFilter={listItems.length}
          hasNamespaceFilter={selectedNamespaces && selectedNamespaces.length > 0}
          onClearNamespaceFilter={() => handleNamespaceChange('all')}
        />
      </div>
    );
  };

  const openClusterDashboards = useStore(useShallow((s) =>
    s.activeTabs.filter((tab) => (tab.state?.resourceListTabs || []).some((rlt: any) => rlt.resource?.kind === 'ClusterDashboard')).map((tab) => tab.id)
  ));

  const isDashboardActiveTab = activeTab?.resource?.kind === 'ClusterDashboard';

  return (
    <div
      className={`resource-list ${focusArea === 'list' ? 'focused' : ''}`}
      onClick={() => setFocusArea('list')}
      onDragOver={(e) => {
        const isTabDrag = e.dataTransfer.types.includes('tab-type');
        const isSamePane = e.dataTransfer.types.includes(`source-pane-id:${paneId || 'root'}`);
        if (isTabDrag && !isSamePane) {
          e.preventDefault();
          e.dataTransfer.dropEffect = 'move';
          if (!e.currentTarget.classList.contains('drag-over')) {
            e.currentTarget.classList.add('drag-over');
          }
        }
      }}
      onDragLeave={(e) => {
        const rect = e.currentTarget.getBoundingClientRect();
        if (e.clientX < rect.left || e.clientX >= rect.right || e.clientY < rect.top || e.clientY >= rect.bottom) {
          e.currentTarget.classList.remove('drag-over');
        }
      }}
      onDrop={async (e) => {
        if (!e.dataTransfer.types.includes('tab-type')) return;
        e.preventDefault();
        e.currentTarget.classList.remove('drag-over');

        const tabType = e.dataTransfer.getData('tab-type');
        const tabId = e.dataTransfer.getData('tab-id');
        const sourcePane = e.dataTransfer.getData('source-pane');
        const sourcePaneId = e.dataTransfer.getData('source-pane-id') || 'root';

        if (tabType === 'detail' && sourcePane === 'detail' && tabId) {
          e.stopPropagation();
          moveDetailTab(tabId, 'center', paneId || 'root');
          setLocalActiveTab(tabId);
        } else if (tabType === 'bottom' && sourcePane === 'bottom' && tabId) {
          e.stopPropagation();
          moveBottomTab(tabId, 'center', paneId || 'root');
          activateTabDelayed(tabId);
        } else if (tabType === 'resource-list' && tabId) {
          const moveToPane = (paneId as string) || 'root';
          try {
            useStore.getState().moveResourceListTabToPane(tabId, moveToPane);
          } catch { }
          setLocalActiveTab(tabId);
          if (sourcePaneId !== moveToPane) {
            setTimeout(() => {
              window.dispatchEvent(new CustomEvent('centerPane:requestClose', { detail: { paneId: sourcePaneId } }));
            }, 0);
          }
        }
      }}
    >
      <Tabs.Root
        value={activeTabId || ''}
        onValueChange={(tabId) => {
          setLocalActiveTab(tabId);
          setActiveResourceListTab(tabId);
          if (paneId) {
            try {
              useStore.getState().setActiveResourceListTabForPane(paneId, tabId);
            } catch { }
          }
          const tab = allCenterTabs.find((t) => t.id === tabId);
          if (!paneId && tab && 'item' in tab) {
            setActiveDetailTab(tabId);
          } else if (!paneId && tab && 'type' in tab && (tab.type === 'logs' || tab.type === 'shell' || tab.type === 'edit')) {
            setActiveBottomTab(tabId);
          } else if (!paneId) {
            setActiveResourceListTab(tabId);
          }
        }}
        className="resource-list-tabs-root"
      >
        <div className="resource-list-tabs-header">
          <Tabs.List
            ref={tabsListRef}
            className="resource-list-tabs-list"
            onDragOver={(e) => {
              const isTab = e.dataTransfer.types.includes('tab-type');
              if (!isTab) return;
              e.preventDefault();
              e.dataTransfer.dropEffect = 'move';
              const isSamePane = e.dataTransfer.types.includes(`source-pane-id:${paneId || 'root'}`);
              if (isSamePane && tabsListRef.current) {
                const tabElements = tabsListRef.current.querySelectorAll('.resource-list-tab-wrapper');
                const mouseX = e.clientX;
                let insertIdx = tabElements.length;
                for (let i = 0; i < tabElements.length; i++) {
                  const rect = tabElements[i].getBoundingClientRect();
                  const midPoint = rect.left + rect.width / 2;
                  if (mouseX < midPoint) {
                    insertIdx = i;
                    break;
                  }
                }
                setDropInsertIndex(insertIdx);
              }
            }}
            onDragLeave={(e) => {
              const rect = e.currentTarget.getBoundingClientRect();
              if (e.clientX < rect.left || e.clientX >= rect.right || e.clientY < rect.top || e.clientY >= rect.bottom) {
                setDropInsertIndex(null);
              }
            }}
            onDrop={async (e) => {
              if (!e.dataTransfer.types.includes('tab-type')) return;
              e.preventDefault();
              const isSamePane = e.dataTransfer.types.includes(`source-pane-id:${paneId || 'root'}`);
              const tabType = e.dataTransfer.getData('tab-type');
              const tabId = e.dataTransfer.getData('tab-id');
              const sourcePane = e.dataTransfer.getData('source-pane');
              const fromIndex = parseInt(e.dataTransfer.getData('tab-index'));

              if (isSamePane && tabType === 'resource-list' && dropInsertIndex !== null) {
                e.stopPropagation();
                const toIndex = fromIndex < dropInsertIndex ? dropInsertIndex - 1 : dropInsertIndex;
                if (fromIndex !== toIndex) {
                  reorderResourceListTabs(fromIndex, toIndex);
                }
                setDropInsertIndex(null);
              } else if (tabType === 'detail' && sourcePane === 'detail' && tabId) {
                moveDetailTab(tabId, 'center', paneId || 'root');
                setLocalActiveTab(tabId);
              } else if (tabType === 'bottom' && sourcePane === 'bottom' && tabId) {
                moveBottomTab(tabId, 'center', paneId || 'root');
                activateTabDelayed(tabId);
              }
              setDropInsertIndex(null);
            }}
          >
            {allCenterTabs.map((tab, index) => {
              const isDetailTabType = 'item' in tab;
              const isResourceListTabType = 'items' in tab;
              const isBottomTabType = 'type' in tab && (tab.type === 'logs' || tab.type === 'deployment-logs' || tab.type === 'shell' || tab.type === 'edit' || tab.type === 'trace');

              return (
                <div
                  key={tab.id}
                  className={`resource-list-tab-wrapper ${isDetailTabType ? 'detail-tab-in-center' : ''} ${isBottomTabType ? 'bottom-tab-in-center' : ''}`}
                  draggable
                  onDragStart={(e) => {
                    const sourcePaneId = (paneId as string) || 'root';
                    e.dataTransfer.effectAllowed = 'move';
                    e.dataTransfer.setData('tab-index', index.toString());
                    e.dataTransfer.setData('tab-id', tab.id);
                    e.dataTransfer.setData('tab-type', isDetailTabType ? 'detail' : isBottomTabType ? 'bottom' : 'resource-list');
                    e.dataTransfer.setData('source-pane', 'center');
                    e.dataTransfer.setData('source-pane-id', sourcePaneId);
                    e.dataTransfer.setData(`source-pane-id:${sourcePaneId}`, '');
                    e.currentTarget.classList.add('dragging');
                  }}
                  onDragEnd={(e) => {
                    e.currentTarget.classList.remove('dragging');
                    setDropInsertIndex(null);
                  }}
                >
                  {dropInsertIndex === index && <div className="tab-drop-indicator" />}
                  <Tabs.Trigger
                    value={tab.id}
                    className="resource-list-tab"
                    onDoubleClick={() => {
                      if ('isPinned' in tab && !tab.isPinned) {
                        if (isResourceListTabType) {
                          pinResourceListTab(tab.id);
                        } else if (isDetailTabType) {
                          pinDetailTab(tab.id);
                        }
                      }
                    }}
                    onContextMenu={(e) => handleTabContextMenu(e, tab, isDetailTabType, isBottomTabType, isResourceListTabType)}
                  >
                    <span className="tab-icon">
                      {isBottomTabType ? (
                        tab.type === 'logs' ? <ReaderIcon /> :
                          tab.type === 'shell' ? <DesktopIcon /> :
                            tab.type === 'edit' ? <FileTextIcon /> :
                              tab.type === 'trace' ? <CrossplaneIcon width={14} height={14} /> : null
                      ) : (
                        getResourceIcon(isDetailTabType ? tab.item?.kind : tab.resource?.kind || 'Unknown')
                      )}
                    </span>
                    <span
                      className="tab-title"
                      style={{
                        fontStyle: 'isPinned' in tab && !tab.isPinned ? 'italic' : 'normal',
                        fontWeight: isFocusedPane && activeTabId === tab.id ? 550 : 'normal',
                      }}
                    >
                      {('customTitle' in tab ? tab.customTitle : null) || tab.title}
                    </span>
                  </Tabs.Trigger>
                  <button
                    className="tab-close"
                    onClick={(e) => {
                      e.stopPropagation();
                      if (isDetailTabType) {
                        closeDetailTab(tab.id);
                      } else if (isBottomTabType) {
                        closeBottomTab(tab.id);
                      } else {
                        closeResourceListTab(tab.id);
                      }
                      checkAndClosePaneIfEmpty();
                    }}
                    title="Close tab"
                  >
                    <Cross2Icon />
                  </button>
                </div>
              );
            })}
            {dropInsertIndex === allCenterTabs.length && <div className="tab-drop-indicator tab-drop-indicator-end" />}
          </Tabs.List>
          <div className="tabs-list-actions">
            <button
              className="split-button"
              onClick={() => window.dispatchEvent(new CustomEvent('centerPane:split', { detail: { paneId: paneId || 'root', direction: 'vertical' } }))}
              title="Split Vertical (⌘\)"
              aria-label="Split pane vertically"
            >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
                <rect x="1" y="1" width="6" height="14" />
                <rect x="9" y="1" width="6" height="14" />
              </svg>
            </button>
            <button
              className="split-button"
              onClick={() => window.dispatchEvent(new CustomEvent('centerPane:split', { detail: { paneId: paneId || 'root', direction: 'horizontal' } }))}
              title="Split Horizontal (⌘⇧\)"
              aria-label="Split pane horizontally"
            >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
                <rect x="1" y="1" width="14" height="6" />
                <rect x="1" y="9" width="14" height="6" />
              </svg>
            </button>
            {paneId && paneId !== 'root' && onRequestPaneClose && (
              <button
                className="split-button close-pane-button"
                onClick={onRequestPaneClose}
                title="Close Pane"
                aria-label="Close pane"
              >
                <Cross2Icon />
              </button>
            )}
          </div>
        </div>
        {allCenterTabs.length === 0 ? (
          <div
            className="resource-list-tab-content"
            style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-secondary)' }}
            onDragOver={(e) => {
              if (e.dataTransfer.types.includes('tab-type')) {
                e.preventDefault();
                e.dataTransfer.dropEffect = 'move';
              }
            }}
            onDrop={async (e) => {
              if (!e.dataTransfer.types.includes('tab-type')) return;
              e.preventDefault();
              const tabId = e.dataTransfer.getData('tab-id');
              const tabType = e.dataTransfer.getData('tab-type');
              const sourcePane = e.dataTransfer.getData('source-pane');

              if (tabType === 'detail' && sourcePane === 'detail' && tabId) {
                e.stopPropagation();
                moveDetailTab(tabId, 'center', paneId || 'root');
                activateTabDelayed(tabId);
              } else if (tabType === 'bottom' && sourcePane === 'bottom' && tabId) {
                e.stopPropagation();
                moveBottomTab(tabId, 'center', paneId || 'root');
                activateTabDelayed(tabId);
              }
            }}
          >
            <div style={{ textAlign: 'center' }}>
              <div style={{ marginBottom: '8px' }}>No tabs open in this pane</div>
              <div style={{ fontSize: '12px', marginBottom: '4px' }}>Select a resource from the sidebar to open a tab</div>
              <div style={{ fontSize: '12px', color: 'var(--text-tertiary)' }}>Or drag tabs here from other panes</div>
            </div>
          </div>
        ) : (
          allCenterTabs.map((tab) => {
            const isDetailTabType = 'item' in tab;
            const isBottomTabType = 'type' in tab && (tab.type === 'logs' || tab.type === 'deployment-logs' || tab.type === 'shell' || tab.type === 'edit' || tab.type === 'trace');

            return (
              <Tabs.Content key={tab.id} value={tab.id} className="resource-list-tab-content">
                {isDetailTabType ? (
                  <DetailTabContent tab={tab} />
                ) : isBottomTabType ? (
                  <BottomTabContent tab={tab} />
                ) : (
                  <div style={{ display: activeTabId === tab.id ? 'flex' : 'none', flexDirection: 'column', height: '100%', minHeight: 0, overflow: 'hidden' }}>
                    {tab.resource?.kind === 'ClusterDashboard' ? (
                      null
                    ) : tab.resource?.kind === 'FinOpsDashboard' ? (
                      <FinOpsDashboard key={`finops-dashboard-${tab.id}`} />
                    ) : tab.resource?.kind === 'HelmReleases' ? (
                      <HelmPage key={`helm-page-${tab.id}`} cluster={currentTab || ''} />
                    ) : tab.resource?.kind === 'IncidentTimeline' ? (
                      <IncidentTimelinePage key={`incident-timeline-${tab.id}`} cluster={currentTab || ''} />
                    ) : tab.resource?.kind === 'ArgoApplicationsOverview' ? (
                      <ArgoApplicationsPage key={`argo-apps-${tab.id}`} cluster={currentTab || ''} />
                    ) : (
                      renderResourceList()
                    )}
                  </div>
                )}
              </Tabs.Content>
            );
          })
        )}
      </Tabs.Root>

      {openClusterDashboards.map(cluster => (
        <div
          key={`persistent-dashboard-${cluster}`}
          className="persistent-dashboard-layer"
          style={{
            display: currentTab === cluster && isDashboardActiveTab ? 'flex' : 'none',
            flexDirection: 'column',
            position: 'absolute',
            top: 38,
            left: 0,
            right: 0,
            bottom: 0,
            zIndex: 10,
            backgroundColor: 'var(--bg-primary)',
            overflow: 'auto',
          }}
        >
          <ClusterDashboard cluster={cluster} />
        </div>
      ))}

      <ResourceListDialogs
        showDeleteConfirm={showDeleteConfirm}
        setShowDeleteConfirm={setShowDeleteConfirm}
        selectedResourcesCount={selectedResources.size}
        onConfirmDelete={actions.confirmDelete}
        showRemoveFinalizersConfirm={showRemoveFinalizersConfirm}
        setShowRemoveFinalizersConfirm={setShowRemoveFinalizersConfirm}
        onConfirmRemoveFinalizers={actions.confirmRemoveFinalizers}
        scaleDialog={scaleDialog}
        setScaleDialog={setScaleDialog}
        isScaling={isScaling}
        onConfirmScale={actions.confirmScale}
        taintDialog={taintDialog}
        setTaintDialog={setTaintDialog}
        isTainting={isTainting}
        onConfirmTaint={actions.confirmTaint}
        drainDialog={drainDialog}
        setDrainDialog={setDrainDialog}
        drainOptions={drainOptions}
        setDrainOptions={setDrainOptions}
        isDraining={isDraining}
        onConfirmDrain={actions.confirmDrain}
        drainResults={drainResults}
        setDrainResults={setDrainResults}
        actionMenu={actionMenu}
        setActionMenu={setActionMenu}
        selectedNode={selectedNode}
        onActionSelect={actions.handleActionSelect}
        tabContextMenu={tabContextMenu}
        setTabContextMenu={setTabContextMenu}
        onTabContextAction={handleTabContextAction}
        onSplitPane={handleSplitPane}
      />
    </div>
  );
};

export default ResourceList;
