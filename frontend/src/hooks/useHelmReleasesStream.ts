import { useCallback, useEffect, useRef } from 'react';
import { useShallow } from 'zustand/react/shallow';
import api from '../services/api';
import { useStore } from '../store';
import {
  collectHelmChunk,
  finishHelmSnapshot,
  helmMountPlan,
  newHelmSnapshotCollector,
  pruneHelmReleases,
} from './helmSnapshot';

const NO_RELEASES: any[] = [];

interface HelmStreamProgress {
  namespacesTotal: number;
  namespacesCompleted: number;
}

interface UseHelmReleasesStreamOptions {
  cluster: string;
  enabled?: boolean;
}

interface UseHelmReleasesStreamResult {
  releases: any[];
  loading: boolean;
  streaming: boolean;
  progress: HelmStreamProgress | null;
  error: string | null;
  refresh: () => void;
}

export const useHelmReleasesStream = (
  options: UseHelmReleasesStreamOptions
): UseHelmReleasesStreamResult => {
  const handlerRef = useRef<((raw: any) => void) | null>(null);
  const hasSubscribedRef = useRef(false);
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clusterRef = useRef(options.cluster);
  clusterRef.current = options.cluster;
  const snapshotRef = useRef(newHelmSnapshotCollector());

  // Only the helm fields of the cluster's tab: other writes to the tab (list
  // updates, tree counts) do not re-render the releases page.
  const { releases, loading, streaming, progress, error } = useStore(
    useShallow((state) => {
      const tabState = state.activeTabs.find(
        (t) => t.id === options.cluster,
      )?.state;
      return {
        releases: tabState?.helmReleases || NO_RELEASES,
        loading: tabState?.helmReleasesLoading ?? true,
        streaming: tabState?.helmReleasesStreaming ?? false,
        progress: tabState?.helmReleasesProgress ?? null,
        error: tabState?.helmReleasesError ?? null,
      };
    }),
  );

  // Get actions from store - these are stable references
  const addHelmReleases = useStore((state) => state.addHelmReleases);
  const removeHelmRelease = useStore((state) => state.removeHelmRelease);
  const setHelmReleasesLoading = useStore((state) => state.setHelmReleasesLoading);
  const setHelmReleasesStreaming = useStore((state) => state.setHelmReleasesStreaming);
  const setHelmReleasesProgress = useStore((state) => state.setHelmReleasesProgress);
  const setHelmReleasesError = useStore((state) => state.setHelmReleasesError);
  const clearHelmReleases = useStore((state) => state.clearHelmReleases);
  const updateHelmReleases = useStore((state) => state.updateHelmReleases);

  const subscribe = useCallback((cluster: string) => {
    // Send subscribe message over existing WS
    (api as any).__sendWS({
      type: 'helm',
      payload: {
        action: 'subscribe',
        cluster: cluster,
      },
    });

    const topic = 'helm';
    snapshotRef.current = newHelmSnapshotCollector();
    const handler = (raw: any) => {
      const data = raw?.payload || raw;
      if (!data) return;

      // Check if this message is for our cluster
      if (data.cluster && data.cluster !== clusterRef.current) return;

      // Handle progress updates
      if (data.progress) {
        setHelmReleasesProgress(clusterRef.current, {
          namespacesTotal: data.progress.namespacesTotal,
          namespacesCompleted: data.progress.namespacesCompleted,
        });
      }

      // Handle releases - add incrementally to the store
      const snapshotId = typeof data.snapshotId === 'number' && data.snapshotId > 0 ? data.snapshotId : null;
      if (data.releases && Array.isArray(data.releases)) {
        if (snapshotId !== null && data.releases.length > 0) {
          collectHelmChunk(snapshotRef.current, snapshotId, data.releases);
        }
        if (data.releases.length > 0) addHelmReleases(clusterRef.current, data.releases);
        setHelmReleasesLoading(clusterRef.current, false);
      }

      // Handle real-time events (added, modified, deleted)
      if (data.eventType && data.release) {
        const { eventType, release } = data;
        console.log(`[Helm] Real-time event: ${eventType} ${release.namespace}/${release.name}`, release);

        if (eventType === 'deleted') {
          console.log(`[Helm] Removing release from store: ${release.namespace}/${release.name}`);
          removeHelmRelease(clusterRef.current, release.namespace, release.name);
        } else if (eventType === 'added' || eventType === 'modified') {
          // Add or update the release
          addHelmReleases(clusterRef.current, [release]);
        }
      } else if (data.eventType) {
        console.log(`[Helm] Received event without release data:`, data);
      }

      // Handle completion. A listing's Done makes it the whole list: the
      // releases it did not include were uninstalled meanwhile.
      if (data.done && snapshotId !== null && !data.error) {
        const keep = finishHelmSnapshot(snapshotRef.current, snapshotId);
        if (keep) {
          const tab = useStore.getState().activeTabs.find((t) => t.id === clusterRef.current);
          const current = tab?.state.helmReleases || [];
          const pruned = pruneHelmReleases(current, keep);
          if (pruned !== current) updateHelmReleases(clusterRef.current, pruned);
        }
      }
      if (data.done) {
        setHelmReleasesStreaming(clusterRef.current, false);
        setHelmReleasesLoading(clusterRef.current, false);
      }

      // Handle errors
      if (data.error) {
        setHelmReleasesError(clusterRef.current, data.error);
        setHelmReleasesStreaming(clusterRef.current, false);
        setHelmReleasesLoading(clusterRef.current, false);
      }
    };

    handlerRef.current = handler;

    // Register handler
    (api as any).wsHandlers.set(
      topic,
      (api as any).wsHandlers.get(topic) || new Set()
    );
    (api as any).wsHandlers.get(topic).add(handler);
    hasSubscribedRef.current = true;
  }, [addHelmReleases, removeHelmRelease, updateHelmReleases, setHelmReleasesLoading, setHelmReleasesStreaming, setHelmReleasesProgress, setHelmReleasesError]);

  const unsubscribe = useCallback((cluster: string) => {
    // Send unsubscribe message
    if (hasSubscribedRef.current) {
      (api as any).__sendWS({
        type: 'helm',
        payload: {
          action: 'unsubscribe',
          cluster: cluster,
        },
      });
    }

    // Remove handler
    const topic = 'helm';
    if (handlerRef.current) {
      const set = (api as any).wsHandlers.get(topic);
      if (set) {
        set.delete(handlerRef.current);
        if (set.size === 0) (api as any).wsHandlers.delete(topic);
      }
      handlerRef.current = null;
    }
    hasSubscribedRef.current = false;
  }, []);

  const refresh = useCallback(() => {
    const cluster = clusterRef.current;
    if (!cluster) return;

    unsubscribe(cluster);
    clearHelmReleases(cluster);
    setHelmReleasesLoading(cluster, true);
    setHelmReleasesStreaming(cluster, true);
    setHelmReleasesError(cluster, null);
    setHelmReleasesProgress(cluster, null);

    // Small delay to ensure unsubscribe is processed
    if (refreshTimerRef.current) clearTimeout(refreshTimerRef.current);
    refreshTimerRef.current = setTimeout(() => {
      refreshTimerRef.current = null;
      subscribe(cluster);
    }, 100);
  }, [unsubscribe, subscribe, clearHelmReleases, setHelmReleasesLoading, setHelmReleasesStreaming, setHelmReleasesError, setHelmReleasesProgress]);

  // A refresh still waiting to resubscribe must not outlive the page.
  useEffect(() => () => {
    if (refreshTimerRef.current) {
      clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = null;
    }
  }, []);

  // Initial load effect - runs once when component mounts or cluster changes
  useEffect(() => {
    if (!options.cluster || options.enabled === false) return;

    const cluster = options.cluster;

    // Always subscribe: the last unmount unsubscribed, so cached releases
    // would otherwise never change again. Fresh ones are shown meanwhile,
    // without a loading state, and the snapshot replaces them.
    const currentTab = useStore.getState().activeTabs.find((t) => t.id === cluster);
    const plan = helmMountPlan({
      releases: currentTab?.state.helmReleases,
      lastFetch: currentTab?.state.helmReleasesLastFetch,
    });

    if (plan.showLoading) {
      setHelmReleasesLoading(cluster, true);
      setHelmReleasesStreaming(cluster, true);
      setHelmReleasesProgress(cluster, null);
    } else {
      setHelmReleasesLoading(cluster, false);
    }
    setHelmReleasesError(cluster, null);

    subscribe(cluster);

    return () => {
      unsubscribe(cluster);
      setHelmReleasesStreaming(cluster, false);
    };
  }, [options.cluster, options.enabled, subscribe, unsubscribe, setHelmReleasesLoading, setHelmReleasesStreaming, setHelmReleasesError, setHelmReleasesProgress]);

  return {
    releases,
    loading,
    streaming,
    progress,
    error,
    refresh,
  };
};

export default useHelmReleasesStream;
