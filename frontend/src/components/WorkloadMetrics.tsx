import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import {
  Chart as ChartJS,
  ChartOptions,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler,
} from 'chart.js';
import zoomPlugin from 'chartjs-plugin-zoom';
import { Line } from 'react-chartjs-2';
import api from '../services/api';
import MonitoringSettingsModal from './MonitoringSettingsModal';
import './PodMetrics.css';
import './WorkloadMetrics.css';

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler,
  zoomPlugin,
);

interface WorkloadMetricsProps {
  cluster: string;
  kind: string;
  namespace: string;
  name: string;
}

interface PodInfo {
  name: string;
  status: string;
  node: string;
  containers: string[];
  ready: boolean;
  restarts: number;
  age: string;
  resourceLimits: { cpu: number; memory: number };
  resourceRequests: { cpu: number; memory: number };
}

interface PodMetricsData {
  labels: string[];
  values: number[];
  unit?: string;
}

type MetricType = 'cpu' | 'memory' | 'network_rx' | 'network_tx' | 'disk_read' | 'disk_write';
type TimeRange = '5m' | '15m' | '1h' | '6h' | '24h';

const METRIC_LABELS: Record<MetricType, string> = {
  cpu: 'CPU Usage',
  memory: 'Memory Usage',
  network_rx: 'Network Receive',
  network_tx: 'Network Transmit',
  disk_read: 'Disk Read',
  disk_write: 'Disk Write',
};

const METRIC_ICONS: Record<MetricType, JSX.Element> = {
  cpu: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <rect x="9" y="9" width="6" height="6" />
      <line x1="9" y1="1" x2="9" y2="4" />
      <line x1="15" y1="1" x2="15" y2="4" />
      <line x1="9" y1="20" x2="9" y2="23" />
      <line x1="15" y1="20" x2="15" y2="23" />
      <line x1="20" y1="9" x2="23" y2="9" />
      <line x1="20" y1="14" x2="23" y2="14" />
      <line x1="1" y1="9" x2="4" y2="9" />
      <line x1="1" y1="14" x2="4" y2="14" />
    </svg>
  ),
  memory: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M5 7h14l-2 8H7L5 7z" />
      <path d="M5 7V5a2 2 0 012-2h10a2 2 0 012 2v2" />
      <line x1="10" y1="11" x2="10" y2="11.01" />
      <line x1="14" y1="11" x2="14" y2="11.01" />
      <path d="M8 19v2m8-2v2m-4-2v2" />
    </svg>
  ),
  network_rx: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M12 2v14m0 0l-5-5m5 5l5-5" />
      <circle cx="12" cy="21" r="2" fill="currentColor" />
    </svg>
  ),
  network_tx: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M12 22V8m0 0l-5 5m5-5l5 5" />
      <circle cx="12" cy="3" r="2" fill="currentColor" />
    </svg>
  ),
  disk_read: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M12 8v8m0 0l-3-3m3 3l3-3" />
    </svg>
  ),
  disk_write: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M12 16V8m0 0l-3 3m3-3l3 3" />
    </svg>
  ),
};

// Color palette for multiple pods
const POD_COLORS = [
  '#3b82f6', // Blue
  '#10b981', // Emerald
  '#f59e0b', // Amber
  '#ef4444', // Red
  '#8b5cf6', // Violet
  '#ec4899', // Pink
  '#06b6d4', // Cyan
  '#84cc16', // Lime
  '#f97316', // Orange
  '#6366f1', // Indigo
];

// Maximum pods to show on the chart for performance and readability
const MAX_PODS_DISPLAYED = 8;

