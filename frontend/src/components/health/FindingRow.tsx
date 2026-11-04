import React, { useState } from 'react';
import type { HealthFinding } from '../../types/health';
import { FixSnippet, SeverityDot } from './HealthParts';
import AIButton from '../ai/AIButton';
import { auditExplainSeed, auditFixSeed } from '../ai/aiContext';

/** One audit finding; expands to the explanation, the fix and ignore actions. */
export const FindingRow: React.FC<{
  cluster: string;
  f: HealthFinding;
  check?: { title: string; explanation: string; fix: string; fixPath?: string };
  onIgnore?: (f: HealthFinding, scope: 'resource' | 'namespace') => void;
}> = ({ cluster, f, check, onIgnore }) => {
  const [open, setOpen] = useState(false);
  return (
    <div className={`health-finding${f.ignored ? ' is-ignored' : ''}`}>
      <div className="health-finding-main" role="button" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        <SeverityDot severity={f.severity} />
        <span className="health-finding-title">{check?.title || f.checkId}</span>
        <span className="health-finding-msg">{f.container ? `${f.container}: ` : ''}{f.message}</span>
        {f.ignored && <span className="ap-badge ap-badge--sm">ignored</span>}
        {!f.ignored && (
          <span className="health-finding-ai" onClick={(e) => e.stopPropagation()}>
            <AIButton variant="icon" label="Explain with AI" seed={() => auditExplainSeed(cluster, f, check)} />
            <AIButton label="Fix with AI" seed={() => auditFixSeed(cluster, f, check)} />
          </span>
        )}
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
