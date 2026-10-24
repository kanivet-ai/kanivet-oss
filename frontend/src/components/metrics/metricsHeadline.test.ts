import { describe, expect, it } from 'vitest';
import { staleLabel } from './MetricsHeadline';

describe('staleLabel', () => {
  it('says when a stale value was last live', () => {
    const at = new Date(2026, 9, 18, 14, 2).getTime();
    expect(staleLabel(at)).toMatch(/^As of .*02/);
  });
  it('says the value is not updating without a time', () => {
    expect(staleLabel(undefined)).toBe('Not updating');
  });
});
