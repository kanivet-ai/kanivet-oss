import { StateCreator } from 'zustand';
import api from '../services/api';
import { ListSync, RealtimeSlice, StoreState, TopicHealth } from './types';
import { PAGE_KINDS } from './pageKinds';
import { isRolloutComplete } from './utils';
import { notifyRolloutComplete } from '../services/islandNotifications';
import {
  applyRealtimeEvents,
  findItem,
  itemKey,
  newRealtimeSession,
  pendingListing,
  ApplyResult,
  RealtimeEvent,
  RealtimeSession,
} from './realtimeReducer';

type TopicFailure = NonNullable<TopicHealth['failing']>;

interface RealtimeRuntime {
  topic: string;
  cluster: string;
  // The resource the topic lists, and the order it was asked for in.
  resource: any;
  sort: { by: string; order: 'asc' | 'desc' };
  session: RealtimeSession;
  pendingEvents: RealtimeEvent[];
  batchTimer: number | null;
  batchTimerKind: 'timeout' | 'raf' | null;
  // Flushes the batch if its animation frame never runs (hidden window).
  frameFallback: number | null;
  // Latest reconciled list for this topic. Kept current while the topic is
  // parked so switching back to its tab needs no resync.
  items: any[];
  // True once the stream has delivered a snapshot, so items can be shown as live.
  synced: boolean;
  // The size of the listing the backend is sending, when it gave one.
  listTotal?: { epoch: number; total: number };
  // When the subscription was sent (Date.now()).
  startedAt: number;
  interaction: { last: number; deferredSince: number };
  markInteraction: () => void;
  onEvent: (evt: any) => void;
  // Set while the backend reports it cannot keep this list current.
  failing: TopicFailure | null;
  // Reports the load as failed when no list arrives in time.
  loadTimer: ReturnType<typeof setTimeout> | null;
  // A subscribe sent again after the backend lost the list; backs off.
  resubscribeTimer: ReturnType<typeof setTimeout> | null;
  resubscribeAttempt: number;
}

// Subscriptions of tabs the user switched away from stay open, up to this
// many, so returning to one of them is instant and already up to date.
const MAX_PARKED_TOPICS = 6;
// Parked topics are not on screen; flushing them less often keeps their
// upkeep off the main thread's busy moments.
const PARKED_FLUSH_MS = 500;
// Animation frames stop while the window is hidden or minimized; a frame that
// has not run by then is replaced by a timer so the batch is still flushed.
const FRAME_FALLBACK_MS = 500;
const MAX_CACHED_TOPICS = 12;
// How long a list may take to arrive before the load is reported as failed.
const LOAD_TIMEOUT_MS = 30000;
const LOAD_TIMEOUT_MESSAGE = 'Loading is taking longer than expected. The resource may not be available in this cluster.';
const LIST_FAILED_MESSAGE = 'The list could not be read from the cluster.';
// Asking again for a list the backend lost: quickly at first, then less often.
const RESUBSCRIBE_BASE_MS = 1000;
const RESUBSCRIBE_MAX_MS = 30000;
// Backend error codes after which only a new subscribe brings the list back:
// it lost track of what this client has, or its watch stopped.
const RESUBSCRIBE_CODES = new Set(['desynced', 'watch_stopped']);

// A list that is still arriving is re-sorted and re-rendered whole on every
// flush, so its flushes are spaced out as it grows: the usual window up to
// 6,000 rows, a second from 50,000.
const FLUSH_MS = 120;
const flushWindow = (rt: RealtimeRuntime) =>
  pendingListing(rt.session) ? Math.min(1000, Math.max(FLUSH_MS, rt.items.length / 50)) : FLUSH_MS;

// How much of rt's list has arrived, or null once it is all there. shown is
// what the tab holds now, returned as it is when nothing changed so that the
// tab's state keeps its identity.
const listSyncOf = (rt: RealtimeRuntime, shown?: ListSync | null): ListSync | null => {
  const pending = pendingListing(rt.session);
  if (!pending) return null;
  const total = rt.listTotal?.epoch === pending.epoch ? rt.listTotal.total : undefined;
  if (shown && shown.loaded === pending.loaded && shown.total === total) return shown;
  return total === undefined ? { loaded: pending.loaded } : { loaded: pending.loaded, total };
};

const runtime = () => (window as any).__kanivetRealtime as RealtimeRuntime | undefined;

const parkedRuntimes = (): Map<string, RealtimeRuntime> => {
  if (!(window as any).__kanivetParkedRealtime) (window as any).__kanivetParkedRealtime = new Map();
  return (window as any).__kanivetParkedRealtime;
};

export const currentRealtimeTopic = () => runtime()?.topic;

export const itemsTopic = (cluster: string, resource: any) =>
  `items:${cluster}:${resource?.group || ''}:${resource?.version}:${resource?.name}:`;

// The live list for a topic whose subscription is still open, or undefined when
// nothing current is known. Used to show a tab's up-to-date rows on the same
// render that activates it.
export const liveItemsFor = (topic: string): any[] | undefined => {
  const active = runtime();
  if (active?.topic === topic && active.synced && !active.session.stopped) return active.items;
  const parked = parkedRuntimes().get(topic);
  return parked?.synced && !parked.session.stopped ? parked.items : undefined;
};

const cacheItems = (topic: string, items: any[]) => {
  const cacheMap: Map<string, any[]> =
    (window as any).__kanivetItemsCache || new Map();
  (window as any).__kanivetItemsCache = cacheMap;
  cacheMap.delete(topic);
  cacheMap.set(topic, items);
  while (cacheMap.size > MAX_CACHED_TOPICS)
    cacheMap.delete(cacheMap.keys().next().value as string);
};

