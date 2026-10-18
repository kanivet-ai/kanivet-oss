import { beforeEach, describe, expect, it, vi } from 'vitest';
import { deleteResources, fetchLatestResource, getResourceDetails, getResourceEvents, removeFinalizers } from './resources';

const { request, get } = vi.hoisted(() => ({ request: vi.fn(), get: vi.fn() }));
vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ delete: request, post: request }), request: get } }));
vi.mock('../../utils/logger', () => ({ default: { error: vi.fn() } }));

describe('detail requests', () => {
  beforeEach(() => get.mockReset().mockResolvedValue([]));

  it('asks past the backend detail cache only when told to', async () => {
    await getResourceDetails('c', 'apps', 'v1', 'Deployment', 'ns', 'api');
    expect(get).toHaveBeenLastCalledWith('/cluster/resource/apps/v1/Deployment/ns/api', { cluster: 'c' }, false, undefined);
    await getResourceDetails('c', 'apps', 'v1', 'Deployment', 'ns', 'api', undefined, { fresh: true });
    expect(get).toHaveBeenLastCalledWith('/cluster/resource/apps/v1/Deployment/ns/api', { cluster: 'c', fresh: '1' }, false, undefined);
  });

  it('names the object whose events it wants by uid, Kind and apiVersion', async () => {
    await getResourceEvents('c', 'apps', 'v1', 'Deployment', 'ns', 'api', undefined, { uid: 'u1', kind: 'Deployment', apiVersion: 'apps/v1' });
    expect(get).toHaveBeenLastCalledWith(
      '/cluster/resource-events/apps/v1/Deployment/ns/api',
      { cluster: 'c', uid: 'u1', kind: 'Deployment', apiVersion: 'apps/v1' },
      false,
      undefined,
    );
  });

  it('loads an object for the editor fresh, from its apiVersion', async () => {
    await fetchLatestResource('c', { kind: 'Ingress', apiVersion: 'networking.k8s.io/v1', metadata: { name: 'web', namespace: 'ns' } });
    expect(get).toHaveBeenLastCalledWith('/cluster/resource/networking.k8s.io/v1/Ingress/ns/web', { cluster: 'c', fresh: '1' }, false, undefined);
  });
});

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
