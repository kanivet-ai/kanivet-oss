import { useState, useRef, useCallback, useEffect, useMemo } from 'react';
import ResourceList from './ResourceList';
import { useStore } from '../store';
import './CenterPaneSplitPane.css';

export interface SplitNode {
  id: string;
  type: 'resourceList' | 'split';
  direction?: 'horizontal' | 'vertical';
  children?: SplitNode[];
  size?: number;
  tabId?: string;
}

type DropZone = 'top' | 'bottom' | 'left' | 'right' | 'center' | null;

interface CenterPaneSplitPaneProps {
  node: SplitNode;
  onSplit: (nodeId: string, direction: 'horizontal' | 'vertical') => void;
  onClose: (nodeId: string) => void;
  onFocus: (nodeId: string) => void;
  onSplitAndMoveTab?: (
    nodeId: string,
    direction: 'horizontal' | 'vertical',
    position: 'before' | 'after',
    tabId: string,
    tabType: string,
    sourcePaneId?: string
  ) => void;
  onSplitAndOpenResource?: (
    nodeId: string,
    direction: 'horizontal' | 'vertical',
    position: 'before' | 'after',
    resourceData: any
  ) => void;
  focusedNodeId?: string;
  hasMultiplePanes?: boolean;
}

const CenterPaneSplitPane = ({
  node,
  onSplit,
  onClose,
  onFocus,
  onSplitAndMoveTab,
  onSplitAndOpenResource,
  focusedNodeId,
  hasMultiplePanes,
}: CenterPaneSplitPaneProps) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const leafRef = useRef<HTMLDivElement>(null);
  const [isResizing, setIsResizing] = useState(false);
  const [sizes, setSizes] = useState<number[]>(
    node.children?.map((child) => child.size || 50) || [],
  );
  const [isDragOver, setIsDragOver] = useState(false);
  const [activeZone, setActiveZone] = useState<DropZone>(null);
  const [isSamePaneDrag, setIsSamePaneDrag] = useState(false);
  const dragCounterRef = useRef(0);
  const activeZoneRef = useRef<DropZone>(null);

  useEffect(() => {
    const cleanup = () => {
      dragCounterRef.current = 0;
      setIsDragOver(false);
      setActiveZone(null);
      setIsSamePaneDrag(false);
    };
    document.addEventListener('drop', cleanup, true);
    document.addEventListener('dragend', cleanup, true);
    return () => {
      document.removeEventListener('drop', cleanup, true);
      document.removeEventListener('dragend', cleanup, true);
    };
  }, []);

  const getDropZone = useCallback((e: React.DragEvent): DropZone => {
    if (!leafRef.current) return 'center';
    const rect = leafRef.current.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const w = rect.width;
    const h = rect.height;
    const edgeSize = 0.2;
    const tabBarHeight = 40;
    if (y < tabBarHeight) return 'center';
    if (y < h * edgeSize) return 'top';
    if (y > h * (1 - edgeSize)) return 'bottom';
    if (x < w * edgeSize) return 'left';
    if (x > w * (1 - edgeSize)) return 'right';
    return 'center';
  }, []);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    const isTabDrag = e.dataTransfer.types.includes('tab-type');
    const isResourceDrag = e.dataTransfer.types.includes('resource-node');
    if (!isTabDrag && !isResourceDrag) return;
    const samePaneDrag = isTabDrag && e.dataTransfer.types.includes(`source-pane-id:${node.id}`);
    e.preventDefault();
    dragCounterRef.current++;
    setIsDragOver(true);
    setIsSamePaneDrag(samePaneDrag);
  }, [node.id]);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    const isTabDrag = e.dataTransfer.types.includes('tab-type');
    const isResourceDrag = e.dataTransfer.types.includes('resource-node');
    if (!isTabDrag && !isResourceDrag) return;
    e.preventDefault();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) {
      setIsDragOver(false);
      setActiveZone(null);
      setIsSamePaneDrag(false);
    }
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    const isTabDrag = e.dataTransfer.types.includes('tab-type');
    const isResourceDrag = e.dataTransfer.types.includes('resource-node');
    if (!isTabDrag && !isResourceDrag) return;
    const samePaneDrag = isTabDrag && e.dataTransfer.types.includes(`source-pane-id:${node.id}`);
    e.preventDefault();
    e.dataTransfer.dropEffect = isResourceDrag ? 'copy' : 'move';
    let zone = getDropZone(e);
    if (samePaneDrag && (zone === 'top' || zone === 'center')) {
      zone = null;
    }
    activeZoneRef.current = zone;
    setActiveZone(zone);
  }, [getDropZone, node.id]);

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    const zone = activeZoneRef.current;
    dragCounterRef.current = 0;
    activeZoneRef.current = null;
    setIsDragOver(false);
    setActiveZone(null);
    setIsSamePaneDrag(false);

    const directionMap: Record<string, 'horizontal' | 'vertical'> = {
      top: 'horizontal', bottom: 'horizontal', left: 'vertical', right: 'vertical'
    };
    const positionMap: Record<string, 'before' | 'after'> = {
      top: 'before', bottom: 'after', left: 'before', right: 'after'
    };

    const dragType = e.dataTransfer.getData('drag-type');
    if (dragType === 'resource') {
      e.stopPropagation();
      const resourceNodeData = e.dataTransfer.getData('resource-node');
      if (resourceNodeData) {
        try {
          const nodeData = JSON.parse(resourceNodeData);
          const { selectNode, loadListItems, openResourceListTab, currentTab, recordNavigation, setFocusArea } = useStore.getState();

          if (zone && zone !== 'center' && onSplitAndOpenResource) {
            onSplitAndOpenResource(node.id, directionMap[zone], positionMap[zone], nodeData);
          } else if (currentTab) {
            selectNode(nodeData);
            await loadListItems(currentTab, nodeData.data);
            recordNavigation('resource', nodeData.id, nodeData.data).catch(() => {});
            await openResourceListTab(nodeData.data, currentTab, true, node.id);
            setFocusArea('list');
          }
        } catch (err) {
          console.error('Failed to handle dropped resource:', err);
        }
      }
      return;
    }

    const tabId = e.dataTransfer.getData('tab-id');
    const tabType = e.dataTransfer.getData('tab-type');
    const sourcePaneId = e.dataTransfer.getData('source-pane-id');
    if (!tabId || !tabType) return;
    if (zone === 'center' || !zone || !onSplitAndMoveTab) return;
    e.stopPropagation();
    onSplitAndMoveTab(node.id, directionMap[zone], positionMap[zone], tabId, tabType, sourcePaneId);
  }, [node.id, onSplitAndMoveTab, onSplitAndOpenResource]);

  const handleMouseDown = useCallback(
    (index: number) => (e: React.MouseEvent) => {
      e.preventDefault();
      setIsResizing(true);

      const startPos = node.direction === 'vertical' ? e.clientX : e.clientY;
      const startSizes = [...sizes];

      const handleMouseMove = (e: MouseEvent) => {
        if (!containerRef.current) return;

        const currentPos =
          node.direction === 'vertical' ? e.clientX : e.clientY;
        const containerSize =
          node.direction === 'vertical'
            ? containerRef.current.offsetWidth
            : containerRef.current.offsetHeight;

        const delta = ((currentPos - startPos) / containerSize) * 100;

        const newSizes = [...startSizes];
        newSizes[index] = Math.max(20, Math.min(80, startSizes[index] + delta));
        newSizes[index + 1] = Math.max(
          20,
          Math.min(80, startSizes[index + 1] - delta),
        );

        setSizes(newSizes);
      };

      const handleMouseUp = () => {
        setIsResizing(false);
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
      };

      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [node.direction, sizes],
  );

  const childrenSizes = useMemo(
    () => (node.children || []).map((child) => child.size || 50),
    [node.children],
  );
  useEffect(() => {
    setSizes(childrenSizes);
  }, [childrenSizes]);

  if (node.type === 'resourceList') {
    const isFocused = hasMultiplePanes && focusedNodeId === node.id;
    return (
      <div
        ref={leafRef}
        className={`center-pane-split-leaf ${isFocused ? 'focused' : ''}`}
        onClick={(e) => {
          e.stopPropagation();
          onFocus(node.id);
        }}
        onDragEnter={handleDragEnter}
        onDragLeave={handleDragLeave}
        onDragOver={handleDragOver}
        onDrop={handleDrop}
      >
        <ResourceList
          paneId={node.id}
          isFocusedPane={isFocused}
          onRequestPaneClose={() => onClose(node.id)}
        />
        {isDragOver && (
          <div className="pane-drop-overlay">
            {!isSamePaneDrag && <div className={`pane-drop-zone top ${activeZone === 'top' ? 'active' : ''}`} />}
            <div className={`pane-drop-zone bottom ${activeZone === 'bottom' ? 'active' : ''}`} />
            <div className={`pane-drop-zone left ${activeZone === 'left' ? 'active' : ''}`} />
            <div className={`pane-drop-zone right ${activeZone === 'right' ? 'active' : ''}`} />
            {!isSamePaneDrag && <div className={`pane-drop-zone center ${activeZone === 'center' ? 'active' : ''}`} />}
          </div>
        )}
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className={`center-pane-split-container ${node.direction}`}
      style={{
        cursor: isResizing
          ? node.direction === 'vertical'
            ? 'col-resize'
            : 'row-resize'
          : undefined,
      }}
    >
      {node.children?.map((child, index) => (
        <div
          key={child.id}
          style={{
            flexBasis: `${sizes[index] || 50}%`,
            flexGrow: 0,
            flexShrink: 0,
            position: 'relative',
            overflow: 'hidden',
          }}
        >
          <CenterPaneSplitPane
            node={child}
            onSplit={onSplit}
            onClose={onClose}
            onFocus={onFocus}
            onSplitAndMoveTab={onSplitAndMoveTab}
            onSplitAndOpenResource={onSplitAndOpenResource}
            focusedNodeId={focusedNodeId}
            hasMultiplePanes={hasMultiplePanes}
          />
          {index < node.children!.length - 1 && (
            <div
              className={`center-pane-split-divider ${node.direction}`}
              onMouseDown={handleMouseDown(index)}
            />
          )}
        </div>
      ))}
    </div>
  );
};

export default CenterPaneSplitPane;
