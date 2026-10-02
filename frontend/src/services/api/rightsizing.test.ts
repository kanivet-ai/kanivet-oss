import { describe, expect, it, vi } from 'vitest';

const get = vi.hoisted(() => vi.fn().mockResolvedValue({ data: { data: {} } }));
vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ get }) } }));

import { getRightsizingReport, getRightsizingWorkload } from './rightsizing';

describe('rightsizing requests', () => {
  it('sends the selected provider for reports and workload evidence', async () => {
    await getRightsizingReport(
      'production',
      'balanced',
      '14d',
      false,
      undefined,
      'mimir',
    );
    expect(get).toHaveBeenLastCalledWith('/rightsizing/report', {
      params: {
        cluster: 'production',
        profile: 'balanced',
        window: '14d',
        provider: 'mimir',
      },
    });
    const ref = { namespace: 'app', kind: 'Deployment', name: 'worker' };
    await getRightsizingWorkload('production', ref, 'balanced', '14d', 'mimir');
    expect(get).toHaveBeenLastCalledWith('/rightsizing/workload', {
      params: {
        cluster: 'production',
        ...ref,
        profile: 'balanced',
        window: '14d',
        provider: 'mimir',
      },
      timeout: 120_000,
    });
  });
  it('requests a cache-only preview followed by a current refresh', async () => {
    const ref = { namespace: 'app', kind: 'Deployment', name: 'worker' };
    get.mockResolvedValueOnce({ status: 204 });
    expect(
      await getRightsizingWorkload(
        'production',
        ref,
        'balanced',
        '14d',
        'mimir',
        'cached',
      ),
    ).toBeNull();
    expect(get).toHaveBeenLastCalledWith('/rightsizing/workload', {
      params: {
        cluster: 'production',
        ...ref,
        profile: 'balanced',
        window: '14d',
        provider: 'mimir',
        cached: '1',
      },
      timeout: 120_000,
    });
    await getRightsizingWorkload(
      'production',
      ref,
      'balanced',
      '14d',
      'mimir',
      'refresh',
    );
    expect(get).toHaveBeenLastCalledWith('/rightsizing/workload', {
      params: {
        cluster: 'production',
        ...ref,
        profile: 'balanced',
        window: '14d',
        provider: 'mimir',
        refresh: '1',
      },
      timeout: 120_000,
    });
  });
});
