import React, {
  useState,
  useEffect,
  useCallback,
  useRef,
  useMemo,
} from 'react';
import {
  Chart as ChartJS,
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

interface NodeMetricsProps {
  cluster: string;
  nodeName: string;
  resourceCapacity?: {
    cpu?: number;
    memory?: number;
  };
  resourceAllocatable?: {
    cpu?: number;
    memory?: number;
  };
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
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
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
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <path d="M5 7h14l-2 8H7L5 7z" />
      <path d="M5 7V5a2 2 0 012-2h10a2 2 0 012 2v2" />
      <line x1="10" y1="11" x2="10" y2="11.01" />
      <line x1="14" y1="11" x2="14" y2="11.01" />
      <path d="M8 19v2m8-2v2m-4-2v2" />
    </svg>
  ),
  network_rx: (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <path d="M12 2v14m0 0l-5-5m5 5l5-5" />
      <circle cx="12" cy="21" r="2" fill="currentColor" />
    </svg>
  ),
  network_tx: (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <path d="M12 22V8m0 0l-5 5m5-5l5 5" />
      <circle cx="12" cy="3" r="2" fill="currentColor" />
    </svg>
  ),
  disk_read: (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M12 8v8m0 0l-3-3m3 3l3-3" />
      <circle cx="12" cy="12" r="1" fill="currentColor" />
    </svg>
  ),
  disk_write: (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <path d="M12 16V8m0 0l-3 3m3-3l3 3" />
      <circle cx="12" cy="12" r="1" fill="currentColor" />
    </svg>
  ),
};

const CHART_COLORS = {
  cpu: '#3b82f6',
  memory: '#10b981',
  network_rx: '#f59e0b',
  network_tx: '#ef4444',
  disk_read: '#8b5cf6',
  disk_write: '#ec4899',
};

