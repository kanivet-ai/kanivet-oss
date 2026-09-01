import { useRef, useEffect, useState, memo, useMemo, useCallback, useId } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import ResourceRow from './ResourceRow';
import { useResizableColumns } from '../hooks/useResizableColumns';
import { hasActions as hasResourceActions } from '../utils/resourceActions';
import { rowItemBus } from '../store/rowItemBus';

interface ResourceTableProps {
  listItems: any[];
  selectedItem: any;
  selectedResources: Set<string>;
  displayColumns: string[];
  onItemOpen: (item: any, isPinned?: boolean) => void;
  onCheckboxChange: (item: any, event?: React.MouseEvent) => void;
  onSelectAll: () => void;
  onSort: (column: string) => void;
  getColumnValue: (item: any, column: string) => any;
  isKeyboardNavigationRef: React.MutableRefObject<boolean>;
  getSortIndicator: (column: string) => React.ReactNode;
  getResourceKey: (item: any) => string;
  selectedNode: any;
  onActionClick: (e: React.MouseEvent, item: any) => void;
  restartingItems: Set<string>;
  resourceKind: string;
  isLoading: boolean;
  hasReceivedData: boolean;
  rolloutStatuses: Map<string, any>;
  handleNamespaceChange: (namespace: string) => void;
  loadError: string | null;
  onRetry: () => void;
  cluster: string | null;
  scrollPosition?: number;
  onScrollChange?: (position: number) => void;
  totalBeforeNamespaceFilter?: number;
  hasNamespaceFilter?: boolean;
  onClearNamespaceFilter?: () => void;
}

const ROW_HEIGHT = 30;
const DEFAULT_OVERSCAN = 10;
const LARGE_LIST_OVERSCAN = 6;
const VIRTUALIZATION_THRESHOLD = 150;
const VERY_LARGE_LIST_THRESHOLD = 2000;

