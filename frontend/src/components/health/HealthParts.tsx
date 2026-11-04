import React, { useState } from 'react';
import { CheckIcon, ClipboardCopyIcon, FileTextIcon, ReaderIcon, EnterIcon } from '@radix-ui/react-icons';
import ResourceLink from '../ResourceLink';
import type { HealthIssue, HealthRef, HealthSeverity } from '../../types/health';
import { ageLabel, SEVERITY_DOT, SEVERITY_LABEL, scoreTone } from './healthView';
import { canOpenLogs, openInspector, openLogs, openYamlEditor } from './healthActions';
import AIButton from '../ai/AIButton';
import { issueFixSeed, issueInvestigateSeed } from '../ai/aiContext';
import './Health.css';

export const ScoreRing: React.FC<{ score: number; size?: number; label?: string }> = ({ score, size = 64, label }) => {
  const stroke = Math.max(4, Math.round(size / 11));
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const tone = scoreTone(score);
  return (
    <div className={`health-ring health-ring--${tone}`} style={{ width: size, height: size }} role="img" aria-label={`${label || 'Score'} ${score} of 100`}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle className="health-ring-track" cx={size / 2} cy={size / 2} r={r} strokeWidth={stroke} fill="none" />
        <circle
          className="health-ring-value"
          cx={size / 2}
          cy={size / 2}
          r={r}
          strokeWidth={stroke}
          fill="none"
          strokeLinecap="round"
          strokeDasharray={`${(c * Math.max(0, Math.min(100, score))) / 100} ${c}`}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
      </svg>
      <span className="health-ring-label ap-tnum" style={{ fontSize: Math.round(size / 3.4) }}>{score}</span>
    </div>
  );
};

export const SeverityDot: React.FC<{ severity: HealthSeverity }> = ({ severity }) => (
  <span className={`ap-dot ${SEVERITY_DOT[severity]}`} title={SEVERITY_LABEL[severity]} />
);

export const RefLink: React.FC<{ cluster: string; r: HealthRef; showKind?: boolean }> = ({ cluster, r, showKind = true }) => (
  <ResourceLink cluster={cluster} apiVersion={r.apiVersion || 'v1'} kind={r.kind} name={r.name} namespace={r.namespace}>
    {showKind ? <span className="health-ref-kind">{r.kind}</span> : null}
    <span className="health-ref-name">{r.name}</span>
  </ResourceLink>
);

/** A YAML fix with copy and, when the object can be edited, open in the YAML editor. */
export const FixSnippet: React.FC<{ cluster: string; fix: string; path?: string; target?: HealthRef }> = ({ cluster, fix, path, target }) => {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(fix).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }).catch(() => {});
  };
  const editable = target && path !== 'new object';
  return (
    <div className="health-fix">
      <div className="health-fix-bar">
        <span className="health-fix-path">{path ? `Under ${path}` : 'Fix'}</span>
        <button type="button" className="ap-icon-btn ap-icon-btn--sm" onClick={copy} title="Copy snippet" aria-label="Copy snippet">
          {copied ? <CheckIcon /> : <ClipboardCopyIcon />}
        </button>
        {editable && (
          <button type="button" className="ap-icon-btn ap-icon-btn--sm" title="Open in YAML editor" aria-label="Open in YAML editor"
            onClick={() => { void openYamlEditor(cluster, target!); }}>
            <FileTextIcon />
          </button>
        )}
      </div>
      <pre className="health-fix-code">{fix}</pre>
    </div>
  );
};

/** Logs, inspector and investigate for one issue's resource. */
export const IssueActions: React.FC<{ cluster: string; issue: HealthIssue }> = ({ cluster, issue }) => {
  const logsRef = canOpenLogs(issue.resource) ? issue.resource : issue.affected?.find(canOpenLogs);
  return (
    <div className="health-issue-actions">
      {logsRef && (
        <button type="button" className="ap-icon-btn ap-icon-btn--sm" title="Open logs" aria-label="Open logs"
          onClick={(e) => { e.stopPropagation(); void openLogs(cluster, logsRef); }}>
          <ReaderIcon />
        </button>
      )}
      <button type="button" className="ap-icon-btn ap-icon-btn--sm" title="Open in inspector" aria-label="Open in inspector"
        onClick={(e) => { e.stopPropagation(); void openInspector(cluster, issue.resource); }}>
        <EnterIcon />
      </button>
      <AIButton variant="icon" label="Investigate with AI" seed={() => issueInvestigateSeed(cluster, issue)} />
      <AIButton label="Fix with AI" seed={() => issueFixSeed(cluster, issue)} />
    </div>
  );
};

/** One issue row: dot, title, resource link, reason, message, age, actions. */
export const IssueRow: React.FC<{ cluster: string; issue: HealthIssue; compact?: boolean; now?: number }> = ({ cluster, issue, compact, now }) => {
  const [open, setOpen] = useState(false);
  const age = ageLabel(issue.since, now);
  // In the inspector (compact) a click shows the whole message.
  const hasMore = compact || (issue.affected?.length || 0) > 0 || !!issue.hint;
  return (
    <div className={`health-issue${open ? ' is-open' : ''}`}>
      <div className="health-issue-main" onClick={() => hasMore && setOpen((o) => !o)} role={hasMore ? 'button' : undefined}>
        <SeverityDot severity={issue.severity} />
        <div className="health-issue-text">
          <div className="health-issue-title">
            <span>{issue.title}</span>
            <span className="health-issue-reason ap-mono">{issue.reason}</span>
            {issue.source === 'event' && <span className="ap-badge ap-badge--sm">event</span>}
          </div>
          {!compact && (
            <div className="health-issue-sub">
              <RefLink cluster={cluster} r={issue.resource} />
              {issue.resource.namespace && <span className="health-issue-ns">{issue.resource.namespace}</span>}
            </div>
          )}
          <div className="health-issue-msg" title={issue.message}>{issue.message}</div>
        </div>
        {age && <span className="health-issue-age ap-tnum" title={issue.since}>{age}</span>}
        <IssueActions cluster={cluster} issue={issue} />
      </div>
      {open && !compact && (
        <div className="health-issue-more">
          {issue.hint && <div className="health-issue-hint">{issue.hint}</div>}
          {(issue.affected?.length || 0) > 0 && (
            <div className="health-issue-affected">
              {issue.affected!.slice(0, 50).map((a) => (
                <RefLink key={`${a.kind}/${a.namespace}/${a.name}`} cluster={cluster} r={a} showKind={false} />
              ))}
              {issue.affected!.length > 50 && <span className="ap-muted">and {issue.affected!.length - 50} more</span>}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export const HealthEmpty: React.FC<{ title: string; detail?: string; action?: React.ReactNode }> = ({ title, detail, action }) => (
  <div className="health-empty">
    <div className="health-empty-title">{title}</div>
    {detail && <div className="health-empty-detail">{detail}</div>}
    {action}
  </div>
);

export const UnavailableNote: React.FC<{ kinds?: string[] }> = ({ kinds }) =>
  kinds && kinds.length > 0 ? (
    <div className="health-unavailable" role="note">
      <span className="ap-dot ap-dot--warning" />
      Could not read {kinds.join(', ')}; checks over them were skipped.
    </div>
  ) : null;
