import { StateCreator } from 'zustand';
import api from '../services/api';
import { FocusArea } from '../types';
import { NavigationSlice, StoreState, NavigationEntry } from './types';

export const createNavigationSlice: StateCreator<StoreState, [], [], NavigationSlice> = (_set, get) => ({
  recordNavigation: async (type: string, path: string, resource: any = null, item: any = null) => {
    const { currentTab } = get();
    if (!currentTab) return;
    await api.addNavigationEntry(currentTab, currentTab, { type, path, resource, item });
  },

  navigateBack: async () => {
    const { currentTab } = get();
    if (!currentTab) return;
    try {
      const entry = await api.navigateBack(currentTab);
      if (entry) await get().restoreNavigationState(entry);
    } catch (error) { console.error('Failed to navigate back:', error); }
  },

  navigateForward: async () => {
    const { currentTab } = get();
    if (!currentTab) return;
    try {
      const entry = await api.navigateForward(currentTab);
      if (entry) await get().restoreNavigationState(entry);
    } catch (error) { console.error('Failed to navigate forward:', error); }
  },

  restoreNavigationState: async (entry: NavigationEntry) => {
    const { currentTab } = get();
    if (!currentTab) return;

    if (entry.type === 'resource' && entry.resource) {
      get().updateCurrentTabState({ selectedNode: { id: '', label: entry.resource.name, type: 'resource', data: entry.resource }, selectedItem: null, detailData: null, focusArea: 'list' });
      await get().loadListItems(currentTab, entry.resource);
      if (entry.item) {
        get().updateCurrentTabState({ selectedItem: entry.item });
        await get().loadDetails(currentTab, entry.resource, entry.item);
      }
    } else if (entry.type === 'item' && entry.item && entry.resource) {
      get().updateCurrentTabState({ selectedNode: { id: '', label: entry.resource.name, type: 'resource', data: entry.resource }, selectedItem: entry.item, focusArea: 'list' });
      await get().loadListItems(currentTab, entry.resource);
      await get().loadDetails(currentTab, entry.resource, entry.item);
    }
  },

  setFocusArea: (area: FocusArea) => { get().updateCurrentTabState({ focusArea: area }); },

  toggleDetailsPanel: () => {
    const tabState = get().getCurrentTabState();
    if (tabState) get().updateCurrentTabState({ isDetailsPanelCollapsed: !tabState.isDetailsPanelCollapsed });
  },

  setDetailsPanelCollapsed: (collapsed: boolean) => { get().updateCurrentTabState({ isDetailsPanelCollapsed: collapsed }); },
});
