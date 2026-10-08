import React, { useMemo, useRef } from 'react';
import { Line } from 'react-chartjs-2';
import {
  Chart as ChartJS,
  LogarithmicScale,
  type ChartOptions,
  type Plugin,
} from 'chart.js';
import type {
  EvidenceEvent,
  Hourly,
  Distribution,
} from '../../types/rightsizing';
import {
  chartTickStyle,
  chartTooltipStyle,
  useChartTheme,
  withAlpha,
  type ChartTheme,
} from '../metrics/chartTheme';
import {
  formatCores,
  formatDayTime,
  formatMem,
  hoursAbove,
} from './rightsizingView';

ChartJS.register(LogarithmicScale);

/** A horizontal reference: the current request, the candidate, the limit. */
export interface RefLine {
  label: string;
  value: number;
  kind: 'current' | 'candidate' | 'limit';
}

const refStyle = (t: ChartTheme, kind: RefLine['kind']) => {
  switch (kind) {
    case 'candidate':
      return { color: t.text, dash: [] as number[], width: 2 };
    case 'limit':
      return { color: t.red, dash: [2, 3], width: 1.5 };
    default:
      return { color: t.text3, dash: [5, 4], width: 1.5 };
  }
};

const EVENT_COLOR = (t: ChartTheme, kind: EvidenceEvent['kind']) =>
  kind === 'oom'
    ? t.red
    : kind === 'restart'
      ? t.orange
      : kind === 'request-change'
        ? t.text2
        : t.purple;

type EventPoint = { x: number; kind: EvidenceEvent['kind'] };

/** Draws events as thin vertical rules with a marker at the top.
 * react-chartjs-2 registers plugins once, when the chart mounts, so the
 * events and colours are read from `live` at each draw: a plugin built from
 * props would keep the first container's markers through tab switches, fresh
 * evidence and theme flips. */
export const eventsPlugin = (live: {
  current: { points: EventPoint[]; theme: ChartTheme };
}): Plugin<'line'> => ({
  id: 'rsEvents',
  afterDatasetsDraw(chart) {
    const { points, theme } = live.current;
    const { ctx, chartArea, scales } = chart;
    const x = scales.x;
    ctx.save();
    for (const e of points) {
      const px = x.getPixelForValue(e.x);
      if (px < chartArea.left || px > chartArea.right) continue;
      const color = EVENT_COLOR(theme, e.kind);
      ctx.strokeStyle = withAlpha(color, 0.55);
      ctx.lineWidth = 1;
      ctx.setLineDash(
        e.kind === 'request-change' || e.kind.startsWith('shift') ? [3, 3] : [],
      );
      ctx.beginPath();
      ctx.moveTo(px, chartArea.top + 6);
      ctx.lineTo(px, chartArea.bottom);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = color;
      ctx.beginPath();
      if (e.kind === 'oom') {
        ctx.moveTo(px - 4, chartArea.top);
        ctx.lineTo(px + 4, chartArea.top);
        ctx.lineTo(px, chartArea.top + 6);
      } else {
        ctx.arc(px, chartArea.top + 3, 3, 0, Math.PI * 2);
      }
      ctx.fill();
    }
    ctx.restore();
  },
});

const hourLabels = (h: Hourly) => {
  const start = new Date(h.start).getTime();
  return h.cpuP50.map((_, i) => start + i * 3600_000);
};

const fmtTick = (ms: number) =>
  new Date(ms).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
  });
const fmtTip = (ms: number) =>
  new Date(ms).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });

function baseOptions(
  theme: ChartTheme,
  fmt: (v: number) => string,
  max: number,
): ChartOptions<'line'> {
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { mode: 'index', intersect: false },
    layout: { padding: { top: 10, right: 6 } },
    plugins: {
      legend: { display: false },
      tooltip: {
        ...chartTooltipStyle(theme),
        filter: (item) =>
          item.raw !== null && !(item.dataset as { isRef?: boolean }).isRef,
        callbacks: {
          title: (items) => (items[0] ? fmtTip(Number(items[0].label)) : ''),
          label: (item) => `${item.dataset.label}: ${fmt(Number(item.raw))}`,
        },
      },
    },
    scales: {
      x: {
        grid: { display: false },
        border: { display: false },
        ticks: {
          ...chartTickStyle(theme),
          maxRotation: 0,
          autoSkip: true,
          maxTicksLimit: 7,
          callback(this: any, value: any) {
            return fmtTick(Number(this.getLabelForValue(value)));
          },
        },
      },
      y: {
        beginAtZero: true,
        suggestedMax: max * 1.08,
        grid: { color: theme.hair },
        border: { display: false },
        ticks: {
          ...chartTickStyle(theme),
          padding: 8,
          maxTicksLimit: 5,
          callback: (v) => fmt(Number(v)),
        },
      },
    },
  };
}