const clearTimer = (rt: RealtimeRuntime) => {
  if (rt.frameFallback !== null) {
    clearTimeout(rt.frameFallback);
    rt.frameFallback = null;
  }
  if (rt.batchTimer === null) return;
  if (rt.batchTimerKind === 'raf') cancelAnimationFrame(rt.batchTimer);
  else clearTimeout(rt.batchTimer);
  rt.batchTimer = null;
  rt.batchTimerKind = null;
};

const listenForInteraction = (rt: RealtimeRuntime) => {
  window.addEventListener('scroll', rt.markInteraction, { capture: true, passive: true });
  window.addEventListener('wheel', rt.markInteraction, { capture: true, passive: true });
  window.addEventListener('pointerdown', rt.markInteraction, { capture: true, passive: true });
};

const stopListeningForInteraction = (rt: RealtimeRuntime) => {
  window.removeEventListener('scroll', rt.markInteraction, { capture: true } as any);
  window.removeEventListener('wheel', rt.markInteraction, { capture: true } as any);
  window.removeEventListener('pointerdown', rt.markInteraction, { capture: true } as any);
};

const closeRuntime = (rt: RealtimeRuntime) => {
  rt.session.stopped = true;
  api.unsubscribe(rt.topic, rt.onEvent);
  clearTimer(rt);
  if (rt.loadTimer) clearTimeout(rt.loadTimer);
  if (rt.resubscribeTimer) clearTimeout(rt.resubscribeTimer);
  rt.loadTimer = null;
  rt.resubscribeTimer = null;
  stopListeningForInteraction(rt);
  rt.pendingEvents.length = 0;
};

const subscribe = (rt: RealtimeRuntime) => {
  const res = rt.resource;
  api.subscribeToItems(rt.cluster, res.group || '', res.version, res.name, '', rt.onEvent, rt.sort.by, rt.sort.order);
};

const sameFailure = (a?: TopicFailure | null, b?: TopicFailure | null) =>
  a === b || (!!a && !!b && a.error === b.error && a.errorCode === b.errorCode && a.since === b.since);

const isLiveList = (resource: any) =>
  !!resource?.version && !!resource?.name && !PAGE_KINDS.has(resource?.kind);

// Whether obj, opened in a detail tab for resource, is an object of the listed
// resource type. Detail tabs keep their resource as it was opened (a plural
// name, or 'customresourcedefinitions' for a CRD), so several spellings match.
const lower = (s: any) => String(s || '').toLowerCase();
const isListedType = (listed: any, resource: any, obj: any) => {
  const kind = lower(listed?.kind);
  const plural = lower(listed?.name);
  const group =
    typeof resource?.group === 'string'
      ? resource.group
      : typeof obj.apiVersion === 'string' && obj.apiVersion
        ? obj.apiVersion.includes('/')
          ? obj.apiVersion.split('/')[0]
          : ''
        : undefined;
  if (group !== undefined && group !== (listed?.group || '')) return false;
  if (obj.kind)
    return lower(obj.kind) === kind || lower(obj.kind) === plural;
  return (
    lower(resource?.kind) === kind ||
    lower(resource?.kind) === plural ||
    lower(resource?.name) === plural
  );
};

export const createRealtimeSlice: StateCreator<
  StoreState,
  [],
  [],
  RealtimeSlice
