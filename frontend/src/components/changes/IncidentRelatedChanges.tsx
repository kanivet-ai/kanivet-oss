import React from 'react';
import type { IncidentChangeMarker } from '../../types/incidents';
import ResourceLink from '../ResourceLink';
import { formatAge } from '../../utils/formatters';
import { SOURCE_LABELS, apiVersionOf } from '../../utils/changes';
import type { ChangeSource } from '../../types/changes';
import './Changes.css';

/** "What changed before it broke": changes that preceded an incident entry. */
const IncidentRelatedChanges: React.FC<{ changes: IncidentChangeMarker[]; cluster: string }> = ({ changes, cluster }) => (
  <div className="incident-changes" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
    <span className="incident-changes-label">Changed before this</span>
    {changes.slice(0, 4).map((c) => (
      <div key={c.id} className="incident-change" title={c.summary || c.title}>
        <span className="ap-dot ap-dot--info" aria-hidden="true" />
        {c.kind === 'HelmRelease' ? (
          <span className="incident-change-title">{c.title}</span>
        ) : (
          <ResourceLink
            cluster={cluster}
            apiVersion={apiVersionOf(c)}
            kind={c.kind}
            name={c.name}
            namespace={c.namespace || undefined}
            openInDetailTab
          >
            <span className="incident-change-title">{c.kind} {c.name}: {c.summary || c.title}</span>
          </ResourceLink>
        )}
        <span className="incident-change-time">
          {SOURCE_LABELS[c.source as ChangeSource] || c.source} · {formatAge(c.time)}
        </span>
      </div>
    ))}
    {changes.length > 4 && <span className="chg-more">and {changes.length - 4} more</span>}
  </div>
);

export default IncidentRelatedChanges;
