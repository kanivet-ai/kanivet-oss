import { useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';

interface ActionMenuProps {
  isOpen: boolean;
  x: number;
  y: number;
  actions: string[];
  onSelect: (action: string) => void;
  onClose: () => void;
}

const ActionMenu = ({
  isOpen,
  x,
  y,
  actions,
  onSelect,
  onClose,
}: ActionMenuProps) => {
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

  if (!isOpen) return null;

  return createPortal(
    <div
      ref={menuRef}
      className="action-menu"
      style={{
        position: 'fixed',
        left: x,
        top: y,
        zIndex: 1000,
      }}
    >
      {actions.map((action) => (
        <div
          key={action}
          className="action-menu-item"
          onClick={() => {
            onSelect(action);
            onClose();
          }}
        >
          {action}
        </div>
      ))}
    </div>,
    document.body,
  );
};

export default ActionMenu;
