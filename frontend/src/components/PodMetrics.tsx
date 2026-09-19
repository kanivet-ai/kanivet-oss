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
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
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

/* ---- Chart theme ---------------------------------------------------------
   Chart.js paints on a canvas, so it cannot use CSS variables directly. The
   colours below are read from the design tokens on <html> at runtime and
   re-read whenever the appearance flips (data-theme / inline theme vars).
   Shared by PodMetrics, NodeMetrics and WorkloadMetrics.
--------------------------------------------------------------------------- */

export interface ChartTheme {
  fontSans: string;
  fontMono: string;
  text: string;
  text2: string;
  text3: string;
  hair: string;
  sep: string;
  content: string;
  card: string;
  toolbar: string;
  blue: string;
  green: string;
  orange: string;
  red: string;
  purple: string;
  teal: string;
  yellow: string;
  pink: string;
  indigo: string;
  gray: string;
  chartCpu: string;
  chartMemory: string;
  chartStorage: string;
  chartNetwork: string;
  chartIdle: string;
}

const CHART_TOKEN_VARS: Record<keyof ChartTheme, string> = {
  fontSans: '--font-sans',
  fontMono: '--font-mono',
  text: '--text',
  text2: '--text2',
  text3: '--text3',
  hair: '--hair',
  sep: '--sep',
  content: '--content',
  card: '--card',
  toolbar: '--toolbar',
  blue: '--blue',
  green: '--green',
  orange: '--orange',
  red: '--red',
  purple: '--purple',
  teal: '--teal',
  yellow: '--yellow',
  pink: '--pink',
  indigo: '--indigo',
  gray: '--gray',
  chartCpu: '--chart-cpu',
  chartMemory: '--chart-memory',
  chartStorage: '--chart-storage',
  chartNetwork: '--chart-network',
  chartIdle: '--chart-idle',
};

const FALLBACK_FONT_SANS = '-apple-system, BlinkMacSystemFont, "SF Pro Text", system-ui, sans-serif';
const FALLBACK_FONT_MONO = 'ui-monospace, "SF Mono", Menlo, Monaco, monospace';

/** Read every chart token from <html>; var() references come back already resolved. */
export const readChartTheme = (): ChartTheme => {
  const style = getComputedStyle(document.documentElement);
  const theme = {} as ChartTheme;
  (Object.keys(CHART_TOKEN_VARS) as (keyof ChartTheme)[]).forEach((key) => {
    theme[key] = style.getPropertyValue(CHART_TOKEN_VARS[key]).trim();
  });
  theme.fontSans ||= FALLBACK_FONT_SANS;
  theme.fontMono ||= FALLBACK_FONT_MONO;
  return theme;
};

let colorCanvas: CanvasRenderingContext2D | null | undefined;

/** Parse #rgb / #rrggbb / #rrggbbaa / rgb() / rgba() (anything else via the canvas) into channels. */
export const parseColor = (input: string): { r: number; g: number; b: number; a: number } | null => {
  const value = (input || '').trim();
  if (!value) return null;
  const hex = value.match(/^#([0-9a-f]{3,8})$/i)?.[1];
  if (hex && hex.length !== 5 && hex.length !== 7) {
    const short = hex.length <= 4;
    const step = short ? 1 : 2;
    const channel = (i: number) => {
      const part = hex.slice(i * step, i * step + step);
      return parseInt(short ? part + part : part, 16);
    };
    const hasAlpha = hex.length === 4 || hex.length === 8;
    return { r: channel(0), g: channel(1), b: channel(2), a: hasAlpha ? channel(3) / 255 : 1 };
  }
  const rgb = value.match(/^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+%?))?\s*\)$/i);
  if (rgb) {
    const alpha = rgb[4] === undefined ? 1 : rgb[4].endsWith('%') ? parseFloat(rgb[4]) / 100 : parseFloat(rgb[4]);
    return { r: +rgb[1], g: +rgb[2], b: +rgb[3], a: alpha };
  }
  if (colorCanvas === undefined) colorCanvas = document.createElement('canvas').getContext('2d');
  if (!colorCanvas) return null;
  colorCanvas.fillStyle = '#010203';
  colorCanvas.fillStyle = value;
  const normalised = String(colorCanvas.fillStyle);
  if (normalised === '#010203' || normalised === value) return null;
  return parseColor(normalised);
};

/** `withAlpha('#0a84ff', 0.22)` → `rgba(10, 132, 255, 0.22)`; works for hex and rgb(a) tokens alike. */
export const withAlpha = (color: string, alpha: number): string => {
  const c = parseColor(color);
  if (!c) return color;
  const a = Math.round(Math.min(1, Math.max(0, alpha)) * 1000) / 1000;
  return `rgba(${Math.round(c.r)}, ${Math.round(c.g)}, ${Math.round(c.b)}, ${a})`;
};

