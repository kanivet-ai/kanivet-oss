import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../utils/logger', () => ({
  default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

const fetch = vi.fn();
let manager: any;

beforeEach(() => {
  vi.resetModules();
  fetch.mockReset().mockResolvedValue({ ok: true });
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('document', new EventTarget());
  // The window loaded with the URL's default port, as it does when it opens
  // before the backend has picked one.
  vi.stubGlobal(
    'window',
    Object.assign(new EventTarget(), {
      location: { search: '?backendPort=53727' },
    }),
  );
});

afterEach(() => {
  clearInterval(manager?.backendHealthInterval);
  vi.unstubAllGlobals();
});

const createManager = async (electronAPI: any) => {
  (window as any).electronAPI = electronAPI;
  const { WebSocketManager } = await import('./websocket');
  manager = new WebSocketManager();
  manager.initWebSocket = vi.fn();
  return manager;
};

const waitForBackend = async (electronAPI: any) =>
  (await createManager(electronAPI)).waitForBackend(1000);

describe('WebSocketManager and the backend port', () => {
  it('asks the main process for the backend port before polling /health', async () => {
    await expect(
      waitForBackend({ backend: { getPort: async () => 61234 } }),
    ).resolves.toBe(true);

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe('http://127.0.0.1:61234/api/v1/health');
  });

  it('keeps the URL port when there is no main process to ask (browser mode)', async () => {
    await expect(waitForBackend({ backend: {} })).resolves.toBe(true);

    expect(fetch.mock.calls[0][0]).toBe('http://127.0.0.1:53727/api/v1/health');
  });

  it('holds requests until the main process has answered with the port', async () => {
    // ThemeProvider asks for the theme while the backend is still starting.
    const answers: Array<(port: number) => void> = [];
    const getPort = () =>
      new Promise<number>((resolve) => {
        answers.push(resolve);
      });
    const created = await createManager({ backend: { getPort } });
    const { getApiBase } = await import('./types');

    const apiBase = created.waitForSessionSecret().then(() => getApiBase());
    answers.forEach((answer) => answer(61234));

    await expect(apiBase).resolves.toBe('http://127.0.0.1:61234/api/v1');
  });
});
