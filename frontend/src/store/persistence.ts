import { persistedItem } from './persistedItem';
import type { BottomTab, Tab, TabState } from './types';

/**
 * One versioned snapshot per cluster tab (`kanivet.tabstate.<cluster>`) holds
 * everything needed to reopen the workspace where it was left. A single writer
 * (installPersistence) produces it, so the shape cannot depend on which code
 * path happened to save last.
 *
 * Version 1 had no `v` field and no bottom tabs, panel state or filters.
 */
export const SNAPSHOT_VERSION = 2;

export const TABSTATE_KEY = (cluster: string) => `kanivet.tabstate.${cluster}`;

/** Keys scoped to one open cluster tab; removed when the tab closes or the
 * cluster is no longer open at startup. */
export const CLUSTER_SCOPED_KEY_PREFIXES = [
  'kanivet.tabstate.',
  'kanivet.lastResource.',
  'kanivet.lastItem.',
  'kanivet.lastDetailTab.',
  'kanivet.namespace.',
  'kanivet.selectedNamespaces.',
];

const THROTTLE_MS = 1000;
/** Bottom tabs whose content can be rebuilt from the object they show. Shell
 * sessions cannot be resumed and a new resource is a draft, so neither is kept. */
const RESTORABLE_BOTTOM_TYPES = new Set<BottomTab['type']>(['logs', 'deployment-logs', 'edit', 'trace']);

export interface PersistedBottomTab {
  id: string;
  type: BottomTab['type'];
  title: string;
  customTitle?: string;
  cluster: string;
  selectedContainer?: string;
  location: 'bottom' | 'center';
  paneId?: string;
  /** What finds the object again; the full object is fetched on restore. */
  ref: { apiVersion?: string; kind?: string; name?: string; namespace?: string };
}

export interface TabSnapshot {
  v: number;
  resourceListTabs: any[];
  activeResourceListTab: string | null;
  activeResourceListTabByPane: Record<string, string | null>;
  detailTabs: any[];
  activeDetailTab: string | null;
  centerPaneLayout?: TabState['centerPaneLayout'];
  focusedCenterPaneId: string | null;
  expandedNodes: string[];
  selectedNode: { id: string; label: string; type: string; data: any } | null;
  searchQuery: string;
  listFilter: string;
  isDetailsPanelCollapsed: boolean;
  sortBy?: string;
  sortOrder?: 'asc' | 'desc';
  selectedNamespace?: string;
  selectedNamespaces?: string[];
  bottomTabs: PersistedBottomTab[];
  activeBottomTab: string | null;
}

let warnedWriteFailure = false;

/** localStorage writes can throw (quota, blocked storage). Saving state must
 * never break the app, but it must not fail silently either. */
export const safeSetItem = (key: string, value: string): boolean => {
  try {
    localStorage.setItem(key, value);
    return true;
  } catch (error) {
    if (!warnedWriteFailure) {
      warnedWriteFailure = true;
      console.warn(`Kanivet could not save UI state (${key}); it will not be restored after a restart.`, error);
    }
    return false;
  }
};

const stripListTab = (rt: any) => {
  if (!rt) return rt;
  // Items are re-fetched on open, and the selected row is kept as a reference.
  const { items: _items, selectedItem, ...rest } = rt;
  return { ...rest, selectedItem: persistedItem(selectedItem) ?? null, items: [] };
};

export const bottomTabRef = (resource: any): PersistedBottomTab['ref'] => {
  const meta = resource?.metadata || {};
  return {
    apiVersion: resource?.apiVersion,
    kind: resource?.kind,
    name: meta.name ?? resource?.name,
    namespace: meta.namespace ?? resource?.namespace,
  };
};

export const restorableBottomTabs = (bottomTabs: BottomTab[], cluster: string): PersistedBottomTab[] =>
  bottomTabs
    .filter((bt) => bt.cluster === cluster && RESTORABLE_BOTTOM_TYPES.has(bt.type) && !bt.resource?._isHelmValues)
    .filter((bt) => !!bottomTabRef(bt.resource).name)
    .map((bt) => ({
      id: bt.id,
      type: bt.type,
      title: bt.title,
      customTitle: bt.customTitle,
      cluster: bt.cluster,
      selectedContainer: bt.selectedContainer,
      location: bt.location,
      paneId: bt.paneId,
      ref: bottomTabRef(bt.resource),
    }));

