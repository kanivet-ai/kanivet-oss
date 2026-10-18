import { StateCreator } from 'zustand';
import { BottomTabSlice, StoreState, BottomTab } from './types';

/** Whether two objects a bottom tab can show are the same one: name,
 * namespace, Kind and apiVersion, a side that does not know one not counting. */
export const sameBottomObject = (a: any, b: any): boolean =>
  (a?.metadata?.name || a?.name) === (b?.metadata?.name || b?.name) &&
  (a?.metadata?.namespace || a?.namespace || '') === (b?.metadata?.namespace || b?.namespace || '') &&
  (!a?.kind || !b?.kind || a.kind === b.kind) &&
  (!a?.apiVersion || !b?.apiVersion || a.apiVersion === b.apiVersion);

/**
 * Prefixes the Kind to the titles of `added` and the tabs of its type and
 * cluster titled the same, once they show objects of different Kinds
 * ("Deployment api (ns)" next to "Service api (ns)").
 */
export const withDistinctTitles = (tabs: BottomTab[], added: BottomTab): BottomTab[] => {
  const clashing = tabs.filter((t) =>
    t.type === added.type && t.cluster === added.cluster && t.title === added.title && t.resource?.kind);
  if (!clashing.some((t) => t.resource.kind !== added.resource?.kind)) return tabs;
  return tabs.map((t) => (clashing.includes(t) ? { ...t, title: `${t.resource.kind} ${t.title}` } : t));
};

export const createBottomTabSlice: StateCreator<StoreState, [], [], BottomTabSlice> = (set, get) => ({
  bottomTabs: [],
  activeBottomTab: null,

  openBottomTab: (type: 'logs' | 'shell' | 'edit' | 'trace' | 'deployment-logs' | 'create', resource: any, cluster: string) => {
    const { bottomTabs } = get();
    const resourceName = resource.metadata?.name || 'Unknown';
    const namespace = resource.metadata?.namespace;
    let title = namespace ? `${resourceName} (${namespace})` : resourceName;

    if (type === 'edit' && resource._isHelmValues) {
      title = `${resource._helmReleaseName} values (${resource._helmReleaseNamespace})`;
    } else if (type === 'shell' && resource.kind === 'Terminal') {
      const terminalCount = bottomTabs.filter((tab) => tab.type === 'shell' && tab.resource.kind === 'Terminal').length;
      title = terminalCount > 0 ? `Terminal ${terminalCount + 1}` : 'Terminal';
    } else if (type === 'create') {
      const createCount = bottomTabs.filter((tab) => tab.type === 'create').length;
      title = createCount > 0 ? `New Resource ${createCount + 1}` : 'New Resource';
    } else {
      // A Deployment and a Service (or a StatefulSet) often share a name: the
      // tab of one must not stand in for the other, or Edit saves the wrong object.
      const existingTab = bottomTabs.find((tab) =>
        tab.type === type && tab.cluster === cluster && sameBottomObject(tab.resource, resource)
      );
      if (existingTab) { set({ activeBottomTab: existingTab.id }); return; }
    }

    const kind = resource.kind || '';
    const newTab: BottomTab = {
      id: `${type}-${kind ? `${kind}-` : ''}${resourceName}-${namespace || 'default'}-${Date.now()}`,
      type, title, resource, cluster, location: 'bottom',
    };
    set({ bottomTabs: withDistinctTitles([...bottomTabs, newTab], newTab), activeBottomTab: newTab.id });
  },

  closeBottomTab: (tabId: string) => {
    const { bottomTabs, activeBottomTab, activeTabs, currentTab } = get();
    const tab = bottomTabs.find((t) => t.id === tabId);

    if (tab && tab.type === 'shell' && tab.resource.kind === 'Terminal') {
      import('../services/terminalManager').then(({ default: TerminalManager }) => { TerminalManager.getInstance().closeSession(tabId); });
    }

    const newTabs = bottomTabs.filter((t) => t.id !== tabId);
    let newActiveTab = activeBottomTab;
    if (activeBottomTab === tabId) {
      const currentIndex = bottomTabs.findIndex((t) => t.id === tabId);
      if (newTabs.length > 0) { const newIndex = Math.min(currentIndex, newTabs.length - 1); newActiveTab = newTabs[newIndex].id; }
      else newActiveTab = null;
    }

    const clusterTab = activeTabs.find((t) => t.id === currentTab);
    if (clusterTab && tab?.location === 'center' && tab.paneId) {
      const perPaneMap = { ...(clusterTab.state.activeResourceListTabByPane || {}) };
      if (perPaneMap[tab.paneId] === tabId) {
        const replacementTab = newTabs.find((bt) => bt.location === 'center' && bt.paneId === tab.paneId);
        perPaneMap[tab.paneId] = replacementTab ? replacementTab.id : null;
        get().updateCurrentTabState({ activeResourceListTabByPane: perPaneMap });
      }
    }

    set({ bottomTabs: newTabs, activeBottomTab: newActiveTab });
  },

  setActiveBottomTab: (tabId: string) => { set({ activeBottomTab: tabId }); },

  updateBottomTabContainer: (tabId: string, container: string) => {
    const { bottomTabs } = get();
    const updatedTabs = bottomTabs.map((tab) => tab.id === tabId ? { ...tab, selectedContainer: container } : tab);
    set({ bottomTabs: updatedTabs });
  },

  renameBottomTab: (tabId: string, customTitle: string) => {
    const { bottomTabs } = get();
    const updatedTabs = bottomTabs.map((tab) => tab.id === tabId ? { ...tab, customTitle } : tab);
    set({ bottomTabs: updatedTabs });
  },

  moveBottomTab: (tabId: string, location: 'bottom' | 'center', targetPaneId?: string) => {
    if (location === 'center' && !targetPaneId) console.warn('moveBottomTab called with center location but no targetPaneId, using root as fallback');
    set((state) => ({
      bottomTabs: state.bottomTabs.map((t) =>
        t.id === tabId ? { ...t, location, paneId: location === 'center' ? targetPaneId || get().getCurrentTabState()?.focusedCenterPaneId || 'root' : undefined } : t
      ),
    }));
  },
});
