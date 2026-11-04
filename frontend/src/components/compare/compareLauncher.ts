import { useSyncExternalStore } from 'react';
import { useStore } from '../../store';
import { pageResources } from '../../store/navigationTargets';
import type { CompareRef } from '../../types/compare';

/*
  The Compare tab's inputs. Entry points (inspector action, list bulk action,
  palette) call openCompare(); the tab reads the request with
  useCompareRequest(). One Compare tab per workspace: a new request replaces
  what it shows. The last pair is remembered across restarts.
*/
export interface CompareRequest {
  a: CompareRef | null;
  b: CompareRef | null;
  /** Bumped on every openCompare so the tab refetches even for the same pair. */
  seq: number;
}

const STORAGE_KEY = 'kanivet.compare.last';

const load = (): CompareRequest => {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const v = JSON.parse(raw);
      return { a: v.a ?? null, b: v.b ?? null, seq: 0 };
    }
  } catch { /* storage unavailable */ }
  return { a: null, b: null, seq: 0 };
};

let current: CompareRequest = load();
const listeners = new Set<() => void>();

const emit = () => listeners.forEach((l) => l());

export const getCompareRequest = () => current;

export const setCompareRefs = (a: CompareRef | null, b: CompareRef | null, bump = false) => {
  current = { a, b, seq: bump ? current.seq + 1 : current.seq };
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ a, b })); } catch { /* ignore */ }
  emit();
};

export const useCompareRequest = (): CompareRequest =>
  useSyncExternalStore(
    (l) => { listeners.add(l); return () => { listeners.delete(l); }; },
    () => current,
  );

/** Open (or focus) the Compare tab, optionally pre-filled. Omitted sides keep what the tab had only when nothing is given. */
export const openCompare = (req: { a?: CompareRef | null; b?: CompareRef | null } = {}) => {
  if (req.a !== undefined || req.b !== undefined) {
    setCompareRefs(req.a ?? null, req.b ?? null, true);
  }
  const { currentTab, openResourceListTab } = useStore.getState();
  const cluster = req.a?.cluster || currentTab;
  if (!cluster) return;
  void openResourceListTab(pageResources.compare, currentTab || cluster, true);
};
