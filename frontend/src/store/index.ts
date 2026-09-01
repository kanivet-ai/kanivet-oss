import { create } from 'zustand';
import { StoreState, BottomTab, MonitoringSettings, ClusterError, ConnectionState, ClusterConnectionState } from './types';
import { loadMonitoringSettings, rebuildTabIndex, createInitialTabState, kindSpecificColumns } from './utils';
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
import { createSsoSlice } from './ssoSlice';
import { createConnectionSlice } from './connectionSlice';
import api from '../services/api';

export type { BottomTab, MonitoringSettings, ClusterError, ConnectionState, ClusterConnectionState };

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
  ...createSsoSlice(...a),
  ...createConnectionSlice(...a),

  monitoringSettings: loadMonitoringSettings(),

  setMonitoringSettings: (settings: Partial<MonitoringSettings>) => {
    const [set, get] = a;
    const current = get().monitoringSettings;
    const updated = { ...current, ...settings };
    set({ monitoringSettings: updated });
    try { localStorage.setItem('kanivet.monitoringSettings', JSON.stringify(updated)); } catch {}
  },

  getDefaultColumns: (resourceKind: string, isNamespaced: boolean = true) => {
    if (!resourceKind) return ['NAME', 'STATUS', 'AGE'];
    const hasNamespace = isNamespaced;
    const baseColumns = ['NAME'];
    if (hasNamespace) baseColumns.push('NAMESPACE');
    const normalizedKind = resourceKind.toLowerCase();
    if (normalizedKind === 'event' || normalizedKind === 'events') {
      return ['TYPE', 'MESSAGE', 'NAMESPACE', 'INVOLVED OBJECT', 'SOURCE', 'COUNT', 'AGE', 'LAST SEEN'];
    }
    const specific = kindSpecificColumns[normalizedKind] || ['STATUS', 'AGE'];
    return [...baseColumns, ...specific];
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
          api.registerActiveCluster(cid);
          api.indexCluster(cid).catch(() => {});
        });
        const shouldUpdateCurrentTab = !get().currentTab;
        set({ activeTabs: newTabs, tabIndexMap: rebuildTabIndex(newTabs), currentTab: shouldUpdateCurrentTab ? (saved || restoredClusters[0] || null) : get().currentTab });
        restoredClusters.forEach((cid) => {
          get().loadClusterStatus(cid);
          get().loadTreeData(cid).catch(() => {});
        });
        restoredClusters.forEach((cid) => {
          try {
            const snapRaw = localStorage.getItem(`kanivet.tabstate.${cid}`);
            if (snapRaw) {
              const snap = JSON.parse(snapRaw);
              set((state) => ({
                activeTabs: state.activeTabs.map((t) => {
                  if (t.id === cid) {
                    const hasExistingTabs = t.state.resourceListTabs && t.state.resourceListTabs.length > 0;
                    if (hasExistingTabs) return t;
                    return {
                      ...t, state: {
                        ...t.state, activeResourceListTab: snap.activeResourceListTab || null, activeResourceListTabByPane: snap.activeResourceListTabByPane || {},
                        resourceListTabs: (snap.resourceListTabs || []).map((rt: any) => ({ ...rt, items: Array.isArray(rt.items) ? rt.items : [], selectedItem: rt.selectedItem || null })),
                        detailTabs: snap.detailTabs || [], activeDetailTab: snap.activeDetailTab || null, focusedCenterPaneId: snap.focusedCenterPaneId || 'root',
                        centerPaneLayout: snap.centerPaneLayout, expandedNodes: new Set(snap.expandedNodes || []), selectedNode: snap.selectedNode || null,
                      },
                    };
                  }
                  return t;
                }),
              }));
            }
          } catch {}
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
    } catch {}
  },
}));

export { useStore };
