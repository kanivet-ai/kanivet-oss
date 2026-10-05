import logger from '../../utils/logger';
import { apiClient } from './client';
import { wsManager } from './websocket';

const METRICS_STREAM_INITIAL_TIMEOUT_MS = 45000;
const METRICS_DETECT_TIMEOUT_MS = 30000;

// How long a detection result is trusted locally before the next card mount
// (or the hook's timer) asks the backend again. Negatives are short so a
// provider that was just installed — or a Prometheus whose pod came back —
// shows up within a minute instead of "unavailable" for the rest of the session.
export const METRICS_PROVIDER_NEGATIVE_TTL_MS = 60_000;
export const METRICS_PROVIDER_POSITIVE_TTL_MS = 10 * 60_000;

export type MetricsProviderFlavor = 'prometheus' | 'thanos' | 'victoriametrics' | 'mimir' | 'cortex';

export interface MetricsProviderInfo {
  type: string;
  found: boolean;
  namespace?: string;
  service?: string;
  url?: string;
  version?: string;
  port?: number;
  flavor?: MetricsProviderFlavor | string;
  path?: string;
  /** True when the backend reached the provider's HTTP API, not just its Service. */
  verified?: boolean;
  /** Plain-words explanation when `found` is false (or the probe was inconclusive). */
  reason?: string;
  /** Mimir answered 401 — reachable, but a tenant (X-Scope-OrgID) must be configured. */
  needsTenant?: boolean;
}

export interface MetricsProvidersStatus {
  prometheus?: MetricsProviderInfo;
  mimir?: MetricsProviderInfo;
  'metrics-server'?: MetricsProviderInfo;
  /** Set locally when a stream/query proved the provider unreachable. */
  unavailable?: boolean;
  unavailableReason?: string;
  /** Unix seconds, from the backend. */
  checkedAt?: number;
}

interface MetricsProviderCacheEntry {
  providers: MetricsProvidersStatus;
  unavailable: boolean;
  unavailableReason?: string;
  expiresAt: number;
}

const metricsProvidersByCluster = new Map<string, MetricsProviderCacheEntry>();

const unavailableProviders = (reason?: string): MetricsProvidersStatus => ({
  prometheus: { type: 'prometheus', found: false },
  mimir: { type: 'mimir', found: false },
  'metrics-server': { type: 'metrics-server', found: false },
  unavailable: true,
  unavailableReason: reason || 'Metrics provider is unavailable',
  checkedAt: Math.floor(Date.now() / 1000),
});

const emitMetricsProviderAvailabilityChange = (cluster?: string, unavailable = false) => {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent('metrics-provider-availability', {
    detail: { cluster, unavailable },
  }));
};

// Signals open metrics charts to tear down and restart their stream after a
// monitoring-settings change (e.g. a new Mimir instance or tenant). The chart
// effects don't depend on settings, so without this they keep the stale stream
// until an app restart.
export function emitMetricsSettingsChanged(cluster: string): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent('metrics-settings-changed', { detail: { cluster } }));
}

/**
 * Transport-level failures that mean "the provider cannot be reached", as
 * opposed to a bad query. Deliberately does NOT match the bare word
 * "prometheus" or "failed to query": a PromQL/bad_data error for one metric
 * must not flip the whole cluster to "unavailable".
 */
export function isMetricsProviderUnavailableError(error: string): boolean {
  const normalized = error.toLowerCase();
  return [
    'no metrics provider',
    'metrics provider unavailable',
    'provider unavailable',
    'provider is unavailable',
    'not detected in cluster',
    'not found in cluster',
    'port forward',
    'port-forward',
    'no ready pod',
    'no running pod',
    'connection refused',
    'connection reset',
    'lost connection',
    'no such host',
    'eof',
    'timed out',
    'mimir returned 401',
    'mimir returned 403',
    'mimir returned 5',
  ].some((fragment) => normalized.includes(fragment));
}

export function markMetricsProviderUnavailable(cluster: string, reason?: string): MetricsProvidersStatus {
  const providers = unavailableProviders(reason);
  metricsProvidersByCluster.set(cluster, {
    providers,
    unavailable: true,
    unavailableReason: reason,
    expiresAt: Date.now() + METRICS_PROVIDER_NEGATIVE_TTL_MS,
  });
  emitMetricsProviderAvailabilityChange(cluster, true);
  return providers;
}

