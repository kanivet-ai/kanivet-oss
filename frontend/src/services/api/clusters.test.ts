import { describe, expect, it, vi } from 'vitest';

const { post, request, invalidateCache } = vi.hoisted(() => ({ post: vi.fn(), request: vi.fn(), invalidateCache: vi.fn() }));

vi.mock('./client', () => ({
  apiClient: {
    getAxios: () => ({ post }),
    request,
    invalidateCache,
    getCacheKey: (endpoint: string, params?: any) => `${endpoint}:${JSON.stringify(params || {})}`,
  },
}));
vi.mock('./sseFetch', () => ({ streamJsonLines: vi.fn() }));

import { getCachedBatchClusterStatus, getClusterStatus } from './clusters';

describe('getCachedBatchClusterStatus', () => {
  it('requests cached statuses without forcing a probe', async () => {
    post.mockResolvedValueOnce({ data: { statuses: { ready: { healthy: true } } } });

    await expect(getCachedBatchClusterStatus(['ready', 'missing'])).resolves.toEqual({ ready: { healthy: true } });
    expect(post).toHaveBeenCalledWith('/clusters/status/batch', { clusters: ['ready', 'missing'], cachedOnly: true });
  });
});

describe('getClusterStatus', () => {
  it('answers an unforced check from the request cache', async () => {
    request.mockResolvedValueOnce({ healthy: true });

    await expect(getClusterStatus('c1')).resolves.toEqual({ healthy: true });
    expect(request).toHaveBeenLastCalledWith('/cluster/status', { cluster: 'c1' });
    expect(invalidateCache).not.toHaveBeenCalled();
  });

  it('asks the backend on every forced check and drops the older cached answer', async () => {
    request.mockResolvedValueOnce({ healthy: false }).mockResolvedValueOnce({ healthy: true });

    // A retry right after a failed one: the second must not repeat the first.
    await expect(getClusterStatus('c1', true)).resolves.toEqual({ healthy: false });
    await expect(getClusterStatus('c1', true)).resolves.toEqual({ healthy: true });

    expect(request).toHaveBeenLastCalledWith('/cluster/status', { cluster: 'c1', force: 'true' }, false);
    expect(invalidateCache).toHaveBeenLastCalledWith('/cluster/status:{"cluster":"c1"}');
  });
});
