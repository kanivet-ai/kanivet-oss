import { describe, it, expect } from 'vitest';
import {
  applyRealtimeEvents,
  newRealtimeSession,
  itemKey,
  findItem,
} from './realtimeReducer';

const pod = (name: string, extra: any = {}) => ({
  name,
  namespace: 'ns',
  ...extra,
});
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

  it('keeps a modified row in place and puts new and re-added rows last', () => {
    const s = newRealtimeSession();
    const r1 = applyRealtimeEvents(
      [pod('a'), pod('b'), pod('c')],
      [
        { action: 'modified', item: pod('a', { phase: 'Failed' }) },
        { action: 'added', item: pod('d') },
      ],
      s,
    );
    expect(names(r1.items)).toEqual(['a', 'b', 'c', 'd']);
    const r2 = applyRealtimeEvents(
      r1.items,
      [
        { action: 'deleted', item: pod('b') },
        { action: 'added', item: pod('b') },
        { action: 'deleted', item: pod('c') },
      ],
      s,
    );
    expect(names(r2.items)).toEqual(['a', 'd', 'b']);
    expect(r2.items[0].phase).toBe('Failed');
  });

  it('never mutates the list it was given', () => {
    const s = newRealtimeSession();
    const base = [pod('a'), pod('b')];
    const r1 = applyRealtimeEvents(
      base,
      [{ action: 'modified', item: pod('a', { phase: 'Running' }) }],
      s,
    );
    const r2 = applyRealtimeEvents(
      r1.items,
      [{ action: 'deleted', item: pod('a') }],
      s,
    );
    expect(names(base)).toEqual(['a', 'b']);
    expect(base[0].phase).toBeUndefined();
    expect(names(r1.items)).toEqual(['a', 'b']);
    expect(names(r2.items)).toEqual(['b']);
  });

  it('a repeated key keeps its first position and its last value', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents(
      [pod('a', { v: 1 }), pod('b'), pod('a', { v: 2 })],
      [{ action: 'added', item: pod('c') }],
      s,
    );
    expect(names(r.items)).toEqual(['a', 'b', 'c']);
    expect(r.items[0].v).toBe(2);
  });

  it('touches only the changed rows once the list is indexed', () => {
    let reads = 0;
    const counted = (name: string) => {
      const item: any = { namespace: 'ns' };
      Object.defineProperty(item, 'name', {
        get: () => {
          reads++;
          return name;
        },
        enumerable: true,
      });
      return item;
    };
    const s = newRealtimeSession();
    const base = Array.from({ length: 5000 }, (_, i) => counted(`p${i}`));
    const r1 = applyRealtimeEvents(
      base,
      [{ action: 'modified', item: pod('p10') }],
      s,
    );
    reads = 0;
    const r2 = applyRealtimeEvents(
      r1.items,
      [
        { action: 'modified', item: pod('p20') },
        { action: 'added', item: pod('new') },
      ],
      s,
    );
    expect(reads).toBeLessThan(10);
    expect(r2.items).toHaveLength(5001);
    // A list replaced from outside is indexed again.
    const replaced = [pod('x'), pod('p20')];
    const r3 = applyRealtimeEvents(
      replaced,
      [{ action: 'deleted', item: pod('p20') }],
      s,
    );
    expect(names(r3.items)).toEqual(['x']);
  });

  it('findItem returns the stored object for a key', () => {
    const s = newRealtimeSession();
    const r = applyRealtimeEvents(
      [pod('a')],
      [{ action: 'modified', item: pod('a', { phase: 'Running' }) }],
      s,
    );
    expect(findItem(s, r.items, itemKey(pod('a'))).phase).toBe('Running');
    expect(findItem(s, r.items, itemKey(pod('zz')))).toBeUndefined();
    const other = [pod('b')];
    expect(findItem(s, other, itemKey(pod('b')))).toBe(other[0]);
  });

  it('matches a full rebuild over random batches', () => {
    // The reducer as it was before it kept an index: rebuild a Map per call.
    const reference = (baseItems: any[], events: any[], s: any) => {
      const map = new Map<string, any>();
      for (const item of baseItems) map.set(itemKey(item), item);
      let changed = false;
      for (const ev of events) {
        if (ev.action === 'sync') {
          const e = ev.epoch || 0;
          if (e > 0) {
            const seen = s.epochSeen.get(e) || new Set<string>();
            const started = s.epochStart.get(e) ?? s.seq + 1;
            if (typeof ev.itemCount !== 'number' || seen.size >= ev.itemCount) {
              for (const key of [...map.keys()]) {
                if (!seen.has(key) && (s.touch.get(key) || 0) < started) {
                  map.delete(key);
                  changed = true;
                }
              }
            }
            for (const k of [...s.epochSeen.keys()])
              if (k <= e) {
                s.epochSeen.delete(k);
                s.epochStart.delete(k);
              }
          }
          continue;
        }
        const item = ev.item;
        const key = itemKey(item);
        s.seq++;
        if (ev.epoch) {
          if (!s.epochStart.has(ev.epoch)) {
            s.epochStart.set(ev.epoch, s.seq);
            s.epochSeen.set(ev.epoch, new Set());
          }
          s.epochSeen.get(ev.epoch).add(key);
        }
        const prev = map.get(key);
        if (ev.action === 'deleted') {
          if (prev) {
            if (item.uid && prev.uid && item.uid !== prev.uid) continue;
            map.delete(key);
            changed = true;
            s.touch.set(key, s.seq);
          }
          continue;
        }
        if (prev && (!item.uid || !prev.uid || item.uid === prev.uid)) {
          const p = parseInt(prev.resourceVersion, 10) || 0;
          const n = parseInt(item.resourceVersion, 10) || 0;
          if (p && n && n < p) {
            s.touch.set(key, s.seq);
            continue;
          }
        }
        map.set(key, item);
        changed = true;
        s.touch.set(key, s.seq);
      }
      return changed ? [...map.values()] : baseItems;
    };

    let seed = 7;
    const rand = (n: number) => {
      seed = (seed * 1103515245 + 12345) % 2147483648;
      return seed % n;
    };
    const actions = ['added', 'modified', 'modified', 'deleted'] as const;
    const s = newRealtimeSession();
    const sRef = newRealtimeSession();
    let items: any[] = [];
    let expected: any[] = [];
    let rv = 1;
    for (let batch = 0; batch < 300; batch++) {
      const events: any[] = [];
      const epoch = batch % 40 === 0 ? batch + 1 : undefined;
      const count = 1 + rand(12);
      for (let k = 0; k < count; k++) {
        const name = `p${rand(30)}`;
        const action = epoch ? 'added' : actions[rand(actions.length)];
        events.push({
          action,
          item: pod(name, {
            uid: `${name}-${rand(2)}`,
            resourceVersion: String(rv++ - rand(3)),
          }),
          epoch,
        });
      }
      if (epoch)
        events.push({
          action: 'sync',
          epoch,
          itemCount: rand(2)
            ? new Set(events.map((e) => e.item.name)).size
            : 999,
        });
      const r = applyRealtimeEvents(items, events, s);
      expected = reference(expected, events, sRef);
      expect(r.items).toEqual(expected);
      items = r.items;
    }
  });
});
