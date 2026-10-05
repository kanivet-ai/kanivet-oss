import { describe, expect, it } from 'vitest';
import { perPodReference, sumPods, totalRequest } from './workloadSeries';

const pod = (name: string, request: number, limit: number) => ({
  name,
  resourceRequests: { cpu: request, memory: request * 1024 },
  resourceLimits: { cpu: limit, memory: limit * 1024 },
});

describe('workload chart references', () => {
  // 20 replicas at 100m each: the request line used to sit at 2000m, the sum
  // over every pod, on a chart whose lines are single pods.
  it('draws one pod’s request and limit, not the workload’s sum', () => {
    const pods = Array.from({ length: 20 }, (_, i) =>
      pod(`web-${i}`, 100, 200),
    );
    expect(perPodReference(pods, 'cpu')).toEqual({ limit: 200, request: 100 });
    expect(perPodReference(pods, 'memory')).toEqual({
      limit: 204800,
      request: 102400,
    });
    expect(perPodReference(pods, 'network_rx')).toEqual({
      limit: 0,
      request: 0,
    });
  });

  it('draws the highest when pods differ', () => {
    expect(
      perPodReference([pod('a', 100, 0), pod('b', 250, 500)], 'cpu'),
    ).toEqual({
      limit: 500,
      request: 250,
    });
  });

  // The headline divided the visible pods' usage by every pod's requests:
  // 8 of 20 pods at 100% of their request read as 40%.
  it('reads the headline total against the requests of the pods it counts', () => {
    const pods = Array.from({ length: 20 }, (_, i) => pod(`web-${i}`, 100, 0));
    const visible = pods.slice(0, 8).map((p) => p.name);
    const series = Object.fromEntries(visible.map((name) => [name, [90, 100]]));
    const { values, counted } = sumPods(series, visible, 2);
    expect(values).toEqual([720, 800]);
    expect(totalRequest(pods, counted, 'cpu')).toBe(800);
  });

  it('leaves out pods with no samples and gaps at a point', () => {
    const { values, counted } = sumPods(
      { a: [1, NaN, 3], b: [NaN, NaN, NaN], c: [1, NaN, NaN] },
      ['a', 'b', 'c', 'd'],
      3,
    );
    expect(counted).toEqual(['a', 'c']);
    expect(values[0]).toBe(2);
    expect(values[1]).toBeNaN();
    expect(values[2]).toBe(3);
  });
});
