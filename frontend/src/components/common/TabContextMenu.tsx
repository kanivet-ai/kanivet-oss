import { useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';
import './TabContextMenu.css';

interface TabContextMenuProps {
  isOpen: boolean;
  x: number;
  y: number;
  onAction: (action: string) => void;
  onClose: () => void;
  onSplitPane?: (direction: 'up' | 'down' | 'left' | 'right') => void;
}

const TabContextMenu = ({
  isOpen,
  x,
  y,
  onAction,
  onClose,
  onSplitPane,
}: TabContextMenuProps) => {
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onClose();
      }
    };

    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose();
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      document.addEventListener('keydown', handleEscape);
      return () => {
        document.removeEventListener('mousedown', handleClickOutside);
        document.removeEventListener('keydown', handleEscape);
      };
    }
  }, [isOpen, onClose]);

  if (!isOpen) {
    return null;
  }

  const handleAction = (action: string) => {
    onAction(action);
    onClose();
  };

  return createPortal(
    <div
      ref={menuRef}
      className="tab-context-menu"
      style={{
        position: 'fixed',
        left: x,
        top: y,
        zIndex: 10000,
      }}
    >
      <div className="context-menu-item" onClick={() => handleAction('close')}>
        <span>Close</span>
        <span className="context-menu-shortcut">⌘W</span>
      </div>
      <div
        className="context-menu-item"
        onClick={() => handleAction('closeOthers')}
      >
        <span>Close Others</span>
        <span className="context-menu-shortcut">⌥⌘T</span>
      </div>
      <div
        className="context-menu-item"
        onClick={() => handleAction('closeToRight')}
      >
        <span>Close to the Right</span>
      </div>
      <div
        className="context-menu-item"
        onClick={() => handleAction('closeAll')}
      >
        <span>Close All</span>
        <span className="context-menu-shortcut">⌘⇧W</span>
      </div>
      {onSplitPane && (
        <>
          <div className="context-menu-separator" />
          <div
            className="context-menu-item"
            onClick={() => {
              onSplitPane('up');
              onClose();
            }}
          >
            <span>Split Up</span>
            <span className="context-menu-shortcut">⌘R ⌘\</span>
          </div>
          <div
            className="context-menu-item"
            onClick={() => {
              onSplitPane('down');
              onClose();
            }}
          >
            <span>Split Down</span>
          </div>
          <div
            className="context-menu-item"
            onClick={() => {
              onSplitPane('left');
              onClose();
            }}
          >
            <span>Split Left</span>
          </div>
          <div
            className="context-menu-item"
            onClick={() => {
              onSplitPane('right');
              onClose();
            }}
          >
            <span>Split Right</span>
          </div>
        </>
      )}
    </div>,
    document.body,
  );
};

export default TabContextMenu;
