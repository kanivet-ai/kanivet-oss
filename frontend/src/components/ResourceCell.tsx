import { memo } from 'react';

interface ResourceCellProps {
  item: any;
  column: string;
  selectedNode?: any;
  rolloutStatuses?: Map<string, any>;
  handleNamespaceChange?: (namespace: string) => void;
  cluster?: string;
  getColumnValue: (
    item: any,
    column: string,
    selectedNode?: any,
    rolloutStatuses?: Map<string, any>,
    handleNamespaceChange?: (namespace: string) => void,
    cluster?: string,
  ) => any;
  onClick: () => void;
  onDoubleClick: () => void;
}

const ResourceCell = memo(
  ({
    item,
    column,
    selectedNode,
    rolloutStatuses,
    handleNamespaceChange,
    cluster,
    getColumnValue,
    onClick,
    onDoubleClick,
  }: ResourceCellProps) => {
    return (
      <td
        className={`column-${column.toLowerCase()}`}
        onClick={onClick}
        onDoubleClick={onDoubleClick}
      >
        <div className="cell-content">
          {getColumnValue(item, column, selectedNode, rolloutStatuses, handleNamespaceChange, cluster)}
        </div>
      </td>
    );
  },
  (prevProps, nextProps) => {
    return (
      prevProps.item === nextProps.item &&
      prevProps.column === nextProps.column &&
      prevProps.getColumnValue === nextProps.getColumnValue
    );
  },
);

ResourceCell.displayName = 'ResourceCell';

export default ResourceCell;

