import { describe, it, expect } from 'vitest';
import { applyRealtimeEvents, newRealtimeSession, itemKey } from './realtimeReducer';

const pod = (name: string, extra: any = {}) => ({ name, namespace: 'ns', ...extra });
const names = (items: any[]) => items.map((i) => i.name);

describe('applyRealtimeEvents', () => {
  it('applies add, modify, delete in arrival order', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([], [
      { action: 'added', item: pod('a', { phase: 'Pending' }) },
      { action: 'modified', item: pod('a', { phase: 'Running' }) },
      { action: 'added', item: pod('b') },
      { action: 'deleted', item: pod('b') },
    ], s);
    expect(names(r.items)).toEqual(['a']);
    expect(r.items[0].phase).toBe('Running');
  });

  it('does not leak a phantom row when add and delete land in one flush', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([pod('existing')], [
      { action: 'added', item: pod('job-pod', { uid: 'u1' }) },
      { action: 'deleted', item: pod('job-pod', { uid: 'u1' }) },
    ], s);
    expect(names(r.items)).toEqual(['existing']);
  });

  it('keeps the newest copy when add then modify land in one flush', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([], [
      { action: 'added', item: pod('a', { phase: 'Pending', resourceVersion: '1' }) },
      { action: 'modified', item: pod('a', { phase: 'Running', resourceVersion: '2' }) },
    ], s);
    expect(r.items[0].phase).toBe('Running');
  });

  it('replaces rather than merges so stale fields clear', () => {
    const s = newRealtimeSession();
    const base = [pod('a', { uid: 'old', deletionTimestamp: '2026-01-01T00:00:00Z', resourceVersion: '5' })];
    const r = applyRealtimeEvents(base, [
      { action: 'added', item: pod('a', { uid: 'new', resourceVersion: '2' }) },
    ], s);
    expect(r.items[0].deletionTimestamp).toBeUndefined();
    expect(r.items[0].uid).toBe('new');
  });

  it('ignores a stale delete for an older incarnation of a recreated pod', () => {
    const s = newRealtimeSession();
    const base = [pod('a', { uid: 'new' })];
    const r = applyRealtimeEvents(base, [
      { action: 'deleted', item: pod('a', { uid: 'old' }) },
    ], s);
    expect(names(r.items)).toEqual(['a']);
  });

  it('drops an out-of-order older update for the same object', () => {
    const s = newRealtimeSession();
    const base = [pod('a', { uid: 'u', resourceVersion: '10', phase: 'Running' })];
    const r = applyRealtimeEvents(base, [
      { action: 'modified', item: pod('a', { uid: 'u', resourceVersion: '9', phase: 'Pending' }) },
    ], s);
    expect(r.items[0].phase).toBe('Running');
  });

  it('reconciles on authoritative sync: rows absent from the epoch snapshot are dropped', () => {
    const s = newRealtimeSession();
    const base = [pod('ghost'), pod('kept')];
    const r = applyRealtimeEvents(base, [
      { action: 'added', item: pod('kept'), epoch: 3 },
      { action: 'sync', epoch: 3, itemCount: 1 },
    ], s);
    expect(names(r.items)).toEqual(['kept']);
  });

  it('authoritative empty snapshot clears the list', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([pod('ghost1'), pod('ghost2')], [
      { action: 'sync', epoch: 7, itemCount: 0 },
    ], s);
    expect(r.items).toEqual([]);
    expect(r.completedEpoch).toBe(7);
  });

  it('sync without epoch performs no reconcile', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([pod('kept')], [
      { action: 'sync' },
    ], s);
    expect(names(r.items)).toEqual(['kept']);
    expect(r.completedEpoch).toBeUndefined();
    expect(r.syncSeen).toBe(true);
  });

  it('keeps live adds that arrive after the snapshot was taken', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([], [
      { action: 'added', item: pod('snap'), epoch: 2 },
      { action: 'added', item: pod('born-mid-sync') },
      { action: 'sync', epoch: 2, itemCount: 1 },
    ], s);
    expect(names(r.items).sort()).toEqual(['born-mid-sync', 'snap']);
  });

  it('does not resurrect a row deleted after its snapshot chunk arrived', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([], [
      { action: 'added', item: pod('a', { uid: 'u' }), epoch: 2 },
      { action: 'deleted', item: pod('a', { uid: 'u' }) },
      { action: 'sync', epoch: 2, itemCount: 1 },
    ], s);
    expect(r.items).toEqual([]);
  });

  it('reconcile spanning multiple flushes still works', () => {
    const s = newRealtimeSession();
    const r1 = applyRealtimeEvents([pod('ghost')], [
      { action: 'added', item: pod('kept'), epoch: 4 },
    ], s);
    const r2 = applyRealtimeEvents(r1.items, [
      { action: 'sync', epoch: 4, itemCount: 1 },
    ], s);
    expect(names(r2.items)).toEqual(['kept']);
  });

  it('returns changed=false and the same array when nothing applied', () => {
    const base = [pod('a')];
    const s = newRealtimeSession();
    const r = applyRealtimeEvents(base, [
      { action: 'deleted', item: pod('unknown') },
    ], s);
    expect(r.changed).toBe(false);
    expect(r.items).toBe(base);
  });

  it('does not sweep when the snapshot arrived incomplete', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([pod('survivor1'), pod('survivor2')], [
      { action: 'added', item: pod('chunk1-item'), epoch: 5 },
      { action: 'sync', epoch: 5, itemCount: 3 },
    ], s);
    expect(names(r.items).sort()).toEqual(['chunk1-item', 'survivor1', 'survivor2']);
    expect(r.completedEpoch).toBeUndefined();
    expect(r.syncSeen).toBe(true);
  });

  it('sweeps normally when the received set matches the announced count', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents([pod('ghost')], [
      { action: 'added', item: pod('a'), epoch: 6 },
      { action: 'added', item: pod('b'), epoch: 6 },
      { action: 'sync', epoch: 6, itemCount: 2 },
    ], s);
    expect(names(r.items).sort()).toEqual(['a', 'b']);
    expect(r.completedEpoch).toBe(6);
  });

  it('itemKey treats empty namespace consistently', () => {
    expect(itemKey({ name: 'n' })).toBe(itemKey({ name: 'n', namespace: '' }));
  });
});