let chartThemeCache: ChartTheme | null = null;
const chartThemeListeners = new Set<() => void>();
let chartThemeObserver: MutationObserver | null = null;

export const getChartTheme = (): ChartTheme => (chartThemeCache ||= readChartTheme());

const ensureChartThemeObserver = () => {
  if (chartThemeObserver || typeof MutationObserver === 'undefined') return;
  chartThemeObserver = new MutationObserver(() => {
    chartThemeCache = null;
    chartThemeListeners.forEach((listener) => listener());
  });
  chartThemeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme', 'style', 'class'],
  });
};

/** The current chart theme; re-renders the caller when the appearance changes. */
export const useChartTheme = (): ChartTheme => {
  const [theme, setTheme] = useState<ChartTheme>(getChartTheme);
  useEffect(() => {
    ensureChartThemeObserver();
    const listener = () => setTheme(getChartTheme());
    chartThemeListeners.add(listener);
    return () => {
      chartThemeListeners.delete(listener);
    };
  }, []);
  return theme;
};

/** Series colour for a metric: CPU blue, memory purple, network teal, disk orange. */
export const chartMetricColor = (theme: ChartTheme, metric: string): string => {
  switch (metric) {
    case 'cpu':
      return theme.chartCpu;
    case 'memory':
      return theme.chartMemory;
    case 'network_rx':
    case 'network_tx':
      return theme.chartNetwork;
    default:
      return theme.chartStorage;
  }
};

/** Soft vertical fill under a line: 0.22 alpha at the top of the plot area fading to 0 at the bottom. */
export const chartAreaGradient = (color: string) => {
  let cached: { key: string; gradient: CanvasGradient } | null = null;
  return (context: { chart: { ctx: CanvasRenderingContext2D; chartArea?: { top: number; bottom: number } } }) => {
    const { ctx, chartArea } = context.chart;
    if (!chartArea) return withAlpha(color, 0.12);
    const key = `${chartArea.top}:${chartArea.bottom}`;
    if (!cached || cached.key !== key) {
      const gradient = ctx.createLinearGradient(0, chartArea.top, 0, chartArea.bottom);
      gradient.addColorStop(0, withAlpha(color, 0.22));
      gradient.addColorStop(1, withAlpha(color, 0));
      cached = { key, gradient };
    }
    return cached.gradient;
  };
};

/** Tooltip colours/typography matching `.ap-tooltip` (vibrancy surface, secondary title, primary body). */
export const chartTooltipStyle = (theme: ChartTheme) => ({
  backgroundColor: theme.toolbar,
  titleColor: theme.text2,
  bodyColor: theme.text,
  borderColor: theme.sep,
  borderWidth: 1,
  cornerRadius: 6,
  padding: { top: 5, bottom: 5, left: 8, right: 8 },
  caretSize: 4,
  caretPadding: 6,
  titleFont: { size: 11, weight: 'normal' as const, family: theme.fontSans },
  bodyFont: { size: 11.5, weight: 'normal' as const, family: theme.fontSans },
  titleMarginBottom: 3,
  bodySpacing: 2,
});

/** Axis tick typography: 10.5px tertiary text in the system font. */
export const chartTickStyle = (theme: ChartTheme) => ({
  color: theme.text3,
  font: { size: 10.5, family: theme.fontSans },
});

// Custom crosshair plugin - subtle vertical line
const crosshairPlugin = {
  id: 'crosshair',
  afterDatasetsDraw: (chart: any) => {
    if (chart.tooltip?._active?.length) {
      const activePoint = chart.tooltip._active[0];
      const ctx = chart.ctx;
      const x = activePoint.element.x;
      const topY = chart.scales.y.top;
      const bottomY = chart.scales.y.bottom;
      const accent = getChartTheme().blue;

      ctx.save();
      // Draw thin vertical line
      ctx.beginPath();
      ctx.moveTo(x, topY);
      ctx.lineTo(x, bottomY);
      ctx.lineWidth = 1;
      ctx.strokeStyle = withAlpha(accent, 0.4);
      ctx.stroke();

      // Draw small dot at top
      ctx.beginPath();
      ctx.arc(x, topY + 3, 2, 0, Math.PI * 2);
      ctx.fillStyle = withAlpha(accent, 0.8);
      ctx.fill();
      ctx.restore();
    }
  },
};

ChartJS.register(crosshairPlugin);

