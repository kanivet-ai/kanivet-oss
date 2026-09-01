import { useState, useRef, useCallback, useEffect, useMemo } from 'react';
import Terminal from './Terminal';
import './TerminalSplitPane.css';

export interface SplitNode {
  id: string;
  type: 'terminal' | 'split';
  direction?: 'horizontal' | 'vertical';
  children?: SplitNode[];
  size?: number; // Percentage of parent
  terminalId?: string;
}

interface TerminalSplitPaneProps {
  node: SplitNode;
  onSplit: (nodeId: string, direction: 'horizontal' | 'vertical') => void;
  onClose: (nodeId: string) => void;
  onFocus: (nodeId: string) => void;
  focusedNodeId?: string;
  showSearch?: boolean;
  onSearchToggle?: () => void;
}

const TerminalSplitPane = ({
  node,
  onSplit,
  onClose,
  onFocus,
  focusedNodeId,
  showSearch,
  onSearchToggle,
}: TerminalSplitPaneProps) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const [isResizing, setIsResizing] = useState(false);
  const [sizes, setSizes] = useState<number[]>(
    node.children?.map((child) => child.size || 50) || [],
  );

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
    // Update sizes when children change
    setSizes(childrenSizes);
  }, [childrenSizes]);

  if (node.type === 'terminal') {
    return (
      <div
        className={`terminal-split-leaf ${
          focusedNodeId === node.id ? 'focused' : ''
        }`}
        onClick={() => onFocus(node.id)}
      >
        <Terminal
          tabId={node.terminalId || node.id}
          showSearch={focusedNodeId === node.id ? showSearch : false}
          onSearchToggle={
            focusedNodeId === node.id ? onSearchToggle : undefined
          }
        />
        <div className="terminal-split-actions">
          <button
            className="terminal-split-action"
            onClick={(e) => {
              e.stopPropagation();
              onSplit(node.id, 'horizontal');
            }}
            title="Split Horizontally (Top/Bottom)"
            aria-label="Split Horizontally"
          >
            ⊟
          </button>
          <button
            className="terminal-split-action"
            onClick={(e) => {
              e.stopPropagation();
              onSplit(node.id, 'vertical');
            }}
            title="Split Vertically (Left/Right)"
            aria-label="Split Vertically"
          >
            ⊞
          </button>
          {node.id !== 'root' && (
            <button
              className="terminal-split-action close"
              onClick={(e) => {
                e.stopPropagation();
                onClose(node.id);
              }}
              title="Close Terminal"
              aria-label="Close Terminal"
            >
              ×
            </button>
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className={`terminal-split-container ${node.direction}`}
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
          <TerminalSplitPane
            node={child}
            onSplit={onSplit}
            onClose={onClose}
            onFocus={onFocus}
            focusedNodeId={focusedNodeId}
            showSearch={showSearch}
            onSearchToggle={onSearchToggle}
          />
          {index < node.children!.length - 1 && (
            <div
              className={`terminal-split-divider ${node.direction}`}
              onMouseDown={handleMouseDown(index)}
            />
          )}
        </div>
      ))}
    </div>
  );
};

export default TerminalSplitPane;
