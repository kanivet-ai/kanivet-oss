import { describe, expect, it } from 'vitest';
import {
  collectHelmChunk,
  finishHelmSnapshot,
  helmMountPlan,
  newHelmSnapshotCollector,
  pruneHelmReleases,
} from './helmSnapshot';

const rel = (name: string) => ({ namespace: 'ns', name, status: 'deployed' });

describe('helm snapshots', () => {
  it('a completed listing drops releases it did not include', () => {
    const shown = [rel('a'), rel('gone'), rel('b')];
    const c = newHelmSnapshotCollector();
    collectHelmChunk(c, 7, [rel('a')]);
    collectHelmChunk(c, 7, [rel('b')]);
    const keep = finishHelmSnapshot(c, 7)!;
    expect(pruneHelmReleases(shown, keep).map((r) => r.name)).toEqual(['a', 'b']);
  });

  it('an empty listing clears the list', () => {
    const c = newHelmSnapshotCollector();
    const keep = finishHelmSnapshot(c, 3)!;
    expect(pruneHelmReleases([rel('a')], keep)).toEqual([]);
  });

  it('a second Done for an applied listing changes nothing', () => {
    const c = newHelmSnapshotCollector();
    collectHelmChunk(c, 1, [rel('a')]);
    expect(finishHelmSnapshot(c, 1)).not.toBeNull();
    expect(finishHelmSnapshot(c, 1)).toBeNull();
  });

  it('a resent listing with the same id is collected afresh', () => {
    const c = newHelmSnapshotCollector();
    collectHelmChunk(c, 1, [rel('a'), rel('b')]);
    finishHelmSnapshot(c, 1);
    collectHelmChunk(c, 1, [rel('a')]);
    expect([...finishHelmSnapshot(c, 1)!]).toEqual(['ns/a']);
  });

  it('a new listing restarts collection', () => {
    const c = newHelmSnapshotCollector();
    collectHelmChunk(c, 1, [rel('a')]);
    collectHelmChunk(c, 2, [rel('b')]);
    expect([...finishHelmSnapshot(c, 2)!]).toEqual(['ns/b']);
  });

  it('keeps the same array when nothing is dropped', () => {
    const shown = [rel('a')];
    expect(pruneHelmReleases(shown, new Set(['ns/a']))).toBe(shown);
  });
});

describe('helmMountPlan', () => {
  const NOW = 1_000_000;
  it('subscribes even when the cached releases are fresh', () => {
    expect(helmMountPlan({ releases: [rel('a')], lastFetch: NOW - 5_000 }, NOW)).toEqual({ subscribe: true, showLoading: false });
  });
  it('shows the loading state without a fresh cache', () => {
    expect(helmMountPlan({ releases: [rel('a')], lastFetch: NOW - 60_000 }, NOW).showLoading).toBe(true);
    expect(helmMountPlan({ releases: [], lastFetch: NOW }, NOW).showLoading).toBe(true);
    expect(helmMountPlan(undefined, NOW)).toEqual({ subscribe: true, showLoading: true });
  });
});
