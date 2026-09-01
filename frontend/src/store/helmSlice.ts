import { StateCreator } from 'zustand';
import { HelmSlice, StoreState } from './types';

export const createHelmSlice: StateCreator<StoreState, [], [], HelmSlice> = (set, get) => ({
  getHelmReleases: (cluster: string) => {
    const { activeTabs } = get();
    const tab = activeTabs.find((t) => t.id === cluster);
    return tab?.state.helmReleases || [];
  },

  updateHelmReleases: (cluster: string, releases: any[]) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === cluster ? { ...t, state: { ...t.state, helmReleases: releases, helmReleasesLastFetch: Date.now() } } : t
      ),
    }));
  },

  addHelmReleases: (cluster: string, newReleases: any[]) => {
    const { activeTabs } = get();
    const tab = activeTabs.find((t) => t.id === cluster);
    if (!tab) return;
    const existingReleases = tab.state.helmReleases || [];
    const releasesMap = new Map<string, any>();
    const hasFullData = (r: any) => r && r.chart && r.chartVersion && r.status;
    existingReleases.forEach((r: any) => { const key = `${r.namespace}/${r.name}`; releasesMap.set(key, r); });
    newReleases.forEach((r: any) => {
      const key = `${r.namespace}/${r.name}`;
      const existing = releasesMap.get(key);
      if (existing && hasFullData(existing) && !hasFullData(r)) {
        if (r.revision && r.revision > existing.revision) releasesMap.set(key, r);
        return;
      }
      releasesMap.set(key, r);
    });
    const allReleases = Array.from(releasesMap.values());
    allReleases.sort((a, b) => { if (a.namespace !== b.namespace) return a.namespace.localeCompare(b.namespace); return a.name.localeCompare(b.name); });
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === cluster ? { ...t, state: { ...t.state, helmReleases: allReleases, helmReleasesLastFetch: Date.now() } } : t
      ),
    }));
  },

  removeHelmRelease: (cluster: string, namespace: string, name: string) => {
    const { activeTabs } = get();
    const tab = activeTabs.find((t) => t.id === cluster);
    if (!tab) return;
    const existingReleases = tab.state.helmReleases || [];
    const key = `${namespace}/${name}`;
    const filteredReleases = existingReleases.filter((r: any) => `${r.namespace}/${r.name}` !== key);
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, helmReleases: filteredReleases } } : t),
    }));
  },

  setHelmReleasesLoading: (cluster: string, loading: boolean) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, helmReleasesLoading: loading } } : t),
    }));
  },

  setHelmReleasesStreaming: (cluster: string, streaming: boolean) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, helmReleasesStreaming: streaming } } : t),
    }));
  },

  setHelmReleasesProgress: (cluster: string, progress: { namespacesTotal: number; namespacesCompleted: number } | null) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, helmReleasesProgress: progress } } : t),
    }));
  },

  setHelmReleasesError: (cluster: string, error: string | null) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === cluster ? { ...t, state: { ...t.state, helmReleasesError: error } } : t),
    }));
  },

  clearHelmReleases: (cluster: string) => {
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === cluster ? {
          ...t, state: { ...t.state, helmReleases: [], helmReleasesLoading: false, helmReleasesStreaming: false, helmReleasesProgress: null, helmReleasesError: null, helmReleasesLastFetch: undefined },
        } : t
      ),
    }));
  },
});
