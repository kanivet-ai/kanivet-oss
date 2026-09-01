import React, { useState, useEffect, useLayoutEffect, useRef, memo, useMemo, useCallback } from 'react';
import { useStore } from '../store';
import api from '../services/api';
import {
  ActivityLogIcon,
  BarChartIcon,
  CircleIcon,
  ClockIcon,
  ComponentInstanceIcon,
  Cross2Icon,
  DashboardIcon,
  ExclamationTriangleIcon,
  FileIcon,
  InfoCircledIcon,
  LayersIcon,
  LightningBoltIcon,
  MixIcon,
  ReloadIcon,
  RocketIcon,
  SizeIcon,
  StackIcon,
  UpdateIcon
} from '@radix-ui/react-icons';
import ArgoOverviewWidget from './ArgoOverviewWidget';
import './ClusterDashboard.css';

interface MetricCard {
  title: string;
  value: number;
  trend?: number;
  color: string;
  icon: React.ReactNode;
  subtitle?: string;
}

interface ResourceUsage {
  cpu: { used: number; total: number; percentage: number };
  memory: { used: number; total: number; percentage: number };
  storage: { used: number; total: number; percentage: number };
  network: { inbound: number; outbound: number };
}

interface PodStatus {
  running: number;
  pending: number;
  failed: number;
  succeeded: number;
}

interface NodeStatus {
  ready: number;
  notReady: number;
  schedulable: number;
  unschedulable: number;
}

interface WorkloadStatus {
  deployments: { healthy: number; total: number };
  statefulsets: { healthy: number; total: number };
  daemonsets: { healthy: number; total: number };
  jobs: { running: number; succeeded: number; failed: number };
}

interface ClusterEvent {
  time: string;
  type: 'Normal' | 'Warning';
  reason: string;
  message: string;
  object: string;
  namespace?: string;
}

interface CriticalAlert {
  severity: 'critical' | 'high' | 'medium' | 'low';
  type: string;
  resource: string;
  namespace: string;
  reason: string;
  message: string;
  age: string;
}

interface TopConsumer {
  name: string;
  namespace: string;
  cpu: number;
  memory: number;
  type: string;
  status: 'healthy' | 'warning' | 'error';
}

interface ClusterDashboardProps {
  cluster: string;
}

const clusterDashboardCache = new Map<string, any>();