export const buildTabSnapshot = (
  state: TabState,
  bottomTabs: BottomTab[],
  activeBottomTab: string | null,
  cluster: string,
): TabSnapshot => {
  const bottom = restorableBottomTabs(bottomTabs, cluster);
  return {
    v: SNAPSHOT_VERSION,
    resourceListTabs: (state.resourceListTabs || []).map(stripListTab),
    activeResourceListTab: state.activeResourceListTab ?? null,
    activeResourceListTabByPane: state.activeResourceListTabByPane || {},
    detailTabs: (state.detailTabs || []).map((dt) => ({ ...dt, item: persistedItem(dt.item) })),
    activeDetailTab: state.activeDetailTab ?? null,
    centerPaneLayout: state.centerPaneLayout,
    focusedCenterPaneId: state.focusedCenterPaneId ?? null,
    expandedNodes: Array.from(state.expandedNodes || []),
    selectedNode: state.selectedNode
      ? {
          id: state.selectedNode.id,
          label: state.selectedNode.label,
          type: state.selectedNode.type,
          data: state.selectedNode.data,
        }
      : null,
    searchQuery: state.searchQuery || '',
    listFilter: state.listFilter || '',
    isDetailsPanelCollapsed: !!state.isDetailsPanelCollapsed,
    sortBy: state.sortBy,
    sortOrder: state.sortOrder,
    selectedNamespace: state.selectedNamespace,
    selectedNamespaces: state.selectedNamespaces,
    bottomTabs: bottom,
    activeBottomTab: bottom.some((bt) => bt.id === activeBottomTab) ? activeBottomTab : null,
  };
};

const asArray = (v: unknown): any[] => (Array.isArray(v) ? v : []);
const asString = (v: unknown, fallback = ''): string => (typeof v === 'string' ? v : fallback);

/**
 * Reads a stored snapshot of any version. Anything that is not the shape the
 * app expects is dropped rather than restored, so a corrupt or older file
 * degrades to a fresh workspace instead of an error.
 */
export const parseTabSnapshot = (raw: string | null): TabSnapshot | null => {
  if (!raw) return null;
  let snap: any;
  try {
    snap = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!snap || typeof snap !== 'object' || Array.isArray(snap)) return null;
  // A snapshot from a newer build may use a shape this one cannot read.
  if (typeof snap.v === 'number' && snap.v > SNAPSHOT_VERSION) return null;

  // Version 1 saved whole objects (a Secret's data, last-applied copies):
  // keep only what finds them again.
  const resourceListTabs = asArray(snap.resourceListTabs)
    .filter((rt) => rt && typeof rt === 'object' && typeof rt.id === 'string')
    .map((rt) => ({ ...rt, items: [], selectedItem: persistedItem(rt.selectedItem) ?? null }));
  const detailTabs = asArray(snap.detailTabs)
    .filter((dt) => dt && typeof dt === 'object' && typeof dt.id === 'string')
    .map((dt) => ({ ...dt, item: persistedItem(dt.item) }));
  const bottomTabs = asArray(snap.bottomTabs).filter(
    (bt): bt is PersistedBottomTab =>
      !!bt && typeof bt.id === 'string' && RESTORABLE_BOTTOM_TYPES.has(bt.type) && !!bt.ref && typeof bt.ref.name === 'string',
  );
  const activeBottomTab = typeof snap.activeBottomTab === 'string' && bottomTabs.some((bt) => bt.id === snap.activeBottomTab)
    ? snap.activeBottomTab
    : null;

  return {
    v: SNAPSHOT_VERSION,
    resourceListTabs,
    activeResourceListTab: typeof snap.activeResourceListTab === 'string' ? snap.activeResourceListTab : null,
    activeResourceListTabByPane:
      snap.activeResourceListTabByPane && typeof snap.activeResourceListTabByPane === 'object' ? snap.activeResourceListTabByPane : {},
    detailTabs,
    activeDetailTab: typeof snap.activeDetailTab === 'string' ? snap.activeDetailTab : null,
    centerPaneLayout: snap.centerPaneLayout && typeof snap.centerPaneLayout === 'object' ? snap.centerPaneLayout : undefined,
    focusedCenterPaneId: typeof snap.focusedCenterPaneId === 'string' ? snap.focusedCenterPaneId : 'root',
    expandedNodes: asArray(snap.expandedNodes).filter((n) => typeof n === 'string'),
    selectedNode: snap.selectedNode && typeof snap.selectedNode === 'object' && typeof snap.selectedNode.type === 'string' ? snap.selectedNode : null,
    searchQuery: asString(snap.searchQuery),
    listFilter: asString(snap.listFilter),
    isDetailsPanelCollapsed: snap.isDetailsPanelCollapsed === true,
    sortBy: typeof snap.sortBy === 'string' ? snap.sortBy : undefined,
    sortOrder: snap.sortOrder === 'asc' || snap.sortOrder === 'desc' ? snap.sortOrder : undefined,
    selectedNamespace: typeof snap.selectedNamespace === 'string' ? snap.selectedNamespace : undefined,
    selectedNamespaces: Array.isArray(snap.selectedNamespaces) ? snap.selectedNamespaces.filter((n: unknown) => typeof n === 'string') : undefined,
    bottomTabs,
    activeBottomTab,
  };
};

