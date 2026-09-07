import { useState, useEffect, useRef, useCallback } from 'react';

import { Cross2Icon, PinLeftIcon, PinRightIcon } from '@radix-ui/react-icons';
import * as Tabs from '@radix-ui/react-tabs';
import * as Tooltip from '@radix-ui/react-tooltip';

import TabContent from './common/TabContent';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import { currentRealtimeTopic } from '../store/realtimeSlice';
import useKeyboard from '../hooks/useKeyboard';
import useResizable from '../hooks/useResizable';
import { useVisibleInterval } from '../hooks/useVisibleInterval';

const EMPTY_DETAIL_TABS: any[] = [];
const DETAIL_STALE_MS = 30000;
import api from '../services/api';
import { getResourceIcon } from '../utils/resourceIcons';
import { workloadControllerKinds } from '../utils/resourceActions';
import './DetailView.css';

interface DetailViewProps {
  mode?: string;
}

const DetailView = ({ mode: _mode }: DetailViewProps = {}) => {
  const {
    setFocusArea,
    toggleDetailsPanel,
    setDetailsPanelCollapsed,
    currentTab,
    updateDetailData,
    closeDetailTab,
    setActiveDetailTab,
    reorderDetailTabs,
    moveDetailTab,
    pinDetailTab,
    openDetailTab,
  } = useStore(useShallow((s) => ({ setFocusArea: s.setFocusArea, toggleDetailsPanel: s.toggleDetailsPanel, setDetailsPanelCollapsed: s.setDetailsPanelCollapsed, currentTab: s.currentTab, updateDetailData: s.updateDetailData, closeDetailTab: s.closeDetailTab, setActiveDetailTab: s.setActiveDetailTab, reorderDetailTabs: s.reorderDetailTabs, moveDetailTab: s.moveDetailTab, pinDetailTab: s.pinDetailTab, openDetailTab: s.openDetailTab })));
  const { detailTabs, activeDetailTab, focusArea, isDetailsPanelCollapsed } = useStore(useShallow((s) => {
    const t = s.getCurrentTabState();
    return { detailTabs: t?.detailTabs || EMPTY_DETAIL_TABS, activeDetailTab: t?.activeDetailTab, focusArea: t?.focusArea || 'tree', isDetailsPanelCollapsed: t?.isDetailsPanelCollapsed || false };
  }));
  const activeTab = detailTabs.find((tab) => tab.id === activeDetailTab);
  const activeCluster = currentTab || '';
  const lastRefreshRef = useRef(0);
  const detailDirtyRef = useRef(true);
  const [viewportWidth, setViewportWidth] = useState<number>(
    typeof window !== 'undefined' ? window.innerWidth : 1280,
  );
  const [showDropZone, setShowDropZone] = useState(false);
  const [tabContextMenu, setTabContextMenu] = useState<{
    x: number;
    y: number;
    tab: any;
  } | null>(null);
  const tabContextMenuRef = useRef<HTMLDivElement>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [isResourceDragOver, setIsResourceDragOver] = useState(false);

  const handleResourceDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    setIsResourceDragOver(false);
    if (!currentTab) return;
    try {
      const data = e.dataTransfer.getData('application/json');
      if (!data) return;
      const resource = JSON.parse(data);
      if (resource.type !== 'k8s-resource') return;
      const details = await api.getResourceDetails(
        currentTab,
        resource.group,
        resource.version,
        resource.kind,
        resource.namespace,
        resource.name,
      );
      if (details) {
        openDetailTab(
          { group: resource.group, version: resource.version, kind: resource.kind },
          details,
          currentTab,
          true,
        );
        setDetailsPanelCollapsed(false);
      }
    } catch (err) {
      console.error('Failed to open dropped resource:', err);
    }
  }, [currentTab, openDetailTab, setDetailsPanelCollapsed]);

  const handleTabContextMenu = (e: React.MouseEvent, tab: any) => {
    e.preventDefault();
    setTabContextMenu({ x: e.clientX, y: e.clientY, tab });
  };

  useEffect(() => {
    const onResize = () => setViewportWidth(window.innerWidth);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        tabContextMenuRef.current &&
        !tabContextMenuRef.current.contains(e.target as Node)
      ) {
        setTabContextMenu(null);
      }
    };

    if (tabContextMenu) {
      document.addEventListener('mousedown', handleClickOutside);
      return () =>
        document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [tabContextMenu]);

  const prevViewportWidthRef = useRef(viewportWidth);
  useEffect(() => {
    const collapseThreshold = 1080;
    const wasAboveThreshold = prevViewportWidthRef.current >= collapseThreshold;
    const isNowBelowThreshold = viewportWidth < collapseThreshold;
    prevViewportWidthRef.current = viewportWidth;
    if (wasAboveThreshold && isNowBelowThreshold && !isDetailsPanelCollapsed) {
      const hasEverOpened =
        (window as any).__kanivetDetailEverOpened === true ||
        detailTabs.length > 0;
      if (hasEverOpened) setDetailsPanelCollapsed(true);
    }
  }, [viewportWidth, isDetailsPanelCollapsed, setDetailsPanelCollapsed, detailTabs.length]);

  // Show drop zone when dragging a detail tab from center
  useEffect(() => {
    let isDraggingDetailTab = false;

    const handleDragStart = (e: DragEvent) => {
      setIsDragging(true);
      const target = e.target as HTMLElement;
      if (!target || typeof target.closest !== 'function') return;

      const isFromCenter = target.closest('.resource-list');
      const isDetailTabWrapper = target.closest('.detail-tab-in-center');

      if (isFromCenter && isDetailTabWrapper) {
        isDraggingDetailTab = true;
      }
    };

    const handleDragOver = (e: DragEvent) => {
      const windowWidth = window.innerWidth;
      const mouseX = e.clientX;
      const nearRightEdge = mouseX > windowWidth - 150;

      if (isDraggingDetailTab && nearRightEdge) {
        setShowDropZone(true);
      } else if (isDraggingDetailTab) {
        setShowDropZone(false);
      }
    };

    const handleDragEnd = () => {
      isDraggingDetailTab = false;
      setShowDropZone(false);
      setIsResourceDragOver(false);
      setTimeout(() => setIsDragging(false), 100);
    };

    const handleDrop = () => {
      isDraggingDetailTab = false;
      setShowDropZone(false);
      setTimeout(() => {
        setIsDragging(false);
        setIsResourceDragOver(false);
      }, 100);
    };

    document.addEventListener('dragstart', handleDragStart);
    document.addEventListener('dragover', handleDragOver);
    document.addEventListener('dragend', handleDragEnd);
    document.addEventListener('drop', handleDrop);

    return () => {
      document.removeEventListener('dragstart', handleDragStart);
      document.removeEventListener('dragover', handleDragOver);
      document.removeEventListener('dragend', handleDragEnd);
      document.removeEventListener('drop', handleDrop);
    };
  }, []);

  // Removed auto-collapse - users have full control over panel width

  // Responsive constraints: ensure minimum width for actions to remain visible
  // Cap maximum width to prevent layout issues at very large sizes
  const responsiveMax = Math.min(
    Math.floor(viewportWidth * 0.6), // Reduce from 75% to 60%
    800, // Hard cap at 800px to prevent content issues
  );

  useKeyboard({
    h: () => focusArea === 'detail' && setFocusArea('list'),
    'meta+\\': toggleDetailsPanel,
    escape: () => !isDetailsPanelCollapsed && toggleDetailsPanel(),
  });

  const item = activeTab?.item;
  const resource = activeTab?.resource;
  const itemName = item?.metadata?.name || item?.name || '';
  const itemNamespace = item?.metadata?.namespace || item?.namespace || '';
  const isHelmRelease = item?.kind === 'HelmRelease' || resource?.kind === 'HelmRelease';
  const refreshKey = activeTab && !isDetailsPanelCollapsed && !isHelmRelease
    ? `${activeCluster}|${resource?.group || ''}|${resource?.version || ''}|${resource?.kind || ''}|${itemNamespace}|${itemName}`
    : '';

  useEffect(() => {
    detailDirtyRef.current = true;
    lastRefreshRef.current = 0;
  }, [refreshKey]);

  useEffect(() => {
    const onChanged = (e: Event) => {
      const d = (e as CustomEvent<{ name: string; namespace: string }>).detail;
      if (d && d.name === itemName && (d.namespace || '') === itemNamespace) detailDirtyRef.current = true;
    };
    window.addEventListener('kanivet:detail-item-changed', onChanged);
    return () => window.removeEventListener('kanivet:detail-item-changed', onChanged);
  }, [itemName, itemNamespace]);

  // While the live watch covers this item, refetch only when it reports a
  // change (with a staleness safety net); otherwise fall back to polling.
  const refreshDetails = useCallback(() => {
    if (!refreshKey) return;
    const [cluster, group, version, kind, ns, name] = refreshKey.split('|');
    const live = currentRealtimeTopic() === `items:${cluster}:${group}:${version}:${resource?.name || ''}:`;
    if (live && !detailDirtyRef.current && Date.now() - lastRefreshRef.current < DETAIL_STALE_MS) return;
    detailDirtyRef.current = false;
    lastRefreshRef.current = Date.now();
    api.getResourceDetails(cluster, group, version, kind, ns, name)
      .then(updateDetailData)
      .catch((error) => console.error('Failed to refresh details:', error));
  }, [refreshKey, resource?.name, updateDetailData]);

  useVisibleInterval(refreshDetails, 5000, { enabled: !!refreshKey });

  // Only show tabs that are in the detail location
  const visibleDetailTabs = detailTabs.filter(
    (tab) => tab.location !== 'center',
  );

  // Content-aware minimum width calculation
  const calculateMinWidth = () => {
    const tabCount = visibleDetailTabs.length;

    // CRITICAL CONSTRAINT 1: Action buttons (permanently anchored right)
    // Calculate ACTUAL button count for current active tab
    const actionBtnWidth = 26;
    const actionBtnGap = 4;

    let actualButtonCount = 1; // pin button always present
    const activeTab = visibleDetailTabs.find(
      (tab) => tab.id === activeDetailTab,
    );
    if (activeTab?.item) {
      const isPod = activeTab.item.kind === 'Pod';
      const isNode = activeTab.item.kind === 'Node';
      const canShowLogs =
        isPod ||
        workloadControllerKinds.includes(
          (activeTab.item.kind || '').toLowerCase(),
        );
      const canShowShell = isPod || isNode;
      const canShowEdit = true;
      // Check if crossplane trace button would be visible
      const isCrossplane =
        activeTab.item.apiVersion &&
        (activeTab.item.apiVersion.includes('crossplane.io') ||
          (activeTab.item.metadata?.labels &&
            Object.keys(activeTab.item.metadata.labels).some((k) =>
              k.includes('crossplane.io'),
            )));

      if (canShowLogs) actualButtonCount++;
      if (canShowShell) actualButtonCount++;
      if (canShowEdit) actualButtonCount++;
      if (isCrossplane) actualButtonCount++;
    } else {
      // Fallback to maximum for safety
      actualButtonCount = 5;
    }

    const actionButtonsWidth =
      actualButtonCount * actionBtnWidth +
      (actualButtonCount - 1) * actionBtnGap;
    const actionButtonsPadding = 8; // padding around action buttons
    const actionAreaMinWidth = actionButtonsWidth + actionButtonsPadding;

    // CRITICAL CONSTRAINT 2: Resource header with metrics/timing (right-aligned)
    // Be MUCH more aggressive about resource name space - very long names will be truncated
    const resourceNameMinWidth = 80; // reduced - aggressive truncation for long names
    const statusBadgesWidth = 60; // space for status indicators
    const containerMetricsWidth = 100; // timing info like "2m ago", restart counts
    const safetyMargin = 20; // extra margin to prevent edge cases
    const resourceHeaderMinWidth =
      resourceNameMinWidth +
      statusBadgesWidth +
      containerMetricsWidth +
      actionAreaMinWidth +
      safetyMargin;

    // CRITICAL CONSTRAINT 3: Tab headers (fixed size)
    const minTabWidth = 80;
    const toggleBtnWidth = 26;
    const toggleBtnMargin = 8;
    const headerPadding = 16;
    const tabBorders = tabCount * 1;
    const tabsMinWidth =
      tabCount > 0 ? Math.max(tabCount * minTabWidth, 160) : 100;
    const tabAreaMinWidth =
      tabsMinWidth +
      toggleBtnWidth +
      toggleBtnMargin +
      headerPadding +
      tabBorders;

    // CRITICAL CONSTRAINT 4: Content area with container info (right-aligned data)
    const contentPadding = 32; // 16px left + 16px right
    const infoLabelMinWidth = 80; // labels like "Status:", "Created:"
    const infoValueMinWidth = 100; // minimum for readable values
    const containerStatusWidth = 60; // "Running", "Pending" badges
    const containerTimeWidth = 80; // "Started 2m ago", restart counts
    const infoRowGap = 8;
    const contentAreaMinWidth =
      infoLabelMinWidth +
      infoValueMinWidth +
      containerStatusWidth +
      containerTimeWidth +
      infoRowGap +
      contentPadding;

    // ABSOLUTE HARD LIMITS - these define the point where content becomes unusable
    const hardConstraints = {
      absolute: 200, // nothing works below this
      resourceHeader: resourceHeaderMinWidth,
      tabArea: tabAreaMinWidth,
      contentArea: contentAreaMinWidth,
      actionButtons: actionAreaMinWidth + 200, // action buttons + minimum content space
    };

    // Find the MAXIMUM constraint - this is our hard limit
    const hardLimit = Math.max(
      hardConstraints.absolute,
      hardConstraints.resourceHeader,
      hardConstraints.tabArea,
      hardConstraints.contentArea,
      hardConstraints.actionButtons,
    );

    // Apply hard limit with EXTRA safety margin for edge cases
    const finalMinWidth = Math.max(hardLimit + 10, 220); // 10px extra safety + 220px absolute minimum

    return finalMinWidth;
  };

  const dynamicMinWidth = calculateMinWidth();

  const { width, startResize, isResizing } = useResizable({
    minWidth: dynamicMinWidth,
    maxWidth: responsiveMax,
    defaultWidth: Math.max(dynamicMinWidth, Math.floor(viewportWidth * 0.2)),
  });

  // Dynamic panel width classification
  const getPanelWidthClass = (currentWidth: number) => {
    if (currentWidth <= 260) return 'detail-ultra-compact';
    if (currentWidth <= 340) return 'detail-compact';
    if (currentWidth <= 420) return 'detail-comfortable';
    if (currentWidth <= 560) return 'detail-spacious';
    return 'detail-extra-wide'; // New category for very wide panels
  };

  const panelWidthClass = getPanelWidthClass(width);

  // Auto-collapse only when the panel had tabs and the last one was closed
  const prevVisibleCountRef = useRef<number>(visibleDetailTabs.length);
  useEffect(() => {
    const prev = prevVisibleCountRef.current;
    if (
      prev > 0 &&
      visibleDetailTabs.length === 0 &&
      !isDetailsPanelCollapsed &&
      !showDropZone &&
      !isDragging
    ) {
      setTimeout(() => setDetailsPanelCollapsed(true), 0);
    }
    prevVisibleCountRef.current = visibleDetailTabs.length;
  }, [
    visibleDetailTabs.length,
    isDetailsPanelCollapsed,
    setDetailsPanelCollapsed,
    showDropZone,
    isDragging,
  ]);

  // Hide the entire detail view if there are no visible tabs
  if (
    visibleDetailTabs.length === 0 &&
    !showDropZone &&
    !isResourceDragOver
  )
    return null;

  return (
    <Tooltip.Provider delayDuration={400}>
      <>
        {showDropZone && (
          <div
            className="detail-drop-zone"
            onDragOver={(e) => {
              e.preventDefault();
              e.dataTransfer.dropEffect = 'move';
              e.currentTarget.classList.add('drag-over');
            }}
            onDragLeave={(e) => {
              e.currentTarget.classList.remove('drag-over');
            }}
            onDrop={(e) => {
              e.preventDefault();
              e.currentTarget.classList.remove('drag-over');
              const tabType = e.dataTransfer.getData('tab-type');
              const tabId = e.dataTransfer.getData('tab-id');
              const sourcePane = e.dataTransfer.getData('source-pane');

              if (tabType === 'detail' && sourcePane === 'center' && tabId) {
                moveDetailTab(tabId, 'detail');
                setActiveDetailTab(tabId);
              }
              setShowDropZone(false);
            }}
          >
            <div className="drop-zone-content">
              <span className="drop-zone-text">
                Drop here to return to detail panel
              </span>
            </div>
          </div>
        )}
        {visibleDetailTabs.length > 0 && (
          <div
            className={`detail-view ${focusArea === 'detail' ? 'focused' : ''
              } ${isDetailsPanelCollapsed ? 'collapsed' : ''} ${isResizing ? 'resizing' : ''
              } ${panelWidthClass} ${isResourceDragOver ? 'resource-drag-over' : ''}`}
            style={{ width: isDetailsPanelCollapsed ? '40px' : `${width}px` }}
            onDragOver={(e) => {
              const hasTabType = e.dataTransfer.types.includes('tab-type');
              const hasJson = e.dataTransfer.types.includes('application/json');
              if (hasTabType || hasJson) {
                e.preventDefault();
                e.dataTransfer.dropEffect = hasTabType ? 'move' : 'copy';
                if (hasJson && !hasTabType) {
                  setIsResourceDragOver(true);
                }
              }
            }}
            onDragLeave={(e) => {
              const rect = e.currentTarget.getBoundingClientRect();
              const x = e.clientX;
              const y = e.clientY;
              if (
                x < rect.left ||
                x >= rect.right ||
                y < rect.top ||
                y >= rect.bottom
              ) {
                e.currentTarget.classList.remove('drag-over');
                setIsResourceDragOver(false);
              }
            }}
            onDrop={(e) => {
              e.currentTarget.classList.remove('drag-over');
              const tabType = e.dataTransfer.getData('tab-type');
              const tabId = e.dataTransfer.getData('tab-id');
              const sourcePane = e.dataTransfer.getData('source-pane');
              if (tabType === 'detail' && sourcePane === 'center' && tabId) {
                e.preventDefault();
                moveDetailTab(tabId, 'detail');
                setActiveDetailTab(tabId);
              } else {
                handleResourceDrop(e);
              }
            }}
          >
            <div className="detail-resize-handle" onMouseDown={startResize} />
            {isDetailsPanelCollapsed && (
              <Tooltip.Root>
                <Tooltip.Trigger asChild>
                  <button
                    className="detail-toggle-btn detail-toggle-btn-collapsed"
                    onClick={toggleDetailsPanel}
                    aria-label="Expand details"
                  >
                    <PinLeftIcon />
                  </button>
                </Tooltip.Trigger>
                <Tooltip.Portal>
                  <Tooltip.Content className="tooltip-content" sideOffset={5}>
                    Expand details (⌘\\)
                    <Tooltip.Arrow className="tooltip-arrow" />
                  </Tooltip.Content>
                </Tooltip.Portal>
              </Tooltip.Root>
            )}
            {!isDetailsPanelCollapsed && visibleDetailTabs.length > 0 && (
              <div className="detail-content-wrapper">
                {visibleDetailTabs.length > 0 && (
                  // Always show tabbed interface
                  <Tabs.Root
                    value={activeDetailTab || ''}
                    onValueChange={setActiveDetailTab}
                    className="detail-tabs-root"
                  >
                    <div className="detail-tabs-header">
                      <Tabs.List className="detail-tabs-list">
                        {visibleDetailTabs.map((tab, index) => (
                          <div
                            key={tab.id}
                            className={`detail-tab-wrapper ${!tab.isPinned ? 'preview-tab' : ''
                              } ${activeDetailTab === tab.id ? 'active' : ''}`}
                            draggable
                            onDragStart={(e) => {
                              e.dataTransfer.effectAllowed = 'move';
                              e.dataTransfer.setData(
                                'tab-index',
                                index.toString(),
                              );
                              e.dataTransfer.setData('tab-id', tab.id);
                              e.dataTransfer.setData('tab-type', 'detail');
                              e.dataTransfer.setData('source-pane', 'detail');
                            }}
                            onDragEnd={(e) => {
                              // Clean up any drag-over states when drag ends
                              document
                                .querySelectorAll('.drag-over')
                                .forEach((el) => {
                                  el.classList.remove('drag-over');
                                });
                              // Also clean up the parent detail view
                              const detailView =
                                e.currentTarget.closest('.detail-view');
                              if (detailView) {
                                detailView.classList.remove('drag-over');
                              }
                            }}
                            onDragOver={(e) => {
                              e.preventDefault();
                              e.dataTransfer.dropEffect = 'move';
                            }}
                            onDrop={(e) => {
                              e.preventDefault();
                              const fromIndex = parseInt(
                                e.dataTransfer.getData('tab-index'),
                              );
                              const sourcePane =
                                e.dataTransfer.getData('source-pane');
                              if (
                                sourcePane === 'detail' &&
                                fromIndex !== index
                              ) {
                                reorderDetailTabs(fromIndex, index);
                              }
                            }}
                          >
                            <Tabs.Trigger
                              value={tab.id}
                              className={`detail-tab${tab.isDeleted ? ' detail-tab-deleted' : ''}`}
                              onContextMenu={(e) =>
                                handleTabContextMenu(e, tab)
                              }
                              onDoubleClick={() => {
                                if (!tab.isPinned) {
                                  pinDetailTab(tab.id);
                                }
                              }}
                            >
                              <span className="tab-icon">
                                {getResourceIcon(tab.item.kind || 'Unknown')}
                              </span>
                              <span className="tab-title">
                                {tab.isDeleted && <span className="tab-deleted-indicator">⚠ </span>}
                                {tab.item?.metadata?.name ||
                                  tab.item?.name ||
                                  tab.title}
                              </span>
                            </Tabs.Trigger>
                            <Tooltip.Root>
                              <Tooltip.Trigger asChild>
                                <button
                                  className="tab-close"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    closeDetailTab(tab.id);
                                  }}
                                  aria-label="Close tab"
                                >
                                  <Cross2Icon />
                                </button>
                              </Tooltip.Trigger>
                              <Tooltip.Portal>
                                <Tooltip.Content
                                  className="tooltip-content"
                                  sideOffset={5}
                                >
                                  Close (⌘W)
                                  <Tooltip.Arrow className="tooltip-arrow" />
                                </Tooltip.Content>
                              </Tooltip.Portal>
                            </Tooltip.Root>
                          </div>
                        ))}
                      </Tabs.List>
                      <Tooltip.Root>
                        <Tooltip.Trigger asChild>
                          <button
                            className="detail-toggle-btn"
                            onClick={toggleDetailsPanel}
                            aria-label={
                              isDetailsPanelCollapsed
                                ? 'Expand details'
                                : 'Collapse details'
                            }
                          >
                            {isDetailsPanelCollapsed ? (
                              <PinLeftIcon />
                            ) : (
                              <PinRightIcon />
                            )}
                          </button>
                        </Tooltip.Trigger>
                        <Tooltip.Portal>
                          <Tooltip.Content
                            className="tooltip-content"
                            sideOffset={5}
                          >
                            {isDetailsPanelCollapsed
                              ? 'Expand details (⌘\\)'
                              : 'Collapse details (⌘\\)'}
                            <Tooltip.Arrow className="tooltip-arrow" />
                          </Tooltip.Content>
                        </Tooltip.Portal>
                      </Tooltip.Root>
                    </div>

                    {visibleDetailTabs.map((tab) => (
                      <Tabs.Content
                        key={tab.id}
                        value={tab.id}
                        className="detail-tab-content"
                      >
                        <TabContent tab={tab} mode="detail" isDeleted={tab.isDeleted} />
                      </Tabs.Content>
                    ))}
                  </Tabs.Root>
                )}
              </div>
            )}
          </div>
        )}
      </>
    </Tooltip.Provider>
  );
};

export default DetailView;
