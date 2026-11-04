import React, { useState } from 'react';
import type { HealthFinding } from '../../types/health';
import { FixSnippet, SeverityDot } from './HealthParts';
import ExpandIcon from '../icons/ExpandIcon';

/** One audit finding; expands to the explanation, the fix and ignore actions. */
export const FindingRow: React.FC<{
  cluster: string;
  f: HealthFinding;
  check?: { title: string; explanation: string; fix: string; fixPath?: string };
  onIgnore?: (f: HealthFinding, scope: 'resource' | 'namespace') => void;
}> = ({ cluster, f, check, onIgnore }) => {
  const [open, setOpen] = useState(false);
  return (
    <div className={`health-finding${f.ignored ? ' is-ignored' : ''}${open ? ' is-open' : ''}`}>
      <div
        className="health-finding-main"
        role="button"
        tabIndex={0}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen((o) => !o); } }}
        aria-expanded={open}
      >
        <SeverityDot severity={f.severity} />
        <div className="health-finding-text">
          <span className="health-finding-title">{check?.title || f.checkId}</span>
          <span className="health-finding-msg" title={open ? undefined : `${f.container ? `${f.container}: ` : ''}${f.message}`}>
            {f.container ? `${f.container}: ` : ''}{f.message}
          </span>
        </div>
        {f.ignored && <span className="ap-badge ap-badge--sm">ignored</span>}
        <span className="health-finding-chevron" aria-hidden="true"><ExpandIcon expanded={open} /></span>
      </div>
      {open && check && (
        <div className="health-finding-more">
          <p className="health-check-expl">{check.explanation}</p>
          <FixSnippet cluster={cluster} fix={check.fix} path={check.fixPath} target={f.resource} />
          {onIgnore && !f.ignored && (
            <div className="health-finding-ignore">
              <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => onIgnore(f, 'resource')}>Ignore for this resource</button>
              {f.resource.namespace && (
                <button type="button" className="ap-btn ap-btn--ghost ap-btn--sm" onClick={() => onIgnore(f, 'namespace')}>Ignore in {f.resource.namespace}</button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
};
