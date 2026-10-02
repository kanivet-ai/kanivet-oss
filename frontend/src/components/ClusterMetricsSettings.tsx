import { useCallback, useEffect, useRef, useState, useId } from 'react';
import { GearIcon } from '@radix-ui/react-icons';
import { useStore, MonitoringSettings } from '../store';
import { useShallow } from 'zustand/react/shallow';
import api from '../services/api';
import type {
  MetricsProviderInfo,
  MetricsProvidersStatus,
  MimirServiceInfo,
} from '../services/api/metrics';
import { getErrorMessage } from '../utils/errorMessage';
import {
  activeProvider,
  providerDetail,
  providerDisplayName,
  requestedProvider,
} from './metrics/metricsProvider';
import {
  changedSettings,
  defaultExpandedProvider,
  mimirSettingsChange,
  saveClusterMetrics,
  type ConfigurableProvider,
  type ProviderKey,
} from './metrics/clusterMetricsForm';
import './MonitoringSettingsModal.css';

interface ClusterMetricsSettingsProps {
  cluster: string;
  onSaved?: () => void;
  onCancel?: () => void;
  /** Provider whose settings start open, for callers that know what the user
   * came to change. */
  initialExpanded?: ConfigurableProvider | null;
  /** Put the cursor in the Mimir tenant field once it can be edited. */
  focusTenant?: boolean;
}

