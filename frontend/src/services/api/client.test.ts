import { beforeEach, describe, expect, it, vi } from 'vitest';

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('axios', () => ({
  default: {
    create: () => ({
      get,
      interceptors: { request: { use: () => {} }, response: { use: () => {} } },
    }),
  },
}));
vi.mock('./websocket', () => ({ wsManager: {} }));
vi.mock('../../utils/logger', () => ({
  default: { debug: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

import { apiClient } from './client';

const deferred = () => {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

beforeEach(() => {
  get.mockReset();
  apiClient.clearCache();
});

describe('ApiClient.request', () => {
  it('sends concurrent identical cacheable GETs once', async () => {
    const response = deferred();
    get.mockReturnValue(response.promise);

    const calls = [
      apiClient.request('/resources/categories', { cluster: 'c1' }),
      apiClient.request('/resources/categories', { cluster: 'c1' }),
      apiClient.request('/resources/categories', { cluster: 'c1' }),
    ];
    response.resolve({ data: { categories: ['workloads'] } });

    await expect(Promise.all(calls)).resolves.toEqual([
      { categories: ['workloads'] },
      { categories: ['workloads'] },
      { categories: ['workloads'] },
    ]);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it('never merges uncached reads or requests with their own abort signal', async () => {
    get.mockResolvedValue({ data: [] });

    await Promise.all([
      apiClient.request('/cluster/vclusters', { cluster: 'c1' }, false),
      apiClient.request('/cluster/vclusters', { cluster: 'c1' }, false),
      apiClient.request(
        '/cluster/events',
        { cluster: 'c1' },
        true,
        new AbortController().signal,
      ),
      apiClient.request(
        '/cluster/events',
        { cluster: 'c1' },
        true,
        new AbortController().signal,
      ),
    ]);

    expect(get).toHaveBeenCalledTimes(4);
  });

  it('does not hand a request started before an invalidation to later callers', async () => {
    const stale = deferred();
    get
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce({ data: 'fresh' });

    const before = apiClient.request('/cluster/namespaces', { cluster: 'c1' });
    apiClient.invalidateCache('/cluster/namespaces');
    const after = apiClient.request('/cluster/namespaces', { cluster: 'c1' });
    stale.resolve({ data: 'stale' });

    await expect(before).resolves.toBe('stale');
    await expect(after).resolves.toBe('fresh');
    expect(get).toHaveBeenCalledTimes(2);
  });

  it('settles every caller on failure and retries on the next call', async () => {
    const failed = deferred();
    get
      .mockReturnValueOnce(failed.promise)
      .mockResolvedValueOnce({ data: 'ok' });

    const first = apiClient.request('/clusters/status');
    const second = apiClient.request('/clusters/status');
    failed.reject(new Error('backend restarting'));

    await expect(first).rejects.toThrow('backend restarting');
    await expect(second).rejects.toThrow('backend restarting');
    await expect(apiClient.request('/clusters/status')).resolves.toBe('ok');
    expect(get).toHaveBeenCalledTimes(2);
  });
});
