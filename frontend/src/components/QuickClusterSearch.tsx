import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import api, { ClusterInfo, ClusterGroup } from '../services/api';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import { parseClusterName } from '../utils/clusterUtils';
import { getClusterStatusPresentation } from '../utils/clusterStatusPresentation';
import { getCachedBatchClusterStatus } from '../services/api/clusters';
import { SearchIcon, KubernetesIcon } from './icons';
import AWSIcon from './AWSIcon';
import GCPIcon from './GCPIcon';
import AzureIcon from './AzureIcon';
import './QuickClusterSearch.css';

const STATUS_REFRESH_INTERVAL_MS = 10000;

interface QuickClusterSearchProps {
  onSelectCluster: (cluster: string) => void;
  onClose: () => void;
}

export const QuickClusterSearch: React.FC<QuickClusterSearchProps> = ({
  onSelectCluster,
  onClose,
}) => {
  const clusterStatuses = useStore((s) => s.clusterStatuses);
  const tabIds = useStore(useShallow((s) => s.activeTabs.map((t) => t.id)));
  const [query, setQuery] = useState('');
  const [clusters, setClusters] = useState<ClusterInfo[]>([]);
  const [aliases, setAliases] = useState<Record<string, string>>({});
  const [groups, setGroups] = useState<ClusterGroup[]>([]);
  const [clustersByGroup, setClustersByGroup] = useState<Record<string, string[]>>({});
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [isLoading, setIsLoading] = useState(true);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const keyNavMousePosRef = useRef<{ x: number; y: number } | null>(null);
  const [mouseEnabled, setMouseEnabled] = useState(true);
  const cachedStatusPollInFlight = useRef(false);

  const clusterToGroup = useMemo(() => {
    const map: Record<string, string> = {};
    groups.forEach((group) => {
      const clustersInGroup = clustersByGroup[group.name] || [];
      clustersInGroup.forEach((clusterName) => {
        map[clusterName] = group.name;
      });
    });
    return map;
  }, [groups, clustersByGroup]);

  useEffect(() => {
    const loadData = async () => {
      const start = performance.now();
      try {
        const timedFetch = async <T,>(name: string, fn: () => Promise<T>): Promise<T> => {
          const s = performance.now();
          const result = await fn();
          console.log(`[QuickSearch] ${name} took ${(performance.now() - s).toFixed(0)}ms`);
          return result;
        };
        const [clustersData, aliasesData, groupsData, clustersByGroupData] = await Promise.all([
          timedFetch('getClusters', () => api.getClusters()),
          timedFetch('getClusterAliases', () => api.getClusterAliases()),
          timedFetch('getClusterGroups', () => api.getClusterGroups()),
          timedFetch('getClustersByGroup', () => api.getClustersByGroup()),
        ]);
        console.log(`[QuickSearch] All API calls took ${(performance.now() - start).toFixed(0)}ms`);
        setClusters(clustersData);
        setAliases(aliasesData);
        setGroups(groupsData);
        setClustersByGroup(clustersByGroupData);
        setIsLoading(false);
      } catch (error) {
        console.error('Failed to load clusters:', error);
        setIsLoading(false);
      }
    };
    loadData();
  }, []);

  useEffect(() => {
    const handleClustersRefreshed = (event: Event) => {
      const detail = (event as CustomEvent<{ clusters: ClusterInfo[] }>).detail;
      if (!detail?.clusters) return;
      setClusters(detail.clusters);
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

  useEffect(() => {
    inputRef.current?.focus();
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;

      e.preventDefault();
      e.stopPropagation();
      e.stopImmediatePropagation();

      if (document.activeElement === inputRef.current) {
        inputRef.current?.blur();
        return;
      }

      onClose();
    };
    document.addEventListener('keydown', handleGlobalKeyDown, true);
    return () => document.removeEventListener('keydown', handleGlobalKeyDown, true);
  }, [onClose]);

  const openedTabIds = useMemo(() => new Set(tabIds), [tabIds]);
  const filteredClusters = useMemo(() => {
    const q = query.toLowerCase();
    return clusters
      .filter((c) => {
        if (!query) return true;
        const nameMatch = c.name.toLowerCase().includes(q);
        const aliasMatch = aliases[c.name]?.toLowerCase().includes(q);
        const groupMatch = clusterToGroup[c.name]?.toLowerCase().includes(q);
        return nameMatch || aliasMatch || groupMatch;
      })
      .sort((a, b) => {
        const aOpened = openedTabIds.has(a.name);
        const bOpened = openedTabIds.has(b.name);
        if (aOpened && !bOpened) return -1;
        if (!aOpened && bOpened) return 1;
        const aOnline = clusterStatuses[a.name]?.healthy === true;
        const bOnline = clusterStatuses[b.name]?.healthy === true;
        if (aOnline && !bOnline) return -1;
        if (!aOnline && bOnline) return 1;
        const aName = aliases[a.name] || a.name;
        const bName = aliases[b.name] || b.name;
        return aName.localeCompare(bName);
      });
  }, [clusters, query, aliases, clusterToGroup, openedTabIds, clusterStatuses]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  useEffect(() => {
    setSelectedIndex((prev) => Math.min(prev, Math.max(filteredClusters.length - 1, 0)));
  }, [filteredClusters.length]);

  useEffect(() => {
    const container = listRef.current, selectedEl = container?.querySelector('.quick-search-item.selected') as HTMLElement | null;
    if (!container || !selectedEl) return;
    const containerRect = container.getBoundingClientRect();
    const selectedRect = selectedEl.getBoundingClientRect();
    if (selectedRect.bottom > containerRect.bottom) {
      container.scrollTop += selectedRect.bottom - containerRect.bottom;
    } else if (selectedRect.top < containerRect.top) {
      container.scrollTop -= containerRect.top - selectedRect.top;
    }
  }, [selectedIndex]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setMouseEnabled(false);
        keyNavMousePosRef.current = null;
        setSelectedIndex((prev) => Math.min(prev + 1, filteredClusters.length - 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setMouseEnabled(false);
        keyNavMousePosRef.current = null;
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (filteredClusters[selectedIndex]) {
          onSelectCluster(filteredClusters[selectedIndex].name);
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        if (document.activeElement === inputRef.current) {
          inputRef.current?.blur();
        } else {
          onClose();
        }
      }
    },
    [filteredClusters, selectedIndex, onSelectCluster, onClose]
  );

  const handleMouseMove = useCallback((e: React.MouseEvent) => {
    if (mouseEnabled) return;
    const pos = keyNavMousePosRef.current;
    if (!pos) {
      keyNavMousePosRef.current = { x: e.clientX, y: e.clientY };
      return;
    }
    const dx = Math.abs(e.clientX - pos.x);
    const dy = Math.abs(e.clientY - pos.y);
    if (dx > 5 || dy > 5) {
      setMouseEnabled(true);
      keyNavMousePosRef.current = null;
    }
  }, [mouseEnabled]);

  const handleMouseEnter = useCallback((index: number) => {
    if (mouseEnabled) {
      setSelectedIndex(index);
    }
  }, [mouseEnabled]);

  const getProviderIcon = (clusterName: string) => {
    const parsed = parseClusterName(clusterName);
    switch (parsed.provider) {
      case 'aws':
        return <AWSIcon size={16} />;
      case 'azure':
        return <AzureIcon size={16} />;
      case 'gcp':
        return <GCPIcon size={16} />;
      default:
        return <KubernetesIcon />;
    }
  };

  const getDisplayName = (cluster: ClusterInfo) => {
    const alias = aliases[cluster.name];
    if (alias) return alias;
    const parsed = parseClusterName(cluster.name);
    return parsed.clusterName;
  };

  return (
    <div className="quick-cluster-search">
      <div className="quick-search-input-wrapper">
        <SearchIcon />
        <input
          ref={inputRef}
          type="text"
          className="quick-search-input"
          placeholder="Search clusters..."
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={handleKeyDown}
        />
        <span className="quick-search-hint">
          <kbd>↑↓</kbd> navigate <kbd>↵</kbd> select <kbd>esc</kbd> close
        </span>
      </div>
      <div className={`quick-search-results${mouseEnabled ? '' : ' keyboard-nav'}`} ref={listRef} onMouseMove={handleMouseMove}>
        {isLoading ? (
          <div className="quick-search-empty">Loading clusters...</div>
        ) : filteredClusters.length === 0 ? (
          <div className="quick-search-empty">
            {query ? 'No matching clusters' : 'No clusters available'}
          </div>
        ) : (
          filteredClusters.map((cluster, index) => {
            const isOpened = openedTabIds.has(cluster.name);
            const parsed = parseClusterName(cluster.name);
            const status = clusterStatuses[cluster.name];
            const statusPresentation = getClusterStatusPresentation(status, isLoading);
            const groupName = clusterToGroup[cluster.name];
            return (
              <div
                key={cluster.name}
                className={`quick-search-item ${index === selectedIndex ? 'selected' : ''} ${isOpened ? 'opened' : ''} ${statusPresentation.isNotReady ? 'not-ready' : ''}`}
                onClick={() => onSelectCluster(cluster.name)}
                onMouseEnter={() => handleMouseEnter(index)}
              >
                <span className={`quick-search-icon ${parsed.provider}`}>
                  {getProviderIcon(cluster.name)}
                </span>
                <div className="quick-search-info">
                  <span className="quick-search-name">{getDisplayName(cluster)}</span>
                  <span className="quick-search-meta">
                    {groupName && <span className="quick-search-group">{groupName}</span>}
                    {parsed.region && <span className="quick-search-region">{parsed.region}</span>}
                  </span>
                </div>
                {isOpened && <span className="quick-search-opened">opened</span>}
                <span className="quick-search-status-container">
                  {status?.responseTimeMs !== undefined && (
                    <span className="quick-search-latency">{status.responseTimeMs}ms</span>
                  )}
                  <span className={`quick-search-status ${statusPresentation.className}`} title={statusPresentation.title} />
                </span>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
};
