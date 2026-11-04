import { useEffect, useSyncExternalStore } from 'react';
// The API client is loaded on first use: the sidebar imports this store, and
// the tree's tests run without a window for the client's logger.
const loadApi = () => import('../../services/api/health');
import type { HealthIssuesReport } from '../../types/health';

/** How often a watched cluster's issues are re-read. */
export const ISSUES_POLL_MS = 20_000;

export interface IssuesState {
  report: HealthIssuesReport | null;
  loading: boolean;
  error: string | null;
  updatedAt: number;
}

const EMPTY: IssuesState = { report: null, loading: false, error: null, updatedAt: 0 };

interface Entry {
  state: IssuesState;
  listeners: Set<() => void>;
  watchers: number;
  timer: ReturnType<typeof setInterval> | null;
  inflight: Promise<void> | null;
}

const entries = new Map<string, Entry>();

function entry(cluster: string): Entry {
  let e = entries.get(cluster);
  if (!e) {
    e = { state: EMPTY, listeners: new Set(), watchers: 0, timer: null, inflight: null };
    entries.set(cluster, e);
  }
  return e;
}

function set(cluster: string, patch: Partial<IssuesState>) {
  const e = entry(cluster);
  e.state = { ...e.state, ...patch };
  e.listeners.forEach((l) => l());
}

/** Re-reads the cluster's issues now. One read at a time per cluster. */
export function refreshIssues(cluster: string, force = false): Promise<void> {
  const e = entry(cluster);
  if (e.inflight) return e.inflight;
  set(cluster, { loading: true });
  e.inflight = loadApi().then((m) => m.getHealthIssues(cluster, { refresh: force }))
    .then((report) => set(cluster, { report, error: null, loading: false, updatedAt: Date.now() }))
    .catch((err: any) => set(cluster, { error: err?.response?.data?.error || err?.message || 'Failed to load issues', loading: false }))
    .finally(() => { e.inflight = null; });
  return e.inflight;
}

function watch(cluster: string) {
  const e = entry(cluster);
  e.watchers++;
  if (e.watchers === 1) {
    if (Date.now() - e.state.updatedAt > ISSUES_POLL_MS / 2) void refreshIssues(cluster);
    e.timer = setInterval(() => {
      if (typeof document !== 'undefined' && document.hidden) return;
      void refreshIssues(cluster);
    }, ISSUES_POLL_MS);
  }
  return () => {
    e.watchers--;
    if (e.watchers === 0 && e.timer) {
      clearInterval(e.timer);
      e.timer = null;
    }
  };
}

/**
 * The cluster's live issues. Every consumer (sidebar badge, Issues page,
 * overview card, inspector) shares one poll per cluster.
 */
export function useIssues(cluster: string | null | undefined): IssuesState {
  const key = cluster || '';
  const state = useSyncExternalStore(
    (l) => {
      if (!key) return () => {};
      const e = entry(key);
      e.listeners.add(l);
      return () => { e.listeners.delete(l); };
    },
    () => (key ? entry(key).state : EMPTY),
  );
  useEffect(() => (key ? watch(key) : undefined), [key]);
  return state;
}
