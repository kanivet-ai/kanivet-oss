import { useCallback, useEffect, useMemo, useState, useId } from 'react';
import { GearIcon } from '@radix-ui/react-icons';
import { useStore, MonitoringSettings } from '../store';
import { useShallow } from 'zustand/react/shallow';
import api from '../services/api';
import type { MetricsProviderInfo, MetricsProvidersStatus, MimirServiceInfo } from '../services/api/metrics';
import { getErrorMessage } from '../utils/errorMessage';
import { providerDetail, providerDisplayName } from './metrics/metricsProvider';
import './MonitoringSettingsModal.css';

interface ClusterMetricsSettingsProps {
  cluster: string;
  onSaved?: () => void;
  onCancel?: () => void;
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

const ClusterMetricsSettings = ({ cluster: activeCluster, onSaved, onCancel }: ClusterMetricsSettingsProps) => {
  const { monitoringSettings, setClusterMonitoringSettings } = useStore(useShallow((s) => ({
    monitoringSettings: s.monitoringSettingsByCluster[activeCluster] || s.monitoringSettings,
    setClusterMonitoringSettings: s.setClusterMonitoringSettings,
  })));
  const id = useId();
  const [expandedProvider, setExpandedProvider] = useState<MonitoringSettings['preferredProvider'] | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [loadingSettings, setLoadingSettings] = useState(true);
  const [settingsError, setSettingsError] = useState<string | null>(null);
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

  useEffect(() => setSaved(false), [settings, tenantInput, chosenMimir]);

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
    setLoadingSettings(true);
    setSettingsError(null);
    try {
      const s = await api.getClusterMetricsSettings(activeCluster);
      setMimirTenant(s.mimirTenant || '');
      setTenantInput(s.mimirTenant || '');
      setChosenMimir(s.mimirService ? `${s.mimirNamespace || ''}/${s.mimirService}` : '');
    } catch (err) {
      setSettingsError(getErrorMessage(err, 'Could not load metrics settings'));
    } finally {
      setLoadingSettings(false);
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
      setSaveError(getErrorMessage(err, 'Could not save the Mimir instance'));
      throw err;
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
      setSaveError(getErrorMessage(err, 'Could not save the Mimir tenant'));
      throw err;
    } finally {
      setTenantSaving(false);
    }
  }, [activeCluster]);

  const handleSave = async () => {
    setSaved(false);
    setSaveError(null);
    try {
      if (tenantInput !== mimirTenant) await persistTenant(tenantInput);
      setClusterMonitoringSettings(activeCluster, settings);
      setSaved(true);
      onSaved?.();
    } catch (err) {
      setSaveError(getErrorMessage(err, 'Could not save metrics settings'));
    }
  };

  const handleRedetect = () => {
    void detectProviders(true);
    void loadMimirServices();
  };

  const prometheus = detected?.prometheus;
  const mimir = detected?.mimir;
  const metricsServer = detected?.['metrics-server'];
  const nothingFound = !!detected && !prometheus?.found && !mimir?.found && !metricsServer?.found;

  const autoDetectLabel = useMemo(() => {
    const first = [prometheus, mimir].find((p) => p?.found && !p.needsTenant) || [prometheus, mimir].find((p) => p?.found);
    if (first) {
      const where = first.service ? `${first.service}${first.namespace ? ` in ${first.namespace}` : ''}` : providerDisplayName(first);
      return `Automatic → ${where}`;
    }
    if (metricsServer?.found) return 'Automatic → Metrics server';
    return detecting && !detected ? 'Automatic' : 'Automatic (nothing found yet)';
  }, [prometheus, mimir, metricsServer, detecting, detected]);

  const providerRows = [
    { key: 'auto' as const, name: 'Automatic', description: `${autoDetectLabel}. Prometheus-compatible first, then Mimir, then metrics-server.` },
    ...PROVIDER_ROWS.map(({ key, fallbackName, kind }) => ({
      key,
      name: detected?.[key]?.found ? providerDisplayName(detected[key]) || fallbackName : fallbackName,
      description: detecting && !detected ? `Looking for a ${kind}…` : providerStatusText(detected?.[key], kind),
    })),
    { key: 'custom' as const, name: 'Custom Prometheus URL', description: settings.customPrometheusUrl || 'Connect to a Prometheus-compatible API.' },
    { key: 'disabled' as const, name: 'Disabled', description: 'Hide metrics panels and stop querying metrics.' },
  ];

  const checkedLabel = relativeTime(detected?.checkedAt, now);

  return (
    <div className="cluster-metrics-settings">
        <div className="ms-body">
          {settingsError && (
            <div className="ms-inline" role="alert">
              <span className="ms-error">{settingsError}</span>
              <button type="button" className="ap-btn ap-btn--sm" onClick={() => void loadTenantSettings()}>Retry</button>
            </div>
          )}
          <section className="ms-section" aria-labelledby={`${id}-ms-providers`}>
            <div className="ms-section-head">
              <span className="ms-section-title" id={`${id}-ms-providers`}>Metrics provider</span>
              <span className="ms-section-meta">
                {detecting ? 'Checking…' : checkedLabel ? `Checked ${checkedLabel}` : ''}
              </span>
            </div>
            <div className="ap-card ms-card">
              {providerRows.map(({ key, name, description }) => {
                const info = key === 'prometheus' || key === 'mimir' || key === 'metrics-server' ? detected?.[key] : undefined;
                const isDetectedProvider = key === 'prometheus' || key === 'mimir' || key === 'metrics-server';
                const tone = detecting && !detected ? 'muted' : providerTone(info);
                const selected = settings.preferredProvider === key;
                const expanded = expandedProvider === key;
                const configurable = key === 'mimir' || key === 'custom';
                return (
                  <div key={key} className={`ms-provider-item${selected ? ' is-selected' : ''}`}>
                    <div className="ms-provider-row">
                      <span className={`ap-dot ap-dot--${tone} ms-provider-dot`} aria-hidden="true" />
                      <div className="ms-provider-text">
                        <div className="ms-provider-heading">
                          <span className="ms-provider-name">{name}</span>
                          {info?.found && (
                            <span className={`ap-badge ap-badge--sm ${tone === 'success' ? 'ap-badge--success' : 'ap-badge--warning'}`}>
                              {tone === 'success' ? 'Ready' : info.needsTenant ? 'Needs tenant' : 'Unverified'}
                            </span>
                          )}
                        </div>
                        <span className="ms-provider-status">{description}</span>
                      </div>
                      <div className="ms-provider-actions">
                        <button
                          type="button"
                          className={`ap-btn ap-btn--sm ms-provider-select${selected ? ' ap-btn--primary' : ''}`}
                          aria-label={`${selected ? 'Selected' : 'Select'} ${name}`}
                          aria-pressed={selected}
                          disabled={isDetectedProvider && !info?.found && !selected}
                          onClick={() => setSettings({ ...settings, preferredProvider: key })}
                        >
                          {selected ? 'Selected' : 'Select'}
                        </button>
                        {configurable ? (
                          <button
                            type="button"
                            className={`ap-icon-btn ms-provider-settings-toggle${expanded ? ' is-active' : ''}`}
                            aria-label={`${name} settings`}
                            title={`${name} settings`}
                            aria-expanded={expanded}
                            aria-controls={`${id}-ms-${key}-settings`}
                            onClick={() => setExpandedProvider(expanded ? null : key)}
                          >
                            <GearIcon />
                          </button>
                        ) : <span className="ms-provider-settings-spacer" aria-hidden="true" />}
                      </div>
                    </div>
                    {configurable && (
                      <div
                        id={`${id}-ms-${key}-settings`}
                        className="ms-provider-settings"
                        role="region"
                        aria-label={`${name} settings`}
                        hidden={!expanded}
                      >
                        {key === 'custom' && (
                          <div className="ms-row ms-row-stacked">
                            <div className="ms-row-text">
                              <label className="ms-row-label" htmlFor={`${id}-ms-custom-url`}>Custom Prometheus URL</label>
                              <span className="ms-row-hint">Full URL of a Prometheus-compatible API, for example http://prometheus.monitoring.svc:9090</span>
                            </div>
                            <input
                              id={`${id}-ms-custom-url`}
                              type="text"
                              className="ap-input ms-input"
                              placeholder="http://prometheus.monitoring.svc:9090"
                              value={settings.customPrometheusUrl || ''}
                              onChange={(e) => setSettings({ ...settings, customPrometheusUrl: e.target.value })}
                            />
                          </div>
                        )}

                        {key === 'mimir' && (
                          <div className="ms-row">
                            <div className="ms-row-text">
                              <label className="ms-row-label" htmlFor={`${id}-ms-mimir-instance`}>Mimir instance</label>
                              <span className="ms-row-hint">
                                {mimirServicesError
                                  ? `Discovery failed: ${mimirServicesError}`
                                  : mimirServices.length > 1
                                    ? 'Several gateways are exposed — pick the one that holds this cluster’s container metrics.'
                                    : mimirServices.length > 0
                                      ? 'Which Mimir gateway Kanivet queries for this cluster.'
                                      : 'No Mimir instances detected. Detect again to refresh the list.'}
                              </span>
                            </div>
                            {mimirServices.length > 0 && (
                              <select
                                id={`${id}-ms-mimir-instance`}
                                className="ap-select ms-select"
                                value={chosenMimir}
                                disabled={loadingSettings || !!settingsError}
                                onChange={(e) => { setSaveError(null); void persistMimirService(e.target.value).catch(() => {}); }}
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

                        {key === 'mimir' && (
                          <div className="ms-row ms-row-stacked">
                            <div className="ms-row-text">
                              <label className="ms-row-label" htmlFor={`${id}-ms-tenant`}>Mimir tenant</label>
                              <span className="ms-row-hint">
                                {tenantSaving
                                  ? 'Saving…'
                                  : 'Sent as X-Scope-OrgID. Multi-tenant Mimir returns nothing without it; Discover probes the gateway for tenants that hold data.'}
                              </span>
                            </div>
                            <div className="ms-inline">
                              <input
                                id={`${id}-ms-tenant`}
                                type="text"
                                className="ap-input ms-input"
                                placeholder="Tenant ID"
                                disabled={loadingSettings || !!settingsError || tenantSaving}
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
                    )}
                  </div>
                );
              })}
              <div className="ms-provider-footer">
                <span className="ms-provider-footnote">
                  {detectError
                    ? detectError
                    : nothingFound
                      ? 'No providers detected. Connect using a custom Prometheus URL or detect again.'
                      : 'Detection checks that the service has ready pods and answers the Prometheus API.'}
                </span>
                <button type="button" className="ap-btn ap-btn--sm" onClick={handleRedetect} disabled={detecting}>
                  {detecting ? 'Detecting…' : 'Detect again'}
                </button>
              </div>
            </div>
          </section>

          {/* Display */}
          <section className="ms-section" aria-labelledby={`${id}-ms-display`}>
            <div className="ms-section-head">
              <span className="ms-section-title" id={`${id}-ms-display`}>Display</span>
            </div>
            <div className="ap-card ms-card">
              <div className="ms-row">
                <div className="ms-row-text">
                  <label className="ms-row-label" htmlFor={`${id}-ms-refresh`}>Auto-refresh</label>
                </div>
                <select
                  id={`${id}-ms-refresh`}
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
                  <span className="ms-row-label" id={`${id}-ms-show-panel-label`}>Show metrics in the inspector</span>
                </div>
                <button
                  type="button"
                  role="switch"
                  className="ap-toggle ms-switch"
                  aria-checked={settings.showMetricsPanel}
                  aria-labelledby={`${id}-ms-show-panel-label`}
                  onClick={() => setSettings({ ...settings, showMetricsPanel: !settings.showMetricsPanel })}
                />
              </div>
            </div>
          </section>
        </div>

        <div className="ap-sheet-footer ms-settings-footer">
          <span className={saveError ? 'ms-error' : 'ms-save-status'} role={saveError ? 'alert' : 'status'}>
            {saveError || (saved ? 'Saved for this cluster' : '')}
          </span>
          {onCancel && <button type="button" className="ap-btn" onClick={onCancel}>Cancel</button>}
          <button type="button" className="ap-btn ap-btn--primary" disabled={loadingSettings || !!settingsError || tenantSaving} onClick={() => void handleSave()}>
            {tenantSaving ? 'Saving…' : 'Save'}
          </button>
        </div>
    </div>
  );
};

export default ClusterMetricsSettings;