export const WorkloadMetrics: React.FC<WorkloadMetricsProps> = ({
  cluster,
  kind,
  namespace,
  name,
}) => {
  const [pods, setPods] = useState<PodInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedMetric, setSelectedMetric] = useState<MetricType>('cpu');
  const [selectedTimeRange, setSelectedTimeRange] = useState<TimeRange>('15m');
  const [podMetricsData, setPodMetricsData] = useState<Record<string, PodMetricsData>>({});
  const [showSettings, setShowSettings] = useState(false);
  const [isZoomed, setIsZoomed] = useState(false);
  const [providersStatus, setProvidersStatus] = useState<any>(() => api.getCachedMetricsProviderStatus(cluster));
  const [installingProvider, setInstallingProvider] = useState(false);
  const [showCustomTime, setShowCustomTime] = useState(false);
  const [customStartTime, setCustomStartTime] = useState('');
  const [customEndTime, setCustomEndTime] = useState('');
  const [visiblePods, setVisiblePods] = useState<Set<string>>(new Set());
  const chartRef = useRef<any>(null);
  const cleanupFnsRef = useRef<Map<string, () => void>>(new Map());
  const customTimeRef = useRef<HTMLDivElement>(null);

  const setMetricsProviderUnavailable = useCallback((reason?: string) => {
    cleanupFnsRef.current.forEach((cleanup) => cleanup());
    cleanupFnsRef.current.clear();
    setProvidersStatus(api.markMetricsProviderUnavailable(cluster, reason));
    setPodMetricsData({});
  }, [cluster]);

  // Click outside to close custom time dropdown
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (customTimeRef.current && !customTimeRef.current.contains(e.target as Node)) {
        setShowCustomTime(false);
      }
    };
    if (showCustomTime) document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [showCustomTime]);

  // Detect metrics provider
  const detectProvider = useCallback(async () => {
    try {
      const status = await api.detectMetricsProvider(cluster);
      setProvidersStatus(status);
    } catch (err) {
      console.error('Failed to detect metrics providers:', err);
      setMetricsProviderUnavailable('Unable to detect metrics provider');
    }
  }, [cluster, setMetricsProviderUnavailable]);

  useEffect(() => {
    detectProvider();
  }, [detectProvider]);

  const handleInstallPrometheus = async () => {
    setInstallingProvider(true);
    api.clearMetricsProviderAvailabilityCache(cluster);
    try {
      await api.installMetricsProvider(cluster, 'prometheus');
      await detectProvider();
    } catch (err: any) {
      setError(err.message || 'Failed to install Prometheus');
    } finally {
      setInstallingProvider(false);
    }
  };

  // Fetch pods
  const fetchPods = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await api.getWorkloadPods(cluster, kind, namespace, name);
      setPods(result.pods);
      // Initialize first MAX_PODS_DISPLAYED pods as visible
      const initialVisible = result.pods.slice(0, MAX_PODS_DISPLAYED).map((p) => p.name);
      setVisiblePods(new Set(initialVisible));
    } catch (err: any) {
      setError(err.message || 'Failed to fetch pods');
    } finally {
      setLoading(false);
    }
  }, [cluster, kind, namespace, name]);

  useEffect(() => {
    fetchPods();
  }, [fetchPods]);

  const hasMetricsProvider = providersStatus?.prometheus?.found || providersStatus?.mimir?.found;

  // Start metrics streams for visible pods only (limited to MAX_PODS_DISPLAYED)
  const startStreams = useCallback(() => {
    cleanupFnsRef.current.forEach((cleanup) => cleanup());
    cleanupFnsRef.current.clear();
    // Don't clear podMetricsData to avoid flicker - new data will replace old

    if (pods.length === 0 || !hasMetricsProvider) return;

    // Only stream for visible pods, limited to MAX_PODS_DISPLAYED
    const podsToStream = pods.filter((p) => visiblePods.has(p.name)).slice(0, MAX_PODS_DISPLAYED);

    podsToStream.forEach((pod) => {
      const cleanup = api.startMetricsStream(
        cluster,
        namespace,
        pod.name,
        selectedMetric,
        selectedTimeRange,
        (data) => {
          setPodMetricsData((prev) => ({
            ...prev,
            [pod.name]: data,
          }));
        },
        (err) => {
          console.error(`Metrics error for pod ${pod.name}:`, err);
          if (api.isMetricsProviderUnavailableError(err)) {
            setMetricsProviderUnavailable(err);
          }
        },
        undefined,
        'prometheus',
        2,
      );
      cleanupFnsRef.current.set(pod.name, cleanup);
    });
  }, [pods, cluster, namespace, selectedMetric, selectedTimeRange, hasMetricsProvider, visiblePods, setMetricsProviderUnavailable]);

  useEffect(() => {
    startStreams();
    return () => {
      cleanupFnsRef.current.forEach((cleanup) => cleanup());
      cleanupFnsRef.current.clear();
    };
  }, [startStreams]);

  const startStreamsRef = useRef(startStreams);
  startStreamsRef.current = startStreams;

  useEffect(() => {
    const onSettingsChanged = (event: Event) => {
      const changed = (event as CustomEvent).detail?.cluster;
      if (changed && changed !== cluster) return;
      api.clearMetricsProviderAvailabilityCache(cluster);
      detectProvider();
      startStreamsRef.current();
    };
    window.addEventListener('metrics-settings-changed', onSettingsChanged);
    return () => window.removeEventListener('metrics-settings-changed', onSettingsChanged);
  }, [cluster, detectProvider]);

  const handleResetZoom = useCallback(() => {
    const chart = chartRef.current;
    if (chart && typeof chart.resetZoom === 'function') {
      chart.resetZoom();
      setIsZoomed(false);
    }
  }, []);

  const togglePodVisibility = (podName: string) => {
    setVisiblePods((prev) => {
      const next = new Set(prev);
      if (next.has(podName)) {
        next.delete(podName);
      } else {
        // Only allow adding if under the limit
        if (next.size < MAX_PODS_DISPLAYED) {
          next.add(podName);
        }
      }
      return next;
    });
  };

  const getShortPodName = (fullName: string): string => {
    const parts = fullName.split('-');
    if (parts.length >= 2) {
      return parts.slice(-2).join('-');
    }
    return fullName.slice(-15);
  };

  // Calculate aggregated limits/requests
  const aggregatedResources = useMemo(() => {
    let totalLimitCpu = 0;
    let totalLimitMemory = 0;
    let totalRequestCpu = 0;
    let totalRequestMemory = 0;

    pods.forEach((pod) => {
      totalLimitCpu += pod.resourceLimits.cpu || 0;
      totalLimitMemory += pod.resourceLimits.memory || 0;
      totalRequestCpu += pod.resourceRequests.cpu || 0;
      totalRequestMemory += pod.resourceRequests.memory || 0;
    });

    return {
      limits: { cpu: totalLimitCpu, memory: totalLimitMemory },
      requests: { cpu: totalRequestCpu, memory: totalRequestMemory },
    };
  }, [pods]);

  // Build chart data with multiple datasets
  const chartData = useMemo(() => {
    let longestLabels: string[] = [];
    Object.values(podMetricsData).forEach((data) => {
      if (data.labels.length > longestLabels.length) {
        longestLabels = data.labels;
      }
    });

    const datasets: any[] = [];

    // Add pod datasets (only visible ones)
    pods.forEach((pod, index) => {
      if (!visiblePods.has(pod.name)) return;
      
      const data = podMetricsData[pod.name];
      const color = POD_COLORS[index % POD_COLORS.length];
      
      datasets.push({
        label: getShortPodName(pod.name),
        data: data?.values || [],
        borderColor: color,
        backgroundColor: `${color}15`,
        borderWidth: 1.5,
        fill: false,
        tension: 0.3,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointBackgroundColor: color,
        pointBorderColor: 'transparent',
        pointHoverBackgroundColor: color,
        pointHoverBorderColor: '#ffffff',
        pointHoverBorderWidth: 2,
      });
    });

    // Add limit line (aggregated)
    const limitValue = selectedMetric === 'cpu' ? aggregatedResources.limits.cpu : aggregatedResources.limits.memory;
    if (limitValue > 0) {
      datasets.push({
        label: 'Limit',
        data: Array(longestLabels.length || 1).fill(limitValue),
        borderColor: 'rgba(239, 68, 68, 0.6)',
        borderWidth: 1.5,
        borderDash: [5, 5],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    // Add request line (aggregated)
    const requestValue = selectedMetric === 'cpu' ? aggregatedResources.requests.cpu : aggregatedResources.requests.memory;
    if (requestValue > 0) {
      datasets.push({
        label: 'Request',
        data: Array(longestLabels.length || 1).fill(requestValue),
        borderColor: 'rgba(251, 191, 36, 0.6)',
        borderWidth: 1.5,
        borderDash: [3, 3],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    return {
      labels: longestLabels,
      datasets,
    };
  }, [pods, podMetricsData, visiblePods, selectedMetric, aggregatedResources]);

  const chartOptions = useMemo<ChartOptions<'line'>>(
    () => ({
      responsive: true,
      maintainAspectRatio: false,
      animation: false as const,
      transitions: {
        active: {
          animation: {
            duration: 0,
          },
        },
      },
      interaction: {
        mode: 'index' as const,
        intersect: false,
      },
      plugins: {
        zoom: {
          zoom: {
            drag: {
              enabled: true,
              backgroundColor: 'rgba(59, 130, 246, 0.1)',
              borderColor: 'rgba(59, 130, 246, 0.5)',
              borderWidth: 1,
            },
            mode: 'x' as const,
            onZoomComplete: () => setIsZoomed(true),
          },
          pan: {
            enabled: true,
            mode: 'x' as const,
          },
          limits: {
            x: { min: 'original' as const, max: 'original' as const },
          },
        },
        legend: {
          display: false, // We'll use custom legend
        },
        title: {
          display: false,
        },
        tooltip: {
          enabled: true,
          mode: 'index' as const,
          intersect: false,
          position: 'nearest' as const,
          backgroundColor: 'rgba(0, 0, 0, 0.8)',
          titleColor: 'rgba(255, 255, 255, 0.7)',
          bodyColor: '#ffffff',
          borderColor: 'rgba(255, 255, 255, 0.15)',
          borderWidth: 1,
          padding: 8,
          displayColors: true,
          usePointStyle: true,
          boxWidth: 6,
          boxHeight: 6,
          cornerRadius: 4,
          titleFont: {
            size: 10,
            weight: 'normal' as const,
            family: "'SF Mono', 'Monaco', monospace",
          },
          bodyFont: {
            size: 10,
            weight: 'normal' as const,
            family: "'SF Mono', 'Monaco', monospace",
          },
          callbacks: {
            title: (context: any) => context[0]?.label || '',
            label: (context: any) => {
              const value = context.parsed.y;
              if (value === null || value === undefined) return '';
              
              const label = context.dataset.label;
              if (selectedMetric === 'cpu') {
                if (value >= 1000) {
                  return `${label}: ${(value / 1000).toFixed(2)} cores`;
                }
                return `${label}: ${value.toFixed(0)}m`;
              } else if (selectedMetric === 'memory') {
                if (value >= 1073741824) {
                  return `${label}: ${(value / 1073741824).toFixed(2)} GiB`;
                } else if (value >= 1048576) {
                  return `${label}: ${(value / 1048576).toFixed(0)} MiB`;
                }
                return `${label}: ${(value / 1024).toFixed(0)} KiB`;
              }
              return `${label}: ${value.toFixed(2)}`;
            },
          },
        },
      },
      scales: {
        x: {
          grid: { display: false, drawBorder: false },
          ticks: {
            color: '#8b949e',
            font: {
              size: 10,
              family: "'Inter', 'SF Pro Text', -apple-system, sans-serif",
            },
            maxTicksLimit: 6,
            maxRotation: 0,
            callback: function (this: any, value: any): string {
              const label: string = this.getLabelForValue(value);
              if (!label) return '';
              try {
                const date = new Date(label);
                if (isNaN(date.getTime())) {
                  const match = label.match(/(\d{1,2}):(\d{2})/);
                  if (match) return `${match[1]}:${match[2]}`;
                  return label;
                }
                return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
              } catch {
                return label;
              }
            },
          },
        },
        y: {
          grid: {
            color: 'rgba(139, 148, 158, 0.08)',
            drawBorder: false,
          },
          border: { display: false },
          ticks: {
            color: '#8b949e',
            font: {
              size: 10,
              family: "'Inter', 'SF Pro Text', -apple-system, sans-serif",
            },
            padding: 8,
            maxTicksLimit: 5,
            callback: function (value: any) {
              if (selectedMetric === 'cpu') {
                if (value >= 1000) return (value / 1000).toFixed(1);
                return value.toFixed(0) + 'm';
              }
              if (value >= 1073741824) return (value / 1073741824).toFixed(1) + 'Gi';
              else if (value >= 1048576) return (value / 1048576).toFixed(1) + 'Mi';
              else if (value >= 1024) return (value / 1024).toFixed(1) + 'Ki';
              return value.toFixed(0);
            },
          },
          beginAtZero: true,
        },
      },
    }),
    [selectedMetric],
  );

  // Provider unavailable should win over pod/chart loading states.
  if (providersStatus !== null && !hasMetricsProvider) {
    const providerMessage = providersStatus?.unavailable
      ? 'Metrics provider unavailable for this cluster'
      : 'No metrics provider detected in cluster';
    return (
      <div className="pod-metrics-no-provider">
        <div className="no-provider-message">
          <span>{providerMessage}</span>
          <div className="no-provider-actions">
            <button
              className="install-provider-btn"
              onClick={handleInstallPrometheus}
              disabled={installingProvider}
            >
              {installingProvider
                ? 'Installing Prometheus...'
                : 'Install Prometheus'}
            </button>
            <button
              className="settings-btn-icon"
              onClick={() => setShowSettings(true)}
              title="Monitoring settings"
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>
          </div>
        </div>
        {showSettings && (
          <MonitoringSettingsModal cluster={cluster} onClose={() => setShowSettings(false)} />
        )}
      </div>
    );
  }

  // Loading state
  if (loading) {
    return (
      <div className="pod-metrics">
        <div className="metrics-chart-container">
          <div className="metrics-loading-state">
            <div className="metrics-loading-spinner" />
            <span className="metrics-loading-text">Loading pods...</span>
          </div>
        </div>
      </div>
    );
  }

  // Error state
  if (error) {
    return (
      <div className="pod-metrics">
        <div className="metrics-chart-container">
          <div className="metrics-error-state">
            <span>Error: {error}</span>
            <button className="retry-btn" onClick={fetchPods}>Retry</button>
          </div>
        </div>
      </div>
    );
  }

  // No pods
  if (pods.length === 0) {
    return (
      <div className="pod-metrics">
        <div className="metrics-chart-container">
          <div className="metrics-no-data">
            No pods found for this {kind.toLowerCase()}
          </div>
        </div>
      </div>
    );
  }

  // Still detecting
  if (providersStatus === null) {
    return (
      <div className="pod-metrics">
        <div className="metrics-chart-container">
          <div className="metrics-loading-state">
            <div className="metrics-loading-spinner" />
            <span className="metrics-loading-text">Detecting metrics provider...</span>
          </div>
        </div>
      </div>
    );
  }

  const hasData = Object.keys(podMetricsData).length > 0 &&
    Object.values(podMetricsData).some((d) => d.values.length > 0);

  return (
    <div className="pod-metrics">
      <div className="metrics-controls">
        <div className="metric-selector">
          {(Object.keys(METRIC_ICONS) as MetricType[]).map((key) => (
            <button
              key={key}
              className={`metric-btn metric-btn-icon ${selectedMetric === key ? 'active' : ''}`}
              onClick={() => setSelectedMetric(key)}
              title={METRIC_LABELS[key]}
            >
              {METRIC_ICONS[key]}
            </button>
          ))}
        </div>

        <div className="time-range-selector">
          {(['5m', '15m', '1h', '6h', '24h'] as TimeRange[]).map((range) => (
            <button
              key={range}
              className={`time-range-btn ${selectedTimeRange === range ? 'active' : ''}`}
              onClick={() => {
                setSelectedTimeRange(range);
                setShowCustomTime(false);
              }}
            >
              {range}
            </button>
          ))}
          <button
            className={`time-range-btn custom-range-icon ${showCustomTime ? 'active' : ''}`}
            onClick={() => {
              if (!showCustomTime) {
                const now = new Date();
                const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);
                const formatDateTime = (d: Date) => {
                  const pad = (n: number) => n.toString().padStart(2, '0');
                  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
                };
                setCustomStartTime(formatDateTime(oneHourAgo));
                setCustomEndTime(formatDateTime(now));
              }
              setShowCustomTime(!showCustomTime);
            }}
            title="Custom time range"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 6v6l4 2" />
            </svg>
          </button>
          {showCustomTime && (
            <div className="custom-time-dropdown" ref={customTimeRef}>
              <div className="time-input-row">
                <label>From</label>
                <input
                  type="datetime-local"
                  value={customStartTime}
                  onChange={(e) => setCustomStartTime(e.target.value)}
                  className="time-input"
                />
              </div>
              <div className="time-input-row">
                <label>To</label>
                <input
                  type="datetime-local"
                  value={customEndTime}
                  onChange={(e) => setCustomEndTime(e.target.value)}
                  className="time-input"
                />
              </div>
              <div className="time-input-actions">
                <button
                  className="apply-custom-time-btn"
                  onClick={() => {
                    startStreams();
                    setShowCustomTime(false);
                  }}
                  disabled={!customStartTime || !customEndTime}
                >
                  Apply
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="metrics-chart-container">
        <div className="metrics-chart-header">
          <div className="metrics-title-section">
            <span className="metrics-chart-title">{METRIC_LABELS[selectedMetric]}</span>
          </div>
          <div className="metrics-header-actions">
            {isZoomed && (
              <button className="reset-zoom-btn-icon" onClick={handleResetZoom} title="Reset zoom">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <circle cx="11" cy="11" r="8" />
                  <path d="M21 21l-4.35-4.35" />
                  <path d="M8 11h6" />
                </svg>
              </button>
            )}
            <button
              className="refresh-btn-icon"
              onClick={startStreams}
              title="Refresh metrics"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M23 4v6h-6M1 20v-6h6" />
                <path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15" />
              </svg>
            </button>
            <button
              className="settings-btn-icon"
              onClick={() => setShowSettings(true)}
              title="Monitoring settings"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>
          </div>
        </div>

        {!hasData ? (
          <div className="metrics-loading-state">
            <div className="metrics-loading-spinner" />
            <span className="metrics-loading-text">Loading metrics...</span>
          </div>
        ) : (
          <div className="metrics-chart">
            <Line ref={chartRef} data={chartData} options={chartOptions} />
          </div>
        )}
      </div>

      {/* Custom legend with checkboxes - outside chart container */}
      {hasData && (
        <div className="workload-legend">
          <span className="legend-count">
            {visiblePods.size} of {pods.length} pods
            {visiblePods.size >= MAX_PODS_DISPLAYED && pods.length > MAX_PODS_DISPLAYED && (
              <span className="legend-limit-hint"> (max {MAX_PODS_DISPLAYED})</span>
            )}
          </span>
          <div className="legend-items">
            {pods.slice(0, Math.max(MAX_PODS_DISPLAYED, pods.length > 20 ? 20 : pods.length)).map((pod, index) => {
              const color = POD_COLORS[index % POD_COLORS.length];
              const isVisible = visiblePods.has(pod.name);
              const isDisabled = !isVisible && visiblePods.size >= MAX_PODS_DISPLAYED;
              return (
                <label 
                  key={pod.name} 
                  className={`legend-item ${isVisible ? '' : 'hidden'} ${isDisabled ? 'disabled' : ''}`}
                  title={isDisabled ? `Max ${MAX_PODS_DISPLAYED} pods can be shown` : pod.name}
                >
                  <input
                    type="checkbox"
                    checked={isVisible}
                    onChange={() => togglePodVisibility(pod.name)}
                    disabled={isDisabled}
                  />
                  <span className="legend-color" style={{ backgroundColor: color }} />
                  <span className="legend-label">{getShortPodName(pod.name)}</span>
                </label>
              );
            })}
            {pods.length > 20 && (
              <span className="legend-more">+{pods.length - 20} more</span>
            )}
          </div>
        </div>
      )}

      {showSettings && (
        <MonitoringSettingsModal cluster={cluster} onClose={() => setShowSettings(false)} />
      )}
    </div>
  );
};

export default WorkloadMetrics;
