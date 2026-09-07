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
  markInteraction: () => void;
  onEvent: (evt: any) => void;
}

const runtime = () => (window as any).__kanivetRealtime as RealtimeRuntime | undefined;

export const currentRealtimeTopic = () => runtime()?.topic;

export const createRealtimeSlice: StateCreator<StoreState, [], [], RealtimeSlice> = (set, get) => ({
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
    const topicKey = `items:${currentTab}:${res.group || ''}:${res.version}:${res.name}:`;
    const existing = runtime();
    if (existing?.topic === topicKey && api.wsHandlers.has(topicKey)) {
      if (forceRefresh) {
        const sortTab = tabState.resourceListTabs?.find((r2) => r2.id === tabState.activeResourceListTab);
        api.subscribeToItems(currentTab, res.group || '', res.version, res.name, '', existing.onEvent, sortTab?.sortBy || 'age', sortTab?.sortOrder || 'desc');
      }
      return;
    }
    get().stopRealtime();

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

    const timeoutId = setTimeout(() => {
      const currentState = get().getCurrentTabState();
      if (currentState?.isLoadingListItems && !currentState?.hasReceivedInitialListData) {
        console.error('⚠️ Timeout reached while loading:', res.name);
        get().updateCurrentTabState({ isLoadingListItems: false, hasReceivedInitialListData: false, loadError: 'Loading is taking longer than expected. The resource may not be available in this cluster.' });
      }
    }, 30000);
    (window as any).__kanivetLoadTimeout = timeoutId;

    const interaction = { last: 0, deferredSince: 0 };
    const markInteraction = () => { interaction.last = performance.now(); };
    const rt: RealtimeRuntime = {
      topic: topicKey,
      cluster: currentTab,
      session: newRealtimeSession(),
      pendingEvents: [],
      batchTimer: null,
      batchTimerKind: null,
      markInteraction,
      onEvent: () => {},
    };
    (window as any).__kanivetRealtime = rt;
    window.addEventListener('scroll', markInteraction, { capture: true, passive: true });
    window.addEventListener('wheel', markInteraction, { capture: true, passive: true });
    window.addEventListener('pointerdown', markInteraction, { capture: true, passive: true });

    const processBatch = () => {
      rt.batchTimer = null;
      rt.batchTimerKind = null;
      if (rt.session.stopped || rt.pendingEvents.length === 0) return;
      const state = get().getCurrentTabState();
      if (!state || !state.selectedNode || state.selectedNode.type !== 'resource') return;
      const resNow: any = state.selectedNode.data;
      const expectedTopic = `items:${get().currentTab}:${resNow.group || ''}:${resNow.version}:${resNow.name}:`;
      if (expectedTopic !== rt.topic) { rt.pendingEvents.length = 0; return; }

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

      const cacheMap: Map<string, Map<string, any>> = (window as any).__kanivetItemsCache || new Map();
      (window as any).__kanivetItemsCache = cacheMap;
      cacheMap.delete(rt.topic);
      cacheMap.set(rt.topic, result.map);
      while (cacheMap.size > 12) cacheMap.delete(cacheMap.keys().next().value as string);

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
      const needsListCommit = result.changed || selectedItem !== state.selectedItem
        || (result.syncSeen && (state.isLoadingListItems || !state.hasReceivedInitialListData));
      if (needsListCommit) {
        const patch: any = { listItems: result.items, selectedItem };
        if (result.syncSeen || authoritative || (result.changed && result.items.length > 0)) {
          patch.isLoadingListItems = false;
          patch.hasReceivedInitialListData = true;
          patch.loadError = undefined;
        }
        set((st) => {
          const idx = st.tabIndexMap.get(ct || '') ?? -1;
          if (idx === -1) return {};
          const t = st.activeTabs[idx];
          const activeId = t.state.activeResourceListTab;
          const resourceListTabs = t.state.resourceListTabs.map((r2) =>
            r2.resource.kind === resNow.kind && r2.resource.group === resNow.group && r2.resource.version === resNow.version
              ? { ...r2, items: result.items, selectedItem: r2.id === activeId ? selectedItem : r2.selectedItem }
              : r2,
          );
          const tabs = [...st.activeTabs];
          tabs[idx] = { ...t, state: { ...t.state, ...patch, resourceListTabs } };
          return { activeTabs: tabs };
        });
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

    const scheduleFlush = () => {
      const handle = setTimeout(() => {
        const now = performance.now();
        if (now - interaction.last < 250 && now - interaction.deferredSince < 1000) {
          scheduleFlush();
          return;
        }
        rt.batchTimer = requestAnimationFrame(() => { processBatch(); }) as unknown as number;
        rt.batchTimerKind = 'raf';
      }, 120) as unknown as number;
      rt.batchTimer = handle;
      rt.batchTimerKind = 'timeout';
    };

    rt.onEvent = (evt: any) => {
      if (rt.session.stopped || !evt.isBatch || !evt.events) return;
      if (evt.topic && evt.topic !== rt.topic) return;
      let pushed = false;

      for (const event of evt.events) {
        const type = event.messageType || event.MessageType || event.type;
        if (type === 'sync_complete') {
          if ((window as any).__kanivetLoadTimeout) { clearTimeout((window as any).__kanivetLoadTimeout); (window as any).__kanivetLoadTimeout = null; }
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
        scheduleFlush();
      }
    };

    const sortListTab = tabState?.resourceListTabs?.find((r2) => r2.id === tabState.activeResourceListTab);
    api.subscribeToItems(currentTab, res.group || '', res.version, res.name, '', rt.onEvent, sortListTab?.sortBy || 'age', sortListTab?.sortOrder || 'desc');
  },

  stopRealtime: () => {
    if ((window as any).__kanivetStartRealtimeTimer) { clearTimeout((window as any).__kanivetStartRealtimeTimer); (window as any).__kanivetStartRealtimeTimer = null; }
    if ((window as any).__kanivetStartRetryTimer) { clearTimeout((window as any).__kanivetStartRetryTimer); (window as any).__kanivetStartRetryTimer = null; }
    const rt = runtime();
    if (rt) {
      rt.session.stopped = true;
      api.unsubscribe(rt.topic, rt.onEvent);
      if (rt.batchTimer !== null) {
        if (rt.batchTimerKind === 'raf') cancelAnimationFrame(rt.batchTimer);
        else clearTimeout(rt.batchTimer);
        rt.batchTimer = null;
      }
      window.removeEventListener('scroll', rt.markInteraction, { capture: true } as any);
      window.removeEventListener('wheel', rt.markInteraction, { capture: true } as any);
      window.removeEventListener('pointerdown', rt.markInteraction, { capture: true } as any);
      rt.pendingEvents.length = 0;
    }
    (window as any).__kanivetRealtime = undefined;
    (window as any).__kanivetVisibleUids = undefined;
    if ((window as any).__kanivetLoadTimeout) { clearTimeout((window as any).__kanivetLoadTimeout); (window as any).__kanivetLoadTimeout = null; }
  },
});
