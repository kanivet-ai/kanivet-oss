import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../utils/logger', () => ({
  default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];
  readyState = 0;
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onclose: ((e: any) => void) | null = null;
  onerror: ((e: any) => void) | null = null;
  onmessage: ((e: any) => void) | null = null;
  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    if (this.readyState !== 1) throw new Error('not open');
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = 3;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  drop(code = 1006, wasClean = false) {
    this.readyState = 3;
    this.onclose?.({ code, reason: '', wasClean });
  }
  message(obj: any) {
    this.onmessage?.({ data: JSON.stringify(obj) });
  }
}

let manager: any;
let events: Array<{ type: string; detail: any }>;
const last = () => FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
const settle = () => vi.advanceTimersByTimeAsync(0);
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);
const types = (ws: FakeWebSocket) => ws.sent.map((m) => `${m.type}:${m.payload?.action ?? m.payload?.channel ?? ''}`);

const connect = async () => {
  manager.initWebSocket();
  await settle();
  last().open();
  await settle();
  return last();
};

beforeEach(async () => {
  vi.useFakeTimers();
  vi.resetModules();
  FakeWebSocket.instances = [];
  events = [];
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true }));
  vi.stubGlobal('document', Object.assign(new EventTarget(), { visibilityState: 'visible' }));
  const win: any = Object.assign(new EventTarget(), { location: { search: '?backendPort=53727' } });
  const dispatch = win.dispatchEvent.bind(win);
  win.dispatchEvent = (e: CustomEvent) => {
    events.push({ type: e.type, detail: e.detail });
    return dispatch(e);
  };
  vi.stubGlobal('window', win);
  const { WebSocketManager } = await import('./websocket');
  manager = new WebSocketManager();
  clearInterval(manager.backendHealthInterval);
  manager.backendReady = true;
  manager.lastBackendState = 'connected';
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const reconnecting = () => events.filter((e) => e.type === 'connection:state' && e.detail.state === 'reconnecting');

describe('subscriptions that must survive a reconnect', () => {
  it('replays dashboard, helm, metrics and discovery starts once after a drop', async () => {
    const ws = await connect();
    manager.sendWS({ type: 'dashboard', payload: { action: 'start', cluster: 'c1' } });
    manager.sendWS({ type: 'helm', payload: { action: 'subscribe', cluster: 'c1' } });
    manager.sendWS({ type: 'metrics', payload: { action: 'start', cluster: 'c1', pod: 'p', metricType: 'cpu', streamingRate: 2 } });
    manager.sendWS({ type: 'cloud.discover', payload: { action: 'start', key: 'k1', provider: 'aws' } });
    expect(ws.sent).toHaveLength(4);

    ws.drop();
    await tick(1100);
    const next = last();
    expect(next).not.toBe(ws);
    next.open();
    await settle();

    expect(types(next).sort()).toEqual(['cloud.discover:start', 'dashboard:start', 'helm:subscribe', 'metrics:start']);
  });

  it('does not replay a stream stopped during the outage', async () => {
    const ws = await connect();
    manager.sendWS({ type: 'dashboard', payload: { action: 'start', cluster: 'c1' } });
    manager.sendWS({ type: 'metrics', payload: { action: 'start', cluster: 'c1', pod: 'p', metricType: 'cpu', streamingRate: 2 } });
    ws.drop();
    manager.sendWS({ type: 'dashboard', payload: { action: 'stop', cluster: 'c1' } });
    manager.sendWS({ type: 'metrics', payload: { action: 'stop', cluster: 'c1', pod: 'p', metricType: 'cpu' } });
    await tick(1100);
    const next = last();
    next.open();
    await settle();
    expect(next.sent).toEqual([]);
  });

  it('forgets a replayable start of a cluster that was closed', async () => {
    const ws = await connect();
    manager.sendWS({ type: 'dashboard', payload: { action: 'start', cluster: 'c1' } });
    manager.sendWS({ type: 'helm', payload: { action: 'subscribe', cluster: 'c2' } });
    manager.unregisterActiveCluster('c1');
    ws.drop();
    await tick(1100);
    last().open();
    await settle();
    expect(types(last())).toEqual(['helm:subscribe']);
  });

  it('can forget a finished discovery so it is not run again', async () => {
    const ws = await connect();
    const msg = { type: 'cloud.discover', payload: { action: 'start', key: 'k1' } };
    manager.sendWS(msg);
    manager.forgetReplay(msg);
    ws.drop();
    await tick(1100);
    last().open();
    await settle();
    expect(last().sent).toEqual([]);
  });
});

