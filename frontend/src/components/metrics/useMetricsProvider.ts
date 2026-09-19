import { useCallback, useEffect, useRef, useState } from 'react';
import api from '../../services/api';
import {
  METRICS_PROVIDER_NEGATIVE_TTL_MS,
  getMetricsProviderCacheRemainingMs,
  type MetricsProvidersStatus,
} from '../../services/api/metrics';
import { activeProvider, providerPhase, providerReason, type ProviderPhase } from './metricsProvider';

export interface MetricsProviderState {
  status: MetricsProvidersStatus | null;
  phase: ProviderPhase;
  reason: string | undefined;
  /** The provider charts stream from, when there is one. */
  provider: ReturnType<typeof activeProvider>;
  /** True while a forced re-detection is in flight. */
  detecting: boolean;
  detect: (options?: { refresh?: boolean }) => Promise<void>;
  install: () => Promise<void>;
  installing: boolean;
  installError: string | null;
  /** Record that a stream/query proved the provider unreachable. */
  markUnavailable: (reason?: string) => void;
  /** Bumps whenever Monitoring settings change for this cluster; put it in stream effect deps. */
  revision: number;
}

/**
 * Provider detection shared by the pod, node and workload cards: one status
 * per cluster (kept in the api module's cache), synced between cards through
 * the `metrics-provider-availability` event, re-checked when the cached
 * negative expires, and restarted when Monitoring settings change.
 */
export function useMetricsProvider(cluster: string): MetricsProviderState {
  const [status, setStatus] = useState<MetricsProvidersStatus | null>(() => api.getCachedMetricsProviderStatus(cluster));
  const [detecting, setDetecting] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const detect = useCallback(async (options: { refresh?: boolean } = {}) => {
    if (options.refresh) {
      setDetecting(true);
      setStatus(null);
    }
    try {
      const next = await api.detectMetricsProvider(cluster, options);
      if (mounted.current) setStatus(next);
    } catch (err) {
      console.error('Failed to detect metrics providers:', err);
      if (mounted.current) {
        setStatus(api.markMetricsProviderUnavailable(cluster, 'Kanivet could not reach its backend to look for a metrics provider'));
      }
    } finally {
      if (mounted.current) setDetecting(false);
    }
  }, [cluster]);

  // Initial detection (served from the local cache when fresh).
  useEffect(() => {
    setStatus(api.getCachedMetricsProviderStatus(cluster));
    void detect();
  }, [cluster, detect]);

  // Keep every card on this cluster in step: when one marks the provider
  // unavailable (or a detection lands) the others pick up the same status.
  useEffect(() => {
    const onAvailability = (event: Event) => {
      const changed = (event as CustomEvent).detail?.cluster;
      if (changed && changed !== cluster) return;
      const cached = api.getCachedMetricsProviderStatus(cluster);
      if (cached) setStatus(cached);
    };
    window.addEventListener('metrics-provider-availability', onAvailability);
    return () => window.removeEventListener('metrics-provider-availability', onAvailability);
  }, [cluster]);

  // Monitoring settings changed (tenant, Mimir instance…): forget what we
  // knew, detect again and tell the card to restart its stream.
  useEffect(() => {
    const onSettingsChanged = (event: Event) => {
      const changed = (event as CustomEvent).detail?.cluster;
      if (changed && changed !== cluster) return;
      api.clearMetricsProviderAvailabilityCache(cluster);
      setRevision((r) => r + 1);
      void detect();
    };
    window.addEventListener('metrics-settings-changed', onSettingsChanged);
    return () => window.removeEventListener('metrics-settings-changed', onSettingsChanged);
  }, [cluster, detect]);

  // A negative answer is only trusted for a minute; while the card is on
  // screen, ask again when it lapses so a provider that comes back is noticed.
  const phase = providerPhase(status);
  useEffect(() => {
    if (phase !== 'none' && phase !== 'unreachable') return;
    const remaining = getMetricsProviderCacheRemainingMs(cluster) || METRICS_PROVIDER_NEGATIVE_TTL_MS;
    const timer = window.setTimeout(() => {
      if (document.visibilityState === 'hidden') return;
      void detect();
    }, remaining + 250);
    return () => window.clearTimeout(timer);
  }, [cluster, phase, status, detect]);

  const markUnavailable = useCallback((reason?: string) => {
    setStatus(api.markMetricsProviderUnavailable(cluster, reason));
  }, [cluster]);

  const install = useCallback(async () => {
    setInstalling(true);
    setInstallError(null);
    api.clearMetricsProviderAvailabilityCache(cluster);
    try {
      await api.installMetricsProvider(cluster, 'prometheus');
      await detect({ refresh: true });
    } catch (err: any) {
      if (mounted.current) setInstallError(err?.response?.data?.error || err?.message || 'Failed to install Prometheus');
    } finally {
      if (mounted.current) setInstalling(false);
    }
  }, [cluster, detect]);

  return {
    status,
    phase,
    reason: providerReason(status),
    provider: activeProvider(status),
    detecting,
    detect,
    install,
    installing,
    installError,
    markUnavailable,
    revision,
  };
}
