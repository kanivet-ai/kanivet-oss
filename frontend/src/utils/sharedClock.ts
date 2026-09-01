type Listener = () => void;

const listeners = new Set<Listener>();
let now = nowMs();
let timer: ReturnType<typeof setInterval> | null = null;

function nowMs(): number {
  return new Date().getTime();
}

function tick(): void {
  now = nowMs();
  for (const l of listeners) l();
}

function running(): boolean {
  return typeof document === 'undefined' || document.visibilityState === 'visible';
}

function start(): void {
  if (timer || !running()) return;
  timer = setInterval(tick, 1000);
}

function stop(): void {
  if (!timer) return;
  clearInterval(timer);
  timer = null;
}

if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (running()) {
      tick();
      if (listeners.size) start();
    } else {
      stop();
    }
  });
}

export const sharedClock = {
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    start();
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) stop();
    };
  },
  getSnapshot(): number {
    return now;
  },
};
