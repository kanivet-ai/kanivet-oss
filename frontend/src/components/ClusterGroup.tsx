import React from 'react';
import { ClusterGroup as ClusterGroupType } from '../services/api';
import {
  ChevronRightIcon,
  ChevronUpIcon,
  ChevronDownIcon,
  FolderIcon,
  EditIcon,
  DeleteIcon,
} from './icons';

interface ClusterGroupProps {
  group: ClusterGroupType;
  index: number;
  totalGroups: number;
  isExpanded: boolean;
  isDragOver: boolean;
  clusterCount: number;
  editingGroup: number | null;
  editGroupName: string;
  onToggle: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onEdit: (group: ClusterGroupType) => void;
  onSaveEdit: (groupId: number) => void;
  onCancelEdit: () => void;
  onDelete: (groupId: number) => void;
  onEditNameChange: (value: string) => void;
  onDragOver: (e: React.DragEvent) => void;
  onDragEnter: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onDrop: (e: React.DragEvent) => void;
  children: React.ReactNode;
}

export const ClusterGroup: React.FC<ClusterGroupProps> = ({
  group,
  index,
  totalGroups,
  isExpanded,
  isDragOver,
  clusterCount,
  editingGroup,
  editGroupName,
  onToggle,
  onMoveUp,
  onMoveDown,
  onEdit,
  onSaveEdit,
  onCancelEdit,
  onDelete,
  onEditNameChange,
  onDragOver,
  onDragEnter,
  onDragLeave,
  onDrop,
  children,
}) => {
  const handleEditKeyPress = (e: React.KeyboardEvent) => {
    e.stopPropagation();
    if (e.key === 'Enter') onSaveEdit(group.id);
    if (e.key === 'Escape') onCancelEdit();
  };

  return (
    <div
      className={`cluster-group ${isDragOver ? 'drag-over' : ''}`}
      onDragOver={onDragOver}
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      <div className="group-header" onClick={onToggle}>
        <div className="group-order-controls">
          <button
            className="order-btn"
            onClick={(e) => {
              e.stopPropagation();
              onMoveUp();
            }}
            disabled={index === 0}
            title="Move up"
          >
            <ChevronUpIcon />
          </button>
          <button
            className="order-btn"
            onClick={(e) => {
              e.stopPropagation();
              onMoveDown();
            }}
            disabled={index === totalGroups - 1}
            title="Move down"
          >
            <ChevronDownIcon />
          </button>
        </div>
        <span className="expand-icon">
          <ChevronRightIcon className={isExpanded ? 'expanded' : ''} />
        </span>
        <span className="group-icon">
          <FolderIcon />
        </span>
        {editingGroup === group.id ? (
          <input
            type="text"
            value={editGroupName}
            onChange={(e) => onEditNameChange(e.target.value)}
            onKeyPress={handleEditKeyPress}
            onClick={(e) => e.stopPropagation()}
            onBlur={() => onSaveEdit(group.id)}
            autoFocus
            className="group-name-input"
          />
        ) : (
          <span
            className="group-name"
            onDoubleClick={(e) => {
              e.stopPropagation();
              onEdit(group);
            }}
            title="Double-click to edit"
          >
            {group.name}
          </span>
        )}
        <span className="group-count">{clusterCount}</span>
        <button
          className="group-edit-btn"
          onClick={(e) => {
            e.stopPropagation();
            onEdit(group);
          }}
          title="Edit group name"
        >
          <EditIcon />
        </button>
        <button
          className="group-delete-btn"
          onClick={(e) => {
            e.stopPropagation();
            onDelete(group.id);
          }}
          title="Delete group"
        >
          <DeleteIcon />
        </button>
      </div>
      {isExpanded && (
        <div className="group-clusters" onClick={(e) => e.stopPropagation()}>
          {children}
        </div>
      )}
    </div>
  );
};