const refDataset = (t: ChartTheme, r: RefLine, n: number) => {
  const s = refStyle(t, r.kind);
  return {
    label: r.label,
    data: new Array(n).fill(r.value),
    borderColor: s.color,
    borderDash: s.dash,
    borderWidth: s.width,
    pointRadius: 0,
    pointHoverRadius: 0,
    fill: false,
    isRef: true,
  };
};

export interface LegendItem {
  label: string;
  swatch:
    | 'band'
    | 'line'
    | 'thin'
    | 'current'
    | 'candidate'
    | 'limit'
    | 'short'
    | 'idle';
  value?: string;
}

export const ChartLegend: React.FC<{
  items: LegendItem[];
  resource: 'cpu' | 'memory';
}> = ({ items, resource }) => (
  <div className="rs-legend">
    {items.map((i) => (
      <span key={i.label} className="rs-legend-item">
        <span
          className={`rs-swatch rs-swatch-${i.swatch} rs-swatch-${resource}`}
          aria-hidden="true"
        />
        {i.label}
        {i.value && <span className="rs-legend-value">{i.value}</span>}
      </span>
    ))}
  </div>
);

const toEventPoints = (
  events: EvidenceEvent[],
  labels: number[],
  kinds: EvidenceEvent['kind'][],
): EventPoint[] => {
  if (labels.length === 0) return [];
  const start = labels[0];
  return events
    .filter((e) => kinds.includes(e.kind))
    .map((e) => ({
      x: Math.floor((new Date(e.at).getTime() - start) / 3600_000),
      kind: e.kind,
    }));
};

/** Charts re-render only when what they draw changes: a slider drag
 * rebuilds the reference lines on every move, but with the same values most
 * of the time, since requests snap to round steps. */
const sameRefs = (a: RefLine[], b: RefLine[]) =>
  a.length === b.length &&
  a.every(
    (r, i) =>
      r.value === b[i].value && r.kind === b[i].kind && r.label === b[i].label,
  );

const CPUChartImpl: React.FC<{
  hourly: Hourly;
  refs: RefLine[];
  events: EvidenceEvent[];
  multiReplica: boolean;
}> = ({ hourly, refs, events, multiReplica }) => {
  const theme = useChartTheme();
  const labels = useMemo(() => hourLabels(hourly), [hourly]);
  const color = theme.chartCpu || theme.blue;
  const max = Math.max(
    ...refs.map((r) => r.value),
    ...(hourly.cpuP95.filter((v) => v !== null) as number[]),
    0,
  );
  const points = useMemo(
    () =>
      toEventPoints(events, labels, [
        'restart',
        'shift-cpu',
        'request-change',
        'oom',
      ]),
    [events, labels],
  );
  const live = useRef({ points, theme });
  live.current = { points, theme };
  const plugins = useMemo(() => [eventsPlugin(live)], []);
  const data = {
    labels,
    datasets: [
      {
        label: 'P95 across replicas',
        data: hourly.cpuP95,
        borderColor: withAlpha(color, 0.0),
        backgroundColor: withAlpha(color, 0.18),
        fill: '+1',
        pointRadius: 0,
        borderWidth: 0,
        spanGaps: false,
      },
      {
        label: 'Median',
        data: hourly.cpuP50,
        borderColor: color,
        borderWidth: 1.5,
        pointRadius: 0,
        fill: false,
        spanGaps: false,
      },
      ...(multiReplica
        ? [
            {
              label: 'Busiest replica',
              data: hourly.cpuMax,
              borderColor: withAlpha(theme.text3, 0.7),
              borderWidth: 1,
              pointRadius: 0,
              fill: false,
              spanGaps: false,
            },
          ]
        : []),
      ...refs.map((r) => refDataset(theme, r, labels.length)),
    ],
  };
  return (
    <div className="rs-chart">
      <Line
        data={data as any}
        options={baseOptions(theme, formatCores, max)}
        plugins={plugins}
      />
    </div>
  );
};

export const CPUChart = React.memo(
  CPUChartImpl,
  (a, b) =>
    a.hourly === b.hourly &&
    a.events === b.events &&
    a.multiReplica === b.multiReplica &&
    sameRefs(a.refs, b.refs),
);

