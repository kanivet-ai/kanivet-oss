import { StateCreator } from 'zustand';
import api from '../services/api';
import { runInWindows } from '../utils/batching';
import { ClusterSlice, StoreState, DashboardOverviewData } from './types';

const emptyDashboard = (): DashboardOverviewData => ({
  metrics: [],
  resourceUsage: null,
  podStatus: null,
  nodeStatus: null,
  events: [],
  alerts: [],
  clusterInfo: null,
});

export const createClusterSlice: StateCreator<StoreState, [], [], ClusterSlice> = (set, get) => ({
  clusters: [],
  clusterStatuses: {},
  clusterAliases: {},
  clusterErrors: {},
  vclusterStatuses: {},
  clusterDashboards: {},

  updateClusterDashboard: (cluster, partial) => {
    set((state) => {
      const prev = state.clusterDashboards[cluster] || emptyDashboard();
      return {
        clusterDashboards: {
          ...state.clusterDashboards,
          [cluster]: { ...prev, ...partial, lastUpdated: Date.now() },
        },
      };
    });
  },

  clearClusterDashboard: (cluster) => {
    set((state) => {
      const { [cluster]: _, ...rest } = state.clusterDashboards;
      return { clusterDashboards: rest };
    });
  },

  loadClusters: async () => {
    const clusterInfos = await api.getClusters();
    const clusters = clusterInfos.map((c) => c.name);
    set({ clusters });
    const { activeTabs, currentTab } = get();
    if (!currentTab && activeTabs.length === 0 && clusters.length > 0) {
      get().hydrateFromStorage();
    }
  },

  loadClusterAliases: async () => {
    try {
      const aliases = await api.getClusterAliases();
      set({ clusterAliases: aliases });
    } catch (error) {
      console.error('Failed to load cluster aliases:', error);
    }
  },

  setClusterAlias: async (cluster: string, alias: string) => {
    try {
      await api.setClusterAlias(cluster, alias);
      set((state) => ({ clusterAliases: { ...state.clusterAliases, [cluster]: alias } }));
    } catch (error) {
      console.error('Failed to set cluster alias:', error);
    }
  },

  deleteClusterAlias: async (cluster: string) => {
    try {
      await api.deleteClusterAlias(cluster);
      set((state) => {
        const updated = { ...state.clusterAliases };
        delete updated[cluster];
        return { clusterAliases: updated };
      });
    } catch (error) {
      console.error('Failed to delete cluster alias:', error);
    }
  },

  loadClusterStatus: async (cluster: string, force = false) => {
    try {
      const status = await api.getClusterStatus(cluster, force);
      set((state) => ({ clusterStatuses: { ...state.clusterStatuses, [cluster]: status } }));
    } catch (error: any) {
      console.warn(`Failed to get status for cluster ${cluster}:`, error.message);
      set((state) => ({
        clusterStatuses: {
          ...state.clusterStatuses,
          [cluster]: {
            name: cluster,
            healthy: false,
            error: error.response?.status === 404 ? 'Not found' : 'Unreachable',
            responseTimeMs: 0,
          },
        },
      }));
    }
  },

  loadBatchClusterStatus: async (clusters: string[], force = false) => {
    if (clusters.length === 0) return;
    try {
      const statuses = await api.getBatchClusterStatus(clusters, force);
      set((state) => ({ clusterStatuses: { ...state.clusterStatuses, ...statuses } }));
    } catch (error: any) {
      console.warn('Failed to load batch cluster statuses:', error.message);
    }
  },

  refreshAllClusterStatuses: async () => {
    const clusters = Object.keys(get().clusterStatuses);
    if (clusters.length === 0) return;
    set({ clusterStatuses: {} });
    try {
      await runInWindows(clusters, async (window) => {
        const statuses = await api.getBatchClusterStatus(window, true);
        set((state) => ({ clusterStatuses: { ...state.clusterStatuses, ...statuses } }));
      });
    } catch (error: any) {
      console.warn('Failed to refresh cluster statuses:', error.message);
    }
  },

  setClusterError: (cluster: string, errorCode: string, errorMessage: string, recoverable: boolean, details?: string) => {
    set((state) => ({
      clusterErrors: {
        ...state.clusterErrors,
        [cluster]: { cluster, errorCode, errorMessage, details, recoverable, timestamp: Date.now() },
      },
    }));
  },

  clearClusterError: (cluster: string) => {
    set((state) => {
      const { [cluster]: _, ...rest } = state.clusterErrors;
      return { clusterErrors: rest };
    });
  },

  setVClusterStatus: (cluster: string, state: 'connecting' | 'healthy' | 'reconnecting' | 'failed', detail?: string, generation?: number) => {
    set((s) => ({
      vclusterStatuses: {
        ...(s.vclusterStatuses || {}),
        [cluster]: { cluster, state, detail, generation, updatedAt: Date.now() },
      },
    }));
  },
});