const ClusterDashboard: React.FC<ClusterDashboardProps> = memo(({ cluster }) => {
  const [eventFilter, setEventFilter] = useState<'all' | 'warning' | 'normal'>('all');
  const updateTabStateRef = useRef<(updates: any) => void>();
  const hasReceivedDataRef = useRef(false);

  const { cachedDashboard, currentTab, updateCurrentTabState } = useStore(
    (state) => {
      const tabIndex = state.tabIndexMap.get(cluster) ?? -1;
      const tabState = tabIndex !== -1 ? state.activeTabs[tabIndex]?.state : null;
      return {
        cachedDashboard: tabState?.dashboardData,
        currentTab: state.currentTab,
        updateCurrentTabState: state.updateCurrentTabState,
      };
    },
    (a, b) => a.cachedDashboard === b.cachedDashboard && a.currentTab === b.currentTab
  );

  updateTabStateRef.current = updateCurrentTabState;
  const isActiveTab = cluster === currentTab;

  useEffect(() => {
    console.log('ClusterDashboard MOUNT', cluster);
  }, []);

  const getInitialState = useCallback(() => {
    const moduleCache = clusterDashboardCache.get(cluster);
    const source = moduleCache || cachedDashboard;
    return {
      resourceUsage: null as ResourceUsage | null,
      podStatus: (source?.podStatus || null) as PodStatus | null,
      nodeStatus: (source?.nodeStatus || null) as NodeStatus | null,
      workloadStatus: (source?.workloadStatus || null) as WorkloadStatus | null,
      events: (source?.events || []) as ClusterEvent[],
      topConsumers: [] as TopConsumer[],
      clusterInfo: source?.clusterInfo || { version: '', platform: '', provider: '', architecture: '', nodeCount: 0 },
      resourceCounts: (source?.resourceCounts || {}) as Record<string, number>,
      metricsAvailable: source?.metricsAvailable ?? true,
      criticalAlerts: (source?.criticalAlerts || []) as CriticalAlert[],
      resourceCapacity: source?.resourceCapacity || null as { cpu: { requested: number; allocatable: number; percentage: number; unit: string }; memory: { requested: number; allocatable: number; percentage: number; unit: string } } | null,
      isLoading: !source,
    };
  }, [cluster, cachedDashboard]);

  const [dashboardState, setDashboardState] = useState(getInitialState);

  const { resourceCounts, metricsAvailable, criticalAlerts, resourceCapacity, isLoading, podStatus, nodeStatus, workloadStatus, events, clusterInfo, resourceUsage, topConsumers } = dashboardState;

  useLayoutEffect(() => {
    const source = clusterDashboardCache.get(cluster) || cachedDashboard;
    if (source && isActiveTab) {
      const hasData = Object.keys(source.resourceCounts || {}).length > 0;
      if (hasData) {
        setDashboardState(prev => {
          const prevHasData = Object.keys(prev.resourceCounts || {}).length > 0;
          if (prevHasData && !hasReceivedDataRef.current) return prev;
          return {
            ...prev,
            podStatus: source.podStatus ?? prev.podStatus,
            nodeStatus: source.nodeStatus ?? prev.nodeStatus,
            workloadStatus: source.workloadStatus ?? prev.workloadStatus,
            events: source.events?.length ? source.events : prev.events,
            clusterInfo: source.clusterInfo ?? prev.clusterInfo,
            resourceCounts: Object.keys(source.resourceCounts || {}).length > 0 ? source.resourceCounts : prev.resourceCounts,
            metricsAvailable: source.metricsAvailable ?? prev.metricsAvailable,
            criticalAlerts: source.criticalAlerts ?? prev.criticalAlerts,
            resourceCapacity: source.resourceCapacity ?? prev.resourceCapacity,
            isLoading: false,
          };
        });
      }
    }
  }, [cluster, cachedDashboard, isActiveTab]);

  useEffect(() => {
    if (!cluster || !isActiveTab) return;

    let subscribed = false;

    const handleDashboardUpdate = (msg: any) => {
      if (msg.type !== 'dashboard' || !msg.data) return;
      if (msg.cluster && msg.cluster !== cluster) return;

      const d = msg.data;
      const hasResourceCounts = d.resourceCounts && Object.keys(d.resourceCounts).length > 0;

      setDashboardState(prev => {
        const prevHasData = Object.keys(prev.resourceCounts || {}).length > 0;
        const newState = {
          resourceCounts: hasResourceCounts ? d.resourceCounts : (prevHasData ? prev.resourceCounts : {}),
          metricsAvailable: d.metricsAvailable !== false,
          criticalAlerts: d.criticalAlerts || prev.criticalAlerts || [],
          resourceCapacity: d.resourceCapacity || prev.resourceCapacity,
          podStatus: d.podStatus || prev.podStatus,
          nodeStatus: d.nodeStatus || prev.nodeStatus,
          workloadStatus: d.workloadStatus || prev.workloadStatus,
          events: d.events?.length ? d.events : prev.events,
          clusterInfo: d.clusterInfo || prev.clusterInfo || { version: '', platform: '', provider: '', architecture: '', nodeCount: 0 },
          isLoading: false,
        };

        if (hasResourceCounts) {
          hasReceivedDataRef.current = true;
          clusterDashboardCache.set(cluster, newState);
          updateTabStateRef.current?.({ dashboardData: newState } as any);
        }

        return { ...prev, ...newState };
      });
    };

    const startDashboardStream = () => {
      if (subscribed) return;
      api.__sendWS({ type: 'dashboard', payload: { action: 'start', cluster } });
      subscribed = true;
    };

    const stopDashboardStream = () => {
      if (!subscribed) return;
      api.__sendWS({ type: 'dashboard', payload: { action: 'stop', cluster } });
      subscribed = false;
    };

    const unsubscribe = api.subscribeToDashboard(cluster, handleDashboardUpdate);
    startDashboardStream();

    return () => {
      stopDashboardStream();
      unsubscribe();
    };
  }, [cluster, isActiveTab]);


  const metrics = useMemo<MetricCard[]>(() => {
    if (!cluster) return [];
    return [
      {
        title: 'Nodes',
        value: resourceCounts[':nodes'] || resourceCounts['nodes'] || 0,
        trend: 0,
        color: '#10b981',
        icon: <LayersIcon />,
        subtitle: nodeStatus ? `${nodeStatus.ready} ready` : ''
      },
      {
        title: 'Namespaces',
        value: resourceCounts[':namespaces'] || resourceCounts['namespaces'] || 0,
        trend: 0,
        color: '#f59e0b',
        icon: <StackIcon />,
        subtitle: 'Active namespaces'
      },
      {
        title: 'Pods',
        value: resourceCounts[':pods'] || resourceCounts['pods'] || 0,
        trend: 0,
        color: '#3b82f6',
        icon: <MixIcon />,
        subtitle: podStatus ? `${podStatus.running} running, ${podStatus.pending} pending${podStatus.failed > 0 ? `, ${podStatus.failed} failed` : ''}` : ''
      },
      {
        title: 'Services',
        value: resourceCounts[':services'] || resourceCounts['services'] || 0,
        trend: 0,
        color: '#8b5cf6',
        icon: <LightningBoltIcon />,
        subtitle: 'Active services'
      },
      {
        title: 'Deployments',
        value: resourceCounts['apps:deployments'] || resourceCounts['deployments'] || 0,
        trend: 0,
        color: '#06b6d4',
        icon: <RocketIcon />,
        subtitle: workloadStatus?.deployments ? `${workloadStatus.deployments.healthy} healthy` : ''
      },
      {
        title: 'Storage Classes',
        value: resourceCounts['storage.k8s.io:storageclasses'] || resourceCounts[':storageclasses'] || 0,
        trend: 0,
        color: '#ef4444',
        icon: <FileIcon />,
        subtitle: 'Available storage'
      },
    ];
  }, [resourceCounts, cluster, nodeStatus, podStatus, workloadStatus]);


  const renderCompactMetricCard = (metric: MetricCard) => (
    <div key={metric.title} className="compact-metric-card">
      <div className="compact-metric-header">
        <div className="compact-metric-icon" style={{ color: metric.color }}>
          {metric.icon}
        </div>
        <div className="compact-metric-info">
          <div className="compact-metric-value">{metric.value.toLocaleString()}</div>
          <div className="compact-metric-title">{metric.title}</div>
        </div>
        {metric.trend !== undefined && metric.trend !== 0 && (
          <div className={`compact-metric-trend ${metric.trend > 0 ? 'positive' : 'negative'}`}>
            {metric.trend > 0 ? <UpdateIcon /> : <UpdateIcon style={{ transform: 'rotate(180deg)' }} />}
            <span>{Math.abs(metric.trend)}</span>
          </div>
        )}
      </div>
      {metric.subtitle && <div className="compact-metric-subtitle">{metric.subtitle}</div>}
    </div>
  );

  const renderCompactPieChart = (data: Record<string, number>, colors: Record<string, string>, title: string, icon: React.ReactNode) => {
    const total = Object.values(data).reduce((sum, val) => sum + val, 0);

    if (total === 0) {
      return (
        <div className="compact-pie-chart">
          <div className="compact-pie-header">
            <div className="compact-pie-icon">{icon}</div>
            <span className="compact-pie-title">{title}</span>
          </div>
          <div className="compact-pie-visual">
            <div className="compact-pie-center">
              <span className="compact-pie-total">0</span>
            </div>
          </div>
          <div className="compact-pie-legend">
            <div className="compact-legend-item">
              <span className="compact-legend-label">No data available</span>
            </div>
          </div>
        </div>
      );
    }

    let currentAngle = 0;

    const segments = Object.entries(data).map(([key, value]) => {
      const percentage = (value / total) * 100;
      const angle = (value / total) * 360;
      const x1 = 50 + 30 * Math.cos((currentAngle - 90) * Math.PI / 180);
      const y1 = 50 + 30 * Math.sin((currentAngle - 90) * Math.PI / 180);
      const x2 = 50 + 30 * Math.cos((currentAngle + angle - 90) * Math.PI / 180);
      const y2 = 50 + 30 * Math.sin((currentAngle + angle - 90) * Math.PI / 180);

      const largeArc = angle > 180 ? 1 : 0;
      const path = `M 50 50 L ${x1} ${y1} A 30 30 0 ${largeArc} 1 ${x2} ${y2} Z`;

      currentAngle += angle;

      return { key, value, percentage, path, color: colors[key] };
    });

    return (
      <div className="compact-pie-chart">
        <div className="compact-pie-header">
          <div className="compact-pie-icon">{icon}</div>
          <span className="compact-pie-title">{title}</span>
        </div>
        <div className="compact-pie-visual">
          <svg viewBox="0 0 100 100" className="compact-pie-svg">
            {segments.map((segment) => (
              <path
                key={segment.key}
                d={segment.path}
                style={{ fill: segment.color }}
                className="compact-pie-segment"
              />
            ))}
          </svg>
          <div className="compact-pie-center">
            <span className="compact-pie-total">{total}</span>
          </div>
        </div>
        <div className="compact-pie-legend">
          {segments.map((segment) => (
            <div key={segment.key} className="compact-legend-item">
              <CircleIcon style={{ color: segment.color, width: '8px', height: '8px' }} />
              <span className="compact-legend-label">{segment.key}</span>
              <span className="compact-legend-value">{segment.value}</span>
            </div>
          ))}
        </div>
      </div>
    );
  };

  if (!cluster) {
    return (
      <div className="cluster-dashboard">
        <div className="dashboard-header">
          <h2>Cluster Dashboard</h2>
          <p>Select a cluster to view dashboard</p>
        </div>
      </div>
    );
  }

  if (isLoading && !cachedDashboard) {
    return (
      <div className="cluster-dashboard">
        <div className="compact-dashboard-header">
          <div className="compact-header-left">
            <DashboardIcon className="compact-header-icon" />
            <div className="compact-header-info">
              <h2 className="compact-header-title">Cluster Overview</h2>
              <span className="compact-header-subtitle">
                {cluster} • Loading...
              </span>
            </div>
          </div>
        </div>
        <div className="compact-metrics-row">
          {[1, 2, 3, 4, 5, 6].map((i) => (
            <div key={i} className="compact-metric-card skeleton">
              <div className="skeleton-text" style={{ width: '60%', height: '16px' }} />
              <div className="skeleton-text" style={{ width: '40%', height: '24px', marginTop: '8px' }} />
            </div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="cluster-dashboard">
      {/* Compact Header */}
      <div className="compact-dashboard-header">
        <div className="compact-header-left">
          <DashboardIcon className="compact-header-icon" />
          <div className="compact-header-info">
            <h2 className="compact-header-title">Cluster Overview</h2>
            <span className="compact-header-subtitle">
              {cluster}
              {clusterInfo.provider && clusterInfo.provider !== 'Unknown' && ` • ${clusterInfo.provider}`}
              {clusterInfo.architecture && clusterInfo.architecture !== 'Unknown' && ` • ${clusterInfo.architecture}`}
              {clusterInfo.version && ` • v${clusterInfo.version}`}
            </span>
          </div>
        </div>
        <div className="compact-header-actions">
          {!metricsAvailable && (
            <div className="metrics-unavailable-badge" title="Metrics server not available">
              <ExclamationTriangleIcon />
              <span>No Metrics</span>
            </div>
          )}
          <button className="compact-action-btn">
            <ReloadIcon />
          </button>
          <button className="compact-action-btn">
            <BarChartIcon />
          </button>
        </div>
      </div>

      {/* Compact Metrics Row */}
      <div className="compact-metrics-row">
        {metrics.map(renderCompactMetricCard)}
      </div>

      <ArgoOverviewWidget cluster={cluster} />

      {/* Critical Alerts Section */}
      {criticalAlerts.length > 0 && (
        <div className="critical-alerts-section">
          <div className="critical-alerts-header">
            <ExclamationTriangleIcon />
            <span>Critical Alerts</span>
            <span className="alert-count">{criticalAlerts.length}</span>
          </div>
          <div className="critical-alerts-list">
            {criticalAlerts.map((alert, idx) => (
              <div key={idx} className={`critical-alert-item severity-${alert.severity}`}>
                <div className="alert-severity-indicator" />
                <div className="alert-content">
                  <div className="alert-header">
                    <div className="alert-type-badge">{alert.type}</div>
                    <div className="alert-resource">{alert.resource}</div>
                    <div className="alert-age">{alert.age}</div>
                  </div>
                  <div className="alert-reason">{alert.reason}</div>
                  {alert.message && <div className="alert-message">{alert.message}</div>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Main Compact Grid */}
      <div className="compact-dashboard-grid">
        {/* Resource Usage (Actual Consumption - requires metrics) */}
        {metricsAvailable && resourceUsage && resourceUsage.cpu.total > 0 && (
          <div className="compact-section compact-resource-usage">
          <div className="compact-section-header">
              <ActivityLogIcon />
              <span>Resource Usage (Actual)</span>
          </div>
          <div className="compact-resource-list">
              <div className="compact-resource-item">
                <div className="compact-resource-icon" style={{ color: '#3b82f6' }}>
                  <ComponentInstanceIcon />
                </div>
                <div className="compact-resource-content">
                  <div className="compact-resource-header">
                    <span className="compact-resource-label">CPU</span>
                    <span className="compact-resource-percentage">{resourceUsage.cpu.percentage.toFixed(1)}%</span>
                  </div>
                  <div className="compact-resource-bar">
                    <div
                      className="compact-resource-fill"
                      style={{ width: `${resourceUsage.cpu.percentage}%`, backgroundColor: '#3b82f6' }}
                    />
                  </div>
                  <div className="compact-resource-values">
                    {resourceUsage.cpu.used.toFixed(1)} cores / {resourceUsage.cpu.total.toFixed(1)} cores
                  </div>
                </div>
              </div>
              <div className="compact-resource-item">
                <div className="compact-resource-icon" style={{ color: '#10b981' }}>
                  <SizeIcon />
                </div>
                <div className="compact-resource-content">
                  <div className="compact-resource-header">
                    <span className="compact-resource-label">Memory</span>
                    <span className="compact-resource-percentage">{resourceUsage.memory.percentage.toFixed(1)}%</span>
                  </div>
                  <div className="compact-resource-bar">
                    <div
                      className="compact-resource-fill"
                      style={{ width: `${resourceUsage.memory.percentage}%`, backgroundColor: '#10b981' }}
                    />
                  </div>
                  <div className="compact-resource-values">
                    {resourceUsage.memory.used.toFixed(1)}GB / {resourceUsage.memory.total.toFixed(1)}GB
                  </div>
                </div>
              </div>
              <div className="compact-resource-item">
                <div className="compact-resource-icon" style={{ color: '#f59e0b' }}>
                  <FileIcon />
                </div>
                <div className="compact-resource-content">
                  <div className="compact-resource-header">
                    <span className="compact-resource-label">Storage</span>
                    <span className="compact-resource-percentage">{resourceUsage.storage.percentage.toFixed(1)}%</span>
                  </div>
                  <div className="compact-resource-bar">
                    <div
                      className="compact-resource-fill"
                      style={{ width: `${resourceUsage.storage.percentage}%`, backgroundColor: '#f59e0b' }}
                    />
                  </div>
                  <div className="compact-resource-values">
                    {resourceUsage.storage.used.toFixed(1)}TB / {resourceUsage.storage.total.toFixed(1)}TB
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* Resource Capacity Planning Section */}
        <div className="compact-section compact-capacity">
          <div className="compact-section-header">
            <BarChartIcon />
            <span>Scheduling Capacity</span>
            <div className="capacity-explainer" title="Based on pod resource requests, not actual usage">
              <InfoCircledIcon />
              <span>Reservations</span>
            </div>
          </div>
          {resourceCapacity ? (
            <>
              {/* Overall Status Banner */}
              <div className={`capacity-status-banner ${
                Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 85 ? 'critical' :
                Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 75 ? 'warning' :
                Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) < 40 ? 'underutilized' : 'healthy'
              }`}>
                <div className="capacity-status-icon">
                  {Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 85 ? 
                    <ExclamationTriangleIcon /> : 
                    Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 75 ?
                    <InfoCircledIcon /> :
                    Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) < 40 ?
                    <ActivityLogIcon /> :
                    <CircleIcon />
                  }
                </div>
                <div className="capacity-status-content">
                  <div className="capacity-status-title">
                    {Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 85 ? 
                      'Critical: Near Capacity Limit' : 
                      Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 75 ?
                      'Warning: Plan to Scale Soon' :
                      Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) < 40 ?
                      'Cluster Underutilized' :
                      'Optimal Capacity'
                    }
                  </div>
                  <div className="capacity-status-detail">
                    {Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 85 ? 
                      'Immediate action required. New pods will likely fail to schedule. Add nodes urgently.' : 
                      Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) > 75 ?
                      'Running hot. Begin capacity planning and consider autoscaling or adding nodes.' :
                      Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage) < 40 ?
                      `Only ${Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage).toFixed(0)}% utilized. Consider reducing node count to optimize costs, or increase pod resource requests to match actual usage.` :
                      `Healthy utilization at ${Math.max(resourceCapacity.cpu.percentage, resourceCapacity.memory.percentage).toFixed(0)}%. Good balance between headroom and efficiency.`
                    }
                  </div>
                </div>
              </div>

              {/* Detailed Capacity Metrics */}
              <div className="capacity-metrics">
                <div className="capacity-metric">
                  <div className="capacity-metric-header">
                    <ComponentInstanceIcon />
                    <span className="capacity-metric-label">CPU</span>
                    <div className={`capacity-percentage-badge ${
                      resourceCapacity.cpu.percentage > 85 ? 'critical' : 
                      resourceCapacity.cpu.percentage > 75 ? 'high' :
                      resourceCapacity.cpu.percentage < 40 ? 'underutilized' : 'optimal'
                    }`}>
                      {resourceCapacity.cpu.percentage.toFixed(1)}%
                    </div>
                  </div>
                  <div className="capacity-bar-large">
                    <div className="capacity-bar-section reserved" style={{ width: `${Math.min(resourceCapacity.cpu.percentage, 100)}%` }}>
                      {resourceCapacity.cpu.percentage > 15 && (
                        <span className="capacity-bar-label">{resourceCapacity.cpu.requested} cores</span>
                      )}
                    </div>
                    <div className="capacity-bar-section free" style={{ width: `${Math.max(100 - resourceCapacity.cpu.percentage, 0)}%` }}>
                      {(100 - resourceCapacity.cpu.percentage) > 15 && (
                        <span className="capacity-bar-label">{(resourceCapacity.cpu.allocatable - resourceCapacity.cpu.requested)} cores free</span>
                      )}
                    </div>
                  </div>
                  <div className="capacity-quick-stats">
                    <span className="capacity-quick-stat">
                      <span className="quick-stat-value">{resourceCapacity.cpu.requested}</span> reserved
                    </span>
                    <span className="capacity-quick-stat-divider">•</span>
                    <span className="capacity-quick-stat success">
                      <span className="quick-stat-value">{(resourceCapacity.cpu.allocatable - resourceCapacity.cpu.requested)}</span> available
                    </span>
                    <span className="capacity-quick-stat-divider">•</span>
                    <span className="capacity-quick-stat">
                      <span className="quick-stat-value">{resourceCapacity.cpu.allocatable}</span> total cores
                    </span>
          </div>
        </div>

                <div className="capacity-metric">
                  <div className="capacity-metric-header">
                    <SizeIcon />
                    <span className="capacity-metric-label">Memory</span>
                    <div className={`capacity-percentage-badge ${
                      resourceCapacity.memory.percentage > 85 ? 'critical' : 
                      resourceCapacity.memory.percentage > 75 ? 'high' :
                      resourceCapacity.memory.percentage < 40 ? 'underutilized' : 'optimal'
                    }`}>
                      {resourceCapacity.memory.percentage.toFixed(1)}%
                    </div>
                  </div>
                  <div className="capacity-bar-large">
                    <div className="capacity-bar-section reserved" style={{ width: `${Math.min(resourceCapacity.memory.percentage, 100)}%` }}>
                      {resourceCapacity.memory.percentage > 15 && (
                        <span className="capacity-bar-label">{resourceCapacity.memory.requested} GiB</span>
                      )}
                    </div>
                    <div className="capacity-bar-section free" style={{ width: `${Math.max(100 - resourceCapacity.memory.percentage, 0)}%` }}>
                      {(100 - resourceCapacity.memory.percentage) > 15 && (
                        <span className="capacity-bar-label">{(resourceCapacity.memory.allocatable - resourceCapacity.memory.requested)} GiB free</span>
                      )}
                    </div>
                  </div>
                  <div className="capacity-quick-stats">
                    <span className="capacity-quick-stat">
                      <span className="quick-stat-value">{resourceCapacity.memory.requested}</span> reserved
                    </span>
                    <span className="capacity-quick-stat-divider">•</span>
                    <span className="capacity-quick-stat success">
                      <span className="quick-stat-value">{(resourceCapacity.memory.allocatable - resourceCapacity.memory.requested)}</span> available
                    </span>
                    <span className="capacity-quick-stat-divider">•</span>
                    <span className="capacity-quick-stat">
                      <span className="quick-stat-value">{resourceCapacity.memory.allocatable}</span> total GiB
                    </span>
                  </div>
                </div>
              </div>
            </>
          ) : (
            <div className="compact-empty-state">
              <p>Loading capacity data...</p>
            </div>
          )}
        </div>

        {/* Two column grid for charts */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
        {/* Pod Status Compact */}
        {podStatus && (
          <div className="compact-section compact-pods">
            {renderCompactPieChart(
              podStatus as unknown as Record<string, number>,
              { running: 'var(--success)', pending: 'var(--warning)', failed: 'var(--danger)', succeeded: 'var(--accent-vcluster)', unknown: 'var(--text-tertiary)' },
              'Pod Status',
              <MixIcon />
            )}
          </div>
        )}

        {/* Workload Health Pie */}
        {workloadStatus && (
          <div className="compact-section compact-workloads">
            {renderCompactPieChart(
              {
                healthy: (workloadStatus.deployments?.healthy || 0) + (workloadStatus.statefulsets?.healthy || 0) + (workloadStatus.daemonsets?.healthy || 0),
                unhealthy: ((workloadStatus.deployments?.total || 0) - (workloadStatus.deployments?.healthy || 0)) +
                          ((workloadStatus.statefulsets?.total || 0) - (workloadStatus.statefulsets?.healthy || 0)) +
                          ((workloadStatus.daemonsets?.total || 0) - (workloadStatus.daemonsets?.healthy || 0)),
                jobs: (workloadStatus.jobs?.running || 0) + (workloadStatus.jobs?.succeeded || 0)
              },
              { healthy: 'var(--success)', unhealthy: 'var(--danger)', jobs: 'var(--accent-vcluster)' },
              'Workload Health',
              <RocketIcon />
            )}
          </div>
        )}
        </div>

        {/* Enhanced Events Section */}
        <div className="compact-section compact-events">
          <div className="compact-events-header">
            <div className="compact-section-header">
              <ActivityLogIcon />
              <span>Recent Events</span>
            </div>
            <div className="compact-events-filters">
              <button
                className={`compact-filter-btn ${eventFilter === 'all' ? 'active' : ''}`}
                onClick={() => setEventFilter('all')}
              >
                <InfoCircledIcon />
                All
              </button>
              <button
                className={`compact-filter-btn ${eventFilter === 'warning' ? 'active' : ''}`}
                onClick={() => setEventFilter('warning')}
              >
                <ExclamationTriangleIcon />
                Warnings
              </button>
              <button
                className={`compact-filter-btn ${eventFilter === 'normal' ? 'active' : ''}`}
                onClick={() => setEventFilter('normal')}
              >
                <InfoCircledIcon />
                Normal
              </button>
            </div>
          </div>
          <div className="compact-events-list">
            {events && events.length > 0 ? (
              events
                .filter(event => eventFilter === 'all' || event.type.toLowerCase() === eventFilter)
                .slice(0, 6)
                .map((event, idx) => (
                  <div key={idx} className={`compact-event-item ${event.type.toLowerCase()}`}>
                    <div className="compact-event-left">
                      <div className={`compact-event-icon ${event.type.toLowerCase()}`}>
                        {event.type === 'Warning' ? <ExclamationTriangleIcon /> : <InfoCircledIcon />}
                      </div>
                      <div className="compact-event-content">
                        <div className="compact-event-header">
                          <span className="compact-event-reason">{event.reason}</span>
                          <span className="compact-event-time">
                            <ClockIcon />
                            {event.time}
                          </span>
                        </div>
                        <div className="compact-event-message">{event.message}</div>
                        <div className="compact-event-object">
                          {event.object}
                          {event.namespace && <span className="compact-event-namespace">in {event.namespace}</span>}
                        </div>
                      </div>
                    </div>
                  </div>
                ))
            ) : (
              <div className="compact-event-item">
                <div className="compact-event-content">
                  <div className="compact-event-message">No recent events</div>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Top Consumers Compact - Only show if metrics available */}
        {metricsAvailable && topConsumers.length > 0 && (
        <div className="compact-section compact-consumers">
          <div className="compact-section-header">
            <BarChartIcon />
            <span>Top Resource Consumers</span>
          </div>
          <div className="compact-consumers-list">
            {topConsumers.slice(0, 4).map((consumer, idx) => (
              <div key={idx} className="compact-consumer-item">
                <div className="compact-consumer-header">
                  <div className="compact-consumer-info">
                    <span className="compact-consumer-name">{consumer.name}</span>
                    <span className="compact-consumer-meta">
                      {consumer.type} • {consumer.namespace}
                    </span>
                  </div>
                  <div className={`compact-consumer-status ${consumer.status}`}>
                    {consumer.status === 'healthy' ? <CircleIcon /> :
                     consumer.status === 'warning' ? <ExclamationTriangleIcon /> :
                     <Cross2Icon />}
                  </div>
                </div>
                <div className="compact-consumer-metrics">
                  <div className="compact-consumer-metric">
                    <ComponentInstanceIcon />
                    <span>{consumer.cpu.toFixed(1)}%</span>
                  </div>
                  <div className="compact-consumer-metric">
                    <SizeIcon />
                    <span>{consumer.memory.toFixed(1)}%</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
        )}
      </div>
    </div>
  );
});

export default ClusterDashboard;