const PROVIDER_ROWS: {
  key: ProviderKey;
  fallbackName: string;
  kind: string;
}[] = [
  {
    key: 'prometheus',
    fallbackName: 'Prometheus',
    kind: 'Prometheus-compatible endpoint',
  },
  { key: 'mimir', fallbackName: 'Mimir', kind: 'Mimir or Cortex gateway' },
  {
    key: 'metrics-server',
    fallbackName: 'Metrics server',
    kind: 'metrics.k8s.io',
  },
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

const providerTone = (
  info: MetricsProviderInfo | undefined,
): 'success' | 'warning' | 'muted' => {
  if (!info?.found) return 'muted';
  if (info.needsTenant || info.verified === false) return 'warning';
  return 'success';
};

const providerStatusText = (
  info: MetricsProviderInfo | undefined,
  kind: string,
): string => {
  if (!info?.found) return info?.reason || `No ${kind} found`;
  if (info.needsTenant) return 'Reachable — needs a tenant (X-Scope-OrgID)';
  const detail = providerDetail(info);
  if (info.verified === false && info.reason)
    return `${detail} · ${info.reason}`;
  return detail || 'Found';
};

const ClusterMetricsSettings = ({
  cluster: activeCluster,
  onSaved,
  onCancel,
  initialExpanded,
  focusTenant = false,
}: ClusterMetricsSettingsProps) => {
  const { monitoringSettings, setClusterMonitoringSettings } = useStore(
    useShallow((s) => ({
      monitoringSettings:
        s.monitoringSettingsByCluster[activeCluster] || s.monitoringSettings,
      setClusterMonitoringSettings: s.setClusterMonitoringSettings,
    })),
  );
  const id = useId();
  const [expandedProvider, setExpandedProvider] =
    useState<ConfigurableProvider | null>(() =>
      defaultExpandedProvider(
        initialExpanded,
        monitoringSettings.preferredProvider,
        null,
      ),
    );
  // Once the user opens or closes a gear, detection landing later must not
  // undo it.
  const expansionTouched = useRef(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadingSettings, setLoadingSettings] = useState(true);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  // Only the fields the user touched are held here; everything else is read
  // from the store on each render. A form left open in the Cluster settings
  // tab therefore shows, and saves around, changes made elsewhere meanwhile.
  const [edits, setEdits] = useState<Partial<MonitoringSettings>>({});
  const settings: MonitoringSettings = { ...monitoringSettings, ...edits };
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
  const tenantField = useRef<HTMLInputElement>(null);
  const tenantFocused = useRef(false);

  // Per-cluster Mimir service. A cluster can expose several Mimir gateways
  // (host-level plus vcluster-mapped copies) and only some hold the container
  // metrics we query, so the operator picks which one. Empty = auto-discovery.
  // Like the tenant, the choice is part of the form and only reaches the
  // backend on Save, so Cancel really cancels it.
  const [mimirServices, setMimirServices] = useState<MimirServiceInfo[]>([]);
  const [mimirServicesError, setMimirServicesError] = useState<string | null>(
    null,
  );
  const [tenantDiscoveryError, setTenantDiscoveryError] = useState<
    string | null
  >(null);
  const [savedMimir, setSavedMimir] = useState<string>('');
  const [chosenMimir, setChosenMimir] = useState<string>('');

  useEffect(() => setSaved(false), [edits, tenantInput, chosenMimir]);

  const mimirKey = (s: { namespace: string; service: string }) =>
    `${s.namespace}/${s.service}`;

  const detectProviders = useCallback(
    async (refresh = false) => {
      if (!activeCluster) {
        setDetecting(false);
        return;
      }
      setDetecting(true);
      setDetectError(null);
      try {
        const providers = await api.detectMetricsProvider(activeCluster, {
          refresh,
        });
        setDetected(providers || {});
        setNow(Date.now());
      } catch (err) {
        console.error('Failed to detect providers:', err);
        setDetectError(
          getErrorMessage(
            err,
            'Could not reach the backend to detect providers',
          ),
        );
      } finally {
        setDetecting(false);
      }
    },
    [activeCluster],
  );

  const loadTenantSettings = useCallback(async () => {
    if (!activeCluster) return;
    setLoadingSettings(true);
    setSettingsError(null);
    try {
      const s = await api.getClusterMetricsSettings(activeCluster);
      const instance = s.mimirService
        ? `${s.mimirNamespace || ''}/${s.mimirService}`
        : '';
      setMimirTenant(s.mimirTenant || '');
      setTenantInput(s.mimirTenant || '');
      setSavedMimir(instance);
      setChosenMimir(instance);
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
      setMimirServicesError(
        getErrorMessage(
          err,
          'Could not look for Mimir services in this cluster',
        ),
      );
    }
  }, [activeCluster]);

  useEffect(() => {
    void detectProviders();
    void loadTenantSettings();
    void loadMimirServices();
  }, [detectProviders, loadTenantSettings, loadMimirServices]);

  // The Mimir fields are re-read when they are saved elsewhere (the sheet
  // opened from a metrics card while this tab stays open), unless the user
  // is editing them here: a stale copy saved later would undo that change.
  const mimirDirty = useRef(false);
  mimirDirty.current =
    tenantInput !== mimirTenant || chosenMimir !== savedMimir;
  useEffect(() => {
    const onChanged = (event: Event) => {
      const changed = (event as CustomEvent).detail?.cluster;
      if (changed !== activeCluster || mimirDirty.current) return;
      void loadTenantSettings();
    };
    window.addEventListener('metrics-settings-changed', onChanged);
    return () =>
      window.removeEventListener('metrics-settings-changed', onChanged);
  }, [activeCluster, loadTenantSettings]);

  // Keep the "checked … ago" label honest while the sheet is open.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(timer);
  }, []);

  // Detection can reveal that Mimir needs a tenant after the form opened;
  // show the field then, unless the user already chose what to look at.
  useEffect(() => {
    if (expansionTouched.current) return;
    const next = defaultExpandedProvider(
      initialExpanded,
      monitoringSettings.preferredProvider,
      detected,
    );
    if (next) setExpandedProvider(next);
  }, [detected, initialExpanded, monitoringSettings.preferredProvider]);

  // The field is disabled while the saved tenant loads, and a disabled input
  // cannot take focus, so wait for it.
  useEffect(() => {
    if (
      !focusTenant ||
      tenantFocused.current ||
      loadingSettings ||
      settingsError
    )
      return;
    if (expandedProvider !== 'mimir') return;
    tenantFocused.current = true;
    tenantField.current?.focus();
  }, [focusTenant, loadingSettings, settingsError, expandedProvider]);

  const runTenantDiscovery = useCallback(async () => {
    if (!activeCluster) return;
    setDiscovering(true);
    setTenantDiscoveryError(null);
    try {
      const hints = tenantInput ? [tenantInput] : [];
      const tenants = await api.discoverMimirTenants(activeCluster, hints);
      setDiscoveredTenants(tenants);
      if (tenants.length === 0) {
        setTenantDiscoveryError(
          'Discovery ran but found no tenant with data. Enter the tenant ID manually.',
        );
      }
    } catch (err) {
      console.error('Failed to discover tenants:', err);
      setDiscoveredTenants([]);
      setTenantDiscoveryError(getErrorMessage(err, 'Tenant discovery failed'));
    } finally {
      setDiscovering(false);
    }
  }, [activeCluster, tenantInput]);

  const prometheus = detected?.prometheus;
  const mimir = detected?.mimir;
  const metricsServer = detected?.['metrics-server'];
  const nothingFound =
    !!detected && !prometheus?.found && !mimir?.found && !metricsServer?.found;

  const selectedProvider =
    settings.preferredProvider === 'auto'
      ? requestedProvider(activeProvider(detected)?.type) ||
        (metricsServer?.found ? 'metrics-server' : undefined)
      : settings.preferredProvider;

  const handleSave = async () => {
    if (!selectedProvider || !activeCluster) return;
    setSaved(false);
    setSaveError(null);
    setSaving(true);
    const local = changedSettings(edits, monitoringSettings);
    const previousLocal: Partial<MonitoringSettings> = {};
    (Object.keys(local) as (keyof MonitoringSettings)[]).forEach((key) => {
      (previousLocal as Record<string, unknown>)[key] = monitoringSettings[key];
    });
    const result = await saveClusterMetrics({
      local,
      previousLocal,
      backend: mimirSettingsChange(
        { tenant: mimirTenant, instance: savedMimir },
        { tenant: tenantInput, instance: chosenMimir },
      ),
      writeLocal: (s) => setClusterMonitoringSettings(activeCluster, s),
      writeBackend: async (change) => {
        const updated = await api.setClusterMetricsSettings(
          activeCluster,
          change,
        );
        setMimirTenant(updated.mimirTenant || '');
        setSavedMimir(
          updated.mimirService
            ? `${updated.mimirNamespace || ''}/${updated.mimirService}`
            : '',
        );
      },
      describe: getErrorMessage,
    });
    setSaving(false);
    if (!result.ok) {
      setSaveError(result.message);
      return;
    }
    setSaved(true);
    onSaved?.();
  };

  const handleRedetect = () => {
    void detectProviders(true);
    void loadMimirServices();
  };

  const providerRows = [
    ...PROVIDER_ROWS.map(({ key, fallbackName, kind }) => ({
      key,
      name: detected?.[key]?.found
        ? providerDisplayName(detected[key]) || fallbackName
        : fallbackName,
      description:
        detecting && !detected
          ? `Looking for a ${kind}…`
          : providerStatusText(detected?.[key], kind),
    })),
    {
      key: 'disabled' as const,
      name: 'Disabled',
      description: 'Hide metrics panels and stop querying metrics.',
    },
  ];

  const checkedLabel = relativeTime(detected?.checkedAt, now);

  return (
    <div className="cluster-metrics-settings">
      <div className="ms-body">
        {settingsError && (
          <div className="ms-inline" role="alert">
            <span className="ms-error">{settingsError}</span>
            <button
              type="button"
              className="ap-btn ap-btn--sm"
              onClick={() => void loadTenantSettings()}
            >
              Retry
            </button>
          </div>
        )}
        <section className="ms-section" aria-labelledby={`${id}-ms-providers`}>
          <div className="ms-section-head">
            <span className="ms-section-title" id={`${id}-ms-providers`}>
              Metrics provider
            </span>
            <span className="ms-section-meta">
              {detecting
                ? 'Checking…'
                : checkedLabel
                  ? `Checked ${checkedLabel}`
                  : ''}
            </span>
          </div>
          <div className="ap-card ms-card">
            {providerRows.map(({ key, name, description }) => {
              const isDetectedProvider =
                key === 'prometheus' ||
                key === 'mimir' ||
                key === 'metrics-server';
              const info = isDetectedProvider ? detected?.[key] : undefined;
              const tone =
                detecting && !detected ? 'muted' : providerTone(info);
              const selected = selectedProvider === key;
              const expanded = expandedProvider === key;
              const configurable = key === 'mimir';
              return (
                <div
                  key={key}
                  className={`ms-provider-item${selected ? ' is-selected' : ''}`}
                >
                  <div className="ms-provider-row">
                    <span
                      className={`ap-dot ap-dot--${tone} ms-provider-dot`}
                      aria-hidden="true"
                    />
                    <div className="ms-provider-text">
                      <div className="ms-provider-heading">
                        <span className="ms-provider-name">{name}</span>
                        {info?.found && (
                          <span
                            className={`ap-badge ap-badge--sm ${tone === 'success' ? 'ap-badge--success' : 'ap-badge--warning'}`}
                          >
                            {tone === 'success'
                              ? 'Ready'
                              : info.needsTenant
                                ? 'Needs tenant'
                                : 'Unverified'}
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
                        disabled={
                          isDetectedProvider && !info?.found && !selected
                        }
                        onClick={() =>
                          setEdits((e) => ({ ...e, preferredProvider: key }))
                        }
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
                          onClick={() => {
                            expansionTouched.current = true;
                            setExpandedProvider(expanded ? null : key);
                          }}
                        >
                          <GearIcon />
                        </button>
                      ) : (
                        <span
                          className="ms-provider-settings-spacer"
                          aria-hidden="true"
                        />
                      )}
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
                      <div className="ms-row">
                        <div className="ms-row-text">
                          <label
                            className="ms-row-label"
                            htmlFor={`${id}-ms-mimir-instance`}
                          >
                            Mimir instance
                          </label>
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
                            disabled={
                              loadingSettings || !!settingsError || saving
                            }
                            onChange={(e) => setChosenMimir(e.target.value)}
                          >
                            <option value="">
                              Automatic → {mimirServices[0].service} in{' '}
                              {mimirServices[0].namespace}
                            </option>
                            {mimirServices.map((s) => (
                              <option key={mimirKey(s)} value={mimirKey(s)}>
                                {s.service} in {s.namespace}
                              </option>
                            ))}
                          </select>
                        )}
                      </div>

                      <div className="ms-row ms-row-stacked">
                        <div className="ms-row-text">
                          <label
                            className="ms-row-label"
                            htmlFor={`${id}-ms-tenant`}
                          >
                            Mimir tenant
                          </label>
                          <span className="ms-row-hint">
                            {saving
                              ? 'Saving…'
                              : 'Sent as X-Scope-OrgID. Multi-tenant Mimir returns nothing without it; Discover probes the gateway for tenants that hold data.'}
                          </span>
                        </div>
                        <div className="ms-inline">
                          <input
                            ref={tenantField}
                            id={`${id}-ms-tenant`}
                            type="text"
                            className="ap-input ms-input"
                            placeholder="Tenant ID"
                            disabled={
                              loadingSettings || !!settingsError || saving
                            }
                            value={tenantInput}
                            onChange={(e) => setTenantInput(e.target.value)}
                          />
                          <button
                            type="button"
                            className="ap-btn"
                            onClick={runTenantDiscovery}
                            disabled={discovering}
                          >
                            {discovering ? 'Probing…' : 'Discover'}
                          </button>
                        </div>
                        {tenantDiscoveryError && (
                          <span className="ms-error" role="alert">
                            {tenantDiscoveryError}
                          </span>
                        )}
                        {discoveredTenants.length > 0 && (
                          <div
                            className="ms-chips"
                            role="group"
                            aria-label="Discovered tenants"
                          >
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
                    ? 'No providers detected. Install Prometheus or point Kanivet at Mimir, then detect again.'
                    : 'Detection checks that the service has ready pods and answers the Prometheus API.'}
              </span>
              <button
                type="button"
                className="ap-btn ap-btn--sm"
                onClick={handleRedetect}
                disabled={detecting}
              >
                {detecting ? 'Detecting…' : 'Detect again'}
              </button>
            </div>
          </div>
        </section>

        {/* Display. Auto-refresh is not offered: nothing reads it yet. */}
        <section className="ms-section" aria-labelledby={`${id}-ms-display`}>
          <div className="ms-section-head">
            <span className="ms-section-title" id={`${id}-ms-display`}>
              Display
            </span>
          </div>
          <div className="ap-card ms-card">
            <div className="ms-row">
              <div className="ms-row-text">
                <span className="ms-row-label" id={`${id}-ms-show-panel-label`}>
                  Show metrics in the inspector
                </span>
              </div>
              <button
                type="button"
                role="switch"
                className="ap-toggle ms-switch"
                aria-checked={settings.showMetricsPanel}
                aria-labelledby={`${id}-ms-show-panel-label`}
                onClick={() =>
                  setEdits((e) => ({
                    ...e,
                    showMetricsPanel: !settings.showMetricsPanel,
                  }))
                }
              />
            </div>
          </div>
        </section>
      </div>

      <div className="ap-sheet-footer ms-settings-footer">
        <span
          className={saveError ? 'ms-error' : 'ms-save-status'}
          role={saveError ? 'alert' : 'status'}
        >
          {saveError || (saved ? 'Saved for this cluster' : '')}
        </span>
        {onCancel && (
          <button type="button" className="ap-btn" onClick={onCancel}>
            Cancel
          </button>
        )}
        <button
          type="button"
          className="ap-btn ap-btn--primary"
          disabled={
            !selectedProvider || loadingSettings || !!settingsError || saving
          }
          onClick={() => void handleSave()}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
};

export default ClusterMetricsSettings;
