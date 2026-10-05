/**
 * Run `callback` once the renderer is idle, or after `timeout` ms at the
 * latest. Returns a function that cancels it.
 */
export function whenIdle(callback: () => void, timeout = 2000): () => void {
  if (typeof window.requestIdleCallback === 'function') {
    const handle = window.requestIdleCallback(() => callback(), { timeout });
    return () => window.cancelIdleCallback(handle);
  }
  const handle = window.setTimeout(callback, 50);
  return () => window.clearTimeout(handle);
}
