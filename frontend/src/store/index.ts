import { create } from 'zustand';
import { StoreState, BottomTab, MonitoringSettings, ClusterError, ConnectionState, ClusterConnectionState } from './types';
import { rebuildTabIndex, createInitialTabState } from './utils';
import { resolveListColumns, PrinterColumnCell } from '../utils/resourceListColumns';
import { createClusterSlice } from './clusterSlice';
import { createTabSlice } from './tabSlice';
import { createResourceSlice } from './resourceSlice';
import { createRealtimeSlice } from './realtimeSlice';
import { createNavigationSlice } from './navigationSlice';
import { createBottomTabSlice } from './bottomTabSlice';
import { createDetailTabSlice } from './detailTabSlice';
import { createResourceListTabSlice } from './resourceListTabSlice';
import { createHelmSlice } from './helmSlice';
import { createToastSlice } from './toastSlice';
import { createCloudAuthSlice } from './cloudAuthSlice';
import { createConnectionSlice } from './connectionSlice';
import { createMonitoringSlice } from './monitoringSlice';
import api from '../services/api';
import {
  TABSTATE_KEY,
  installPersistence,
  markHydrated,
  parseTabSnapshot,
  pruneClusterScopedKeys,
  safeSetItem,
  snapshotToTabState,
  type TabSnapshot,
} from './persistence';
import { restoreBottomTabs, restoreDetailTabs } from './restoreWorkspace';
import { reconnectRestoredVCluster, vclusterGate } from './vclusterRestore';

export type { BottomTab, MonitoringSettings, ClusterError, ConnectionState, ClusterConnectionState };

// Saved tabs whose objects are loaded again the first time their cluster is
// shown, so a launch with many clusters does not query all of them at once.
const pendingContentRestores = new Map<string, TabSnapshot>();

