import React, { useMemo, useState } from 'react';
import type { RbacRisk, RiskSeverity } from '../../types/rbac';
import RefLink from './RefLink';
import AIButton from '../ai/AIButton';
import { accessSeed } from '../ai/aiContext';
import { countBySeverity, scopeLabel, SEVERITY_ORDER, severityBadge, subjectKey } from './accessUtils';

interface Props {
  cluster: string;
  risks: RbacRisk[];
  compact?: boolean;
  onOpenSubject?: (s: RbacRisk['subjects'][number]) => void;
}

const LABEL: Record<RiskSeverity, string> = { critical: 'Critical', high: 'High', medium: 'Medium' };

/** Risky grants grouped by severity, each with the binding that grants it. */
const RiskList: React.FC<Props> = ({ cluster, risks, compact, onOpenSubject }) => {
  const [severity, setSeverity] = useState<RiskSeverity | 'all'>('all');
  const counts = useMemo(() => countBySeverity(risks), [risks]);
  const shown = severity === 'all' ? risks : risks.filter((r) => r.severity === severity);
  const riskSeed = (r: RbacRisk, kind: 'explain' | 'tighten') => {
    const who = r.subjects.map((x) => `${x.kind} ${x.namespace ? x.namespace + '/' : ''}${x.name}`).join(', ') || 'its subjects';
    const detail = [`${r.title} (${r.severity}, ${scopeLabel(r.scope)})`, r.detail,
      `Binding: ${(r.binding as any).kind || 'RoleBinding'} ${(r.binding as any).namespace ? (r.binding as any).namespace + '/' : ''}${(r.binding as any).name}`,
      `Role: ${(r.role as any).kind || 'Role'} ${(r.role as any).namespace ? (r.role as any).namespace + '/' : ''}${(r.role as any).name}`,
      `Subjects: ${who}`].join('\n');
    const role = r.role as any;
    return accessSeed(cluster, who, detail, kind, role?.name ? { kind: role.kind || (role.namespace ? 'Role' : 'ClusterRole'), apiVersion: 'rbac.authorization.k8s.io/v1', namespace: role.namespace, name: role.name } : undefined);
  };

  return (
    <div className={`access-risks${compact ? ' is-compact' : ''}`}>
      {!compact && (
        <div className="access-risks-head">
          <div className="ap-segmented ap-segmented--sm" role="group" aria-label="Severity">
            <button type="button" aria-pressed={severity === 'all'} onClick={() => setSeverity('all')}>All {risks.length}</button>
            {SEVERITY_ORDER.map((s) => (
              <button key={s} type="button" aria-pressed={severity === s} onClick={() => setSeverity(s)}>
                {LABEL[s]} {counts[s]}
              </button>
            ))}
          </div>
        </div>
      )}
      {shown.length === 0 ? (
        <div className="access-empty">No risky grants{severity !== 'all' ? ` at ${severity} severity` : ''}.</div>
      ) : (
        <ul className="access-risk-list">
          {shown.map((r) => (
            <li key={r.id} className={`access-risk access-risk--${r.severity}`}>
              <div className="access-risk-top">
                <span className={`ap-badge ap-badge--sm ${severityBadge(r.severity)}`}>{LABEL[r.severity]}</span>
                <span className="access-risk-title">{r.title}</span>
                <span className="access-risk-scope">{scopeLabel(r.scope)}</span>
                <span className="access-risk-ai">
                  <AIButton variant="icon" label="Explain these permissions" seed={() => riskSeed(r, 'explain')} />
                  <AIButton label="Tighten with AI" seed={() => riskSeed(r, 'tighten')} />
                </span>
              </div>
              <div className="access-risk-detail">{r.detail}</div>
              <div className="access-risk-chain">
                <RefLink cluster={cluster} refObj={r.binding} showKind />
                <span className="access-arrow" aria-hidden="true">→</span>
                <RefLink cluster={cluster} refObj={r.role} showKind qualified={false} />
              </div>
              {!compact && r.subjects.length > 0 && (
                <div className="access-risk-subjects">
                  {r.subjects.map((s) => (
                    <button
                      key={subjectKey(s)}
                      type="button"
                      className="access-chip"
                      onClick={() => onOpenSubject?.(s)}
                      title={`Show ${s.kind} permissions`}
                    >
                      <span className="access-chip-kind">{s.kind === 'ServiceAccount' ? 'SA' : s.kind}</span>
                      {s.namespace ? `${s.namespace}/` : ''}
                      {s.name}
                    </button>
                  ))}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};

export default RiskList;
