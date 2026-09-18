import { describe, expect, it, vi } from 'vitest';
import { subscribeLogStream } from './logSubscription';

describe('log stream reconnection', () => {
  it('restarts with the original options and resets state before sending', () => {
    const events = new EventTarget();
    const handlers = new Map();
    const order: string[] = [];
    const transport = { getHandlers: () => handlers, sendWS: vi.fn(() => order.push('send')) };
    const reset = vi.fn(() => order.push('reset'));
    const disconnected = vi.fn();
    const handler = vi.fn();
    const payload = { key: 'stream', cluster: 'cluster', namespace: 'ns', name: 'pod', container: 'app', tailLines: 1000, follow: true };
    const stop = subscribeLogStream(transport, events, payload, handler, reset, disconnected);
    const state = new Event('connection:state');
    Object.assign(state, { detail: { type: 'websocket', state: 'disconnected' } });
    events.dispatchEvent(state);
    expect(disconnected).toHaveBeenCalledOnce();
    events.dispatchEvent(new Event('connection:restored'));
    expect(reset).toHaveBeenCalledTimes(2);
    expect(order).toEqual(['reset', 'send', 'reset', 'send']);
    expect(transport.sendWS).toHaveBeenLastCalledWith({ type: 'logs', payload: { ...payload, action: 'start' } });
    expect(handlers.get('logs').size).toBe(1);
    const visible = new Event('connection:restored');
    Object.assign(visible, { detail: { reason: 'visibility' } });
    events.dispatchEvent(visible);
    expect(reset).toHaveBeenCalledTimes(2);
    stop();
    expect(handlers.has('logs')).toBe(false);
    expect(transport.sendWS).toHaveBeenLastCalledWith({ type: 'logs', payload: { action: 'stop', key: 'stream' } });
    events.dispatchEvent(new Event('connection:restored'));
    events.dispatchEvent(state);
    expect(reset).toHaveBeenCalledTimes(2);
    expect(disconnected).toHaveBeenCalledOnce();
    expect(transport.sendWS).toHaveBeenCalledTimes(3);
  });

  it('keeps other log viewers subscribed when one closes', () => {
    const events = new EventTarget();
    const handlers = new Map();
    const transport = { getHandlers: () => handlers, sendWS: vi.fn() };
    const stop = subscribeLogStream(transport, events, { key: 'one' }, vi.fn(), vi.fn(), vi.fn());
    const remaining = vi.fn();
    const stopOther = subscribeLogStream(transport, events, { key: 'two' }, remaining, vi.fn(), vi.fn());
    stop();
    expect(handlers.get('logs')).toEqual(new Set([remaining]));
    events.dispatchEvent(new Event('connection:restored'));
    expect(transport.sendWS).toHaveBeenLastCalledWith({ type: 'logs', payload: { key: 'two', action: 'start' } });
    stopOther();
  });
});