export const NodeMetrics: React.FC<NodeMetricsProps> = ({
  cluster,
  nodeName,
  resourceCapacity,
  resourceAllocatable,
}) => {
  const [selectedMetric, setSelectedMetric] = useState<MetricType>('cpu');
  const [selectedTimeRange, setSelectedTimeRange] = useState<TimeRange>('15m');
  const [metricsData, setMetricsData] = useState<{
    labels: string[];
    values: number[];
    unit?: string;
  } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [providersStatus, setProvidersStatus] = useState<any>(() => api.getCachedMetricsProviderStatus(cluster));
  const [installingProvider, setInstallingProvider] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [isZoomed, setIsZoomed] = useState(false);
  const cleanupRef = useRef<(() => void) | null>(null);
  const chartRef = useRef<any>(null);

  const setMetricsProviderUnavailable = useCallback((reason?: string) => {
    if (cleanupRef.current) {
      cleanupRef.current();
      cleanupRef.current = null;
    }
    setProvidersStatus(api.markMetricsProviderUnavailable(cluster, reason));
    setMetricsData(null);
    setLoading(false);
    setError(null);
  }, [cluster]);

  const checkProvidersStatus = useCallback(async () => {
    try {
      const status = await api.detectMetricsProvider(cluster);
      setProvidersStatus(status);
    } catch (err) {
      console.error('Failed to detect metrics providers:', err);
      setMetricsProviderUnavailable('Unable to detect metrics provider');
    }
  }, [cluster, setMetricsProviderUnavailable]);

  useEffect(() => {
    checkProvidersStatus();
  }, [checkProvidersStatus]);

  const hasMetricsProvider = providersStatus?.prometheus?.found || providersStatus?.mimir?.found;

  useEffect(() => {
    if (hasMetricsProvider) {
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }

      setMetricsData(null);
      setError(null);
      setLoading(true);

      const cleanup = api.startMetricsStream(
        cluster,
        '', // No namespace for node metrics
        '', // No pod for node metrics
        selectedMetric,
        selectedTimeRange,
        (data) => {
          setMetricsData(data);
          setLoading(false);
          setError(null);
        },
        (errorMsg) => {
          if (api.isMetricsProviderUnavailableError(errorMsg)) {
            setMetricsProviderUnavailable(errorMsg);
            return;
          }
          setError(errorMsg);
          setLoading(false);
        },
        undefined, // containerName
        undefined, // provider
        5, // streamingRate
        nodeName, // nodeName - this is the key parameter for node metrics
      );

      cleanupRef.current = cleanup;
    } else {
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }
      setLoading(false);
    }
  }, [
    selectedMetric,
    selectedTimeRange,
    hasMetricsProvider,
    cluster,
    nodeName,
    setMetricsProviderUnavailable,
  ]);

  useEffect(() => {
    return () => {
      if (cleanupRef.current) {
        cleanupRef.current();
      }
    };
  }, []);

  const restartStream = () => {
    if (hasMetricsProvider) {
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }

      setMetricsData(null);
      setError(null);
      setLoading(true);

      const cleanup = api.startMetricsStream(
        cluster,
        '', // No namespace for node metrics
        '', // No pod for node metrics
        selectedMetric,
        selectedTimeRange,
        (data) => {
          setMetricsData(data);
          setLoading(false);
          setError(null);
        },
        (errorMsg) => {
          if (api.isMetricsProviderUnavailableError(errorMsg)) {
            setMetricsProviderUnavailable(errorMsg);
            return;
          }
          setError(errorMsg);
          setLoading(false);
        },
        undefined, // containerName
        undefined, // provider
        5, // streamingRate
        nodeName, // nodeName - this is the key parameter for node metrics
      );

      cleanupRef.current = cleanup;
    }
  };

  const restartStreamRef = useRef(restartStream);
  restartStreamRef.current = restartStream;

  useEffect(() => {
    const onSettingsChanged = (event: Event) => {
      const changed = (event as CustomEvent).detail?.cluster;
      if (changed && changed !== cluster) return;
      api.clearMetricsProviderAvailabilityCache(cluster);
      checkProvidersStatus();
      restartStreamRef.current();
    };
    window.addEventListener('metrics-settings-changed', onSettingsChanged);
    return () => window.removeEventListener('metrics-settings-changed', onSettingsChanged);
  }, [cluster, checkProvidersStatus]);

  const handleInstallPrometheus = async () => {
    setInstallingProvider(true);
    api.clearMetricsProviderAvailabilityCache(cluster);
    try {
      await api.installMetricsProvider(cluster, 'prometheus');
      await checkProvidersStatus();
    } catch (err: any) {
      setError(err.message || 'Failed to install Prometheus');
    } finally {
      setInstallingProvider(false);
    }
  };

  const handleRefresh = () => {
    restartStream();
  };

  const handleResetZoom = () => {
    const chart = chartRef.current;
    if (chart) {
      chart.resetZoom();
      setIsZoomed(false);
    }
  };

  const datasets = useMemo(() => {
    const result: any[] = [
      {
        label: `${METRIC_LABELS[selectedMetric]} ${
          metricsData?.unit ? `(${metricsData.unit})` : ''
        }`,
        data: metricsData?.values || [],
        borderColor: CHART_COLORS[selectedMetric],
        backgroundColor: 'transparent',
        borderWidth: 2,
        fill: false,
        tension: 0.3,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointBackgroundColor: CHART_COLORS[selectedMetric],
        pointBorderColor: '#1a1a1a',
        pointBorderWidth: 2,
        pointHoverBackgroundColor: CHART_COLORS[selectedMetric],
        pointHoverBorderColor: '#ffffff',
        pointHoverBorderWidth: 2,
      },
    ];

    if (
      (selectedMetric === 'cpu' && resourceCapacity?.cpu) ||
      (selectedMetric === 'memory' && resourceCapacity?.memory)
    ) {
      const capacityValue =
        selectedMetric === 'cpu' ? resourceCapacity.cpu : resourceCapacity?.memory;
      result.push({
        label: 'Capacity',
        data: Array(metricsData?.labels.length || 1).fill(capacityValue),
        borderColor: 'rgba(239, 68, 68, 0.6)',
        borderWidth: 1.5,
        borderDash: [5, 5],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    if (
      (selectedMetric === 'cpu' && resourceAllocatable?.cpu) ||
      (selectedMetric === 'memory' && resourceAllocatable?.memory)
    ) {
      const allocatableValue =
        selectedMetric === 'cpu'
          ? resourceAllocatable.cpu
          : resourceAllocatable?.memory;
      result.push({
        label: 'Allocatable',
        data: Array(metricsData?.labels.length || 1).fill(allocatableValue),
        borderColor: 'rgba(251, 191, 36, 0.6)',
        borderWidth: 1.5,
        borderDash: [3, 3],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    return result;
  }, [selectedMetric, metricsData, resourceCapacity, resourceAllocatable]);

  const chartData = useMemo(
    () => ({
      labels: metricsData?.labels || [],
      datasets: datasets,
    }),
    [metricsData, datasets],
  );

  const chartOptions = useMemo(
    () => ({
      responsive: true,
      maintainAspectRatio: false,
      animation: {
        duration: 0,
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
            onZoomComplete: () => {
              setIsZoomed(true);
            },
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
          display: false,
        },
        title: {
          display: false,
        },
        tooltip: {
          enabled: true,
          mode: 'index' as const,
          intersect: false,
          position: 'nearest' as const,
          // Compact styling - matching PodMetrics
          backgroundColor: 'rgba(0, 0, 0, 0.8)',
          titleColor: 'rgba(255, 255, 255, 0.7)',
          bodyColor: '#ffffff',
          borderColor: 'rgba(255, 255, 255, 0.15)',
          borderWidth: 1,
          padding: 6,
          displayColors: false,
          cornerRadius: 4,
          caretSize: 4,
          caretPadding: 4,
          // Smaller fonts for compact tooltip
          titleFont: {
            size: 9,
            weight: 'normal' as const,
            family: "'SF Mono', 'Monaco', monospace",
          },
          bodyFont: {
            size: 10,
            weight: 'normal' as const,
            family: "'SF Mono', 'Monaco', monospace",
          },
          titleMarginBottom: 2,
          bodySpacing: 2,
          // Position at top of chart
          yAlign: 'bottom' as const,
          xAlign: 'center' as const,
          callbacks: {
            title: function (context: any) {
              const label = context[0]?.label;
              return label || '';
            },
            label: function (context: any) {
              const label = context.dataset.label;
              let value = context.parsed.y;

              // Skip null/undefined values
              if (value === null || value === undefined) return '';

              if (label === 'Capacity' || label === 'Allocatable') {
                if (selectedMetric === 'cpu') {
                  if (value >= 1000) {
                    return `${label}: ${(value / 1000).toFixed(1)}c`;
                  }
                  return `${label}: ${value.toFixed(0)}m`;
                } else if (selectedMetric === 'memory') {
                  if (value >= 1073741824) {
                    return `${label}: ${(value / 1073741824).toFixed(1)}Gi`;
                  } else if (value >= 1048576) {
                    return `${label}: ${(value / 1048576).toFixed(0)}Mi`;
                  }
                  return `${label}: ${(value / 1024).toFixed(0)}Ki`;
                }
              }

              if (selectedMetric === 'cpu') {
                if (value >= 1000) {
                  return `${(value / 1000).toFixed(2)} cores`;
                }
                return `${value.toFixed(0)} millicores`;
              } else if (selectedMetric === 'memory') {
                if (value >= 1073741824) {
                  return `${(value / 1073741824).toFixed(2)} GiB`;
                } else if (value >= 1048576) {
                  return `${(value / 1048576).toFixed(0)} MiB`;
                }
                return `${(value / 1024).toFixed(0)} KiB`;
              }
              if (metricsData?.unit) {
                return `${value.toFixed(2)} ${metricsData.unit}`;
              }
              return value.toFixed(2);
            },
          },
        },
      },
      scales: {
        x: {
          grid: {
            display: false,
            drawBorder: false,
          },
          ticks: {
            color: '#8b949e', // Visible gray for both themes
            font: {
              size: 10,
              family: "'Inter', 'SF Pro Text', -apple-system, sans-serif",
            },
            maxTicksLimit: 6,
            maxRotation: 0,
            callback: function (this: any, value: any): string {
              // Get the label at this index
              const label: string = this.getLabelForValue(value);
              if (!label) return '';
              
              // Parse the timestamp and format as HH:MM
              try {
                // Handle different timestamp formats
                const date = new Date(label);
                if (isNaN(date.getTime())) {
                  // If it's already in a time format like "10:30:45", extract HH:MM
                  const match: RegExpMatchArray | null = label.match(/(\d{1,2}):(\d{2})/);
                  if (match) {
                    return `${match[1]}:${match[2]}`;
                  }
                  return label;
                }
                // Format as HH:MM
                return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
              } catch {
                return label;
              }
            },
          },
        },
        y: {
          grid: {
            color: 'rgba(139, 148, 158, 0.08)', // Very subtle grid lines
            drawBorder: false,
          },
          border: {
            display: false,
          },
          ticks: {
            color: '#8b949e', // Visible gray for both themes
            font: {
              size: 10,
              family: "'Inter', 'SF Pro Text', -apple-system, sans-serif",
            },
            padding: 8,
            maxTicksLimit: 5,
            callback: function (value: any) {
              // Special formatting for CPU (millicores)
              if (selectedMetric === 'cpu') {
                if (value >= 1000) {
                  return (value / 1000).toFixed(1); // Convert millicores to cores
                }
                return value.toFixed(0) + 'm'; // Show millicores
              }
              // Memory and other metrics
              if (value >= 1073741824) {
                // >= 1 GiB
                return (value / 1073741824).toFixed(1) + 'Gi';
              } else if (value >= 1048576) {
                // >= 1 MiB
                return (value / 1048576).toFixed(1) + 'Mi';
              } else if (value >= 1024) {
                // >= 1 KiB
                return (value / 1024).toFixed(1) + 'Ki';
              }
              return value.toFixed(0);
            },
          },
          beginAtZero: true,
          suggestedMax: function (context: any) {
            const max = Math.max(
              ...(context.chart.data.datasets[0].data || [0]),
            );
            return max * 1.1;
          },
        },
      },
    }),
    [selectedMetric, metricsData],
  );

  // Still detecting providers - show loading state
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

  if (!hasMetricsProvider) {
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

  return (
    <div className="pod-metrics">
      <div className="metrics-controls">
        <div className="metric-selector">
          {(Object.keys(METRIC_ICONS) as MetricType[]).map((key) => (
            <button
              key={key}
              className={`metric-btn metric-btn-icon ${
                selectedMetric === key ? 'active' : ''
              }`}
              onClick={() => setSelectedMetric(key)}
              title={METRIC_LABELS[key]}
            >
              <span className="metric-icon">{METRIC_ICONS[key]}</span>
            </button>
          ))}
        </div>

        <div className="time-range-selector">
          {(['5m', '15m', '1h', '6h', '24h'] as TimeRange[]).map((range) => (
            <button
              key={range}
              className={`time-range-btn ${
                selectedTimeRange === range ? 'active' : ''
              }`}
              onClick={() => setSelectedTimeRange(range)}
            >
              {range}
            </button>
          ))}
        </div>
      </div>

      <div className="metrics-chart-container">
        <div className="metrics-chart-header">
          <div className="metrics-title-section">
            <span className="metrics-chart-title">
              {METRIC_LABELS[selectedMetric]}
            </span>
          </div>
          <div className="metrics-header-actions">
            {isZoomed && (
              <button
                className="reset-zoom-btn-icon"
                onClick={handleResetZoom}
                title="Reset zoom"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M3.98 8.223A10.477 10.477 0 0 1 12 2c5.523 0 10 4.477 10 10s-4.477 10-10 10a10.477 10.477 0 0 1-10-10" />
                  <path d="M4 4v4h4" />
                </svg>
              </button>
            )}
            <button
              className="refresh-btn-icon"
              onClick={handleRefresh}
              disabled={loading}
              title="Refresh metrics"
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <path d="M23 4v6h-6M1 20v-6h6" />
                <path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15" />
              </svg>
            </button>
          </div>
        </div>

        {error && (
          <div className="metrics-error">
            {error.includes('EOF') || error.includes('connection') || error.includes('failed to query')
              ? 'Metrics unavailable - Prometheus connection failed'
              : error}
          </div>
        )}

        {!error && !loading && metricsData && (
          <div className="chart-wrapper">
            <div className="chart-hint">
              <span>Drag to zoom</span>
            </div>
            <Line ref={chartRef} data={chartData} options={chartOptions} />
          </div>
        )}

        {!error && loading && (
          <div className="metrics-loading-state">
            <div className="metrics-loading-spinner" />
            <span className="metrics-loading-text">Loading metrics...</span>
          </div>
        )}

        {!error && !loading && !metricsData && (
          <div className="metrics-no-data">No data available</div>
        )}
      </div>
    </div>
  );
};
