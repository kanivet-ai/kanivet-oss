import { useRef, ReactNode } from 'react';
import NamespaceSelector from './NamespaceSelector';
import BulkActionsDropdown from './BulkActionsDropdown';
import SearchInput from './SearchInput';
import './ResourceControlsBar.css';

interface ResourceControlsBarProps {
  namespaces?: string[];
  selectedNamespaces?: string[];
  onNamespaceChange?: (namespace: string) => void;
  selectedCount?: number;
  isActionsDisabled?: boolean;
  resourceKind?: string;
  onBulkAction?: (action: string) => void;
  onClearSelection?: () => void;
  searchQuery?: string;
  onSearchChange?: (query: string) => void;
  filteredCount?: number;
  totalCount?: number;
  itemCount?: number;
  showNamespaces?: boolean;
  showBulkActions?: boolean;
  showSearch?: boolean;
  onRefresh?: () => void;
  customStatus?: ReactNode;
}

const ResourceControlsBar = ({
  namespaces = [],
  selectedNamespaces = [],
  onNamespaceChange,
  selectedCount = 0,
  isActionsDisabled = false,
  resourceKind = '',
  onBulkAction,
  onClearSelection,
  searchQuery = '',
  onSearchChange,
  filteredCount = 0,
  totalCount = 0,
  itemCount,
  showNamespaces = true,
  showBulkActions = true,
  showSearch = true,
  onRefresh,
  customStatus,
}: ResourceControlsBarProps) => {
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const effectiveTotalCount = itemCount ?? totalCount;

  return (
    <div className="resource-controls">
      {showNamespaces && namespaces.length > 0 ? (
        <NamespaceSelector
          namespaces={namespaces}
          selectedNamespaces={selectedNamespaces}
          onChange={onNamespaceChange!}
        />
      ) : (
        showNamespaces && <div className="namespace-controls-placeholder"></div>
      )}

      <div className="item-count">
        {effectiveTotalCount} {effectiveTotalCount === 1 ? 'item' : 'items'}
      </div>

      {showBulkActions && (
        <BulkActionsDropdown
          selectedCount={selectedCount}
          isDisabled={isActionsDisabled}
          resourceKind={resourceKind}
          onAction={onBulkAction!}
          onClearSelection={onClearSelection}
        />
      )}

      {customStatus && (
        <div className="custom-status-container">
          {customStatus}
        </div>
      )}

      {showSearch && (
        <div className="search-container">
          <SearchInput
            ref={searchInputRef}
            value={searchQuery}
            onChange={onSearchChange!}
            onClear={() => searchInputRef.current?.focus()}
            showResultCount={true}
            resultCount={filteredCount}
            totalCount={effectiveTotalCount}
          />
        </div>
      )}

      {onRefresh && (
        <button
          className="refresh-button"
          onClick={onRefresh}
          title="Refresh"
        >
          ↻
        </button>
      )}
    </div>
  );
};

export default ResourceControlsBar;
