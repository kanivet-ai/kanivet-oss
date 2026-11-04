import React from 'react';
import type { ChangeEntry } from '../../types/changes';
import ResourceLink from '../ResourceLink';
import { formatAge } from '../../utils/formatters';
import { CATEGORY_LABELS, SOURCE_LABELS, apiVersionOf, categoryBadgeClass, formatClock, primaryCategory } from '../../utils/changes';

interface Props {
  entry: ChangeEntry;
  cluster: string;
  selected?: boolean;
  baseline?: boolean;
  /** Show the subject (feed); the History tab already is about it. */
  showSubject?: boolean;
  onSelect?: (e: ChangeEntry) => void;
}

const dotClass = (e: ChangeEntry) => {
  if (e.warning) return 'ap-dot--danger';
  const c = primaryCategory(e);
  if (c === 'created') return 'ap-dot--success';
  if (c === 'env' || c === 'config' || c === 'secret') return 'ap-dot--warning';
  if (c === 'sync' || c === 'replicas') return 'ap-dot--purple';
  if (c === 'event' || c === 'edit' || c === 'other') return 'ap-dot--muted';
  return 'ap-dot--info';
};

const ChangeEntryRow: React.FC<Props> = ({ entry, cluster, selected, baseline, showSubject, onSelect }) => {
  const cats = entry.categories.filter((c) => c !== 'rollout' || entry.categories.length === 1);
  const interactive = !!onSelect;
  return (
    <div
      className={`chg-row${selected ? ' is-selected' : ''}${baseline ? ' is-baseline' : ''}${interactive ? ' is-interactive' : ''}`}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-pressed={interactive ? !!selected : undefined}
      onClick={() => onSelect?.(entry)}
      onKeyDown={(ev) => {
        if (interactive && (ev.key === 'Enter' || ev.key === ' ')) {
          ev.preventDefault();
          onSelect?.(entry);
        }
      }}
    >
      <div className="chg-rail" aria-hidden="true">
        <span className={`ap-dot ${dotClass(entry)}`} />
      </div>
      <div className="chg-body">
        <div className="chg-head">
          <span className="chg-title">{entry.title}</span>
          {entry.current && <span className="ap-badge ap-badge--sm ap-badge--success">current</span>}
          {baseline && <span className="ap-badge ap-badge--sm">baseline</span>}
          <span className="chg-time ap-tnum" title={new Date(entry.time).toLocaleString()}>
            {formatClock(entry.time)} · {formatAge(entry.time)}
          </span>
        </div>
        {showSubject && (
          <div className="chg-subject">
            <span className="chg-kind">{entry.subject.kind}</span>
            {entry.subject.kind === 'HelmRelease' ? (
              <span className="chg-name">{entry.subject.namespace}/{entry.subject.name}</span>
            ) : (
              <span onClick={(ev) => ev.stopPropagation()}>
                <ResourceLink
                  cluster={cluster}
                  apiVersion={apiVersionOf(entry.subject)}
                  kind={entry.subject.kind}
                  name={entry.subject.name}
                  namespace={entry.subject.namespace || undefined}
                  openInDetailTab
                >
                  {entry.subject.namespace ? `${entry.subject.namespace}/` : ''}{entry.subject.name}
                </ResourceLink>
              </span>
            )}
          </div>
        )}
        {entry.summary && <div className="chg-summary">{entry.summary}</div>}
        {entry.fields && entry.fields.length > 0 && (
          <div className="chg-fields">
            {entry.fields.slice(0, 8).map((f) => <code key={f} className="chg-field">{f}</code>)}
            {entry.fields.length > 8 && <span className="chg-more">+{entry.fields.length - 8}</span>}
          </div>
        )}
        <div className="chg-meta">
          {cats.map((c) => (
            <span key={c} className={`ap-badge ap-badge--sm ${categoryBadgeClass(c)}`}>{CATEGORY_LABELS[c] || c}</span>
          ))}
          <span className="chg-source">from {SOURCE_LABELS[entry.source] || entry.source}</span>
          {entry.revision && <span className="chg-source ap-tnum">rev {entry.revision}</span>}
          {entry.actor && <span className="chg-source">by {entry.actor}</span>}
          {entry.origin && entry.origin.kind !== 'HelmRelease' && entry.origin.name !== entry.subject.name && (
            <span className="chg-source" onClick={(ev) => ev.stopPropagation()}>
              <ResourceLink
                cluster={cluster}
                apiVersion={apiVersionOf(entry.origin)}
                kind={entry.origin.kind}
                name={entry.origin.name}
                namespace={entry.origin.namespace || undefined}
                openInDetailTab
              >
                {entry.origin.kind} {entry.origin.name}
              </ResourceLink>
            </span>
          )}
        </div>
      </div>
    </div>
  );
};

export default ChangeEntryRow;
