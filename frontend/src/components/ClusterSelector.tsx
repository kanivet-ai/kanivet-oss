import React, { useState, useEffect, useCallback, useRef } from 'react';
import api, { ClusterGroup, ClusterInfo } from '../services/api';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import { ClusterItem } from './ClusterItem';
import { GroupForm } from './GroupForm';
import { ClusterGroup as ClusterGroupComponent } from './ClusterGroup';
import { PlusIcon, ResetIcon } from './icons';
import { parseClusterName } from '../utils/clusterUtils';
import { getCachedBatchClusterStatus } from '../services/api/clusters';
import './ClusterSelector.css';

const STATUS_REFRESH_INTERVAL_MS = 10000;

interface ClusterSelectorProps {
  onSelectCluster: (cluster: string) => void;
  activeCluster?: string;
  isMainView?: boolean;
}

export const ClusterSelector: React.FC<ClusterSelectorProps> = ({
  onSelectCluster,
  activeCluster,
  isMainView = false,
}) => {
  const { clusterStatuses, setClusterAlias, deleteClusterAlias } = useStore(useShallow((s) => ({ clusterStatuses: s.clusterStatuses, setClusterAlias: s.setClusterAlias, deleteClusterAlias: s.deleteClusterAlias })));
  const openTabIds = useStore(useShallow((s) => s.activeTabs.map((t) => t.id)));
  const [clusters, setClusters] = useState<ClusterInfo[]>([]);
  const [clusterKubeconfigs, setClusterKubeconfigs] = useState<Record<string, string>>({});
  const [groups, setGroups] = useState<ClusterGroup[]>([]);
  const [clustersByGroup, setClustersByGroup] = useState<
    Record<string, string[]>
  >({});
  const [assignments, setAssignments] = useState<Record<string, number>>({});
  const [aliases, setAliases] = useState<Record<string, string>>({});
  const [newGroupName, setNewGroupName] = useState('');
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());
  const [showNewGroupForm, setShowNewGroupForm] = useState(false);
  const [draggedCluster, setDraggedCluster] = useState<string | null>(null);
  const [dragOverGroup, setDragOverGroup] = useState<string | null>(null);
  const [editingGroup, setEditingGroup] = useState<number | null>(null);
  const [editGroupName, setEditGroupName] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [isLoadingClusters, setIsLoadingClusters] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const cachedStatusPollInFlight = useRef(false);

  useEffect(() => {
    loadData();
  }, []);

  useEffect(() => {
    const handleClustersRefreshed = (event: Event) => {
      const detail = (event as CustomEvent<{ clusters: ClusterInfo[] }>).detail;
      if (!detail?.clusters) return;
      setClusters(detail.clusters);
      const kubeconfigMap: Record<string, string> = {};
      detail.clusters.forEach((cluster) => {
        kubeconfigMap[cluster.name] = cluster.kubeconfig;
      });
      setClusterKubeconfigs(kubeconfigMap);
    };

    window.addEventListener('clusters:refreshed', handleClustersRefreshed);
    return () => window.removeEventListener('clusters:refreshed', handleClustersRefreshed);
  }, []);

  const loadClusterStatuses = useCallback(async () => {
    const clusterNames = clusters.map((c) => c.name);
    if (!clusterNames.length || cachedStatusPollInFlight.current) return;
    cachedStatusPollInFlight.current = true;
    try {
      const statuses = await getCachedBatchClusterStatus(clusterNames);
      useStore.setState((state) => ({ clusterStatuses: { ...state.clusterStatuses, ...statuses } }));
    } catch (error) {
      console.warn('Failed to load cached cluster statuses:', error);
    } finally {
      cachedStatusPollInFlight.current = false;
    }
  }, [clusters]);

  useEffect(() => {
    void loadClusterStatuses();
  }, [loadClusterStatuses]);

  useEffect(() => {
    if (!clusters.length) return;

    const intervalId = window.setInterval(() => {
      void loadClusterStatuses();
    }, STATUS_REFRESH_INTERVAL_MS);

    return () => window.clearInterval(intervalId);
  }, [clusters.length, loadClusterStatuses]);

  const loadData = async () => {
    const start = performance.now();
    try {
      setIsLoadingClusters(true);
      const [groupsData, clustersByGroupData, assignmentsData, aliasesData, clustersData] = await Promise.all([
        api.getClusterGroups(),
        api.getClustersByGroup(),
        api.getClusterAssignments(),
        api.getClusterAliases(),
        api.getClusters(),
      ]);
      console.log(`[ClusterSelector] API calls took ${(performance.now() - start).toFixed(0)}ms`);
      setGroups(groupsData);
      setClustersByGroup(clustersByGroupData);
      setAssignments(assignmentsData);
      setAliases(aliasesData);
      setClusters(clustersData);
      const kubeconfigMap: Record<string, string> = {};
      clustersData.forEach((c) => { kubeconfigMap[c.name] = c.kubeconfig; });
      setClusterKubeconfigs(kubeconfigMap);
      const groupsWithClusters = new Set<string>();
      groupsData.forEach((group) => {
        if (clustersByGroupData[group.name]?.length > 0) {
          groupsWithClusters.add(group.name);
        }
      });
      setExpandedGroups(groupsWithClusters);
    } catch (error) {
      console.error('Failed to load cluster data:', error);
    } finally {
      console.log(`[ClusterSelector] Total load took ${(performance.now() - start).toFixed(0)}ms`);
      setIsLoadingClusters(false);
    }
  };

  const handleRefresh = async () => {
    setIsRefreshing(true);
    setRefreshError(null);
    try {
      const freshClusters = await api.refreshClusters();
      useStore.setState({ clusterStatuses: {} });
      setClusters(freshClusters);
      const kubeconfigMap: Record<string, string> = {};
      freshClusters.forEach((c) => { kubeconfigMap[c.name] = c.kubeconfig; });
      setClusterKubeconfigs(kubeconfigMap);
    } catch (error) {
      console.error('Failed to refresh clusters:', error);
      setRefreshError('Failed to refresh clusters');
      setTimeout(() => setRefreshError(null), 3000);
    } finally {
      setIsRefreshing(false);
    }
  };

  const handleAliasChange = async (cluster: string, newAlias: string) => {
    try {
      // Get the original cluster name from parseClusterName
      const clusterInfo = parseClusterName(cluster);
      const originalName = clusterInfo.clusterName;
      
      // If the new alias matches the original name, delete the alias
      if (newAlias === originalName || newAlias === cluster) {
        await deleteClusterAlias(cluster);
        setAliases((prev) => {
          const updated = { ...prev };
          delete updated[cluster];
          return updated;
        });
      } else {
        await setClusterAlias(cluster, newAlias);
        setAliases((prev) => ({
          ...prev,
          [cluster]: newAlias,
        }));
      }
    } catch (error) {
      console.error('Failed to update cluster alias:', error);
    }
  };

  const handleAliasReset = async (cluster: string) => {
    try {
      await deleteClusterAlias(cluster);
      setAliases((prev) => {
        const updated = { ...prev };
        delete updated[cluster];
        return updated;
      });
    } catch (error) {
      console.error('Failed to reset cluster alias:', error);
    }
  };

  const toggleGroup = useCallback((groupName: string) => {
    setExpandedGroups((prev) => {
      const newExpanded = new Set(prev);
      if (newExpanded.has(groupName)) {
        newExpanded.delete(groupName);
      } else {
        newExpanded.add(groupName);
      }
      return newExpanded;
    });
  }, []);

  const handleCreateGroup = async () => {
    if (!newGroupName) return;
    try {
      await api.createClusterGroup(newGroupName, '');
      setNewGroupName('');
      setShowNewGroupForm(false);
      await loadData();
    } catch (error) {
      console.error('Failed to create group:', error);
    }
  };

  const handleDeleteGroup = async (groupId: number) => {
    if (!window.confirm('Are you sure you want to delete this group?')) return;
    try {
      await api.deleteClusterGroup(groupId);
      await loadData();
    } catch (error) {
      console.error('Failed to delete group:', error);
    }
  };

  const handleEditGroup = (group: ClusterGroup) => {
    setEditingGroup(group.id);
    setEditGroupName(group.name);
  };

  const handleSaveGroupEdit = async (groupId: number) => {
    if (!editGroupName.trim()) return;
    try {
      await api.updateClusterGroup(groupId, editGroupName, '');
      setEditingGroup(null);
      setEditGroupName('');
      await loadData();
    } catch (error) {
      console.error('Failed to update group:', error);
    }
  };

  const handleCancelEdit = useCallback(() => {
    setEditingGroup(null);
    setEditGroupName('');
  }, []);

  const handleDragStart = (e: React.DragEvent, cluster: string) => {
    e.stopPropagation();
    setDraggedCluster(cluster);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', cluster);
  };

  const handleDragEnd = useCallback(() => {
    setDraggedCluster(null);
    setDragOverGroup(null);
  }, []);

  const handleMoveGroup = async (groupId: number, direction: 'up' | 'down') => {
    const currentIndex = groups.findIndex((g) => g.id === groupId);
    if (currentIndex === -1) return;

    const newIndex = direction === 'up' ? currentIndex - 1 : currentIndex + 1;
    if (newIndex < 0 || newIndex >= groups.length) return;

    const newGroups = [...groups];
    const [removed] = newGroups.splice(currentIndex, 1);
    newGroups.splice(newIndex, 0, removed);

    const groupOrders = newGroups.map((group, index) => ({
      id: group.id,
      order: index + 1,
    }));

    try {
      await api.updateGroupOrder(groupOrders);
      await loadData();
    } catch (error) {
      console.error('Failed to update group order:', error);
    }
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  };

  const handleDragEnter = (e: React.DragEvent, groupName: string) => {
    e.preventDefault();
    setDragOverGroup(groupName);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setDragOverGroup(null);
    }
  };

  const handleDrop = async (e: React.DragEvent, groupId: number) => {
    e.preventDefault();
    setDragOverGroup(null);

    if (!draggedCluster) return;

    try {
      if (assignments[draggedCluster]) {
        await api.removeClusterFromGroup(draggedCluster);
      }
      await api.assignClusterToGroup(draggedCluster, groupId);
      await loadData();
    } catch (error) {
      console.error('Failed to move cluster:', error);
    }

    setDraggedCluster(null);
  };

  const handleRemoveFromGroup = async (clusterName: string) => {
    try {
      await api.removeClusterFromGroup(clusterName);
      await loadData();
    } catch (error) {
      console.error('Failed to remove cluster from group:', error);
    }
  };

  const unassignedClusters = clusters
    .map((c) => c.name)
    .filter((name) => !assignments[name])
    .filter((name) => {
      const query = searchQuery.toLowerCase();
      const clusterMatch = name.toLowerCase().includes(query);
      const aliasMatch = aliases[name]?.toLowerCase().includes(query);
      return clusterMatch || aliasMatch;
    })
    .sort((a, b) => {
      const aOpened = openTabIds.includes(a);
      const bOpened = openTabIds.includes(b);
      if (aOpened && !bOpened) return -1;
      if (!aOpened && bOpened) return 1;
      const aName = aliases[a] || a;
      const bName = aliases[b] || b;
      return aName.localeCompare(bName);
    });

  const handleUnassignedDrop = useCallback(
    async (e: React.DragEvent) => {
      e.preventDefault();
      setDragOverGroup(null);
      if (draggedCluster && assignments[draggedCluster]) {
        try {
          await api.removeClusterFromGroup(draggedCluster);
          await loadData();
        } catch (error) {
          console.error('Failed to unassign cluster:', error);
        }
      }
      setDraggedCluster(null);
    },
    [draggedCluster, assignments],
  );

  const renderClusterItem = (cluster: string, showRemoveBtn = false) => {
    const isOpened = openTabIds.includes(cluster);
    const isCheckingStatus = isLoadingClusters && !clusterStatuses[cluster];
    return (
      <ClusterItem
        key={cluster}
        cluster={cluster}
        alias={aliases[cluster]}
        kubeconfig={clusterKubeconfigs[cluster]}
        status={clusterStatuses[cluster]}
        isLoading={isCheckingStatus}
        isActive={cluster === activeCluster}
        isDragging={draggedCluster === cluster}
        showRemoveBtn={showRemoveBtn}
        isOpened={isOpened}
        onSelect={onSelectCluster}
        onRemove={handleRemoveFromGroup}
        onAliasChange={handleAliasChange}
        onAliasReset={handleAliasReset}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      />
    );
  };

  return (
    <div className={`cluster-selector ${isMainView ? 'main-view' : ''}`}>
      <div className="cluster-selector-body">
        <div className="assigned-clusters-section">
          <div className="section-header">
            <h3>Cluster Groups</h3>
            <button
              className="add-group-btn"
              onClick={() => setShowNewGroupForm(true)}
              title="Create new group"
            >
              <PlusIcon />
            </button>
          </div>

          {showNewGroupForm && (
            <GroupForm
              value={newGroupName}
              onChange={setNewGroupName}
              onSave={handleCreateGroup}
              onCancel={() => {
                setShowNewGroupForm(false);
                setNewGroupName('');
              }}
            />
          )}

          <div className="groups-list">
            {groups.map((group, index) => (
              <ClusterGroupComponent
                key={group.id}
                group={group}
                index={index}
                totalGroups={groups.length}
                isExpanded={expandedGroups.has(group.name)}
                isDragOver={dragOverGroup === group.name}
                clusterCount={clustersByGroup[group.name]?.length || 0}
                editingGroup={editingGroup}
                editGroupName={editGroupName}
                onToggle={() => toggleGroup(group.name)}
                onMoveUp={() => handleMoveGroup(group.id, 'up')}
                onMoveDown={() => handleMoveGroup(group.id, 'down')}
                onEdit={handleEditGroup}
                onSaveEdit={handleSaveGroupEdit}
                onCancelEdit={handleCancelEdit}
                onDelete={handleDeleteGroup}
                onEditNameChange={setEditGroupName}
                onDragOver={handleDragOver}
                onDragEnter={(e) => handleDragEnter(e, group.name)}
                onDragLeave={handleDragLeave}
                onDrop={(e) => handleDrop(e, group.id)}
              >
                {clustersByGroup[group.name]?.length === 0 ? (
                  <div className="empty-group">Drag clusters here</div>
                ) : (
                  [...(clustersByGroup[group.name] || [])]
                    .sort((a, b) => {
                      const aOpened = openTabIds.includes(a);
                      const bOpened = openTabIds.includes(b);
                      if (aOpened && !bOpened) return -1;
                      if (!aOpened && bOpened) return 1;
                      return 0;
                    })
                    .map((cluster) => renderClusterItem(cluster, true))
                )}
              </ClusterGroupComponent>
            ))}
          </div>
        </div>

        <div
          className={`unassigned-clusters-section ${
            dragOverGroup === 'unassigned' ? 'drag-over' : ''
          }`}
          onDragOver={handleDragOver}
          onDragEnter={(e) => handleDragEnter(e, 'unassigned')}
          onDragLeave={handleDragLeave}
          onDrop={handleUnassignedDrop}
        >
          <div className="section-header">
            <h3>
              Available Clusters
              <span className="unassigned-count">
                {unassignedClusters.length}
              </span>
            </h3>
            <button
              className={`refresh-clusters-btn ${isRefreshing ? 'refreshing' : ''} ${refreshError ? 'error' : ''}`}
              onClick={handleRefresh}
              disabled={isRefreshing || isLoadingClusters}
              title={refreshError || "Refresh kubeconfigs"}
            >
              <ResetIcon />
            </button>
            {refreshError && <span className="refresh-error">{refreshError}</span>}
          </div>
          <input
            type="text"
            className="cluster-search-input"
            placeholder="Search clusters..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          <div className="unassigned-clusters-list">
            {isLoadingClusters ? (
              <div className="no-unassigned">Discovering clusters...</div>
            ) : unassignedClusters.length === 0 ? (
              <div className="no-unassigned">
                {searchQuery ? 'No matching clusters' : 'All clusters are assigned'}
              </div>
            ) : (
              unassignedClusters.map((cluster) =>
                renderClusterItem(cluster, false),
              )
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
