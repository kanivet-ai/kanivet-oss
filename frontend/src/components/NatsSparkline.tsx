interface NatsSparklineProps {
  values: number[];
  height?: number;
}

/** 3-point moving average - smooths poll-timing jitter out of a rate series. */
const smooth = (values: number[]): number[] =>
  values.map((_, i) => {
    const window = values.slice(Math.max(0, i - 2), i + 1);
    return window.reduce((sum, v) => sum + v, 0) / window.length;
  });

const LOGICAL_WIDTH = 100;

/**
 * Filled trend area for a rate series - no charting library needed for a
 * stat-tile sparkline. Scales to its container's width (via a fixed logical
 * viewBox + preserveAspectRatio="none") instead of a hardcoded pixel size, so
 * it never overflows a narrower stat card. Values are smoothed first so
 * counter-delta jitter between polls doesn't read as a sawtooth.
 */
const NatsSparkline = ({ values, height = 26 }: NatsSparklineProps) => {
  if (values.length < 2) return null;
  const smoothed = smooth(values);
  const max = Math.max(...smoothed, 1);
  const step = LOGICAL_WIDTH / (smoothed.length - 1);
  const linePoints = smoothed.map(
    (v, i) =>
      `${(i * step).toFixed(1)},${(height - (v / max) * height).toFixed(1)}`,
  );
  const areaPoints = [
    `0,${height}`,
    ...linePoints,
    `${LOGICAL_WIDTH},${height}`,
  ].join(' ');
  const gradientId = 'nats-sparkline-fill';

  return (
    <svg
      className="nats-sparkline"
      height={height}
      viewBox={`0 0 ${LOGICAL_WIDTH} ${height}`}
      preserveAspectRatio="none"
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="currentColor" stopOpacity="0.35" />
          <stop offset="100%" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <polygon points={areaPoints} fill={`url(#${gradientId})`} stroke="none" />
      <polyline
        points={linePoints.join(' ')}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
};

export default NatsSparkline;
