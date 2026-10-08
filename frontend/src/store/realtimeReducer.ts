export interface RealtimeEvent {
  action: 'added' | 'modified' | 'deleted' | 'sync';
  item?: any;
  epoch?: number;
  itemCount?: number;
}

export interface RealtimeSession {
  seq: number;
  touch: Map<string, number>;
  epochStart: Map<number, number>;
  epochSeen: Map<number, Set<string>>;
  stopped?: boolean;
  // The list the last call returned and each key's position in it. Kept across
  // calls so a batch costs O(events), not O(list); rebuilt whenever the list
  // passed in is not the one this session returned last.
  items?: any[];
  index?: Map<string, number>;
}

export interface ApplyResult {
  items: any[];
  changed: boolean;
  completedEpoch?: number;
  syncSeen: boolean;
}

export const newRealtimeSession = (): RealtimeSession => ({
  seq: 0,
  touch: new Map(),
  epochStart: new Map(),
  epochSeen: new Map(),
});

export const itemKey = (item: any) => `${item?.namespace || ''}/${item?.name || ''}`;

// The listing still on its way, if there is one: its epoch and how many of
// its rows have arrived. A listing is under way from its first page until
// the sync that closes it.
export const pendingListing = (s: RealtimeSession): { epoch: number; loaded: number } | null => {
  let epoch = 0;
  for (const e of s.epochSeen.keys()) if (e > epoch) epoch = e;
  return epoch ? { epoch, loaded: s.epochSeen.get(epoch)!.size } : null;
};

const rvNum = (item: any) => {
  const rv = item?.resourceVersion;
  if (!rv) return 0;
  const n = parseInt(rv, 10);
  return Number.isNaN(n) ? 0 : n;
};

// Fills a deleted slot until the end of the batch, when the list is compacted.
const REMOVED = Symbol('removed');

const reindex = (s: RealtimeSession, baseItems: any[]) => {
  const index = new Map<string, number>();
  let items = baseItems;
  for (let i = 0; i < baseItems.length; i++) {
    const key = itemKey(baseItems[i]);
    const pos = index.get(key);
    if (pos === undefined) {
      index.set(key, items === baseItems ? i : items.length);
      if (items !== baseItems) items.push(baseItems[i]);
    } else {
      // A repeated key keeps its first position and its last value.
      if (items === baseItems) items = baseItems.slice(0, i);
      items[pos] = baseItems[i];
    }
  }
  s.items = items;
  s.index = index;
};

// The object stored under key in items, found through the session's index.
export const findItem = (
  s: RealtimeSession,
  items: any[],
  key: string,
): any => {
  if (s.items !== items || !s.index) reindex(s, items);
  const pos = s.index!.get(key);
  return pos === undefined ? undefined : s.items![pos];
};

export function applyRealtimeEvents(
  baseItems: any[],
  events: RealtimeEvent[],
  s: RealtimeSession,
): ApplyResult {
  if (s.items !== baseItems || !s.index) reindex(s, baseItems);
  const index = s.index!;
  const base = s.items!;
  // Copy of the list, made on the first change; the input is never mutated.
  let next: any[] | null = null;
  let removed = 0;
  const remove = (key: string, pos: number) => {
    next ??= base.slice();
    next[pos] = REMOVED;
    index.delete(key);
    removed++;
  };
  let changed = false;
  let completedEpoch: number | undefined;
  let syncSeen = false;

  for (const ev of events) {
    if (ev.action === 'sync') {
      syncSeen = true;
      const e = ev.epoch || 0;
      if (e > 0) {
        const seen = s.epochSeen.get(e) || new Set<string>();
        const started = s.epochStart.get(e) ?? s.seq + 1;
        const complete = typeof ev.itemCount !== 'number' || seen.size >= ev.itemCount;
        if (complete) {
          for (const [key, pos] of [...index]) {
            if (!seen.has(key) && (s.touch.get(key) || 0) < started) {
              remove(key, pos);
              changed = true;
            }
          }
          completedEpoch = e;
        }
        for (const k of [...s.epochSeen.keys()]) {
          if (k <= e) {
            s.epochSeen.delete(k);
            s.epochStart.delete(k);
          }
        }
      }
      continue;
    }

    const item = ev.item;
    if (!item || !item.name) continue;
    const key = itemKey(item);
    s.seq++;

    if (ev.epoch) {
      if (!s.epochStart.has(ev.epoch)) {
        s.epochStart.set(ev.epoch, s.seq);
        s.epochSeen.set(ev.epoch, new Set());
      }
      s.epochSeen.get(ev.epoch)!.add(key);
    }

    const pos = index.get(key);
    const prev = pos === undefined ? undefined : (next ?? base)[pos];
    if (ev.action === 'deleted') {
      if (prev) {
        if (item.uid && prev.uid && item.uid !== prev.uid) continue;
        remove(key, pos!);
        changed = true;
        s.touch.set(key, s.seq);
      }
      continue;
    }

    const sameObject = !prev || !item.uid || !prev.uid || item.uid === prev.uid;
    if (prev && sameObject) {
      const prevRv = rvNum(prev);
      const nextRv = rvNum(item);
      if (prevRv && nextRv && nextRv < prevRv) {
        s.touch.set(key, s.seq);
        continue;
      }
    }
    // An equal resourceVersion still replaces: the backend sends a minimal
    // projection first and the full one after it, both at the same version.
    next ??= base.slice();
    if (pos === undefined) {
      // New keys, including one deleted earlier in this batch, go last.
      index.set(key, next.length);
      next.push(item);
    } else {
      next[pos] = item;
    }
    changed = true;
    s.touch.set(key, s.seq);
  }

  if (removed > 0) {
    const list = next!;
    let w = 0;
    for (let r = 0; r < list.length; r++) {
      const item = list[r];
      if (item === REMOVED) continue;
      if (w !== r) {
        list[w] = item;
        index.set(itemKey(item), w);
      }
      w++;
    }
    list.length = w;
  }

  if (s.touch.size > index.size * 2 + 512) {
    for (const key of [...s.touch.keys()]) {
      if (!index.has(key)) s.touch.delete(key);
    }
  }

  if (!changed) return { items: baseItems, changed, completedEpoch, syncSeen };
  s.items = next!;
  return { items: next!, changed, completedEpoch, syncSeen };
}
