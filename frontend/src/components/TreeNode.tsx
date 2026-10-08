import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useMemo,
  useState,
  useSyncExternalStore,
  memo,
} from 'react';
import clsx from 'clsx';
import { getResourceIcon, getCategoryIcon } from '../utils/resourceIcons';
import { useStore } from '../store';
import ExpandIcon from './icons/ExpandIcon';
import './TreeNode.css';

/**
 * The two highlighted rows of a tree, read by each node for itself so that
 * moving either re-renders the rows involved, not the whole tree.
 */
export interface TreeCursor {
  /** Keyboard cursor: the row j/k and the arrow keys move. */
  focused: string | null;
  /**
   * The one tree node whose list is open. Resolved once by the sidebar so a
   * selection made elsewhere (search, a link, a tab) highlights a single row.
   */
  selected: string | null;
  subscribe: (listener: () => void) => () => void;
  set: (focused: string | null, selected: string | null) => void;
}

export const createTreeCursor = (): TreeCursor => {
  const listeners = new Set<() => void>();
  const cursor: TreeCursor = {
    focused: null,
    selected: null,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    set: (focused, selected) => {
      if (focused === cursor.focused && selected === cursor.selected) return;
      cursor.focused = focused;
      cursor.selected = selected;
      listeners.forEach((l) => l());
    },
  };
  return cursor;
};

export const TreeCursorContext = createContext<TreeCursor>(createTreeCursor());

interface TreeNodeProps {
  node: any;
  level: number;
  searchQuery: string;
  onNodeClick: (node: any, isPinned?: boolean) => void;
  isLast?: boolean;
  parentPath?: boolean[];
  ancestorLabels?: string[];
}

// Shared defaults, so a node's memoized child props stay the same arrays.
const NO_PATH: boolean[] = [];
const NO_LABELS: string[] = [];

export const nodeHasChevron = (n: any): boolean =>
  !n.disabled &&
  n.type !== 'resource' &&
  n.type !== 'overview' &&
  n.type !== 'argo-overview' &&
  n.type !== 'helm' &&
  n.type !== 'vcluster' &&
  n.type !== 'finops' &&
  n.type !== 'rightsizing' &&
  n.type !== 'cluster-settings' &&
  n.type !== 'incident-timeline' &&
  n.type !== 'nats';

// Nodes that open a tab: a click opens it as a preview, a second click within
// the double-click window pins it.
const OPENS_TAB = new Set(['resource', 'overview', 'argo-overview', 'helm', 'finops', 'rightsizing', 'cluster-settings', 'incident-timeline', 'nats']);

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
  onNodeClick,
  isLast = false,
  parentPath = NO_PATH,
  ancestorLabels = NO_LABELS,
}: TreeNodeProps) => {
  const cursor = useContext(TreeCursorContext);
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
  const isFocused = useSyncExternalStore(
    cursor.subscribe,
    () => cursor.focused === node.id,
  );
  const isSelected = useSyncExternalStore(
    cursor.subscribe,
    () => !!cursor.selected && cursor.selected === node.id,
  );

  const shouldShowNode = useMemo(() => {
    return nodeMatchesSearch(node, searchQuery, ancestorLabels);
  }, [node, searchQuery, ancestorLabels]);

  const childAncestorLabels = useMemo(
    () => [...ancestorLabels, node.label],
    [ancestorLabels, node.label],
  );
  const childParentPath = useMemo(
    () => [...parentPath, isLast],
    [parentPath, isLast],
  );

  const filteredChildren = useMemo(() => {
    if (!node.children || !expanded) return [];
    return node.children.filter((child: any) =>
      nodeMatchesSearch(child, searchQuery, childAncestorLabels),
    );
  }, [node.children, expanded, searchQuery, childAncestorLabels]);

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

    if (OPENS_TAB.has(node.type)) {
      // The first click opens the resource at once; a second click within the
      // double-click window pins the tab it opened.
      if (clickTimer) {
        clearTimeout(clickTimer);
        setClickTimer(null);
        onNodeClick(node, true);
      } else {
        onNodeClick(node, false);
        setClickTimer(setTimeout(() => setClickTimer(null), 200));
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
    if (node.type === 'resource' && useStore.getState().currentTab) {
      // Emit custom event to TreeSidebar/Store to open CRD detail
      const event = new CustomEvent('tree:see-detail', {
        detail: { node },
      });
      window.dispatchEvent(event);
    }
    setContextMenu(null);
  };

  const handleOpenToSide = async () => {
    if (node.type === 'resource' && useStore.getState().currentTab) {
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
        data-tour={node.type === 'rightsizing' ? 'rightsizing-nav' : undefined}
        onClick={handleClick}
        onDoubleClick={handleDoubleClick}
        onContextMenu={handleContextMenu}
        tabIndex={-1}
        aria-disabled={disabled}
        title={disabled ? node.disabledReason || `${node.label} is not installed in this cluster` : undefined}
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
              background: var(--card);
              border: 0;
              border-radius: 7px;
              color: var(--text);
              font-family: var(--font-sans);
              font-size: 13px;
              position: absolute;
              top: -1000px;
              left: -1000px;
              pointer-events: none;
              box-shadow: 0 0 0 0.5px var(--sep), var(--shadow-pop);
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
          {/* The chevron column is always reserved, as in an outline view: a
              group of leaves would otherwise sit left of its own parent. */}
          {nodeHasChevron(node) ? (
            <span className="tree-node-arrow">
              <ExpandIcon expanded={expanded} />
            </span>
          ) : (
            <span className="tree-node-arrow tree-node-arrow-placeholder" aria-hidden="true" />
          )}
          {(node.type === 'resource' || node.type === 'apiVersion') && !node.hideCount && (
            <span className="tree-node-count">
              {node.count === undefined || node.count === null ? '-' : `${node.count}`}
            </span>
          )}
          <span className="tree-node-icon">
            {node.type === 'overview'
              ? getCategoryIcon('overview')
              : node.type === 'argo-overview'
                ? getCategoryIcon('argocd')
                : node.type === 'finops'
                  ? getCategoryIcon('finops')
                  : node.type === 'rightsizing'
                    ? getCategoryIcon('rightsizing')
                  : node.type === 'cluster-settings'
                    ? getCategoryIcon('cluster-settings')
                  : node.type === 'helm'
                    ? getCategoryIcon('helm')
                    : node.type === 'nats'
                      ? getCategoryIcon('nats')
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
            <MemoTreeNode
              key={child.id}
              node={child}
              level={level + 1}
              searchQuery={searchQuery}
              onNodeClick={onNodeClick}
              isLast={index === filteredChildren.length - 1}
              parentPath={childParentPath}
              ancestorLabels={childAncestorLabels}
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

// Children render through the memoized node too, so a count update re-renders
// only the nodes on the path to the changed one.
const MemoTreeNode = memo(TreeNode);

export default MemoTreeNode;