/** The TabState fields a snapshot restores. */
export const snapshotToTabState = (snap: TabSnapshot): Partial<TabState> => ({
  activeResourceListTab: snap.activeResourceListTab,
  activeResourceListTabByPane: snap.activeResourceListTabByPane,
  resourceListTabs: snap.resourceListTabs,
  detailTabs: snap.detailTabs,
  activeDetailTab: snap.activeDetailTab,
  focusedCenterPaneId: snap.focusedCenterPaneId || 'root',
  centerPaneLayout: snap.centerPaneLayout,
  expandedNodes: new Set(snap.expandedNodes),
  selectedNode: snap.selectedNode as TabState['selectedNode'],
  searchQuery: snap.searchQuery,
  listFilter: snap.listFilter,
  isDetailsPanelCollapsed: snap.isDetailsPanelCollapsed,
  ...(snap.sortBy ? { sortBy: snap.sortBy } : {}),
  ...(snap.sortOrder ? { sortOrder: snap.sortOrder } : {}),
  ...(snap.selectedNamespace !== undefined ? { selectedNamespace: snap.selectedNamespace } : {}),
  ...(snap.selectedNamespaces !== undefined ? { selectedNamespaces: snap.selectedNamespaces } : {}),
});

// Change detection ------------------------------------------------------------
//
// The store updates many times a second while a list streams. A snapshot only
// has to be rewritten when something it holds changed, so changes are found by
// comparing the persisted fields and, for tabs, the references a snapshot keeps
// instead of the live objects (whose identity changes on every refresh).

const sameRef = (a: any, b: any): boolean => {
  if (a === b) return true;
  if (!a || !b) return false;
  const pa = persistedItem(a);
  const pb = persistedItem(b);
  return pa.name === pb.name && pa.namespace === pb.namespace && pa.uid === pb.uid && pa.kind === pb.kind;
};

const sameListTabs = (a: any[] = [], b: any[] = []): boolean =>
  a === b ||
  (a.length === b.length &&
    a.every((x, i) => {
      const y = b[i];
      return (
        x === y ||
        (x.id === y.id &&
          x.title === y.title &&
          x.resource === y.resource &&
          x.selectedNamespaces === y.selectedNamespaces &&
          x.sortBy === y.sortBy &&
          x.sortOrder === y.sortOrder &&
          x.isPinned === y.isPinned &&
          x.paneId === y.paneId &&
          sameRef(x.selectedItem, y.selectedItem))
      );
    }));

const sameDetailTabs = (a: any[] = [], b: any[] = []): boolean =>
  a === b ||
  (a.length === b.length &&
    a.every((x, i) => {
      const y = b[i];
      return (
        x === y ||
        (x.id === y.id &&
          x.title === y.title &&
          x.resource === y.resource &&
          x.isPinned === y.isPinned &&
          x.location === y.location &&
          x.paneId === y.paneId &&
          sameRef(x.item, y.item))
      );
    }));

const sameSelectedNode = (a: any, b: any): boolean => a === b || (!!a && !!b && a.id === b.id && a.type === b.type && a.data === b.data);

const DIRECT_KEYS: Array<keyof TabState> = [
  'activeResourceListTab',
  'activeResourceListTabByPane',
  'activeDetailTab',
  'centerPaneLayout',
  'focusedCenterPaneId',
  'expandedNodes',
  'searchQuery',
  'listFilter',
  'isDetailsPanelCollapsed',
  'sortBy',
  'sortOrder',
  'selectedNamespace',
  'selectedNamespaces',
];

export const tabStateChanged = (prev: TabState, next: TabState): boolean => {
  if (prev === next) return false;
  for (const key of DIRECT_KEYS) {
    if (prev[key] !== next[key]) return true;
  }
  return !(
    sameListTabs(prev.resourceListTabs, next.resourceListTabs) &&
    sameDetailTabs(prev.detailTabs, next.detailTabs) &&
    sameSelectedNode(prev.selectedNode, next.selectedNode)
  );
};

