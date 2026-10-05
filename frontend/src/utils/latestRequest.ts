/**
 * A series of requests of which only the newest may be answered, such as a
 * search run on each keystroke: starting one aborts the one before it, so a
 * slower earlier answer can never replace a later one.
 */
export function createLatestRequest() {
  let current: AbortController | null = null;
  return {
    start() {
      current?.abort();
      const controller = new AbortController();
      current = controller;
      return {
        signal: controller.signal,
        isCurrent: () => current === controller,
      };
    },
    /** Drops the pending request, when what it was for is no longer shown. */
    cancel() {
      current?.abort();
      current = null;
    },
  };
}
