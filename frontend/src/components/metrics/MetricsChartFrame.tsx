import React from 'react';
import { GearIcon, RefreshIcon, ResetZoomIcon } from './metricsIcons';

export type ChartFrameState = 'ready' | 'loading' | 'empty' | 'error';

interface MetricsChartFrameProps {
  title: string;
  /** Small chip next to the title: which provider the series comes from. */
  providerLabel?: string;
  providerTitle?: string;
  state: ChartFrameState;
  /** Previous series still on screen while the next one loads. */
  stale?: boolean;
  errorMessage?: string;
  emptyMessage?: string;
  loadingMessage?: string;
  zoomed?: boolean;
  onResetZoom?: () => void;
  onRefresh?: () => void;
  refreshing?: boolean;
  onSettings?: () => void;
  children: React.ReactNode;
}

/** Title row with actions, then the plot area with its loading/empty/error overlay. */
export const MetricsChartFrame: React.FC<MetricsChartFrameProps> = ({
  title,
  providerLabel,
  providerTitle,
  state,
  stale = false,
  errorMessage,
  emptyMessage = 'No samples in this window',
  loadingMessage = 'Loading metrics…',
  zoomed = false,
  onResetZoom,
  onRefresh,
  refreshing = false,
  onSettings,
  children,
}) => (
  <div className="mx-frame">
    <div className="mx-frame-head">
      <div className="mx-frame-title-wrap">
        <span className="mx-frame-title">{title}</span>
        {providerLabel && (
          <span className="mx-provider-chip" title={providerTitle || providerLabel}>
            <span className="ap-dot ap-dot--success" />
            {providerLabel}
          </span>
        )}
      </div>
      <div className="mx-frame-actions">
        {zoomed && onResetZoom && (
          <button type="button" className="ap-icon-btn ap-icon-btn--sm is-active" onClick={onResetZoom} title="Reset zoom">
            <ResetZoomIcon />
          </button>
        )}
        {onRefresh && (
          <button
            type="button"
            className={`ap-icon-btn ap-icon-btn--sm mx-refresh${refreshing ? ' is-spinning' : ''}`}
            onClick={onRefresh}
            disabled={refreshing}
            title="Refresh"
          >
            <RefreshIcon />
          </button>
        )}
        {onSettings && (
          <button type="button" className="ap-icon-btn ap-icon-btn--sm" onClick={onSettings} title="Monitoring settings">
            <GearIcon />
          </button>
        )}
      </div>
    </div>

    <div className="mx-plot" data-state={state} data-stale={stale ? 'true' : undefined}>
      {children}
      {state !== 'ready' && (
        <div className="mx-plot-overlay" role={state === 'error' ? 'alert' : 'status'}>
          {state === 'loading' && (
            <>
              <span className="ap-spinner" />
              <span className="mx-plot-overlay-text">{loadingMessage}</span>
            </>
          )}
          {state === 'empty' && <span className="mx-plot-overlay-text">{emptyMessage}</span>}
          {state === 'error' && (
            <span className="mx-plot-overlay-text mx-plot-overlay-error">{errorMessage || 'Metrics unavailable'}</span>
          )}
        </div>
      )}
    </div>
  </div>
);

export default MetricsChartFrame;
