import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { whenIdleUntouched } from './idle';

describe('whenIdleUntouched', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // A renderer without requestIdleCallback: whenIdle falls back to a timer.
    vi.stubGlobal(
      'window',
      Object.assign(new EventTarget(), {
        setTimeout: (cb: () => void, ms: number) => setTimeout(cb, ms),
        clearTimeout: (h: any) => clearTimeout(h),
      }),
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('runs once idle when nothing was pressed', () => {
    const open = vi.fn();
    whenIdleUntouched(open, 3000);
    vi.advanceTimersByTime(3000);
    expect(open).toHaveBeenCalledTimes(1);
  });

  // The star prompt is a modal that traps focus: opening it after the user
  // started typing (the command palette, the tree search) would take the
  // rest of their keystrokes.
  it('is skipped once a key or the pointer was pressed before it was due', () => {
    for (const type of ['keydown', 'pointerdown']) {
      const open = vi.fn();
      whenIdleUntouched(open, 3000);
      window.dispatchEvent(new Event(type));
      vi.advanceTimersByTime(3000);
      expect(open).not.toHaveBeenCalled();
    }
  });

  it('can be cancelled', () => {
    const open = vi.fn();
    whenIdleUntouched(open, 3000)();
    vi.advanceTimersByTime(3000);
    expect(open).not.toHaveBeenCalled();
  });
});
