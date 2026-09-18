import { beforeEach, describe, expect, it, vi } from 'vitest';

const get = vi.fn();

vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ get }) } }));
vi.mock('./websocket', () => ({ wsManager: {} }));
// The logger reads `window` at import time; vitest runs in node here.
vi.mock('../../utils/logger', () => ({
  default: { error: vi.fn(), warn: vi.fn(), info: vi.fn(), debug: vi.fn() },
}));

import { discoverMimirTenants, listMimirServices } from './metrics';

beforeEach(() => {
  get.mockReset();
});

describe('Mimir discovery surfaces failures instead of empty results', () => {
  it('listMimirServices rejects when the backend reports a probe error', async () => {
    get.mockResolvedValueOnce({
      data: { services: [], error: 'dial tcp: connection refused' },
    });
    await expect(listMimirServices('c1')).rejects.toThrow(
      'dial tcp: connection refused',
    );
  });

  it('listMimirServices returns services on a clean response', async () => {
    const services = [
      {
        type: 'mimir',
        found: true,
        namespace: 'obs',
        service: 'mimir-gw',
        url: 'http://x',
        port: 8080,
      },
    ];
    get.mockResolvedValueOnce({ data: { services } });
    await expect(listMimirServices('c1')).resolves.toEqual(services);
  });

  it('listMimirServices returns an empty list when nothing was found and no error occurred', async () => {
    get.mockResolvedValueOnce({ data: { services: [] } });
    await expect(listMimirServices('c1')).resolves.toEqual([]);
  });

  it('listMimirServices propagates transport failures', async () => {
    get.mockRejectedValueOnce(new Error('Network Error'));
    await expect(listMimirServices('c1')).rejects.toThrow('Network Error');
  });

  it('discoverMimirTenants rejects when the backend reports a probe error', async () => {
    get.mockResolvedValueOnce({
      data: { tenants: [], error: 'mimir returned 401' },
    });
    await expect(discoverMimirTenants('c1', ['acme'])).rejects.toThrow(
      'mimir returned 401',
    );
  });

  it('discoverMimirTenants returns tenants and forwards hints', async () => {
    get.mockResolvedValueOnce({ data: { tenants: ['acme', 'beta'] } });
    await expect(discoverMimirTenants('c1', ['acme', ''])).resolves.toEqual([
      'acme',
      'beta',
    ]);
    expect(get).toHaveBeenCalledWith('/metrics/tenants?cluster=c1&hint=acme');
  });
});
