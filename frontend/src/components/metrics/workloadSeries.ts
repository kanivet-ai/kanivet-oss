/* Helpers for the workload chart, whose series are single pods. */
import type { MetricType } from './metricsFormat';

export interface PodResources {
  name: string;
  resourceLimits?: { cpu: number; memory: number };
  resourceRequests?: { cpu: number; memory: number };
}

const figure = (
  pod: PodResources,
  kind: 'resourceLimits' | 'resourceRequests',
  metric: MetricType,
): number => {
  if (metric === 'cpu') return pod[kind]?.cpu || 0;
  if (metric === 'memory') return pod[kind]?.memory || 0;
  return 0;
};

/**
 * Limit and request lines for a chart of single pods: one pod's figure, not
 * the workload's sum, which sat far above every pod line and squashed them
 * against the axis. Pods of one template share it; when they differ the
 * highest is drawn.
 */
export function perPodReference(
  pods: PodResources[],
  metric: MetricType,
): { limit: number; request: number } {
  let limit = 0;
  let request = 0;
  for (const pod of pods) {
    limit = Math.max(limit, figure(pod, 'resourceLimits', metric));
    request = Math.max(request, figure(pod, 'resourceRequests', metric));
  }
  return { limit, request };
}

/**
 * The given pods' total at each point of the axis (NaN where none has a
 * sample), and which pods have any sample at all.
 */
export function sumPods(
  series: Record<string, number[]>,
  podNames: string[],
  length: number,
): { values: number[]; counted: string[] } {
  const counted = podNames.filter((name) =>
    series[name]?.some((v) => Number.isFinite(v)),
  );
  const values = Array.from({ length }, (_, i) => {
    let total = 0;
    let any = false;
    for (const name of counted) {
      const v = series[name][i];
      if (Number.isFinite(v)) {
        total += v;
        any = true;
      }
    }
    return any ? total : NaN;
  });
  return { values, counted };
}

/**
 * Requests summed over the pods a headline total counts, so "% of requested"
 * compares the same pods on both sides.
 */
export function totalRequest(
  pods: PodResources[],
  podNames: string[],
  metric: MetricType,
): number {
  const names = new Set(podNames);
  return pods
    .filter((pod) => names.has(pod.name))
    .reduce((sum, pod) => sum + figure(pod, 'resourceRequests', metric), 0);
}
