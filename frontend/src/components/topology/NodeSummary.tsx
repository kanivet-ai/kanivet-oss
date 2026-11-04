// The side summary for the selected node: status, key facts, its relations
// and the actions (inspector, logs, refocus, expand). Clicking a node shows
// this; double-click or Enter goes straight to the inspector.
import React, { useMemo } from 'react';
import { getResourceIcon } from '../../utils/resourceIcons';
import { RELATION_LABEL, type Health, type KnownGraph, type TopoNode, type ViewNode } from './graphModel';

interface Props {
  v: ViewNode;
  known: KnownGraph;
  /** Underlying resource ID -> drawn node ID, to select a related node. */
  drawnAs: Map<string, string>;
  appLabel?: string;
  expandable: boolean;
  expanded: boolean;
  hidden: number;
  isFocus: boolean;
  onClose: () => void;
  onInspect: (n: TopoNode) => void;
  onLogs?: (n: TopoNode) => void;
  onRefocus?: (n: TopoNode) => void;
  onExpand: () => void;
  onCollapse: () => void;
  onUngroup?: () => void;
  onSelect: (drawnId: string) => void;
}

const HEALTH_LABEL: Record<Health, string> = { healthy: 'Healthy', warning: 'Degraded', error: 'Failing', unknown: 'No status' };
const LOG_KINDS = new Set(['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job']);

const INCOMING_LABEL: Record<string, string> = {
  owns: 'owned by', selects: 'selected by', exposes: 'exposed by', routes: 'routed from', attaches: 'attached to',
  mounts: 'mounted by', env: 'env of', 'runs-as': 'used by', tls: 'TLS for', binds: 'bound by', class: 'class of',
  scales: 'scaled by', protects: 'protected by', 'runs-on': 'runs', 'applies-to': 'policy', 'pulls-with': 'pull secret of',
};

interface Rel {
  key: string;
  label: string;
  other: TopoNode;
}

