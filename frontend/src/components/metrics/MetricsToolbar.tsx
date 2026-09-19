import React, { useEffect, useRef, useState } from 'react';
import {
  METRIC_LABELS,
  METRIC_ORDER,
  METRIC_TITLES,
  TIME_RANGES,
  customRangeMinutes,
  describeTimeRange,
  isPresetTimeRange,
  toDateTimeLocal,
  type MetricType,
  type TimeRange,
} from './metricsFormat';
import { ClockIcon, METRIC_ICONS } from './metricsIcons';

interface MetricsToolbarProps {
  metric: MetricType;
  onMetricChange: (metric: MetricType) => void;
  timeRange: TimeRange;
  onTimeRangeChange: (range: TimeRange) => void;
  /** Hide the custom-range clock (node metrics only offer presets). */
  allowCustomRange?: boolean;
}

/**
 * Two segmented controls: which metric, and how far back. The metric segments
 * carry icon + label; below the card's collapse width (see PodMetrics.css)
 * the labels hide and the icons stand alone with their titles as tooltips.
 */
export const MetricsToolbar: React.FC<MetricsToolbarProps> = ({
  metric,
  onMetricChange,
  timeRange,
  onTimeRangeChange,
  allowCustomRange = true,
}) => {
  const [customOpen, setCustomOpen] = useState(false);
  const [customStart, setCustomStart] = useState('');
  const [customEnd, setCustomEnd] = useState('');
  const popoverRef = useRef<HTMLDivElement>(null);
  const isCustom = !isPresetTimeRange(timeRange);

  useEffect(() => {
    if (!customOpen) return;
    const onPointerDown = (e: MouseEvent) => {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node)) setCustomOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setCustomOpen(false);
      }
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKey, true);
    };
  }, [customOpen]);

  const openCustom = () => {
    if (!customOpen) {
      const now = new Date();
      setCustomStart(toDateTimeLocal(new Date(now.getTime() - 60 * 60 * 1000)));
      setCustomEnd(toDateTimeLocal(now));
    }
    setCustomOpen((open) => !open);
  };

  const minutes = customRangeMinutes(customStart, customEnd);

  const applyCustom = () => {
    if (!minutes) return;
    onTimeRangeChange(`${minutes}m`);
    setCustomOpen(false);
  };

  return (
    <div className="mx-toolbar">
      <div className="mx-seg mx-seg-metrics" role="tablist" aria-label="Metric">
        {METRIC_ORDER.map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            className="mx-seg-btn"
            aria-selected={metric === key}
            title={METRIC_TITLES[key]}
            onClick={() => onMetricChange(key)}
          >
            <span className="mx-seg-icon">{METRIC_ICONS[key]}</span>
            <span className="mx-seg-label">{METRIC_LABELS[key]}</span>
          </button>
        ))}
      </div>

      <div className="mx-seg mx-seg-time" role="tablist" aria-label="Time range">
        {TIME_RANGES.map((range) => (
          <button
            key={range}
            type="button"
            role="tab"
            className="mx-seg-btn mx-seg-btn-range"
            aria-selected={timeRange === range}
            onClick={() => {
              onTimeRangeChange(range);
              setCustomOpen(false);
            }}
          >
            {range}
          </button>
        ))}
        {allowCustomRange && (
          <button
            type="button"
            role="tab"
            className="mx-seg-btn mx-seg-btn-range mx-seg-btn-custom"
            aria-selected={isCustom || customOpen}
            aria-haspopup="dialog"
            aria-expanded={customOpen}
            title="Custom range"
            onClick={openCustom}
          >
            <span className="mx-seg-icon">
              <ClockIcon />
            </span>
            {isCustom && <span className="mx-seg-custom-value">{describeTimeRange(timeRange)}</span>}
          </button>
        )}
        {customOpen && (
          <div className="mx-popover" ref={popoverRef} role="dialog" aria-label="Custom time range">
            <label className="mx-field">
              <span className="mx-field-label">From</span>
              <input
                type="datetime-local"
                className="mx-input"
                value={customStart}
                max={customEnd || undefined}
                onChange={(e) => setCustomStart(e.target.value)}
              />
            </label>
            <label className="mx-field">
              <span className="mx-field-label">To</span>
              <input
                type="datetime-local"
                className="mx-input"
                value={customEnd}
                min={customStart || undefined}
                onChange={(e) => setCustomEnd(e.target.value)}
              />
            </label>
            <div className="mx-popover-actions">
              {customStart && customEnd && (
                <span className={`ap-badge ${minutes ? 'ap-badge--info' : 'ap-badge--danger'}`}>
                  {minutes ? describeTimeRange(`${minutes}m`) : 'End before start'}
                </span>
              )}
              <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" onClick={applyCustom} disabled={!minutes}>
                Apply
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

export default MetricsToolbar;