export function markMetricsProviderAvailable(cluster: string, providers?: MetricsProvidersStatus): MetricsProvidersStatus {
  const cached = metricsProvidersByCluster.get(cluster);
  const availableProviders = providers || (cached && !cached.unavailable ? cached.providers : null) || {
    prometheus: { type: 'prometheus', found: true },
    mimir: { type: 'mimir', found: false },
    'metrics-server': { type: 'metrics-server', found: false },
  };
  metricsProvidersByCluster.set(cluster, {
    providers: availableProviders,
    unavailable: false,
    expiresAt: Date.now() + METRICS_PROVIDER_POSITIVE_TTL_MS,
  });
  emitMetricsProviderAvailabilityChange(cluster, false);
  return availableProviders;
}

export function clearMetricsProviderAvailabilityCache(cluster?: string): void {
  if (cluster) {
    metricsProvidersByCluster.delete(cluster);
    emitMetricsProviderAvailabilityChange(cluster, false);
    return;
  }
  metricsProvidersByCluster.clear();
  emitMetricsProviderAvailabilityChange(undefined, false);
}

export function isMetricsProviderUnavailableCached(cluster: string): boolean {
  return getCachedUnavailableProviders(cluster) !== null;
}

export function getCachedMetricsProviderStatus(cluster: string): MetricsProvidersStatus | null {
  return getCachedMetricsProviders(cluster)?.providers || null;
}

/** Milliseconds until the cached detection for `cluster` expires; 0 when nothing is cached. */
export function getMetricsProviderCacheRemainingMs(cluster: string): number {
  const cached = getCachedMetricsProviders(cluster);
  return cached ? Math.max(0, cached.expiresAt - Date.now()) : 0;
}

function getCachedUnavailableProviders(cluster: string): MetricsProvidersStatus | null {
  const cached = getCachedMetricsProviders(cluster);
  if (!cached?.unavailable) return null;
  return cached.providers;
}

function getCachedMetricsProviders(cluster: string): MetricsProviderCacheEntry | null {
  const cached = metricsProvidersByCluster.get(cluster);
  if (!cached) return null;
  if (cached.expiresAt <= Date.now()) {
    metricsProvidersByCluster.delete(cluster);
    return null;
  }
  return cached;
}

export async function getAvailableMetricProviders(): Promise<string[]> {
  try {
    const response = await apiClient.getAxios().get('/metrics/providers');
    return response.data.providers || [];
  } catch (error) {
    logger.error('Failed to get available metric providers', { error });
    throw error;
  }
}

export interface DetectMetricsProviderOptions {
  /** Bust the backend's detection caches too, not just the local one. */
  refresh?: boolean;
}

export async function detectMetricsProvider(
  cluster: string,
  options: DetectMetricsProviderOptions = {},
): Promise<MetricsProvidersStatus> {
  if (isMockMetricsEnabled()) return mockProvidersStatus(cluster);

  if (options.refresh) {
    metricsProvidersByCluster.delete(cluster);
  } else {
    const cachedProviders = getCachedMetricsProviders(cluster);
    if (cachedProviders) return cachedProviders.providers;
  }

  try {
    const params: Record<string, string> = { cluster };
    if (options.refresh) params.refresh = '1';
    const response = await apiClient.getAxios().get('/metrics/detect', {
      params,
      timeout: METRICS_DETECT_TIMEOUT_MS,
    });
    const providers: MetricsProvidersStatus = { ...(response.data.providers || {}) };
    if (typeof response.data.checkedAt === 'number') providers.checkedAt = response.data.checkedAt;
    const hasProvider = providers.prometheus?.found || providers.mimir?.found || providers['metrics-server']?.found;
    if (!hasProvider) {
      const reason =
        providers.prometheus?.reason ||
        providers.mimir?.reason ||
        providers['metrics-server']?.reason ||
        'No metrics provider detected in cluster';
      // Cached as "unavailable" so MetricsPropertyGroup collapses the section
      // the way it always has, but the real per-provider reasons are kept so
      // the card can say *why* nothing was found.
      metricsProvidersByCluster.set(cluster, {
        providers,
        unavailable: true,
        unavailableReason: reason,
        expiresAt: Date.now() + METRICS_PROVIDER_NEGATIVE_TTL_MS,
      });
      emitMetricsProviderAvailabilityChange(cluster, true);
    } else {
      markMetricsProviderAvailable(cluster, providers);
    }
    return providers;
  } catch (error) {
    logger.error('Failed to detect metrics provider', { error, cluster });
    throw error;
  }
}

