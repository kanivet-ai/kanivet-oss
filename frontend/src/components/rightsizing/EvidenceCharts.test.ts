import { describe, expect, it, vi } from 'vitest';
import type { ChartTheme } from '../metrics/chartTheme';
// Hammer.js, under the zoom plugin, needs a window to load.
vi.mock('chartjs-plugin-zoom', () => ({ default: {} }));
import { eventsPlugin } from './EvidenceCharts';

/** A chart whose canvas records where markers go. */
function fakeChart() {
  const drawn: number[] = [];
  const fills: string[] = [];
  const ctx: Record<string, unknown> = {
    save() {},
    restore() {},
    setLineDash() {},
    beginPath() {},
    lineTo() {},
    stroke() {},
    arc() {},
    moveTo(x: number, y: number) {
      if (y === 6) drawn.push(x);
    },
    fill() {
      fills.push(ctx.fillStyle as string);
    },
  };
  const chart = {
    ctx,
    chartArea: { left: 0, right: 1000, top: 0, bottom: 100 },
    scales: { x: { getPixelForValue: (v: number) => v * 10 } },
  };
  return { chart, drawn, fills };
}

const theme = (red: string) =>
  ({
    red,
    orange: '#ff9500',
    purple: '#af52de',
    text2: '#666666',
  }) as ChartTheme;

describe('evidence chart event markers', () => {
  it('draw the events and colours current at each redraw, not those at mount', () => {
    const live = {
      current: {
        points: [{ x: 3, kind: 'oom' as const }],
        theme: theme('#ff0000'),
      },
    };
    const plugin = eventsPlugin(live) as any;
    const first = fakeChart();
    plugin.afterDatasetsDraw(first.chart);
    expect(first.drawn).toEqual([30]);
    expect(first.fills).toEqual(['#ff0000']);

    // Another container's events, after a theme flip, on the same chart.
    live.current = {
      points: [
        { x: 7, kind: 'oom' },
        { x: 9, kind: 'oom' },
      ],
      theme: theme('#cc0000'),
    };
    const next = fakeChart();
    plugin.afterDatasetsDraw(next.chart);
    expect(next.drawn).toEqual([70, 90]);
    expect(next.fills).toEqual(['#cc0000', '#cc0000']);
  });
});
