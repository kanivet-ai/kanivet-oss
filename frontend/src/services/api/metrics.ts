import logger from '../../utils/logger';
import { apiClient } from './client';
import { wsManager } from './websocket';

const METRICS_STREAM_INITIAL_TIMEOUT_MS = 45000;

interface MetricsProviderCacheEntry {
  providers: any;
  unavailable: boolean;
  unavailableReason?: string;
}

const metricsProvidersByCluster = new Map<string, MetricsProviderCacheEntry>();

const unavailableProviders = (reason?: string) => ({
  prometheus: { type: 'prometheus', found: false },
  mimir: { type: 'mimir', found: false },
  'metrics-server': { type: 'metrics-server', found: false },
  unavailable: true,
  unavailableReason: reason || 'Metrics provider is unavailable',
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

export function isMetricsProviderUnavailableError(error: string): boolean {
  const normalized = error.toLowerCase();
  return [
    'no metrics provider',
    'metrics provider unavailable',
    'provider unavailable',
    'provider is unavailable',
    'provider not',
    'not detected',
    'port forward',
    'connection',
    'eof',
    'failed to query',
    'mimir returned',
    'prometheus',
    'query timed out',
    'timed out',
  ].some((fragment) => normalized.includes(fragment));
}

export function markMetricsProviderUnavailable(cluster: string, reason?: string): any {
  const providers = unavailableProviders(reason);
  metricsProvidersByCluster.set(cluster, {
    providers,
    unavailable: true,
    unavailableReason: reason,
  });
  emitMetricsProviderAvailabilityChange(cluster, true);
  return providers;
}

export function markMetricsProviderAvailable(cluster: string, providers?: any): any {
  const cached = metricsProvidersByCluster.get(cluster);
  const availableProviders = providers || (cached && !cached.unavailable ? cached.providers : null) || {
    prometheus: { type: 'prometheus', found: true },
    mimir: { type: 'mimir', found: false },
    'metrics-server': { type: 'metrics-server', found: false },
  };
  metricsProvidersByCluster.set(cluster, {
    providers: availableProviders,
    unavailable: false,
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

export function getCachedMetricsProviderStatus(cluster: string): any | null {
  return getCachedMetricsProviders(cluster)?.providers || null;
}

function getCachedUnavailableProviders(cluster: string): any | null {
  const cached = getCachedMetricsProviders(cluster);
  if (!cached?.unavailable) return null;
  return cached.providers;
}

function getCachedMetricsProviders(cluster: string): MetricsProviderCacheEntry | null {
  const cached = metricsProvidersByCluster.get(cluster);
  if (!cached) return null;
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

export async function detectMetricsProvider(cluster: string): Promise<any> {
  const cachedProviders = getCachedMetricsProviders(cluster);
  if (cachedProviders) return cachedProviders.providers;

  try {
    const response = await apiClient.getAxios().get('/metrics/detect', { params: { cluster } });
    const providers = response.data.providers;
    const hasProvider = providers?.prometheus?.found || providers?.mimir?.found || providers?.['metrics-server']?.found;
    if (!hasProvider) {
      markMetricsProviderUnavailable(cluster, 'No metrics provider detected in cluster');
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

export async function listMimirServices(cluster: string): Promise<MimirServiceInfo[]> {
  try {
    const response = await apiClient.getAxios().get('/metrics/mimir-services', { params: { cluster } });
    return response.data.services || [];
  } catch (error) {
    logger.error('Failed to list Mimir services', { error, cluster });
    return [];
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
    return response.data.tenants || [];
  } catch (error) {
    logger.error('Failed to discover Mimir tenants', { error, cluster });
    return [];
  }
}

export function startMetricsStream(
  cluster: string,
  namespace: string,
  pod: string,
  metricType: string,
  timeRange: string,
  onData: (data: { labels: string[]; values: number[]; unit?: string }) => void,
  onError: (error: string) => void,
  containerName?: string,
  provider?: string,
  streamingRate: number = 2,
  nodeName?: string
): () => void {
  const cachedUnavailable = getCachedUnavailableProviders(cluster);
  if (cachedUnavailable) {
    onError(cachedUnavailable.unavailableReason || 'Metrics provider is unavailable');
    return () => undefined;
  }

  const topicId = nodeName || pod;
  const topic = `metrics:${cluster}:${namespace}:${topicId}:${metricType}:${timeRange}`;
  const messageType = 'metrics';

  const handlers = wsManager.getHandlers();
  if (!handlers.has(messageType)) handlers.set(messageType, new Set());

  let receivedInitialResponse = false;
  const initialResponseTimeout = window.setTimeout(() => {
    if (receivedInitialResponse) return;
    const message = 'Metrics provider is unavailable';
    markMetricsProviderUnavailable(cluster, message);
    onError(message);
  }, METRICS_STREAM_INITIAL_TIMEOUT_MS);

  const handler = (msg: any) => {
    if (msg.payload) {
      const payload = msg.payload;
      if (payload.topic === topic) {
        receivedInitialResponse = true;
        window.clearTimeout(initialResponseTimeout);
        if (payload.error) {
          if (isMetricsProviderUnavailableError(payload.error)) {
            markMetricsProviderUnavailable(cluster, payload.error);
          }
          onError(payload.error);
        } else if (payload.data) {
          markMetricsProviderAvailable(cluster);
          onData(payload.data);
        }
      }
    }
  };

  handlers.get(messageType)!.add(handler);

  wsManager.sendWS({
    type: 'metrics',
    payload: {
      action: 'start', cluster, namespace, pod: nodeName ? '' : pod, nodeName,
      container: containerName, metricType, timeRange, provider, streamingRate,
    },
  });

  return () => {
    window.clearTimeout(initialResponseTimeout);
    const h = handlers.get(messageType);
    if (h) {
      h.delete(handler);
      if (h.size === 0) handlers.delete(messageType);
    }
    wsManager.sendWS({ type: 'metrics', payload: { action: 'stop', cluster, namespace, pod, metricType, timeRange } });
  };
}

export async function queryPodMetrics(
  cluster: string,
  namespace: string,
  pod: string,
  metricType: string,
  timeRange: string,
  containerName?: string,
  provider?: string
): Promise<{ labels: string[]; values: number[]; unit?: string }> {
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
    return response.data;
  } catch (error: any) {
    const message = error?.response?.data?.error || error?.message || String(error);
    if (isMetricsProviderUnavailableError(message)) {
      markMetricsProviderUnavailable(cluster, message);
    }
    logger.error('Failed to query pod metrics', { error, cluster, namespace, pod, metricType });
    throw error;
  }
}
