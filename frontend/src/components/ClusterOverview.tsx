import React, { useEffect, useRef, type ReactNode } from 'react';
import { DesktopIcon, CubeIcon, Link2Icon, FileIcon, ArrowTopRightIcon, ArrowBottomRightIcon, ExclamationTriangleIcon } from '@radix-ui/react-icons';
import { useStore } from '../store';
import api from '../services/api';
import { DashboardOverviewMetric } from '../store/types';
import './ClusterOverview.css';

const ICONS: Record<DashboardOverviewMetric['iconKey'], ReactNode> = {
  nodes: <DesktopIcon />,
  pods: <CubeIcon />,
  services: <Link2Icon />,
  namespaces: <FileIcon />,
};

const buildMetrics = (counts: Record<string, number>): DashboardOverviewMetric[] => ([
  { title: 'Nodes', value: counts[':nodes'] || 0, color: '#10b981', iconKey: 'nodes' },
  { title: 'Pods', value: counts[':pods'] || 0, color: '#3b82f6', iconKey: 'pods' },
  { title: 'Services', value: counts[':services'] || 0, color: '#8b5cf6', iconKey: 'services' },
  { title: 'Namespaces', value: counts[':namespaces'] || 0, color: '#f59e0b', iconKey: 'namespaces' },
]);

const EMPTY_DASHBOARD = { metrics: [] as DashboardOverviewMetric[], resourceUsage: null, podStatus: null, nodeStatus: null, events: [] as any[], alerts: [] as any[], clusterInfo: null };