describe('offline outbox', () => {
  it('keeps no timer per queued message and flushes once on open', async () => {
    for (let i = 0; i < 51; i++) manager.sendWS({ type: 'logs', payload: { action: 'start', key: 'l1' } });
    await tick(5000);
    // Only the connection attempt's own timers exist.
    expect(vi.getTimerCount()).toBeLessThan(5);
    last().open();
    await settle();
    expect(types(last())).toEqual(['logs:start']);
  });

  it('cancels a subscribe whose unsubscribe came before the socket opened', async () => {
    manager.sendWS({ type: 'logs', payload: { action: 'start', key: 'l1' } });
    manager.sendWS({ type: 'logs', payload: { action: 'stop', key: 'l1' } });
    manager.sendWS({ type: 'logs', payload: { action: 'start', key: 'l2' } });
    await settle();
    last().open();
    await settle();
    expect(last().sent.map((m) => m.payload.key)).toEqual(['l2']);
  });

  it('sends an item subscribe queued offline once, with the latest sort', async () => {
    manager.registerActiveCluster('c1');
    manager.subscribeToItems('c1', '', 'v1', 'Pod', 'ns', () => {}, 'name', 'asc');
    manager.subscribeToItems('c1', '', 'v1', 'Pod', 'ns', undefined, 'age', 'desc');
    await settle();
    last().open();
    await settle();
    const subs = last().sent.filter((m) => m.type === 'subscribe');
    expect(subs).toHaveLength(1);
    expect(subs[0].payload.sortBy).toBe('age');
  });

  it('does not subscribe a topic whose consumer left during the outage', async () => {
    manager.registerActiveCluster('c1');
    const ws = await connect();
    const onEvent = () => {};
    const topic = manager.subscribeToItems('c1', '', 'v1', 'Pod', '', onEvent);
    ws.drop();
    manager.subscribeToItems('c1', '', 'v1', 'Secret', '', onEvent);
    manager.unsubscribe(topic, onEvent);
    manager.unsubscribe('items:c1::v1:Secret:', onEvent);
    await tick(1100);
    last().open();
    await settle();
    expect(last().sent).toEqual([]);
  });

  it('bounds the outbox', async () => {
    for (let i = 0; i < 2000; i++) manager.sendWS({ type: 'logs', payload: { action: 'start', key: `k${i}` } });
    await settle();
    last().open();
    await settle();
    expect(last().sent.length).toBeLessThanOrEqual(500);
    expect(last().sent[last().sent.length - 1].payload.key).toBe('k1999');
  });

  it('still sends straight away while connected', async () => {
    const ws = await connect();
    manager.sendWS({ type: 'logs', payload: { action: 'start', key: 'l1' } });
    expect(ws.sent).toHaveLength(1);
  });
});

describe('a reconnect sends every list snapshot request once', () => {
  it('does not resubscribe a topic that a connection:restored handler already restarted', async () => {
    manager.registerActiveCluster('c1');
    const ws = await connect();
    const onEvent = () => {};
    manager.subscribeToItems('c1', '', 'v1', 'Pod', '', onEvent);
    manager.subscribeToItems('c1', '', 'v1', 'Secret', '', onEvent);
    ws.drop();
    await tick(1100);
    // Like Layout: close everything, subscribe the on-screen topic again.
    window.addEventListener('connection:restored', () => {
      manager.unsubscribe('items:c1::v1:Pod:', onEvent);
      manager.unsubscribe('items:c1::v1:Secret:', onEvent);
      manager.subscribeToItems('c1', '', 'v1', 'Pod', '', onEvent);
    });
    last().open();
    await settle();

    const subs = last().sent.filter((m) => m.type === 'subscribe').map((m) => m.payload.kind);
    expect(subs).toEqual(['Pod']);
  });

  it('resubscribes every live topic once when nothing restarts them', async () => {
    manager.registerActiveCluster('c1');
    const ws = await connect();
    manager.subscribeToItems('c1', '', 'v1', 'Pod', '', () => {});
    manager.subscribeToCounts('c1', () => {});
    ws.drop();
    await tick(1100);
    last().open();
    await settle();
    const subs = last().sent.filter((m) => m.type === 'subscribe');
    expect(subs).toHaveLength(2);
    expect(events.filter((e) => e.type === 'connection:restored')).toHaveLength(1);
  });

  it('tells listeners about the restore before replaying, so caches are cleared first', async () => {
    manager.registerActiveCluster('c1');
    const ws = await connect();
    manager.subscribeToItems('c1', '', 'v1', 'Pod', '', () => {});
    ws.drop();
    await tick(1100);
    let sentAtRestore = -1;
    window.addEventListener('connection:restored', () => {
      sentAtRestore = last().sent.length;
    });
    last().open();
    await settle();
    expect(sentAtRestore).toBe(0);
  });
});

