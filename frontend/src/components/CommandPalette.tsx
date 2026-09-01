import React, { useState, useEffect, useRef, useCallback } from 'react';
import './CommandPalette.css';
import api from '../services/api';
import { useStore } from '../store';
import type { SearchResult } from '../types/search';
import logger from '../utils/logger';
import { getResourceIcon } from '../utils/resourceIcons';
import {
  kindToResource,
  getResourceCategory,
  kindToResourceDef,
} from '../utils/resourceUtils';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
}

const getShortClusterName = (cluster: string): string => {
  if (cluster.startsWith('arn:aws:eks:')) {
    const match = cluster.match(/cluster\/(.+)$/);
    if (match) return match[1];
  }
  return cluster;
};

const CommandPalette: React.FC<CommandPaletteProps> = ({ isOpen, onClose }) => {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [recentSearches, setRecentSearches] = useState<{ name: string; kind: string; namespace?: string; cluster: string; apiVersion?: string; category?: string }[]>([]);
  const [showRecent, setShowRecent] = useState(true);
  // Filters
  const [clusters, setClusters] = useState<string[]>([]);
  const [selectedClusters, setSelectedClusters] = useState<string[]>([]);
  const [isContextOpen, setIsContextOpen] = useState(false);
  const [categoryFilter, setCategoryFilter] = useState<string>('All');
  const [openActionFor, setOpenActionFor] = useState<string | null>(null);
  const [, setClusterMode] = useState(false);
  const commandList = [
    {
      id: 'switch-cluster',
      name: 'switch cluster',
      description: 'Quickly change the active cluster',
      category: 'Commands',
    },
  ];

  const inputRef = useRef<HTMLInputElement>(null);
  const searchTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const resultsRef = useRef<HTMLDivElement>(null);

  const {
    currentTab,
    loadListItems,
    selectNode,
    setFocusArea,
    recordNavigation,
  } = useStore();

  // Focus input when opened
  useEffect(() => {
    if (isOpen && inputRef.current) {
      inputRef.current.focus();
      loadRecentSearches();
      // Initialize filters
      if (currentTab) setSelectedClusters([currentTab]);
      // Load cluster list for dropdown
      const known = useStore.getState().clusters;
      if (known.length > 0) setClusters(known);
      api
        .getClusters()
        .then((list) => setClusters(list.map((c) => c.name)))
        .catch(() => {
          console.error('Failed to load clusters');
        });
    } else {
      // Reset state when closed
      setQuery('');
      setResults([]);
      setSelectedIndex(0);
      setShowRecent(true);
      setCategoryFilter('All');
      setIsContextOpen(false);
    }
  }, [isOpen, currentTab]);

  const primeClusterSwitch = useCallback(async () => {
    setClusterMode(true);
    setQuery('> switch cluster ');
    setShowRecent(false);
    const known = useStore.getState().clusters;
    if (known.length > 0) {
      setResults(known.map((name) => ({ resource: { id: `cluster:${name}`, name, kind: 'Cluster', cluster: name } })) as any);
      setSelectedIndex(0);
    }
    try {
      const list = await api.getClusters();
      const fakeResults: any[] = list.map((c) => ({
        resource: { id: `cluster:${c.name}`, name: c.name, kind: 'Cluster', cluster: c.name },
      }));
      setResults(fakeResults as any);
      setSelectedIndex(0);
    } catch {
      // Ignore error - history is not critical
    }
  }, []);

  // Cluster switch mode: listen for hint event and prefill prompt
  useEffect(() => {
    const handler = () => {
      if (!isOpen) return;
      primeClusterSwitch();
    };
    window.addEventListener('commandPalette:clusterSwitch', handler);
    return () =>
      window.removeEventListener('commandPalette:clusterSwitch', handler);
  }, [isOpen, primeClusterSwitch]);

  // Close actions menu on outside click
  useEffect(() => {
    const onDocClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target || typeof target.closest !== 'function') return;
      const isMenu = target.closest('[data-action-menu]');
      const isTrigger = target.closest('[data-action-trigger]');
      if (!isMenu && !isTrigger) setOpenActionFor(null);
    };
    window.addEventListener('mousedown', onDocClick);
    return () => window.removeEventListener('mousedown', onDocClick);
  }, []);

  // Load recent searches
  const loadRecentSearches = async () => {
    try {
      const recent = await api.getRecentSearches(5);
      setRecentSearches(recent);
    } catch (error) {
      logger.error('Failed to load recent searches', error);
    }
  };

  // Perform search
  const performSearch = useCallback(
    async (searchQuery: string, overrideClusters?: string[]) => {
      if (!searchQuery.trim()) {
        setResults([]);
        setShowRecent(true);
        return;
      }

      setLoading(true);
      setShowRecent(false);

      try {
        const effectiveClusters = overrideClusters ?? selectedClusters;
        const useClusters =
          effectiveClusters && effectiveClusters.length > 0
            ? effectiveClusters
            : undefined;
        const searchResults = await api.search(searchQuery, {
          clusters: useClusters,
          limit: 50,
        });

        // Prioritize current tab cluster when showing All clusters
        let ordered = searchResults;
        if (!useClusters && currentTab) {
          ordered = [...searchResults].sort((a, b) => {
            const aPri = a.resource.cluster === currentTab ? 0 : 1;
            const bPri = b.resource.cluster === currentTab ? 0 : 1;
            if (aPri !== bPri) return aPri - bPri;
            return a.resource.kind.localeCompare(b.resource.kind);
          });
        }
        setResults(ordered);
        setSelectedIndex(0);
      } catch (error) {
        logger.error('Search failed', error);
        setResults([]);
      } finally {
        setLoading(false);
      }
    },
    [currentTab, selectedClusters],
  );

  // Handle input change with debouncing
  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setQuery(value);

    // Command parsing: "> switch cluster ..."
    const trimmed = value.trimStart();
    const cmdPrefix = /^>\s*switch\s+cluster\s*/i;
    if (cmdPrefix.test(trimmed)) {
      setClusterMode(true);
      const term = trimmed.replace(cmdPrefix, '').toLowerCase();
      const list = clusters.length ? clusters : selectedClusters; // ensure some array
      const filtered = (clusters.length ? clusters : list)
        .filter((c) => c.toLowerCase().includes(term))
        .map((c) => ({
          resource: {
            id: `cluster:${c}`,
            name: c,
            kind: 'Cluster',
            cluster: c,
          },
        }));
      setResults(filtered as any);
      setSelectedIndex(0);
      setShowRecent(false);
      setLoading(false);
      // Do not trigger backend search in command mode
      if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current);
      return;
    }

    // Generic commands list: "> <term>"
    const anyCmdPrefix = /^>\s*/;
    if (anyCmdPrefix.test(trimmed)) {
      setClusterMode(false);
      const term = trimmed.replace(anyCmdPrefix, '').toLowerCase();
      const filtered = commandList
        .filter((c) => c.name.toLowerCase().includes(term))
        .map((c) => ({
          resource: {
            id: `cmd:${c.id}`,
            name: c.name,
            kind: 'Command',
            cluster: '',
            category: c.category,
          },
        }));
      setResults(filtered as any);
      setSelectedIndex(0);
      setShowRecent(false);
      setLoading(false);
      if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current);
      return;
    }

    setClusterMode(false);

    // Clear previous timeout
    if (searchTimeoutRef.current) {
      clearTimeout(searchTimeoutRef.current);
    }
    // Debounce content search (require at least 2 characters)
    if (value.trim().length >= 2) {
      searchTimeoutRef.current = setTimeout(() => {
        performSearch(value);
      }, 100);
    } else {
      setResults([]);
      setShowRecent(true);
      setLoading(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, results.length - 1));
        scrollToSelected();
        break;

      case 'ArrowUp':
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        scrollToSelected();
        break;

      case 'Enter':
        e.preventDefault();
        if (results.length > 0 && selectedIndex >= 0) {
          const sel = results[selectedIndex] as any;
          if (sel?.resource?.kind === 'Cluster' && sel?.resource?.name) {
            const { openTab, setCurrentTab } = useStore.getState();
            openTab(sel.resource.name);
            setCurrentTab(sel.resource.name);
            onClose();
            return;
          }
          if (sel?.resource?.kind === 'Command') {
            const id = String(sel.resource.id || '').replace(/^cmd:/, '');
            if (id === 'switch-cluster') {
              primeClusterSwitch();
              return;
            }
          }
          handleResultSelect(results[selectedIndex]);
        }
        break;

      case 'Escape':
        e.preventDefault();
        onClose();
        break;
    }
  };

  // Context dropdown logic
  const toggleContext = () => setIsContextOpen((prev) => !prev);
  const isClusterSelected = (c: string) => selectedClusters.includes(c);
  const handleClusterToggle = async (c: string) => {
    let next: string[];
    if (isClusterSelected(c)) {
      next = selectedClusters.filter((x) => x !== c);
    } else {
      next = [...selectedClusters, c];
    }
    setSelectedClusters(next);
    if (query.trim()) {
      await performSearch(query, next);
    }
  };
  const handleAllClusters = async () => {
    setSelectedClusters([]);
    if (query.trim()) {
      await performSearch(query, []);
    }
  };

  // Category tab click
  const handleCategoryTabClick = (category: string) => {
    setCategoryFilter(category);
  };

  // Scroll to selected item
  const scrollToSelected = () => {
    if (resultsRef.current) {
      const selectedElement = resultsRef.current.querySelector('.selected');
      if (selectedElement) {
        selectedElement.scrollIntoView({
          block: 'nearest',
          behavior: 'smooth',
        });
      }
    }
  };

  // Handle kind definition selection
  const handleKindSelect = async (result: SearchResult) => {
    const { resource } = result;
    const targetKind = resource.name; // The actual kind name
    const targetCluster = resource.cluster;

    onClose();

    const { setCurrentTab, openTab, updateCurrentTabState } =
      useStore.getState();
    if (targetCluster !== currentTab) {
      openTab(targetCluster);
      setCurrentTab(targetCluster);
      await new Promise((resolve) => setTimeout(resolve, 100));
    }

    const {
      loadTreeData,
      getCurrentTabState,
      expandNode,
      openResourceListTab,
      selectNode,
      loadListItems,
      recordNavigation,
      setFocusArea,
    } = useStore.getState();

    await loadTreeData(targetCluster);
    await new Promise((resolve) => setTimeout(resolve, 50));

    const cur = getCurrentTabState();
    if (
      cur?.selectedNamespace !== 'all' &&
      cur?.selectedNamespace !== undefined
    ) {
      updateCurrentTabState({
        selectedNamespace: 'all',
        selectedNamespaces: [],
      });
      await new Promise((resolve) => setTimeout(resolve, 50));
    }

    // Navigate to the kind in the tree
    // For custom resources, we need to get the actual plural resource name from labels
    let resourceName = '';

    // Get resource definition info - check if it's a known k8s resource
    const resourceDef = kindToResourceDef(targetKind);
    let group = '';
    let version = 'v1';
    let namespaced = true;

    if (resourceDef) {
      // Known Kubernetes resource - use our plural conversion
      resourceName = kindToResource(targetKind);
      group = resourceDef.group;
      version = resourceDef.version;
      namespaced = resourceDef.namespaced;
    } else {
      // Custom resource - extract info from search result
      // Use group and version from the search result if available
      if (resource.group) {
        group = resource.group;
      } else if (resource.labels && resource.labels['group']) {
        group = resource.labels['group'];
      }

      if (resource.version) {
        version = resource.version;
      } else if (resource.labels && resource.labels['version']) {
        version = resource.labels['version'];
      }

      // Check namespaced flag
      if (resource.labels && resource.labels['namespaced'] === 'false') {
        namespaced = false;
      }

      // The resource.labels should contain the actual plural resource name
      if (resource.labels && resource.labels['resource-name']) {
        resourceName = resource.labels['resource-name'];
      } else {
        // Fallback to simple pluralization
        resourceName = kindToResource(targetKind);
      }
    }

    const categoryId = getResourceCategory(group, resourceName);

    await expandNode(targetCluster, categoryId, 'category', { categoryId });
    await new Promise((resolve) => setTimeout(resolve, 50));

    // For custom resources, we might need to expand the apiVersion level
    if (categoryId === 'crossplane' || categoryId === 'custom') {
      // We don't have group/version info for kind definitions, so we can't expand further
      // User will need to manually expand to find the specific API version
    }

    // Create a tree node for this kind
    const treeNodeId = `${categoryId}-${
      group || 'core'
    }-${version}-${resourceName}`;
    const node = {
      id: treeNodeId,
      label: resourceName,
      type: 'resource' as const,
      data: {
        name: resourceName, // The plural resource name (e.g., "pods", "tenants")
        group: group,
        version: version,
        kind: targetKind, // Backend expects singular Kind and will pluralize it
        namespaced: namespaced,
      },
    };

    selectNode(node as any);
    setFocusArea('list');

    await loadListItems(targetCluster, (node as any).data);
    await openResourceListTab(
      (node as any).data,
      targetCluster,
      true,
      undefined,
    );
    await recordNavigation('resource', treeNodeId, (node as any).data);

    logger.info('Selected kind definition', {
      kind: targetKind,
      cluster: targetCluster,
    });
  };

  // Handle result selection
  const handleResultSelect = async (result: SearchResult) => {
    const { resource } = result;

    // Save clicked resource to history
    api.saveSearchHistory({
      name: resource.name,
      kind: resource.kind,
      namespace: resource.namespace,
      cluster: resource.cluster,
      apiVersion: resource.apiVersion,
      category: resource.category,
    });

    // Handle kind definitions differently
    if (resource.kind === 'KindDefinition') {
      return handleKindSelect(result);
    }

    onClose();

    const { setCurrentTab, openTab, updateCurrentTabState } =
      useStore.getState();
    if (resource.cluster !== currentTab) {
      openTab(resource.cluster);
      setCurrentTab(resource.cluster);
      await new Promise((resolve) => setTimeout(resolve, 100));
    }

    const {
      loadTreeData,
      getCurrentTabState,
      expandNode,
      toggleNodeExpansion,
      openResourceListTab,
      updateResourceListTab,
      selectItem,
      loadDetails,
    } = useStore.getState();

    await loadTreeData(resource.cluster);
    await new Promise((resolve) => setTimeout(resolve, 50));

    const cur = getCurrentTabState();
    if (
      resource.namespace &&
      cur?.selectedNamespace !== 'all' &&
      cur?.selectedNamespace !== undefined
    ) {
      updateCurrentTabState({
        selectedNamespace: 'all',
        selectedNamespaces: [],
      });
      await new Promise((resolve) => setTimeout(resolve, 50));
    }

    const rawKind = String(resource.kind || '');
    const capKind = rawKind
      ? rawKind.charAt(0).toUpperCase() + rawKind.slice(1)
      : '';
    const resourceName = rawKind.toLowerCase().endsWith('s')
      ? rawKind.toLowerCase()
      : kindToResource(capKind);
    const group =
      resource.group ||
      (resource.apiVersion && resource.apiVersion.includes('/')
        ? resource.apiVersion.split('/')[0]
        : '');
    const version =
      resource.version ||
      (resource.apiVersion && resource.apiVersion.includes('/')
        ? resource.apiVersion.split('/')[1]
        : resource.apiVersion || 'v1');
    const categoryId = getResourceCategory(group, resourceName);

    await expandNode(resource.cluster, categoryId, 'category', { categoryId });
    await new Promise((resolve) => setTimeout(resolve, 50));

    if (categoryId === 'crossplane' || categoryId === 'custom') {
      const apiVersionNodeId = `${categoryId}-${group || 'core'}-${version}`;
      toggleNodeExpansion(apiVersionNodeId);
      await new Promise((resolve) => setTimeout(resolve, 30));
    }

    const treeNodeId = `${categoryId}-${
      group || 'core'
    }-${version}-${resourceName}`;
    const nodeData = {
      name: resourceName,
      group,
      version,
      kind: capKind,
      namespaced: !!resource.namespace,
    };
    const node = {
      id: treeNodeId,
      label: resourceName,
      type: 'resource' as const,
      data: nodeData,
    };

    selectNode(node as any);
    setFocusArea('list');

    await loadListItems(resource.cluster, nodeData);
    await openResourceListTab(nodeData, resource.cluster, false, undefined);
    await recordNavigation('resource', treeNodeId, nodeData);

    const findTarget = () => {
      const state = getCurrentTabState();
      const items = state?.listItems || [];
      return items.find(
        (i: any) =>
          i?.name === resource.name &&
          (!resource.namespace || i?.namespace === resource.namespace),
      );
    };

    let targetItem = findTarget();
    const maxWaitMs = 1500;
    const step = 75;
    for (let waited = 0; !targetItem && waited < maxWaitMs; waited += step) {
      await new Promise((r) => setTimeout(r, step));
      targetItem = findTarget();
    }

    if (targetItem) {
      selectItem(targetItem);
      const activeListTabId = getCurrentTabState()?.activeResourceListTab;
      if (activeListTabId)
        updateResourceListTab(activeListTabId, { selectedItem: targetItem });
      const enhancedItem = {
        kind: capKind,
        apiVersion: group ? `${group}/${version}` : version,
        metadata: {
          name: targetItem.name,
          namespace: targetItem.namespace,
          creationTimestamp: targetItem.creationTimestamp,
          labels: targetItem.labels || {},
          annotations: targetItem.annotations || {},
          uid: targetItem.uid,
          resourceVersion: targetItem.resourceVersion,
          ownerReferences: targetItem.ownerReferences || [],
          ...(targetItem.metadata || {}),
        },
        status: {
          phase: targetItem.phase || targetItem.status,
          conditions: targetItem.conditions || [],
          ...(targetItem.status && typeof targetItem.status === 'object' ? targetItem.status : {}),
        },
        spec: targetItem.spec || {},
        ...targetItem,
      };
      const { openDetailTab } = useStore.getState();
      openDetailTab(nodeData, enhancedItem, resource.cluster);
      await loadDetails(resource.cluster, nodeData, targetItem);
      updateCurrentTabState({ isDetailsPanelCollapsed: false });
    } else {
      logger.warn('Target item not found after waiting', {
        name: resource.name,
        namespace: resource.namespace,
        resource: resourceName,
      });
    }

    logger.info('Selected search result', {
      resource: resource.name,
      kind: resource.kind,
    });
  };

  // Handle recent resource selection
  const handleRecentResourceSelect = (resource: { name: string; kind: string; namespace?: string; cluster: string; apiVersion?: string; category?: string }) => {
    const [group, version] = (resource.apiVersion || 'v1').includes('/')
      ? (resource.apiVersion || '').split('/')
      : ['', resource.apiVersion || 'v1'];
    const searchResult: SearchResult = {
      resource: {
        id: `${resource.cluster}/${resource.kind}/${resource.namespace || ''}/${resource.name}`,
        name: resource.name,
        kind: resource.kind,
        namespace: resource.namespace || '',
        cluster: resource.cluster,
        apiVersion: resource.apiVersion || 'v1',
        category: resource.category || '',
        group: group,
        version: version,
        createdAt: '',
        updatedAt: '',
      },
      score: 1,
      matches: [],
    };
    handleResultSelect(searchResult);
  };

  // No pluralization helper needed; backend returns already-plural resource kinds

  // Separate kind definition results from regular resource results
  const kindDefinitionResults = results.filter(
    (r) => r.resource.kind === 'KindDefinition',
  );
  const resourceResults = results.filter(
    (r) => r.resource.kind !== 'KindDefinition',
  );

  // Apply category filter
  let displayResults = resourceResults;
  if (categoryFilter !== 'All') {
    displayResults = displayResults.filter(
      (r) => r.resource.category === categoryFilter,
    );
  }

  // Group regular results by category
  const groupedResults = displayResults.reduce<Record<string, SearchResult[]>>(
    (acc, result) => {
      const groupKey = result.resource.category || 'Other';
      if (!acc[groupKey]) {
        acc[groupKey] = [];
      }
      acc[groupKey].push(result);
      return acc;
    },
    {},
  );

  const categoryOrder = [
    'Workloads',
    'Networking',
    'Configuration',
    'Storage',
    'Security',
    'Autoscaling',
    'Policy',
    'Cluster',
    'Other',
  ];
  const sortedCategories = Object.keys(groupedResults).sort((a, b) => {
    const aIndex = categoryOrder.indexOf(a);
    const bIndex = categoryOrder.indexOf(b);
    return (aIndex === -1 ? 999 : aIndex) - (bIndex === -1 ? 999 : bIndex);
  });

  const availableCategories = Array.from(
    new Set(resourceResults.map((r) => r.resource.category || 'Other')),
  ).sort((a, b) => categoryOrder.indexOf(a) - categoryOrder.indexOf(b));

  if (!isOpen) return null;

  return (
    <div className="command-palette-overlay" onClick={onClose}>
      <div className="command-palette" onClick={(e) => e.stopPropagation()}>
        <div className="command-palette-header">
          <input
            ref={inputRef}
            type="text"
            className="command-palette-input"
            placeholder="Search resources by name, kind, or label..."
            value={query}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            autoComplete="off"
            spellCheck={false}
          />
          {loading && (
            <div className="command-palette-loading">Searching...</div>
          )}
        </div>

        {/* Filters Section */}
        {results.length > 0 && (
          <div className="command-palette-filters">
            <div className="command-palette-filters-row">
              <span className="command-palette-filters-label">Type</span>
              <div className="command-palette-filter-group">
                <button
                  className={`command-palette-filter-chip ${
                    categoryFilter === 'All' ? 'active' : ''
                  }`}
                  onClick={() => handleCategoryTabClick('All')}
                >
                  All
                  <span className="count">{resourceResults.length}</span>
                </button>
                {availableCategories.map((cat) => {
                  const count = resourceResults.filter(
                    (r) => r.resource.category === cat,
                  ).length;
                  return (
                    <button
                      key={cat}
                      className={`command-palette-filter-chip ${
                        categoryFilter === cat ? 'active' : ''
                      }`}
                      onClick={() => handleCategoryTabClick(cat)}
                    >
                      {cat}
                      <span className="count">{count}</span>
                    </button>
                  );
                })}
              </div>
            </div>
            <div className="command-palette-filters-row">
              <span className="command-palette-filters-label">Context</span>
              <div className="command-palette-context-filter">
                <button
                  className={`command-palette-context-button ${
                    isContextOpen ? 'open' : ''
                  }`}
                  onClick={toggleContext}
                >
                  <span>
                    {selectedClusters.length === 0
                      ? 'All clusters'
                      : selectedClusters.length === 1
                        ? selectedClusters[0].length > 20
                          ? selectedClusters[0].substring(0, 20) + '...'
                          : selectedClusters[0]
                        : `${selectedClusters.length} clusters`}
                  </span>
                  <span>{isContextOpen ? '▲' : '▼'}</span>
                </button>
                {isContextOpen && (
                  <div className="command-palette-context-dropdown">
                    <div
                      className="command-palette-context-option"
                      onClick={handleAllClusters}
                    >
                      <input
                        type="checkbox"
                        readOnly
                        checked={selectedClusters.length === 0}
                      />
                      <span>All clusters</span>
                    </div>
                    <div className="command-palette-context-sep" />
                    {clusters.map((c) => (
                      <label
                        key={c}
                        className="command-palette-context-option"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleClusterToggle(c);
                        }}
                      >
                        <input
                          type="checkbox"
                          readOnly
                          checked={isClusterSelected(c)}
                        />
                        <span title={c}>
                          {c.length > 30 ? c.substring(0, 30) + '...' : c}
                        </span>
                      </label>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        <div className="command-palette-results" ref={resultsRef}>
          {showRecent && recentSearches.length > 0 && (
            <div className="command-palette-section">
              <div className="command-palette-section-header">
                Recent
              </div>
              {recentSearches.map((resource, index) => (
                <div
                  key={index}
                  className="command-palette-recent-item"
                  onClick={() => handleRecentResourceSelect(resource)}
                >
                  <span className="command-palette-icon">{getResourceIcon(resource.kind)}</span>
                  <div className="command-palette-result-content">
                    <div className="command-palette-result-name">{resource.name}</div>
                    <div className="command-palette-result-meta">
                      <span className="command-palette-kind">{resource.kind}</span>
                      {resource.namespace && (
                        <>
                          <span className="command-palette-separator">•</span>
                          <span className="command-palette-namespace">{resource.namespace}</span>
                        </>
                      )}
                      <span className="command-palette-separator">•</span>
                      <span className="command-palette-cluster">{getShortClusterName(resource.cluster)}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}

          {!showRecent && results.length === 0 && !loading && query && (
            <div className="command-palette-no-results">
              No results found for &quot;{query}&quot;
            </div>
          )}

          {!showRecent && results.length > 0 && (
            <>
              {/* Show kind definitions first if any */}
              {kindDefinitionResults.length > 0 && (
                <div className="command-palette-section command-palette-kinds-section">
                  <div className="command-palette-section-header">
                    Resource Kinds
                  </div>
                  {kindDefinitionResults.map((result: SearchResult) => {
                    const globalIndex = results.indexOf(result);
                    const isSelected = globalIndex === selectedIndex;
                    const resource = result.resource;
                    const actualKind = resource.name; // The actual kind name is stored in name field

                    return (
                      <div
                        key={resource.id}
                        className={`command-palette-result ${
                          isSelected ? 'selected' : ''
                        }`}
                        onMouseEnter={() => setSelectedIndex(globalIndex)}
                        onClick={() => handleResultSelect(result)}
                      >
                        <span className="command-palette-icon">
                          {getResourceIcon(actualKind)}
                        </span>
                        <div className="command-palette-result-content">
                          <div className="command-palette-result-name">
                            {actualKind}
                          </div>
                        </div>
                        <div className="command-palette-result-right">
                          <span className="command-palette-api-version">
                            {resource.group ? `${resource.group}/${resource.version}` : resource.version}
                          </span>
                          <span className="command-palette-cluster">
                            {getShortClusterName(resource.cluster)}
                          </span>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Show regular resource results */}
              {sortedCategories.map((category) => (
                <div key={category} className="command-palette-section">
                  <div className="command-palette-section-header">
                    {category}
                  </div>
                  {groupedResults[category].map((result: SearchResult) => {
                    const globalIndex = results.indexOf(result);
                    const isSelected = globalIndex === selectedIndex;

                    const resource = result.resource;
                    const kind = resource.kind.toLowerCase();

                    const hasActions =
                      kind === 'pods' ||
                      kind === 'services' ||
                      kind === 'deployments' ||
                      kind === 'statefulsets' ||
                      kind === 'daemonsets' ||
                      kind === 'jobs' ||
                      kind === 'replicasets' ||
                      kind === 'configmaps' ||
                      kind === 'secrets';

                    const handleQuickAction = async (action: string) => {
                      // Navigate to resource first to reuse existing action handlers
                      await handleResultSelect(result);
                      const {
                        getCurrentTabState,
                        restartResource,
                        deleteResources,
                        loadDetails,
                        updateCurrentTabState,
                      } = useStore.getState();
                      const state = getCurrentTabState();
                      const selectedNode = state?.selectedNode;
                      const currentTabId = useStore.getState().currentTab;
                      if (!selectedNode || !currentTabId) return;

                      try {
                        if (action === 'logs' && kind === 'pods') {
                          // Ensure details loaded and switch to logs tab
                          const item = {
                            name: resource.name,
                            namespace: resource.namespace,
                          };
                          await loadDetails(
                            currentTabId,
                            selectedNode.data,
                            item,
                          );
                          updateCurrentTabState({
                            isDetailsPanelCollapsed: false,
                          });
                          const evt = new CustomEvent('detail:setActiveTab', {
                            detail: 'logs',
                          });
                          window.dispatchEvent(evt);
                          return;
                        }
                        if (action === 'exec' && kind === 'pods') {
                          // Open shell tab (similar approach to logs)
                          const item = {
                            name: resource.name,
                            namespace: resource.namespace,
                          };
                          await loadDetails(
                            currentTabId,
                            selectedNode.data,
                            item,
                          );
                          updateCurrentTabState({
                            isDetailsPanelCollapsed: false,
                          });
                          const evt = new CustomEvent('detail:setActiveTab', {
                            detail: 'shell',
                          });
                          window.dispatchEvent(evt);
                          return;
                        }
                        if (
                          action === 'restart' &&
                          (kind === 'deployments' ||
                            kind === 'statefulsets' ||
                            kind === 'daemonsets' ||
                            kind === 'replicasets')
                        ) {
                          await restartResource(
                            currentTabId,
                            selectedNode.data,
                            {
                              name: resource.name,
                              namespace: resource.namespace,
                            },
                          );
                          return;
                        }
                        if (
                          action === 'scale' &&
                          (kind === 'deployments' ||
                            kind === 'statefulsets' ||
                            kind === 'daemonsets' ||
                            kind === 'replicasets')
                        ) {
                          // Dispatch custom event for ResourceList to open scale dialog
                          const evt = new CustomEvent(
                            'resourcelist:openScaleDialog',
                            {
                              detail: {
                                name: resource.name,
                                namespace: resource.namespace,
                              },
                            },
                          );
                          window.dispatchEvent(evt);
                          return;
                        }
                        if (action === 'edit') {
                          // Focus details and switch to edit tab
                          const item = {
                            name: resource.name,
                            namespace: resource.namespace,
                          };
                          await loadDetails(
                            currentTabId,
                            selectedNode.data,
                            item,
                          );
                          updateCurrentTabState({
                            isDetailsPanelCollapsed: false,
                          });
                          const evt = new CustomEvent('detail:setActiveTab', {
                            detail: 'edit',
                          });
                          window.dispatchEvent(evt);
                          return;
                        }
                        if (action === 'delete') {
                          await deleteResources(
                            currentTabId,
                            selectedNode.data,
                            [
                              {
                                name: resource.name,
                                namespace: resource.namespace,
                              },
                            ],
                          );
                          return;
                        }
                      } catch (e) {
                        console.error('Quick action failed', e);
                      }
                    };

                    return (
                      <div
                        key={resource.id}
                        className={`command-palette-result ${
                          isSelected ? 'selected' : ''
                        }`}
                        onMouseEnter={() => setSelectedIndex(globalIndex)}
                      >
                        <span className="command-palette-icon">
                          {getResourceIcon(resource.kind)}
                        </span>
                        <div
                          className="command-palette-result-content"
                          onClick={() => handleResultSelect(result)}
                        >
                          <div className="command-palette-result-name">
                            {resource.name}
                          </div>
                          <div className="command-palette-result-meta">
                            <span className="command-palette-kind">
                              {resource.kind}
                            </span>
                            {resource.namespace && (
                              <>
                                <span className="command-palette-separator">
                                  •
                                </span>
                                <span className="command-palette-namespace">
                                  {resource.namespace}
                                </span>
                              </>
                            )}
                            <span className="command-palette-separator">•</span>
                            <span className="command-palette-cluster">
                              {getShortClusterName(resource.cluster)}
                            </span>
                            {result.score && (
                              <>
                                <span className="command-palette-separator">
                                  •
                                </span>
                                <span className="command-palette-score">
                                  {Math.round(result.score * 10) / 10}
                                </span>
                              </>
                            )}
                          </div>
                          {result.matches && result.matches.length > 0 && (
                            <div className="command-palette-result-matches">
                              {result.matches
                                .slice(0, 3)
                                .map((match: any, idx: number) => (
                                  <span
                                    key={idx}
                                    className="command-palette-match"
                                  >
                                    {match.field}:{' '}
                                    {match.value.length > 30
                                      ? match.value.substring(0, 30) + '...'
                                      : match.value}
                                  </span>
                                ))}
                            </div>
                          )}
                        </div>
                        {hasActions && (
                          <>
                            <button
                              className="actions-trigger"
                              data-action-trigger
                              aria-label="Actions"
                              onClick={(e) => {
                                e.stopPropagation();
                                setOpenActionFor(
                                  openActionFor === resource.id
                                    ? null
                                    : resource.id,
                                );
                              }}
                            >
                              ⋯
                            </button>
                            {openActionFor === resource.id && (
                              <div className="actions-menu" data-action-menu>
                                {kind === 'pods' && (
                                  <>
                                    <div
                                      className="actions-menu-item"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('logs');
                                      }}
                                    >
                                      Logs
                                    </div>
                                    <div
                                      className="actions-menu-item"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('exec');
                                      }}
                                    >
                                      Exec
                                    </div>
                                    <div
                                      className="actions-menu-item destructive"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('delete');
                                      }}
                                    >
                                      Delete
                                    </div>
                                  </>
                                )}
                                {(kind === 'deployments' ||
                                  kind === 'statefulsets' ||
                                  kind === 'daemonsets' ||
                                  kind === 'replicasets') && (
                                  <>
                                    <div
                                      className="actions-menu-item"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('restart');
                                      }}
                                    >
                                      Restart
                                    </div>
                                    <div
                                      className="actions-menu-item"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('scale');
                                      }}
                                    >
                                      Scale
                                    </div>
                                    <div
                                      className="actions-menu-item"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('edit');
                                      }}
                                    >
                                      Edit
                                    </div>
                                    <div
                                      className="actions-menu-item destructive"
                                      onClick={(e) => {
                                        e.stopPropagation();
                                        setOpenActionFor(null);
                                        handleQuickAction('delete');
                                      }}
                                    >
                                      Delete
                                    </div>
                                  </>
                                )}
                                {(kind === 'configmaps' ||
                                  kind === 'secrets' ||
                                  kind === 'services' ||
                                  kind === 'jobs') && (
                                  <div
                                    className="actions-menu-item destructive"
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      setOpenActionFor(null);
                                      handleQuickAction('delete');
                                    }}
                                  >
                                    Delete
                                  </div>
                                )}
                              </div>
                            )}
                          </>
                        )}
                        {/* Hide hint to avoid overlap with actions */}
                      </div>
                    );
                  })}
                </div>
              ))}
            </>
          )}
        </div>

        <div className="command-palette-footer">
          <div className="command-palette-help">
            <span>↑↓ Navigate</span>
            <span>↵ Select</span>
            <span>esc Close</span>
          </div>
        </div>
      </div>
    </div>
  );
};

export default CommandPalette;
