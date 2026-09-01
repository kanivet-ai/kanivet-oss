import { useState, useMemo } from 'react';
import clsx from 'clsx';
import { HelmRelease } from '../types/helm';
import { getColumnValue } from '../utils/resourceColumnValues';
import ScrollContainer from './ScrollContainer';
import ResourceControlsBar from './common/ResourceControlsBar';
import { useHelmReleasesStream } from '../hooks/useHelmReleasesStream';
import './ResourceList.css';

interface HelmReleaseListProps {
  cluster: string;
  onSelectRelease: (release: HelmRelease) => void;
}

const HELM_COLUMNS = ['NAME', 'NAMESPACE', 'CHART', 'VERSION', 'STATUS', 'REVISION', 'UPDATED'];

const HelmReleaseList = ({ cluster, onSelectRelease }: HelmReleaseListProps) => {
  const { releases, loading, streaming, progress, error, refresh } = useHelmReleasesStream({
    cluster,
    enabled: true,
  });

  const [searchQuery, setSearchQuery] = useState('');
  const [selectedNamespaces, setSelectedNamespaces] = useState<string[]>([]);
  const [sortBy, setSortBy] = useState<string>('NAMESPACE');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('asc');
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [selectedResources, setSelectedResources] = useState<Set<string>>(new Set());

  // Extract unique namespaces from releases
  const namespaces = useMemo(() => {
    const nsSet = new Set<string>();
    releases.forEach(r => nsSet.add(r.namespace));
    return Array.from(nsSet).sort();
  }, [releases]);

  const handleNamespaceChange = (namespace: string) => {
    if (namespace === 'all') {
      setSelectedNamespaces([]);
    } else {
      setSelectedNamespaces(prev => {
        if (prev.includes(namespace)) {
          return prev.filter(ns => ns !== namespace);
        }
        return [...prev, namespace];
      });
    }
  };

  const filteredReleases = useMemo(() => {
    let result = releases;
    
    // Filter by namespace
    if (selectedNamespaces.length > 0) {
      result = result.filter(r => selectedNamespaces.includes(r.namespace));
    }
    
    // Filter by search query
    if (searchQuery) {
      const query = searchQuery.toLowerCase();
      result = result.filter(
        r => r.name.toLowerCase().includes(query) ||
             r.namespace.toLowerCase().includes(query) ||
             r.chart.toLowerCase().includes(query)
      );
    }
    
    // Sort
    result = [...result].sort((a, b) => {
      let aVal = getColumnValue(a, sortBy, null) || '';
      let bVal = getColumnValue(b, sortBy, null) || '';
      if (typeof aVal === 'object') aVal = a.name;
      if (typeof bVal === 'object') bVal = b.name;
      const cmp = String(aVal).localeCompare(String(bVal));
      return sortOrder === 'asc' ? cmp : -cmp;
    });
    return result;
  }, [releases, selectedNamespaces, searchQuery, sortBy, sortOrder]);

  const handleSort = (column: string) => {
    if (sortBy === column) {
      setSortOrder(o => o === 'asc' ? 'desc' : 'asc');
    } else {
      setSortBy(column);
      setSortOrder('asc');
    }
  };

  const getSortIndicator = (column: string) => {
    if (sortBy !== column) return '';
    return sortOrder === 'asc' ? ' ↑' : ' ↓';
  };

  const getResourceKey = (release: HelmRelease) => `${release.namespace}/${release.name}`;

  const handleRowClick = (release: HelmRelease) => {
    const key = getResourceKey(release);
    setSelectedKey(key);
    onSelectRelease(release);
  };

  const handleCheckboxChange = (release: HelmRelease, e: React.MouseEvent) => {
    e.stopPropagation();
    const key = getResourceKey(release);
    setSelectedResources(prev => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const handleSelectAll = () => {
    if (selectedResources.size === filteredReleases.length) {
      setSelectedResources(new Set());
    } else {
      setSelectedResources(new Set(filteredReleases.map(getResourceKey)));
    }
  };

  const getProgressInfo = () => {
    if (!streaming || !progress) return null;
    const { namespacesCompleted, namespacesTotal } = progress;
    if (namespacesTotal === 0) return { full: 'Starting...', compact: '...', pct: 0 };
    const pct = Math.round((namespacesCompleted / namespacesTotal) * 100);
    return {
      full: `Loading: ${namespacesCompleted}/${namespacesTotal} namespaces (${pct}%)`,
      compact: `${pct}%`,
      pct,
    };
  };

  const getEmptyMessage = () => {
    if (loading && releases.length === 0) return 'Loading resources...';
    if (error) {
      return (
        <div className="error-state">
          <span className="error-message">{error}</span>
          <button className="retry-button" onClick={refresh}>Retry</button>
        </div>
      );
    }
    // If namespace filter is active and there are releases in other namespaces
    if (selectedNamespaces.length > 0 && releases.length > 0 && filteredReleases.length === 0) {
      return (
        <div className="empty-state-with-action">
          <span>No releases in selected namespace</span>
          <button
            className="try-all-namespaces-button"
            onClick={() => handleNamespaceChange('all')}
          >
            Show all namespaces
          </button>
        </div>
      );
    }
    if (searchQuery) return 'No releases match your filter';
    return 'No Helm releases found';
  };

  const progressInfo = getProgressInfo();

  return (
    <div className="resource-list resource-list-content">
      <ResourceControlsBar
        namespaces={namespaces}
        selectedNamespaces={selectedNamespaces}
        onNamespaceChange={handleNamespaceChange}
        itemCount={releases.length}
        filteredCount={filteredReleases.length}
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        resourceKind="HelmRelease"
        showBulkActions={true}
        selectedCount={selectedResources.size}
        onClearSelection={() => setSelectedResources(new Set())}
        customStatus={progressInfo ? (
          <span className="streaming-status" title={progressInfo.full}>
            <span className="streaming-indicator" />
            <span className="streaming-text-full">{progressInfo.full}</span>
            <span className="streaming-text-compact">{progressInfo.compact}</span>
          </span>
        ) : undefined}
      />
      <div className="list-viewport">
        <ScrollContainer className="table-container">
          <table className="resource-table" style={{ width: '100%' }}>
            <thead>
              <tr>
                <th className="checkbox-column">
                  <input
                    type="checkbox"
                    checked={selectedResources.size === filteredReleases.length && filteredReleases.length > 0}
                    onChange={handleSelectAll}
                  />
                </th>
                <th className="actions-column"></th>
                {HELM_COLUMNS.map(column => (
                  <th
                    key={column}
                    className="sortable-column"
                    onClick={() => handleSort(column)}
                  >
                    <div className="th-content">
                      {column}
                      {getSortIndicator(column)}
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(loading && releases.length === 0) || (filteredReleases.length === 0 && !streaming) ? (
                <tr>
                  <td colSpan={HELM_COLUMNS.length + 2} className="empty-message">
                    {getEmptyMessage()}
                  </td>
                </tr>
              ) : (
                filteredReleases.map(release => {
                  const key = getResourceKey(release);
                  const isSelected = selectedKey === key;
                  const isChecked = selectedResources.has(key);
                  return (
                    <tr
                      key={key}
                      className={clsx('resource-row', {
                        'resource-row-selected': isSelected,
                        'resource-row-checked': isChecked,
                      })}
                    >
                      <td className="checkbox-column" onClick={(e) => e.stopPropagation()}>
                        <input
                          type="checkbox"
                          checked={isChecked}
                          onClick={(e) => handleCheckboxChange(release, e)}
                          onChange={() => {}}
                        />
                      </td>
                      <td className="actions-column" onClick={(e) => e.stopPropagation()}>
                        <button
                          className="action-menu-button"
                          title="Actions"
                          onClick={(e) => {
                            e.stopPropagation();
                          }}
                        >
                          ⋮
                        </button>
                      </td>
                      {HELM_COLUMNS.map(column => (
                        <td
                          key={column}
                          className={`column-${column.toLowerCase()}`}
                          onClick={() => handleRowClick(release)}
                          onDoubleClick={() => handleRowClick(release)}
                        >
                          <div className="cell-content">
                            {getColumnValue(release, column, null, undefined, handleNamespaceChange)}
                          </div>
                        </td>
                      ))}
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </ScrollContainer>
      </div>
    </div>
  );
};

export default HelmReleaseList;
