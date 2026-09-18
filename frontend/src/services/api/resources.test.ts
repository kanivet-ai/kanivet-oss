import { beforeEach, describe, expect, it, vi } from 'vitest';
import { deleteResources, removeFinalizers } from './resources';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ delete: request, post: request }) } }));
vi.mock('../../utils/logger', () => ({ default: { error: vi.fn() } }));

describe.each([
  ['delete', deleteResources, { deleted: 1 }, { deleted: 0, errors: ['Forbidden'] }],
  ['remove finalizers', removeFinalizers, { success: 1, failed: 0 }, { success: 0, failed: 1, errors: ['Forbidden'] }],
] as const)('%s results', (_name, action, success, failure) => {
  beforeEach(() => vi.clearAllMocks());

  it('reports HTTP 206 operation failures as failures', async () => {
    request.mockResolvedValueOnce({ status: 206, data: failure });
    await expect(action('cluster', '', 'v1', 'pods', [{ namespace: 'ns', name: 'denied' }])).resolves.toEqual({ succeeded: 0, failed: ['ns/denied'] });
  });

  it('counts successful operations separately from API and transport failures', async () => {
    request.mockResolvedValueOnce({ status: 200, data: success });
    request.mockResolvedValueOnce({ status: 206, data: failure });
    request.mockRejectedValueOnce(new Error('Network error'));
    const items = ['ok', 'denied', 'offline'].map(name => ({ name }));
    await expect(action('cluster', '', 'v1', 'pods', items)).resolves.toEqual({ succeeded: 1, failed: ['denied', 'offline'] });
  });
});