const MemoryChartImpl: React.FC<{
  hourly: Hourly;
  refs: RefLine[];
  events: EvidenceEvent[];
}> = ({ hourly, refs, events }) => {
  const theme = useChartTheme();
  const labels = useMemo(() => hourLabels(hourly), [hourly]);
  const color = theme.chartMemory || theme.purple;
  const max = Math.max(
    ...refs.map((r) => r.value),
    ...(hourly.memMax.filter((v) => v !== null) as number[]),
    0,
  );
  const points = useMemo(
    () =>
      toEventPoints(events, labels, [
        'oom',
        'restart',
        'shift-memory',
        'request-change',
      ]),
    [events, labels],
  );
  const live = useRef({ points, theme });
  live.current = { points, theme };
  const plugins = useMemo(() => [eventsPlugin(live)], []);
  const data = {
    labels,
    datasets: [
      {
        label: 'Peak, busiest replica',
        data: hourly.memMax,
        borderColor: color,
        backgroundColor: withAlpha(color, 0.14),
        borderWidth: 1.5,
        pointRadius: 0,
        fill: 'origin',
        spanGaps: false,
      },
      ...refs.map((r) => refDataset(theme, r, labels.length)),
    ],
  };
  return (
    <div className="rs-chart">
      <Line
        data={data as any}
        options={baseOptions(theme, formatMem, max)}
        plugins={plugins}
      />
    </div>
  );
};

/** A diagonal hatch: capacity that exists but holds nothing, drawn as an
 * outline rather than a solid that would outweigh the data. */
function hatch(color: string): CanvasPattern | string {
  if (typeof document === 'undefined') return color;
  const size = 6 * (window.devicePixelRatio || 1);
  const c = document.createElement('canvas');
  c.width = c.height = size;
  const ctx = c.getContext('2d');
  if (!ctx) return color;
  ctx.strokeStyle = color;
  ctx.lineWidth = window.devicePixelRatio || 1;
  ctx.beginPath();
  ctx.moveTo(0, size);
  ctx.lineTo(size, 0);
  ctx.moveTo(-size / 2, size / 2);
  ctx.lineTo(size / 2, -size / 2);
  ctx.moveTo(size / 2, size * 1.5);
  ctx.lineTo(size * 1.5, size / 2);
  ctx.stroke();
  return ctx.createPattern(c, 'repeat') ?? color;
}

/** The duration curve's y range. A current request far above everything
 * would flatten the curve into the floor, so it is left off the chart (the
 * legend says so). */
export function durationScale(
  dist: Distribution,
  current: number,
  candidate: number,
): { yTop: number; currentOnChart: boolean } {
  let peak = 0;
  dist.cpu.forEach((v) => {
    if (v !== null && v !== undefined && Number.isFinite(v))
      peak = Math.max(peak, v);
  });
  let yTop = Math.max(peak, candidate) * 1.15;
  const currentOnChart = current > 0 && current <= yTop * 1.6;
  if (currentOnChart) yTop = Math.max(yTop, current * 1.1);
  return { yTop, currentOnChart };
}

export const MemoryChart = React.memo(
  MemoryChartImpl,
  (a, b) =>
    a.hourly === b.hourly && a.events === b.events && sameRefs(a.refs, b.refs),
);

