type Listener = () => void;

const items = new Map<string, any>();
const listeners = new Map<string, Set<Listener>>();
let generation = 0;

const scoped = (scope: string, key: string) => `${scope}\u0000${key}`;

export const rowItemBus = {
  publishList(scope: string, list: any[], getKey: (item: any) => string) {
    generation++;
    const prefix = `${scope}\u0000`;
    const seen = new Set<string>();
    for (const item of list) {
      const key = scoped(scope, getKey(item));
      seen.add(key);
      const prev = items.get(key);
      if (prev !== item) {
        items.set(key, item);
        const set = listeners.get(key);
        if (set) for (const l of set) l();
      }
    }
    for (const key of items.keys()) {
      if (key.startsWith(prefix) && !seen.has(key)) {
        items.delete(key);
        const set = listeners.get(key);
        if (set) for (const l of set) l();
      }
    }
  },
  get(scope: string, key: string) {
    return items.get(scoped(scope, key));
  },
  subscribe(scope: string, key: string, listener: Listener): () => void {
    const k = scoped(scope, key);
    let set = listeners.get(k);
    if (!set) {
      set = new Set();
      listeners.set(k, set);
    }
    set.add(listener);
    return () => {
      const s = listeners.get(k);
      if (!s) return;
      s.delete(listener);
      if (s.size === 0) listeners.delete(k);
    };
  },
  clearScope(scope: string) {
    const prefix = `${scope}\u0000`;
    for (const key of [...items.keys()]) {
      if (key.startsWith(prefix)) {
        items.delete(key);
        const set = listeners.get(key);
        if (set) for (const l of set) l();
      }
    }
  },
  generation() {
    return generation;
  },
  clear() {
    items.clear();
    for (const set of listeners.values()) for (const l of set) l();
  },
};
