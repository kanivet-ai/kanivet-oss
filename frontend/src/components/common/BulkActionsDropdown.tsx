import { useState, useRef, useEffect } from 'react';
import { TrashIcon, ExclamationTriangleIcon, ReloadIcon, UpdateIcon } from '@radix-ui/react-icons';
import './BulkActionsDropdown.css';

interface BulkActionsDropdownProps {
  selectedCount: number;
  isDisabled?: boolean;
  resourceKind?: string;
  onAction: (action: string) => void;
  onClearSelection?: () => void;
}

const BulkActionsDropdown = ({
  selectedCount,
  isDisabled = false,
  resourceKind = '',
  onAction,
  onClearSelection,
}: BulkActionsDropdownProps) => {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(e.target as Node)
      ) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const handleActionClick = (action: string) => {
    setIsOpen(false);
    onAction(action);
  };

  const isRestartable = () => {
    const kind = resourceKind?.toLowerCase();
    return (
      kind === 'deployment' ||
      kind === 'deployments' ||
      kind === 'statefulset' ||
      kind === 'statefulsets' ||
      kind === 'daemonset' ||
      kind === 'daemonsets'
    );
  };

  const isHelmRelease = () => {
    const kind = resourceKind?.toLowerCase();
    return kind === 'helmrelease' || kind === 'helmreleases';
  };

  if (selectedCount === 0) {
    return null;
  }

  return (
    <div className="bulk-actions-container">
      <span className="selected-count">
        {selectedCount} selected
        {onClearSelection && (
          <button
            className="clear-selection-button"
            onClick={onClearSelection}
            title="Clear selection"
          >
            ×
          </button>
        )}
      </span>
      <div className="bulk-actions-dropdown" ref={dropdownRef}>
        <button
          className="bulk-actions-button"
          onClick={() => setIsOpen(!isOpen)}
          disabled={isDisabled}
        >
          Actions
          <span className="caret">▾</span>
        </button>
        {isOpen && (
          <div className="bulk-actions-menu">
            <div
              className="bulk-action-item"
              onClick={() => handleActionClick('delete')}
            >
              <TrashIcon className="bulk-action-icon" />
              Delete
            </div>
            {!isHelmRelease() && (
              <div
                className="bulk-action-item"
                onClick={() => handleActionClick('removeFinalizers')}
              >
                <ExclamationTriangleIcon className="bulk-action-icon" />
                Remove Finalizers
              </div>
            )}
            {!isHelmRelease() && (
              <div
                className="bulk-action-item"
                onClick={() => handleActionClick('forceRefresh')}
              >
                <ReloadIcon className="bulk-action-icon" />
                Force Refresh
              </div>
            )}
            {isRestartable() && (
              <div
                className="bulk-action-item"
                onClick={() => handleActionClick('restart')}
              >
                <UpdateIcon className="bulk-action-icon" />
                Restart All
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
};

export default BulkActionsDropdown;