const DurationCurveImpl: React.FC<{
  dist: Distribution;
  current: number;
  candidate: number;
}> = ({ dist, current, candidate }) => {
  const theme = useChartTheme();
  const usage = theme.chartCpu || theme.blue;
  const pts = useMemo(() => {
    const out: { x: number; y: number }[] = [];
    dist.q.forEach((q, i) => {
      const v = dist.cpu[i];
      if (v !== null && v !== undefined && Number.isFinite(v))
        out.push({ x: (1 - q) * 24, y: Math.max(0, v) });
    });
    return out.sort((a, b) => a.x - b.x);
  }, [dist]);
  const { yTop, currentOnChart } = durationScale(dist, current, candidate);
  // react-chartjs-2 registers plugins once, when the chart mounts: the
  // callouts read the latest values from here, or they would stay where
  // the candidate was first drawn.
  const live = useRef({ candidate, current, currentOnChart, dist, theme });
  live.current = { candidate, current, currentOnChart, dist, theme };
  const labels = useMemo<Plugin<'line'>>(
    () => ({
      id: 'rsDurationLabels',
      afterDatasetsDraw(chart) {
        const { candidate, current, currentOnChart, dist, theme } =
          live.current;
        const candHours = hoursAbove(dist, candidate);
        const currHours = current > 0 ? hoursAbove(dist, current) : 0;
        const { ctx, chartArea, scales, tooltip } = chart;
        const font = (weight = 500) =>
          `${weight} 11px ${getComputedStyle(document.body).fontFamily}`;
        const text = (
          s: string,
          x: number,
          y: number,
          align: CanvasTextAlign,
          color = theme.text2,
          weight = 500,
        ) => {
          ctx.save();
          ctx.font = font(weight);
          ctx.fillStyle = color;
          ctx.textAlign = align;
          ctx.textBaseline = 'bottom';
          ctx.fillText(s, x, y);
          ctx.restore();
        };
        const crossing = (hours: number, value: number, note: string) => {
          if (hours < 1 / 60 || hours >= 24) return;
          const px = scales.x.getPixelForValue(hours);
          const py = scales.y.getPixelForValue(value);
          ctx.save();
          ctx.fillStyle = theme.text;
          ctx.strokeStyle = theme.card ?? '#000';
          ctx.lineWidth = 2;
          ctx.beginPath();
          ctx.arc(px, py, 4, 0, Math.PI * 2);
          ctx.fill();
          ctx.stroke();
          ctx.restore();
          text(note, px + 8, py - 4, 'left', theme.text, 600);
        };
        // The lines are named in the legend; the canvas only calls out where
        // each crosses the usage, which is the answer the chart exists for.
        const yc = scales.y.getPixelForValue(candidate);
        crossing(
          candHours,
          candidate,
          `above it ${formatDayTime(candHours)} a day`,
        );
        if (currentOnChart) {
          const ycur = scales.y.getPixelForValue(current);
          if (Math.abs(ycur - yc) > 16)
            crossing(
              currHours,
              current,
              `today: ${formatDayTime(currHours)} a day`,
            );
        }
        // Crosshair at the hovered hour.
        const active = tooltip?.getActiveElements?.() ?? [];
        if (active.length > 0) {
          const px = active[0].element.x;
          ctx.save();
          ctx.strokeStyle = withAlpha(theme.text3, 0.6);
          ctx.lineWidth = 1;
          ctx.beginPath();
          ctx.moveTo(px, chartArea.top);
          ctx.lineTo(px, chartArea.bottom);
          ctx.stroke();
          ctx.restore();
        }
      },
    }),
    [],
  );
  if (pts.length < 2) return null;
  const along = (y: number) => pts.map((p) => ({ x: p.x, y }));

  const options: ChartOptions<'line'> = {
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    parsing: false,
    interaction: { mode: 'nearest', axis: 'x', intersect: false },
    plugins: {
      legend: { display: false },
      tooltip: {
        ...chartTooltipStyle(theme),
        filter: (item) => item.datasetIndex === 1,
        callbacks: {
          title: () => '',
          label: (item) => {
            const { x, y } = item.raw as { x: number; y: number };
            return `Above ${formatCores(y)} for ${formatDayTime(x)} a day`;
          },
        },
      },
    },
    scales: {
      x: {
        type: 'linear',
        min: 0,
        max: 24,
        grid: { display: false },
        border: { display: false },
        title: {
          display: true,
          text: 'hours a day, busiest first',
          color: theme.text3,
          font: { size: 11 },
          padding: { top: 2 },
        },
        ticks: {
          ...chartTickStyle(theme),
          stepSize: 3,
          callback: (v) => `${v}h`,
        },
      },
      y: {
        min: 0,
        suggestedMax: yTop,
        grid: { color: theme.hair },
        border: { display: false },
        ticks: {
          ...chartTickStyle(theme),
          maxTicksLimit: 4,
          callback: (v) => formatCores(Number(v)),
        },
      },
    },
  };
  const short = theme.orange;
  const idle = hatch(withAlpha(theme.text3, 0.35));
  const data = {
    datasets: [
      {
        // Usage the request covers.
        label: 'Covered',
        data: pts.map((p) => ({ x: p.x, y: Math.min(p.y, candidate) })),
        borderWidth: 0,
        pointRadius: 0,
        backgroundColor: withAlpha(usage, 0.18),
        fill: 'origin',
      },
      {
        // Usage itself; the part above the request is where it borrows.
        label: 'Usage',
        data: pts,
        borderColor: usage,
        borderWidth: 2,
        pointRadius: 0,
        pointHoverRadius: 3,
        fill: {
          target: 0,
          above: withAlpha(short, 0.45),
          below: 'transparent',
        },
      },
      {
        // The request; the gap down to usage is paid for and idle.
        label: 'Candidate',
        data: along(candidate),
        borderColor: theme.text,
        borderWidth: 2,
        pointRadius: 0,
        pointHoverRadius: 0,
        fill: { target: 0, above: idle, below: 'transparent' },
      },
      ...(currentOnChart
        ? [
            {
              label: 'Current',
              data: along(current),
              borderColor: theme.text3,
              borderDash: [5, 4],
              borderWidth: 1.5,
              pointRadius: 0,
              pointHoverRadius: 0,
              fill: false,
            },
          ]
        : []),
    ],
  };
  return (
    <div className="rs-chart rs-chart-duration">
      <Line data={data as any} options={options} plugins={[labels]} />
    </div>
  );
};

export const DurationCurve = React.memo(DurationCurveImpl);
