import { useEffect, useState, useCallback, useRef } from 'react';
import api from '../services/api';
import { useStore } from '../store';
import { ArgoStats, ArgoAppListEntry } from '../services/api/resources';
import { useVisibleInterval } from '../hooks/useVisibleInterval';
import './ArgoOverviewWidget.css';

interface Props {
  cluster: string;
}

const ArgoOverviewWidget = ({ cluster }: Props) => {
  const [stats, setStats] = useState<ArgoStats | null>(null);
  const [installed, setInstalled] = useState<boolean | null>(null);

  const load = useCallback(
    async (silent = false) => {
      try {
        const det = await api.getArgoDetection(cluster);
        setInstalled(!!det.installed);
        if (!det.installed) {
          setStats(null);
          return;
        }
        const s = await api.getArgoStats(cluster);
        setStats((prev) => (s && s.total > 0 ? s : prev ?? s));
      } catch {
        if (!silent) setStats((prev) => prev ?? null);
      }
    },
    [cluster],
  );

  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setStats(null);
    setInstalled(null);
    load();
  }, [load]);
  useVisibleInterval(() => load(true), 15000, { targetRef: rootRef });

  if (installed === null) return null;
  if (!installed) return null;
  if (!stats) return null;

  const open = () => {
    const overviewNode = { id: 'argo-overview', label: 'Apps Overview', type: 'argo-overview' as const, data: { cluster } };
    useStore.getState().selectNode(overviewNode as any);
    useStore.getState().openResourceListTab(
      { name: 'argo-applications-overview', group: 'argoproj.io', version: 'v1alpha1', kind: 'ArgoApplicationsOverview', namespaced: false },
      cluster,
      true,
    );
  };

  const openApp = (entry: ArgoAppListEntry) => {
    const apiVersion = 'argoproj.io/v1alpha1';
    const resource = { name: 'applications', group: 'argoproj.io', version: 'v1alpha1', kind: 'Application', namespaced: true };
    const item = { name: entry.name, namespace: entry.namespace, uid: `${entry.namespace || 'cluster'}-${entry.name}`, kind: 'Application', apiVersion };
    const { openDetailTab, loadDetails } = useStore.getState();
    openDetailTab(resource, item, cluster, false);
    loadDetails(cluster, resource, item).catch(() => {});
  };

  const synced = stats.bySync['Synced'] || 0;
  const outOfSync = stats.bySync['OutOfSync'] || 0;
  const unknown = stats.bySync['Unknown'] || 0;
  const healthy = stats.byHealth['Healthy'] || 0;
  const progressing = stats.byHealth['Progressing'] || 0;
  const degraded = stats.byHealth['Degraded'] || 0;
  const missing = stats.byHealth['Missing'] || 0;
  const suspended = stats.byHealth['Suspended'] || 0;

  return (
    <div className="argo-widget" ref={rootRef}>
      <div className="argo-widget-left">
        <div className="argo-widget-header">
          <h3 className="argo-widget-title">Argo CD</h3>
          <button className="argo-widget-link" onClick={open}>Open Apps Overview →</button>
        </div>

        <div className="argo-widget-tiles">
          <div className="argo-widget-tile">
            <span className="argo-widget-tile-value">{stats.total}</span>
            <span className="argo-widget-tile-label">Applications</span>
          </div>
          <div className="argo-widget-tile argo-widget-syncing">
            <span className="argo-widget-tile-value">{stats.syncing}</span>
            <span className="argo-widget-tile-label">Syncing</span>
          </div>
          <div className="argo-widget-tile">
            <span className="argo-widget-tile-value">{stats.recentSync24h}</span>
            <span className="argo-widget-tile-label">Synced (24h)</span>
          </div>
        </div>

        <div className="argo-widget-bars">
          <div className="argo-widget-bar-row">
            <div className="argo-widget-bar-header">
              <span className="argo-widget-bar-label">Sync</span>
            </div>
            <Bar
              segments={[
                { label: 'Synced', value: synced, className: 'arg-seg-good' },
                { label: 'OutOfSync', value: outOfSync, className: 'arg-seg-warn' },
                { label: 'Unknown', value: unknown, className: 'arg-seg-neutral' },
              ]}
            />
          </div>
          <div className="argo-widget-bar-row">
            <div className="argo-widget-bar-header">
              <span className="argo-widget-bar-label">Health</span>
            </div>
            <Bar
              segments={[
                { label: 'Healthy', value: healthy, className: 'arg-seg-good' },
                { label: 'Progressing', value: progressing, className: 'arg-seg-warn' },
                { label: 'Degraded', value: degraded, className: 'arg-seg-bad' },
                { label: 'Missing', value: missing, className: 'arg-seg-bad' },
                { label: 'Suspended', value: suspended, className: 'arg-seg-neutral' },
              ]}
            />
          </div>
        </div>
      </div>

      <div className="argo-widget-right">
        {stats.topUnhealthy && stats.topUnhealthy.length > 0 ? (
          <div className="argo-widget-unhealthy">
            <div className="argo-widget-section-label">Needs attention</div>
            <div className="argo-widget-unhealthy-list">
              {stats.topUnhealthy.map((app) => (
                <button
                  type="button"
                  key={`${app.namespace}/${app.name}`}
                  className="argo-widget-unhealthy-row"
                  onClick={() => openApp(app)}
                >
                  <span className="argo-widget-unhealthy-name">{app.name}</span>
                  <span className="argo-widget-unhealthy-ns">{app.namespace}</span>
                  <span className={`argo-widget-health-pill ${app.health.toLowerCase()}`}>{app.health}</span>
                </button>
              ))}
            </div>
          </div>
        ) : (
          <div className="argo-widget-allgood">
            <div className="argo-widget-section-label">Status</div>
            <div className="argo-widget-allgood-msg">All applications healthy</div>
          </div>
        )}
      </div>
    </div>
  );
};

const Bar = ({ segments }: { segments: { label: string; value: number; className: string }[] }) => {
  const total = segments.reduce((acc, s) => acc + s.value, 0) || 1;
  return (
    <div className="argo-widget-bar">
      <div className="argo-widget-bar-track">
        {segments.map((s) => {
          if (s.value === 0) return null;
          const pct = (s.value / total) * 100;
          return (
            <div
              key={s.label}
              className={`argo-widget-bar-seg ${s.className}`}
              style={{ width: `${pct}%` }}
              title={`${s.label}: ${s.value}`}
            />
          );
        })}
      </div>
      <div className="argo-widget-bar-legend">
        {segments.map((s) =>
          s.value > 0 ? (
            <span key={s.label} className={`argo-widget-bar-legend-item ${s.className}`}>
              {s.label} <span>{s.value}</span>
            </span>
          ) : null,
        )}
      </div>
    </div>
  );
};

export default ArgoOverviewWidget;
