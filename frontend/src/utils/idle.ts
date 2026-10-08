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

const INPUT_EVENTS = ['keydown', 'pointerdown'] as const;

/**
 * whenIdle for something that takes focus, such as a modal prompt: skipped
 * when a key or the pointer is pressed before it is due, so it never opens
 * over (and takes the keystrokes of) what the user has started. Returns a
 * function that cancels it.
 */
export function whenIdleUntouched(callback: () => void, timeout = 2000): () => void {
  let touched = false;
  const onInput = () => {
    touched = true;
  };
  for (const type of INPUT_EVENTS) window.addEventListener(type, onInput, true);
  const stopListening = () => {
    for (const type of INPUT_EVENTS) window.removeEventListener(type, onInput, true);
  };
  const cancel = whenIdle(() => {
    stopListening();
    if (!touched) callback();
  }, timeout);
  return () => {
    stopListening();
    cancel();
  };
}