export async function installMetricsProvider(cluster: string, provider: string, namespace?: string): Promise<void> {
  try {
    clearMetricsProviderAvailabilityCache(cluster);
    await apiClient.getAxios().post(
      '/metrics/install',
      { provider, namespace: namespace || 'kanivet-monitoring' },
      { params: { cluster } }
    );
  } catch (error) {
    logger.error('Failed to install metrics provider', { error, cluster, provider });
    throw error;
  }
}

export interface ClusterMetricsSettings {
  clusterName: string;
  mimirTenant: string;
  mimirService?: string;
  mimirNamespace?: string;
}

export interface MimirServiceInfo {
  type: string;
  found: boolean;
  namespace: string;
  service: string;
  url: string;
  port: number;
}

export async function getClusterMetricsSettings(cluster: string): Promise<ClusterMetricsSettings> {
  try {
    const response = await apiClient.getAxios().get('/metrics/settings', { params: { cluster } });
    return response.data;
  } catch (error) {
    logger.error('Failed to get cluster metrics settings', { error, cluster });
    throw error;
  }
}

export async function setClusterMetricsSettings(cluster: string, settings: { mimirTenant?: string; mimirService?: string; mimirNamespace?: string }): Promise<ClusterMetricsSettings> {
  try {
    clearMetricsProviderAvailabilityCache(cluster);
    const response = await apiClient.getAxios().put('/metrics/settings', settings, { params: { cluster } });
    emitMetricsSettingsChanged(cluster);
    return response.data;
  } catch (error) {
    logger.error('Failed to save cluster metrics settings', { error, cluster });
    throw error;
  }
}

/**
 * Discovery endpoints answer 200 with an empty list plus an `error` field when
 * the probe itself failed. Surface that as a rejection so callers can tell
 * "nothing found" from "could not look".
 */
function discoveryError(data: any, fallback: string): Error | null {
  return data?.error ? new Error(String(data.error)) : data ? null : new Error(fallback);
}

export async function listMimirServices(cluster: string): Promise<MimirServiceInfo[]> {
  try {
    const response = await apiClient.getAxios().get('/metrics/mimir-services', { params: { cluster } });
    const failure = discoveryError(response.data, 'Empty response from Mimir service discovery');
    if (failure) throw failure;
    return response.data.services || [];
  } catch (error) {
    logger.error('Failed to list Mimir services', { error, cluster });
    throw error;
  }
}

export async function discoverMimirTenants(cluster: string, hints: string[] = []): Promise<string[]> {
  try {
    const params = new URLSearchParams();
    params.append('cluster', cluster);
    for (const h of hints) {
      if (h) params.append('hint', h);
    }
    const response = await apiClient.getAxios().get(`/metrics/tenants?${params.toString()}`);
    const failure = discoveryError(response.data, 'Empty response from Mimir tenant discovery');
    if (failure) throw failure;
    return response.data.tenants || [];
  } catch (error) {
    logger.error('Failed to discover Mimir tenants', { error, cluster });
    throw error;
  }
}

export interface MetricsSeries {
  /** Axis labels, one per value: clock times, with the date once a chart spans a day. */
  labels: string[];
  /** One sample per label; NaN where the store had none, which charts draw as a gap. */
  values: number[];
  unit?: string;
}

/** A workload chart: every pod on one time axis. */
export interface WorkloadSeries {
  labels: string[];
  /** Pod name → one sample per label (NaN for a gap). */
  pods: Record<string, number[]>;
  unit?: string;
}

/** A chart series as the backend sends it. */
interface WireSeries {
  labels?: string[];
  /** Unix seconds of the query's step grid; `values` has one entry per timestamp. */
  timestamps?: number[];
  values?: Array<number | null>;
  step?: number;
  /** Each value is the highest sample within its step. */
  peak?: boolean;
  unit?: string;
}

