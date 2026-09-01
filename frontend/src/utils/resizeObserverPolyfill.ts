// Polyfill to suppress ResizeObserver loop errors
export function setupResizeObserverErrorHandler() {
  if (typeof window === 'undefined' || !window.ResizeObserver) {
    return;
  }

  const OriginalResizeObserver = window.ResizeObserver;

  // Create a debounced ResizeObserver that prevents loop errors
  class DebouncedResizeObserver extends OriginalResizeObserver {
    private rafId: number | null = null;

    constructor(callback: ResizeObserverCallback) {
      const debouncedCallback: ResizeObserverCallback = (entries, observer) => {
        if (this.rafId) {
          window.cancelAnimationFrame(this.rafId);
        }

        this.rafId = window.requestAnimationFrame(() => {
          try {
            callback(entries, observer);
          } catch (error) {
            // Silently ignore errors
          }
          this.rafId = null;
        });
      };

      super(debouncedCallback);
    }

    disconnect() {
      if (this.rafId) {
        window.cancelAnimationFrame(this.rafId);
        this.rafId = null;
      }
      super.disconnect();
    }
  }

  // Replace global ResizeObserver
  (window as any).ResizeObserver = DebouncedResizeObserver;
}
