import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { anyCountPending, countsSettled, refetchCountsLater } from './countRefetch';

describe('countRefetch', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    countsSettled('k');
    vi.useRealTimers();
  });

  it('chases only the counts the backend may still read', () => {
    expect(anyCountPending([{ count: 0 }, { count: 12 }] as any)).toBe(false);
    // Not installed or not allowed: absent, and asking again will not help.
    expect(anyCountPending([{ count: 3 }, { count: null }] as any)).toBe(false);
    expect(anyCountPending([{ count: 3 }, { count: null, countPending: true }] as any)).toBe(true);
  });

  it('asks again soon, then less often, until the counts are known', () => {
    const run = vi.fn(() => refetchCountsLater('k', run));
    refetchCountsLater('k', run);
    vi.advanceTimersByTime(1_999);
    expect(run).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(run).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(4_999);
    expect(run).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    expect(run).toHaveBeenCalledTimes(2);

    // Past the last step it settles at once a minute.
    vi.advanceTimersByTime(10_000 + 20_000 + 30_000 + 60_000);
    expect(run).toHaveBeenCalledTimes(6);
    vi.advanceTimersByTime(60_000);
    expect(run).toHaveBeenCalledTimes(7);

    countsSettled('k');
    vi.advanceTimersByTime(600_000);
    expect(run).toHaveBeenCalledTimes(7);
  });

  it('keeps one schedule per key', () => {
    const run = vi.fn();
    refetchCountsLater('k', run);
    refetchCountsLater('k', run);
    vi.advanceTimersByTime(5_000);
    expect(run).toHaveBeenCalledTimes(1);
  });
});
