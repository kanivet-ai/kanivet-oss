import { describe, it, expect, vi } from 'vitest';
import { formatAge, formatAgeSince } from './formatters';

describe('formatAgeSince', () => {
  const t = Date.parse('2026-10-01T00:00:00Z');

  it('uses the largest whole unit', () => {
    expect(formatAgeSince(t, t + 42_000)).toBe('42s');
    expect(formatAgeSince(t, t + 5 * 60_000)).toBe('5m');
    expect(formatAgeSince(t, t + 3 * 3_600_000 + 59_000)).toBe('3h');
    expect(formatAgeSince(t, t + 2 * 86_400_000)).toBe('2d');
  });

  it('gives the same text on most clock ticks, so a live age rarely re-renders', () => {
    const now = t + 3 * 86_400_000;
    let changes = 0;
    for (let s = 1; s <= 60; s++) {
      if (
        formatAgeSince(t, now + s * 1000) !==
        formatAgeSince(t, now + (s - 1) * 1000)
      )
        changes++;
    }
    expect(changes).toBe(0);
  });

  it('formatAge measures from the current time', () => {
    vi.useFakeTimers();
    vi.setSystemTime(t + 90_000);
    expect(formatAge('2026-10-01T00:00:00Z')).toBe('1m');
    expect(formatAge('')).toBe('-');
    vi.useRealTimers();
  });
});
