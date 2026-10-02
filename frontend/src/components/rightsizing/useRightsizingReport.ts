import { useCallback, useEffect, useRef, useState } from 'react';
import api from '../../services/api';
import { useStore } from '../../store';
import type {
  RightsizingProfile,
  RightsizingReport,
  RightsizingWindow,
} from '../../types/rightsizing';

/** While a report computes, poll often; once ready, check back occasionally so
 * an old report is refreshed by the server's own TTL. */
const POLL_COMPUTING_MS = 2_000;
const POLL_READY_MS = 60_000;

const PROFILE_KEY = 'kanivet.rightsizing.profile';
const WINDOW_KEY = 'kanivet.rightsizing.window';

function readPref<T extends string>(
  key: string,
  allowed: readonly T[],
  fallback: T,
): T {
  try {
    const v = localStorage.getItem(key) as T | null;
    return v && allowed.includes(v) ? v : fallback;
  } catch {
    return fallback;
  }
}

function writePref(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Preferences are a convenience; private windows may refuse storage.
  }
}

/** The user's risk profile and window, remembered across sessions and shared by every rightsizing view. */
export function useRightsizingPrefs() {
  const [profile, setProfileState] = useState<RightsizingProfile>(() =>
    readPref(
      PROFILE_KEY,
      ['conservative', 'balanced', 'aggressive'] as const,
      'balanced',
    ),
  );
  const [window, setWindowState] = useState<RightsizingWindow>(() =>
    readPref(WINDOW_KEY, ['7d', '14d', '28d'] as const, '14d'),
  );
  useEffect(() => {
    const sync = () => {
      setProfileState(
        readPref(
          PROFILE_KEY,
          ['conservative', 'balanced', 'aggressive'] as const,
          'balanced',
        ),
      );
      setWindowState(
        readPref(WINDOW_KEY, ['7d', '14d', '28d'] as const, '14d'),
      );
    };
    globalThis.addEventListener('rightsizing-prefs-changed', sync);
    return () =>
      globalThis.removeEventListener('rightsizing-prefs-changed', sync);
  }, []);
  const setProfile = useCallback((p: RightsizingProfile) => {
    writePref(PROFILE_KEY, p);
    globalThis.dispatchEvent(new Event('rightsizing-prefs-changed'));
  }, []);
  const setWindow = useCallback((w: RightsizingWindow) => {
    writePref(WINDOW_KEY, w);
    globalThis.dispatchEvent(new Event('rightsizing-prefs-changed'));
  }, []);
  return { profile, window, setProfile, setWindow };
}

export function useRightsizingProvider(cluster: string | undefined) {
  return useStore((s) =>
    (cluster && s.monitoringSettingsByCluster[cluster]?.preferredProvider)
      || s.monitoringSettings.preferredProvider,
  );
}

/** Last full report per cluster/profile/window, shared by every view that
 * shows one, so the dashboard, the FinOps tab and the workload card neither
 * fetch it separately nor start empty. Bounded: a few keys at most. */
const held = new Map<string, RightsizingReport>();
const inflight = new Map<string, Promise<RightsizingReport>>();
const HELD_MAX = 6;

function reportKey(
  cluster: string,
  profile: RightsizingProfile,
  window: RightsizingWindow,
  provider = 'auto',
) {
  return `${cluster}|${profile}|${window}|${provider}`;
}

/** Fetches a report, sending the version already held so an unchanged report
 * comes back without its workloads. Concurrent polls of the same report share
 * one request. */
export function loadReport(
  cluster: string,
  profile: RightsizingProfile,
  window: RightsizingWindow,
  refresh: boolean,
  provider = 'auto',
): Promise<RightsizingReport> {
  const key = reportKey(cluster, profile, window, provider);
  const pending = inflight.get(key);
  if (pending && !refresh) return pending;
  const prev = held.get(key);
  const p = api
    .getRightsizingReport(cluster, profile, window, refresh, prev?.version, provider)
    .then((r) => {
      const next = mergeReport(prev, r);
      held.delete(key);
      held.set(key, next);
      if (held.size > HELD_MAX) held.delete(held.keys().next().value!);
      return next;
    })
    .finally(() => {
      if (inflight.get(key) === p) inflight.delete(key);
    });
  inflight.set(key, p);
  return p;
}

/** Applies a poll's answer to the report held. An unchanged answer keeps the
 * held workloads; if nothing on screen would change, the held object itself is
 * returned so React skips re-rendering a table of thousands of rows. */
export function mergeReport(
  prev: RightsizingReport | undefined,
  r: RightsizingReport,
): RightsizingReport {
  if (!r.unchanged) return r;
  if (!prev) return { ...r, workloads: [], unchanged: false };
  const same =
    !r.progress &&
    !prev.progress &&
    r.status === prev.status &&
    !!r.stale === !!prev.stale &&
    (r.refreshError ?? '') === (prev.refreshError ?? '') &&
    (r.error ?? '') === (prev.error ?? '');
  if (same) return prev;
  return {
    ...r,
    workloads: prev.workloads,
    summary: prev.summary,
    unchanged: false,
  };
}

export function useRightsizingReport(
  cluster: string | undefined,
  profile: RightsizingProfile,
  window: RightsizingWindow,
) {
  const provider = useRightsizingProvider(cluster);
  const [report, setReport] = useState<RightsizingReport | null>(
    () => (cluster && held.get(reportKey(cluster, profile, window, provider))) || null,
  );
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const gen = useRef(0);

  const fetchReport = useCallback(
    async (refresh = false) => {
      if (!cluster) return;
      // A hidden window doesn't poll: nobody is looking, and every poll can
      // start a refresh that queries the metrics store.
      if (!refresh && typeof document !== 'undefined' && document.hidden) {
        if (timer.current) clearTimeout(timer.current);
        timer.current = null;
        return;
      }
      const mine = ++gen.current;
      if (timer.current) clearTimeout(timer.current);
      try {
        const r = await loadReport(cluster, profile, window, refresh, provider);
        if (mine !== gen.current) return;
        setReport(r);
        setError(null);
        const busy = r.status === 'computing' || !!r.progress;
        timer.current = setTimeout(
          () => fetchReport(false),
          busy ? POLL_COMPUTING_MS : POLL_READY_MS,
        );
      } catch (e: any) {
        if (mine !== gen.current) return;
        setError(
          e?.response?.data?.error ||
            e?.message ||
            'Failed to load rightsizing',
        );
        timer.current = setTimeout(() => fetchReport(false), POLL_READY_MS);
      }
    },
    [cluster, profile, window, provider],
  );

  useEffect(() => {
    setReport(
      (cluster && held.get(reportKey(cluster, profile, window, provider))) || null,
    );
    setError(null);
    fetchReport(false);
    const onSettings = () => fetchReport(true);
    const onVisible = () => {
      if (!document.hidden && !timer.current) fetchReport(false);
    };
    globalThis.addEventListener('metrics-settings-changed', onSettings);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      gen.current++;
      if (timer.current) clearTimeout(timer.current);
      globalThis.removeEventListener('metrics-settings-changed', onSettings);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [fetchReport]);

  return {
    report,
    error,
    refresh: () => fetchReport(true),
    reload: () => fetchReport(false),
  };
}
