import { describe, expect, it } from 'vitest';
import { staleResultLabel } from './searchFreshness';

describe('staleResultLabel', () => {
  const now = Date.UTC(2026, 9, 18, 12, 0, 0);

  it('marks nothing on a current result', () => {
    expect(staleResultLabel({ stale: false, indexedAt: now - 1000 }, now)).toBeNull();
    expect(staleResultLabel({}, now)).toBeNull();
  });

  it('says how long ago a stale result was indexed', () => {
    const label = staleResultLabel({ stale: true, indexedAt: now - 3 * 24 * 3600 * 1000 }, now);
    expect(label?.text).toBe('indexed 3d ago');
  });

  it('still marks a stale result without an index time', () => {
    expect(staleResultLabel({ stale: true }, now)?.text).toBe('not refreshed');
  });
});