describe('reconnect delay', () => {
  it('retries after about a second, then backs off up to 30s', async () => {
    const ws = await connect();
    vi.spyOn(Math, 'random').mockReturnValue(1);
    ws.drop();
    expect(reconnecting().pop()!.detail.countdown).toBe(1);
    await tick(900);
    expect(FakeWebSocket.instances).toHaveLength(1);
    await tick(200);
    expect(FakeWebSocket.instances).toHaveLength(2);

    const delays: number[] = [];
    for (let i = 0; i < 8; i++) {
      last().drop();
      delays.push(reconnecting().pop()!.detail.countdown);
      await tick(31_000);
    }
    // Browser mode: the third handshake failure parks the retry at 30s.
    expect(delays.slice(0, 1)).toEqual([2]);
    expect(Math.max(...delays)).toBeLessThanOrEqual(30);
  });

  it('spreads retries with jitter', async () => {
    const ws = await connect();
    ws.drop();
    await tick(1100);
    vi.spyOn(Math, 'random').mockReturnValue(0);
    last().drop();
    expect(reconnecting().pop()!.detail.countdown).toBe(1);
    await tick(1100);
    expect(FakeWebSocket.instances).toHaveLength(3);
  });

  it('starts again from a fast retry after a successful open', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(1);
    let ws = await connect();
    for (let i = 0; i < 2; i++) {
      ws.drop(1000, true);
      await tick(31_000);
      ws = last();
    }
    expect(reconnecting().pop()!.detail.countdown).toBeGreaterThan(1);
    ws.open();
    await settle();
    ws.drop(1000, true);
    expect(reconnecting().pop()!.detail.countdown).toBe(1);
  });

  it('keeps one retry timer however often a send kicks the connection', async () => {
    const ws = await connect();
    ws.drop();
    manager.ensureWSReady();
    await settle();
    expect(FakeWebSocket.instances.length).toBeLessThanOrEqual(2);
  });
});

describe('handshake failures', () => {
  it('waits out the backoff before reconnecting with a refreshed secret', async () => {
    (window as any).electronAPI = { security: { getSessionSecret: async () => 's' } };
    manager.sessionSecret = 's';
    const ws = await connect();
    ws.drop();
    await tick(31_000);
    last().drop();
    await tick(31_000);
    const before = FakeWebSocket.instances.length;
    last().drop();
    await settle();
    // The refresh path no longer reconnects on the spot.
    expect(FakeWebSocket.instances).toHaveLength(before);
    await tick(31_000);
    expect(FakeWebSocket.instances.length).toBeGreaterThan(before);
  });

  it('keeps a slow retry going in browser mode and toasts once', async () => {
    const ws = await connect();
    ws.drop();
    for (let i = 0; i < 10; i++) {
      await tick(31_000);
      last().drop();
    }
    await settle();
    expect(events.filter((e) => e.type === 'toast:error')).toHaveLength(1);
    expect(events.filter((e) => e.type === 'session:invalid').length).toBeGreaterThanOrEqual(1);
    const before = FakeWebSocket.instances.length;
    await tick(31_000);
    expect(FakeWebSocket.instances.length).toBeGreaterThan(before);
  });
});

describe('heartbeat', () => {
  it('counts any message as a sign of life', async () => {
    const ws = await connect();
    manager.lastHeartbeat = 0;
    ws.message({ type: 'bulk_list', topic: 'x', items: [] });
    expect(Date.now() - manager.lastHeartbeat).toBeLessThan(1000);
  });
});
