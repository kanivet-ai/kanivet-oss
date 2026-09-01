import { afterEach, describe, expect, it, vi } from 'vitest';

const state = vi.hoisted(() => ({
  cleanups: [] as Array<() => void>,
  refreshSession: vi.fn(),
  startLogin: vi.fn(),
}));

vi.mock('react', () => ({
  useEffect: (effect: () => void | (() => void)) => {
    const cleanup = effect();
    if (cleanup) state.cleanups.push(cleanup);
  },
  useRef: <T>(current: T) => ({ current }),
}));

vi.mock('../store', () => ({
  useStore: () => ({
    ssoSessions: [{ startUrl: 'https://example.awsapps.com/start', region: 'eu-west-1', expiresAt: 0 }],
    loadSsoSessions: vi.fn(),
    refreshSsoSession: state.refreshSession,
    ssoSessionsLoaded: true,
  }),
}));

vi.mock('../services/cloudService', () => ({
  default: { startAWSSSOLogin: state.startLogin },
}));

import { useSSOAutoRefresh } from './useSSOAutoRefresh';

afterEach(() => {
  state.cleanups.splice(0).forEach((cleanup) => cleanup());
  state.refreshSession.mockReset();
  state.startLogin.mockReset();
  vi.useRealTimers();
});

describe('SSO auto-refresh', () => {
  it('delegates to the store refresh path instead of bypassing expiry persistence', async () => {
    vi.useFakeTimers();

    useSSOAutoRefresh();
    await Promise.resolve();

    expect(state.refreshSession).toHaveBeenCalledOnce();
    expect(state.refreshSession).toHaveBeenCalledWith('https://example.awsapps.com/start', 'eu-west-1');
    expect(state.startLogin).not.toHaveBeenCalled();
  });
});
