import { useEffect, useRef, useMemo, useState, memo } from 'react';
import clsx from 'clsx';
import { DashboardIcon } from '@radix-ui/react-icons';
import { getResourceIcon, getCategoryIcon } from '../utils/resourceIcons';
import { useStore } from '../store';
import ExpandIcon from './icons/ExpandIcon';
import './TreeNode.css';

interface TreeNodeProps {
  node: any;
  level: number;
  searchQuery: string;
  focusedNodeId?: string | null;
  onNodeClick: (node: any, isPinned?: boolean) => void;
  isLast?: boolean;
  parentPath?: boolean[];
  ancestorLabels?: string[];
  siblingsHaveChevron?: boolean;
}

const nodeHasChevron = (n: any): boolean =>
  !n.disabled &&
  n.type !== 'resource' &&
  n.type !== 'overview' &&
  n.type !== 'argo-overview' &&
  n.type !== 'helm' &&
  n.type !== 'vcluster' &&
  n.type !== 'finops' &&
  n.type !== 'incident-timeline';

const nodeMatchesSearch = (
  node: any,
  searchQuery: string,
  ancestorLabels: string[] = [],
): boolean => {
  if (!searchQuery) return true;
  const query = searchQuery.toLowerCase();

  const fullPath = [...ancestorLabels, node.label].join(' ').toLowerCase();
  if (fullPath.includes(query)) return true;

  if (node.children) {
    return node.children.some((child: any) =>
      nodeMatchesSearch(child, searchQuery, [...ancestorLabels, node.label]),
    );
  }
  return false;
};

