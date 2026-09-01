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
}

export interface ApplyResult {
  items: any[];
  map: Map<string, any>;
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

const rvNum = (item: any) => {
  const rv = item?.resourceVersion;
  if (!rv) return 0;
  const n = parseInt(rv, 10);
  return Number.isNaN(n) ? 0 : n;
};

export function applyRealtimeEvents(baseItems: any[], events: RealtimeEvent[], s: RealtimeSession): ApplyResult {
  const map = new Map<string, any>();
  for (const item of baseItems) map.set(itemKey(item), item);
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
          for (const key of [...map.keys()]) {
            if (!seen.has(key) && (s.touch.get(key) || 0) < started) {
              map.delete(key);
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

    const sameObject = !prev || !item.uid || !prev.uid || item.uid === prev.uid;
    if (prev && sameObject) {
      const prevRv = rvNum(prev);
      const nextRv = rvNum(item);
      if (prevRv && nextRv && nextRv < prevRv) {
        s.touch.set(key, s.seq);
        continue;
      }
    }
    map.set(key, item);
    changed = true;
    s.touch.set(key, s.seq);
  }

  if (s.touch.size > map.size * 2 + 512) {
    for (const key of [...s.touch.keys()]) {
      if (!map.has(key)) s.touch.delete(key);
    }
  }

  return {
    items: changed ? [...map.values()] : baseItems,
    map,
    changed,
    completedEpoch,
    syncSeen,
  };
}