interface WireWorkloadSeries {
  timestamps?: number[];
  step?: number;
  peak?: boolean;
  pods?: Record<string, WireSeries>;
}

const DAY_SECONDS = 24 * 60 * 60;

const pad2 = (n: number) => n.toString().padStart(2, '0');

const describeSeconds = (seconds: number): string => {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
};

// One formatter for every label: toLocaleDateString with options builds a
// new one per call, ~7ms for the 145 labels of a 24h chart on each refresh.
let dayFormat: Intl.DateTimeFormat | undefined;
const formatDay = (d: Date): string => {
  if (!dayFormat)
    dayFormat = new Intl.DateTimeFormat(undefined, {
      month: 'short',
      day: 'numeric',
    });
  return dayFormat.format(d);
};

/**
 * Axis labels for sample times (unix seconds), in local time: "HH:MM:SS", or
 * "Oct 5 14:30" once the chart spans a day, so the two ends of a 24h chart
 * no longer read the same. When each value is the peak of its step the label
 * says so after " · " — the axis shows only the time, the tooltip all of it.
 */
export function timeAxisLabels(
  timestamps: number[],
  peakStepSeconds?: number,
): string[] {
  if (timestamps.length === 0) return [];
  const withDate =
    timestamps[timestamps.length - 1] - timestamps[0] >= DAY_SECONDS;
  const suffix = peakStepSeconds
    ? ` · peak of ${describeSeconds(peakStepSeconds)}`
    : '';
  return timestamps.map((ts) => {
    const d = new Date(ts * 1000);
    const hm = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
    const label = withDate
      ? `${formatDay(d)} ${hm}`
      : `${hm}:${pad2(d.getSeconds())}`;
    return label + suffix;
  });
}

const samples = (values: Array<number | null> | undefined): number[] =>
  (values || []).map((v) => (typeof v === 'number' ? v : NaN));

/** Backend series → what the charts draw: gaps as NaN, labels from the timestamps. */
export function normalizeSeries(data: WireSeries): MetricsSeries {
  const labels = data.timestamps?.length
    ? timeAxisLabels(data.timestamps, data.peak ? data.step : undefined)
    : data.labels || [];
  return { labels, values: samples(data.values), unit: data.unit };
}

export function normalizeWorkloadSeries(
  data: WireWorkloadSeries,
): WorkloadSeries {
  const pods: Record<string, number[]> = {};
  let unit: string | undefined;
  for (const [name, series] of Object.entries(data.pods || {})) {
    pods[name] = samples(series.values);
    unit = unit || series.unit;
  }
  return {
    labels: timeAxisLabels(
      data.timestamps || [],
      data.peak ? data.step : undefined,
    ),
    pods,
    unit,
  };
}

/** The topic the backend answers a pod, container or node stream on (buildTopic in metrics_handler.go). */
export function metricsStreamTopic(
  cluster: string,
  namespace: string,
  pod: string,
  metricType: string,
  timeRange: string,
  containerName?: string,
  nodeName?: string,
): string {
  const target = (nodeName || pod) + (containerName ? `/${containerName}` : '');
  return `metrics:${cluster}:${namespace}:${target}:${metricType}:${timeRange}`;
}

/** The topic of a workload stream: the pod set is part of it, so two workloads never share one. */
export function workloadStreamTopic(
  cluster: string,
  namespace: string,
  podNames: string[],
  metricType: string,
  timeRange: string,
): string {
  const pods = [...new Set(podNames)].sort().join(',');
  return `workload_metrics:${cluster}:${namespace}:${metricType}:${timeRange}:${pods}`;
}

interface MetricsStreamOptions<T> {
  cluster: string;
  messageType: 'metrics' | 'workload_metrics';
  topic: string;
  /** What the stream charts. Sent with both start and stop, so the stop addresses the stream the start opened. */
  identity: Record<string, unknown>;
  settings: Record<string, unknown>;
  normalize: (data: any) => T;
  onData: (data: T) => void;
  onError: (error: string) => void;
}

