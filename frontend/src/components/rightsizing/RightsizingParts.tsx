import React, { memo, useState, useSyncExternalStore } from 'react';
import {
  CheckCircledIcon,
  CrossCircledIcon,
  ExclamationTriangleIcon,
  InfoCircledIcon,
} from '@radix-ui/react-icons';
import type {
  ContainerReport,
  Finding,
  RightsizingReport,
  Verdict,
} from '../../types/rightsizing';
import { Tooltip } from '../common/Tooltip';
import MonitoringSettingsModal from '../MonitoringSettingsModal';
import { sharedClock } from '../../utils/sharedClock';
import {
  VERDICT_META,
  formatMoney,
  formatPct,
  formatResource,
  pctChange,
  relativeTime,
} from './rightsizingView';
// Shared visual language with FinOps: stat cards, banners, kind badges.
import '../finops/FinOpsDashboard.css';
import '../finops/SavingsOpportunities.css';
import './Rightsizing.css';

export const VerdictBadge: React.FC<{ verdict: Verdict; short?: boolean }> = ({
  verdict,
  short,
}) => {
  const m = VERDICT_META[verdict];
  return (
    <span className={`rs-verdict rs-tone-${m.tone}`}>
      <span
        className={`ap-dot ap-dot--${m.tone === 'neutral' ? 'muted' : m.tone}`}
      />
      {short ? m.short : m.label}
    </span>
  );
};

const CONFIDENCE_TEXT = {
  high: 'At least a week of steady data, a narrow interval, and the backtest held up on held-out days.',
  medium:
    'Enough data to act on, with a wider interval or no backtest yet. Review before applying.',
  low: 'Little data or a wide interval. Treat the number as a direction, not a value.',
};

export const ConfidenceMeter: React.FC<{
  level: 'high' | 'medium' | 'low';
  label?: boolean;
  /** Off while a table row hasn't been hovered: its tooltip mounts lazily. */
  tooltip?: boolean;
}> = ({ level, label = true, tooltip = true }) => {
  const filled = level === 'high' ? 3 : level === 'medium' ? 2 : 1;
  return (
    <Tooltip content={tooltip ? CONFIDENCE_TEXT[level] : null}>
      <span
        className={`rs-confidence rs-confidence-${level}`}
        aria-label={`${level} confidence`}
      >
        <span className="rs-confidence-bars" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <span key={i} className={i < filled ? 'on' : ''} />
          ))}
        </span>
        {label && (
          <span className="rs-confidence-label">
            {level[0].toUpperCase() + level.slice(1)}
          </span>
        )}
      </span>
    </Tooltip>
  );
};

/** "12 min ago", kept current. Polls that change nothing leave the report,
 * and so the dashboard, as it was; this re-renders on its own, and only when
 * its text changes. */
export const RelativeTime = memo(function RelativeTime({
  iso,
}: {
  iso: string;
}) {
  // The clock only runs while something listens, so until its first tick
  // its time can be old.
  const [mounted] = useState(Date.now);
  const read = () =>
    relativeTime(iso, Math.max(sharedClock.getSnapshot(), mounted));
  return <>{useSyncExternalStore(sharedClock.subscribe, read, read)}</>;
});

/** "1 core → 220m", muted when there is no change. */
export const ResourceDelta: React.FC<{
  resource: 'cpu' | 'memory';
  from: number;
  to: number;
}> = ({ resource, from, to }) => {
  if (!to) return <span className="rs-delta rs-delta-none">—</span>;
  const same = from > 0 && Math.abs(to - from) / from < 0.02;
  if (same)
    return (
      <span className="rs-delta rs-delta-same">
        {formatResource(resource, from)}
      </span>
    );
  const dir = from === 0 ? 'set' : to < from ? 'down' : 'up';
  return (
    <span className={`rs-delta rs-delta-${dir}`}>
      <span className="rs-delta-from">
        {from ? formatResource(resource, from) : 'unset'}
      </span>
      <span className="rs-delta-arrow" aria-hidden="true">
        →
      </span>
      <span className="rs-delta-to">{formatResource(resource, to)}</span>
    </span>
  );
};

