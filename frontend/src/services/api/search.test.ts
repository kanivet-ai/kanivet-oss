import { describe, expect, it, vi } from 'vitest';
import { search } from './search';

const { get, logError } = vi.hoisted(() => ({
  get: vi.fn(),
  logError: vi.fn(),
}));
vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ get }) } }));
vi.mock('../../utils/logger', () => ({ default: { error: logError } }));

describe('search', () => {
  it('passes the abort signal to the request', async () => {
    get.mockResolvedValueOnce({
      data: { results: [{ resource: { name: 'api' } }] },
    });
    const controller = new AbortController();
    await expect(
      search('api', { limit: 5 }, controller.signal),
    ).resolves.toHaveLength(1);
    expect(get.mock.calls[0][1].signal).toBe(controller.signal);
  });

  it('does not report an aborted search as a failure', async () => {
    const controller = new AbortController();
    controller.abort();
    get.mockRejectedValueOnce(
      Object.assign(new Error('canceled'), { name: 'CanceledError' }),
    );
    await expect(search('api', {}, controller.signal)).rejects.toThrow(
      'canceled',
    );
    expect(logError).not.toHaveBeenCalled();
  });
});