> = (set, get) => {
  // ---- Topic health -------------------------------------------------------

  const writeHealth = (topic: string, next: TopicHealth) => {
    const prev = get().topicHealth?.[topic];
    if (
      prev &&
      prev.live === next.live &&
      prev.lastSyncAt === next.lastSyncAt &&
      sameFailure(prev.failing, next.failing)
    )
      return;
    set((st) => ({ topicHealth: { ...(st.topicHealth || {}), [topic]: next } }));
  };

  // Patches the list state of rt's cluster tab while rt is the list on screen.
  const patchShownList = (rt: RealtimeRuntime, patch: Record<string, any>) => {
    if (runtime() !== rt) return;
    const st = get();
    if (st.currentTab !== rt.cluster) return;
    const state = st.getCurrentTabState();
    const node = state?.selectedNode;
    if (!state || node?.type !== 'resource' || itemsTopic(rt.cluster, node.data) !== rt.topic) return;
    const changed = Object.keys(patch).some((k) => (state as any)[k] !== patch[k]);
    if (changed) st.updateCurrentTabState(patch);
  };

  // A list arrived whole, or its rows are arriving: what is shown is current.
  const markLive = (rt: RealtimeRuntime, complete: boolean, lastSyncAt?: number) => {
    const prev = get().topicHealth?.[rt.topic];
    if (complete) {
      rt.failing = null;
      rt.resubscribeAttempt = 0;
      if (rt.resubscribeTimer) {
        clearTimeout(rt.resubscribeTimer);
        rt.resubscribeTimer = null;
      }
    }
    writeHealth(rt.topic, {
      live: true,
      failing: rt.failing,
      lastSyncAt: complete ? lastSyncAt || Date.now() : prev?.lastSyncAt,
    });
  };

  // The backend cannot keep rt's list current. The rows stay; they are no
  // longer presented as live.
  const markFailing = (rt: RealtimeRuntime, failure: TopicFailure, lastSyncAt?: number) => {
    const prev = get().topicHealth?.[rt.topic];
    // A repeated report of the same failure keeps when it started.
    const since = rt.failing && rt.failing.error === failure.error && rt.failing.errorCode === failure.errorCode
      ? rt.failing.since
      : failure.since;
    rt.failing = { ...failure, since };
    if (rt.loadTimer) {
      clearTimeout(rt.loadTimer);
      rt.loadTimer = null;
    }
    writeHealth(rt.topic, {
      live: false,
      failing: rt.failing,
      lastSyncAt: lastSyncAt || prev?.lastSyncAt,
    });
    if (failure.errorCode && RESUBSCRIBE_CODES.has(failure.errorCode)) scheduleResubscribe(rt);
  };

  // Asks the backend for rt's list again, backing off while it keeps failing.
  const scheduleResubscribe = (rt: RealtimeRuntime) => {
    if (rt.resubscribeTimer || rt.session.stopped) return;
    const delay = Math.min(RESUBSCRIBE_MAX_MS, RESUBSCRIBE_BASE_MS * 2 ** rt.resubscribeAttempt);
    rt.resubscribeAttempt++;
    rt.resubscribeTimer = setTimeout(() => {
      rt.resubscribeTimer = null;
      if (rt.session.stopped) return;
      // A stopped watch is restarted by a fresh subscription; a lost one only
      // needs the snapshot sent again.
      if (rt.failing?.errorCode === 'watch_stopped') api.unsubscribe(rt.topic, rt.onEvent);
      subscribe(rt);
    }, delay);
  };

  // Reports rt's load as failed when no list arrived in time.
  const armLoadTimer = (rt: RealtimeRuntime, ms = LOAD_TIMEOUT_MS) => {
    if (rt.loadTimer) clearTimeout(rt.loadTimer);
    rt.loadTimer = setTimeout(() => {
      rt.loadTimer = null;
      if (rt.session.stopped) return;
      if (runtime() === rt) {
        const currentState = get().getCurrentTabState();
        if (currentState?.isLoadingListItems && !currentState?.hasReceivedInitialListData) {
          console.error('⚠️ Timeout reached while loading:', rt.resource?.name);
          get().updateCurrentTabState({ isLoadingListItems: false, hasReceivedInitialListData: false, loadError: LOAD_TIMEOUT_MESSAGE });
        }
      }
      if (!rt.synced && !rt.failing) {
        markFailing(rt, { error: LOAD_TIMEOUT_MESSAGE, errorCode: 'timeout', since: Date.now() });
      }
    }, ms);
  };

  // A topic_status message: the backend's view of the topic, sent when it
  // changes and repeated while the topic is failing.
  const onTopicStatus = (rt: RealtimeRuntime, event: any) => {
    const lastSyncAt = typeof event.lastSyncAt === 'number' && event.lastSyncAt > 0 ? event.lastSyncAt : undefined;
    if (event.state === 'failing') {
      const failure: TopicFailure = {
        error: event.error || LIST_FAILED_MESSAGE,
        ...(event.errorCode && { errorCode: event.errorCode }),
        since: typeof event.failingSince === 'number' && event.failingSince > 0 ? event.failingSince : Date.now(),
      };
      markFailing(rt, failure, lastSyncAt);
      patchShownList(rt, { isLoadingListItems: false, loadError: failure.error });
      return;
    }
    if (event.state === 'ok') {
      const prev = get().topicHealth?.[rt.topic];
      rt.failing = null;
      rt.resubscribeAttempt = 0;
      writeHealth(rt.topic, { live: rt.synced, failing: null, lastSyncAt: lastSyncAt || prev?.lastSyncAt });
      patchShownList(
        rt,
        rt.synced
          ? { isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined }
          : { loadError: undefined },
      );
    }
  };

  // What a batch says about the list: it arrived ('ok', complete), its rows
  // are arriving ('streamed'), or the backend could not read it ('failed').
  const outcomeOf = (rt: RealtimeRuntime, result: ApplyResult, events: RealtimeEvent[]) => {
    if (result.failure) {
      const failure: TopicFailure = {
        error: result.failure.error || LIST_FAILED_MESSAGE,
        ...(result.failure.errorCode && { errorCode: result.failure.errorCode }),
        since: Date.now(),
      };
      markFailing(rt, failure, result.failure.lastSyncAt || undefined);
      return 'failed' as const;
    }
    if (result.syncSeen) {
      let lastSyncAt: number | undefined;
      for (const ev of events) if (ev.action === 'sync' && !ev.failed && ev.lastSyncAt) lastSyncAt = ev.lastSyncAt;
      rt.synced = true;
      markLive(rt, true, lastSyncAt);
      return 'ok' as const;
    }
    // Rows of the listing this subscription asked for. Rows from a cache or
    // an earlier visit are not: they wait for a sync before they count as live.
    if (result.changed && events.some((ev) => ev.epoch && ev.action !== 'sync')) {
      rt.synced = true;
      markLive(rt, false);
      return 'streamed' as const;
    }
    return null;
  };

  // Open detail tabs of objects a completed listing no longer has: they went
  // while nothing was watching (a reconnect, a parked list), so no deleted
  // event will ever say so.
  const markListingRemovals = (rt: RealtimeRuntime, result: ApplyResult) => {
    if (!result.removed?.length) return;
    const idx = get().tabIndexMap.get(rt.cluster) ?? -1;
    const tab = idx === -1 ? undefined : get().activeTabs[idx];
    if (!tab || tab.state.detailTabs.length === 0) return;
    const gone = new Map<string, any>();
    for (const obj of result.removed) {
      const key = itemKey(obj);
      // Added back later in the same batch.
      if (findItem(rt.session, result.items, key)) continue;
      gone.set(key, obj);
    }
    if (gone.size === 0) return;
    let changed = false;
    const detailTabs = tab.state.detailTabs.map((dt) => {
      if (dt.isDeleted || (dt.cluster && dt.cluster !== rt.cluster) || !dt.item) return dt;
      const name = dt.item.metadata?.name || dt.item.name;
      const namespace = dt.item.metadata?.namespace || dt.item.namespace || '';
      const obj = gone.get(`${namespace}/${name}`);
      if (!obj || !isListedType(rt.resource, dt.resource, dt.item)) return dt;
      const uid = dt.item.metadata?.uid || dt.item.uid;
      if (uid && obj.uid && uid !== obj.uid) return dt;
      changed = true;
      return { ...dt, isDeleted: true };
    });
    if (!changed) return;
    set((st) => ({
      activeTabs: st.activeTabs.map((t) =>
        t.id === rt.cluster ? { ...t, state: { ...t.state, detailTabs } } : t,
      ),
    }));
  };

  // Writes a topic's list into the current cluster tab: the tab-level list and
  // every open list tab showing the same resource type.
  const commitList = (resNow: any, items: any, patch: any, selectedItem: any) => {
    const ct = get().currentTab;
    set((st) => {
      const idx = st.tabIndexMap.get(ct || '') ?? -1;
      if (idx === -1) return {};
      const t = st.activeTabs[idx];
      const activeId = t.state.activeResourceListTab;
      const resourceListTabs = t.state.resourceListTabs.map((r2) =>
        r2.resource.kind === resNow.kind && r2.resource.group === resNow.group && r2.resource.version === resNow.version
          ? { ...r2, items, selectedItem: r2.id === activeId ? selectedItem : r2.selectedItem }
          : r2,
      );
      const tabs = [...st.activeTabs];
      tabs[idx] = { ...t, state: { ...t.state, ...patch, listItems: items, selectedItem, resourceListTabs } };
      return { activeTabs: tabs };
    });
  };

  // The list tabs of rt's cluster tab that show rt's topic in a split pane.
  // Only while that cluster tab is on screen, unless anyCluster: a pane of a
  // cluster tab in the background is still a pane, and its list must survive
  // until that tab is shown again.
  const paneTabsShowing = (rt: RealtimeRuntime, anyCluster = false): Set<string> | null => {
    const st = get();
    if (!anyCluster && st.currentTab !== rt.cluster) return null;
    const idx = st.tabIndexMap.get(rt.cluster) ?? -1;
    const t = idx === -1 ? undefined : st.activeTabs[idx];
    const shown = Object.values(t?.state.activeResourceListTabByPane || {});
    if (!t || shown.length < 2) return null;
    let ids: Set<string> | null = null;
    for (const r2 of t.state.resourceListTabs) {
      if (
        shown.includes(r2.id) &&
        itemsTopic(r2.cluster, r2.resource) === rt.topic
      )
        (ids ??= new Set()).add(r2.id);
    }
    return ids;
  };

  // Writes rt's list into the list tabs of split panes that show it.
  const writePaneTabs = (rt: RealtimeRuntime, paneTabs: Set<string>) => {
    set((st) => {
      const idx = st.tabIndexMap.get(rt.cluster) ?? -1;
      if (idx === -1) return {};
      const t = st.activeTabs[idx];
      if (!t.state.resourceListTabs.some((r2) => paneTabs.has(r2.id) && r2.items !== rt.items)) return {};
      const resourceListTabs = t.state.resourceListTabs.map((r2) =>
        paneTabs.has(r2.id) ? { ...r2, items: rt.items } : r2,
      );
      const tabs = [...st.activeTabs];
      tabs[idx] = { ...t, state: { ...t.state, resourceListTabs } };
      return { activeTabs: tabs };
    });
  };

  const requestFlushFrame = (rt: RealtimeRuntime) => {
    if (typeof document !== 'undefined' && document.hidden) {
      rt.batchTimer = setTimeout(
        () => processBatch(rt),
        PARKED_FLUSH_MS,
      ) as unknown as number;
      rt.batchTimerKind = 'timeout';
      return;
    }
    rt.batchTimer = requestAnimationFrame(() => {
      processBatch(rt);
    }) as unknown as number;
    rt.batchTimerKind = 'raf';
    rt.frameFallback = setTimeout(() => {
      rt.frameFallback = null;
      if (rt.batchTimerKind !== 'raf' || rt.batchTimer === null) return;
      cancelAnimationFrame(rt.batchTimer);
      processBatch(rt);
    }, FRAME_FALLBACK_MS) as unknown as number;
  };

  const scheduleFlush = (rt: RealtimeRuntime) => {
    const { interaction } = rt;
    if (runtime() !== rt && !paneTabsShowing(rt)) {
      rt.batchTimer = setTimeout(
        () => processBatch(rt),
        PARKED_FLUSH_MS,
      ) as unknown as number;
      rt.batchTimerKind = 'timeout';
      return;
    }
    // The first rows of a list not shown yet go out on the next frame; only
    // later updates wait for the coalescing window.
    if (!rt.synced) {
      requestFlushFrame(rt);
      return;
    }
    const handle = setTimeout(() => {
      const now = performance.now();
      if (now - interaction.last < 250 && now - interaction.deferredSince < 1000) {
        scheduleFlush(rt);
        return;
      }
      requestFlushFrame(rt);
    }, flushWindow(rt)) as unknown as number;
    rt.batchTimer = handle;
    rt.batchTimerKind = 'timeout';
  };

  const processBatch = (rt: RealtimeRuntime) => {
    if (rt.frameFallback !== null) {
      clearTimeout(rt.frameFallback);
      rt.frameFallback = null;
    }
    rt.batchTimer = null;
    rt.batchTimerKind = null;
    if (rt.session.stopped || rt.pendingEvents.length === 0) return;

    const state = get().getCurrentTabState();
    const onScreen = runtime() === rt && state?.selectedNode?.type === 'resource'
      && itemsTopic(get().currentTab || '', state.selectedNode.data) === rt.topic;
    if (!onScreen || !state) {
      // Parked, or its tab was just switched away from: keep the list current
      // off-screen. The store is written only for list tabs that another split
      // pane still shows.
      const events = rt.pendingEvents.splice(0, rt.pendingEvents.length);
      const result = applyRealtimeEvents(rt.items, events, rt.session);
      rt.items = result.items;
      outcomeOf(rt, result, events);
      cacheItems(rt.topic, result.items);
      const paneTabs = result.changed ? paneTabsShowing(rt) : null;
      if (paneTabs) writePaneTabs(rt, paneTabs);
      markListingRemovals(rt, result);
      return;
    }
    const resNow: any = state.selectedNode!.data;

    const events = rt.pendingEvents.splice(0, rt.pendingEvents.length);

    const activeListTab = state.resourceListTabs?.find((r2) => r2.id === state.activeResourceListTab);
    const activeListTabMatches = activeListTab
      && activeListTab.cluster === rt.cluster
      && (activeListTab.resource.group || '') === (resNow.group || '')
      && (activeListTab.resource.version || '') === (resNow.version || '')
      && (
        (activeListTab.resource.name || '').toLowerCase() === (resNow.name || '').toLowerCase()
        || (activeListTab.resource.kind || '').toLowerCase() === (resNow.kind || '').toLowerCase()
      );
    const baseItems = state.listItems?.length ? state.listItems : (activeListTabMatches ? (activeListTab.items || []) : []);

    const result = applyRealtimeEvents(baseItems, events, rt.session);
    rt.items = result.items;

    cacheItems(rt.topic, result.items);

    const resourceKind = resNow?.kind?.toLowerCase() || '';
    if (resourceKind.includes('deployment') || resourceKind.includes('statefulset') || resourceKind.includes('daemonset')) {
      for (const ev of events) {
        if (ev.action !== 'added' && ev.action !== 'modified') continue;
        const current = findItem(rt.session, result.items, itemKey(ev.item));
        if (current && isRolloutComplete(current)) {
          const rolloutKey = `${resNow.group || '_'}/${resNow.version}/${resNow.name}/${current.namespace || '_'}/${current.name}`;
          if (state.rolloutRequests?.has(rolloutKey)) {
            notifyRolloutComplete(current.name);
            get().removeRolloutTracking(rolloutKey);
          }
        }
      }
    }

    // Keep a restored selection until its row arrives in the streamed snapshot.
    let selectedItem = state.selectedItem;
    if (selectedItem) {
      const selectedKey = itemKey(selectedItem);
      const liveItem = findItem(rt.session, result.items, selectedKey);
      const removed = result.completedEpoch !== undefined || events.some((event) => event.action === 'deleted' && itemKey(event.item) === selectedKey);
      selectedItem = liveItem ?? (removed ? null : selectedItem);
    }

    const outcome = outcomeOf(rt, result, events);
    const listSync = listSyncOf(rt, state.listSync);
    // A failed list leaves the rows as they were, no longer shown as live.
    const patch: any = { listSync };
    if (outcome === 'failed') {
      patch.isLoadingListItems = false;
      patch.loadError = rt.failing?.error;
    } else if (outcome) {
      patch.isLoadingListItems = false;
      patch.hasReceivedInitialListData = true;
      patch.loadError = rt.failing?.error;
    }
    const flagsChanged = Object.keys(patch).some((k) => k !== 'listSync' && (state as any)[k] !== patch[k]);
    const needsListCommit = result.changed || selectedItem !== state.selectedItem
      || flagsChanged
      || listSync !== (state.listSync ?? null);
    if (needsListCommit) commitList(resNow, result.items, patch, selectedItem);

    updateDetails(resNow, events);
    markListingRemovals(rt, result);
  };

  // Open detail tabs and the detail panel follow live changes of the objects
  // they show. Only objects of the listed type match: a Deployment, its
  // Service and its ConfigMap often share a name.
  const updateDetails = (resNow: any, events: RealtimeEvent[]) => {
    const ct = get().currentTab;
    const tab = get().activeTabs.find((t) => t.id === ct);
    if (!tab) return;
    const { detailData, detailTabs } = tab.state;
    if (!detailData && detailTabs.length === 0) return;

    // Only the last event per object matters for what a detail shows.
    const lastByKey = new Map<string, RealtimeEvent>();
    for (const ev of events) {
      if (ev.action === 'sync' || !ev.item) continue;
      lastByKey.set(
        `${ev.item.namespace || ev.item.metadata?.namespace || ''}/${ev.item.name || ev.item.metadata?.name}`,
        ev,
      );
    }
    if (lastByKey.size === 0) return;

    const eventFor = (resource: any, obj: any) => {
      if (!obj) return undefined;
      const ev = lastByKey.get(
        `${obj.metadata?.namespace || obj.namespace || ''}/${obj.metadata?.name || obj.name}`,
      );
      return ev && isListedType(resNow, resource, obj) ? ev : undefined;
    };

    const changed = new Map<string, { name: string; namespace: string }>();
    const noteChange = (ev: RealtimeEvent) => {
      // A snapshot replays every object; that is not a change to refetch for.
      if (ev.action === 'added' && ev.epoch) return;
      const name = ev.item.name || ev.item.metadata?.name;
      const namespace = ev.item.namespace || ev.item.metadata?.namespace || '';
      changed.set(`${namespace}/${name}`, { name, namespace });
    };

    let needsUpdate = false;
    let newDetailData = detailData;
    const dataEv = eventFor(undefined, detailData);
    if (dataEv && dataEv.action !== 'deleted') {
      const conditions = dataEv.item.conditions;
      newDetailData = {
        ...detailData,
        status: { ...detailData.status, conditions },
        conditions,
      };
      needsUpdate = true;
      noteChange(dataEv);
    }
    const newDetailTabs = detailTabs.map((dt) => {
      const ev = eventFor(dt.resource, dt.item);
      if (!ev) return dt;
      if (ev.action === 'deleted') {
        if (dt.isDeleted) return dt;
        needsUpdate = true;
        return { ...dt, isDeleted: true };
      }
      needsUpdate = true;
      noteChange(ev);
      const conditions = ev.item.conditions;
      return {
        ...dt,
        isDeleted: false,
        item: {
          ...dt.item,
          status: { ...dt.item?.status, conditions },
          conditions,
        },
      };
    });

    if (needsUpdate) {
      set((st) => ({
        activeTabs: st.activeTabs.map((t) =>
          t.id === ct
            ? {
                ...t,
                state: {
                  ...t.state,
                  detailData: newDetailData,
                  detailTabs: newDetailTabs,
                },
              }
            : t,
        ),
      }));
    }
    for (const detail of changed.values()) {
      window.dispatchEvent(
        new CustomEvent('kanivet:detail-item-changed', { detail }),
      );
    }
  };

  const createRuntime = (
    topic: string,
    cluster: string,
    resource: any,
    items: any[],
    sort: { by: string; order: 'asc' | 'desc' },
  ): RealtimeRuntime => {
    // No interaction yet: starting at 0 would read as "the user just did
    // something" for the first 250ms after load and hold updates back.
    const interaction = { last: -Infinity, deferredSince: 0 };
    const rt: RealtimeRuntime = {
      interaction,
      topic,
      cluster,
      resource,
      sort,
      session: newRealtimeSession(),
      pendingEvents: [],
      batchTimer: null,
      batchTimerKind: null,
      frameFallback: null,
      items,
      synced: false,
      startedAt: Date.now(),
      markInteraction: () => {
        interaction.last = performance.now();
      },
      onEvent: () => {},
      failing: null,
      loadTimer: null,
      resubscribeTimer: null,
      resubscribeAttempt: 0,
    };
    rt.onEvent = (evt: any) => {
      if (rt.session.stopped || !evt.isBatch || !evt.events) return;
      if (evt.topic && evt.topic !== rt.topic) return;
      let pushed = false;

      for (const event of evt.events) {
        const type = event.messageType || event.MessageType || event.type;
        if (type === 'sync_complete') {
          if (rt.loadTimer) {
            clearTimeout(rt.loadTimer);
            rt.loadTimer = null;
          }
          const count = event.itemCount ?? event.ItemCount;
          const epoch = event.epoch || event.Epoch || 0;
          // Epoch 0 closes no listing: the backend could not read the list.
          const failed = event.failed === true || !(epoch > 0);
          const lastSyncAt = typeof event.lastSyncAt === 'number' && event.lastSyncAt > 0 ? event.lastSyncAt : undefined;
          rt.pendingEvents.push({
            action: 'sync',
            epoch,
            itemCount: typeof count === 'number' ? count : undefined,
            ...(failed && { failed: true, error: event.error || undefined, errorCode: event.errorCode || undefined }),
            ...(lastSyncAt && { lastSyncAt }),
          });
          pushed = true;
          continue;
        }
        if (type === 'topic_status') {
          onTopicStatus(rt, event);
          continue;
        }
        const channel = event.Channel || event.channel;
        if (channel !== undefined && channel !== 'items') continue;
        const action = (event.action || event.Action || '').toLowerCase();
        if (action !== 'added' && action !== 'modified' && action !== 'deleted') continue;
        rt.pendingEvents.push({ action, item: event.item || event.Item || {}, epoch: evt.bulk ? evt.epoch : undefined });
        pushed = true;
      }
      if (evt.bulk && evt.epoch && typeof evt.total === 'number') rt.listTotal = { epoch: evt.epoch, total: evt.total };

      if (pushed && !rt.batchTimer) {
        interaction.deferredSince = performance.now();
        scheduleFlush(rt);
      }
    };
    // A fresh subscription is not live until its list arrives. A failure
    // the backend reported stays shown until then; a timeout of our own is
    // for the subscription it timed.
    const prev = get().topicHealth?.[topic];
    const failing = prev?.failing?.errorCode === 'timeout' ? null : prev?.failing ?? null;
    writeHealth(topic, { live: false, failing, lastSyncAt: prev?.lastSyncAt });
    return rt;
  };

  // Moves the active subscription to the parked set, keeping it open.
  const parkActive = () => {
    const rt = runtime();
    (window as any).__kanivetRealtime = undefined;
    (window as any).__kanivetVisibleUids = undefined;
    if (!rt || rt.session.stopped) return;
    stopListeningForInteraction(rt);
    if (!api.wsHandlers.has(rt.topic)) {
      closeRuntime(rt);
      return;
    }
    // A pending on-screen flush becomes a parked flush (or a split pane's).
    if (rt.batchTimer !== null) {
      clearTimer(rt);
      scheduleFlush(rt);
    }
    const parked = parkedRuntimes();
    const previous = parked.get(rt.topic);
    if (previous && previous !== rt) closeRuntime(previous);
    parked.delete(rt.topic);
    parked.set(rt.topic, rt);
    while (parked.size > MAX_PARKED_TOPICS) {
      // The oldest no split pane shows, on any cluster tab: a pane's list
      // must stay live, or come back frozen when its cluster tab does.
      let oldest: string | undefined;
      for (const [topic, p] of parked) {
        if (!paneTabsShowing(p, true)) {
          oldest = topic;
          break;
        }
      }
      if (oldest === undefined) break;
      const evicted = parked.get(oldest)!;
      parked.delete(oldest);
      closeRuntime(evicted);
    }
  };

  // Asks again for the list of a parked topic a split pane shows, as a
  // restart does for the on-screen one.
  const resubscribePane = (rt: RealtimeRuntime, tabIds: Set<string>) => {
    const tab = get().getCurrentTabState()?.resourceListTabs.find((r2) => tabIds.has(r2.id));
    if (!tab) return;
    rt.sort = { by: tab.sortBy || 'age', order: tab.sortOrder || 'desc' };
    subscribe(rt);
  };

  // Every list a split pane of the current cluster tab shows is subscribed,
  // not only the focused one: a cluster tab switched back to, or restored,
  // starts only its focused list, and the others would stay as they were.
  const ensurePaneSubscriptions = () => {
    const st = get();
    const cluster = st.currentTab;
    if (!cluster) return;
    const idx = st.tabIndexMap.get(cluster) ?? -1;
    const t = idx === -1 ? undefined : st.activeTabs[idx];
    const shown = Object.values(t?.state.activeResourceListTabByPane || {});
    if (!t || shown.length < 2) return;
    const onScreen = runtime();
    const parked = parkedRuntimes();
    for (const r2 of t.state.resourceListTabs) {
      if (!shown.includes(r2.id) || !isLiveList(r2.resource)) continue;
      if (r2.cluster && r2.cluster !== cluster) continue;
      const topic = itemsTopic(cluster, r2.resource);
      if (onScreen?.topic === topic && !onScreen.session.stopped) continue;
      const existing = parked.get(topic);
      if (existing && !existing.session.stopped && api.wsHandlers.has(topic)) {
        // Kept current while its cluster tab was in the background: only
        // the subscription saw those changes.
        const paneTabs = paneTabsShowing(existing);
        if (paneTabs && existing.synced) writePaneTabs(existing, paneTabs);
        continue;
      }
      if (existing) {
        parked.delete(topic);
        closeRuntime(existing);
      }
      const rt = createRuntime(topic, cluster, r2.resource, r2.items || [], {
        by: r2.sortBy || 'age',
        order: r2.sortOrder || 'desc',
      });
      parked.set(topic, rt);
      armLoadTimer(rt);
      subscribe(rt);
    }
  };

  // Makes rt the on-screen subscription.
  const activate = (rt: RealtimeRuntime) => {
    (window as any).__kanivetRealtime = rt;
    listenForInteraction(rt);
  };

  // Starts the list of the selected tree node.
  const startShownList = (forceRefresh?: boolean) => {
    const { currentTab } = get();
    const tabState = get().getCurrentTabState();
    if (!currentTab || !tabState?.selectedNode || tabState.selectedNode.type !== 'resource') return;
    const res = tabState.selectedNode.data as any;
    const topicKey = itemsTopic(currentTab, res);
    const sortTab = tabState.resourceListTabs?.find((r2) => r2.id === tabState.activeResourceListTab);
    const sort = { by: sortTab?.sortBy || 'age', order: sortTab?.sortOrder || 'desc' } as const;
    const existing = runtime();
    if (existing?.topic === topicKey && api.wsHandlers.has(topicKey)) {
      // A synced subscription is already live, so a refresh would only resend
      // the same list. Resubscribe only while it is still waiting for data.
      if (forceRefresh && !existing.synced) {
        existing.sort = { ...sort };
        subscribe(existing);
      }
      return;
    }
    parkActive();

    // A parked subscription that already delivered its list resumes as is, and
    // one still waiting for its list keeps waiting while the wait is shorter
    // than the load timeout. An older one is replaced below, so a failed
    // subscribe is retried.
    const parked = parkedRuntimes().get(topicKey);
    if (parked) parkedRuntimes().delete(topicKey);
    const resumable =
      parked &&
      !parked.session.stopped &&
      api.wsHandlers.has(topicKey) &&
      (parked.synced || Date.now() - parked.startedAt < LOAD_TIMEOUT_MS);
    if (parked && resumable && parked.synced) {
      activate(parked);
      const state = get().getCurrentTabState();
      const selectedItem = state?.selectedItem
        ? (findItem(
            parked.session,
            parked.items,
            itemKey(state.selectedItem),
          ) ?? null)
        : null;
      // A list the backend stopped keeping current is shown as such.
      const loadError = parked.failing?.error;
      const upToDate =
        state?.listItems === parked.items &&
        state?.hasReceivedInitialListData &&
        !state?.isLoadingListItems &&
        state?.loadError === loadError &&
        selectedItem === (state?.selectedItem ?? null);
      const listSync = listSyncOf(parked, state?.listSync);
      if (!upToDate || listSync !== (state?.listSync ?? null)) {
        commitList(res, parked.items, { isLoadingListItems: false, hasReceivedInitialListData: true, loadError, listSync }, selectedItem);
      }
      // Events that arrived while parked are flushed on the on-screen cadence.
      if (parked.pendingEvents.length > 0) {
        clearTimer(parked);
        parked.interaction.deferredSince = performance.now();
        scheduleFlush(parked);
      }
      return;
    }
    if (parked && !resumable) closeRuntime(parked);

    const activeListTab = tabState.resourceListTabs?.find((rt) => rt.id === tabState.activeResourceListTab);
    const activeListTabMatches = activeListTab
      && activeListTab.cluster === currentTab
      && (activeListTab.resource.group || '') === (res.group || '')
      && (activeListTab.resource.version || '') === (res.version || '')
      && (
        (activeListTab.resource.name || '').toLowerCase() === (res.name || '').toLowerCase()
        || (activeListTab.resource.kind || '').toLowerCase() === (res.kind || '').toLowerCase()
      );
    // Rows already on screen (a cache, an earlier visit) stay while the list
    // arrives, but as rows still loading: hasReceivedInitialListData is set
    // only by the stream.
    const preservedItems = tabState.listItems?.length
      ? tabState.listItems
      : activeListTabMatches
        ? activeListTab.items
        : [];
    get().updateCurrentTabState({
      isLoadingListItems: true,
      hasReceivedInitialListData: false,
      loadError: undefined,
      listSync: null,
      listItems: preservedItems,
    });

    // Not resubscribed even when forced: its list is on its way, and asking
    // again would make the backend send all of it a second time. The load
    // timeout still counts from the first subscribe.
    if (parked && resumable) {
      activate(parked);
      if (!parked.loadTimer && !parked.failing) {
        armLoadTimer(parked, LOAD_TIMEOUT_MS - (Date.now() - parked.startedAt));
      }
      if (parked.failing) get().updateCurrentTabState({ isLoadingListItems: false, loadError: parked.failing.error });
      if (parked.pendingEvents.length > 0) {
        clearTimer(parked);
        scheduleFlush(parked);
      }
      return;
    }

    const rt = createRuntime(topicKey, currentTab, res, preservedItems, { ...sort });
    activate(rt);
    armLoadTimer(rt);
    subscribe(rt);
  };

  return {
    topicHealth: {},

    // Starts at once; calls that follow within 50ms collapse into one more
    // start at the end of the burst. That one is never forced: resubscribing
    // would only repeat a subscribe this burst has just sent.
    startRealtime: (forceRefresh?: boolean) => {
      const w = window as any;
      const burst = !!w.__kanivetStartRealtimeTimer;
      if (burst) clearTimeout(w.__kanivetStartRealtimeTimer);
      else get()._startRealtimeInternal(forceRefresh);
      w.__kanivetStartRealtimeTimer = setTimeout(() => {
        w.__kanivetStartRealtimeTimer = null;
        if (burst) get()._startRealtimeInternal();
      }, 50);
    },

    _startRealtimeInternal: (forceRefresh?: boolean) => {
      if (!get().currentTab) return;
      if (!api.isReady()) {
        if ((window as any).__kanivetStartRetryTimer) clearTimeout((window as any).__kanivetStartRetryTimer);
        (window as any).__kanivetStartRetryTimer = setTimeout(() => get()._startRealtimeInternal(forceRefresh), 100);
        return;
      }
      startShownList(forceRefresh);
      ensurePaneSubscriptions();
    },

    parkRealtime: () => {
      if ((window as any).__kanivetStartRealtimeTimer) { clearTimeout((window as any).__kanivetStartRealtimeTimer); (window as any).__kanivetStartRealtimeTimer = null; }
      if ((window as any).__kanivetStartRetryTimer) { clearTimeout((window as any).__kanivetStartRetryTimer); (window as any).__kanivetStartRetryTimer = null; }
      parkActive();
    },

    releaseRealtimeTopics: (shouldRelease: (topic: string) => boolean) => {
      const rt = runtime();
      if (rt && shouldRelease(rt.topic)) {
        closeRuntime(rt);
        (window as any).__kanivetRealtime = undefined;
        (window as any).__kanivetVisibleUids = undefined;
      }
      const parked = parkedRuntimes();
      for (const [topic, p] of [...parked]) {
        if (shouldRelease(topic)) {
          parked.delete(topic);
          closeRuntime(p);
        }
      }
    },

    stopRealtime: () => {
      if ((window as any).__kanivetStartRealtimeTimer) { clearTimeout((window as any).__kanivetStartRealtimeTimer); (window as any).__kanivetStartRealtimeTimer = null; }
      if ((window as any).__kanivetStartRetryTimer) { clearTimeout((window as any).__kanivetStartRetryTimer); (window as any).__kanivetStartRetryTimer = null; }
      const rt = runtime();
      if (rt) closeRuntime(rt);
      const parked = parkedRuntimes();
      for (const [topic, p] of [...parked]) {
        // Callers restart only the on-screen list; one a split pane shows
        // stays open and asks for its list again instead of freezing. A pane
        // of a cluster tab in the background is closed, and subscribed again
        // when that tab is shown.
        const shown = p.session.stopped ? null : paneTabsShowing(p);
        if (shown) {
          resubscribePane(p, shown);
          continue;
        }
        parked.delete(topic);
        closeRuntime(p);
      }
      (window as any).__kanivetRealtime = undefined;
      (window as any).__kanivetVisibleUids = undefined;
    },
  };
};
