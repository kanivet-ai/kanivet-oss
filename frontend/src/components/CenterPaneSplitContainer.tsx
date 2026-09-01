import { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { flushSync } from 'react-dom';
import CenterPaneSplitPane, { SplitNode } from './CenterPaneSplitPane';
import { useRegisteredKeyboard } from '../hooks/useRegisteredKeyboard';
import { useStore } from '../store';
import './CenterPaneSplitContainer.css';

const countLeafNodes = (node: SplitNode): number => {
  if (node.type === 'resourceList') return 1;
  return (node.children || []).reduce((sum, child) => sum + countLeafNodes(child), 0);
};

interface CenterPaneSplitContainerProps {
  tabId: string;
}

const getInitialLayout = (tabId: string): { rootNode: SplitNode; focusedNodeId: string; nodeCounter: number } => {
  const tabState = useStore.getState().getCurrentTabState();
  const rootNode = (tabState?.centerPaneLayout?.id
    ? tabState.centerPaneLayout
    : { id: 'root', type: 'resourceList', tabId }) as SplitNode;
  const focusedNodeId = tabState?.focusedCenterPaneId || 'root';
  const findMaxPaneId = (node: SplitNode): number => {
    const match = node.id.match(/^pane-(\d+)$/);
    const current = match ? parseInt(match[1], 10) : 0;
    if (!node.children) return current;
    return Math.max(current, ...node.children.map(findMaxPaneId));
  };
  return { rootNode, focusedNodeId, nodeCounter: findMaxPaneId(rootNode) + 1 };
};

const CenterPaneSplitContainer = ({ tabId }: CenterPaneSplitContainerProps) => {
  const { updateCurrentTabState } = useStore();
  const initialState = useRef(getInitialLayout(tabId));
  const [rootNode, setRootNode] = useState<SplitNode>(initialState.current.rootNode);
  const [focusedNodeId, setFocusedNodeId] = useState<string>(initialState.current.focusedNodeId);
  const nodeCounter = useRef(initialState.current.nodeCounter);

  const hasMultiplePanes = useMemo(() => countLeafNodes(rootNode) > 1, [rootNode]);

  const generateNodeId = useCallback(() => {
    return `pane-${nodeCounter.current++}`;
  }, []);

  // Helper function to reassign tabs from one pane to another
  const reassignTabPaneIds = (fromPaneId: string, toPaneId: string) => {
    const {
      getCurrentTabState,
      updateResourceListTab,
      updateCurrentTabState,
      bottomTabs,
      moveDetailTab,
    } = useStore.getState();
    const tabState = getCurrentTabState();

    // Reassign resource list tabs
    if (tabState?.resourceListTabs) {
      const tabsToReassign = tabState.resourceListTabs.filter(
        (tab) => tab.paneId === fromPaneId,
      );
      tabsToReassign.forEach((tab) => {
        updateResourceListTab(tab.id, { paneId: toPaneId });
      });
    }

    // Handle detail tabs - they need moveDetailTab since they have a location property
    if (tabState?.detailTabs) {
      const detailTabsToReassign = tabState.detailTabs.filter(
        (tab) => tab.location === 'center' && tab.paneId === fromPaneId,
      );
      detailTabsToReassign.forEach((tab) => {
        // We can't directly update paneId for detail tabs, so we need to update the whole tab
        moveDetailTab(tab.id, 'center');
        // After moving, update the paneId
        const updatedTabState = useStore.getState().getCurrentTabState();
        if (updatedTabState?.detailTabs) {
          const movedTab = updatedTabState.detailTabs.find(
            (t) => t.id === tab.id,
          );
          if (movedTab) {
            movedTab.paneId = toPaneId;
          }
        }
      });
    }

    // Handle bottom tabs similarly
    const bottomTabsToReassign = bottomTabs.filter(
      (tab) => tab.location === 'center' && tab.paneId === fromPaneId,
    );
    bottomTabsToReassign.forEach((tab) => {
      tab.paneId = toPaneId;
    });

    // Clean up and reassign per-pane active tab mapping
    const perPaneMap = { ...(tabState?.activeResourceListTabByPane || {}) };
    const activeInFromPane = perPaneMap[fromPaneId];

    // Remove the old pane mapping
    delete perPaneMap[fromPaneId];

    // If there was an active tab in the from pane, set it as active in the to pane
    // (unless the to pane already has an active tab)
    if (activeInFromPane && !perPaneMap[toPaneId]) {
      perPaneMap[toPaneId] = activeInFromPane;
    }

    updateCurrentTabState({ activeResourceListTabByPane: perPaneMap });
  };

  // On mount, ensure any resource list tabs without paneId are assigned to root
  useEffect(() => {
    const { getCurrentTabState, updateResourceListTab } = useStore.getState();
    const tabState = getCurrentTabState();
    if (tabState?.resourceListTabs) {
      tabState.resourceListTabs
        .filter((tab) => !tab.paneId)
        .forEach((tab) => updateResourceListTab(tab.id, { paneId: 'root' }));
    }
  }, [tabId]);

  const findNode = useCallback(
    (node: SplitNode, nodeId: string): SplitNode | null => {
      if (node.id === nodeId) return node;
      if (node.children) {
        for (const child of node.children) {
          const found = findNode(child, nodeId);
          if (found) return found;
        }
      }
      return null;
    },
    [],
  );

  // Helper function to check if a pane exists
  const paneExists = useCallback(
    (paneId: string): boolean => {
      if (paneId === 'root') return true;
      return findNode(rootNode, paneId) !== null;
    },
    [rootNode, findNode],
  );

  // Helper function to clean up orphaned pane references
  const cleanupOrphanedPaneReferences = useCallback(() => {
    const { getCurrentTabState, updateCurrentTabState } = useStore.getState();
    const tabState = getCurrentTabState();

    if (!tabState?.activeResourceListTabByPane) return;

    const perPaneMap = { ...tabState.activeResourceListTabByPane };
    let hasChanges = false;

    // Remove references to non-existent panes
    Object.keys(perPaneMap).forEach((paneId) => {
      if (!paneExists(paneId)) {
        delete perPaneMap[paneId];
        hasChanges = true;
      }
    });

    if (hasChanges) {
      updateCurrentTabState({ activeResourceListTabByPane: perPaneMap });
    }
  }, [paneExists]);

  // Persist layout on change and cleanup orphaned references
  useEffect(() => {
    updateCurrentTabState({ centerPaneLayout: rootNode });
    cleanupOrphanedPaneReferences();
  }, [rootNode, updateCurrentTabState, cleanupOrphanedPaneReferences]);

  const findParent = useCallback(
    (node: SplitNode, nodeId: string): SplitNode | null => {
      if (node.children) {
        for (const child of node.children) {
          if (child.id === nodeId) return node;
          const found = findParent(child, nodeId);
          if (found) return found;
        }
      }
      return null;
    },
    [],
  );

  const updateNode = useCallback(
    (
      node: SplitNode,
      nodeId: string,
      updater: (node: SplitNode) => SplitNode,
    ): SplitNode => {
      if (node.id === nodeId) {
        return updater(node);
      }
      if (node.children) {
        return {
          ...node,
          children: node.children.map((child) =>
            updateNode(child, nodeId, updater),
          ),
        };
      }
      return node;
    },
    [],
  );

  const handleSplit = useCallback(
    (nodeId: string, direction: 'horizontal' | 'vertical') => {
      const targetNode = findNode(rootNode, nodeId);
      if (!targetNode || targetNode.type !== 'resourceList') {
        return;
      }

      const newPaneId = generateNodeId();
      const splitContainerId = generateNodeId();
      const newNode: SplitNode = {
        id: splitContainerId,
        type: 'split',
        direction,
        children: [
          {
            id: nodeId,
            type: 'resourceList',
            tabId: targetNode.tabId,
            size: 50,
          },
          {
            id: newPaneId,
            type: 'resourceList',
            tabId: targetNode.tabId,
            size: 50,
          },
        ],
      };

      setRootNode((prevRoot) => {
        const updated = updateNode(prevRoot, nodeId, () => newNode);
        const { updateCurrentTabState: setTabState } = useStore.getState();
        setTabState({
          centerPaneLayout: updated,
          focusedCenterPaneId: newPaneId,
        });
        return updated;
      });

      flushSync(() => setFocusedNodeId(newPaneId));
    },
    [findNode, updateNode, generateNodeId, rootNode],
  );

  const handleSplitAndMoveTab = useCallback(
    (
      nodeId: string,
      direction: 'horizontal' | 'vertical',
      position: 'before' | 'after',
      tabId: string,
      tabType: string,
      sourcePaneId?: string
    ) => {
      const targetNode = findNode(rootNode, nodeId);
      if (!targetNode || targetNode.type !== 'resourceList') return;

      const newPaneId = generateNodeId();
      const splitContainerId = generateNodeId();
      const children: SplitNode[] = position === 'before'
        ? [
            { id: newPaneId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
            { id: nodeId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
          ]
        : [
            { id: nodeId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
            { id: newPaneId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
          ];

      const newNode: SplitNode = { id: splitContainerId, type: 'split', direction, children };

      setRootNode((prevRoot) => {
        const updated = updateNode(prevRoot, nodeId, () => newNode);
        const {
          getCurrentTabState,
          updateCurrentTabState: setTabState,
          moveDetailTab,
          moveBottomTab,
          setActiveBottomTab,
          setActiveDetailTab,
          bottomTabs,
        } = useStore.getState();
        const tabState = getCurrentTabState();

        let updatedResourceListTabs = tabState?.resourceListTabs || [];
        const perPaneMap = { ...(tabState?.activeResourceListTabByPane || {}) };

        if (tabType === 'resource-list') {
          updatedResourceListTabs = updatedResourceListTabs.map((tab) =>
            tab.id === tabId ? { ...tab, paneId: newPaneId } : tab
          );
          perPaneMap[newPaneId] = tabId;
        }

        setTabState({
          centerPaneLayout: updated,
          focusedCenterPaneId: newPaneId,
          resourceListTabs: updatedResourceListTabs,
          activeResourceListTabByPane: perPaneMap,
        });

        if (tabType === 'detail') {
          moveDetailTab(tabId, 'center');
          const updatedState = useStore.getState().getCurrentTabState();
          const detailTab = updatedState?.detailTabs?.find((t) => t.id === tabId);
          if (detailTab) detailTab.paneId = newPaneId;
          setActiveDetailTab(tabId);
        } else if (tabType === 'bottom') {
          moveBottomTab(tabId, 'center');
          const bTab = bottomTabs.find((t) => t.id === tabId);
          if (bTab) bTab.paneId = newPaneId;
          setActiveBottomTab(tabId);
        }

        if (sourcePaneId && sourcePaneId !== nodeId) {
          setTimeout(() => {
            window.dispatchEvent(new CustomEvent('centerPane:requestClose', { detail: { paneId: sourcePaneId } }));
          }, 0);
        }

        return updated;
      });

      flushSync(() => setFocusedNodeId(newPaneId));
    },
    [findNode, updateNode, generateNodeId, rootNode],
  );

  const handleSplitAndOpenResource = useCallback(
    (
      nodeId: string,
      direction: 'horizontal' | 'vertical',
      position: 'before' | 'after',
      resourceData: any
    ) => {
      const targetNode = findNode(rootNode, nodeId);
      if (!targetNode || targetNode.type !== 'resourceList') return;

      const newPaneId = generateNodeId();
      const splitContainerId = generateNodeId();
      const children: SplitNode[] = position === 'before'
        ? [
            { id: newPaneId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
            { id: nodeId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
          ]
        : [
            { id: nodeId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
            { id: newPaneId, type: 'resourceList', tabId: targetNode.tabId, size: 50 },
          ];

      const newNode: SplitNode = { id: splitContainerId, type: 'split', direction, children };

      setRootNode((prevRoot) => {
        const updated = updateNode(prevRoot, nodeId, () => newNode);
        const { updateCurrentTabState: setTabState } = useStore.getState();
        setTabState({
          centerPaneLayout: updated,
          focusedCenterPaneId: newPaneId,
        });
        return updated;
      });

      flushSync(() => setFocusedNodeId(newPaneId));

      setTimeout(async () => {
        const { selectNode, openResourceListTab, currentTab, setFocusArea } = useStore.getState();
        if (currentTab) {
          selectNode(resourceData);
          await openResourceListTab(resourceData.data, currentTab, true, newPaneId);
          setFocusArea('list');
        }
      }, 0);
    },
    [findNode, updateNode, generateNodeId, rootNode],
  );

  const handleClose = useCallback(
    (nodeId: string) => {
      if (nodeId === 'root') return;

      // Check if this is the last pane - if so, don't close it
      if (rootNode.type === 'resourceList' && rootNode.id === nodeId) {
        return; // Can't close the last remaining pane
      }

      // Close all tabs in the pane being closed
      const {
        getCurrentTabState,
        closeResourceListTab,
        closeDetailTab,
        closeBottomTab,
        bottomTabs,
      } = useStore.getState();
      const tabState = getCurrentTabState();

      // Close resource list tabs in this pane
      if (tabState?.resourceListTabs) {
        const tabsToClose = tabState.resourceListTabs.filter(
          (tab) => tab.paneId === nodeId,
        );
        tabsToClose.forEach((tab) => closeResourceListTab(tab.id));
      }

      // Close detail tabs in this pane
      if (tabState?.detailTabs) {
        const detailTabsToClose = tabState.detailTabs.filter(
          (tab) => tab.location === 'center' && tab.paneId === nodeId,
        );
        detailTabsToClose.forEach((tab) => closeDetailTab(tab.id));
      }

      // Close bottom tabs in this pane
      const bottomTabsToClose = bottomTabs.filter(
        (tab) => tab.location === 'center' && tab.paneId === nodeId,
      );
      bottomTabsToClose.forEach((tab) => closeBottomTab(tab.id));

      setRootNode((prevRoot) => {
        const parent = findParent(prevRoot, nodeId);
        if (!parent || !parent.children) return prevRoot;

        if (parent.children.length === 2) {
          const otherChild = parent.children.find(
            (child) => child.id !== nodeId,
          )!;

          if (parent.id === 'root') {
            const newRoot = { ...otherChild, id: 'root' } as SplitNode;
            setFocusedNodeId('root');
            updateCurrentTabState({ focusedCenterPaneId: 'root' });

            // Reassign tabs from the other child to root
            reassignTabPaneIds(otherChild.id, 'root');

            return newRoot;
          }

          const updated = updateNode(prevRoot, parent.id, () => ({
            ...otherChild,
            id: parent.id,
          }));
          setFocusedNodeId(parent.id);
          updateCurrentTabState({ focusedCenterPaneId: parent.id });

          // Reassign tabs from the other child to parent
          reassignTabPaneIds(otherChild.id, parent.id);

          return updated;
        }

        return updateNode(prevRoot, parent.id, (node) => ({
          ...node,
          children: node.children?.filter((child) => child.id !== nodeId),
        }));
      });
    },
    [updateCurrentTabState, rootNode, findParent, updateNode],
  );

  const handleFocus = useCallback(
    (nodeId: string) => {
      const { getCurrentTabState } = useStore.getState();
      const currentLayout = getCurrentTabState()?.centerPaneLayout;
      const findNodeInLayout = (node: any, targetId: string): any => {
        if (!node) return null;
        if (node.id === targetId) return node;
        if (node.children) {
          for (const child of node.children) {
            const found = findNodeInLayout(child, targetId);
            if (found) return found;
          }
        }
        return null;
      };
      const node = currentLayout ? findNodeInLayout(currentLayout, nodeId) : null;
      if (!node || node.type !== 'resourceList') return;
      setFocusedNodeId(nodeId);
      updateCurrentTabState({ focusedCenterPaneId: nodeId });
    },
    [updateCurrentTabState],
  );

  const handleSplitVertical = useCallback(() => {
    handleSplit(focusedNodeId, 'vertical');
  }, [focusedNodeId, handleSplit]);

  const handleSplitHorizontal = useCallback(() => {
    handleSplit(focusedNodeId, 'horizontal');
  }, [focusedNodeId, handleSplit]);

  useRegisteredKeyboard({
    'meta+\\': {
      category: 'panes',
      description: 'Split pane vertically',
      handler: (e) => {
        e.preventDefault();
        handleSplitVertical();
      },
    },
    'meta+shift+\\': {
      category: 'panes',
      description: 'Split pane horizontally',
      handler: (e) => {
        e.preventDefault();
        handleSplitHorizontal();
      },
    },
  }, 'centerPane');

  useEffect(() => {
    const closeHandler = (e: any) => {
      const paneId = e?.detail?.paneId;
      if (!paneId || paneId === 'root') return;
      // Trigger close if pane has no tabs
      const { getCurrentTabState } = useStore.getState();
      const s = getCurrentTabState();
      if (!s) return;
      const resourceTabs = (s.resourceListTabs || []).filter(
        (t) => t.paneId === paneId,
      );
      const detailTabs = (s.detailTabs || []).filter(
        (t) => t.location === 'center' && t.paneId === paneId,
      );
      const bottomTabs = (useStore.getState().bottomTabs || []).filter(
        (t) => t.location === 'center' && t.paneId === paneId,
      );
      if (resourceTabs.length + detailTabs.length + bottomTabs.length === 0) {
        handleClose(paneId);
      }
    };

    const splitHandler = (e: any) => {
      const paneId = e?.detail?.paneId || 'root';
      const direction = e?.detail?.direction || 'vertical';
      handleSplit(paneId, direction);
    };

    window.addEventListener('centerPane:requestClose', closeHandler as any);
    window.addEventListener('centerPane:split', splitHandler as any);
    return () => {
      window.removeEventListener(
        'centerPane:requestClose',
        closeHandler as any,
      );
      window.removeEventListener('centerPane:split', splitHandler as any);
    };
  }, [handleClose, handleSplit]);


  return (
    <div className="center-pane-split-container">
      <CenterPaneSplitPane
        node={rootNode}
        onSplit={handleSplit}
        onClose={handleClose}
        onFocus={handleFocus}
        onSplitAndMoveTab={handleSplitAndMoveTab}
        onSplitAndOpenResource={handleSplitAndOpenResource}
        focusedNodeId={focusedNodeId}
        hasMultiplePanes={hasMultiplePanes}
      />
    </div>
  );
};

export default CenterPaneSplitContainer;