const ClusterOverview: React.FC = () => {
  const currentTab = useStore(s => s.currentTab);
  const updateClusterDashboard = useStore(s => s.updateClusterDashboard);
  const clusterData = useStore(s => (currentTab && s.clusterDashboards[currentTab]) || EMPTY_DASHBOARD);
  const refreshRef = useRef<NodeJS.Timeout | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const loadDashboard = async () => {
    const cluster = currentTab;
    if (!cluster) return;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      await api.streamClusterDashboard(cluster, (type, data) => {
        switch (type) {
          case 'resourceCounts':
            updateClusterDashboard(cluster, { metrics: buildMetrics(data || {}) });
            break;
          case 'resourceCapacity':
            if (data) updateClusterDashboard(cluster, {
              resourceUsage: {
                cpu: { used: data.cpu.requested, total: data.cpu.allocatable, percentage: data.cpu.percentage },
                memory: { used: data.memory.requested, total: data.memory.allocatable, percentage: data.memory.percentage },
              },
            });
            break;
          case 'podStatus':
            updateClusterDashboard(cluster, { podStatus: data });
            break;
          case 'nodeStatus':
            if (data) updateClusterDashboard(cluster, { nodeStatus: { ready: data.ready, notReady: data.notReady, schedulable: data.schedulable } });
            break;
          case 'events':
            updateClusterDashboard(cluster, { events: (data || []).map((e: any) => ({ time: e.time, type: e.type, reason: e.reason, message: e.message, object: e.object, namespace: e.namespace })) });
            break;
          case 'criticalAlerts':
            updateClusterDashboard(cluster, { alerts: data || [] });
            break;
          case 'clusterInfo':
            updateClusterDashboard(cluster, { clusterInfo: data });
            break;
          case 'error':
            window.dispatchEvent(new CustomEvent('cluster:error', {
              detail: { cluster, errorCode: 'cluster_error', errorMessage: typeof data === 'string' ? data : 'Failed to load cluster dashboard', recoverable: true },
            }));
            break;
        }
      }, ctrl.signal);
    } catch (err: any) {
      if (err?.name !== 'AbortError') console.error('Failed to load dashboard:', err);
    }
  };

  useEffect(() => {
    if (!currentTab) return;
    loadDashboard();
    refreshRef.current = setInterval(loadDashboard, 300000);
    return () => {
      if (refreshRef.current) clearInterval(refreshRef.current);
      abortRef.current?.abort();
    };
  }, [currentTab]);

  const renderMetricCard = (metric: DashboardOverviewMetric) => (
    <div key={metric.title} className="metric-card" style={{ borderLeftColor: metric.color }}>
      <div className="metric-header">
        <span className="metric-icon">{ICONS[metric.iconKey]}</span>
        <span className="metric-title">{metric.title}</span>
      </div>
      <div className="metric-value">{metric.value.toLocaleString()}</div>
      {metric.trend !== undefined && metric.trend !== 0 && (
        <div className={`metric-trend ${metric.trend > 0 ? 'positive' : 'negative'}`}>
          {metric.trend > 0 ? <ArrowTopRightIcon /> : <ArrowBottomRightIcon />} {Math.abs(metric.trend)}
        </div>
      )}
    </div>
  );

  const renderResourceBar = (label: string, usage: { used: number; total: number; percentage: number }, unit: string, color: string) => (
    <div key={label} className="resource-bar">
      <div className="resource-label">
        <span>{label}</span>
        <span className="resource-values">{usage.used.toFixed(1)}{unit} / {usage.total.toFixed(1)}{unit}</span>
      </div>
      <div className="resource-progress">
        <div
          className="resource-fill"
          style={{ width: `${usage.percentage}%`, backgroundColor: color }}
        />
      </div>
      <div className="resource-percentage">{usage.percentage.toFixed(1)}%</div>
    </div>
  );

  const renderDonutChart = (data: Record<string, number>, colors: Record<string, string>) => {
    const total = Object.values(data).reduce((sum, val) => sum + val, 0);
    let currentAngle = 0;

    const segments = Object.entries(data).map(([key, value]) => {
      const percentage = (value / total) * 100;
      const angle = (value / total) * 360;
      const x1 = 50 + 40 * Math.cos((currentAngle - 90) * Math.PI / 180);
      const y1 = 50 + 40 * Math.sin((currentAngle - 90) * Math.PI / 180);
      const x2 = 50 + 40 * Math.cos((currentAngle + angle - 90) * Math.PI / 180);
      const y2 = 50 + 40 * Math.sin((currentAngle + angle - 90) * Math.PI / 180);

      const largeArc = angle > 180 ? 1 : 0;
      const path = `M 50 50 L ${x1} ${y1} A 40 40 0 ${largeArc} 1 ${x2} ${y2} Z`;

      currentAngle += angle;

      return { key, value, percentage, path, color: colors[key] };
    });

    return (
      <div className="donut-chart">
        <svg viewBox="0 0 100 100" className="donut-svg">
          {segments.map((segment) => (
            <path
              key={segment.key}
              d={segment.path}
              fill={segment.color}
              className="donut-segment"
            />
          ))}
        </svg>
        <div className="donut-legend">
          {segments.map((segment) => (
            <div key={segment.key} className="legend-item">
              <div className="legend-color" style={{ backgroundColor: segment.color }} />
              <span className="legend-label">{segment.key}</span>
              <span className="legend-value">{segment.value}</span>
            </div>
          ))}
        </div>
      </div>
    );
  };

  if (!currentTab) {
    return (
      <div className="cluster-overview">
        <div className="overview-header">
          <h3>Cluster Overview</h3>
          <span className="overview-subtitle">Select a cluster to view overview</span>
        </div>
      </div>
    );
  }

  return (
    <div className="cluster-overview">
      <div className="overview-header">
        <h3>Cluster Overview</h3>
        <span className="overview-subtitle">{currentTab}</span>
      </div>

      {/* Metric Cards */}
      <div className="metrics-grid">
        {clusterData.metrics.map(renderMetricCard)}
      </div>

      {/* Resource Usage */}
      {clusterData.resourceUsage && (
        <div className="overview-section">
          <h4>Resource Utilization</h4>
          <div className="resource-usage">
            {renderResourceBar('CPU', clusterData.resourceUsage.cpu, ' cores', '#3b82f6')}
            {renderResourceBar('Memory', clusterData.resourceUsage.memory, ' GiB', '#10b981')}
          </div>
        </div>
      )}

      {/* Pod Status */}
      {clusterData.podStatus && (
        <div className="overview-section">
          <h4>Pod Status Distribution</h4>
          {renderDonutChart(clusterData.podStatus as unknown as Record<string, number>, {
            running: '#10b981',
            pending: '#f59e0b',
            failed: '#ef4444',
            succeeded: '#8b5cf6'
          })}
        </div>
      )}

      {/* Recent Events */}
      <div className="overview-section">
        <h4>Recent Events</h4>
        <div className="events-list">
          {clusterData.events.map((event, idx) => (
            <div key={idx} className={`event-item ${event.type.toLowerCase()}`}>
              <div className="event-header">
                <span className="event-time">{event.time}</span>
                <span className={`event-type ${event.type.toLowerCase()}`}>{event.type}</span>
              </div>
              <div className="event-reason">{event.reason}</div>
              <div className="event-message">{event.message}</div>
              <div className="event-object">{event.object}</div>
            </div>
          ))}
        </div>
      </div>

      {/* Critical Alerts */}
      {clusterData.alerts.length > 0 && (
        <div className="overview-section">
          <h4>Critical Alerts</h4>
          <div className="alerts-list">
            {clusterData.alerts.slice(0, 8).map((alert, idx) => (
              <div key={idx} className={`alert-item ${alert.severity}`}>
                <div className="alert-header">
                  <ExclamationTriangleIcon className="alert-icon" />
                  <span className={`alert-severity ${alert.severity}`}>{alert.severity}</span>
                  <span className="alert-age">{alert.age}</span>
                </div>
                <div className="alert-resource">{alert.resource}</div>
                <div className="alert-message">{alert.message || alert.reason}</div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

export default ClusterOverview;