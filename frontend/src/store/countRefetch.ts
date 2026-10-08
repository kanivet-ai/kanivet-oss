/**
 * A count the backend could not read (expired credentials, a slow or
 * unreachable API server) comes back marked pending. Rather than leave the
 * sidebar waiting for the next time its category is expanded, the fetch is
 * repeated until none is pending: quickly at first, then once a minute, and at
 * once when credentials change or the network returns.
 */
const DELAYS_MS = [2_000, 5_000, 10_000, 20_000, 30_000, 60_000];

interface Pending {
  timer: ReturnType<typeof setTimeout>;
  attempt: number;
  run: () => void;
}

const pending = new Map<string, Pending>();

const fire = (key: string) => {
  const entry = pending.get(key);
  if (!entry) return;
  clearTimeout(entry.timer);
  // The entry stays until the run reports back, so its attempt is kept.
  entry.run();
};

/** Repeats run after a delay that grows with each call for the same key. */
export function refetchCountsLater(key: string, run: () => void): void {
  const previous = pending.get(key);
  if (previous) clearTimeout(previous.timer);
  const attempt = previous ? previous.attempt + 1 : 0;
  const delay = DELAYS_MS[Math.min(attempt, DELAYS_MS.length - 1)];
  pending.set(key, { attempt, run, timer: setTimeout(() => fire(key), delay) });
}

/** No count is pending, or nobody is looking: stop repeating. */
export function countsSettled(key: string): void {
  const entry = pending.get(key);
  if (!entry) return;
  clearTimeout(entry.timer);
  pending.delete(key);
}

/**
 * True when the backend could not read some count this time but may on the
 * next try. A count it will never have (a CRD that is not installed, a
 * resource the user may not list) is not marked, and is not asked for again.
 */
export function anyCountPending(resources: Array<{ countPending?: boolean }>): boolean {
  return resources.some((res) => res.countPending === true);
}

const refetchNow = () => {
  for (const key of Array.from(pending.keys())) fire(key);
};

if (typeof window !== 'undefined') {
  window.addEventListener('cloud:auth-changed', refetchNow);
  window.addEventListener('online', refetchNow);
}
