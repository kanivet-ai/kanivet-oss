export interface IslandOptions {
  type: 'success' | 'error' | 'info' | 'warning';
  message: string;
  icon?: string;
  duration?: number;
}

export interface IslandRules {
  cooldownMs?: number;
  backgroundOnly?: boolean;
  ignoreGlobalGap?: boolean;
}

export const createIslandGate = (deps: { now?: () => number; hasFocus?: () => boolean } = {}) => {
  const now = deps.now || (() => Date.now());
  const hasFocus = deps.hasFocus || (() => typeof document !== 'undefined' && document.hasFocus());
  const lastByKey = new Map<string, number>();
  let lastGlobal = -Infinity;
  return {
    allows(key: string, rules: IslandRules = {}): boolean {
      if (rules.backgroundOnly && hasFocus()) return false;
      const t = now();
      const last = lastByKey.get(key);
      if (last !== undefined && t - last < (rules.cooldownMs ?? 60000)) return false;
      if (!rules.ignoreGlobalGap && t - lastGlobal < 4000) return false;
      lastByKey.set(key, t);
      if (!rules.ignoreGlobalGap) lastGlobal = t;
      if (lastByKey.size > 512) {
        for (const [k, v] of lastByKey) {
          if (t - v > 300000) lastByKey.delete(k);
        }
      }
      return true;
    },
  };
};

const gate = createIslandGate();

export const islandNotify = (key: string, opts: IslandOptions, rules?: IslandRules) => {
  const island = (window as any).electronAPI?.island;
  if (!island?.notify) return;
  if (!gate.allows(key, rules)) return;
  Promise.resolve(island.notify(opts)).catch(() => {});
};

export const notifyWelcome = (clusterCount: number) =>
  islandNotify(
    'welcome',
    {
      type: 'info',
      message: clusterCount > 0 ? `Kanivet ready — ${clusterCount} cluster${clusterCount === 1 ? '' : 's'}` : 'Kanivet ready',
      icon: 'check',
      duration: 3500,
    },
    { cooldownMs: Number.MAX_SAFE_INTEGER }
  );

export const notifyRolloutComplete = (name: string) =>
  islandNotify(
    `rollout:${name}`,
    { type: 'success', message: `${name} rolled out`, icon: 'check', duration: 4000 },
    { cooldownMs: 120000 }
  );

export const notifyDrainComplete = (node: string, ok: boolean) =>
  islandNotify(
    `drain:${node}`,
    ok
      ? { type: 'success', message: `Node ${node} drained`, icon: 'check', duration: 4500 }
      : { type: 'error', message: `Drain failed on ${node}`, duration: 5000 },
    { cooldownMs: 30000 }
  );