// Writer ---------------------------------------------------------------------

interface PersistenceStore {
  getState: () => { activeTabs: Tab[]; bottomTabs: BottomTab[]; activeBottomTab: string | null };
  subscribe: (listener: (state: any) => void) => () => void;
}

const pending = new Map<string, ReturnType<typeof setTimeout>>();
let hydrated = false;
let installedStore: PersistenceStore | null = null;

const saveCluster = (cluster: string) => {
  pending.delete(cluster);
  if (!installedStore) return;
  const { activeTabs, bottomTabs, activeBottomTab } = installedStore.getState();
  const tab = activeTabs.find((t) => t.id === cluster);
  // A closed tab must not bring its snapshot back.
  if (!tab) return;
  safeSetItem(TABSTATE_KEY(cluster), JSON.stringify(buildTabSnapshot(tab.state, bottomTabs, activeBottomTab, cluster)));
};

/** Throttled, not debounced: a list that streams updates forever would keep a
 * debounce from ever firing. */
const schedule = (cluster: string) => {
  if (pending.has(cluster)) return;
  pending.set(cluster, setTimeout(() => saveCluster(cluster), THROTTLE_MS));
};

export const flushPendingSnapshots = () => {
  for (const [cluster, handle] of [...pending.entries()]) {
    clearTimeout(handle);
    saveCluster(cluster);
  }
};

export const cancelPendingSnapshot = (cluster: string) => {
  const handle = pending.get(cluster);
  if (handle) clearTimeout(handle);
  pending.delete(cluster);
};

/** Saving starts once the stored workspace has been read back, so the empty
 * state a launch begins with can never overwrite it. */
export const markHydrated = () => {
  hydrated = true;
};

export const installPersistence = (store: PersistenceStore, { alreadyHydrated = false } = {}): (() => void) => {
  installedStore = store;
  hydrated = alreadyHydrated;
  let prevTabs = new Map(store.getState().activeTabs.map((t) => [t.id, t.state]));
  let prevBottom = store.getState().bottomTabs;
  let prevActiveBottom = store.getState().activeBottomTab;

  const unsubscribe = store.subscribe((s) => {
    const tabs: Tab[] = s.activeTabs;
    const bottom: BottomTab[] = s.bottomTabs;
    const activeBottom: string | null = s.activeBottomTab;
    const tabsChanged = tabs.some((t) => prevTabs.get(t.id) !== t.state);
    const bottomChanged = bottom !== prevBottom || activeBottom !== prevActiveBottom;
    if (!tabsChanged && !bottomChanged && tabs.length === prevTabs.size) return;

    if (hydrated) {
      for (const tab of tabs) {
        const before = prevTabs.get(tab.id);
        if (!before || tabStateChanged(before, tab.state)) schedule(tab.id);
      }
      if (bottomChanged) {
        const touched = new Set<string>();
        for (const bt of [...bottom, ...prevBottom]) touched.add(bt.cluster);
        for (const cluster of touched) schedule(cluster);
        // Switching the active bottom tab touches the cluster that owns it.
        if (activeBottom !== prevActiveBottom) {
          const owner = bottom.find((bt) => bt.id === activeBottom)?.cluster;
          if (owner) schedule(owner);
        }
      }
    }
    prevTabs = new Map(tabs.map((t) => [t.id, t.state]));
    prevBottom = bottom;
    prevActiveBottom = activeBottom;
  });

  const flush = () => flushPendingSnapshots();
  if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
    // Write what is pending at once on reload / quit, so the last changes before
    // closing are kept.
    window.addEventListener('beforeunload', flush);
    window.addEventListener('pagehide', flush);
  }
  return () => {
    unsubscribe();
    if (typeof window !== 'undefined' && typeof window.removeEventListener === 'function') {
      window.removeEventListener('beforeunload', flush);
      window.removeEventListener('pagehide', flush);
    }
  };
};

/** Drops saved per-cluster state for clusters that are no longer open. */
export const pruneClusterScopedKeys = (openClusters: string[]) => {
  try {
    const open = new Set(openClusters);
    const keys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key) keys.push(key);
    }
    for (const key of keys) {
      const prefix = CLUSTER_SCOPED_KEY_PREFIXES.find((p) => key.startsWith(p));
      if (!prefix) continue;
      if (!open.has(key.slice(prefix.length))) localStorage.removeItem(key);
    }
  } catch {}
};