const useStore = create<StoreState>()((...a) => ({
  ...createClusterSlice(...a),
  ...createTabSlice(...a),
  ...createResourceSlice(...a),
  ...createRealtimeSlice(...a),
  ...createNavigationSlice(...a),
  ...createBottomTabSlice(...a),
  ...createDetailTabSlice(...a),
  ...createResourceListTabSlice(...a),
  ...createHelmSlice(...a),
  ...createToastSlice(...a),
  ...createCloudAuthSlice(...a),
  ...createConnectionSlice(...a),

  ...createMonitoringSlice(...a),

  getDefaultColumns: (resourceKind: string, isNamespaced: boolean = true, printerColumns?: PrinterColumnCell[] | null) => {
    return resolveListColumns({ kind: resourceKind, namespaced: isNamespaced, printerColumns });
  },

  restoreWorkspaceContent: (cluster: string) => {
    const [set, get] = a;
    const snap = pendingContentRestores.get(cluster);
    if (!snap) return;
    pendingContentRestores.delete(cluster);
    restoreDetailTabs(cluster, get, set).catch(() => {});
    restoreBottomTabs(cluster, snap.bottomTabs, snap.activeBottomTab, get, set).catch(() => {});
  },

  hydrateFromStorage: () => {
    const [set, get] = a;
    try {
      const saved = localStorage.getItem('kanivet.currentTab');
      const savedClustersRaw = localStorage.getItem('kanivet.activeClusters');
      const restoredClusters: string[] = savedClustersRaw ? JSON.parse(savedClustersRaw) : [];
      const { clusters, activeTabs } = get();

      if (restoredClusters.length > 0) {
        const newTabs = [...activeTabs];
        restoredClusters.forEach((cid) => {
          const exists = newTabs.find((t) => t.id === cid);
          if (!exists) newTabs.push({ id: cid, name: cid, state: createInitialTabState() });
          // A vcluster has no connection after a restart: make it again first.
          reconnectRestoredVCluster(cid);
          api.registerActiveCluster(cid);
          (vclusterGate(cid) ?? Promise.resolve(true))
            .then((ok) => (ok ? api.indexCluster(cid) : undefined))
            .catch(() => {});
        });
        const shouldUpdateCurrentTab = !get().currentTab;
        set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: shouldUpdateCurrentTab ? (saved || restoredClusters[0] || null) : get().currentTab });
        restoredClusters.forEach((cid) => get().loadClusterStatus(cid));
        pruneClusterScopedKeys(restoredClusters);
        restoredClusters.forEach((cid) => {
          const snap = parseTabSnapshot(localStorage.getItem(TABSTATE_KEY(cid)));
          if (!snap) return;
          // Snapshots of earlier releases saved whole objects (a Secret's
          // data, last-applied copies); the parse keeps and the rewrite below
          // stores only what finds them again.
          const restored = snapshotToTabState(snap);
          if (restored.selectedNamespace === undefined) {
            const ns = localStorage.getItem(`kanivet.namespace.${cid}`);
            if (ns) restored.selectedNamespace = ns;
          }
          if (restored.selectedNamespaces === undefined) {
            try {
              const multi = JSON.parse(localStorage.getItem(`kanivet.selectedNamespaces.${cid}`) || 'null');
              if (Array.isArray(multi)) restored.selectedNamespaces = multi;
            } catch {}
          }
          set((state) => ({
            activeTabs: state.activeTabs.map((t) => {
              if (t.id !== cid) return t;
              const hasExistingTabs = t.state.resourceListTabs && t.state.resourceListTabs.length > 0;
              if (hasExistingTabs) return t;
              return { ...t, state: { ...t.state, ...restored } };
            }),
          }));
          pendingContentRestores.set(cid, snap);
          safeSetItem(TABSTATE_KEY(cid), JSON.stringify(snap));
        });
        // After the snapshots: the tree load expands the nodes they restore,
        // and the sidebar's and the restore effect's loads join this one.
        restoredClusters.forEach((cid) => {
          get()
            .loadTreeData(cid)
            .catch(() => {});
        });
      } else if (saved) {
        const exists = activeTabs.find((t) => t.id === saved);
        if (!exists) {
          const newTabState = createInitialTabState();
          const newTabs = [...activeTabs, { id: saved, name: saved, state: newTabState }];
          set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: saved });
          api.registerActiveCluster(saved);
          api.indexCluster(saved).catch(() => {});
          get().loadClusterStatus(saved);
          get().loadTreeData(saved).catch(() => {});
        } else {
          set({ currentTab: saved });
        }
      } else if (activeTabs.length === 0 && clusters.length > 0) {
        const firstCluster = clusters[0];
        const newTabState = createInitialTabState();
        const newTabs = [{ id: firstCluster, name: firstCluster, state: newTabState }];
        set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: firstCluster });
        api.registerActiveCluster(firstCluster);
        api.indexCluster(firstCluster).catch(() => {});
        get().loadClusterStatus(firstCluster);
        setTimeout(async () => {
          await get().loadTreeData(firstCluster);
          const { activeTabs } = get();
          const tab = activeTabs.find((t) => t.id === firstCluster);
          if (!tab) return;
          const workloadsCategory = tab.state.treeData.find((cat) => cat.id === 'workloads');
          if (workloadsCategory) {
            await get().expandNode(firstCluster, 'workloads', 'category', { categoryId: 'workloads' });
            setTimeout(async () => {
              const tabNow = get().activeTabs.find((t) => t.id === firstCluster);
              if (tabNow && tabNow.state.treeData) {
                const workloadsCategoryNow = tabNow.state.treeData.find((cat) => cat.id === 'workloads');
                if (workloadsCategoryNow?.children) {
                  const podsResource = workloadsCategoryNow.children.find((res) => res.label === 'pods');
                  if (podsResource) {
                    await get().openResourceListTab(podsResource.data, firstCluster, true, 'root');
                    get().selectNode(podsResource);
                    await get().loadListItems(firstCluster, podsResource.data);
                    get().setFocusArea('list');
                  }
                }
              }
            }, 100);
          }
        }, 100);
      }
    } catch {
    } finally {
      markHydrated();
    }
  },
}));

installPersistence(useStore);

export { useStore };
