import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { useStore, MonitoringSettings } from '../store';
import { useShallow } from 'zustand/react/shallow';
import api from '../services/api';
import type { MetricsProviderInfo, MetricsProvidersStatus, MimirServiceInfo } from '../services/api/metrics';
import { getErrorMessage } from '../utils/errorMessage';
import { providerDetail, providerDisplayName } from './metrics/metricsProvider';
import './MonitoringSettingsModal.css';

interface MonitoringSettingsModalProps {
  onClose: () => void;
  cluster?: string;
}

type ProviderKey = 'prometheus' | 'mimir' | 'metrics-server';

const PROVIDER_ROWS: { key: ProviderKey; fallbackName: string; kind: string }[] = [
  { key: 'prometheus', fallbackName: 'Prometheus', kind: 'Prometheus-compatible endpoint' },
  { key: 'mimir', fallbackName: 'Mimir', kind: 'Mimir or Cortex gateway' },
  { key: 'metrics-server', fallbackName: 'Metrics server', kind: 'metrics.k8s.io' },
];

const relativeTime = (unixSeconds: number | undefined, now: number): string => {
  if (!unixSeconds) return '';
  const seconds = Math.max(0, Math.round(now / 1000 - unixSeconds));
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.round(minutes / 60)}h ago`;
};

const providerTone = (info: MetricsProviderInfo | undefined): 'success' | 'warning' | 'muted' => {
  if (!info?.found) return 'muted';
  if (info.needsTenant || info.verified === false) return 'warning';
  return 'success';
};

const providerStatusText = (info: MetricsProviderInfo | undefined, kind: string): string => {
  if (!info?.found) return info?.reason || `No ${kind} found`;
  if (info.needsTenant) return 'Reachable — needs a tenant (X-Scope-OrgID)';
  const detail = providerDetail(info);
  if (info.verified === false && info.reason) return `${detail} · ${info.reason}`;
  return detail || 'Found';
};

const MonitoringSettingsModal = ({ onClose, cluster }: MonitoringSettingsModalProps) => {
  const { monitoringSettings, setMonitoringSettings, currentTab } = useStore(useShallow((s) => ({ monitoringSettings: s.monitoringSettings, setMonitoringSettings: s.setMonitoringSettings, currentTab: s.currentTab })));
  const [settings, setSettings] = useState<MonitoringSettings>({ ...monitoringSettings });
  const [detected, setDetected] = useState<MetricsProvidersStatus | null>(null);
  const [detecting, setDetecting] = useState(true);
  const [detectError, setDetectError] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  // Per-cluster Mimir tenant. Multi-tenant Mimir silently returns empty data
  // when X-Scope-OrgID is missing or wrong, so we expose a discoverable
  // picker plus a free-text override.
  const [mimirTenant, setMimirTenant] = useState<string>('');
  const [tenantInput, setTenantInput] = useState<string>('');
  const [discoveredTenants, setDiscoveredTenants] = useState<string[]>([]);
  const [discovering, setDiscovering] = useState(false);
  const [tenantSaving, setTenantSaving] = useState(false);

  // Per-cluster Mimir service. A cluster can expose several Mimir gateways
  // (host-level plus vcluster-mapped copies) and only some hold the container
  // metrics we query, so the operator picks which one. Empty = auto-discovery.
  const [mimirServices, setMimirServices] = useState<MimirServiceInfo[]>([]);
  const [mimirServicesError, setMimirServicesError] = useState<string | null>(null);
  const [tenantDiscoveryError, setTenantDiscoveryError] = useState<string | null>(null);
  const [chosenMimir, setChosenMimir] = useState<string>('');

  const activeCluster = cluster || currentTab;
  const mimirKey = (s: { namespace: string; service: string }) => `${s.namespace}/${s.service}`;

  const detectProviders = useCallback(async (refresh = false) => {
    if (!activeCluster) {
      setDetecting(false);
      return;
    }
    setDetecting(true);
    setDetectError(null);
    try {
      const providers = await api.detectMetricsProvider(activeCluster, { refresh });
      setDetected(providers || {});
      setNow(Date.now());
    } catch (err) {
      console.error('Failed to detect providers:', err);
      setDetectError(getErrorMessage(err, 'Could not reach the backend to detect providers'));
    } finally {
      setDetecting(false);
    }
  }, [activeCluster]);

  const loadTenantSettings = useCallback(async () => {
    if (!activeCluster) return;
    try {
      const s = await api.getClusterMetricsSettings(activeCluster);
      setMimirTenant(s.mimirTenant || '');
      setTenantInput(s.mimirTenant || '');
      setChosenMimir(s.mimirService ? `${s.mimirNamespace || ''}/${s.mimirService}` : '');
    } catch (err) {
      console.error('Failed to load tenant settings:', err);
    }
  }, [activeCluster]);

  const loadMimirServices = useCallback(async () => {
    if (!activeCluster) return;
    try {
      setMimirServices(await api.listMimirServices(activeCluster));
      setMimirServicesError(null);
    } catch (err) {
      console.error('Failed to list Mimir services:', err);
      setMimirServices([]);
      setMimirServicesError(getErrorMessage(err, 'Could not look for Mimir services in this cluster'));
    }
  }, [activeCluster]);

  useEffect(() => {
    void detectProviders();
    void loadTenantSettings();
    void loadMimirServices();
  }, [detectProviders, loadTenantSettings, loadMimirServices]);

  // Keep the "checked … ago" label honest while the sheet is open.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(timer);
  }, []);

  const persistMimirService = useCallback(async (value: string) => {
    if (!activeCluster) return;
    const [namespace, service] = value ? value.split('/') : ['', ''];
    try {
      await api.setClusterMetricsSettings(activeCluster, { mimirService: service, mimirNamespace: namespace });
      setChosenMimir(value);
    } catch (err) {
      console.error('Failed to save Mimir service:', err);
    }
  }, [activeCluster]);

  const runTenantDiscovery = useCallback(async () => {
    if (!activeCluster) return;
    setDiscovering(true);
    setTenantDiscoveryError(null);
    try {
      const hints = tenantInput ? [tenantInput] : [];
      const tenants = await api.discoverMimirTenants(activeCluster, hints);
      setDiscoveredTenants(tenants);
      if (tenants.length === 0) {
        setTenantDiscoveryError('Discovery ran but found no tenant with data. Enter the tenant ID manually.');
      }
    } catch (err) {
      console.error('Failed to discover tenants:', err);
      setDiscoveredTenants([]);
      setTenantDiscoveryError(getErrorMessage(err, 'Tenant discovery failed'));
    } finally {
      setDiscovering(false);
    }
  }, [activeCluster, tenantInput]);

  const persistTenant = useCallback(async (value: string) => {
    if (!activeCluster) return;
    setTenantSaving(true);
    try {
      const updated = await api.setClusterMetricsSettings(activeCluster, { mimirTenant: value });
      setMimirTenant(updated.mimirTenant || '');
      setTenantInput(updated.mimirTenant || '');
    } catch (err) {
      console.error('Failed to save tenant:', err);
    } finally {
      setTenantSaving(false);
    }
  }, [activeCluster]);

  const handleSave = () => {
    setMonitoringSettings(settings);
    if (tenantInput !== mimirTenant) {
      void persistTenant(tenantInput);
    }
    onClose();
  };

  const handleRedetect = () => {
    void detectProviders(true);
    void loadMimirServices();
  };

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('keydown', handleKeyDown, true);
    return () => document.removeEventListener('keydown', handleKeyDown, true);
  }, [onClose]);

  const prometheus = detected?.prometheus;
  const mimir = detected?.mimir;
  const metricsServer = detected?.['metrics-server'];
  const nothingFound = !!detected && !prometheus?.found && !mimir?.found && !metricsServer?.found;
  const showTenantRow = !!mimir?.found || settings.preferredProvider === 'mimir';

  const autoDetectLabel = useMemo(() => {
    const first = [prometheus, mimir].find((p) => p?.found && !p.needsTenant) || [prometheus, mimir].find((p) => p?.found);
    if (first) {
      const where = first.service ? `${first.service}${first.namespace ? ` in ${first.namespace}` : ''}` : providerDisplayName(first);
      return `Automatic → ${where}`;
    }
    if (metricsServer?.found) return 'Automatic → Metrics server';
    return detecting && !detected ? 'Automatic' : 'Automatic (nothing found yet)';
  }, [prometheus, mimir, metricsServer, detecting, detected]);

  const providerOptionLabel = (key: ProviderKey, fallbackName: string) => {
    const info = detected?.[key];
    if (!info?.found) return `${fallbackName} — not found`;
    const name = providerDisplayName(info) || fallbackName;
    return info.service ? `${name} — ${info.service}${info.namespace ? ` in ${info.namespace}` : ''}` : name;
  };

  const checkedLabel = relativeTime(detected?.checkedAt, now);

  const sheet = (
    <div className="ap-overlay ms-overlay" onClick={onClose} role="presentation">
      <div
        className="ap-sheet ms-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="ms-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="ms-header">
          <h3 className="ap-sheet-title" id="ms-title">Monitoring</h3>
          <button type="button" className="ap-icon-btn" onClick={onClose} aria-label="Close" title="Close">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>

        <div className="ms-body">
          {/* Detected providers */}
          <section className="ms-section" aria-labelledby="ms-detected">
            <div className="ms-section-head">
              <span className="ms-section-title" id="ms-detected">Detected in this cluster</span>
              <span className="ms-section-meta">
                {detecting ? 'Checking…' : checkedLabel ? `Checked ${checkedLabel}` : ''}
              </span>
            </div>
            <div className="ap-card ms-card">
              {PROVIDER_ROWS.map(({ key, fallbackName, kind }) => {
                const info = detected?.[key];
                const tone = detecting && !detected ? 'muted' : providerTone(info);
                const name = info?.found ? providerDisplayName(info) || fallbackName : fallbackName;
                return (
                  <div key={key} className="ms-provider-row">
                    <span className={`ap-dot ap-dot--${tone} ms-provider-dot`} aria-hidden="true" />
                    <div className="ms-provider-text">
                      <span className="ms-provider-name">{name}</span>
                      <span className="ms-provider-status">
                        {detecting && !detected ? `Looking for a ${kind}…` : providerStatusText(info, kind)}
                      </span>
                    </div>
                    {info?.found && (
                      <span className={`ap-badge ap-badge--sm ${tone === 'success' ? 'ap-badge--success' : 'ap-badge--warning'}`}>
                        {tone === 'success' ? 'Ready' : info.needsTenant ? 'Needs tenant' : 'Unverified'}
                      </span>
                    )}
                  </div>
                );
              })}
              <div className="ms-provider-footer">
                <span className="ms-provider-footnote">
                  {detectError
                    ? detectError
                    : nothingFound
                      ? 'Charts need Prometheus, Thanos, VictoriaMetrics or Mimir. Install Prometheus from the metrics card, or point Kanivet at a custom URL.'
                      : 'Detection checks that the service has ready pods and answers the Prometheus API.'}
                </span>
                <button type="button" className="ap-btn ap-btn--sm" onClick={handleRedetect} disabled={detecting}>
                  {detecting ? 'Detecting…' : 'Detect again'}
                </button>
              </div>
            </div>
          </section>

          {/* Source */}
          <section className="ms-section" aria-labelledby="ms-source">
            <div className="ms-section-head">
              <span className="ms-section-title" id="ms-source">Source</span>
            </div>
            <div className="ap-card ms-card">
              <div className="ms-row">
                <div className="ms-row-text">
                  <label className="ms-row-label" htmlFor="ms-provider">Provider</label>
                  <span className="ms-row-hint">
                    {settings.preferredProvider === 'auto' && 'Prometheus-compatible first, then Mimir, then metrics-server.'}
                    {settings.preferredProvider === 'prometheus' && (prometheus?.found ? `Always query ${prometheus.service || 'Prometheus'}.` : 'Nothing Prometheus-compatible was found.')}
                    {settings.preferredProvider === 'mimir' && (mimir?.found ? `Always query ${mimir.service || 'Mimir'}.` : 'No Mimir gateway was found.')}
                    {settings.preferredProvider === 'metrics-server' && 'Current values only — metrics-server keeps no history.'}
                    {settings.preferredProvider === 'custom' && 'Query the URL below directly.'}
                    {settings.preferredProvider === 'disabled' && 'Metrics panels are hidden and nothing is queried.'}
                  </span>
                </div>
                <select
                  id="ms-provider"
                  className="ap-select ms-select"
                  value={settings.preferredProvider}
                  onChange={(e) => setSettings({ ...settings, preferredProvider: e.target.value as MonitoringSettings['preferredProvider'] })}
                >
                  <option value="auto">{autoDetectLabel}</option>
                  <option value="prometheus" disabled={!prometheus?.found}>{providerOptionLabel('prometheus', 'Prometheus')}</option>
                  <option value="mimir" disabled={!mimir?.found}>{providerOptionLabel('mimir', 'Mimir')}</option>
                  <option value="metrics-server" disabled={!metricsServer?.found}>{providerOptionLabel('metrics-server', 'Metrics server')}</option>
                  <option value="custom">Custom Prometheus URL</option>
                  <option value="disabled">Disabled</option>
                </select>
              </div>

              {settings.preferredProvider === 'custom' && (
                <div className="ms-row ms-row-stacked">
                  <div className="ms-row-text">
                    <label className="ms-row-label" htmlFor="ms-custom-url">Custom Prometheus URL</label>
                    <span className="ms-row-hint">Full URL of a Prometheus-compatible API, for example http://prometheus.monitoring.svc:9090</span>
                  </div>
                  <input
                    id="ms-custom-url"
                    type="text"
                    className="ap-input ms-input"
                    placeholder="http://prometheus.monitoring.svc:9090"
                    value={settings.customPrometheusUrl || ''}
                    onChange={(e) => setSettings({ ...settings, customPrometheusUrl: e.target.value })}
                  />
                </div>
              )}

              {(mimirServices.length > 0 || mimirServicesError) && (
                <div className="ms-row">
                  <div className="ms-row-text">
                    <label className="ms-row-label" htmlFor="ms-mimir-instance">Mimir instance</label>
                    <span className="ms-row-hint">
                      {mimirServicesError
                        ? `Discovery failed: ${mimirServicesError}`
                        : mimirServices.length > 1
                          ? 'Several gateways are exposed — pick the one that holds this cluster’s container metrics.'
                          : 'Which Mimir gateway Kanivet queries for this cluster.'}
                    </span>
                  </div>
                  {mimirServices.length > 0 && (
                    <select
                      id="ms-mimir-instance"
                      className="ap-select ms-select"
                      value={chosenMimir}
                      onChange={(e) => void persistMimirService(e.target.value)}
                    >
                      <option value="">Automatic → {mimirServices[0].service} in {mimirServices[0].namespace}</option>
                      {mimirServices.map((s) => (
                        <option key={mimirKey(s)} value={mimirKey(s)}>
                          {s.service} in {s.namespace}
                        </option>
                      ))}
                    </select>
                  )}
                </div>
              )}

              {showTenantRow && (
                <div className="ms-row ms-row-stacked">
                  <div className="ms-row-text">
                    <label className="ms-row-label" htmlFor="ms-tenant">Mimir tenant</label>
                    <span className="ms-row-hint">
                      {tenantSaving
                        ? 'Saving…'
                        : 'Sent as X-Scope-OrgID. Multi-tenant Mimir returns nothing without it; Discover probes the gateway for tenants that hold data.'}
                    </span>
                  </div>
                  <div className="ms-inline">
                    <input
                      id="ms-tenant"
                      type="text"
                      className="ap-input ms-input"
                      placeholder="Tenant ID"
                      value={tenantInput}
                      onChange={(e) => setTenantInput(e.target.value)}
                    />
                    <button type="button" className="ap-btn" onClick={runTenantDiscovery} disabled={discovering}>
                      {discovering ? 'Probing…' : 'Discover'}
                    </button>
                  </div>
                  {tenantDiscoveryError && (
                    <span className="ms-error" role="alert">{tenantDiscoveryError}</span>
                  )}
                  {discoveredTenants.length > 0 && (
                    <div className="ms-chips" role="group" aria-label="Discovered tenants">
                      {discoveredTenants.map((t) => (
                        <button
                          key={t}
                          type="button"
                          className="ms-chip"
                          aria-pressed={tenantInput === t}
                          onClick={() => setTenantInput(t)}
                        >
                          {t}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          </section>

          {/* Display */}
          <section className="ms-section" aria-labelledby="ms-display">
            <div className="ms-section-head">
              <span className="ms-section-title" id="ms-display">Display</span>
            </div>
            <div className="ap-card ms-card">
              <div className="ms-row">
                <div className="ms-row-text">
                  <label className="ms-row-label" htmlFor="ms-refresh">Auto-refresh</label>
                </div>
                <select
                  id="ms-refresh"
                  className="ap-select ms-select ms-select-narrow"
                  value={settings.autoRefreshInterval}
                  onChange={(e) => setSettings({ ...settings, autoRefreshInterval: parseInt(e.target.value, 10) })}
                >
                  <option value="0">Off</option>
                  <option value="15">Every 15 seconds</option>
                  <option value="30">Every 30 seconds</option>
                  <option value="60">Every minute</option>
                  <option value="300">Every 5 minutes</option>
                </select>
              </div>
              <div className="ms-row">
                <div className="ms-row-text">
                  <span className="ms-row-label" id="ms-show-panel-label">Show metrics in the inspector</span>
                </div>
                <button
                  type="button"
                  role="switch"
                  className="ap-toggle ms-switch"
                  aria-checked={settings.showMetricsPanel}
                  aria-labelledby="ms-show-panel-label"
                  onClick={() => setSettings({ ...settings, showMetricsPanel: !settings.showMetricsPanel })}
                />
              </div>
            </div>
          </section>
        </div>

        <div className="ap-sheet-footer">
          <button type="button" className="ap-btn" onClick={onClose}>Cancel</button>
          <button type="button" className="ap-btn ap-btn--primary" onClick={handleSave}>Save</button>
        </div>
      </div>
    </div>
  );

  return typeof document !== 'undefined' ? createPortal(sheet, document.body) : sheet;
};

export default MonitoringSettingsModal;