function openMetricsStream<T>(options: MetricsStreamOptions<T>): () => void {
  const { cluster, messageType, topic, onData, onError } = options;
  const cachedUnavailable = getCachedUnavailableProviders(cluster);
  if (cachedUnavailable) {
    onError(cachedUnavailable.unavailableReason || 'Metrics provider is unavailable');
    return () => undefined;
  }

  const handlers = wsManager.getHandlers();
  if (!handlers.has(messageType)) handlers.set(messageType, new Set());

  let receivedInitialResponse = false;
  const initialResponseTimeout = window.setTimeout(() => {
    if (receivedInitialResponse) return;
    const message = 'Metrics provider is unavailable: no answer from the metrics stream';
    markMetricsProviderUnavailable(cluster, message);
    onError(message);
  }, METRICS_STREAM_INITIAL_TIMEOUT_MS);

  const handler = (msg: any) => {
    const payload = msg?.payload;
    if (!payload || payload.topic !== topic) return;
    receivedInitialResponse = true;
    window.clearTimeout(initialResponseTimeout);
    if (payload.error) {
      // A transient failure is one the stream retries on its own. It only
      // gets here before any data or once it has repeated, and says nothing
      // about the provider being gone.
      if (
        !payload.transient &&
        isMetricsProviderUnavailableError(payload.error)
      ) {
        markMetricsProviderUnavailable(cluster, payload.error);
      }
      onError(payload.error);
    } else if (payload.data) {
      // A preview from the cache says nothing about reaching the provider now.
      if (!payload.stale) markMetricsProviderAvailable(cluster);
      onData(options.normalize(payload.data));
    }
  };

  handlers.get(messageType)!.add(handler);

  wsManager.sendWS({
    type: 'metrics',
    payload: { action: 'start', ...options.identity, ...options.settings },
  });

  return () => {
    window.clearTimeout(initialResponseTimeout);
    const h = handlers.get(messageType);
    if (h) {
      h.delete(handler);
      if (h.size === 0) handlers.delete(messageType);
    }
    wsManager.sendWS({
      type: 'metrics',
      payload: { action: 'stop', ...options.identity },
    });
  };
}

export function startMetricsStream(
  cluster: string,
  namespace: string,
  pod: string,
  metricType: string,
  timeRange: string,
  onData: (data: MetricsSeries) => void,
  onError: (error: string) => void,
  containerName?: string,
  provider?: string,
  streamingRate: number = 2,
  nodeName?: string,
): () => void {
  if (isMockMetricsEnabled()) {
    return startMockMetricsStream(
      pod || nodeName || 'mock',
      metricType,
      timeRange,
      onData,
    );
  }
  return openMetricsStream({
    cluster,
    messageType: 'metrics',
    topic: metricsStreamTopic(
      cluster,
      namespace,
      pod,
      metricType,
      timeRange,
      containerName,
      nodeName,
    ),
    identity: {
      cluster,
      namespace,
      pod: nodeName ? '' : pod,
      nodeName,
      container: containerName,
      metricType,
      timeRange,
    },
    settings: { provider, streamingRate },
    normalize: normalizeSeries,
    onData,
    onError,
  });
}

/** One stream for a workload's pods: one store query and one message per refresh, whatever the pod count. */
export function startWorkloadMetricsStream(
  cluster: string,
  namespace: string,
  podNames: string[],
  metricType: string,
  timeRange: string,
  onData: (data: WorkloadSeries) => void,
  onError: (error: string) => void,
  provider?: string,
  streamingRate: number = 2,
): () => void {
  const pods = [...new Set(podNames)].sort();
  if (isMockMetricsEnabled()) {
    const latest: WorkloadSeries = { labels: [], pods: {} };
    const stops = pods.map((pod) =>
      startMockMetricsStream(pod, metricType, timeRange, (data) => {
        latest.labels = data.labels;
        latest.unit = data.unit;
        latest.pods = { ...latest.pods, [pod]: data.values };
        onData({ ...latest });
      }),
    );
    return () => stops.forEach((stop) => stop());
  }
  return openMetricsStream({
    cluster,
    messageType: 'workload_metrics',
    topic: workloadStreamTopic(cluster, namespace, pods, metricType, timeRange),
    identity: { cluster, namespace, podNames: pods, metricType, timeRange },
    settings: { provider, streamingRate },
    normalize: normalizeWorkloadSeries,
    onData,
    onError,
  });
}

