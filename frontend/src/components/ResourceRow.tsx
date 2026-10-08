import { memo, useCallback, useEffect, useRef, useState } from 'react';
import clsx from 'clsx';
import ResourceCell from './ResourceCell';

interface ResourceRowProps {
  item: any;
  isSelected: boolean;
  isChecked: boolean;
  isRestarting: boolean;
  /** Position in the whole list, not in the rendered window. */
  rowIndex: number;
  onCheckboxChange: (item: any, event: any) => void;
  onItemOpen: (item: any, isDoubleClick: boolean) => void;
  onActionClick: (event: any, item: any) => void;
  hasActions: (item: any) => boolean;
  displayColumns: string[];
  getColumnValue: (
    item: any,
    column: string,
    selectedNode?: any,
    rolloutStatuses?: Map<string, any>,
    handleNamespaceChange?: (namespace: string) => void,
  ) => any;
  selectedNode?: any;
  rolloutStatuses?: Map<string, any>;
  handleNamespaceChange?: (namespace: string) => void;
  onRowRef?: (uid: string | null, el: HTMLTableRowElement | null) => void;
  cluster?: string;
  hasFiller?: boolean;
}

const ResourceRow = memo(
  ({
    item,
    isSelected,
    isChecked,
    isRestarting,
    rowIndex,
    onCheckboxChange,
    onItemOpen,
    onActionClick,
    hasActions,
    displayColumns,
    getColumnValue,
    selectedNode,
    rolloutStatuses,
    handleNamespaceChange,
    onRowRef,
    cluster,
    hasFiller,
  }: ResourceRowProps) => {
    const [isUpdated, setIsUpdated] = useState(false);
    const prevRvRef = useRef<string | null>(null);
    const timerRef = useRef<NodeJS.Timeout | null>(null);
    const [draggable, setDraggable] = useState(false);
    const dragArmTimerRef = useRef<NodeJS.Timeout | null>(null);

    useEffect(() => {
      const currentRv = item?.resourceVersion ?? null;
      const prevRv = prevRvRef.current;
      if (prevRv && currentRv && prevRv !== currentRv) {
        setIsUpdated(true);
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => {
          setIsUpdated(false);
          timerRef.current = null;
        }, 2000);
      }
      prevRvRef.current = currentRv;
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }, [item]);

    const isPendingDeletion = !!item?.deletionTimestamp;
    const kindLower = (item?.kind || selectedNode?.data?.kind || '')?.toLowerCase();
    const isNode = kindLower === 'node' || kindLower === 'nodes';
    const isCordoned = isNode && item?.unschedulable === true;
    const isTainted = isNode && Array.isArray(item?.taints) && item.taints.length > 0;

    const handleDragStart = useCallback(
      (e: React.DragEvent) => {
        const apiVersion = item?.apiVersion || '';
        const [group, version] = apiVersion.includes('/') ? apiVersion.split('/') : ['', apiVersion || 'v1'];
        const dragData = {
          type: 'k8s-resource',
          kind: item?.kind || selectedNode?.data?.kind || 'Resource',
          name: item?.name,
          namespace: item?.namespace || '',
          cluster: cluster || '',
          group: group || selectedNode?.data?.group || '',
          version: version || selectedNode?.data?.version || 'v1',
        };
        e.dataTransfer.setData('application/json', JSON.stringify(dragData));
        e.dataTransfer.effectAllowed = 'copy';
      },
      [item, selectedNode, cluster],
    );

    const handlePointerDown = useCallback(() => {
      if (dragArmTimerRef.current) clearTimeout(dragArmTimerRef.current);
      dragArmTimerRef.current = setTimeout(() => setDraggable(true), 120);
    }, []);

    const handlePointerUp = useCallback(() => {
      if (dragArmTimerRef.current) {
        clearTimeout(dragArmTimerRef.current);
        dragArmTimerRef.current = null;
      }
      if (draggable) setDraggable(false);
    }, [draggable]);

    useEffect(() => {
      return () => {
        if (dragArmTimerRef.current) clearTimeout(dragArmTimerRef.current);
      };
    }, []);

    const handleCellClick = useCallback(() => {
      onItemOpen(item, false);
    }, [onItemOpen, item]);

    const handleCellDoubleClick = useCallback(() => {
      onItemOpen(item, true);
    }, [onItemOpen, item]);

    const handleContextMenu = useCallback(
      (e: React.MouseEvent) => {
        e.preventDefault();
        if (hasActions(item)) onActionClick(e, item);
      },
      [hasActions, onActionClick, item],
    );

    const setRowRef = useCallback(
      (el: HTMLTableRowElement | null) => {
        if (onRowRef) onRowRef(item?.uid || null, el);
      },
      [onRowRef, item?.uid],
    );

    if (!item) return null;

    return (
      <tr
        ref={setRowRef}
        draggable={draggable}
        onDragStart={draggable ? handleDragStart : undefined}
        onMouseDown={handlePointerDown}
        onMouseUp={handlePointerUp}
        onMouseLeave={handlePointerUp}
        data-uid={item.uid}
        data-index={rowIndex}
        className={clsx('resource-row', {
          'resource-row-selected': isSelected,
          'resource-row-checked': isChecked,
          'resource-row-restarting': isRestarting,
          'resource-row-pending-deletion': isPendingDeletion,
          'resource-row-cordoned': isCordoned,
          'resource-row-tainted': isTainted,
          'resource-row-updated': isUpdated,
          'resource-row-event-warning': item.type === 'Warning',
        })}
        onContextMenu={handleContextMenu}
      >
        <td className="checkbox-column" onClick={(e) => e.stopPropagation()}>
          <input
            type="checkbox"
            checked={isChecked}
            onClick={(e) => onCheckboxChange(item, e)}
            onChange={() => {}}
          />
        </td>
        {displayColumns.map((column: string) => (
          <ResourceCell
            key={column}
            item={item}
            column={column}
            selectedNode={selectedNode}
            rolloutStatuses={rolloutStatuses}
            handleNamespaceChange={handleNamespaceChange}
            cluster={cluster}
            getColumnValue={getColumnValue}
            onClick={handleCellClick}
            onDoubleClick={handleCellDoubleClick}
          />
        ))}
        {hasFiller && <td className="resource-table-filler-cell" />}
      </tr>
    );
  },
);

ResourceRow.displayName = 'ResourceRow';

export default ResourceRow;