const NodeSummary: React.FC<Props> = ({ v, known, drawnAs, appLabel, expandable, expanded, hidden, isFocus, ...a }) => {
  const n = v.node;
  const group = !!v.members;
  const owner = n.owner ? known.nodes.get(n.owner) : undefined;

  const rels = useMemo(() => {
    if (group) return [] as Rel[];
    const out: Rel[] = [];
    const seen = new Set<string>();
    for (const e of known.edges.values()) {
      const outgoing = e.source === n.id;
      if (!outgoing && e.target !== n.id) continue;
      const other = known.nodes.get(outgoing ? e.target : e.source);
      if (!other) continue;
      const label = outgoing ? RELATION_LABEL[e.relation] || e.relation : INCOMING_LABEL[e.relation] || `${e.relation} by`;
      const key = `${label}|${other.id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ key, label, other });
    }
    // Pods last; they are many and the group already shows them.
    return out.sort((x, y) => Number(x.other.kind === 'Pod') - Number(y.other.kind === 'Pod') || x.label.localeCompare(y.label) || x.other.name.localeCompare(y.other.name));
  }, [group, known, n.id]);

  const health = n.missing ? 'unknown' : n.health;

  return (
    <aside className="topo-side" aria-label={`${n.kind} ${n.name}`}>
      <div className="topo-side-head">
        <span className={`topo-side-tile topo-side-tile--${health}`} aria-hidden>{getResourceIcon(n.kind)}</span>
        <span className="topo-side-title">
          <span className="topo-side-name" title={group ? undefined : n.name}>{group ? `${v.members!.length} pods` : n.name}</span>
          <span className="topo-side-sub">{group ? `Pods of ${owner?.name || 'owner'}` : n.kind}{n.namespace ? ` · ${n.namespace}` : ''}</span>
        </span>
        <button type="button" className="ap-icon-btn ap-icon-btn--sm" aria-label="Close summary" title="Close (Esc)" onClick={a.onClose}>
          <svg viewBox="0 0 12 12" aria-hidden><path d="M3 3l6 6M9 3l-6 6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
        </button>
      </div>

      <div className="topo-side-actions">
        {!group && !n.missing && (
          <button type="button" className="ap-btn ap-btn--primary ap-btn--sm" onClick={() => a.onInspect(n)}>Open inspector</button>
        )}
        {group && owner && (
          <button type="button" className="ap-btn ap-btn--primary ap-btn--sm" onClick={() => a.onInspect(owner)}>Open {owner.kind}</button>
        )}
        {a.onLogs && !n.missing && LOG_KINDS.has(group ? owner?.kind || '' : n.kind) && (
          <button type="button" className="ap-btn ap-btn--sm" onClick={() => a.onLogs!(group ? owner! : n)}>Logs</button>
        )}
        {a.onRefocus && !group && !isFocus && (
          <button type="button" className="ap-btn ap-btn--sm" onClick={() => a.onRefocus!(n)} title="Show this resource's neighbourhood (F)">Focus</button>
        )}
        {expandable && !group && (expanded ? (
          <button type="button" className="ap-btn ap-btn--sm" onClick={a.onCollapse}>Collapse</button>
        ) : hidden > 0 ? (
          <button type="button" className="ap-btn ap-btn--sm" onClick={a.onExpand}>Expand {hidden}</button>
        ) : null)}
        {group && a.onUngroup && <button type="button" className="ap-btn ap-btn--sm" onClick={a.onUngroup}>Show each pod</button>}
      </div>

      <div className="topo-side-scroll">
        <div className="ap-card topo-side-card">
          <div className="ap-card-row">
            <span>Status</span>
            <span className="topo-side-status"><span className={`topo-dot topo-dot--${health}`} aria-hidden />{n.missing ? 'Not found' : n.status || HEALTH_LABEL[n.health]}</span>
          </div>
          {appLabel && <div className="ap-card-row"><span>Application</span><span className="topo-side-val">{appLabel}</span></div>}
          {owner && !group && (
            <div className="ap-card-row">
              <span>Owner</span>
              <button type="button" className="topo-link" onClick={() => { const d = drawnAs.get(owner.id); if (d) a.onSelect(d); else a.onInspect(owner); }}>
                {owner.kind} {owner.name}
              </button>
            </div>
          )}
          {n.missing && <div className="ap-card-row"><span>Note</span><span className="topo-side-val">Referenced but not in the cluster</span></div>}
        </div>

        {group && (
          <>
            <div className="ap-group-label topo-side-label">Pods</div>
            <div className="ap-card topo-side-card topo-side-list">
              {v.members!.slice(0, 50).map((p) => (
                <button key={p.id} type="button" className="topo-side-rel" onClick={() => a.onInspect(p)} title="Open in the inspector">
                  <span className={`topo-dot topo-dot--${p.health}`} aria-hidden />
                  <span className="topo-side-rel-name">{p.name}</span>
                  <span className="topo-side-rel-kind">{p.status}</span>
                </button>
              ))}
              {v.members!.length > 50 && <div className="topo-side-more">and {v.members!.length - 50} more</div>}
            </div>
          </>
        )}

        {rels.length > 0 && (
          <>
            <div className="ap-group-label topo-side-label">Related</div>
            <div className="ap-card topo-side-card topo-side-list">
              {rels.slice(0, 40).map((r) => {
                const drawn = drawnAs.get(r.other.id);
                return (
                  <button
                    key={r.key}
                    type="button"
                    className="topo-side-rel"
                    onClick={() => (drawn ? a.onSelect(drawn) : a.onRefocus ? a.onRefocus(r.other) : a.onInspect(r.other))}
                    title={drawn ? 'Select in the graph' : 'Focus on it'}
                  >
                    <span className="topo-side-rel-icon" aria-hidden>{getResourceIcon(r.other.kind)}</span>
                    <span className="topo-side-rel-name">{r.other.name}</span>
                    <span className="topo-side-rel-kind">{r.label} · {r.other.kind}</span>
                  </button>
                );
              })}
            </div>
          </>
        )}
        <div className="topo-side-hint">Double-click a node or press Enter to open it in the inspector.</div>
      </div>
    </aside>
  );
};

export default NodeSummary;
