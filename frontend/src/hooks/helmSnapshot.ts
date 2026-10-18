/**
 * Helm release listings arrive as chunks carrying a snapshot id, ended by a
 * Done frame with the same id. A listing is authoritative: on its Done, the
 * releases it did not include are gone (uninstalled while the stream was down
 * or before a relist), so the client replaces its list rather than merging.
 */
export interface HelmSnapshotCollector {
  id: number | null;
  keys: Set<string>;
  open: boolean;
}

export const newHelmSnapshotCollector = (): HelmSnapshotCollector => ({ id: null, keys: new Set(), open: false });

export const helmReleaseKey = (r: { namespace?: string; name?: string }) => `${r.namespace}/${r.name}`;

/** Notes the releases of a chunk of listing `id`. */
export function collectHelmChunk(c: HelmSnapshotCollector, id: number, releases: any[]): void {
  if (!c.open || c.id !== id) {
    c.id = id;
    c.keys = new Set();
    c.open = true;
  }
  for (const r of releases) c.keys.add(helmReleaseKey(r));
}

/**
 * Ends listing `id`. Returns the keys it held, which are all the releases
 * there are, or null when this Done ends a listing already applied.
 */
export function finishHelmSnapshot(c: HelmSnapshotCollector, id: number): Set<string> | null {
  if (c.id === id && !c.open) return null;
  // A listing with no chunk for this client held no releases.
  const keys = c.id === id ? c.keys : new Set<string>();
  c.id = id;
  c.keys = new Set();
  c.open = false;
  return keys;
}

/** The releases a completed listing kept, or the same array when it kept them all. */
export function pruneHelmReleases<T extends { namespace?: string; name?: string }>(releases: T[], keep: Set<string>): T[] {
  const kept = releases.filter((r) => keep.has(helmReleaseKey(r)));
  return kept.length === releases.length ? releases : kept;
}

/** Cached releases younger than this are shown without a loading state. */
export const HELM_FRESH_CACHE_MS = 30_000;

/**
 * What a Helm page mounting does with the releases cached for its cluster.
 * It always subscribes: the page unsubscribed when it last unmounted, so
 * without one the cached list would never change again. A fresh cache only
 * spares the loading state while the snapshot arrives.
 */
export function helmMountPlan(
  cached: { releases?: any[]; lastFetch?: number } | undefined,
  now = Date.now(),
): { subscribe: true; showLoading: boolean } {
  const fresh = !!cached?.releases?.length && !!cached.lastFetch && now - cached.lastFetch < HELM_FRESH_CACHE_MS;
  return { subscribe: true, showLoading: !fresh };
}
