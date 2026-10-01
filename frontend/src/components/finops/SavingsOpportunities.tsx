import React, { useState } from 'react';
import { ExternalLinkIcon } from '@radix-ui/react-icons';
import { CostRecommendation, formatCost } from '../../types/finops';
import { workloadResource } from './finopsView';
import './SavingsOpportunities.css';

interface Props {
  recommendations: CostRecommendation[];
  onOpenWorkload?: (kind: string, namespace: string, name: string) => void;
  onOpenNode?: (name: string) => void;
  /** Rightsizing rows open the engine's evidence instead of the workload. */
  onOpenRightsizing?: (w: NonNullable<CostRecommendation['rightsizing']>) => void;
}

const TYPE_LABEL: Record<CostRecommendation['type'], string> = {
  rightsize: 'Right-size',
  'underutilized-node': 'Consolidate',
  'no-requests': 'Set requests',
};

const COLLAPSED_COUNT = 5;

export const SavingsOpportunities: React.FC<Props> = ({ recommendations, onOpenWorkload, onOpenNode, onOpenRightsizing }) => {
  const [expanded, setExpanded] = useState(false);
  if (recommendations.length === 0) return null;

  const visible = expanded ? recommendations : recommendations.slice(0, COLLAPSED_COUNT);
  const hidden = recommendations.length - visible.length;

  const openable = (r: CostRecommendation) =>
    r.kind === 'Node' ? !!onOpenNode : !!onOpenWorkload && !!r.kind && !!workloadResource(r.kind) && !r.vclusterNamespace;

  const open = (r: CostRecommendation) => {
    if (r.kind === 'Node') onOpenNode?.(r.resource);
    else if (r.kind) onOpenWorkload?.(r.kind, r.namespace || '', r.resource);
  };

  return (
    <div className="finops-section">
      <div className="section-header">
        <h3>Savings Opportunities</h3>
        <span className="section-count">{recommendations.length} found</span>
      </div>
      <div className="section-content savings-list">
        {visible.map((r, i) => (
          <div key={`${r.type}/${r.namespace}/${r.resource}/${i}`} className="savings-row">
            <span className={`savings-type savings-${r.type}`}>{TYPE_LABEL[r.type] ?? r.type}</span>
            <div className="savings-body">
              <div className="savings-target">
                {r.rightsizing && onOpenRightsizing && (
                  <button className="savings-evidence" onClick={() => onOpenRightsizing(r.rightsizing!)}>Evidence</button>
                )}
                {r.kind && r.kind !== 'Node' && <span className="wl-kind-badge">{r.kind}</span>}
                <span className="savings-name">{r.resource}</span>
                {r.namespace && (
                  <span className="savings-ns">{r.vclusterNamespace ? `${r.namespace} › ${r.vclusterNamespace}` : r.namespace}</span>
                )}
                {openable(r) && (
                  <button className="row-open-btn" onClick={() => open(r)} title={`Open ${r.resource}`} aria-label={`Open ${r.resource}`}>
                    <ExternalLinkIcon />
                  </button>
                )}
              </div>
              <div className="savings-text">{r.recommendation}</div>
            </div>
            <div className="savings-amount">
              {r.projectedSavings > 0 ? (
                <>
                  <span className="savings-value">{formatCost(r.projectedSavings)}</span>
                  <span className="savings-unit">/mo</span>
                </>
              ) : (
                <span className="savings-unpriced">hygiene</span>
              )}
            </div>
          </div>
        ))}
        {recommendations.length > COLLAPSED_COUNT && (
          <button className="savings-more" onClick={() => setExpanded(!expanded)}>
            {expanded ? 'Show fewer' : `Show ${hidden} more`}
          </button>
        )}
      </div>
    </div>
  );
};
