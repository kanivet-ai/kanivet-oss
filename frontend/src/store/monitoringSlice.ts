import type { StateCreator } from 'zustand';
import type { MonitoringSettings, StoreState } from './types';
import { loadMonitoringSettings } from './utils';

const CLUSTER_SETTINGS_KEY = 'kanivet.monitoringSettingsByCluster';

function loadClusterSettings(): Record<string, MonitoringSettings> {
  try {
    const saved = JSON.parse(localStorage.getItem(CLUSTER_SETTINGS_KEY) || '{}');
    return saved && typeof saved === 'object' && !Array.isArray(saved) ? saved : {};
  } catch {
    return {};
  }
}

type MonitoringSlice = Pick<StoreState, 'monitoringSettings' | 'monitoringSettingsByCluster' | 'setMonitoringSettings' | 'setClusterMonitoringSettings'>;

export const createMonitoringSlice: StateCreator<StoreState, [], [], MonitoringSlice> = (set, get) => ({
  monitoringSettings: loadMonitoringSettings(),
  monitoringSettingsByCluster: loadClusterSettings(),
  setMonitoringSettings: (settings) => {
    const updated = { ...get().monitoringSettings, ...settings };
    set({ monitoringSettings: updated });
    try { localStorage.setItem('kanivet.monitoringSettings', JSON.stringify(updated)); } catch {}
  },
  setClusterMonitoringSettings: (cluster, settings) => {
    const state = get();
    const current = state.monitoringSettingsByCluster[cluster] || state.monitoringSettings;
    const updated = { ...state.monitoringSettingsByCluster, [cluster]: { ...current, ...settings } };
    localStorage.setItem(CLUSTER_SETTINGS_KEY, JSON.stringify(updated));
    set({ monitoringSettingsByCluster: updated });
  },
});