export async function queryPodMetrics(
  cluster: string,
  namespace: string,
  pod: string,
  metricType: string,
  timeRange: string,
  containerName?: string,
  provider?: string
): Promise<MetricsSeries> {
  const cachedUnavailable = getCachedUnavailableProviders(cluster);
  if (cachedUnavailable) {
    throw new Error(cachedUnavailable.unavailableReason || 'Metrics provider is unavailable');
  }

  try {
    const response = await apiClient.getAxios().post(
      `/metrics/pods/${namespace}/${pod}`,
      { metricType, timeRange, containerName, provider },
      { params: { cluster }, timeout: 300000 }
    );
    markMetricsProviderAvailable(cluster);
    return normalizeSeries(response.data);
  } catch (error: any) {
    const message = error?.response?.data?.error || error?.message || String(error);
    if (isMetricsProviderUnavailableError(message)) {
      markMetricsProviderUnavailable(cluster, message);
    }
    logger.error('Failed to query pod metrics', { error, cluster, namespace, pod, metricType });
    throw error;
  }
}

/* ---- Dev-only mock ---------------------------------------------------------
   The screenshot harness sets `window.__kanivetMockMetrics = true` before the
   app loads so the chart states can be captured without a cluster that has a
   working Prometheus. Compiled out of production builds by the DEV guard.
--------------------------------------------------------------------------- */

function isMockMetricsEnabled(): boolean {
  return Boolean(import.meta.env.DEV) && typeof window !== 'undefined' && Boolean((window as any).__kanivetMockMetrics);
}

function mockProvidersStatus(cluster: string): MetricsProvidersStatus {
  const status: MetricsProvidersStatus = {
    prometheus: {
      type: 'prometheus',
      found: true,
      namespace: 'monitoring',
      service: 'prometheus-operated',
      url: 'http://prometheus-operated.monitoring.svc.cluster.local:9090',
      version: '3.4.1',
      port: 9090,
      flavor: 'prometheus',
      verified: true,
    },
    mimir: { type: 'mimir', found: false, reason: 'No Mimir or Cortex gateway found' },
    'metrics-server': { type: 'metrics-server', found: false, reason: 'metrics.k8s.io is not served' },
    checkedAt: Math.floor(Date.now() / 1000),
  };
  markMetricsProviderAvailable(cluster, status);
  return status;
}

function startMockMetricsStream(
  seedKey: string,
  metricType: string,
  timeRange: string,
  onData: (data: MetricsSeries) => void,
): () => void {
  const minutes = parseInt(timeRange, 10) || 15;
  const spanMs = (timeRange.endsWith('h') ? minutes * 60 : minutes) * 60_000;
  const points = 40;
  let seed = 0;
  for (let i = 0; i < seedKey.length; i++) seed = (seed * 31 + seedKey.charCodeAt(i)) >>> 0;
  const rand = () => {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    return seed / 0xffffffff;
  };
  const base: Record<string, [number, number, string]> = {
    cpu: [180, 90, 'millicores'],
    memory: [340 * 1048576, 60 * 1048576, 'bytes'],
    network_rx: [42, 25, 'KB/s'],
    network_tx: [18, 12, 'KB/s'],
    disk_read: [3, 4, 'KB/s'],
    disk_write: [7, 6, 'KB/s'],
  };
  const [level, swing, unit] = base[metricType] || base.cpu;
  const build = (): MetricsSeries => {
    const now = Date.now();
    const labels: string[] = [];
    const values: number[] = [];
    let v = level;
    for (let i = 0; i < points; i++) {
      const t = new Date(now - spanMs + (spanMs * i) / (points - 1));
      labels.push(t.toTimeString().slice(0, 8));
      v = Math.max(0, v + (rand() - 0.48) * swing * 0.35 + Math.sin(i / 5) * swing * 0.08);
      values.push(Math.round(v * 100) / 100);
    }
    return { labels, values, unit };
  };
  const first = window.setTimeout(() => onData(build()), 250);
  const tick = window.setInterval(() => onData(build()), 5000);
  return () => {
    window.clearTimeout(first);
    window.clearInterval(tick);
  };
}
