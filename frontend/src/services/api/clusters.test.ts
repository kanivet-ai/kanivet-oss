import { describe, expect, it, vi } from 'vitest';

const post = vi.fn();

vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ post }) } }));
vi.mock('./sseFetch', () => ({ streamJsonLines: vi.fn() }));

import { getCachedBatchClusterStatus } from './clusters';

describe('getCachedBatchClusterStatus', () => {
  it('requests cached statuses without forcing a probe', async () => {
    post.mockResolvedValueOnce({ data: { statuses: { ready: { healthy: true } } } });

    await expect(getCachedBatchClusterStatus(['ready', 'missing'])).resolves.toEqual({ ready: { healthy: true } });
    expect(post).toHaveBeenCalledWith('/clusters/status/batch', { clusters: ['ready', 'missing'], cachedOnly: true });
  });
});
