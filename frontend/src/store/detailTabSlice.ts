import { StateCreator } from 'zustand';
import { DetailTabSlice, StoreState, DetailTab } from './types';

export const createDetailTabSlice: StateCreator<StoreState, [], [], DetailTabSlice> = (set, get) => ({
  openDetailTab: (resource: any, item: any, cluster: string, isPinned: boolean = false) => {
    const { activeTabs, currentTab } = get();
    if (cluster !== currentTab) get().openTab(cluster);
    const tab = activeTabs.find((t) => t.id === cluster);
    if (!tab) return;

    const resourceName = item.metadata?.name || item.name || 'Unknown';
    const namespace = item.metadata?.namespace || item.namespace;
    const kind = item.kind || resource?.kind || resource?.name || 'Resource';
    const apiVersion = item.apiVersion || resource?.apiVersion || '';
    const title = namespace ? `${resourceName} (${namespace})` : resourceName;
    const identity = `${apiVersion}/${kind}/${namespace || 'default'}/${resourceName}`;

    const existingTab = tab.state.detailTabs.find((dt) => {
      const dtKind = dt.item.kind || dt.resource?.kind || dt.resource?.name || 'Resource';
      const dtApiVersion = dt.item.apiVersion || dt.resource?.apiVersion || '';
      const dtName = dt.item.metadata?.name || dt.item.name;
      const dtNamespace = dt.item.metadata?.namespace || dt.item.namespace;
      const resourceMatch = dtName === resourceName && dtNamespace === namespace && dtKind === kind && dtApiVersion === apiVersion && dt.cluster === cluster && dt.location === 'detail';
      if (isPinned) return resourceMatch;
      return resourceMatch && !dt.isPinned;
    });

    if (existingTab) {
      const detailData = item || { kind: 'Resource', metadata: { name: resourceName, namespace }, name: resourceName, namespace };
      // When a preview tab is upgraded to pinned, give it the unique pinned id.
      // Preview tabs all share the constant `detail-${cluster}-preview` id, so
      // leaving it in place makes every double-click-pinned tab collide on one
      // id — closing one then closes all, and opening the next pod evicts the
      // previous one as if it were still the preview slot.
      const upgradingToPinned = isPinned && !existingTab.isPinned;
      const newId = upgradingToPinned ? `detail-${cluster}-pinned-${identity}` : existingTab.id;
      set((state) => ({
        activeTabs: state.activeTabs.map((t) =>
          t.id === cluster ? {
            ...t, state: { ...t.state, activeDetailTab: newId, isDetailsPanelCollapsed: false, detailData,
              detailTabs: t.state.detailTabs.map((dt) => dt.id === existingTab.id ? { ...dt, id: newId, item: detailData, isPinned: isPinned || dt.isPinned } : dt),
            },
          } : t
        ),
      }));
      return;
    }

    const initialData = item || { kind: 'Resource', metadata: { name: resourceName, namespace }, name: resourceName, namespace };
    let newDetailTabs = tab.state.detailTabs;
    if (!isPinned) {
      const existingPreviewIndex = tab.state.detailTabs.findIndex((dt) => !dt.isPinned && dt.location === 'detail');
      if (existingPreviewIndex >= 0) { newDetailTabs = [...tab.state.detailTabs]; newDetailTabs.splice(existingPreviewIndex, 1); }
    }

    const idSuffix = isPinned ? `pinned-${identity}` : 'preview';
    const newTab: DetailTab = { id: `detail-${cluster}-${idSuffix}`, title, resource, item: initialData, cluster, isPinned, location: 'detail' };
    const updatedDetailTabs = isPinned
      ? [...newDetailTabs.filter((t) => !t.isPinned), newTab, ...newDetailTabs.filter((t) => t.isPinned)]
      : [newTab, ...newDetailTabs];

    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === cluster ? { ...t, state: { ...t.state, detailTabs: updatedDetailTabs, activeDetailTab: newTab.id, isDetailsPanelCollapsed: false, detailData: newTab.item } } : t
      ),
    }));
  },

  closeDetailTab: (tabId: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;

    const closingTab = tab.state.detailTabs.find((dt) => dt.id === tabId);
    const newTabs = tab.state.detailTabs.filter((dt) => dt.id !== tabId);
    let newActiveTab = tab.state.activeDetailTab;
    const perPaneMap = { ...(tab.state.activeResourceListTabByPane || {}) };

    if (closingTab?.location === 'center' && closingTab.paneId) {
      if (perPaneMap[closingTab.paneId] === tabId) {
        const replacementTab = newTabs.find((dt) => dt.location === 'center' && dt.paneId === closingTab.paneId);
        perPaneMap[closingTab.paneId] = replacementTab ? replacementTab.id : null;
      }
    }

    if (tab.state.activeDetailTab === tabId) {
      const currentIndex = tab.state.detailTabs.findIndex((dt) => dt.id === tabId);
      if (newTabs.length > 0) { const newIndex = Math.min(currentIndex, newTabs.length - 1); newActiveTab = newTabs[newIndex].id; }
      else newActiveTab = null;
    }

    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? { ...t, state: { ...t.state, detailTabs: newTabs, activeDetailTab: newActiveTab, activeResourceListTabByPane: perPaneMap, detailData: newActiveTab ? newTabs.find((dt) => dt.id === newActiveTab)?.item : null } } : t
      ),
    }));
  },

  setActiveDetailTab: (tabId: string) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const detailTab = tab.state.detailTabs.find((dt) => dt.id === tabId);
    if (!detailTab) return;

    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: { ...t.state, activeDetailTab: tabId, detailData: detailTab.item,
            isDetailsPanelCollapsed: detailTab.location === 'detail' ? false : t.state.isDetailsPanelCollapsed,
          },
        } : t
      ),
    }));
    try { (window as any).__kanivetDetailEverOpened = true; } catch {}
  },

  pinDetailTab: (tabId: string) => {
    const { currentTab } = get();
    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: (() => {
            const tabToPin = t.state.detailTabs.find((dt) => dt.id === tabId);
            if (!tabToPin || tabToPin.isPinned) return t.state;
            // Regenerate the id when pinning so the formerly-shared preview id
            // doesn't collide with other pinned tabs (see openDetailTab).
            const it = tabToPin.item || {};
            const name = it.metadata?.name || it.name || 'Unknown';
            const ns = it.metadata?.namespace || it.namespace || 'default';
            const kind = it.kind || tabToPin.resource?.kind || tabToPin.resource?.name || 'Resource';
            const apiVersion = it.apiVersion || tabToPin.resource?.apiVersion || '';
            const newId = `detail-${currentTab}-pinned-${apiVersion}/${kind}/${ns}/${name}`;
            const pinnedTab = { ...tabToPin, id: newId, isPinned: true };
            const otherTabs = t.state.detailTabs.filter((dt) => dt.id !== tabId);
            const previewTabs = otherTabs.filter((dt) => !dt.isPinned);
            const pinnedTabs = otherTabs.filter((dt) => dt.isPinned);
            const activeDetailTab = t.state.activeDetailTab === tabId ? newId : t.state.activeDetailTab;
            return { ...t.state, activeDetailTab, detailTabs: [...previewTabs, pinnedTab, ...pinnedTabs] };
          })(),
        } : t
      ),
    }));
  },

  reorderDetailTabs: (fromIndex: number, toIndex: number) => {
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const newTabs = [...tab.state.detailTabs];
    const [removed] = newTabs.splice(fromIndex, 1);
    newTabs.splice(toIndex, 0, removed);
    set((state) => ({
      activeTabs: state.activeTabs.map((t) => t.id === currentTab ? { ...t, state: { ...t.state, detailTabs: newTabs } } : t),
    }));
  },

  moveDetailTab: (tabId: string, location: 'detail' | 'center', targetPaneId?: string) => {
    if (location === 'center' && !targetPaneId) console.warn('moveDetailTab called with center location but no targetPaneId, using root as fallback');
    const { activeTabs, currentTab } = get();
    const tab = activeTabs.find((t) => t.id === currentTab);
    if (!tab) return;
    const detailTab = tab.state.detailTabs.find((dt) => dt.id === tabId);
    if (!detailTab) return;

    const perPaneMap = { ...(tab.state.activeResourceListTabByPane || {}) };
    if (detailTab.location === 'center' && detailTab.paneId) {
      if (perPaneMap[detailTab.paneId] === tabId) delete perPaneMap[detailTab.paneId];
    }

    let newPaneId: string | undefined;
    if (location === 'center') {
      newPaneId = targetPaneId || tab.state.focusedCenterPaneId || 'root';
      if (newPaneId !== 'root' && tab.state.centerPaneLayout) {
        const findNode = (node: any, nodeId: string): any => {
          if (node.id === nodeId) return node;
          if (node.children) { for (const child of node.children) { const found = findNode(child, nodeId); if (found) return found; } }
          return null;
        };
        if (!findNode(tab.state.centerPaneLayout, newPaneId)) { console.warn(`Target pane ${newPaneId} not found in layout, falling back to root`); newPaneId = 'root'; }
      }
      perPaneMap[newPaneId] = tabId;
    }

    set((state) => ({
      activeTabs: state.activeTabs.map((t) =>
        t.id === currentTab ? {
          ...t, state: { ...t.state, detailTabs: t.state.detailTabs.map((dt) => dt.id === tabId ? { ...dt, location, paneId: newPaneId } : dt), activeResourceListTabByPane: perPaneMap },
        } : t
      ),
    }));
  },
});
