/**
 * List scroll positions, kept in memory while the app runs and saved to
 * localStorage so a restart reopens each list where it was scrolled to.
 * Keys start with the cluster id (`<cluster>-<list>`).
 */
const STORAGE_KEY = 'kanivet.scrollPositions';
const MAX_ENTRIES = 200;
const SAVE_DELAY_MS = 1000;

const positions = new Map<string, number>();
let loaded = false;
let saveTimer: ReturnType<typeof setTimeout> | null = null;

const load = () => {
  if (loaded) return;
  loaded = true;
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
    if (raw && typeof raw === 'object') {
      for (const [key, value] of Object.entries(raw)) {
        if (typeof value === 'number' && Number.isFinite(value) && value >= 0) positions.set(key, value);
      }
    }
  } catch {}
};

const save = () => {
  saveTimer = null;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(positions)));
  } catch {}
};

const scheduleSave = () => {
  if (saveTimer) return;
  saveTimer = setTimeout(save, SAVE_DELAY_MS);
};

export const getScrollPosition = (key: string): number | undefined => {
  load();
  return positions.get(key);
};

export const setScrollPosition = (key: string, position: number) => {
  load();
  // Re-inserting keeps the map ordered by recent use, so the oldest is evicted.
  positions.delete(key);
  positions.set(key, position);
  if (positions.size > MAX_ENTRIES) {
    const oldest = positions.keys().next().value;
    if (oldest !== undefined) positions.delete(oldest);
  }
  scheduleSave();
};

export const forgetScrollPositions = (cluster: string) => {
  load();
  let changed = false;
  for (const key of [...positions.keys()]) {
    if (key.startsWith(`${cluster}-`)) {
      positions.delete(key);
      changed = true;
    }
  }
  if (changed) scheduleSave();
};

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('beforeunload', () => {
    if (saveTimer) {
      clearTimeout(saveTimer);
      save();
    }
  });
}