const ResourceTable = memo(
  ({
    listItems,
    selectedItem,
    selectedResources,
    displayColumns,
    onItemOpen,
    onCheckboxChange,
    onSelectAll,
    onSort,
    getColumnValue,
    isKeyboardNavigationRef,
    getSortIndicator,
    getResourceKey,
    selectedNode,
    onActionClick,
    restartingItems,
    resourceKind,
    hasReceivedData,
    rolloutStatuses,
    handleNamespaceChange,
    loadError,
    onRetry,
    cluster,
    scrollPosition,
    onScrollChange,
    totalBeforeNamespaceFilter = 0,
    hasNamespaceFilter = false,
    onClearNamespaceFilter,
  }: ResourceTableProps) => {
    const selectedRowRef = useRef<HTMLTableRowElement | null>(null);
    const headerContainerRef = useRef<HTMLDivElement | null>(null);
    const effectiveResourceKind = resourceKind || selectedNode?.data?.kind || 'default';
    const selectedKey = selectedItem ? getResourceKey(selectedItem) : null;
    const rowRefs = useRef<Map<string, HTMLTableRowElement>>(new Map());
    const scrollContainerRef = useRef<HTMLDivElement | null>(null);
    const [showLoading, setShowLoading] = useState(false);
    const lastNodeIdRef = useRef<string | null>(null);
    const [awaitingData, setAwaitingData] = useState(false);
    const reconciledItemsRef = useRef<any[]>([]);
    const reconciledMapRef = useRef<Map<string, any>>(new Map());
    const cachedColumnsRef = useRef<string[]>(displayColumns);
    const cacheNodeIdRef = useRef<string | null>(null);
    const isUserScrollingRef = useRef(false);
    const scrollTimeoutRef = useRef<NodeJS.Timeout | null>(null);
    const lastSelectedKeyRef = useRef<string | null>(null);
    const scrollRestoredRef = useRef(false);

    const displayColumnsToUse = useMemo(() => {
      const isWaiting = (awaitingData || (!hasReceivedData && !loadError)) && !showLoading;
      if (isWaiting && cachedColumnsRef.current.length > 0) {
        return cachedColumnsRef.current;
      }
      cachedColumnsRef.current = displayColumns;
      return displayColumns;
    }, [displayColumns, awaitingData, hasReceivedData, loadError, showLoading]);

    const displayItems = useMemo(() => {
      const nodeId = selectedNode?.id ?? null;
      const isWaiting = (awaitingData || (!hasReceivedData && !loadError)) && !showLoading;
      const cacheMatchesCurrent = cacheNodeIdRef.current === nodeId;
      if (isWaiting && reconciledItemsRef.current.length > 0 && cacheMatchesCurrent) {
        return reconciledItemsRef.current;
      }
      if (listItems.length === 0) {
        reconciledItemsRef.current = [];
        reconciledMapRef.current.clear();
        cacheNodeIdRef.current = nodeId;
        return reconciledItemsRef.current;
      }
      cacheNodeIdRef.current = nodeId;
      const result: any[] = [];
      const oldMap = reconciledMapRef.current;
      const newMap = new Map<string, any>();
      const shallowEqual = (a: any, b: any) => {
        const keysA = Object.keys(a);
        const keysB = Object.keys(b);
        if (keysA.length !== keysB.length) return false;
        for (const k of keysA) {
          if (a[k] !== b[k]) return false;
        }
        return true;
      };
      for (const newItem of listItems) {
        const key = getResourceKey(newItem);
        const existing = oldMap.get(key);
        if (existing && shallowEqual(existing, newItem)) {
          result.push(existing);
        } else {
          result.push(newItem);
        }
        newMap.set(key, result[result.length - 1]);
      }
      reconciledItemsRef.current = result;
      reconciledMapRef.current = newMap;
      return result;
    }, [listItems, awaitingData, hasReceivedData, loadError, showLoading, getResourceKey, selectedNode?.id]);

    const itemKeys = useMemo(() => displayItems.map(getResourceKey), [displayItems, getResourceKey]);

    const busScope = useId();
    useEffect(() => {
      rowItemBus.publishList(busScope, displayItems, getResourceKey);
    }, [busScope, displayItems, getResourceKey]);
    useEffect(() => () => rowItemBus.clearScope(busScope), [busScope]);

    useEffect(() => {
      const nodeId = selectedNode?.id ?? null;
      if (nodeId !== lastNodeIdRef.current) {
        lastNodeIdRef.current = nodeId;
        setAwaitingData(true);
        setShowLoading(false);
      }
      if (hasReceivedData || loadError) {
        setAwaitingData(false);
      }
    }, [selectedNode?.id, hasReceivedData, loadError]);

    useEffect(() => {
      if (awaitingData) {
        const timer = setTimeout(() => setShowLoading(true), 500);
        return () => clearTimeout(timer);
      }
      setShowLoading(false);
    }, [awaitingData]);

    const [autoWidths, setAutoWidths] = useState<Record<string, number>>({});
    const measuredKeyRef = useRef<string | null>(null);
    const [wrapperWidth, setWrapperWidth] = useState(0);

    useEffect(() => {
      const wrapper = scrollContainerRef.current?.closest('.resource-table-scroll-wrapper') as HTMLElement | null;
      if (!wrapper) return;
      const update = () => setWrapperWidth(wrapper.clientWidth);
      update();
      const ro = new ResizeObserver(update);
      ro.observe(wrapper);
      return () => ro.disconnect();
    }, []);

    const { columnWidths, startResize, hasOverrides } = useResizableColumns({
      columns: displayColumnsToUse,
      resourceKind: effectiveResourceKind,
      autoWidths,
      minWidth: 40,
    });

    const getScrollElement = useCallback(() => {
      return (scrollContainerRef.current?.closest('.resource-table-scroll-wrapper') as HTMLElement) || null;
    }, []);

    const overscan = displayItems.length > VERY_LARGE_LIST_THRESHOLD ? LARGE_LIST_OVERSCAN : DEFAULT_OVERSCAN;

    const virtualizer = useVirtualizer({
      count: displayItems.length,
      getScrollElement,
      estimateSize: () => ROW_HEIGHT,
      overscan,
      getItemKey: (index) => itemKeys[index] ?? index,
    });

    const virtualItems = virtualizer.getVirtualItems();
    const totalHeight = virtualizer.getTotalSize();
    const visibleRangeStart = virtualItems[0]?.index ?? 0;
    const shouldVirtualize = displayItems.length >= VIRTUALIZATION_THRESHOLD;

    useEffect(() => {
      const container = getScrollElement();
      if (!container) return;
      const handleScroll = () => {
        isUserScrollingRef.current = true;
        if (scrollTimeoutRef.current) clearTimeout(scrollTimeoutRef.current);
        scrollTimeoutRef.current = setTimeout(() => {
          isUserScrollingRef.current = false;
        }, 150);
        if (onScrollChange) onScrollChange(container.scrollTop);
      };
      container.addEventListener('scroll', handleScroll, { passive: true });
      return () => {
        container.removeEventListener('scroll', handleScroll);
        if (scrollTimeoutRef.current) clearTimeout(scrollTimeoutRef.current);
      };
    }, [onScrollChange, getScrollElement]);

    useEffect(() => {
      if (scrollPosition !== undefined && !scrollRestoredRef.current) {
        const container = getScrollElement();
        if (container && displayItems.length > 0) {
          const maxScrollTop = Math.max(0, displayItems.length * ROW_HEIGHT - container.clientHeight);
          container.scrollTop = Math.min(scrollPosition, maxScrollTop);
          scrollRestoredRef.current = true;
        }
      }
    }, [scrollPosition, displayItems.length, getScrollElement]);

    useEffect(() => {
      scrollRestoredRef.current = false;
    }, [resourceKind]);

    useEffect(() => {
      if (!selectedItem) return;
      const currentKey = getResourceKey(selectedItem);
      const isNewSelection = currentKey !== lastSelectedKeyRef.current;
      lastSelectedKeyRef.current = currentKey;
      if (isKeyboardNavigationRef.current && !isUserScrollingRef.current && isNewSelection) {
        const selectedIndex = itemKeys.indexOf(currentKey);
        if (selectedIndex >= 0) {
          if (shouldVirtualize) {
            virtualizer.scrollToIndex(selectedIndex, { align: 'center' });
          } else {
            const container = getScrollElement();
            if (container) {
              const rowTop = selectedIndex * ROW_HEIGHT;
              const rowBottom = rowTop + ROW_HEIGHT;
              const viewportTop = container.scrollTop;
              const viewportBottom = viewportTop + container.clientHeight;
              if (rowTop < viewportTop || rowBottom > viewportBottom) {
                container.scrollTop = Math.max(0, rowTop - (container.clientHeight - ROW_HEIGHT) / 2);
              }
            }
          }
        }
      }
      isKeyboardNavigationRef.current = false;
    }, [selectedItem, itemKeys, getResourceKey, isKeyboardNavigationRef, virtualizer, shouldVirtualize, getScrollElement]);

    const initialMeasureDoneRef = useRef<string>('');
    useEffect(() => {
      const key = `${effectiveResourceKind}::${displayColumnsToUse.join('|')}`;
      if (displayItems.length === 0) return;
      // Only re-measure when the column key or wrapper width changes;
      // not on every item-content update. Item additions/changes occasionally
      // need a wider column, but we trade a tiny visual sub-optimality for
      // a huge CPU win on busy clusters.
      const measureSignature = `${key}::${wrapperWidth}`;
      if (initialMeasureDoneRef.current === measureSignature) return;
      initialMeasureDoneRef.current = measureSignature;

      const raf = requestAnimationFrame(() => {
        const wrapper = scrollContainerRef.current?.closest('.resource-table-scroll-wrapper');
        const tbody = wrapper?.querySelector('.resource-table-body tbody');
        const headerRow = headerContainerRef.current?.querySelector('thead tr');
        if (!tbody || !headerRow) return;
        const rows = tbody.querySelectorAll('tr.resource-row');
        if (rows.length === 0) return;

        const measureContent = (el: Element | null): number => {
          if (!el) return 0;
          const range = document.createRange();
          range.selectNodeContents(el);
          const rect = range.getBoundingClientRect();
          range.detach?.();
          return rect.width;
        };

        const widths: Record<string, number> = {};
        const ths = headerRow.querySelectorAll('th.resizable-column');
        ths.forEach((th, idx) => {
          const col = displayColumnsToUse[idx];
          if (!col) return;
          const inner = th.querySelector('.column-header');
          widths[col] = measureContent(inner) + 24;
        });

        rows.forEach((row) => {
          const tds = row.querySelectorAll('td');
          for (let i = 1; i < tds.length; i++) {
            const col = displayColumnsToUse[i - 1];
            if (!col) continue;
            const td = tds[i] as HTMLElement;
            const inner = td.querySelector('.cell-content');
            const w = measureContent(inner);
            const padded = w + 24;
            if (padded > (widths[col] || 0)) widths[col] = padded;
          }
        });

        const maxColWidth = wrapperWidth > 0
          ? Math.max(160, Math.min(400, Math.round(wrapperWidth * 0.3)))
          : 240;
        const finalized: Record<string, number> = {};
        for (const col of displayColumnsToUse) {
          const raw = widths[col] ?? 60;
          finalized[col] = Math.min(maxColWidth, Math.max(40, Math.ceil(raw)));
        }
        setAutoWidths((prev) => {
          if (measuredKeyRef.current !== key) {
            measuredKeyRef.current = key;
            return finalized;
          }
          const merged = { ...prev };
          let changed = false;
          for (const col of displayColumnsToUse) {
            if (finalized[col] > (merged[col] || 0)) {
              merged[col] = finalized[col];
              changed = true;
            }
          }
          return changed ? merged : prev;
        });
      });
      return () => cancelAnimationFrame(raf);
    }, [displayItems, displayColumnsToUse, effectiveResourceKind, wrapperWidth]);

    useEffect(() => {
      const key = `${effectiveResourceKind}::${displayColumnsToUse.join('|')}`;
      if (measuredKeyRef.current && measuredKeyRef.current !== key) {
        setAutoWidths({});
        measuredKeyRef.current = null;
      }
    }, [effectiveResourceKind, displayColumnsToUse]);

    const tableMinWidth = 26 + displayColumnsToUse.reduce((sum: number, col: string) => sum + (columnWidths[col] || 100), 0);
    const fillerWidth = hasOverrides ? Math.max(0, wrapperWidth - tableMinWidth) : 0;
    const tableWidth: number | string = hasOverrides ? tableMinWidth + fillerWidth : '100%';

    const stableHasActions = useCallback(
      (item: any) => hasResourceActions(item, selectedNode),
      [selectedNode],
    );

    const stableOnRowRef = useCallback(
      (uid: string | null, el: HTMLTableRowElement | null) => {
        if (uid && el) {
          rowRefs.current.set(uid, el);
          if (selectedKey && uid === selectedKey) selectedRowRef.current = el;
        } else if (uid) {
          rowRefs.current.delete(uid);
        }
      },
      [selectedKey],
    );

    return (
      <div className="resource-table-scroll-wrapper">
        <div ref={headerContainerRef} className="resource-table-header-container">
          <table className="resource-table resource-table-header" style={{ width: tableWidth, minWidth: tableMinWidth }}>
            <colgroup>
              <col style={{ width: '26px' }} />
              {displayColumnsToUse.map((column: string) => (
                <col key={column} style={{ width: `${columnWidths[column]}px` }} />
              ))}
              {fillerWidth > 0 && <col key="__filler" style={{ width: `${fillerWidth}px` }} />}
            </colgroup>
            <thead>
              <tr>
                <th className="checkbox-column">
                  <input
                    type="checkbox"
                    checked={selectedResources.size === displayItems.length && displayItems.length > 0}
                    onChange={onSelectAll}
                  />
                </th>
                {displayColumnsToUse.map((column: string, index: number) => (
                  <th key={column} className="resizable-column" style={{ width: `${columnWidths[column]}px`, position: 'relative' }}>
                    <div className="column-header" onClick={() => onSort(column)} style={{ cursor: 'pointer', userSelect: 'none' }}>
                      {column}
                      {getSortIndicator(column)}
                    </div>
                    {index < displayColumnsToUse.length - 1 && (
                      <div className="column-resize-handle" onMouseDown={(e) => startResize(column, e, displayColumnsToUse[index + 1])} />
                    )}
                  </th>
                ))}
                {fillerWidth > 0 && <th key="__filler" className="resource-table-filler-column" />}
              </tr>
            </thead>
          </table>
        </div>

        <div
          ref={scrollContainerRef}
          className={`resource-table-body-container ${shouldVirtualize ? 'resource-table-body-container-virtualized' : ''}`}
          style={{
            position: 'relative',
            width: tableWidth,
            minWidth: tableMinWidth,
            height: displayItems.length === 0 ? ROW_HEIGHT : shouldVirtualize ? totalHeight : undefined,
          }}
        >
          {displayItems.length === 0 ? (
            <table className="resource-table resource-table-body" style={{ width: tableWidth, minWidth: tableMinWidth }}>
              <colgroup>
                <col style={{ width: '26px' }} />
                {displayColumnsToUse.map((column: string) => (
                  <col key={column} style={{ width: `${columnWidths[column]}px` }} />
                ))}
                {fillerWidth > 0 && <col key="__filler" style={{ width: `${fillerWidth}px` }} />}
              </colgroup>
              <tbody>
                <tr>
                  <td colSpan={displayColumnsToUse.length + 1} className="empty-message">
                    {selectedNode
                      ? (() => {
                        if (showLoading) return 'Loading resources...';
                        if (awaitingData || (!hasReceivedData && !loadError)) return null;
                        if (loadError) {
                          return (
                            <div className="error-state">
                              <span className="error-icon">⚠️</span>
                              <span className="error-message">{loadError}</span>
                              <button className="retry-button" onClick={onRetry}>Retry</button>
                            </div>
                          );
                        }
                        if (hasNamespaceFilter && totalBeforeNamespaceFilter > 0) {
                          return (
                            <div className="empty-state-with-action">
                              <span>No resources in selected namespace</span>
                              <button className="try-all-namespaces-button" onClick={onClearNamespaceFilter}>
                                Show all namespaces
                              </button>
                            </div>
                          );
                        }
                        return 'No resources found';
                      })()
                      : 'Select a resource from the tree to view items'}
                  </td>
                </tr>
              </tbody>
            </table>
          ) : shouldVirtualize ? (
            <table
              className="resource-table resource-table-body"
              style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: tableWidth,
                minWidth: tableMinWidth,
                zIndex: 1,
                transform: `translate3d(0, ${virtualItems[0]?.start ?? 0}px, 0)`,
              }}
            >
              <colgroup>
                <col style={{ width: '26px' }} />
                {displayColumnsToUse.map((column: string) => (
                  <col key={column} style={{ width: `${columnWidths[column]}px` }} />
                ))}
                {fillerWidth > 0 && <col key="__filler" style={{ width: `${fillerWidth}px` }} />}
              </colgroup>
              <tbody>
                {virtualItems.map((virtualRow) => {
                  const item = displayItems[virtualRow.index];
                  if (!item) return null;
                  const key = itemKeys[virtualRow.index];
                  const isSelected = selectedKey === key;
                  const isChecked = selectedResources.has(key);
                  const isRestarting = restartingItems.has(key);
                  return (
                    <ResourceRow
                      busScope={busScope}
                      key={key}
                      itemKey={key}
                      fallbackItem={item}
                      index={virtualRow.index - visibleRangeStart}
                      isSelected={isSelected}
                      isChecked={isChecked}
                      isRestarting={isRestarting}
                      visibleRangeStart={visibleRangeStart}
                      onCheckboxChange={onCheckboxChange}
                      onItemOpen={onItemOpen}
                      onActionClick={onActionClick}
                      hasActions={stableHasActions}
                      displayColumns={displayColumnsToUse}
                      getColumnValue={getColumnValue}
                      selectedNode={selectedNode}
                      rolloutStatuses={rolloutStatuses}
                      handleNamespaceChange={handleNamespaceChange}
                      onRowRef={stableOnRowRef}
                      cluster={cluster || undefined}
                      hasFiller={fillerWidth > 0}
                    />
                  );
                })}
              </tbody>
            </table>
          ) : (
            <table className="resource-table resource-table-body" style={{ width: tableWidth, minWidth: tableMinWidth }}>
              <colgroup>
                <col style={{ width: '26px' }} />
                {displayColumnsToUse.map((column: string) => (
                  <col key={column} style={{ width: `${columnWidths[column]}px` }} />
                ))}
                {fillerWidth > 0 && <col key="__filler" style={{ width: `${fillerWidth}px` }} />}
              </colgroup>
              <tbody>
                {displayItems.map((item, index) => {
                  const key = itemKeys[index];
                  const isSelected = selectedKey === key;
                  const isChecked = selectedResources.has(key);
                  const isRestarting = restartingItems.has(key);
                  return (
                    <ResourceRow
                      busScope={busScope}
                      key={key}
                      itemKey={key}
                      fallbackItem={item}
                      index={index}
                      isSelected={isSelected}
                      isChecked={isChecked}
                      isRestarting={isRestarting}
                      visibleRangeStart={0}
                      onCheckboxChange={onCheckboxChange}
                      onItemOpen={onItemOpen}
                      onActionClick={onActionClick}
                      hasActions={stableHasActions}
                      displayColumns={displayColumnsToUse}
                      getColumnValue={getColumnValue}
                      selectedNode={selectedNode}
                      rolloutStatuses={rolloutStatuses}
                      handleNamespaceChange={handleNamespaceChange}
                      onRowRef={stableOnRowRef}
                      cluster={cluster || undefined}
                      hasFiller={fillerWidth > 0}
                    />
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>
    );
  },
  (prevProps, nextProps) => {
    if (prevProps.listItems !== nextProps.listItems) return false;
    if (prevProps.listItems.length !== nextProps.listItems.length) return false;
    if (prevProps.selectedItem !== nextProps.selectedItem) return false;
    if (prevProps.selectedResources !== nextProps.selectedResources) return false;
    if (prevProps.selectedResources.size !== nextProps.selectedResources.size) return false;
    if (prevProps.displayColumns.length !== nextProps.displayColumns.length) return false;
    if (prevProps.displayColumns.join(',') !== nextProps.displayColumns.join(',')) return false;
    if (prevProps.selectedNode?.id !== nextProps.selectedNode?.id) return false;
    if (prevProps.restartingItems !== nextProps.restartingItems) return false;
    if (prevProps.restartingItems.size !== nextProps.restartingItems.size) return false;
    if (prevProps.resourceKind !== nextProps.resourceKind) return false;
    if (prevProps.isLoading !== nextProps.isLoading) return false;
    if (prevProps.hasReceivedData !== nextProps.hasReceivedData) return false;
    if (prevProps.rolloutStatuses !== nextProps.rolloutStatuses) return false;
    if (prevProps.loadError !== nextProps.loadError) return false;
    if (prevProps.cluster !== nextProps.cluster) return false;
    return true;
  },
);

export default ResourceTable;
