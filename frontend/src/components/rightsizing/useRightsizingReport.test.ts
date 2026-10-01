import { describe, expect, it, vi } from 'vitest';
import type { RightsizingReport } from '../../types/rightsizing';

vi.mock('../../services/api', () => ({ default: {} }));

import { mergeReport } from './useRightsizingReport';

const full = {
  status: 'ready',
  version: 'v1',
  summary: { workloads: 2 },
  workloads: [{ name: 'a' }, { name: 'b' }],
} as unknown as RightsizingReport;

describe('mergeReport', () => {
  it('replaces the held report when the answer has workloads', () => {
    const next = { ...full, version: 'v2', workloads: [] };
    expect(mergeReport(full, next)).toBe(next);
  });

  it('keeps the held object when nothing on screen changes', () => {
    const slim = {
      status: 'ready',
      version: 'v1',
      unchanged: true,
    } as RightsizingReport;
    expect(mergeReport(full, slim)).toBe(full);
  });

  it('keeps the held workloads while a refresh reports progress', () => {
    const slim = {
      status: 'ready',
      version: 'v1',
      unchanged: true,
      stale: true,
      progress: { done: 1, total: 4 },
    } as RightsizingReport;
    const merged = mergeReport(full, slim);
    expect(merged.workloads).toBe(full.workloads);
    expect(merged.summary).toBe(full.summary);
    expect(merged.progress).toEqual({ done: 1, total: 4 });
    expect(merged.unchanged).toBe(false);
  });
});
