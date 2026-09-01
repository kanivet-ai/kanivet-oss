import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import TreeNode from './TreeNode';
import ScrollContainer from './ScrollContainer';
import DebugPanel from './DebugPanel';
import { useRegisteredKeyboard } from '../hooks/useRegisteredKeyboard';
import useDebounce from '../hooks/useDebounce';
import { useStore } from '../store';
import { createTreeNavigationHandlers } from '../utils/keyboardShortcuts';
import api from '../services/api';
import './TreeSidebar.css';

interface TreeSidebarProps {
  mode?: string;
}

const TreeSidebar = ({ mode: _mode }: TreeSidebarProps = {}) => {
  const {
    currentTab,
    loadTreeData,
    setSearchQuery,
    expandNode,
    selectNode,
    loadListItems,
    loadDetails,
    recordNavigation,
    setFocusArea,
    getCurrentTabState,
    toggleNodeExpansion,
    openResourceListTab,
    openDetailTab,
  } = useStore();

  const tabState = getCurrentTabState();
  const treeData = tabState?.treeData || [];
  const searchQuery = tabState?.searchQuery || '';
  const focusArea = tabState?.focusArea || 'tree';

  const [localSearch, setLocalSearch] = useState('');
  const [focusedNodeId, setFocusedNodeId] = useState<string | null>(null);
  const [width, setWidth] = useState(() => {
    const saved = localStorage.getItem('treeSidebarWidth');
    const defaultWidth = Math.floor(window.innerWidth * 0.2);
    const savedWidth = saved ? parseInt(saved, 10) : defaultWidth;
    return Math.max(savedWidth, 160);
  });
  const [isResizing, setIsResizing] = useState(false);
  const [debugExpanded, setDebugExpanded] = useState(() => {
    const saved = localStorage.getItem('debugPanelExpanded');
    return saved === 'true';
  });
  const sidebarRef = useRef<HTMLDivElement>(null);
  const pendingCountUpdates = useRef<Map<string, number>>(new Map());
  const countUpdateTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const debouncedSearch = useDebounce(localSearch, 300);

  useEffect(() => {
    const handler = async (e: any) => {
      const node = e.detail?.node;
      if (!node?.type || node.type !== 'resource' || !currentTab) return;

      // If it's a custom/crossplane kind, open the CRD definition
      if (
        typeof node.id === 'string' &&
        (node.id.startsWith('custom-') || node.id.startsWith('crossplane-')) &&
        node.data?.group && node.data?.name
      ) {
        const crdName = `${node.data.name}.${node.data.group}`;
        const crdResource = {
          name: 'customresourcedefinitions',
          group: 'apiextensions.k8s.io',
          version: 'v1',
          kind: 'customresourcedefinitions',
          namespaced: false,
        } as any;
        const crdItem = {
          kind: 'CustomResourceDefinition',
          apiVersion: 'apiextensions.k8s.io/v1',
          metadata: { name: crdName },
        } as any;
        openDetailTab(crdResource, crdItem, currentTab, true);
        await loadDetails(currentTab, crdResource, { name: crdName });
        return;
      }

      // Otherwise open API resource definition tab for standard resources
      if (node.data) {
        const defResource = {
          name: node.data.name,
          group: node.data.group || '',
          version: node.data.version,
          kind: node.data.kind,
          namespaced: !!node.data.namespaced,
        } as any;
        const defItem = {
          kind: 'APIResourceDefinition',
          apiVersion: `${node.data.group || ''}/${node.data.version}`.replace(/^\//, ''),
          apiResource: {
            name: node.data.name,
            group: node.data.group || '',
            version: node.data.version,
            kind: node.data.kind,
            namespaced: !!node.data.namespaced,
          },
          metadata: { name: node.data.kind },
        } as any;
        openDetailTab(defResource, defItem, currentTab, true);
      }
    };
    window.addEventListener('tree:see-detail', handler as EventListener);
    return () => window.removeEventListener('tree:see-detail', handler as EventListener);
  }, [currentTab, openDetailTab, loadDetails]);

  useEffect(() => {
    if (currentTab) {
      loadTreeData(currentTab);
    }
  }, [currentTab, loadTreeData]);

  // Subscribe to real-time count updates
  useEffect(() => {
    if (!currentTab) return;

    const handleCountUpdate = (msg: { group: string; resource: string; count: number }) => {
      const key = `${msg.group}/${msg.resource}`;
      pendingCountUpdates.current.set(key, msg.count);
      if (countUpdateTimeoutRef.current) return;
      countUpdateTimeoutRef.current = setTimeout(() => {
        const updates = new Map(pendingCountUpdates.current);
        pendingCountUpdates.current.clear();
        countUpdateTimeoutRef.current = null;
        if (updates.size === 0) return;
        useStore.setState((state) => {
          const tabIndex = state.activeTabs.findIndex((t) => t.id === currentTab);
          if (tabIndex === -1) return state;
          const currentState = state.activeTabs[tabIndex].state;
          const expandedNodes = currentState.expandedNodes;
          const updateTreeNodeCount = (nodes: any[]): any[] => {
            let changed = false;
            const next = nodes.map((node: any) => {
              let updatedNode = node;
              if (node.children) {
                const children = updateTreeNodeCount(node.children);
                if (children !== node.children) updatedNode = { ...updatedNode, children };
              }
              if (expandedNodes.has(node.id) && !updatedNode.expanded) {
                updatedNode = updatedNode === node ? { ...node } : updatedNode;
                updatedNode.expanded = true;
              }
              if (node.type === 'resource' && node.data) {
                const nodeKey = `${node.data.group || ''}/${node.data.name || ''}`;
                if (updates.has(nodeKey) && updatedNode.count !== updates.get(nodeKey)) {
                  updatedNode = updatedNode === node ? { ...node } : updatedNode;
                  updatedNode.count = updates.get(nodeKey);
                }
              }
              if (updatedNode !== node) changed = true;
              return updatedNode;
            });
            return changed ? next : nodes;
          };
          const treeData = updateTreeNodeCount(currentState.treeData);
          if (treeData === currentState.treeData) return state;
          const updatedTabs = [...state.activeTabs];
          updatedTabs[tabIndex] = {
            ...updatedTabs[tabIndex],
            state: { ...updatedTabs[tabIndex].state, treeData },
          };
          return { activeTabs: updatedTabs };
        });
      }, 100);
    };

    const unsubscribeCounts = api.subscribeToCounts(currentTab, handleCountUpdate);

    return () => {
      unsubscribeCounts();
      if (countUpdateTimeoutRef.current) {
        clearTimeout(countUpdateTimeoutRef.current);
        countUpdateTimeoutRef.current = null;
      }
    };
  }, [currentTab]);

  useEffect(() => {
    setSearchQuery(debouncedSearch);
  }, [debouncedSearch, setSearchQuery]);

  const clusterData = treeData;

  const getAllNodes = useCallback((nodes: any[], parent: any = null): any[] => {
    let result: any[] = [];
    nodes.forEach((node) => {
      result.push({ ...node, parent });
      if (node.expanded && node.children) {
        result = result.concat(getAllNodes(node.children, node));
      }
    });
    return result;
  }, []);

  const allNodesMemo = useMemo(() => getAllNodes(clusterData), [clusterData, getAllNodes]);

  const nodeIndexMap = useMemo(() => {
    const map = new Map<string, any>();
    const indexNodes = (nodes: any[]) => {
      nodes.forEach(node => {
        map.set(node.id, node);
        if (node.children) indexNodes(node.children);
      });
    };
    indexNodes(clusterData);
    return map;
  }, [clusterData]);

  const handleNodeClick = useCallback(
    async (node: any, isPinned: boolean = false) => {
      if (node.disabled) return;
      console.log(
        'handleNodeClick called with node:',
        node,
        'isPinned:',
        isPinned,
      );
      // Read focusedCenterPaneId fresh from store to avoid stale closure
      const freshPaneId = useStore.getState().getCurrentTabState()?.focusedCenterPaneId;
      setFocusedNodeId(node.id);
      if (node.type === 'overview') {
        console.log('Node is overview, opening dashboard tab...');
        selectNode(node);
        if (currentTab) {
          const dashboardResource = {
            name: 'cluster-dashboard',
            group: '',
            version: 'v1',
            kind: 'ClusterDashboard',
            namespaced: false,
          };
          await openResourceListTab(
            dashboardResource,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          recordNavigation('overview', node.id, node.data).catch((error) => {
            console.error('Error recording navigation:', error);
          });
        }
      } else if (node.type === 'argo-overview') {
        selectNode(node);
        if (currentTab) {
          const argoOverviewResource = {
            name: 'argo-applications-overview',
            group: 'argoproj.io',
            version: 'v1alpha1',
            kind: 'ArgoApplicationsOverview',
            namespaced: false,
          };
          await openResourceListTab(
            argoOverviewResource,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          recordNavigation('argo-overview', node.id, node.data).catch(() => {});
        }
      } else if (node.type === 'helm') {
        console.log('Node is helm, opening helm releases tab...');
        selectNode(node);
        if (currentTab) {
          const helmResource = {
            name: 'helm-releases',
            group: '',
            version: 'v1',
            kind: 'HelmReleases',
            namespaced: false,
          };
          await openResourceListTab(
            helmResource,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          recordNavigation('helm', node.id, node.data).catch((error) => {
            console.error('Error recording navigation:', error);
          });
        }
      } else if (node.type === 'finops') {
        console.log('Node is finops, opening finops dashboard tab...');
        selectNode(node);
        if (currentTab) {
          const finopsResource = {
            name: 'finops-dashboard',
            group: '',
            version: 'v1',
            kind: 'FinOpsDashboard',
            namespaced: false,
          };
          await openResourceListTab(
            finopsResource,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          recordNavigation('finops', node.id, node.data).catch((error) => {
            console.error('Error recording navigation:', error);
          });
        }
      } else if (node.type === 'incident-timeline') {
        console.log('Node is incident-timeline, opening incidents tab...');
        selectNode(node);
        if (currentTab) {
          const incidentsResource = {
            name: 'incident-timeline',
            group: '',
            version: 'v1',
            kind: 'IncidentTimeline',
            namespaced: false,
          };
          await openResourceListTab(
            incidentsResource,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          recordNavigation('incident-timeline', node.id, node.data).catch((error) => {
            console.error('Error recording navigation:', error);
          });
        }
      } else if (node.type === 'vcluster') {
        if (!node.data) return;
        try {
          const conn = await api.connectVCluster(node.data.host, node.data.namespace, node.data.name);
          await useStore.getState().openTab(conn.id);
        } catch (err: any) {
          const message = err?.response?.data?.error || err?.message || 'Failed to connect to vcluster';
          window.dispatchEvent(new CustomEvent('toast:error', { detail: { message } }));
        }
        return;
      } else if (node.type === 'resource') {
        console.log('Node is resource, opening tab immediately...');
        selectNode(node);
        if (currentTab) {
          await openResourceListTab(
            node.data,
            currentTab,
            isPinned,
            freshPaneId || undefined,
          );
          Promise.all([
            loadListItems(currentTab, node.data),
            recordNavigation('resource', node.id, node.data),
          ]).catch((error) => {
            console.error('Error loading list items or navigation:', error);
          });
        }
      } else if (node.type === 'apiVersion') {
        toggleNodeExpansion(node.id);
      } else {
        if (!node.expanded) {
          toggleNodeExpansion(node.id);
          if (!node.children && currentTab) {
            await expandNode(currentTab, node.id, node.type, node.data);
          }
        } else {
          toggleNodeExpansion(node.id);
        }
      }
    },
    [
      currentTab,
      selectNode,
      loadListItems,
      recordNavigation,
      expandNode,
      toggleNodeExpansion,
      openResourceListTab,
      setFocusedNodeId,
    ],
  );

  const treeNavHandlers = createTreeNavigationHandlers(
    focusArea,
    () => allNodesMemo,
    focusedNodeId,
    setFocusedNodeId,
  );

  useRegisteredKeyboard({
    '/': {
      category: 'tree',
      description: 'Focus tree search',
      handler: (e) => {
        e.preventDefault();
        document.getElementById('tree-search')?.focus();
      },
    },
    'meta+f': {
      category: 'tree',
      description: 'Focus tree search (alt)',
      handler: (e) => {
        e.preventDefault();
        document.getElementById('tree-search')?.focus();
      },
    },
    'alt+enter': {
      category: 'tree',
      description: 'Open in split pane',
      handler: async (e) => {
        if (focusArea !== 'tree' || !focusedNodeId) return;
        e.preventDefault();
        const node = nodeIndexMap.get(focusedNodeId);
        if (node && (node.type === 'resource' || node.type === 'argo-overview')) {
          const event = new CustomEvent('centerPane:split', {
            detail: { paneId: 'root', direction: 'vertical' },
          });
          window.dispatchEvent(event);
          setTimeout(() => {
            handleNodeClick(node, true);
          }, 100);
        }
      },
    },
    ...Object.fromEntries(
      Object.entries(treeNavHandlers).map(([key, handler]) => [
        key,
        { category: 'tree' as const, description: `Navigate tree (${key})`, handler },
      ])
    ),
    h: {
      category: 'tree',
      description: 'Collapse / go to parent',
      handler: (e) => {
        if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
          return;
        e.preventDefault();
        if (!focusedNodeId) return;
        const node = nodeIndexMap.get(focusedNodeId);
        if (node?.expanded && node.type !== 'resource') {
          toggleNodeExpansion(node.id);
        } else {
          const currentNode = allNodesMemo.find((n) => n.id === focusedNodeId);
          if (currentNode?.parent) {
            setFocusedNodeId(currentNode.parent.id);
          }
        }
      },
    },
    left: {
      category: 'tree',
      description: 'Collapse / go to parent (arrow)',
      handler: (e) => {
        if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
          return;
        e.preventDefault();
        if (!focusedNodeId) return;
        const node = nodeIndexMap.get(focusedNodeId);
        if (node?.expanded && node.type !== 'resource') {
          toggleNodeExpansion(node.id);
        } else {
          const currentNode = allNodesMemo.find((n) => n.id === focusedNodeId);
          if (currentNode?.parent) {
            setFocusedNodeId(currentNode.parent.id);
          }
        }
      },
    },
    l: {
      category: 'tree',
      description: 'Expand / go to first child',
      handler: async (e) => {
        if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
          return;
        e.preventDefault();
        if (!focusedNodeId) return;
        const node = nodeIndexMap.get(focusedNodeId);
        if (!node || node.disabled) return;
        if (node.type === 'resource' || node.type === 'argo-overview') {
          await handleNodeClick(node, false);
        } else if (node.type === 'apiVersion') {
          if (!node.expanded) {
            toggleNodeExpansion(node.id);
          } else if (node.children?.length > 0) {
            setFocusedNodeId(node.children[0].id);
          }
        } else if (!node.expanded) {
          toggleNodeExpansion(node.id);
          if (!node.children && currentTab) {
            await expandNode(currentTab, node.id, node.type, node.data);
          }
        } else if (node.children?.length > 0) {
          setFocusedNodeId(node.children[0].id);
        }
      },
    },
    right: {
      category: 'tree',
      description: 'Expand / go to first child (arrow)',
      handler: async (e) => {
        if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
          return;
        e.preventDefault();
        if (!focusedNodeId) return;
        const node = nodeIndexMap.get(focusedNodeId);
        if (!node || node.disabled) return;
        if (node.type === 'resource' || node.type === 'argo-overview') {
          await handleNodeClick(node, false);
        } else if (node.type === 'apiVersion') {
          if (!node.expanded) {
            toggleNodeExpansion(node.id);
          } else if (node.children?.length > 0) {
            setFocusedNodeId(node.children[0].id);
          }
        } else if (!node.expanded) {
          toggleNodeExpansion(node.id);
          if (!node.children && currentTab) {
            await expandNode(currentTab, node.id, node.type, node.data);
          }
        } else if (node.children?.length > 0) {
          setFocusedNodeId(node.children[0].id);
        }
      },
    },
    enter: {
      category: 'tree',
      description: 'Open resource',
      handler: async (e) => {
        if (focusArea !== 'tree' || document.activeElement?.id === 'tree-search')
          return;
        e.preventDefault();
        if (!focusedNodeId) return;
        const node = nodeIndexMap.get(focusedNodeId);
        if (node) {
          await handleNodeClick(node, true);
        }
      },
    },
  }, 'tree');

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setIsResizing(true);
  }, []);

  const toggleDebugPanel = useCallback(() => {
    setDebugExpanded((prev) => {
      const newState = !prev;
      localStorage.setItem('debugPanelExpanded', String(newState));
      return newState;
    });
  }, []);


  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing) return;

      const newWidth = e.clientX;
      const clampedWidth = Math.min(Math.max(newWidth, 160), 600);
      setWidth(clampedWidth);
    };

    const handleMouseUp = () => {
      if (isResizing) {
        setIsResizing(false);
        localStorage.setItem('treeSidebarWidth', width.toString());
        document.body.classList.remove('resizing');
      }
    };

    if (isResizing) {
      document.body.classList.add('resizing');
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.classList.remove('resizing');
    };
  }, [isResizing, width]);

  return (
    <div
      ref={sidebarRef}
      className={`tree-sidebar ${focusArea === 'tree' ? 'focused' : ''}`}
      style={{
        width: `${width}px`,
        minWidth: `${width}px`,
        maxWidth: `${width}px`,
      }}
      onClick={() => setFocusArea('tree')}
    >
      <div className="sidebar-header">
        <div className="search-box">
          <input
            id="tree-search"
            type="text"
            placeholder="Search resources (/ or ⌘F)"
            value={localSearch}
            onChange={(e) => setLocalSearch(e.target.value)}
            className="search-input"
          />
        </div>
      </div>
      <ScrollContainer
        className="tree-scroll-area"
        viewportClassName="tree-viewport"
      >
        <div className="tree-content">
          {(() => {
            const ideNode = {
              id: 'kanivetide-root',
              label: 'kanivetIDE',
              type: 'category',
              expanded: true,
              children: clusterData,
            };
            return (
              <>
                <TreeNode
                  key={`${currentTab}-${ideNode.id}`}
                  node={ideNode}
                  level={0}
                  searchQuery={searchQuery}
                  focusedNodeId={focusedNodeId}
                  onNodeClick={handleNodeClick}
                  ancestorLabels={[]}
                />
              </>
            );
          })()}
        </div>
      </ScrollContainer>
      <DebugPanel
        treeData={clusterData}
        isExpanded={debugExpanded}
        onToggle={toggleDebugPanel}
      />
      <div
        className={`resize-handle ${isResizing ? 'resizing' : ''} ${width <= 160 ? 'at-minimum' : ''}`}
        onMouseDown={handleMouseDown}
        title={width <= 160 ? 'Minimum width reached' : 'Drag to resize'}
      />
    </div>
  );
};

export default TreeSidebar;