const SEVERITY_ICON = {
  critical: <CrossCircledIcon />,
  warning: <ExclamationTriangleIcon />,
  info: <InfoCircledIcon />,
  good: <CheckCircledIcon />,
};

export const FindingList: React.FC<{ findings: Finding[] }> = ({
  findings,
}) => {
  if (findings.length === 0) return null;
  const rank = { critical: 0, warning: 1, info: 2, good: 3 } as const;
  const sorted = [...findings].sort(
    (a, b) => rank[a.severity] - rank[b.severity],
  );
  return (
    <ul className="rs-findings">
      {sorted.map((f, i) => (
        <li
          key={`${f.code}-${i}`}
          className={`rs-finding rs-finding-${f.severity}`}
        >
          {SEVERITY_ICON[f.severity]}
          <span>{f.message}</span>
        </li>
      ))}
    </ul>
  );
};

/** What to do when the cluster can't support recommendations yet. */
export const HistorySourceState: React.FC<{
  report: RightsizingReport;
  cluster: string;
  onRetry: () => void;
  compact?: boolean;
}> = ({ report, cluster, onRetry, compact }) => {
  const [settings, setSettings] = useState(false);
  const needsTenant = report.status === 'needs-tenant';
  const noData = report.status === 'no-container-data';
  // Both cases are fixed by the Mimir tenant: one has none, the other has
  // one that holds no container metrics.
  const mimirFix = needsTenant || (noData && report.source.type === 'mimir');
  const where = report.source.service ? (
    <strong>
      {report.source.namespace}/{report.source.service}
    </strong>
  ) : (
    'the metrics store'
  );
  return (
    <div
      className={`rs-source-state${compact ? ' compact' : ''}`}
      data-tour="rs-setup"
    >
      <div className="rs-source-title">
        {needsTenant
          ? 'Mimir needs a tenant before it returns data'
          : noData
            ? 'The metrics store has no container metrics for this cluster'
            : 'Rightsizing needs usage history'}
      </div>
      <p>
        {noData ? (
          <>
            {where} answered, but returned no{' '}
            <code>container_cpu_usage_seconds_total</code> series with pod
            labels.
            {report.source.type === 'mimir'
              ? ' Multi-tenant Mimir answers an unknown tenant with empty results: choose the tenant this cluster writes to.'
              : ' It may be a different Prometheus than the one scraping the kubelet: choose the right service.'}
          </>
        ) : needsTenant ? (
          <>
            Kanivet found{' '}
            {report.source.service ? (
              <strong>
                {report.source.namespace}/{report.source.service}
              </strong>
            ) : (
              'a Mimir gateway'
            )}
            , but it answers only for a tenant. Pick one and recommendations
            start computing.
          </>
        ) : (
          <>
            Recommendations come from at least 3 days of CPU and memory history,
            read from Prometheus, Thanos, VictoriaMetrics or Mimir in the
            cluster.
            {report.source.metricsServer
              ? ' metrics-server is running, but it only keeps the last minute, which is not enough to recommend anything safely.'
              : ''}
            {report.source.reason ? (
              <span className="rs-source-reason">
                {' '}
                Detection said: {report.source.reason}.
              </span>
            ) : null}
          </>
        )}
      </p>
      <div className="rs-source-actions">
        <button
          className="ap-btn ap-btn--primary"
          onClick={() => setSettings(true)}
        >
          {mimirFix
            ? 'Choose tenant'
            : noData
              ? 'Choose service'
              : 'Connect Prometheus or Mimir'}
        </button>
        <button className="ap-btn" onClick={onRetry}>
          Detect again
        </button>
      </div>
      {settings && (
        <MonitoringSettingsModal
          cluster={cluster}
          // The button promised a tenant (or a Mimir with data), so open the
          // form on the Mimir fields instead of behind its gear.
          initialExpanded={mimirFix ? 'mimir' : null}
          focusTenant={mimirFix}
          onClose={() => {
            setSettings(false);
            onRetry();
          }}
        />
      )}
    </div>
  );
};

