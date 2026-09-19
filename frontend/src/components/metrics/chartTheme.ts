import { useEffect, useState } from 'react';
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

let registered = false;

/** Register the scales/elements/plugins every metrics chart needs. Idempotent. */
export const registerMetricsChart = (): void => {
  if (registered) return;
  registered = true;
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
    crosshairPlugin,
  );
};

registerMetricsChart();