const TreeNode = ({
  node,
  level,
  searchQuery,
  focusedNodeId,
  onNodeClick,
  isLast = false,
  parentPath = [],
  ancestorLabels = [],
  siblingsHaveChevron = true,
}: TreeNodeProps) => {
  const currentTab = useStore((state) => state.currentTab);
  const selectedNode = useStore(
    (state) => state.getCurrentTabState()?.selectedNode,
  );
  const nodeRef = useRef<HTMLDivElement>(null);
  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
  } | null>(null);
  const contextMenuRef = useRef<HTMLDivElement>(null);
  const [clickTimer, setClickTimer] = useState<NodeJS.Timeout | null>(null);
  const expanded = node.expanded || false;
  const disabled = !!node.disabled;
  const fullPath = [...ancestorLabels, node.label].join(' ').toLowerCase();
  const isMatch = searchQuery && fullPath.includes(searchQuery.toLowerCase());
  const isFocused = focusedNodeId === node.id;
  const isSelected = useMemo(() => {
    if (!selectedNode) return false;
    if (selectedNode.id && selectedNode.id === node.id) return true;
    if (node.type === 'resource' && selectedNode.type === 'resource' && node.data && selectedNode.data) {
      return node.data.name === selectedNode.data.name &&
        node.data.group === selectedNode.data.group &&
        node.data.version === selectedNode.data.version;
    }
    return false;
  }, [selectedNode, node]);

  const shouldShowNode = useMemo(() => {
    return nodeMatchesSearch(node, searchQuery, ancestorLabels);
  }, [node, searchQuery, ancestorLabels]);

  const filteredChildren = useMemo(() => {
    if (!node.children || !expanded) return [];
    return node.children.filter((child: any) =>
      nodeMatchesSearch(child, searchQuery, [...ancestorLabels, node.label])
    );
  }, [node.children, expanded, searchQuery, ancestorLabels, node.label]);

  const childrenHaveChevron = useMemo(
    () => filteredChildren.some((c: any) => nodeHasChevron(c)),
    [filteredChildren],
  );

  const childMaxCountDigits = useMemo(() => {
    let m = 0;
    for (const c of filteredChildren) {
      if ((c.type !== 'resource' && c.type !== 'apiVersion') || c.hideCount) continue;
      const len = c.count === undefined || c.count === null ? 1 : String(c.count).length;
      if (len > m) m = len;
    }
    return m;
  }, [filteredChildren]);

  useEffect(() => {
    if (isFocused && nodeRef.current) {
      requestAnimationFrame(() => {
        nodeRef.current?.scrollIntoView({
          behavior: 'instant',
          block: 'nearest',
        });
      });
    }
  }, [isFocused]);

  useEffect(() => {
    return () => {
      if (clickTimer) {
        clearTimeout(clickTimer);
      }
    };
  }, [clickTimer]);

  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (disabled) return;

    if (node.type === 'resource' || node.type === 'overview' || node.type === 'argo-overview' || node.type === 'helm') {
      // Single vs double click: double click pins the tab
      if (clickTimer) {
        clearTimeout(clickTimer);
        setClickTimer(null);
        onNodeClick(node, true);
      } else {
        const timer = setTimeout(() => {
          setClickTimer(null);
          onNodeClick(node, false);
        }, 200);
        setClickTimer(timer);
      }
    } else {
      // For non-resource nodes, just expand/collapse
      onNodeClick(node);
    }
  };

  const handleDoubleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    // Double click is now handled in handleClick
  };

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (node.type === 'resource') {
      setContextMenu({ x: e.clientX, y: e.clientY });
    }
  };

  const handleSeeDetail = async () => {
    if (node.type === 'resource' && currentTab) {
      // Emit custom event to TreeSidebar/Store to open CRD detail
      const event = new CustomEvent('tree:see-detail', {
        detail: { node },
      });
      window.dispatchEvent(event);
    }
    setContextMenu(null);
  };

  const handleOpenToSide = async () => {
    if (node.type === 'resource' && currentTab) {
      const event = new CustomEvent('centerPane:split', {
        detail: {
          paneId: 'root',
          direction: 'vertical',
        },
      });
      window.dispatchEvent(event);
      setTimeout(() => {
        onNodeClick(node, true);
      }, 100);
    }
    setContextMenu(null);
  };

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        contextMenuRef.current &&
        !contextMenuRef.current.contains(e.target as Node)
      ) {
        setContextMenu(null);
      }
    };

    if (contextMenu) {
      document.addEventListener('mousedown', handleClickOutside);
      return () =>
        document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [contextMenu]);

  if (!shouldShowNode) {
    return null;
  }

  return (
    <div className="tree-node-container">
      <div
        ref={nodeRef}
        className={clsx('tree-node', `tree-node-level-${level}`, {
          'tree-node-match': isMatch,
          'tree-node-expanded': expanded,
          'tree-node-focused': isFocused,
          'tree-node-selected': isSelected,
          'tree-node-has-children': node.children && node.children.length > 0,
          'tree-node-disabled': disabled,
        })}
        onClick={handleClick}
        onDoubleClick={handleDoubleClick}
        onContextMenu={handleContextMenu}
        tabIndex={-1}
        aria-disabled={disabled}
        draggable={node.type === 'resource' && !disabled}
        onDragStart={(e) => {
          if (node.type === 'resource') {
            e.dataTransfer.effectAllowed = 'copy';
            e.dataTransfer.setData('resource-node', JSON.stringify(node));
            e.dataTransfer.setData('drag-type', 'resource');
            
            const dragPreview = document.createElement('div');
            dragPreview.style.cssText = `
              display: flex;
              align-items: center;
              gap: 8px;
              padding: 6px 12px;
              background: var(--bg-secondary, #1e1e1e);
              border: 1px solid var(--border-color, #333);
              border-radius: 4px;
              color: var(--text-primary, #fff);
              font-size: 13px;
              position: absolute;
              top: -1000px;
              left: -1000px;
              pointer-events: none;
              box-shadow: 0 2px 8px rgba(0, 0, 0, 0.3);
            `;
            
            const iconElement = e.currentTarget.querySelector('.tree-node-icon');
            if (iconElement) {
              const iconClone = iconElement.cloneNode(true) as HTMLElement;
              iconClone.style.cssText = 'display: flex; align-items: center; width: 15px; height: 15px;';
              dragPreview.appendChild(iconClone);
            }
            
            const textElement = document.createElement('span');
            textElement.textContent = node.label;
            textElement.style.cssText = 'white-space: nowrap;';
            dragPreview.appendChild(textElement);
            
            document.body.appendChild(dragPreview);
            e.dataTransfer.setDragImage(dragPreview, 0, 0);
            
            requestAnimationFrame(() => {
              document.body.removeChild(dragPreview);
            });
            
            e.currentTarget.style.opacity = '0.5';
          }
        }}
        onDragEnd={(e) => {
          e.currentTarget.style.opacity = '';
        }}
      >
        <div className="tree-node-indent" style={{ width: `${level * 12}px` }}>
          {Array.from({ length: level }).map((_, i) => {
            const isCurrentLevel = i === level - 1;
            const shouldHideLine = parentPath[i] === true && i < level - 1;
            return (
              <span
                key={i}
                className={clsx('tree-indent-line', {
                  'tree-indent-line-last': isCurrentLevel && isLast,
                  'tree-indent-line-hidden': shouldHideLine,
                })}
              />
            );
          })}
        </div>
        <div className="tree-node-content">
          {nodeHasChevron(node) ? (
            <span className="tree-node-arrow">
              <ExpandIcon expanded={expanded} />
            </span>
          ) : siblingsHaveChevron ? (
            <span className="tree-node-arrow tree-node-arrow-placeholder" aria-hidden="true" />
          ) : null}
          {(node.type === 'resource' || node.type === 'apiVersion') && !node.hideCount && (
            <span className="tree-node-count">
              {node.count === undefined || node.count === null ? '-' : `${node.count}`}
            </span>
          )}
          <span className="tree-node-icon">
            {node.id === 'kakauide-root'
              ? getCategoryIcon('kakauide')
              : node.type === 'overview'
                ? <DashboardIcon />
                : node.type === 'argo-overview'
                  ? getCategoryIcon('argocd')
                : node.type === 'finops'
                  ? getCategoryIcon('finops')
                  : node.type === 'helm'
                    ? getCategoryIcon('helm')
                    : node.type === 'resource'
                      ? getResourceIcon(node.label)
                      : node.type === 'category'
                        ? getCategoryIcon(node.label)
                        : node.type === 'apiVersion'
                          ? getCategoryIcon('package')
                          : node.type === 'vclusters' || node.type === 'vcluster'
                            ? getCategoryIcon('vclusters')
                            : getCategoryIcon('folder')}
          </span>
          <div className="tree-node-label-wrapper">
            <span className="tree-node-label">{node.label}</span>
          </div>
        </div>
      </div>
      {expanded && node.children && (
        <div
          className="tree-node-children"
          style={
            {
              '--parent-indent': `${level * 12}px`,
              '--count-min-width': childMaxCountDigits > 0 ? `calc(${childMaxCountDigits}ch + 8px)` : undefined,
            } as React.CSSProperties
          }
        >
          {filteredChildren.map((child: any, index: number) => (
            <TreeNode
              key={child.id}
              node={child}
              level={level + 1}
              searchQuery={searchQuery}
              focusedNodeId={focusedNodeId}
              onNodeClick={onNodeClick}
              isLast={index === filteredChildren.length - 1}
              parentPath={[...parentPath, isLast]}
              ancestorLabels={[...ancestorLabels, node.label]}
              siblingsHaveChevron={childrenHaveChevron}
            />
          ))}
        </div>
      )}
      {contextMenu && node.type === 'resource' && (
        <div
          ref={contextMenuRef}
          className="tree-node-context-menu"
          style={{
            position: 'fixed',
            left: contextMenu.x,
            top: contextMenu.y,
            zIndex: 10000,
          }}
        >
          <div className="context-menu-item" onClick={handleSeeDetail}>
            <span>See detail</span>
          </div>
          <div className="context-menu-item" onClick={handleOpenToSide}>
            <span>Open to the Side</span>
            <span className="context-menu-shortcut">⌥⏎</span>
          </div>
        </div>
      )}
    </div>
  );
};

export default memo(TreeNode);
