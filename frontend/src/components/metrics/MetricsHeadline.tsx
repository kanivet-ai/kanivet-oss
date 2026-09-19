import React from 'react';
import { formatMetricValue, joinValue, type MetricType } from './metricsFormat';

interface MetricsHeadlineProps {
  metric: MetricType;
  values: number[] | undefined;
  unit?: string;
  /** The request (pod) or allocatable (node) figure the current value is read against. */
  reference?: number;
  referenceLabel?: string;
  /** Extra trailing context, e.g. "across 8 pods". */
  suffix?: string;
}

/**
 * Latest sample in large type, how it relates to the reference figure, and
 * the change across the window. Renders nothing until there is a sample so
 * the frame below never shifts twice.
 */
export const MetricsHeadline: React.FC<MetricsHeadlineProps> = ({
  metric,
  values,
  unit,
  reference,
  referenceLabel = 'requested',
  suffix,
}) => {
  const series = (values || []).filter((v): v is number => typeof v === 'number' && Number.isFinite(v));
  if (series.length === 0) return <div className="mx-headline mx-headline-empty" aria-hidden="true" />;

  const last = series[series.length - 1];
  const first = series[0];
  const current = formatMetricValue(metric, last, unit);
  const ref = reference && reference > 0 ? formatMetricValue(metric, reference, unit) : null;
  const delta = first > 0 ? Math.round(((last - first) / first) * 100) : null;
  const pct = ref && reference ? Math.round((last / reference) * 100) : null;

  const context: string[] = [];
  if (ref) context.push(`of ${joinValue(ref)} ${referenceLabel}`);
  if (pct !== null) context.push(`${pct}%`);
  if (suffix) context.push(suffix);

  return (
    <div className="mx-headline">
      {/* "286m" reads as one token; "1.2 cores" / "512 MiB" keep the space */}
      <span className="mx-headline-value">{current.unit === 'm' ? `${current.value}m` : current.value}</span>
      {current.unit !== 'm' && <span className="mx-headline-unit">{current.unit}</span>}
      {context.length > 0 && <span className="mx-headline-context">{context.join(' · ')}</span>}
      {delta !== null && delta !== 0 && (
        <span
          className={`mx-headline-delta ${delta < 0 ? 'is-down' : 'is-up'}`}
          title="Change across the selected window"
        >
          {delta < 0 ? '↓' : '↑'} {Math.abs(delta)}%
        </span>
      )}
    </div>
  );
};

export default MetricsHeadline;
