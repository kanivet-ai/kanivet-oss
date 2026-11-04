import React from 'react';
import type { RbacPath } from '../../types/rbac';
import RefLink from './RefLink';
import { scopeLabel } from './accessUtils';

const ruleSummary = (p: RbacPath): string => {
  const r = p.rule;
  const res = r.resources?.length ? r.resources : r.nonResourceURLs || [];
  const groups = r.apiGroups?.length ? ` (${r.apiGroups.map((g) => g || 'core').join(', ')})` : '';
  return `${r.verbs.join(', ')} ${res.join(', ')}${groups}`;
};

/** Each binding → role → rule chain that explains a grant. */
const PathList: React.FC<{ cluster: string; paths: RbacPath[] }> = ({ cluster, paths }) => (
  <ul className="access-paths">
    {paths.map((p, i) => (
      <li key={i} className="access-path">
        <div className="access-path-chain">
          {p.binding.kind && (
            <>
              <RefLink cluster={cluster} refObj={p.binding} showKind />
              <span className="access-arrow" aria-hidden="true">→</span>
            </>
          )}
          <RefLink cluster={cluster} refObj={p.role} showKind qualified={false} />
          {p.rule.source && (
            <span className="access-path-meta">
              aggregated from <RefLink cluster={cluster} refObj={{ kind: 'ClusterRole', name: p.rule.source }} />
            </span>
          )}
          <span className={`ap-badge ap-badge--sm ${p.scope ? '' : 'ap-badge--purple'}`}>{scopeLabel(p.scope)}</span>
        </div>
        <div className="access-path-rule">
          <code>{ruleSummary(p)}</code>
          {p.rule.resourceNames?.length ? <span className="access-path-meta">only {p.rule.resourceNames.join(', ')}</span> : null}
          {p.via && <span className="access-path-meta">as {p.via}</span>}
        </div>
      </li>
    ))}
  </ul>
);

export default PathList;