interface PodMetricsProps {
  cluster: string;
  namespace: string;
  podName: string;
  containerName?: string;
  resourceLimits?: {
    cpu?: number; // in millicores
    memory?: number; // in bytes
  };
  resourceRequests?: {
    cpu?: number; // in millicores
    memory?: number; // in bytes
  };
}

type MetricType =
  | 'cpu'
  | 'memory'
  | 'network_rx'
  | 'network_tx'
  | 'disk_read'
  | 'disk_write';
type TimeRange = '5m' | '15m' | '1h' | '6h' | '24h' | string;

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

export const PodMetrics: React.FC<PodMetricsProps> = ({
  cluster,
  namespace,
  podName,
  containerName,
  resourceLimits,
  resourceRequests,
}) => {
  const { monitoringSettings } = useStore(useShallow((s) => ({ monitoringSettings: s.monitoringSettings })));
  const theme = useChartTheme();
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
  const [showCustomTime, setShowCustomTime] = useState(false);
  const [customStartTime, setCustomStartTime] = useState('');
  const [customEndTime, setCustomEndTime] = useState('');
  const cleanupRef = useRef<(() => void) | null>(null);
  const chartRef = useRef<any>(null);
  const customTimeRef = useRef<HTMLDivElement>(null);

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

  // Close custom time panel when clicking outside
  useEffect(() => {
    if (!showCustomTime) return;
    
    const handleClickOutside = (e: MouseEvent) => {
      if (customTimeRef.current && !customTimeRef.current.contains(e.target as Node)) {
        setShowCustomTime(false);
      }
    };
    
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [showCustomTime]);

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
      // Clean up existing stream
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }

      // Keep previous chart visible while next pod's data arrives —
      // setMetricsData(null) here causes a flash on every pod switch.
      setError(null);
      setLoading(true);

      console.log('[Metrics] Starting metrics stream:', { 
        metric: selectedMetric, 
        timeRange: selectedTimeRange,
        pod: podName,
        namespace,
        container: containerName 
      });

      // Start new WebSocket stream
      const cleanup = api.startMetricsStream(
        cluster,
        namespace,
        podName,
        selectedMetric,
        selectedTimeRange,
        (data) => {
          console.log('[Metrics] Received data:', {
            labelsCount: data?.labels?.length,
            valuesCount: data?.values?.length,
            firstLabel: data?.labels?.[0],
            lastLabel: data?.labels?.[data?.labels?.length - 1],
            allLabels: data?.labels,
          });
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
        containerName,
        undefined, // provider
        5, // 5-second streaming rate for better performance
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
    namespace,
    podName,
    containerName,
    setMetricsProviderUnavailable,
  ]);

  // Cleanup stream on component unmount
  useEffect(() => {
    return () => {
      if (cleanupRef.current) {
        cleanupRef.current();
      }
    };
  }, []); // Only run on mount/unmount

  const restartStream = () => {
    if (hasMetricsProvider) {
      // Clean up existing stream
      if (cleanupRef.current) {
        cleanupRef.current();
        cleanupRef.current = null;
      }

      // Clear data and restart stream
      setMetricsData(null);
      setError(null);
      setLoading(true);

      // Start new WebSocket stream
      const cleanup = api.startMetricsStream(
        cluster,
        namespace,
        podName,
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
        containerName,
        undefined, // provider
        5, // 5-second streaming rate for better performance
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

  // Build datasets with usage, limits, and requests
  const datasets = useMemo(() => {
    const color = chartMetricColor(theme, selectedMetric);
    const result: any[] = [
      {
        label: `${METRIC_LABELS[selectedMetric]} ${
          metricsData?.unit ? `(${metricsData.unit})` : ''
        }`,
        data: metricsData?.values || [],
        borderColor: color,
        backgroundColor: chartAreaGradient(color),
        borderWidth: 2,
        fill: true,
        tension: 0.4,
        pointRadius: 0,
        pointHoverRadius: 4,
        pointBackgroundColor: color,
        pointBorderColor: 'transparent',
        pointBorderWidth: 0,
        pointHoverBackgroundColor: color,
        pointHoverBorderColor: theme.content,
        pointHoverBorderWidth: 2,
      },
    ];

    // Add limit line if available
    if (
      (selectedMetric === 'cpu' && resourceLimits?.cpu) ||
      (selectedMetric === 'memory' && resourceLimits?.memory)
    ) {
      const limitValue =
        selectedMetric === 'cpu' ? resourceLimits.cpu : resourceLimits?.memory;
      result.push({
        label: 'Limit',
        data: Array(metricsData?.labels.length || 1).fill(limitValue),
        borderColor: theme.orange,
        borderWidth: 1,
        borderDash: [4, 4],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    // Add request line if available
    if (
      (selectedMetric === 'cpu' && resourceRequests?.cpu) ||
      (selectedMetric === 'memory' && resourceRequests?.memory)
    ) {
      const requestValue =
        selectedMetric === 'cpu'
          ? resourceRequests.cpu
          : resourceRequests?.memory;
      result.push({
        label: 'Request',
        data: Array(metricsData?.labels.length || 1).fill(requestValue),
        borderColor: theme.text3,
        borderWidth: 1,
        borderDash: [3, 3],
        fill: false,
        pointRadius: 0,
        pointHoverRadius: 0,
        tension: 0,
      });
    }

    return result;
  }, [theme, selectedMetric, metricsData, resourceLimits, resourceRequests]);

  const chartData = useMemo(
    () => ({
      labels: metricsData?.labels || [],
      datasets: datasets,
    }),
    [metricsData, datasets],
  );

  // Headline: latest sample, how it relates to the request, and the change over the window.
  const headline = useMemo(() => {
    const values = (metricsData?.values || []).filter(
      (v): v is number => typeof v === 'number' && Number.isFinite(v),
    );
    if (values.length === 0) return null;
    const last = values[values.length - 1];
    const first = values[0];
    const fmt = (v: number): { value: string; unit: string } => {
      if (selectedMetric === 'cpu') {
        return v >= 1000
          ? { value: (v / 1000).toFixed(2), unit: 'cores' }
          : { value: Math.round(v).toString(), unit: 'm' };
      }
      if (selectedMetric === 'memory') {
        if (v >= 1073741824) return { value: (v / 1073741824).toFixed(2), unit: 'GiB' };
        if (v >= 1048576) return { value: (v / 1048576).toFixed(0), unit: 'MiB' };
        return { value: (v / 1024).toFixed(0), unit: 'KiB' };
      }
      return { value: v.toFixed(2), unit: metricsData?.unit || '' };
    };
    const current = fmt(last);
    const request =
      selectedMetric === 'cpu'
        ? resourceRequests?.cpu
        : selectedMetric === 'memory'
          ? resourceRequests?.memory
          : undefined;
    const req = request ? fmt(request) : null;
    const join = (n: string, u: string) => (u === 'm' ? `${n}m` : `${n} ${u}`.trim());
    const context = req
      ? `${current.unit} of ${join(req.value, req.unit)} requested`
      : current.unit;
    const delta = first > 0 ? Math.round(((last - first) / first) * 100) : null;
    return { value: current.value, context, delta };
  }, [metricsData, selectedMetric, resourceRequests]);

  const handleResetZoom = () => {
    const chart = chartRef.current;
    if (chart) {
      console.log('[Metrics] Resetting zoom, chart instance:', chart);
      if (typeof chart.resetZoom === 'function') {
        chart.resetZoom();
        setIsZoomed(false);
      } else {
        console.warn('[Metrics] resetZoom not available on chart instance');
      }
    } else {
      console.warn('[Metrics] Chart ref not available');
    }
  };

  const chartOptions = useMemo(
    () => ({
      responsive: true,
      maintainAspectRatio: false,
      animation: {
        duration: 0, // Disable animations for better performance
      },
      interaction: {
        mode: 'index' as const,
        intersect: false,
      },
      scales: {
        x: {
          grid: {
            display: false,
            drawBorder: false,
          },
          border: {
            display: false,
          },
          ticks: {
            ...chartTickStyle(theme),
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
            color: theme.hair, // hairline gridlines
            drawBorder: false,
          },
          border: {
            display: false,
          },
          ticks: {
            ...chartTickStyle(theme),
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
            return max * 1.1; // Add 10% padding at the top
          },
        },
      },
      plugins: {
        zoom: {
          zoom: {
            drag: {
              enabled: true,
              backgroundColor: withAlpha(theme.blue, 0.1),
              borderColor: withAlpha(theme.blue, 0.5),
              borderWidth: 1,
            },
            mode: 'x' as const,
            onZoomComplete: () => {
              console.log('[Metrics] Zoom completed');
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
          // Styled like .ap-tooltip, colours from the design tokens
          ...chartTooltipStyle(theme),
          displayColors: false,
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

              if (label === 'Limit' || label === 'Request') {
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
    }),
    [theme, selectedMetric, metricsData],
  );

  if (!monitoringSettings.showMetricsPanel || monitoringSettings.preferredProvider === 'disabled') {
    return null;
  }

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
          <button
            className={`time-range-btn custom-range-icon ${showCustomTime ? 'active' : ''}`}
            onClick={() => {
              console.log('[Metrics] Custom range button clicked, showCustomTime:', showCustomTime);
              if (!showCustomTime) {
                // Set default values: last 1 hour (maps exactly to 1h option)
                const now = new Date();
                const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);
                // Format for datetime-local input (YYYY-MM-DDTHH:MM)
                const formatDateTime = (d: Date) => {
                  const pad = (n: number) => n.toString().padStart(2, '0');
                  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
                };
                const startFormatted = formatDateTime(oneHourAgo);
                const endFormatted = formatDateTime(now);
                console.log('[Metrics] Setting default times:', { start: startFormatted, end: endFormatted });
                setCustomStartTime(startFormatted);
                setCustomEndTime(endFormatted);
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
                  onChange={(e) => {
                    console.log('[Metrics] Start time changed:', e.target.value);
                    setCustomStartTime(e.target.value);
                  }}
                  className="time-input"
                />
              </div>
              <div className="time-input-row">
                <label>To</label>
                <input
                  type="datetime-local"
                  value={customEndTime}
                  onChange={(e) => {
                    console.log('[Metrics] End time changed:', e.target.value);
                    setCustomEndTime(e.target.value);
                  }}
                  className="time-input"
                />
              </div>
              <div className="time-input-actions">
                {(() => {
                  // Calculate preview range
                  if (customStartTime && customEndTime) {
                    const start = new Date(customStartTime);
                    const end = new Date(customEndTime);
                    const durationMins = Math.round((end.getTime() - start.getTime()) / 60000);
                    if (durationMins <= 0) return <span className="custom-time-preview custom-time-error">Invalid range</span>;
                    // Format nicely
                    let display = `${durationMins}m`;
                    if (durationMins >= 60) {
                      const hours = Math.floor(durationMins / 60);
                      const mins = durationMins % 60;
                      display = mins > 0 ? `${hours}h ${mins}m` : `${hours}h`;
                    }
                    return <span className="custom-time-preview">→ {display}</span>;
                  }
                  return null;
                })()}
                <button
                  className="apply-time-btn"
                  onClick={() => {
                    console.log('[Metrics] Apply clicked:', { customStartTime, customEndTime });
                    if (customStartTime && customEndTime) {
                      const start = new Date(customStartTime);
                      const end = new Date(customEndTime);
                      const durationMs = end.getTime() - start.getTime();
                      const durationMins = Math.round(durationMs / 60000);
                      
                      console.log('[Metrics] Duration calculation:', {
                        start: start.toISOString(),
                        end: end.toISOString(),
                        durationMs,
                        durationMins,
                      });
                      
                      if (durationMins <= 0) {
                        console.warn('[Metrics] Invalid duration:', durationMins);
                        return;
                      }
                      
                      // Send exact duration in minutes to backend
                      const customRange = `${durationMins}m`;
                      console.log('[Metrics] Setting custom time range to:', customRange);
                      setSelectedTimeRange(customRange);
                      setShowCustomTime(false);
                    } else {
                      console.warn('[Metrics] Apply failed - missing times:', { customStartTime, customEndTime });
                    }
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

        {headline && (
          <div className="metrics-headline">
            <span className="metrics-headline-value">{headline.value}</span>
            <span className="metrics-headline-context">{headline.context}</span>
            {headline.delta !== null && headline.delta !== 0 && (
              <span className={`metrics-headline-delta ${headline.delta < 0 ? 'down' : 'up'}`}>
                {headline.delta < 0 ? '↓' : '↑'}
                {Math.abs(headline.delta)}%
              </span>
            )}
          </div>
        )}

        <div className="chart-wrapper" data-stale={loading && !!metricsData ? 'true' : undefined}>
          <div className="chart-hint">
            <span>Drag to zoom</span>
          </div>
          <Line ref={chartRef} data={chartData} options={chartOptions} />

          {(error || (!metricsData && loading) || (!metricsData && !loading)) && (
            <div className="chart-overlay" data-state={error ? 'error' : loading ? 'loading' : 'empty'}>
              {error ? (
                <div className="chart-overlay-error">
                  {error.includes('EOF') || error.includes('connection') || error.includes('failed to query') || error.includes('mimir returned')
                    ? 'Metrics unavailable'
                    : error}
                </div>
              ) : loading ? (
                <>
                  <div className="metrics-loading-spinner" />
                  <span className="metrics-loading-text">Loading metrics…</span>
                </>
              ) : (
                <span className="metrics-loading-text">No data available</span>
              )}
            </div>
          )}
        </div>
      </div>

      {showSettings && (
        <MonitoringSettingsModal cluster={cluster} onClose={() => setShowSettings(false)} />
      )}
    </div>
  );
};
