import { StateCreator } from 'zustand';
import api from '../services/api';
import { ListSync, RealtimeSlice, StoreState } from './types';
import { isRolloutComplete } from './utils';
import { notifyRolloutComplete } from '../services/islandNotifications';
import {
  applyRealtimeEvents,
  findItem,
  itemKey,
  newRealtimeSession,
  pendingListing,
  RealtimeEvent,
  RealtimeSession,
} from './realtimeReducer';

interface RealtimeRuntime {
  topic: string;
  cluster: string;
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
  stopListeningForInteraction(rt);
  rt.pendingEvents.length = 0;
};

const clearLoadTimeout = () => {
  if ((window as any).__kanivetLoadTimeout) { clearTimeout((window as any).__kanivetLoadTimeout); (window as any).__kanivetLoadTimeout = null; }
};

export const createRealtimeSlice: StateCreator<
  StoreState,
  [],
  [],
  RealtimeSlice
> = (set, get) => {
  const armLoadTimeout = (resourceName: string, ms = LOAD_TIMEOUT_MS) => {
    clearLoadTimeout();
    (window as any).__kanivetLoadTimeout = setTimeout(() => {
      const currentState = get().getCurrentTabState();
      if (currentState?.isLoadingListItems && !currentState?.hasReceivedInitialListData) {
        console.error('⚠️ Timeout reached while loading:', resourceName);
        get().updateCurrentTabState({ isLoadingListItems: false, hasReceivedInitialListData: false, loadError: 'Loading is taking longer than expected. The resource may not be available in this cluster.' });
      }
    }, ms);
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

  // The list tabs of the current cluster tab that show rt's topic in a split
  // pane while another topic is the on-screen one.
  const paneTabsShowing = (rt: RealtimeRuntime): Set<string> | null => {
    const st = get();
    if (st.currentTab !== rt.cluster) return null;
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
      if (
        result.syncSeen ||
        result.completedEpoch !== undefined ||
        (result.changed && result.items.length > 0)
      )
        rt.synced = true;
      cacheItems(rt.topic, result.items);
      const paneTabs = result.changed ? paneTabsShowing(rt) : null;
      if (paneTabs) {
        set((st) => {
          const idx = st.tabIndexMap.get(rt.cluster) ?? -1;
          if (idx === -1) return {};
          const t = st.activeTabs[idx];
          const resourceListTabs = t.state.resourceListTabs.map((r2) =>
            paneTabs.has(r2.id) ? { ...r2, items: result.items } : r2,
          );
          const tabs = [...st.activeTabs];
          tabs[idx] = { ...t, state: { ...t.state, resourceListTabs } };
          return { activeTabs: tabs };
        });
      }
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

    const authoritative = result.completedEpoch !== undefined;
    const live = result.syncSeen || authoritative || (result.changed && result.items.length > 0);
    if (live) rt.synced = true;
    const listSync = listSyncOf(rt, state.listSync);
    const needsListCommit = result.changed || selectedItem !== state.selectedItem
      || (result.syncSeen && (state.isLoadingListItems || !state.hasReceivedInitialListData))
      || listSync !== (state.listSync ?? null);
    if (needsListCommit) {
      const patch: any = { listSync };
      if (live) {
        patch.isLoadingListItems = false;
        patch.hasReceivedInitialListData = true;
        patch.loadError = undefined;
      }
      commitList(resNow, result.items, patch, selectedItem);
    }

    updateDetails(resNow, events);
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

    // Detail tabs keep their resource as it was opened (a plural name, or
    // 'customresourcedefinitions' for a CRD), so several spellings match.
    const lower = (s: any) => String(s || '').toLowerCase();
    const kind = lower(resNow.kind);
    const plural = lower(resNow.name);
    const isListedType = (resource: any, obj: any) => {
      const group =
        typeof resource?.group === 'string'
          ? resource.group
          : typeof obj.apiVersion === 'string' && obj.apiVersion
            ? obj.apiVersion.includes('/')
              ? obj.apiVersion.split('/')[0]
              : ''
            : undefined;
      if (group !== undefined && group !== (resNow.group || '')) return false;
      if (obj.kind)
        return lower(obj.kind) === kind || lower(obj.kind) === plural;
      return (
        lower(resource?.kind) === kind ||
        lower(resource?.kind) === plural ||
        lower(resource?.name) === plural
      );
    };
    const eventFor = (resource: any, obj: any) => {
      if (!obj) return undefined;
      const ev = lastByKey.get(
        `${obj.metadata?.namespace || obj.namespace || ''}/${obj.metadata?.name || obj.name}`,
      );
      return ev && isListedType(resource, obj) ? ev : undefined;
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

  const createRuntime = (topic: string, cluster: string, items: any[]): RealtimeRuntime => {
    // No interaction yet: starting at 0 would read as "the user just did
    // something" for the first 250ms after load and hold updates back.
    const interaction = { last: -Infinity, deferredSince: 0 };
    const rt: RealtimeRuntime = {
      interaction,
      topic,
      cluster,
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
    };
    rt.onEvent = (evt: any) => {
      if (rt.session.stopped || !evt.isBatch || !evt.events) return;
      if (evt.topic && evt.topic !== rt.topic) return;
      let pushed = false;

      for (const event of evt.events) {
        const type = event.messageType || event.MessageType || event.type;
        if (type === 'sync_complete') {
          if (runtime() === rt) clearLoadTimeout();
          const count = event.itemCount ?? event.ItemCount;
          rt.pendingEvents.push({ action: 'sync', epoch: event.epoch || event.Epoch || 0, itemCount: typeof count === 'number' ? count : undefined });
          pushed = true;
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
    return rt;
  };

  // Moves the active subscription to the parked set, keeping it open.
  const parkActive = () => {
    const rt = runtime();
    (window as any).__kanivetRealtime = undefined;
    (window as any).__kanivetVisibleUids = undefined;
    clearLoadTimeout();
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
      // The oldest no split pane shows: a pane's list must stay live.
      let oldest: string | undefined;
      for (const [topic, p] of parked) {
        if (!paneTabsShowing(p)) {
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
    const res: any = tab.resource;
    api.subscribeToItems(rt.cluster, res.group || '', res.version, res.name, '', rt.onEvent, tab.sortBy || 'age', tab.sortOrder || 'desc');
  };

  // Makes rt the on-screen subscription.
  const activate = (rt: RealtimeRuntime) => {
    (window as any).__kanivetRealtime = rt;
    listenForInteraction(rt);
  };

  return {
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
      const { currentTab } = get();
      const tabState = get().getCurrentTabState();
      if (!currentTab || !tabState?.selectedNode || tabState.selectedNode.type !== 'resource') return;
      const res = tabState.selectedNode.data as any;
      if (!api.isReady()) {
        if ((window as any).__kanivetStartRetryTimer) clearTimeout((window as any).__kanivetStartRetryTimer);
        (window as any).__kanivetStartRetryTimer = setTimeout(() => get()._startRealtimeInternal(forceRefresh), 100);
        return;
      }
      const topicKey = itemsTopic(currentTab, res);
      const existing = runtime();
      if (existing?.topic === topicKey && api.wsHandlers.has(topicKey)) {
        // A synced subscription is already live, so a refresh would only resend
        // the same list. Resubscribe only while it is still waiting for data.
        if (forceRefresh && !existing.synced) {
          const sortTab = tabState.resourceListTabs?.find((r2) => r2.id === tabState.activeResourceListTab);
          api.subscribeToItems(currentTab, res.group || '', res.version, res.name, '', existing.onEvent, sortTab?.sortBy || 'age', sortTab?.sortOrder || 'desc');
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
        const upToDate =
          state?.listItems === parked.items &&
          state?.hasReceivedInitialListData &&
          !state?.isLoadingListItems &&
          selectedItem === (state?.selectedItem ?? null);
        const listSync = listSyncOf(parked, state?.listSync);
        if (!upToDate || listSync !== (state?.listSync ?? null)) {
          commitList(res, parked.items, { isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined, listSync }, selectedItem);
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
        armLoadTimeout(
          res.name,
          LOAD_TIMEOUT_MS - (Date.now() - parked.startedAt),
        );
        activate(parked);
        if (parked.pendingEvents.length > 0) {
          clearTimer(parked);
          scheduleFlush(parked);
        }
        return;
      }
      armLoadTimeout(res.name);

      const sortListTab = tabState?.resourceListTabs?.find(
        (r2) => r2.id === tabState.activeResourceListTab,
      );
      const rt = createRuntime(topicKey, currentTab, preservedItems);
      activate(rt);

      api.subscribeToItems(
        currentTab,
        res.group || '',
        res.version,
        res.name,
        '',
        rt.onEvent,
        sortListTab?.sortBy || 'age',
        sortListTab?.sortOrder || 'desc',
      );
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
        clearLoadTimeout();
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
        // stays open and asks for its list again instead of freezing.
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
      clearLoadTimeout();
    },
  };
};
