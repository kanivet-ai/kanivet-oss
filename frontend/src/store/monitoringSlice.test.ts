import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { create } from 'zustand';
import { createMonitoringSlice } from './monitoringSlice';

const makeStore = () => create<any>()((...args) => createMonitoringSlice(...args));

describe('cluster monitoring preferences', () => {
  beforeEach(() => {
    const data = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => data.set(key, value),
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it('keeps existing defaults and persists changes only for the chosen cluster', () => {
    const defaults = { preferredProvider: 'auto', autoRefreshInterval: 60, showMetricsPanel: true };
    localStorage.setItem('kanivet.monitoringSettings', JSON.stringify(defaults));
    const store = makeStore();
    store.getState().setClusterMonitoringSettings('staging', { preferredProvider: 'mimir' });
    store.getState().setClusterMonitoringSettings('production', { showMetricsPanel: false });
    store.getState().setClusterMonitoringSettings('staging', { autoRefreshInterval: 15 });

    const restored = makeStore().getState();
    expect(restored.monitoringSettings).toEqual(defaults);
    expect(restored.monitoringSettingsByCluster.staging).toEqual({ ...defaults, preferredProvider: 'mimir', autoRefreshInterval: 15 });
    expect(restored.monitoringSettingsByCluster.production).toEqual({ ...defaults, showMetricsPanel: false });
    expect(restored.monitoringSettingsByCluster.other).toBeUndefined();
  });

  it('reports storage failure without applying an unsaved preference', () => {
    const store = makeStore();
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('Storage full'); });
    expect(() => store.getState().setClusterMonitoringSettings('staging', { preferredProvider: 'disabled' })).toThrow('Storage full');
    expect(store.getState().monitoringSettingsByCluster.staging).toBeUndefined();
  });

  it('reads a stored custom-URL choice, which nothing queried, as automatic', () => {
    localStorage.setItem('kanivet.monitoringSettings', JSON.stringify({ preferredProvider: 'custom', autoRefreshInterval: 30, showMetricsPanel: true, customPrometheusUrl: 'http://p:9090' }));
    localStorage.setItem('kanivet.monitoringSettingsByCluster', JSON.stringify({ staging: { preferredProvider: 'custom', autoRefreshInterval: 15, showMetricsPanel: false } }));
    const state = makeStore().getState();
    expect(state.monitoringSettings.preferredProvider).toBe('auto');
    expect(state.monitoringSettings.customPrometheusUrl).toBe('http://p:9090');
    expect(state.monitoringSettingsByCluster.staging).toEqual({ preferredProvider: 'auto', autoRefreshInterval: 15, showMetricsPanel: false });
  });
});
