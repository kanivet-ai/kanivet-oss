import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Evidence } from '../../types/rightsizing';
const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('../../services/api/rightsizing', () => ({
  getRightsizingWorkload: request,
}));
import {
  busyRetryMs,
  loadEvidence,
  type EvidenceQuery,
  type EvidenceState,
} from './evidenceLoader';

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const snapshot = (asOf: string) => ({ asOf }) as Evidence;
const query = (name: string): EvidenceQuery => ({
  cluster: 'production',
  provider: 'mimir',
  profile: 'balanced',
  window: '14d',
  ref: { namespace: 'apps', kind: 'Deployment', name },
});
const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

function requests() {
  const cached = deferred<Evidence | null>();
  const fresh = deferred<Evidence | null>();
  request.mockImplementation((...args) =>
    args[5] === 'cached' ? cached.promise : fresh.promise,
  );
  return { cached, fresh };
}

describe('progressive workload evidence', () => {
  it('shows cached history while upstream is pending, then replaces it and reuses it on reopen', async () => {
    const { cached, fresh } = requests();
    const states: EvidenceState[] = [];
    const q = query('progressive');
    const stop = loadEvidence(q, (s) => states.push(s));
    cached.resolve(snapshot('old'));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('old'),
      refreshing: true,
      error: null,
      busy: null,
    });
    fresh.resolve(snapshot('new'));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('new'),
      refreshing: false,
      error: null,
      busy: null,
    });
    stop();
    const next = requests();
    const reopened: EvidenceState[] = [];
    const close = loadEvidence(q, (s) => reopened.push(s));
    expect(reopened[0].evidence?.asOf).toBe('new');
    next.fresh.resolve(snapshot('newer'));
    await flush();
    expect(reopened[reopened.length - 1]?.evidence?.asOf).toBe('newer');
    close();
  });

  it('does not overwrite a fresh result with a slow cache response', async () => {
    const { cached, fresh } = requests();
    const states: EvidenceState[] = [];
    const stop = loadEvidence(query('ordering'), (s) => states.push(s));
    fresh.resolve(snapshot('new'));
    await flush();
    cached.resolve(snapshot('old'));
    await flush();
    expect(states[states.length - 1]?.evidence?.asOf).toBe('new');
    stop();
  });

  it('retains cached history when refresh fails, including a later cache response', async () => {
    const { cached, fresh } = requests();
    const states: EvidenceState[] = [];
    const stop = loadEvidence(query('offline'), (s) => states.push(s));
    fresh.reject(new Error('offline'));
    await flush();
    cached.resolve(snapshot('old'));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('old'),
      refreshing: false,
      error: 'offline',
      busy: null,
    });
    stop();
  });

  it('handles cache misses and ignores updates after closing or changing workloads', async () => {
    const { cached, fresh } = requests();
    const states: EvidenceState[] = [];
    const stop = loadEvidence(query('closed'), (s) => states.push(s));
    cached.resolve(null);
    await flush();
    expect(states[states.length - 1]?.refreshing).toBe(true);
    stop();
    const count = states.length;
    fresh.resolve(snapshot('new'));
    await flush();
    expect(states).toHaveLength(count);
  });

  it('isolates providers and deduplicates simultaneous upstream refreshes', async () => {
    const { cached, fresh } = requests();
    request.mockClear();
    const q = query('isolation');
    const states: EvidenceState[] = [];
    const stop = loadEvidence(q, (s) => states.push(s));
    const stop2 = loadEvidence(q, () => {});
    expect(
      request.mock.calls.filter((args) => args[5] === 'refresh'),
    ).toHaveLength(1);
    cached.resolve(snapshot('mimir'));
    fresh.resolve(snapshot('mimir'));
    await flush();
    const other = requests();
    const second: EvidenceState[] = [];
    const stop3 = loadEvidence({ ...q, provider: 'prometheus' }, (s) =>
      second.push(s),
    );
    expect(second[0].evidence).toBeNull();
    other.cached.resolve(null);
    other.fresh.resolve(snapshot('prometheus'));
    await flush();
    expect(second[second.length - 1]?.evidence?.asOf).toBe('prometheus');
    stop();
    stop2();
    stop3();
  });
});

/** Requests that settle on demand and fail as axios does when aborted. */
function abortable() {
  const calls: {
    mode: string;
    signal: AbortSignal;
    settle: ReturnType<typeof deferred<Evidence | null>>;
  }[] = [];
  request.mockImplementation((...args) => {
    const settle = deferred<Evidence | null>();
    const signal = args[6] as AbortSignal;
    signal.addEventListener('abort', () =>
      settle.reject(
        Object.assign(new Error('canceled'), { name: 'CanceledError' }),
      ),
    );
    calls.push({ mode: args[5], signal, settle });
    return settle.promise;
  });
  const refreshes = () => calls.filter((c) => c.mode === 'refresh');
  return { calls, refreshes };
}

