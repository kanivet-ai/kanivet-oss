import { StateCreator } from 'zustand';
import api from '../services/api';
import { RealtimeSlice, StoreState } from './types';
import { isRolloutComplete } from './utils';
import { notifyRolloutComplete } from '../services/islandNotifications';
import { applyRealtimeEvents, itemKey, newRealtimeSession, RealtimeEvent, RealtimeSession } from './realtimeReducer';

interface RealtimeRuntime {
  topic: string;
  cluster: string;
  session: RealtimeSession;
  pendingEvents: RealtimeEvent[];
  batchTimer: number | null;
  batchTimerKind: 'timeout' | 'raf' | null;
  // Latest reconciled list for this topic. Kept current while the topic is
  // parked so switching back to its tab needs no resync.
  items: any[];
  // True once the stream has delivered a snapshot, so items can be shown as live.
  synced: boolean;
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
const MAX_CACHED_TOPICS = 12;

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

const cacheItems = (topic: string, map: Map<string, any>) => {
  const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
  (window as any).__kanivetItemsCache = cacheMap;
  cacheMap.delete(topic);
  cacheMap.set(topic, map);
  while (cacheMap.size > MAX_CACHED_TOPICS) cacheMap.delete(cacheMap.keys().next().value as string);
};

const clearTimer = (rt: RealtimeRuntime) => {
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

export const createRealtimeSlice: StateCreator<StoreState, [], [], RealtimeSlice> = (set, get) => {
  const armLoadTimeout = (resourceName: string) => {
    clearLoadTimeout();
    (window as any).__kanivetLoadTimeout = setTimeout(() => {
      const currentState = get().getCurrentTabState();
      if (currentState?.isLoadingListItems && !currentState?.hasReceivedInitialListData) {
        console.error('⚠️ Timeout reached while loading:', resourceName);
        get().updateCurrentTabState({ isLoadingListItems: false, hasReceivedInitialListData: false, loadError: 'Loading is taking longer than expected. The resource may not be available in this cluster.' });
      }
    }, 30000);
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

  const scheduleFlush = (rt: RealtimeRuntime) => {
    const { interaction } = rt;
    if (runtime() !== rt) {
      rt.batchTimer = setTimeout(() => processBatch(rt), PARKED_FLUSH_MS) as unknown as number;
      rt.batchTimerKind = 'timeout';
      return;
    }
    const handle = setTimeout(() => {
      const now = performance.now();
      if (now - interaction.last < 250 && now - interaction.deferredSince < 1000) {
        scheduleFlush(rt);
        return;
      }
      rt.batchTimer = requestAnimationFrame(() => { processBatch(rt); }) as unknown as number;
      rt.batchTimerKind = 'raf';
    }, 120) as unknown as number;
    rt.batchTimer = handle;
    rt.batchTimerKind = 'timeout';
  };

  const processBatch = (rt: RealtimeRuntime) => {
    rt.batchTimer = null;
    rt.batchTimerKind = null;
    if (rt.session.stopped || rt.pendingEvents.length === 0) return;

    const state = get().getCurrentTabState();
    const onScreen = runtime() === rt && state?.selectedNode?.type === 'resource'
      && itemsTopic(get().currentTab || '', state.selectedNode.data) === rt.topic;
    if (!onScreen || !state) {
      // Parked, or its tab was just switched away from: keep the list current
      // off-screen, without touching the store.
      const events = rt.pendingEvents.splice(0, rt.pendingEvents.length);
      const result = applyRealtimeEvents(rt.items, events, rt.session);
      rt.items = result.items;
      if (result.syncSeen || result.completedEpoch !== undefined || (result.changed && result.items.length > 0)) rt.synced = true;
      cacheItems(rt.topic, result.map);
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

    cacheItems(rt.topic, result.map);

    const resourceKind = resNow?.kind?.toLowerCase() || '';
    if (resourceKind.includes('deployment') || resourceKind.includes('statefulset') || resourceKind.includes('daemonset')) {
      for (const ev of events) {
        if (ev.action !== 'added' && ev.action !== 'modified') continue;
        const current = result.map.get(itemKey(ev.item));
        if (current && isRolloutComplete(current)) {
          const rolloutKey = `${resNow.group || '_'}/${resNow.version}/${resNow.name}/${current.namespace || '_'}/${current.name}`;
          if (state.rolloutRequests?.has(rolloutKey)) {
            notifyRolloutComplete(current.name);
            get().removeRolloutTracking(rolloutKey);
          }
        }
      }
    }

    let selectedItem = state.selectedItem;
    if (selectedItem && !result.map.has(itemKey(selectedItem))) selectedItem = null;

    const { activeTabs, currentTab: ct } = get();
    const tab = activeTabs.find((t) => t.id === ct);
    const authoritative = result.completedEpoch !== undefined;
    const live = result.syncSeen || authoritative || (result.changed && result.items.length > 0);
    if (live) rt.synced = true;
    const needsListCommit = result.changed || selectedItem !== state.selectedItem
      || (result.syncSeen && (state.isLoadingListItems || !state.hasReceivedInitialListData));
    if (needsListCommit) {
      const patch: any = {};
      if (live) {
        patch.isLoadingListItems = false;
        patch.hasReceivedInitialListData = true;
        patch.loadError = undefined;
      }
      commitList(resNow, result.items, patch, selectedItem);
    }

    if (tab) {
      const detailData = tab.state.detailData;
      const detailTabs = tab.state.detailTabs;
      let needsUpdate = false;
      let newDetailData = detailData;
      let newDetailTabs = detailTabs;

      for (const ev of events) {
        if (ev.action === 'sync' || !ev.item) continue;
        const item = ev.item;
        const itemName = item.name || item.metadata?.name;
        const itemNs = item.namespace || item.metadata?.namespace || '';

        if (ev.action === 'deleted') {
          newDetailTabs = newDetailTabs.map((dt) => {
            const tabName = dt.item?.metadata?.name || dt.item?.name;
            const tabNs = dt.item?.metadata?.namespace || dt.item?.namespace || '';
            if (tabName === itemName && tabNs === itemNs && !dt.isDeleted) { needsUpdate = true; return { ...dt, isDeleted: true }; }
            return dt;
          });
          continue;
        }

        if (detailData) {
          const detailName = detailData.metadata?.name || detailData.name;
          const detailNs = detailData.metadata?.namespace || detailData.namespace || '';
          if (detailName === itemName && detailNs === itemNs) {
            newDetailData = { ...detailData, status: { ...detailData.status, conditions: item.conditions }, conditions: item.conditions };
            needsUpdate = true;
          }
        }
        window.dispatchEvent(new CustomEvent('kanivet:detail-item-changed', { detail: { name: itemName, namespace: itemNs } }));

        newDetailTabs = newDetailTabs.map((dt) => {
          const tabName = dt.item?.metadata?.name || dt.item?.name;
          const tabNs = dt.item?.metadata?.namespace || dt.item?.namespace || '';
          if (tabName === itemName && tabNs === itemNs) {
            needsUpdate = true;
            return { ...dt, isDeleted: false, item: { ...dt.item, status: { ...dt.item?.status, conditions: item.conditions }, conditions: item.conditions } };
          }
          return dt;
        });
      }

      if (needsUpdate) {
        set((st) => ({
          activeTabs: st.activeTabs.map((t) => t.id === ct ? { ...t, state: { ...t.state, detailData: newDetailData, detailTabs: newDetailTabs } } : t),
        }));
      }
    }
  };

  const createRuntime = (topic: string, cluster: string, items: any[]): RealtimeRuntime => {
    const interaction = { last: 0, deferredSince: 0 };
    const rt: RealtimeRuntime = {
      interaction,
      topic,
      cluster,
      session: newRealtimeSession(),
      pendingEvents: [],
      batchTimer: null,
      batchTimerKind: null,
      items,
      synced: false,
      markInteraction: () => { interaction.last = performance.now(); },
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
    if (!api.wsHandlers.has(rt.topic)) { closeRuntime(rt); return; }
    // A pending on-screen flush becomes a parked flush.
    if (rt.batchTimer !== null) {
      clearTimer(rt);
      rt.batchTimer = setTimeout(() => processBatch(rt), PARKED_FLUSH_MS) as unknown as number;
      rt.batchTimerKind = 'timeout';
    }
    const parked = parkedRuntimes();
    const previous = parked.get(rt.topic);
    if (previous && previous !== rt) closeRuntime(previous);
    parked.delete(rt.topic);
    parked.set(rt.topic, rt);
    while (parked.size > MAX_PARKED_TOPICS) {
      const oldest = parked.keys().next().value as string;
      const evicted = parked.get(oldest)!;
      parked.delete(oldest);
      closeRuntime(evicted);
    }
  };

  // Makes rt the on-screen subscription.
  const activate = (rt: RealtimeRuntime) => {
    (window as any).__kanivetRealtime = rt;
    listenForInteraction(rt);
  };

  return {
    startRealtime: (forceRefresh?: boolean) => {
      if ((window as any).__kanivetStartRealtimeTimer) clearTimeout((window as any).__kanivetStartRealtimeTimer);
      (window as any).__kanivetStartRealtimeTimer = setTimeout(() => {
        (window as any).__kanivetStartRealtimeTimer = null;
        get()._startRealtimeInternal(forceRefresh);
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

      // A parked subscription that already delivered its list resumes as is.
      // One that never synced is replaced below, so a failed subscribe is retried.
      const parked = parkedRuntimes().get(topicKey);
      if (parked) parkedRuntimes().delete(topicKey);
      if (parked && parked.synced && !parked.session.stopped && api.wsHandlers.has(topicKey)) {
        activate(parked);
        const state = get().getCurrentTabState();
        let selectedItem = state?.selectedItem ?? null;
        if (selectedItem && !parked.items.some((i) => itemKey(i) === itemKey(selectedItem))) selectedItem = null;
        const upToDate = state?.listItems === parked.items && state?.hasReceivedInitialListData && !state?.isLoadingListItems
          && selectedItem === (state?.selectedItem ?? null);
        if (!upToDate) {
          commitList(res, parked.items, { isLoadingListItems: false, hasReceivedInitialListData: true, loadError: undefined }, selectedItem);
        }
        // Events that arrived while parked are flushed on the on-screen cadence.
        if (parked.pendingEvents.length > 0) {
          clearTimer(parked);
          parked.interaction.deferredSince = performance.now();
          scheduleFlush(parked);
        }
        return;
      }
      if (parked) closeRuntime(parked);

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
      get().updateCurrentTabState({ isLoadingListItems: true, hasReceivedInitialListData: false, loadError: undefined, listItems: preservedItems });
      armLoadTimeout(res.name);

      const rt = createRuntime(topicKey, currentTab, preservedItems);
      activate(rt);

      const sortListTab = tabState?.resourceListTabs?.find((r2) => r2.id === tabState.activeResourceListTab);
      api.subscribeToItems(currentTab, res.group || '', res.version, res.name, '', rt.onEvent, sortListTab?.sortBy || 'age', sortListTab?.sortOrder || 'desc');
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
      for (const p of parked.values()) closeRuntime(p);
      parked.clear();
      (window as any).__kanivetRealtime = undefined;
      (window as any).__kanivetVisibleUids = undefined;
      clearLoadTimeout();
    },
  };
};
