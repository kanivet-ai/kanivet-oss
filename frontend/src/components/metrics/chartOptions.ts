import { useMemo } from 'react';
import type { ChartOptions } from 'chart.js';
import { chartTickStyle, chartTooltipStyle, withAlpha, type ChartTheme } from './chartTheme';
import {
  formatAxisTick,
  formatReferenceValue,
  formatTimeLabel,
  formatTooltipValue,
  type MetricType,
} from './metricsFormat';

export interface LineChartOptionsInput {
  theme: ChartTheme;
  metric: MetricType;
  unit?: string;
  /** Dataset labels drawn as dashed reference lines (Limit, Request, Capacity…). */
  referenceLabels?: string[];
  /** Prefix tooltip rows with the dataset label (multi-series charts). */
  multiSeries?: boolean;
  onZoomComplete?: () => void;
}

const suggestedMaxWithHeadroom = (context: { chart: { data: { datasets: Array<{ data?: unknown[] }> } } }) => {
  let max = 0;
  for (const dataset of context.chart.data.datasets) {
    for (const v of dataset.data || []) {
      if (typeof v === 'number' && Number.isFinite(v) && v > max) max = v;
    }
  }
  return max > 0 ? max * 1.1 : undefined;
};

/** Chart.js options shared by every metrics card: hairline y grid, HH:MM x ticks, tokenised tooltip, drag-to-zoom. */
export const useLineChartOptions = ({
  theme,
  metric,
  unit,
  referenceLabels = [],
  multiSeries = false,
  onZoomComplete,
}: LineChartOptionsInput): ChartOptions<'line'> =>
  useMemo<ChartOptions<'line'>>(
    () => ({
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      transitions: { active: { animation: { duration: 0 } } },
      interaction: { mode: 'index', intersect: false },
      layout: { padding: { top: 6, right: 4, left: 0, bottom: 0 } },
      scales: {
        x: {
          grid: { display: false },
          border: { display: false },
          ticks: {
            ...chartTickStyle(theme),
            maxTicksLimit: 6,
            maxRotation: 0,
            autoSkipPadding: 18,
            callback(this: any, value: any): string {
              return formatTimeLabel(this.getLabelForValue(value));
            },
          },
        },
        y: {
          grid: { color: theme.hair },
          border: { display: false },
          beginAtZero: true,
          ticks: {
            ...chartTickStyle(theme),
            padding: 8,
            maxTicksLimit: 5,
            callback(value: any) {
              return formatAxisTick(metric, Number(value));
            },
          },
          // 10% headroom above the tallest series so the line never kisses the top.
          // Scale options are scriptable at runtime; the typings only know the number form.
          suggestedMax: suggestedMaxWithHeadroom as unknown as number,
        },
      },
      plugins: {
        legend: { display: false },
        title: { display: false },
        zoom: {
          zoom: {
            drag: {
              enabled: true,
              backgroundColor: withAlpha(theme.blue, 0.1),
              borderColor: withAlpha(theme.blue, 0.5),
              borderWidth: 1,
            },
            mode: 'x',
            onZoomComplete: () => onZoomComplete?.(),
          },
          pan: { enabled: true, mode: 'x' },
          limits: { x: { min: 'original', max: 'original' } },
        },
        tooltip: {
          enabled: true,
          mode: 'index',
          intersect: false,
          position: 'nearest',
          ...chartTooltipStyle(theme),
          displayColors: multiSeries,
          usePointStyle: true,
          boxWidth: 6,
          boxHeight: 6,
          boxPadding: 4,
          yAlign: 'bottom',
          xAlign: 'center',
          callbacks: {
            title: (items: any[]) => items[0]?.label || '',
            label: (item: any) => {
              const value = item.parsed?.y;
              if (value === null || value === undefined) return '';
              const label: string = item.dataset.label || '';
              if (referenceLabels.includes(label)) return `${label}: ${formatReferenceValue(metric, value)}`;
              const text = formatTooltipValue(metric, value, unit);
              return multiSeries ? `${label}: ${text}` : text;
            },
          },
        },
      },
    }),
    [theme, metric, unit, referenceLabels, multiSeries, onZoomComplete],
  );

/** Dashed reference line (limit/request/capacity) spanning the whole x axis. */
export const referenceDataset = (label: string, value: number, length: number, color: string, dash: number[]) => ({
  label,
  data: Array(Math.max(length, 1)).fill(value),
  borderColor: color,
  borderWidth: 1,
  borderDash: dash,
  fill: false,
  pointRadius: 0,
  pointHoverRadius: 0,
  tension: 0,
});
