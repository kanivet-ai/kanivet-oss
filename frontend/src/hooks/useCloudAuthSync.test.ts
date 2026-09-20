import { afterEach, describe, expect, it, vi } from 'vitest';

// The hook only needs window as an event bus; give the node environment one.
if (typeof window === 'undefined') {
  (globalThis as any).window = new EventTarget();
}

const state = vi.hoisted(() => ({
  cleanups: [] as Array<() => void>,
  loadAuthSummary: vi.fn(),
  beginLogin: vi.fn(),
  islandNotify: vi.fn(),
  sessions: [] as any[],
  authLoaded: true,
}));

vi.mock('react', () => {
  const react = {
    useEffect: (effect: () => void | (() => void)) => {
      const cleanup = effect();
      if (cleanup) state.cleanups.push(cleanup);
    },
    useRef: <T>(current: T) => ({ current }),
  };
  return { ...react, default: react };
});

vi.mock('../store', () => ({
  useStore: (selector: (s: any) => any) =>
    selector({
      loadAuthSummary: state.loadAuthSummary,
      authLoaded: state.authLoaded,
      ssoSessions: state.sessions,
    }),
}));

vi.mock('../services/islandNotifications', () => ({
  islandNotify: state.islandNotify,
}));

vi.mock('../services/cloudService', () => ({
  default: { beginAWSSSOLogin: state.beginLogin },
}));

import { useCloudAuthSync } from './useCloudAuthSync';

afterEach(() => {
  state.cleanups.splice(0).forEach((cleanup) => cleanup());
  state.loadAuthSummary.mockReset();
  state.beginLogin.mockReset();
  state.islandNotify.mockReset();
  vi.useRealTimers();
});

describe('cloud auth sync', () => {
  it('never starts an interactive login on its own, even for expired sessions', async () => {
    vi.useFakeTimers();
    state.sessions = [
      {
        startUrl: 'https://example.awsapps.com/start',
        region: 'eu-west-1',
        state: 'expired',
        expiresAt: 0,
      },
    ];

    useCloudAuthSync();
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);

    expect(state.beginLogin).not.toHaveBeenCalled();
    expect(state.loadAuthSummary).toHaveBeenCalledWith(true);
  });

  it('re-reads the summary when the backend announces an auth change', () => {
    state.sessions = [];
    useCloudAuthSync();
    state.loadAuthSummary.mockClear();

    window.dispatchEvent(
      new CustomEvent('cloud:auth-changed', { detail: { provider: 'aws' } }),
    );

    expect(state.loadAuthSummary).toHaveBeenCalledWith(true);
  });
});