const busyError = (retryAfterSeconds?: number) =>
  Object.assign(new Error('Request failed with status code 503'), {
    response: {
      status: 503,
      headers: {},
      data: {
        error: 'Metrics store paused for maintenance',
        retryAfterSeconds,
      },
    },
  });

describe('abandoned workload evidence', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('cancels the refresh once no view waits for it', async () => {
    vi.useFakeTimers();
    const { calls, refreshes } = abortable();
    const stop = loadEvidence(query('abandoned'), () => {});
    expect(calls.map((c) => c.mode)).toEqual(['cached', 'refresh']);
    stop();
    // The cache read is the view's own; the refresh gets a moment's grace.
    expect(calls[0].signal.aborted).toBe(true);
    expect(refreshes()[0].signal.aborted).toBe(false);
    vi.advanceTimersByTime(1_000);
    expect(refreshes()[0].signal.aborted).toBe(true);
    await flush();
    // Reopening starts afresh rather than joining the cancelled request.
    const states: EvidenceState[] = [];
    const again = loadEvidence(query('abandoned'), (s) => states.push(s));
    expect(refreshes()).toHaveLength(2);
    expect(states[states.length - 1].error).toBeNull();
    again();
  });

  it('keeps a refresh another view, or the same one remounting, still needs', async () => {
    vi.useFakeTimers();
    const { refreshes } = abortable();
    const q = query('shared');
    const first = loadEvidence(q, () => {});
    const states: EvidenceState[] = [];
    const second = loadEvidence(q, (s) => states.push(s));
    first();
    vi.advanceTimersByTime(5_000);
    expect(refreshes()).toHaveLength(1);
    expect(refreshes()[0].signal.aborted).toBe(false);
    // StrictMode unmounts and remounts at once: the refresh carries on.
    second();
    const third = loadEvidence(q, (s) => states.push(s));
    vi.advanceTimersByTime(5_000);
    expect(refreshes()).toHaveLength(1);
    expect(refreshes()[0].signal.aborted).toBe(false);
    refreshes()[0].settle.resolve(snapshot('shared'));
    await flush();
    expect(states[states.length - 1]?.evidence?.asOf).toBe('shared');
    third();
  });
});

describe('a paused metrics store', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('keeps the cached evidence, says why, and retries once after Retry-After', async () => {
    vi.useFakeTimers();
    const { calls, refreshes } = abortable();
    const states: EvidenceState[] = [];
    const stop = loadEvidence(query('paused'), (s) => states.push(s));
    calls[0].settle.resolve(snapshot('cached'));
    refreshes()[0].settle.reject(busyError(7));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('cached'),
      refreshing: true,
      error: null,
      busy: 'Metrics store paused for maintenance',
    });
    vi.advanceTimersByTime(6_999);
    expect(refreshes()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(refreshes()).toHaveLength(2);
    refreshes()[1].settle.resolve(snapshot('fresh'));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('fresh'),
      refreshing: false,
      error: null,
      busy: null,
    });
    stop();
  });

  it('retries only once and never shows the raw request error', async () => {
    vi.useFakeTimers();
    const { refreshes } = abortable();
    const states: EvidenceState[] = [];
    const stop = loadEvidence(query('still-paused'), (s) => states.push(s));
    refreshes()[0].settle.reject(busyError());
    await flush();
    vi.advanceTimersByTime(10_000);
    refreshes()[1].settle.reject(busyError(5));
    await flush();
    vi.advanceTimersByTime(60_000);
    expect(refreshes()).toHaveLength(2);
    expect(states[states.length - 1]).toMatchObject({
      refreshing: false,
      error: 'Metrics store paused for maintenance',
      busy: null,
    });
    stop();
  });

  it('cancels a pending retry when the view closes', async () => {
    vi.useFakeTimers();
    const { refreshes } = abortable();
    const stop = loadEvidence(query('closed-while-paused'), () => {});
    refreshes()[0].settle.reject(busyError(3));
    await flush();
    stop();
    vi.advanceTimersByTime(60_000);
    expect(refreshes()).toHaveLength(1);
  });

  it('reads the delay from the body or the header, bounded', () => {
    const res = (data: object, headers: object = {}) => ({
      response: { status: 503, data, headers },
    });
    expect(busyRetryMs(res({ retryAfterSeconds: 30 }))).toBe(30_000);
    expect(busyRetryMs(res({}, { 'retry-after': '4' }))).toBe(4_000);
    expect(busyRetryMs(res({}))).toBe(10_000);
    expect(busyRetryMs(res({ retryAfterSeconds: 3600 }))).toBe(120_000);
    expect(busyRetryMs({ response: { status: 500 } })).toBeNull();
    expect(busyRetryMs(new Error('offline'))).toBeNull();
  });
});
