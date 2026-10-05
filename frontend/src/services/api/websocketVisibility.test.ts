import { afterAll, beforeEach, describe, expect, it, vi } from 'vitest';

const env = vi.hoisted(() => {
  const g = globalThis as any;
  const visibilityHandlers: Array<() => void> = [];
  const dispatched: string[] = [];
  g.window = g;
  g.location ??= { search: '' };
  g.addEventListener = () => {};
  g.dispatchEvent = (e: Event) => {
    dispatched.push(e.type);
    return true;
  };
  g.document = {
    visibilityState: 'visible',
    addEventListener: (type: string, fn: () => void) => {
      if (type === 'visibilitychange') visibilityHandlers.push(fn);
    },
  };
  g.WebSocket ??= { OPEN: 1 };
  return { visibilityHandlers, dispatched };
});

vi.mock('../../utils/logger', () => ({
  default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

const { WebSocketManager } = await import('./websocket');

describe('showing the window again', () => {
  let manager: any;
  let reconnects = 0;
  const setVisibility = (state: 'visible' | 'hidden') => {
    (document as any).visibilityState = state;
    for (const handler of env.visibilityHandlers) handler();
  };

  beforeEach(() => {
    vi.useFakeTimers();
    env.visibilityHandlers.length = 0;
    env.dispatched.length = 0;
    manager = new WebSocketManager();
    reconnects = 0;
    manager.reconnectWebSocket = () => {
      reconnects++;
    };
    manager.ws = { readyState: WebSocket.OPEN };
  });
  afterAll(() => vi.useRealTimers());

  it('refreshes nothing while the open socket kept its heartbeat', () => {
    setVisibility('hidden');
    for (let s = 0; s < 300; s += 10) {
      vi.advanceTimersByTime(10_000);
      manager.lastHeartbeat = Date.now();
    }
    setVisibility('visible');
    expect(env.dispatched).not.toContain('connection:restored');
    expect(reconnects).toBe(0);
  });

  it('reconnects a socket that went silent while hidden', () => {
    manager.lastHeartbeat = Date.now();
    setVisibility('hidden');
    vi.advanceTimersByTime(120_000);
    setVisibility('visible');
    expect(reconnects).toBe(1);
  });

  it('reconnects a socket that closed while hidden', () => {
    manager.ws = { readyState: 3 };
    setVisibility('visible');
    expect(reconnects).toBe(1);
  });
});
