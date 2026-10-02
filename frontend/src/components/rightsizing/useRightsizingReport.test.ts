import { describe, expect, it, vi } from 'vitest';
import type { RightsizingReport } from '../../types/rightsizing';

const mocks = vi.hoisted(() => ({
  getReport: vi.fn(),
  state: {
    monitoringSettings: { preferredProvider: 'prometheus' },
    monitoringSettingsByCluster: { production: { preferredProvider: 'mimir' } },
  },
}));
vi.mock('../../services/api', () => ({ default: { getRightsizingReport: mocks.getReport } }));
vi.mock('../../store', () => ({ useStore: (select: (s: typeof mocks.state) => unknown) => select(mocks.state) }));

import { loadReport, mergeReport, useRightsizingProvider } from './useRightsizingReport';

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


describe('rightsizing provider selection', () => {
  it('uses the cluster override and falls back to monitoring defaults', () => {
    expect(useRightsizingProvider('production')).toBe('mimir');
    expect(useRightsizingProvider('other')).toBe('prometheus');
  });

  it('keeps cached reports and concurrent requests separate by provider', async () => {
    mocks.getReport.mockResolvedValueOnce(full);
    await loadReport('cache-test', 'balanced', '14d', false, 'prometheus');
    expect(mocks.getReport).toHaveBeenLastCalledWith('cache-test', 'balanced', '14d', false, undefined, 'prometheus');

    let complete!: (r: RightsizingReport) => void;
    mocks.getReport.mockImplementationOnce(() => new Promise<RightsizingReport>((resolve) => { complete = resolve; }));
    const mimir = loadReport('cache-test', 'balanced', '14d', false, 'mimir');
    expect(mocks.getReport).toHaveBeenLastCalledWith('cache-test', 'balanced', '14d', false, undefined, 'mimir');
    expect(loadReport('cache-test', 'balanced', '14d', false, 'mimir')).toBe(mimir);

    mocks.getReport.mockResolvedValueOnce({ ...full, unchanged: true });
    const prometheus = loadReport('cache-test', 'balanced', '14d', false, 'prometheus');
    expect(prometheus).not.toBe(mimir);
    expect(mocks.getReport).toHaveBeenLastCalledWith('cache-test', 'balanced', '14d', false, 'v1', 'prometheus');
    complete({ ...full, version: 'mimir-v1' });
    expect((await mimir).version).toBe('mimir-v1');
    expect(await prometheus).toBe(full);
  });
});
