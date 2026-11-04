import { create } from 'zustand';
import { useStore } from '../../store';
import type { SubjectRef } from '../../types/rbac';

export type AccessMode = 'who-can' | 'subject' | 'mine' | 'risks';

export const ACCESS_PAGE_RESOURCE = {
  name: 'access-explorer',
  group: '',
  version: 'v1',
  kind: 'AccessExplorer',
  namespaced: false,
};

export interface AccessIntent {
  mode: AccessMode;
  subject?: SubjectRef;
  namespace?: string;
  /** Bumped on every request so the page reacts even when it is open. */
  seq: number;
}

interface AccessIntentState {
  intent: AccessIntent;
  request: (i: Omit<AccessIntent, 'seq'>) => void;
}

// What the Access page should show when it next renders or is asked again.
// Kept outside the main store: it is a one-shot UI hand-off, not workspace state.
export const useAccessIntent = create<AccessIntentState>((set, get) => ({
  intent: { mode: 'who-can', seq: 0 },
  request: (i) => set({ intent: { ...i, seq: get().intent.seq + 1 } }),
}));

/** Opens (or focuses) the cluster's Access page in a given mode. */
export async function openAccessPage(cluster: string | null | undefined, intent: Omit<AccessIntent, 'seq'>): Promise<void> {
  const target = cluster || useStore.getState().currentTab;
  if (!target) return;
  useAccessIntent.getState().request(intent);
  await useStore.getState().openResourceListTab(ACCESS_PAGE_RESOURCE, target, true);
}