/** The recommendation at a glance: what changes, by how much, what it saves,
 * and whether it held up on unseen days. */
export const ChangeSummary: React.FC<{ c: ContainerReport }> = ({ c }) => {
  const pct = (from: number, to: number) => {
    const p = pctChange(from, to);
    if (!from || Math.abs(p) < 0.02) return null;
    return (
      <span className={`rs-pct ${p < 0 ? 'down' : 'up'}`}>
        {p > 0 ? '+' : '−'}
        {formatPct(Math.abs(p), 0)}
      </span>
    );
  };
  const bt = c.backtest;
  return (
    <div className="rs-summary">
      <div className="rs-summary-tile">
        <span className="rs-summary-label">CPU request</span>
        <span className="rs-summary-value">
          <ResourceDelta
            resource="cpu"
            from={c.cpu.request}
            to={c.cpu.recommended}
          />{' '}
          {pct(c.cpu.request, c.cpu.recommended)}
        </span>
      </div>
      <div className="rs-summary-tile">
        <span className="rs-summary-label">Memory request and limit</span>
        <span className="rs-summary-value">
          <ResourceDelta
            resource="memory"
            from={c.memory.request}
            to={c.memory.recommended}
          />{' '}
          {pct(c.memory.request, c.memory.recommended)}
        </span>
      </div>
      <div className="rs-summary-tile">
        <span className="rs-summary-label">Per month</span>
        <span className="rs-summary-value">
          {c.cpuMonthly > 0 || c.memMonthly > 0 ? (
            c.monthlySavings >= 0 ? (
              <span className="rs-money-save">
                {c.monthlySavings > 0.005
                  ? `saves ${formatMoney(c.monthlySavings)}`
                  : 'no change'}
              </span>
            ) : (
              <span className="rs-money-add">
                +{formatMoney(-c.monthlySavings)}
              </span>
            )
          ) : (
            <span className="rs-muted">no prices</span>
          )}
        </span>
      </div>
      <div className="rs-summary-tile">
        <span className="rs-summary-label">On unseen days</span>
        <span className="rs-summary-value">
          {bt ? (
            <Tooltip
              content={`Re-fitted on ${bt.trainDays} days, scored on the last ${bt.testDays}: CPU over ${formatPct(bt.cpuExceedance)} of the ${bt.bursty ? 'busy ' : ''}time (target ${formatPct(bt.cpuTarget, 0)}), memory ${bt.memBreached ? 'went over' : 'stayed under'}.`}
            >
              <span className={bt.calibrated ? 'rs-ok' : 'rs-warn'}>
                {bt.calibrated ? 'Held up' : 'Missed'}
              </span>
            </Tooltip>
          ) : (
            <span className="rs-muted">too little data</span>
          )}
        </span>
      </div>
    </div>
  );
};

/** Findings as tags; the full sentences on demand. */
export const FindingTags: React.FC<{ findings: Finding[] }> = ({
  findings,
}) => {
  const [open, setOpen] = useState(false);
  if (findings.length === 0) return null;
  const rank = { critical: 0, warning: 1, info: 2, good: 3 } as const;
  const sorted = [...findings].sort(
    (a, b) => rank[a.severity] - rank[b.severity],
  );
  return (
    <div className="rs-finding-tags">
      <div className="rs-finding-tag-row">
        {sorted.map((f, i) => (
          <Tooltip
            key={`${f.code}-${i}`}
            content={open ? undefined : f.message}
          >
            <span className={`rs-tag rs-tag-${f.severity}`}>
              {f.title || f.code}
            </span>
          </Tooltip>
        ))}
        <button
          className="rs-link"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
        >
          {open ? 'Hide details' : 'Details'}
        </button>
      </div>
      {open && <FindingList findings={sorted} />}
    </div>
  );
};
