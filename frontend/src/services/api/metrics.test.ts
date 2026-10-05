import { beforeEach, describe, expect, it, vi } from 'vitest';

const get = vi.fn();
const sendWS = vi.fn();
const handlers = new Map<string, Set<(msg: any) => void>>();

vi.mock('./client', () => ({ apiClient: { getAxios: () => ({ get }) } }));
vi.mock('./websocket', () => ({
  wsManager: {
    sendWS: (msg: unknown) => sendWS(msg),
    getHandlers: () => handlers,
  },
}));
// The logger reads `window` at import time; vitest runs in node here.
vi.mock('../../utils/logger', () => ({
  default: { error: vi.fn(), warn: vi.fn(), info: vi.fn(), debug: vi.fn() },
}));

import {
  clearMetricsProviderAvailabilityCache,
  discoverMimirTenants,
  getCachedMetricsProviderStatus,
  listMimirServices,
  normalizeSeries,
  startMetricsStream,
  startWorkloadMetricsStream,
  timeAxisLabels,
} from './metrics';
import { formatTimeLabel } from '../../components/metrics/metricsFormat';

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

describe('metrics streams', () => {
  beforeEach(() => {
    vi.stubGlobal('window', {
      setTimeout,
      clearTimeout,
      dispatchEvent: () => true,
    });
    clearMetricsProviderAvailabilityCache();
    sendWS.mockReset();
    handlers.clear();
  });

  const sent = () =>
    sendWS.mock.calls.map(([msg]) => JSON.parse(JSON.stringify(msg.payload)));
  const deliver = (type: string, payload: unknown) =>
    handlers.get(type)?.forEach((h) => h({ payload }));

  // The stop used to leave nodeName out, so the backend never found the
  // node's stream and kept polling the store until the app closed.
  it('stops a node stream with the identity it was started with', () => {
    const stop = startMetricsStream(
      'prod',
      '',
      '',
      'cpu',
      '15m',
      () => undefined,
      () => undefined,
      undefined,
      undefined,
      5,
      'node-a',
    );
    stop();
    const [start, end] = sent();
    expect(start.nodeName).toBe('node-a');
    const { provider: _p, streamingRate: _r, ...identity } = start;
    expect(end).toEqual({ ...identity, action: 'stop' });
  });

  it('keeps two container charts of one pod apart', () => {
    const app = vi.fn();
    const sidecar = vi.fn();
    const noop = () => undefined;
    startMetricsStream('c', 'apps', 'web-0', 'cpu', '15m', app, noop, 'app');
    startMetricsStream(
      'c',
      'apps',
      'web-0',
      'cpu',
      '15m',
      sidecar,
      noop,
      'sidecar',
    );
    deliver('metrics', {
      topic: 'metrics:c:apps:web-0/app:cpu:15m',
      data: { timestamps: [1], values: [5] },
    });
    expect(app).toHaveBeenCalledTimes(1);
    expect(sidecar).not.toHaveBeenCalled();
  });

  // The stream holds a transient failure back while the chart has data and
  // reports it once it has repeated: the chart is told, the cluster is not
  // greyed out.
  it('reports transient errors without marking the provider unavailable, and trusts only live data', () => {
    const onData = vi.fn();
    const onError = vi.fn();
    startMetricsStream('c', 'apps', 'web-0', 'cpu', '15m', onData, onError);
    const topic = 'metrics:c:apps:web-0:cpu:15m';

    deliver('metrics', {
      topic,
      stale: true,
      data: { timestamps: [1], values: [1] },
    });
    expect(onData).toHaveBeenCalledTimes(1);
    expect(getCachedMetricsProviderStatus('c')).toBeNull();

    deliver('metrics', {
      topic,
      error: 'The metrics store is slow to answer; retrying',
      transient: true,
    });
    expect(onError).toHaveBeenCalledWith(
      'The metrics store is slow to answer; retrying',
    );
    expect(getCachedMetricsProviderStatus('c')).toBeNull();

    deliver('metrics', { topic, data: { timestamps: [1], values: [2] } });
    expect(getCachedMetricsProviderStatus('c')?.unavailable).toBeFalsy();
    expect(getCachedMetricsProviderStatus('c')).not.toBeNull();
  });

  it('streams a workload once for all its pods', () => {
    const onData = vi.fn();
    const stop = startWorkloadMetricsStream(
      'c',
      'apps',
      ['web-1', 'web-0', 'web-1'],
      'memory',
      '1h',
      onData,
      () => undefined,
    );
    deliver('workload_metrics', {
      topic: 'workload_metrics:c:apps:memory:1h:web-0,web-1',
      data: {
        timestamps: [60, 120],
        pods: {
          'web-0': { values: [1, 2] },
          'web-1': { values: [null, 3], unit: 'bytes' },
        },
      },
    });
    stop();
    const [start, end] = sent();
    expect(start.podNames).toEqual(['web-0', 'web-1']);
    expect(end).toEqual({
      action: 'stop',
      cluster: 'c',
      namespace: 'apps',
      podNames: ['web-0', 'web-1'],
      metricType: 'memory',
      timeRange: '1h',
    });
    const data = onData.mock.calls[0][0];
    expect(data.labels).toHaveLength(2);
    expect(data.unit).toBe('bytes');
    expect(data.pods['web-0']).toEqual([1, 2]);
    expect(data.pods['web-1'][0]).toBeNaN();
  });
});

describe('chart series', () => {
  it('turns gaps into NaN on a time axis built from the timestamps', () => {
    const series = normalizeSeries({
      timestamps: [0, 15, 30],
      values: [1, null, 3],
      unit: 'millicores',
    });
    expect(series.labels).toHaveLength(3);
    expect(series.values[0]).toBe(1);
    expect(series.values[1]).toBeNaN();
    expect(series.unit).toBe('millicores');
    expect(normalizeSeries({ values: [] })).toEqual({
      labels: [],
      values: [],
      unit: undefined,
    });
  });

  it('dates the labels of a chart that spans a day', () => {
    const start = Date.UTC(2026, 9, 4, 12) / 1000;
    const day = timeAxisLabels([start, start + 86400]);
    expect(day[0]).not.toBe(day[1]);
    expect(day[0]).not.toMatch(/^\d{2}:\d{2}:\d{2}$/);
    expect(formatTimeLabel(day[0])).toBe(day[0]);
    const hour = timeAxisLabels([start, start + 3600]);
    expect(hour[0]).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });

  it('says when values are per-step peaks, in the tooltip but not on the axis', () => {
    const [label] = timeAxisLabels([Date.UTC(2026, 9, 4, 12) / 1000], 600);
    expect(label).toMatch(/ · peak of 10m$/);
    expect(formatTimeLabel(label)).toMatch(/^\d{2}:\d{2}$/);
  });
});
