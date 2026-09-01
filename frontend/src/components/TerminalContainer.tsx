import { useState, useCallback, useRef } from 'react';
import TerminalSplitPane, { SplitNode } from './TerminalSplitPane';
import TerminalToolbar from './TerminalToolbar';
import TerminalManager from '../services/terminalManager';
import './TerminalContainer.css';

interface TerminalContainerProps {
  initialTabId: string;
}

const TerminalContainer = ({ initialTabId }: TerminalContainerProps) => {
  const [rootNode, setRootNode] = useState<SplitNode>({
    id: 'root',
    type: 'terminal',
    terminalId: initialTabId,
  });
  const [focusedNodeId, setFocusedNodeId] = useState<string>('root');
  const [showSearch, setShowSearch] = useState(false);
  const nodeCounter = useRef(1);

  const generateNodeId = () => {
    return `node-${nodeCounter.current++}`;
  };

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

  // Clean up all terminal sessions in a node and its children
  const cleanupTerminalSessions = useCallback((node: SplitNode) => {
    if (node.type === 'terminal' && node.terminalId) {
      const manager = TerminalManager.getInstance();
      const session = manager.getSession(node.terminalId);

      // Send close message to backend
      if (
        session?.ws &&
        session.ws.readyState === WebSocket.OPEN &&
        session.sessionId
      ) {
        try {
          session.ws.send(
            JSON.stringify({
              type: 'terminal',
              payload: { action: 'close', sessionId: session.sessionId },
            }),
          );
        } catch (e) {
          console.error('Error sending close message:', e);
        }
      }

      // Close the session in TerminalManager
      try {
        manager.closeSession(node.terminalId);
      } catch (e) {
        console.error('Error closing session:', e);
      }
    }

    // Recursively clean up children
    if (node.children) {
      node.children.forEach((child) => cleanupTerminalSessions(child));
    }
  }, []);

  const handleSplit = useCallback(
    (nodeId: string, direction: 'horizontal' | 'vertical') => {
      setRootNode((prevRoot) => {
        const targetNode = findNode(prevRoot, nodeId);
        if (!targetNode || targetNode.type !== 'terminal') return prevRoot;

        const newTerminalId = generateNodeId();
        const newNode: SplitNode = {
          id: targetNode.id,
          type: 'split',
          direction,
          children: [
            {
              id: generateNodeId(),
              type: 'terminal',
              terminalId: targetNode.terminalId,
              size: 50,
            },
            {
              id: newTerminalId,
              type: 'terminal',
              terminalId: `${initialTabId}-${newTerminalId}`,
              size: 50,
            },
          ],
        };

        return updateNode(prevRoot, nodeId, () => newNode);
      });
    },
    [initialTabId, findNode, updateNode],
  );

  const handleClose = useCallback(
    (nodeId: string) => {
      if (nodeId === 'root') return; // Can't close root

      // First, find the node to close and clean up its sessions
      const nodeToClose = findNode(rootNode, nodeId);
      if (nodeToClose) {
        cleanupTerminalSessions(nodeToClose);
      }

      setRootNode((prevRoot) => {
        const parent = findParent(prevRoot, nodeId);
        if (!parent || !parent.children) return prevRoot;

        // If parent has only 2 children, replace parent with the other child
        if (parent.children.length === 2) {
          const otherChild = parent.children.find(
            (child) => child.id !== nodeId,
          )!;

          // If this is the root split, make the other child the new root
          if (parent.id === 'root') {
            return { ...otherChild, id: 'root' };
          }

          // Otherwise, replace parent with other child in grandparent
          return updateNode(prevRoot, parent.id, () => ({
            ...otherChild,
            id: parent.id,
          }));
        }

        // If parent has more than 2 children, just remove this child
        return updateNode(prevRoot, parent.id, (node) => ({
          ...node,
          children: node.children?.filter((child) => child.id !== nodeId),
        }));
      });
    },
    [rootNode, findNode, findParent, updateNode, cleanupTerminalSessions],
  );

  const handleFocus = useCallback((nodeId: string) => {
    setFocusedNodeId(nodeId);
  }, []);

  const handleSearchToggle = useCallback(() => {
    setShowSearch(!showSearch);
  }, [showSearch]);

  // Get the currently focused terminal ID
  const getFocusedTerminalId = () => {
    const focusedNode = findNode(rootNode, focusedNodeId);
    return focusedNode?.terminalId || initialTabId;
  };

  const handleSplitHorizontal = useCallback(() => {
    handleSplit(focusedNodeId, 'horizontal');
  }, [focusedNodeId, handleSplit]);

  const handleSplitVertical = useCallback(() => {
    handleSplit(focusedNodeId, 'vertical');
  }, [focusedNodeId, handleSplit]);

  // Don't clean up sessions on unmount - terminals should persist when moved between panes
  // The sessions will be cleaned up when the tab is actually closed

  return (
    <div className="terminal-container-wrapper">
      <TerminalToolbar
        currentTabId={getFocusedTerminalId()}
        onSearchToggle={handleSearchToggle}
        isSearchOpen={showSearch}
        onSplitHorizontal={handleSplitHorizontal}
        onSplitVertical={handleSplitVertical}
      />
      <div className="terminal-split-wrapper">
        <TerminalSplitPane
          node={rootNode}
          onSplit={handleSplit}
          onClose={handleClose}
          onFocus={handleFocus}
          focusedNodeId={focusedNodeId}
          showSearch={showSearch}
          onSearchToggle={handleSearchToggle}
        />
      </div>
    </div>
  );
};

export default TerminalContainer;
