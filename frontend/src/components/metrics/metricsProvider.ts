import type { MetricsProviderInfo, MetricsProvidersStatus } from '../../services/api/metrics';

export type { MetricsProviderInfo, MetricsProvidersStatus };

/**
 * What the card should show for a detection result:
 *  detecting     – no answer yet
 *  ready         – a range-query provider (Prometheus-compatible or Mimir) answered
 *  needs-tenant  – Mimir is reachable but rejects requests without X-Scope-OrgID
 *  unreachable   – a provider was detected but a stream/query could not reach it
 *  none          – nothing in the cluster serves the Prometheus API
 */
export type ProviderPhase = 'detecting' | 'ready' | 'needs-tenant' | 'unreachable' | 'none';

const rangeProviders = (status: MetricsProvidersStatus): MetricsProviderInfo[] =>
  [status.prometheus, status.mimir].filter((p): p is MetricsProviderInfo => !!p && p.found);

/** The provider the charts will stream from (Prometheus-compatible first, then Mimir). */
export const activeProvider = (status: MetricsProvidersStatus | null): MetricsProviderInfo | null => {
  if (!status) return null;
  const usable = rangeProviders(status).filter((p) => !p.needsTenant);
  if (usable.length > 0) return usable[0];
  return rangeProviders(status)[0] || null;
};

export const providerPhase = (status: MetricsProvidersStatus | null): ProviderPhase => {
  if (status === null) return 'detecting';
  if (status.unavailable) return 'unreachable';
  const found = rangeProviders(status);
  if (found.length === 0) return 'none';
  if (found.every((p) => p.needsTenant)) return 'needs-tenant';
  return 'ready';
};

/** Why there is nothing to chart, in the backend's words when it gave any. */
export const providerReason = (status: MetricsProvidersStatus | null): string | undefined => {
  if (!status) return undefined;
  if (status.unavailable && status.unavailableReason) return status.unavailableReason;
  const found = rangeProviders(status);
  if (found.length > 0) return found.find((p) => p.reason)?.reason;
  const reasons = [status.prometheus?.reason, status.mimir?.reason].filter(
    (r, i, all): r is string => !!r && all.indexOf(r) === i,
  );
  if (reasons.length > 0) return reasons.join(' · ');
  if (status['metrics-server']?.found) {
    return 'Only metrics-server answered — it has no history, so charts need Prometheus, Thanos, VictoriaMetrics or Mimir.';
  }
  return undefined;
};

const FLAVOR_NAMES: Record<string, string> = {
  prometheus: 'Prometheus',
  thanos: 'Thanos',
  victoriametrics: 'VictoriaMetrics',
  mimir: 'Mimir',
  cortex: 'Cortex',
  'metrics-server': 'Metrics server',
};

export const providerDisplayName = (info: MetricsProviderInfo | null | undefined): string => {
  if (!info) return '';
  return FLAVOR_NAMES[info.flavor || ''] || FLAVOR_NAMES[info.type] || info.type;
};

/** "Prometheus · monitoring" — the chip in the chart title row. */
export const providerChipLabel = (info: MetricsProviderInfo | null | undefined): string => {
  if (!info) return '';
  const name = providerDisplayName(info);
  return info.namespace ? `${name} · ${info.namespace}` : name;
};

/** "prometheus-operated in monitoring · v3.4.1" — the longer form for the settings sheet. */
export const providerDetail = (info: MetricsProviderInfo | null | undefined): string => {
  if (!info || !info.found) return '';
  const parts: string[] = [];
  if (info.service) parts.push(info.namespace ? `${info.service} in ${info.namespace}` : info.service);
  else if (info.namespace) parts.push(info.namespace);
  if (info.port) parts.push(`port ${info.port}`);
  if (info.version) parts.push(`v${info.version.replace(/^v/, '')}`);
  return parts.join(' · ');
};

/**
 * Provider to ask the backend for. Only an explicit choice in Monitoring
 * settings is forwarded; otherwise the backend's own order (Prometheus-
 * compatible, then Mimir, then metrics-server) applies, so a stale local
 * detection can never pin a stream to the wrong provider.
 */
export const requestedProvider = (preferred: string | undefined): string | undefined => {
  if (preferred === 'prometheus' || preferred === 'mimir' || preferred === 'metrics-server') return preferred;
  return undefined;
};
