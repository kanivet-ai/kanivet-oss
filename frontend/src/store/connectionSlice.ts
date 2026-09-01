import { StateCreator } from 'zustand';
import { ConnectionSlice, StoreState, ConnectionState, ClusterConnectionState, ClusterConnection } from './types';
import api from '../services/api';

export const createConnectionSlice: StateCreator<StoreState, [], [], ConnectionSlice> = (set, get) => {
  if (typeof window !== 'undefined') {
    setInterval(() => {
      const ready = api.isReady();
      const { websocketState, backendState } = get();
      if (ready && websocketState !== 'connected') {
        set({ websocketState: 'connected' });
      }
      if (ready && backendState !== 'connected') {
        set({ backendState: 'connected', lastBackendPing: Date.now() });
      }
    }, 3000);

    window.addEventListener('connection:state', ((event: CustomEvent) => {
      const { type, state, timestamp, cluster, error } = event.detail;
      if (type === 'backend') {
        set({ backendState: state as ConnectionState, lastBackendPing: timestamp });
      } else if (type === 'websocket') {
        set({ websocketState: state as ConnectionState });
      } else if (type === 'cluster' && cluster) {
        const current = get().clusterConnections[cluster] || { reconnectAttempts: 0 };
        set((s) => ({
          clusterConnections: {
            ...s.clusterConnections,
            [cluster]: {
              state,
              lastConnected: state === 'connected' ? Date.now() : current.lastConnected,
              lastError: error,
              reconnectAttempts: state === 'connected' ? 0 : current.reconnectAttempts + (state === 'error' ? 1 : 0),
            },
          },
        }));
      }
    }) as EventListener);
  }

  return {
  backendState: 'connecting' as ConnectionState,
  websocketState: 'connecting' as ConnectionState,
  clusterConnections: {} as Record<string, ClusterConnection>,
  lastBackendPing: undefined as number | undefined,
  reconnectCountdown: undefined as number | undefined,

  setBackendState: (state: ConnectionState) => set({ backendState: state }),
  setWebsocketState: (state: ConnectionState) => set({ websocketState: state }),

  setClusterConnection: (cluster: string, state: ClusterConnectionState, error?: string) => {
    set((s) => ({
      clusterConnections: {
        ...s.clusterConnections,
        [cluster]: {
          state,
          lastConnected: state === 'connected' ? Date.now() : s.clusterConnections[cluster]?.lastConnected,
          lastError: error,
          reconnectAttempts: state === 'connected' ? 0 : (s.clusterConnections[cluster]?.reconnectAttempts || 0) + (state === 'error' ? 1 : 0),
        },
      },
    }));
  },

  clearClusterConnection: (cluster: string) => {
    set((s) => {
      const { [cluster]: _, ...rest } = s.clusterConnections;
      return { clusterConnections: rest };
    });
  },

  setLastBackendPing: (timestamp: number) => set({ lastBackendPing: timestamp }),
  setReconnectCountdown: (seconds?: number) => set({ reconnectCountdown: seconds }),

  isFullyConnected: () => {
    const { backendState, websocketState } = get();
    return backendState === 'connected' && websocketState === 'connected';
  },

  getClusterConnectionState: (cluster: string) => {
    return get().clusterConnections[cluster]?.state || 'disconnected';
  },

  getOverallState: () => {
    const { backendState, websocketState } = get();
    if (backendState === 'disconnected' || websocketState === 'disconnected') return 'disconnected';
    if (backendState === 'reconnecting' || websocketState === 'reconnecting') return 'reconnecting';
    if (backendState === 'connecting' || websocketState === 'connecting') return 'connecting';
    return 'connected';
  },
};};
