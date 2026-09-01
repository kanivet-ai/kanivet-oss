import React, { useState, useRef, useEffect } from 'react';
import { parseClusterName } from '../utils/clusterUtils';
import AWSIcon from './AWSIcon';
import GCPIcon from './GCPIcon';
import AzureIcon from './AzureIcon';
import { KubernetesIcon, CloseIcon, EditIcon, ResetIcon, DragHandleIcon } from './icons';
import { Tooltip } from './common/Tooltip';
import { CloudProvider } from '../types';
import { getClusterStatusPresentation } from '../utils/clusterStatusPresentation';

interface ClusterItemProps {
  cluster: string;
  alias?: string;
  kubeconfig?: string;
  status?: any;
  isLoading: boolean;
  isActive?: boolean;
  isDragging?: boolean;
  showRemoveBtn?: boolean;
  isOpened?: boolean;
  onSelect: (cluster: string) => void;
  onRemove?: (cluster: string) => void;
  onAliasChange?: (cluster: string, alias: string) => void;
  onAliasReset?: (cluster: string) => void;
  onDragStart: (e: React.DragEvent, cluster: string) => void;
  onDragEnd: () => void;
}

export const ClusterItem: React.FC<ClusterItemProps> = ({
  cluster,
  alias,
  kubeconfig,
  status,
  isLoading,
  isActive = false,
  isDragging = false,
  showRemoveBtn = false,
  isOpened = false,
  onSelect,
  onRemove,
  onAliasChange,
  onAliasReset,
  onDragStart,
  onDragEnd,
}) => {
  const clusterInfo = parseClusterName(cluster, alias);
  const statusPresentation = getClusterStatusPresentation(status, isLoading);
  const [isEditing, setIsEditing] = useState(false);
  const [editValue, setEditValue] = useState(clusterInfo.displayName);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [isEditing]);

  // Update editValue when alias changes externally
  useEffect(() => {
    setEditValue(clusterInfo.displayName);
  }, [clusterInfo.displayName]);

  const handleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!isEditing) {
      onSelect(cluster);
    }
  };

  const handleRemove = (e: React.MouseEvent) => {
    e.stopPropagation();
    onRemove?.(cluster);
  };

  const handleDoubleClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (onAliasChange) {
      setEditValue(clusterInfo.displayName);
      setIsEditing(true);
    }
  };

  const handleEditClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (onAliasChange) {
      setEditValue(clusterInfo.displayName);
      setIsEditing(true);
    }
  };

  const handleSave = () => {
    const trimmedValue = editValue.trim();
    if (trimmedValue && onAliasChange) {
      // Only save if the value is different from the original cluster name
      // If it matches the original, we'll delete the alias
      onAliasChange(cluster, trimmedValue);
    }
    setIsEditing(false);
  };

  const handleCancel = () => {
    setEditValue(clusterInfo.displayName);
    setIsEditing(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    e.stopPropagation();
    if (e.key === 'Enter') {
      handleSave();
    } else if (e.key === 'Escape') {
      handleCancel();
    }
  };

  // Use provider from status if available, otherwise fall back to context name parsing
  const provider: CloudProvider = status?.provider || clusterInfo.provider;
  const region = status?.region || clusterInfo.region;
  const accountId = clusterInfo.accountId;

  const renderClusterIcon = () => {
    switch (provider) {
      case 'aws':
        return (
          <span className="cluster-icon aws">
            <AWSIcon size={16} />
          </span>
        );
      case 'gcp':
        return (
          <span className="cluster-icon gcp">
            <GCPIcon size={16} />
          </span>
        );
      case 'azure':
        return (
          <span className="cluster-icon azure">
            <AzureIcon size={16} />
          </span>
        );
      default:
        return (
          <span className="cluster-icon">
            <KubernetesIcon />
          </span>
        );
    }
  };

  const displayName = clusterInfo.displayName;
  const hasAlias = clusterInfo.hasAlias;

  const getProviderLabel = () => {
    switch (provider) {
      case 'aws': return accountId || 'AWS';
      case 'gcp': return 'GCP';
      case 'azure': return 'Azure';
      default: return null;
    }
  };

  const providerLabel = getProviderLabel();

  return (
    <div
      className={`cluster-item ${isActive ? 'active' : ''} ${
        isDragging ? 'dragging' : ''
      } ${statusPresentation.isNotReady ? 'not-ready' : ''} ${isOpened ? 'opened' : ''}`}
      draggable={!isEditing}
      onDragStart={(e) => onDragStart(e, cluster)}
      onDragEnd={onDragEnd}
    >
      <div className="cluster-content" onClick={handleClick}>
        {renderClusterIcon()}
        <div className="cluster-info">
          {isEditing ? (
            <input
              ref={inputRef}
              type="text"
              className="cluster-name-input"
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onKeyDown={handleKeyDown}
              onBlur={handleSave}
              onClick={(e) => e.stopPropagation()}
            />
          ) : (
            <>
              <div className="cluster-name-row">
                <Tooltip
                  content={
                    <div className="cluster-tooltip">
                      <div className="cluster-tooltip-name">{cluster}</div>
                      {hasAlias && <div className="cluster-tooltip-alias">Alias: {displayName}</div>}
                    </div>
                  }
                  side="top"
                  delayDuration={400}
                >
                  <span
                    className={`cluster-name ${hasAlias ? 'has-alias' : ''}`}
                    onDoubleClick={handleDoubleClick}
                  >
                    {displayName}
                  </span>
                </Tooltip>
                {onAliasChange && (
                  <button
                    className="cluster-edit-btn"
                    onClick={handleEditClick}
                    title="Edit cluster alias"
                  >
                    <EditIcon />
                  </button>
                )}
                {hasAlias && onAliasReset && (
                  <button
                    className="cluster-reset-btn"
                    onClick={(e) => {
                      e.stopPropagation();
                      onAliasReset(cluster);
                    }}
                    title="Reset to original name"
                  >
                    <ResetIcon />
                  </button>
                )}
              </div>
              <div className="cluster-meta-row">
                {kubeconfig && (
                  <Tooltip content={kubeconfig} side="bottom" delayDuration={200}>
                    <span className="cluster-kubeconfig">{kubeconfig.split('/').pop()?.replace(/^config\.?/, '') || kubeconfig.split('/').pop()}</span>
                  </Tooltip>
                )}
                {providerLabel && <span className={`cluster-provider ${provider}`}>{providerLabel}</span>}
                {region && <span className="cluster-region">{region}</span>}
              </div>
            </>
          )}
        </div>
        <div className="cluster-status-container">
          {isOpened && (
            <span className="cluster-opened-indicator" title="Cluster is open in a tab">●</span>
          )}
          <div
            className={`cluster-status ${statusPresentation.className}`}
            title={statusPresentation.title}
          />
          {status?.responseTimeMs !== undefined && (
            <span className="cluster-latency">{status.responseTimeMs}ms</span>
          )}
        </div>
      </div>
      {showRemoveBtn && !isEditing && (
        <button
          className="remove-btn"
          onClick={handleRemove}
          title="Remove from group"
        >
          <CloseIcon />
        </button>
      )}
      {!isEditing && (
        <span className="cluster-drag-handle" title="Drag to reorder">
          <DragHandleIcon />
        </span>
      )}
    </div>
  );
};
