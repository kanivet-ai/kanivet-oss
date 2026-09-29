interface NatsSparklineProps {
  values: number[];
  height?: number;
}

const LOGICAL_WIDTH = 100;

/**
 * Filled trend area for a rate series - no charting library needed for a
 * stat-tile sparkline. Scales to its container's width (via a fixed logical
 * viewBox + preserveAspectRatio="none") instead of a hardcoded pixel size, so
 * it never overflows a narrower stat card.
 *
 * Plots the values exactly as given, unsmoothed - its last point must always
 * match the headline number next to it, or the two visibly disagree.
 */
const NatsSparkline = ({ values, height = 26 }: NatsSparklineProps) => {
  if (values.length < 2) return null;
  const max = Math.max(...values, 1);
  const step = LOGICAL_WIDTH / (values.length - 1);
  const linePoints = values.map(
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
