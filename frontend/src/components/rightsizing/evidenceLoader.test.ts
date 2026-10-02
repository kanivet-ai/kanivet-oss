import { describe, expect, it, vi } from 'vitest';
import type { Evidence } from '../../types/rightsizing';
const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('../../services/api', () => ({
  default: { getRightsizingWorkload: request },
}));
import {
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
    });
    fresh.resolve(snapshot('new'));
    await flush();
    expect(states[states.length - 1]).toEqual({
      evidence: snapshot('new'),
      refreshing: false,
      error: null,
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
