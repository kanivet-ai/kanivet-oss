import { useEffect, useMemo, useState } from 'react';
import { notifyWelcome } from '../services/islandNotifications';
import TreeSidebar from './TreeSidebar';
import CenterPaneSplitContainer from './CenterPaneSplitContainer';
import DetailView from './DetailView';
import TabBar from './TabBar';
import CommandPalette from './CommandPalette';
import BottomDock from './BottomDock';
import { ThemeSettings } from './ThemeSettings';
import { ComponentLibrary } from './ComponentLibrary';
import ToastContainer from './ToastContainer';
import UpdateBanner from './UpdateBanner';
import ClusterErrorBanner from './ClusterErrorBanner';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import api from '../services/api';
import { ClusterSelectorModal } from './ClusterSelectorModal';
import { getCachedBatchClusterStatus } from '../services/api/clusters';
import { useRegisteredKeyboard } from '../hooks/useRegisteredKeyboard';
import { useCloudAuthSync } from '../hooks/useCloudAuthSync';
import { isAuthErrorCode } from '../utils/clusterAuthErrors';
import {
  createTabSwitchHandlers,
  createFocusNavigationHandlers,
} from '../utils/keyboardShortcuts';
import './Layout.css';

const Layout = () => {
  const {
    loadClusters,
    loadClusterAliases,
    currentTab,
    navigateBack,
    navigateForward,
    setFocusArea,
    setCurrentTab,
    openTab,
    closeTab,
    hydrateFromStorage,
    openBottomTab,
    ssoSessions,
    signInSSO,
  } = useStore(useShallow((s) => ({ loadClusters: s.loadClusters, loadClusterAliases: s.loadClusterAliases, currentTab: s.currentTab, navigateBack: s.navigateBack, navigateForward: s.navigateForward, setFocusArea: s.setFocusArea, setCurrentTab: s.setCurrentTab, openTab: s.openTab, closeTab: s.closeTab, hydrateFromStorage: s.hydrateFromStorage, openBottomTab: s.openBottomTab, ssoSessions: s.ssoSessions, signInSSO: s.signInSSO })));
  const hasClusterError = useStore((s) => Boolean(currentTab && s.clusterErrors[currentTab]));
  const setClusterError = useStore((s) => s.setClusterError);

  useEffect(() => {
    const handleClusterError = (event: Event) => {
      const detail = (event as CustomEvent<{
        cluster: string;
        errorCode: string;
        errorMessage: string;
        details?: string;
        recoverable: boolean;
      }>).detail;
      const vcStatus = useStore.getState().vclusterStatuses?.[detail.cluster];
      if (vcStatus && (vcStatus.state === 'connecting' || vcStatus.state === 'reconnecting')) {
        return;
      }
      setClusterError(detail.cluster, detail.errorCode, detail.errorMessage, detail.recoverable, detail.details);
    };
    window.addEventListener('cluster:error', handleClusterError);
    const handleClusterErrorCleared = (event: Event) => {
      const detail = (event as CustomEvent<{ cluster: string }>).detail;
      if (!detail?.cluster) return;
      useStore.getState().clearClusterError(detail.cluster);
    };
    window.addEventListener('cluster:error-cleared', handleClusterErrorCleared);
    return () => {
      window.removeEventListener('cluster:error', handleClusterError);
      window.removeEventListener('cluster:error-cleared', handleClusterErrorCleared);
    };
  }, [setClusterError]);

  useEffect(() => {
    const handleVClusterStatus = (event: Event) => {
      const detail = (event as CustomEvent<{
        cluster: string;
        state: 'connecting' | 'healthy' | 'reconnecting' | 'failed';
        generation?: number;
        detail?: string;
      }>).detail;
      const { setVClusterStatus, clearClusterError } = useStore.getState();
      setVClusterStatus(detail.cluster, detail.state, detail.detail, detail.generation);
      if (detail.state === 'healthy') {
        clearClusterError(detail.cluster);
      }
    };
    window.addEventListener('vcluster:status', handleVClusterStatus);
    return () => window.removeEventListener('vcluster:status', handleVClusterStatus);
  }, []);
  const [isCommandPaletteOpen, setIsCommandPaletteOpen] = useState(false);
  const [showClusterSelector, setShowClusterSelector] = useState(false);
  const [showThemeSettings, setShowThemeSettings] = useState(false);
  const [showComponentLibrary, setShowComponentLibrary] = useState(false);
  const { focusArea, hasListItems, hasDetailData, hasDetailTabs, isDetailsPanelCollapsed } = useStore(useShallow((s) => {
    const t = s.getCurrentTabState();
    return { focusArea: t?.focusArea || 'tree', hasListItems: (t?.listItems?.length || 0) > 0, hasDetailData: !!t?.detailData, hasDetailTabs: (t?.detailTabs?.length || 0) > 0, isDetailsPanelCollapsed: t?.isDetailsPanelCollapsed || false };
  }));
  const tabIds = useStore(useShallow((s) => s.activeTabs.map((t) => t.id)));
  const tabNames = useStore(useShallow((s) => s.activeTabs.map((t) => t.name)));
  const activeTabs = useMemo(() => tabIds.map((id, i) => ({ id, name: tabNames[i] })), [tabIds, tabNames]);

  useCloudAuthSync();

  useEffect(() => {
    loadClusters().then(() => notifyWelcome(useStore.getState().clusters.length)).catch(() => {});
    loadClusterAliases();
    hydrateFromStorage();
  }, [loadClusters, loadClusterAliases, hydrateFromStorage]);


  useEffect(() => {
    const handleClustersRefreshed = (event: Event) => {
      const detail = (event as CustomEvent<{ clusters: Array<{ name: string }>; reason?: string }>).detail;
      const clusterNames = (detail?.clusters || []).map((cluster) => cluster.name);
      useStore.setState({
        clusters: clusterNames,
        clusterStatuses: {},
      });
      if (clusterNames.length > 0) {
        getCachedBatchClusterStatus(clusterNames)
          .then((statuses) => useStore.setState((state) => ({ clusterStatuses: { ...state.clusterStatuses, ...statuses } })))
          .catch((error) => console.error('Failed to load cached cluster statuses after external refresh:', error));
      }
    };

    window.addEventListener('clusters:refreshed', handleClustersRefreshed);
    return () => window.removeEventListener('clusters:refreshed', handleClustersRefreshed);
  }, []);

  // Auto-open cluster selector when no tabs are open
  useEffect(() => {
    if (activeTabs.length === 0 && !currentTab) {
      const timer = setTimeout(() => setShowClusterSelector(true), 100);
      return () => clearTimeout(timer);
    }
  }, [activeTabs.length, currentTab]);

  useEffect(() => {
    if (!showThemeSettings && !showComponentLibrary) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;

      event.preventDefault();
      event.stopPropagation();

      if (showComponentLibrary) {
        setShowComponentLibrary(false);
        return;
      }

      setShowThemeSettings(false);
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [showThemeSettings, showComponentLibrary]);

  // After hydration, auto-restore resource list and last selected item (and detail tab) if present
  useEffect(() => {
    console.log('Layout: restore effect running, currentTab:', currentTab);
    if (!currentTab) return;
    const ns = localStorage.getItem(`kanivet.namespace.${currentTab}`) || 'all';
    const nsMulti = localStorage.getItem(
      `kanivet.selectedNamespaces.${currentTab}`,
    );
    const resourceJson = localStorage.getItem(
      `kanivet.lastResource.${currentTab}`,
    );
    const itemJson = localStorage.getItem(`kanivet.lastItem.${currentTab}`);
    const lastDetailTab = localStorage.getItem(
      `kanivet.lastDetailTab.${currentTab}`,
    );
    console.log('Layout: checking for saved resource:', resourceJson ? 'found' : 'not found');
    if (!resourceJson) return;
    try {
      const resource = JSON.parse(resourceJson);
      const {
        updateCurrentTabState,
        loadTreeData,
        selectNode,
        loadListItems,
        selectItem,
        loadDetails,
        startRealtime,
      } = useStore.getState();
      const parsedMulti = nsMulti ? JSON.parse(nsMulti) : [];
      updateCurrentTabState({
        selectedNamespace: ns,
        selectedNamespaces: parsedMulti,
      });
      // Ensure categories/tree are present; then select and load items
      loadTreeData(currentTab).then(async () => {
        selectNode({ id: '', label: '', type: 'resource', data: resource });
        await loadListItems(currentTab, resource);
        startRealtime();
        if (itemJson) {
          const savedItem = JSON.parse(itemJson);
          const itemsNow =
            useStore.getState().getCurrentTabState()?.listItems || [];
          const match = itemsNow.find(
            (i: any) =>
              i.name === savedItem.name &&
              (i.namespace || '') === (savedItem.namespace || ''),
          );
          if (match) {
            selectItem(match);
            await loadDetails(currentTab, resource, match);
            if (lastDetailTab) {
              const evt = new CustomEvent('detail:setActiveTab', {
                detail: lastDetailTab,
              });
              window.dispatchEvent(evt);
            }
          }
        }
      });
    } catch {}
  }, [currentTab]);

  useRegisteredKeyboard({
    'meta+k': {
      category: 'global',
      description: 'Command palette',
      handler: (e) => {
        e.preventDefault();
        setIsCommandPaletteOpen(true);
      },
    },
    'meta+shift+p': {
      category: 'global',
      description: 'Command palette (alt)',
      handler: (e) => {
        e.preventDefault();
        setIsCommandPaletteOpen(true);
      },
    },
    's+c': {
      category: 'global',
      description: 'Switch cluster',
      handler: (e) => {
        e.preventDefault();
        setIsCommandPaletteOpen(true);
        setTimeout(() => {
          const evt = new CustomEvent('commandPalette:clusterSwitch');
          window.dispatchEvent(evt);
        }, 0);
      },
    },
    'meta+n': {
      category: 'global',
      description: 'Create new resource',
      handler: (e) => {
        e.preventDefault();
        if (currentTab) {
          openBottomTab(
            'create',
            { kind: 'New', metadata: { name: 'new-resource' } },
            currentTab,
          );
        }
      },
    },
    'meta+,': {
      category: 'global',
      description: 'Theme settings',
      handler: (e) => {
        e.preventDefault();
        setShowThemeSettings(true);
      },
    },
    'ctrl+`': {
      category: 'global',
      description: 'Open terminal',
      handler: (e) => {
        e.preventDefault();
        openBottomTab(
          'shell',
          { kind: 'Terminal', metadata: { name: 'Terminal' } },
          'local',
        );
      },
    },
    'alt+left': {
      category: 'navigation',
      description: 'Navigate back',
      handler: navigateBack,
    },
    'alt+right': {
      category: 'navigation',
      description: 'Navigate forward',
      handler: navigateForward,
    },
    'meta+[': {
      category: 'navigation',
      description: 'Navigate back (Mac)',
      handler: navigateBack,
    },
    'meta+]': {
      category: 'navigation',
      description: 'Navigate forward (Mac)',
      handler: navigateForward,
    },
    'ctrl+o': {
      category: 'navigation',
      description: 'Navigate back (Vim)',
      handler: navigateBack,
    },
    'ctrl+i': {
      category: 'navigation',
      description: 'Navigate forward (Vim)',
      handler: navigateForward,
    },
    'meta+t': {
      category: 'global',
      description: 'Open cluster selector',
      handler: (e) => {
        e.preventDefault();
        setShowClusterSelector(true);
      },
    },
    'meta+w': {
      category: 'global',
      description: 'Close current tab',
      handler: (e) => {
        e.preventDefault();
        if (currentTab) closeTab(currentTab);
      },
    },
    'meta+shift+w': {
      category: 'global',
      description: 'Close all tabs',
      handler: (e) => {
        e.preventDefault();
        activeTabs.forEach((tab) => closeTab(tab.id));
      },
    },
    ...Object.fromEntries(
      Object.entries(createTabSwitchHandlers(activeTabs, setCurrentTab)).map(([key, handler]) => [
        key,
        { category: 'global' as const, description: `Switch to tab ${key.split('+')[1]}`, handler },
      ])
    ),
    ...Object.fromEntries(
      Object.entries(
        createFocusNavigationHandlers(
          focusArea,
          setFocusArea,
          hasListItems,
          hasDetailData,
          isDetailsPanelCollapsed,
          hasDetailTabs,
        )
      ).map(([key, handler]) => [
        key,
        { category: 'navigation' as const, description: `Focus navigation (${key})`, handler },
      ])
    ),
  }, 'layout');

  // Listen for cluster selector events from other components
  useEffect(() => {
    const handleOpenClusterSelector = () => setShowClusterSelector(true);
    const handleOpenCommandPalette = () => setIsCommandPaletteOpen(true);

    window.addEventListener('layout:openClusterSelector', handleOpenClusterSelector);
    window.addEventListener('layout:openCommandPalette', handleOpenCommandPalette);
    return () => {
      window.removeEventListener('layout:openClusterSelector', handleOpenClusterSelector);
      window.removeEventListener('layout:openCommandPalette', handleOpenCommandPalette);
    };
  }, []);

  // Listen for cluster retry events
  useEffect(() => {
    const inFlight = new Set<string>();
    const handleClusterRetry = async (event: Event) => {
      const { cluster } = (event as CustomEvent<{ cluster: string }>).detail;
      if (!cluster || inFlight.has(cluster)) return;
      inFlight.add(cluster);
      console.log('[Layout] Retrying cluster connection, clearing cache for:', cluster);
      const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache;
      if (cacheMap) {
        for (const key of cacheMap.keys()) {
          if (key.startsWith(`items:${cluster}:`)) cacheMap.delete(key);
        }
      }
      const { loadClusterStatus, loadTreeData, loadListItems, getCurrentTabState, stopRealtime, startRealtime } = useStore.getState();
      stopRealtime();
      let healthy = false;
      try {
        await api.refreshClusters();
        await loadClusterStatus(cluster, true);
        healthy = Boolean(useStore.getState().clusterStatuses[cluster]?.healthy);
        if (healthy) {
          await loadTreeData(cluster);
          const state = getCurrentTabState();
          if (state?.selectedNode?.type === 'resource' && state.selectedNode.data) {
            await loadListItems(cluster, state.selectedNode.data);
            startRealtime();
          }
        }
      } finally {
        inFlight.delete(cluster);
        window.dispatchEvent(new CustomEvent('cluster:retry-done', { detail: { cluster, healthy } }));
      }
    };
    window.addEventListener('cluster:retry', handleClusterRetry);
    return () => window.removeEventListener('cluster:retry', handleClusterRetry);
  }, []);

  // When credentials change (a sign-in here, a silent token refresh, or a
  // terminal `aws sso login` / `az login` / `gcloud auth login`), retry every
  // cluster that is failing for an auth reason. This never prompts; it only
  // reconnects clusters whose credentials just became valid.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const handleAuthChanged = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(async () => {
        const { clusterErrors, currentTab: activeCluster, loadClusterStatus, clearClusterError } = useStore.getState();
        const failing = Object.values(clusterErrors)
          .filter((e) => isAuthErrorCode(e.errorCode))
          .map((e) => e.cluster);
        if (failing.length === 0) return;
        console.log('[Layout] Cloud credentials changed; retrying clusters with auth errors:', failing);
        try {
          await api.refreshClusters();
        } catch {
          /* retried per cluster below */
        }
        for (const cluster of failing) {
          if (cluster === activeCluster) {
            window.dispatchEvent(new CustomEvent('cluster:retry', { detail: { cluster } }));
            continue;
          }
          try {
            await loadClusterStatus(cluster, true);
            if (useStore.getState().clusterStatuses[cluster]?.healthy) clearClusterError(cluster);
          } catch {
            /* leave the error in place */
          }
        }
      }, 1200);
    };
    window.addEventListener('cloud:auth-changed', handleAuthChanged);
    return () => {
      window.removeEventListener('cloud:auth-changed', handleAuthChanged);
      if (timer) clearTimeout(timer);
    };
  }, []);

  // Listen for connection restored events to refresh data
  useEffect(() => {
    const handleConnectionRestored = async () => {
      const ct = useStore.getState().currentTab;
      if (!ct) return;
      console.log('[Layout] Connection restored, clearing cache and refreshing data');
      (window as any).__kanivetItemsCache = new Map();
      const { stopRealtime, startRealtime, getCurrentTabState } = useStore.getState();
      const state = getCurrentTabState();
      if (state?.selectedNode?.type === 'resource') {
        stopRealtime();
        startRealtime(true);
      }
    };
    window.addEventListener('connection:restored', handleConnectionRestored);
    return () => window.removeEventListener('connection:restored', handleConnectionRestored);
  }, []);

  // Listen for SSO refresh events to trigger data refresh
  useEffect(() => {
    const handleSsoRefreshed = async () => {
      const ct = useStore.getState().currentTab;
      if (!ct) return;
      console.log('[Layout] SSO session refreshed, clearing cache and refreshing data');
      (window as any).__kanivetItemsCache = new Map();
      const { loadClusterStatus, stopRealtime, startRealtime, getCurrentTabState } = useStore.getState();
      await loadClusterStatus(ct, true);
      const state = getCurrentTabState();
      if (state?.selectedNode?.type === 'resource') {
        stopRealtime();
        startRealtime(true);
      }
    };
    window.addEventListener('sso:refreshed', handleSsoRefreshed);
    return () => window.removeEventListener('sso:refreshed', handleSsoRefreshed);
  }, []);

  // Sync tabs to system tray menu
  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (electronAPI?.tray?.updateTabs) {
      const tabsData = activeTabs.map((tab) => ({ id: tab.id, name: tab.name || tab.id }));
      electronAPI.tray.updateTabs(tabsData);
    }
  }, [activeTabs]);

  // Sync SSO sessions to system tray menu
  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (electronAPI?.tray?.updateSSOSessions) {
      const sessionsData = ssoSessions.map((s) => ({
        startUrl: s.startUrl,
        label: s.label,
        state: s.state,
        refreshable: s.refreshable,
        expiresAt: s.expiresAt,
      }));
      electronAPI.tray.updateSSOSessions(sessionsData);
    }
  }, [ssoSessions]);

  // Listen for tray menu events
  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (!electronAPI?.tray) return;

    const cleanupSwitchTab = electronAPI.tray.onSwitchTab?.((tabId: string) => {
      setCurrentTab(tabId);
    });
    const cleanupOpenSettings = electronAPI.tray.onOpenSettings?.(() => {
      setShowThemeSettings(true);
    });
    const cleanupSignInSSO = electronAPI.tray.onSignInSSO?.((startUrl: string) => {
      const session = ssoSessions.find((s) => s.startUrl === startUrl);
      signInSSO(startUrl, session?.region);
    });
    const cleanupOpenAccounts = electronAPI.tray.onOpenCloudAccounts?.(() => {
      window.dispatchEvent(new CustomEvent('cloud:openAccounts'));
    });

    return () => {
      cleanupSwitchTab?.();
      cleanupOpenSettings?.();
      cleanupSignInSSO?.();
      cleanupOpenAccounts?.();
    };
  }, [setCurrentTab, signInSSO, ssoSessions]);

  return (
    <div className="layout">
      <ToastContainer>
        <UpdateBanner />
      </ToastContainer>
      <TabBar onOpenSettings={() => setShowThemeSettings(true)} />
      <div className="layout-body">
        <div className="main-content">
          <TreeSidebar />
          <div style={{ display: 'flex', flex: 1, overflow: 'hidden', position: 'relative' }}>
            {!currentTab ? (
              <div className="layout-empty">
                <span className="ap-tile layout-empty-tile" aria-hidden="true">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 2.5l8 4.5v9l-8 4.5-8-4.5v-9z" />
                  </svg>
                </span>
                <div className="layout-empty-title">No cluster selected</div>
                <div className="layout-empty-subtitle">
                  Choose a cluster to browse its resources, or press <kbd>⌘T</kbd> to open the cluster picker.
                </div>
                <button
                  className="ap-btn ap-btn--primary"
                  onClick={() => setShowClusterSelector(true)}
                >
                  Open Cluster Selector
                </button>
              </div>
            ) : (
              <div className="cluster-main-view" style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
                <div
                  style={{
                    display: 'flex',
                    flex: 1,
                    overflow: 'hidden',
                    flexDirection: 'column',
                  }}
                >
                  {/* Keyed by cluster: pane ids differ between clusters, so each one
                      gets its own layout instead of inheriting the previous tab's. */}
                  <CenterPaneSplitContainer key={currentTab} tabId={currentTab} />
                  <BottomDock />
                </div>
                {(hasDetailData || hasDetailTabs) && <DetailView />}
              </div>
            )}
            {hasClusterError && currentTab && <ClusterErrorBanner />}
          </div>
        </div>
      </div>
      <CommandPalette
        isOpen={isCommandPaletteOpen}
        onClose={() => setIsCommandPaletteOpen(false)}
      />
      {showClusterSelector && (
        <ClusterSelectorModal
          isOpen={showClusterSelector}
          onClose={() => setShowClusterSelector(false)}
          onSelectCluster={(cluster) => {
            openTab(cluster);
            setShowClusterSelector(false);
          }}
        />
      )}
      {showThemeSettings && (
        <div
          tabIndex={-1}
          onClick={() => setShowThemeSettings(false)}
          onKeyDownCapture={(event) => {
            if (event.key !== 'Escape') return;

            event.preventDefault();
            event.stopPropagation();
            setShowThemeSettings(false);
          }}
          className="ap-overlay"
          style={{ zIndex: 1000 }}
        >
          <div
            onClick={(event) => event.stopPropagation()}
            className="ap-sheet"
            style={{
              padding: '20px',
              width: 'min(640px, 92vw)',
              maxHeight: '80vh',
              overflow: 'auto',
              position: 'relative',
            }}
          >
            <button
              className="ap-icon-btn"
              onClick={() => setShowThemeSettings(false)}
              aria-label="Close settings"
              style={{ position: 'absolute', top: '10px', right: '10px' }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round">
                <path d="M6 6l12 12M18 6L6 18" />
              </svg>
            </button>
            <ThemeSettings onOpenComponentLibrary={() => setShowComponentLibrary(true)} />
          </div>
        </div>
      )}
      {showComponentLibrary && (
        <ComponentLibrary onClose={() => setShowComponentLibrary(false)} />
      )}
    </div>
  );
};

export default Layout;
