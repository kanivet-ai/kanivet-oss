import api from '../services/api';
import { vclusterGate } from './vclusterRestore';
import type { BottomTab, DetailTab, StoreState } from './types';
import type { PersistedBottomTab } from './persistence';

type Get = () => StoreState;
type Set = (partial: Partial<StoreState> | ((state: StoreState) => Partial<StoreState>)) => void;

const CONCURRENCY = 4;

/** Runs `task` over `items`, `limit` at a time, so reopening a workspace with
 * many tabs does not open a request for each at once. */
const mapLimited = async <T, R>(items: T[], limit: number, task: (item: T) => Promise<R>): Promise<R[]> => {
  const results: R[] = new Array(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await task(items[index]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
};

const splitApiVersion = (apiVersion?: string) => {
  const value = apiVersion || 'v1';
  const slash = value.lastIndexOf('/');
  return slash === -1 ? { group: '', version: value } : { group: value.slice(0, slash), version: value.slice(slash + 1) };
};

const isNotFound = (error: any) => error?.response?.status === 404;

const RETRY_DELAYS_MS = [2000, 5000];

/** Loads an object, retrying a failure that may pass (a cluster still
 * connecting). A 404 is final: the object was deleted while the app was closed. */
const fetchObject = async (load: () => Promise<any>, delays = RETRY_DELAYS_MS): Promise<any> => {
  for (let attempt = 0; ; attempt++) {
    try {
      return await load();
    } catch (error) {
      if (isNotFound(error) || attempt >= delays.length) throw error;
      await new Promise((resolve) => setTimeout(resolve, delays[attempt]));
    }
  }
};

// Kinds whose detail view loads its own data from the reference.
const selfLoadingKinds = new Set(['HelmRelease', 'NatsStream']);

/**
 * A saved detail tab keeps only a reference to its object (a Secret's data
 * must not sit in localStorage). Loads the objects again so every restored tab
 * shows its details, not just the one that was selected.
 */
export const restoreDetailTabs = async (cluster: string, get: Get, set: Set): Promise<void> => {
  const ready = vclusterGate(cluster);
  if (ready && !(await ready)) return;
  const tab = get().activeTabs.find((t) => t.id === cluster);
  if (!tab) return;
  const targets = tab.state.detailTabs.filter((dt: DetailTab) => {
    const item = dt.item;
    const resource = dt.resource;
    return !!item?.name && !!resource?.version && !!resource?.kind && !selfLoadingKinds.has(item.kind) && !selfLoadingKinds.has(resource.kind);
  });
  if (targets.length === 0) return;

  // The tab the user was looking at loads first.
  targets.sort((a, b) => Number(b.id === tab.state.activeDetailTab) - Number(a.id === tab.state.activeDetailTab));

  await mapLimited(targets, CONCURRENCY, async (target) => {
    try {
      const details = await fetchObject(() =>
        api.getResourceDetails(
          cluster,
          target.resource.group || '',
          target.resource.version,
          target.resource.kind,
          target.item.namespace || '',
          target.item.name,
        ),
      );
      if (!details) return;
      set((state) => ({
        activeTabs: state.activeTabs.map((t) => {
          if (t.id !== cluster) return t;
          // Only fill the tab that still shows this object.
          const current = t.state.detailTabs.find((dt) => dt.id === target.id);
          if (!current || current.item?.name !== target.item.name || (current.item?.namespace || '') !== (target.item.namespace || '')) return t;
          // A tab that already holds the full object (opened again meanwhile) is left alone.
          if (current.item?.metadata) return t;
          return {
            ...t,
            state: {
              ...t.state,
              detailTabs: t.state.detailTabs.map((dt) => (dt.id === target.id ? { ...dt, item: details } : dt)),
              detailData: t.state.activeDetailTab === target.id ? details : t.state.detailData,
            },
          };
        }),
      }));
    } catch (error) {
      if (!isNotFound(error)) return;
      set((state) => ({
        activeTabs: state.activeTabs.map((t) =>
          t.id === cluster
            ? { ...t, state: { ...t.state, detailTabs: t.state.detailTabs.map((dt) => (dt.id === target.id ? { ...dt, isDeleted: true } : dt)) } }
            : t,
        ),
      }));
    }
  });
};

/**
 * Reopens the log, edit and trace tabs of the dock. The object each one shows
 * is loaded again; a tab whose object no longer exists is dropped, together
 * with any pane that still pointed at it.
 */
export const restoreBottomTabs = async (
  cluster: string,
  saved: PersistedBottomTab[],
  activeId: string | null,
  get: Get,
  set: Set,
): Promise<void> => {
  const ready = vclusterGate(cluster);
  if (ready && !(await ready)) return;
  const fresh = saved.filter((bt) => !get().bottomTabs.some((existing) => existing.id === bt.id));
  if (fresh.length === 0) return;

  const restored = await mapLimited(fresh, CONCURRENCY, async (bt): Promise<BottomTab | null> => {
    const { ref } = bt;
    let resource: any;
    if (bt.type === 'trace') {
      resource = { apiVersion: ref.apiVersion, kind: ref.kind, metadata: { name: ref.name, namespace: ref.namespace } };
    } else {
      try {
        const { group, version } = splitApiVersion(ref.apiVersion);
        resource = await fetchObject(() => api.getResourceDetails(cluster, group, version, ref.kind || '', ref.namespace || '', ref.name || ''));
      } catch {
        return null;
      }
      if (!resource) return null;
    }
    return {
      id: bt.id,
      type: bt.type,
      title: bt.title,
      customTitle: bt.customTitle,
      resource,
      cluster,
      selectedContainer: bt.selectedContainer,
      location: bt.location,
      paneId: bt.paneId,
    };
  });

  const tabs = restored.filter((bt): bt is BottomTab => !!bt);
  const dropped = new Set(fresh.filter((bt) => !tabs.some((t) => t.id === bt.id)).map((bt) => bt.id));

  set((state) => {
    // The cluster may have been closed while the objects loaded.
    const clusterTab = state.activeTabs.find((t) => t.id === cluster);
    if (!clusterTab) return {};
    const ids = new Set(state.bottomTabs.map((bt) => bt.id));
    const added = tabs.filter((bt) => !ids.has(bt.id));
    const patch: Partial<StoreState> = {};
    if (added.length > 0) {
      patch.bottomTabs = [...state.bottomTabs, ...added];
      if (!state.activeBottomTab && activeId && added.some((bt) => bt.id === activeId)) patch.activeBottomTab = activeId;
    }
    const byPane = clusterTab.state.activeResourceListTabByPane || {};
    if (dropped.size > 0 && Object.values(byPane).some((id) => id && dropped.has(id))) {
      const cleaned = Object.fromEntries(Object.entries(byPane).map(([pane, id]) => [pane, id && dropped.has(id) ? null : id]));
      patch.activeTabs = state.activeTabs.map((t) =>
        t.id === cluster ? { ...t, state: { ...t.state, activeResourceListTabByPane: cleaned } } : t,
      );
    }
    return patch;
  });
};
