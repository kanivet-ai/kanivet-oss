import { beforeEach, describe, expect, it, vi } from 'vitest';

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('axios', () => ({
  default: {
    create: () => ({
      get,
      interceptors: { request: { use: () => {} }, response: { use: () => {} } },
    }),
  },
}));
vi.mock('./websocket', () => ({ wsManager: {} }));
vi.mock('../../utils/logger', () => ({
  default: { debug: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios';
import {
  LONG_REQUEST_SLOTS,
  RequestGate,
  apiClient,
  gateLongRequests,
} from './client';

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

const deferred = () => {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

beforeEach(() => {
  get.mockReset();
  apiClient.clearCache();
});

describe('ApiClient.request', () => {
  it('sends concurrent identical cacheable GETs once', async () => {
    const response = deferred();
    get.mockReturnValue(response.promise);

    const calls = [
      apiClient.request('/resources/categories', { cluster: 'c1' }),
      apiClient.request('/resources/categories', { cluster: 'c1' }),
      apiClient.request('/resources/categories', { cluster: 'c1' }),
    ];
    response.resolve({ data: { categories: ['workloads'] } });

    await expect(Promise.all(calls)).resolves.toEqual([
      { categories: ['workloads'] },
      { categories: ['workloads'] },
      { categories: ['workloads'] },
    ]);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it('never merges uncached reads or requests with their own abort signal', async () => {
    get.mockResolvedValue({ data: [] });

    await Promise.all([
      apiClient.request('/cluster/vclusters', { cluster: 'c1' }, false),
      apiClient.request('/cluster/vclusters', { cluster: 'c1' }, false),
      apiClient.request(
        '/cluster/events',
        { cluster: 'c1' },
        true,
        new AbortController().signal,
      ),
      apiClient.request(
        '/cluster/events',
        { cluster: 'c1' },
        true,
        new AbortController().signal,
      ),
    ]);

    expect(get).toHaveBeenCalledTimes(4);
  });

  it('does not hand a request started before an invalidation to later callers', async () => {
    const stale = deferred();
    get
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce({ data: 'fresh' });

    const before = apiClient.request('/cluster/namespaces', { cluster: 'c1' });
    apiClient.invalidateCache('/cluster/namespaces');
    const after = apiClient.request('/cluster/namespaces', { cluster: 'c1' });
    stale.resolve({ data: 'stale' });

    await expect(before).resolves.toBe('stale');
    await expect(after).resolves.toBe('fresh');
    expect(get).toHaveBeenCalledTimes(2);
  });

  it('settles every caller on failure and retries on the next call', async () => {
    const failed = deferred();
    get
      .mockReturnValueOnce(failed.promise)
      .mockResolvedValueOnce({ data: 'ok' });

    const first = apiClient.request('/clusters/status');
    const second = apiClient.request('/clusters/status');
    failed.reject(new Error('backend restarting'));

    await expect(first).rejects.toThrow('backend restarting');
    await expect(second).rejects.toThrow('backend restarting');
    await expect(apiClient.request('/clusters/status')).resolves.toBe('ok');
    expect(get).toHaveBeenCalledTimes(2);
  });
});

describe('long requests', () => {
  it('leave four of the six connections Chromium allows per host', () => {
    expect(6 - LONG_REQUEST_SLOTS).toBeGreaterThanOrEqual(4);
  });

  it('run at most two at a time, in the order they were made', async () => {
    const gate = new RequestGate(2);
    const replies = Array.from({ length: 5 }, () => deferred());
    const started: number[] = [];
    let running = 0;
    let peak = 0;
    const runs = replies.map((reply, i) =>
      gate.run(async () => {
        started.push(i);
        peak = Math.max(peak, ++running);
        try {
          return await reply.promise;
        } finally {
          running--;
        }
      }),
    );
    await flush();
    expect(started).toEqual([0, 1]);
    replies[1].resolve('b');
    await flush();
    expect(started).toEqual([0, 1, 2]);
    replies[0].resolve('a');
    replies[2].resolve('c');
    await flush();
    expect(started).toEqual([0, 1, 2, 3, 4]);
    replies[3].resolve('d');
    replies[4].resolve('e');
    await expect(Promise.all(runs)).resolves.toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(peak).toBe(2);
  });

  it('drop a request aborted while it waits, without ever starting it', async () => {
    const gate = new RequestGate(1);
    const first = deferred();
    const running = gate.run(() => first.promise);
    const controller = new AbortController();
    const task = vi.fn(async () => 'never');
    const aborted = gate.run(task, controller.signal);
    const next = gate.run(async () => 'next');
    controller.abort();
    await expect(aborted).rejects.toMatchObject({ name: 'AbortError' });
    first.resolve('first');
    await expect(running).resolves.toBe('first');
    await expect(next).resolves.toBe('next');
    expect(task).not.toHaveBeenCalled();
    // Already aborted: it never queues.
    await expect(gate.run(task, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
    expect(task).not.toHaveBeenCalled();
  });

  it('give the slot back when a request fails', async () => {
    const gate = new RequestGate(1);
    const failing = gate.run(async () => {
      throw new Error('backend restarting');
    });
    const next = gate.run(async () => 'ok');
    await expect(failing).rejects.toThrow('backend restarting');
    await expect(next).resolves.toBe('ok');
  });

  it('hold back only the axios requests marked long', async () => {
    const { default: axios } =
      await vi.importActual<typeof import('axios')>('axios');
    const sent: string[] = [];
    const replies = new Map<string, ReturnType<typeof deferred>>();
    const send: AxiosAdapter = (config) => {
      sent.push(config.url!);
      const reply = deferred();
      replies.set(config.url!, reply);
      return reply.promise as ReturnType<AxiosAdapter>;
    };
    const ok = (config: InternalAxiosRequestConfig) => ({
      data: config.url,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    });
    const http = axios.create({
      adapter: gateLongRequests(send, new RequestGate(2)),
    });

    const a = http.get('/evidence/a', { long: true });
    const b = http.get('/evidence/b', { long: true });
    const controller = new AbortController();
    const c = http.get('/evidence/c', {
      long: true,
      signal: controller.signal,
    });
    const d = http.get('/evidence/d', { long: true });
    const fast = http.get('/namespaces');
    await flush();
    expect([...sent].sort()).toEqual([
      '/evidence/a',
      '/evidence/b',
      '/namespaces',
    ]);

    controller.abort();
    expect(axios.isCancel(await c.catch((e) => e))).toBe(true);
    replies.get('/evidence/a')!.reject(new Error('backend restarting'));
    await expect(a).rejects.toThrow('backend restarting');
    await flush();
    // The aborted request was never sent; the failed one's slot went on.
    expect(sent).toHaveLength(4);
    expect(sent[3]).toBe('/evidence/d');

    for (const url of ['/evidence/b', '/evidence/d', '/namespaces']) {
      replies.get(url)!.resolve(ok({ url } as InternalAxiosRequestConfig));
    }
    await expect(Promise.all([b, d, fast])).resolves.toMatchObject([
      { data: '/evidence/b' },
      { data: '/evidence/d' },
      { data: '/namespaces' },
    ]);
  });
});
