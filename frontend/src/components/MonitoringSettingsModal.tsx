import { useState, useEffect, useCallback } from 'react';
import { useStore, MonitoringSettings } from '../store';
import api from '../services/api';
import type { MimirServiceInfo } from '../services/api/metrics';
import './MonitoringSettingsModal.css';

interface ProviderInfo {
  type: string;
  found: boolean;
  namespace?: string;
  service?: string;
  port?: number;
  url?: string;
}

interface MonitoringSettingsModalProps {
  onClose: () => void;
  cluster?: string;
}

const MonitoringSettingsModal = ({ onClose, cluster }: MonitoringSettingsModalProps) => {
  const { monitoringSettings, setMonitoringSettings, currentTab } = useStore();
  const [settings, setSettings] = useState<MonitoringSettings>({ ...monitoringSettings });
  const [detectedProviders, setDetectedProviders] = useState<Record<string, ProviderInfo>>({});
  const [loading, setLoading] = useState(true);

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
  const [chosenMimir, setChosenMimir] = useState<string>('');

  const activeCluster = cluster || currentTab;
  const mimirKey = (s: { namespace: string; service: string }) => `${s.namespace}/${s.service}`;

  const detectProviders = useCallback(async () => {
    if (!activeCluster) {
      setLoading(false);
      return;
    }
    try {
      const providers = await api.detectMetricsProvider(activeCluster);
      setDetectedProviders(providers || {});
    } catch (err) {
      console.error('Failed to detect providers:', err);
    } finally {
      setLoading(false);
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
    } catch (err) {
      console.error('Failed to list Mimir services:', err);
    }
  }, [activeCluster]);

  useEffect(() => {
    detectProviders();
    loadTenantSettings();
    loadMimirServices();
  }, [detectProviders, loadTenantSettings, loadMimirServices]);

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
    try {
      const hints = tenantInput ? [tenantInput] : [];
      const tenants = await api.discoverMimirTenants(activeCluster, hints);
      setDiscoveredTenants(tenants);
    } catch (err) {
      console.error('Failed to discover tenants:', err);
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

  const getProviderLabel = (key: string, info: ProviderInfo): string => {
    if (!info.found) return `${key} (not found)`;
    const details = [];
    if (info.service) details.push(info.service);
    if (info.namespace) details.push(`in ${info.namespace}`);
    if (info.port) details.push(`:${info.port}`);
    return details.length > 0 ? `${key} (${details.join(' ')})` : key;
  };

  const getAutoDetectLabel = (): string => {
    const prometheus = detectedProviders.prometheus;
    const mimir = detectedProviders.mimir;
    const metricsServer = detectedProviders['metrics-server'];
    if (prometheus?.found) {
      const parts = [];
      if (prometheus.service) parts.push(prometheus.service);
      if (prometheus.namespace) parts.push(`in ${prometheus.namespace}`);
      return `Auto-detect → ${parts.join(' ') || 'Prometheus'}`;
    }
    if (mimir?.found) {
      const parts = [];
      if (mimir.service) parts.push(mimir.service);
      if (mimir.namespace) parts.push(`in ${mimir.namespace}`);
      return `Auto-detect → ${parts.join(' ') || 'Mimir'}`;
    }
    if (metricsServer?.found) {
      return 'Auto-detect → Metrics Server';
    }
    return 'Auto-detect (no provider found)';
  };

  return (
    <div className="monitoring-settings-overlay" onClick={onClose}>
      <div className="monitoring-settings-modal" onClick={(e) => e.stopPropagation()}>
        <div className="monitoring-settings-header">
          <h3>Monitoring Settings</h3>
          <button className="close-btn" onClick={onClose}>×</button>
        </div>

        <div className="monitoring-settings-content">
          <div className="setting-group">
            <label>Metrics Provider</label>
            {loading ? (
              <div className="setting-loading">Detecting providers...</div>
            ) : (
              <>
                <select
                  value={settings.preferredProvider}
                  onChange={(e) => setSettings({ ...settings, preferredProvider: e.target.value as MonitoringSettings['preferredProvider'] })}
                >
                  <option value="auto">{getAutoDetectLabel()}</option>
                  <option value="prometheus" disabled={!detectedProviders.prometheus?.found}>
                    {getProviderLabel('Prometheus', detectedProviders.prometheus || { type: 'prometheus', found: false })}
                  </option>
                  <option value="mimir" disabled={!detectedProviders.mimir?.found}>
                    {getProviderLabel('Mimir', detectedProviders.mimir || { type: 'mimir', found: false })}
                  </option>
                  <option value="metrics-server" disabled={!detectedProviders['metrics-server']?.found}>
                    {getProviderLabel('Metrics Server', detectedProviders['metrics-server'] || { type: 'metrics-server', found: false })}
                  </option>
                  <option value="custom">Custom Prometheus URL</option>
                  <option value="disabled">Disabled</option>
                </select>
                <span className="setting-hint">
                  {settings.preferredProvider === 'auto' && 'Tries Prometheus, Mimir, then Metrics Server'}
                  {settings.preferredProvider === 'prometheus' && detectedProviders.prometheus?.found &&
                    `Using ${detectedProviders.prometheus.service || 'Prometheus'} in ${detectedProviders.prometheus.namespace || 'cluster'}`}
                  {settings.preferredProvider === 'mimir' && detectedProviders.mimir?.found &&
                    `Using ${detectedProviders.mimir.service || 'Mimir'} in ${detectedProviders.mimir.namespace || 'cluster'}`}
                  {settings.preferredProvider === 'metrics-server' && 'Using Kubernetes Metrics Server API'}
                  {settings.preferredProvider === 'custom' && 'Enter a custom Prometheus URL below'}
                  {settings.preferredProvider === 'disabled' && 'Metrics collection is disabled'}
                </span>
              </>
            )}
          </div>

          {mimirServices.length > 0 && (
            <div className="setting-group">
              <label>Mimir Instance</label>
              <select
                value={chosenMimir}
                onChange={(e) => void persistMimirService(e.target.value)}
              >
                <option value="">Auto-detect → {mimirServices[0].service} in {mimirServices[0].namespace}</option>
                {mimirServices.map((s) => (
                  <option key={mimirKey(s)} value={mimirKey(s)}>
                    {s.service} in {s.namespace}
                  </option>
                ))}
              </select>
              <span className="setting-hint">
                {mimirServices.length > 1
                  ? 'This cluster exposes several Mimir gateways — pick the one that holds your container metrics.'
                  : 'Override which Mimir Kanivet queries for this cluster.'}
              </span>
            </div>
          )}

          {detectedProviders.mimir?.found && (
            <div className="setting-group">
              <label>Mimir Tenant (X-Scope-OrgID)</label>
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  type="text"
                  className="setting-input"
                  placeholder="Tenant ID"
                  value={tenantInput}
                  onChange={(e) => setTenantInput(e.target.value)}
                  style={{ flex: 1 }}
                />
                <button
                  className="cancel-btn"
                  type="button"
                  onClick={runTenantDiscovery}
                  disabled={discovering}
                >
                  {discovering ? 'Probing…' : 'Discover'}
                </button>
              </div>
              {discoveredTenants.length > 0 && (
                <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {discoveredTenants.map((t) => (
                    <button
                      key={t}
                      type="button"
                      className={tenantInput === t ? 'save-btn' : 'cancel-btn'}
                      onClick={() => setTenantInput(t)}
                      style={{ padding: '4px 10px', fontSize: 12 }}
                    >
                      {t}
                    </button>
                  ))}
                </div>
              )}
              <span className="setting-hint">
                {tenantSaving
                  ? 'Saving…'
                  : 'Multi-tenant Mimir requires this header. Click Discover to probe the cluster’s Mimir for tenants that have data.'}
              </span>
            </div>
          )}

          {settings.preferredProvider === 'custom' && (
            <div className="setting-group">
              <label>Custom Prometheus URL</label>
              <input
                type="text"
                className="setting-input"
                placeholder="http://prometheus.monitoring.svc:9090"
                value={settings.customPrometheusUrl || ''}
                onChange={(e) => setSettings({ ...settings, customPrometheusUrl: e.target.value })}
              />
              <span className="setting-hint">
                Full URL to your Prometheus server (e.g., http://prometheus:9090)
              </span>
            </div>
          )}

          <div className="setting-group">
            <label>Auto-refresh Interval</label>
            <select
              value={settings.autoRefreshInterval}
              onChange={(e) => setSettings({ ...settings, autoRefreshInterval: parseInt(e.target.value) })}
            >
              <option value="0">Disabled</option>
              <option value="15">15 seconds</option>
              <option value="30">30 seconds</option>
              <option value="60">1 minute</option>
              <option value="300">5 minutes</option>
            </select>
          </div>

          <div className="setting-group checkbox-group">
            <label>
              <input
                type="checkbox"
                checked={settings.showMetricsPanel}
                onChange={(e) => setSettings({ ...settings, showMetricsPanel: e.target.checked })}
              />
              Show metrics panel in detail views
            </label>
          </div>

          {!loading && !detectedProviders.prometheus?.found && !detectedProviders.mimir?.found && !detectedProviders['metrics-server']?.found && (
            <div className="setting-group warning-group">
              <span className="warning-text">No metrics provider detected in cluster. You can install Prometheus from the metrics panel or configure a custom URL.</span>
            </div>
          )}
        </div>

        <div className="monitoring-settings-footer">
          <button className="cancel-btn" onClick={onClose}>Cancel</button>
          <button className="save-btn" onClick={handleSave}>Save</button>
        </div>
      </div>
    </div>
  );
};

export default MonitoringSettingsModal;
