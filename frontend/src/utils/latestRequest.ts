/**
 * A series of requests of which only the newest may be answered, such as a
 * search run on each keystroke: starting one aborts the one before it, so a
 * slower earlier answer can never replace a later one.
 *
 * onPending, when given, hears whether a request is pending: true as one
 * starts, false once the current one finishes or is cancelled. An indicator
 * bound to it can't outlive its request, as one that only an answer cleared
 * did when the request was cancelled instead.
 */
export function createLatestRequest(onPending?: (pending: boolean) => void) {
  let current: AbortController | null = null;
  return {
    start() {
      current?.abort();
      const controller = new AbortController();
      current = controller;
      onPending?.(true);
      return {
        signal: controller.signal,
        isCurrent: () => current === controller,
        /** Marks the request answered, ending the pending state if it is
         * still the current one. */
        finish: () => {
          if (current !== controller) return;
          current = null;
          onPending?.(false);
        },
      };
    },
    /** Drops the pending request, when what it was for is no longer shown. */
    cancel() {
      current?.abort();
      current = null;
      onPending?.(false);
    },
  };
}
