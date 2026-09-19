import React from 'react';
import { KeyIcon, SearchIcon, UnpluggedIcon, WaveformIcon } from './metricsIcons';
import type { MetricsProviderInfo } from './metricsProvider';
import { providerDisplayName, type ProviderPhase } from './metricsProvider';

interface MetricsProviderStateProps {
  phase: Exclude<ProviderPhase, 'ready'>;
  reason?: string;
  provider?: MetricsProviderInfo | null;
  detecting?: boolean;
  onDetectAgain: () => void;
  onInstall?: () => void;
  installing?: boolean;
  installError?: string | null;
  onSettings: () => void;
}

/**
 * The card body when there is nothing to chart yet. Same height as the plot
 * so the card does not jump when a provider appears.
 */
export const MetricsProviderState: React.FC<MetricsProviderStateProps> = ({
  phase,
  reason,
  provider,
  detecting = false,
  onDetectAgain,
  onInstall,
  installing = false,
  installError,
  onSettings,
}) => {
  if (phase === 'detecting') {
    return (
      <div className="mx-state" role="status" aria-live="polite">
        <span className="ap-spinner" />
        <span className="mx-state-body">Looking for a metrics provider…</span>
      </div>
    );
  }

  const detectLabel = detecting ? 'Detecting…' : 'Detect again';

  if (phase === 'needs-tenant') {
    const name = providerDisplayName(provider) || 'Mimir';
    return (
      <div className="mx-state" role="status">
        <span className="mx-state-icon is-warning">
          <KeyIcon />
        </span>
        <span className="mx-state-title">{name} needs a tenant</span>
        <span className="mx-state-body">
          {reason || `${name} answered, but rejects queries without an X-Scope-OrgID header. Choose the tenant that holds this cluster's metrics.`}
        </span>
        <div className="mx-state-actions">
          <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" onClick={onSettings}>
            Choose tenant…
          </button>
          <button type="button" className="ap-btn ap-btn--sm" onClick={onDetectAgain} disabled={detecting}>
            {detectLabel}
          </button>
        </div>
      </div>
    );
  }

  if (phase === 'unreachable') {
    return (
      <div className="mx-state" role="alert">
        <span className="mx-state-icon is-danger">
          <UnpluggedIcon />
        </span>
        <span className="mx-state-title">Metrics provider unreachable</span>
        <span className="mx-state-body">
          {reason || 'A provider was detected, but the query stream could not reach it.'}
        </span>
        <div className="mx-state-actions">
          <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" onClick={onDetectAgain} disabled={detecting}>
            {detecting ? 'Trying…' : 'Try again'}
          </button>
          <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={onSettings}>
            Settings…
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-state" role="status">
      <span className="mx-state-icon">{detecting ? <SearchIcon /> : <WaveformIcon />}</span>
      <span className="mx-state-title">No metrics provider</span>
      <span className="mx-state-body">
        {reason || 'Nothing in this cluster answers the Prometheus API.'}
      </span>
      {installError && <span className="mx-state-error">{installError}</span>}
      <div className="mx-state-actions">
        {onInstall && (
          <button type="button" className="ap-btn ap-btn--sm ap-btn--primary" onClick={onInstall} disabled={installing}>
            {installing ? 'Installing Prometheus…' : 'Install Prometheus'}
          </button>
        )}
        <button type="button" className="ap-btn ap-btn--sm" onClick={onDetectAgain} disabled={detecting || installing}>
          {detectLabel}
        </button>
        <button type="button" className="ap-btn ap-btn--sm ap-btn--ghost" onClick={onSettings}>
          Settings…
        </button>
      </div>
    </div>
  );
};

export default MetricsProviderState;
