import React, { useCallback, useMemo, useState } from 'react';
import { lazyView } from '../../utils/lazyView';
import {
  buildView,
  hiddenNeighbourCount,
  searchMatches,
  type KindFamily,
  type ViewNode,
} from './graphModel';
import { useTopologyGraph, type TopologyMode } from './useTopologyGraph';
import { openInInspector } from './topologyNav';
import './Topology.css';

// React Flow and dagre load with the first graph, not with the app.
const TopologyCanvas = lazyView(() => import('./TopologyCanvas'), <div className="topo-canvas topo-canvas--loading" />);

const FAMILIES: { id: KindFamily; label: string }[] = [
  { id: 'network', label: 'Network' },
  { id: 'workloads', label: 'Workloads' },
  { id: 'pods', label: 'Pods' },
  { id: 'config', label: 'Config' },
  { id: 'storage', label: 'Storage' },
  { id: 'policy', label: 'Policy' },
  { id: 'nodes', label: 'Nodes' },
];

export interface TopologyViewProps {
  cluster: string;
  mode: TopologyMode | null;
  compact?: boolean;
  /** Double-click / F on a node: make it the focus. */
  onRefocus?: (n: ViewNode['node']) => void;
  /** Extra controls rendered at the start of the toolbar. */
  leading?: React.ReactNode;
}

const TopologyView: React.FC<TopologyViewProps> = ({ cluster, mode, compact, onRefocus, leading }) => {
  const [includeIdle, setIncludeIdle] = useState(false);
  const g = useTopologyGraph(cluster, mode, { includeIdle, poll: true });
  const [hiddenFamilies, setHiddenFamilies] = useState<Set<KindFamily>>(
    () => new Set<KindFamily>(mode?.kind === 'namespace' ? ['nodes', 'config'] : []),
  );
  const [ungrouped, setUngrouped] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState('');
  const [matchIndex, setMatchIndex] = useState(0);
  const [centerSignal, setCenterSignal] = useState(0);
  const [fitSignal, setFitSignal] = useState(0);

  const view = useMemo(
    () => buildView(g.known, g.visible, { hiddenFamilies, ungrouped, pinned: g.focusId }),
    [g.known, g.visible, hiddenFamilies, ungrouped, g.focusId],
  );
  const hiddenCounts = useMemo(() => {
    const m = new Map<string, number>();
    if (mode?.kind !== 'focus') return m;
    for (const v of view.nodes) if (!v.members) m.set(v.id, hiddenNeighbourCount(g.known, v.id, g.visible));
    return m;
  }, [view, g.known, g.visible, mode?.kind]);

  const matchList = useMemo(() => searchMatches(view, query), [view, query]);
  const matches = useMemo(() => new Set(matchList), [matchList]);
  const searchActive = query.trim().length > 0;

  const onOpen = useCallback((v: ViewNode) => {
    if (v.members) {
      setUngrouped((s) => new Set(s).add(v.node.owner!));
      return;
    }
    if (v.node.missing) return;
    void openInInspector(cluster, v.node);
  }, [cluster]);
  const onUngroup = useCallback((owner: string) => setUngrouped((s) => new Set(s).add(owner)), []);
  const onRefocusId = useCallback((id: string) => {
    const n = g.known.nodes.get(id);
    if (n && onRefocus) onRefocus(n);
  }, [g.known, onRefocus]);

  const toggleFamily = (f: KindFamily) =>
    setHiddenFamilies((s) => { const next = new Set(s); next.has(f) ? next.delete(f) : next.add(f); return next; });

  const stepMatch = (dir: number) => {
    if (!matchList.length) return;
    const i = (matchIndex + dir + matchList.length) % matchList.length;
    setMatchIndex(i);
    setCenterSignal((n) => n + 1);
  };

  const grouped = view.nodes.some((v) => v.members);
  const nodeCount = view.nodes.reduce((acc, v) => acc + (v.members ? v.members.length : 1), 0);

  let body: React.ReactNode;
  if (!mode) {
    body = <div className="topo-empty">Pick a resource or a namespace to see how things connect.</div>;
  } else if (g.loading && g.known.nodes.size === 0) {
    body = <div className="topo-empty" role="status"><span className="topo-spinner" /> Mapping relationships…</div>;
  } else if (g.error && g.known.nodes.size === 0) {
    body = (
      <div className="topo-empty" role="alert">
        <span>{g.error}</span>
        <button type="button" className="ap-btn" onClick={() => void g.refresh()}>Try again</button>
      </div>
    );
  } else if (view.nodes.length === 0) {
    body = (
      <div className="topo-empty">
        {g.known.nodes.size === 0 ? 'Nothing here yet.' : 'Every resource is hidden by the filters.'}
      </div>
    );
  } else {
    body = (
      <TopologyCanvas
        view={view}
        focusId={g.focusId}
        expandable={mode.kind === 'focus'}
        expanded={g.expanded}
        hiddenCounts={hiddenCounts}
        loadingIds={g.loadingIds}
        matches={matches}
        searchActive={searchActive}
        centerOn={matchList[matchIndex]}
        centerSignal={centerSignal}
        fitSignal={fitSignal}
        compact={compact}
        onExpand={g.expand}
        onCollapse={g.collapse}
        onOpen={onOpen}
        onUngroup={onUngroup}
        onRefocus={onRefocus ? onRefocusId : undefined}
      />
    );
  }

  return (
    <div className={`topo-view ${compact ? 'topo-view--compact' : ''}`}>
      <div className="topo-toolbar">
        {leading}
        <label className="ap-search ap-search--sm topo-search">
          <svg viewBox="0 0 16 16" aria-hidden><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M10.5 10.5 14 14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
          <input
            value={query}
            placeholder="Find in graph"
            aria-label="Find in graph"
            onChange={(e) => { setQuery(e.target.value); setMatchIndex(0); setCenterSignal((n) => n + 1); }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); stepMatch(e.shiftKey ? -1 : 1); }
              if (e.key === 'Escape') setQuery('');
            }}
          />
          {searchActive && <span className="topo-search-count">{matchList.length ? `${matchIndex + 1}/${matchList.length}` : '0'}</span>}
        </label>
        {!compact && (
          <div className="topo-chips" role="group" aria-label="Show kinds">
            {FAMILIES.map((f) => (
              <button
                key={f.id}
                type="button"
                className={`topo-chip ${hiddenFamilies.has(f.id) ? '' : 'is-on'}`}
                aria-pressed={!hiddenFamilies.has(f.id)}
                onClick={() => toggleFamily(f.id)}
              >
                {f.label}
              </button>
            ))}
            <button
              type="button"
              className={`topo-chip ${includeIdle ? 'is-on' : ''}`}
              aria-pressed={includeIdle}
              title="Show ReplicaSets of old rollouts"
              onClick={() => setIncludeIdle((v) => !v)}
            >
              Old revisions
            </button>
          </div>
        )}
        <span className="topo-toolbar-spacer" />
        {grouped || ungrouped.size > 0 ? (
          <button type="button" className="ap-btn ap-btn--ghost topo-tool" onClick={() => setUngrouped(ungrouped.size ? new Set() : new Set(view.nodes.filter((v) => v.members).map((v) => v.node.owner!)))}>
            {ungrouped.size ? 'Group pods' : 'Show each pod'}
          </button>
        ) : null}
        {mode?.kind === 'focus' && !compact && (
          <button type="button" className="ap-btn ap-btn--ghost topo-tool" onClick={() => void g.expandAll()} title="Expand every node on screen">
            Expand all
          </button>
        )}
        <button type="button" className="ap-btn ap-btn--ghost topo-tool" onClick={() => setFitSignal((n) => n + 1)} title="Fit to view">
          Fit
        </button>
      </div>
      {(g.known.warnings.length > 0 || g.known.truncated || (g.error && g.known.nodes.size > 0)) && (
        <div className="topo-notice" role="status">
          {g.error && g.known.nodes.size > 0 ? `${g.error}. Showing the last graph.` : null}
          {g.known.truncated ? 'Large namespace: showing the first resources by importance. ' : null}
          {g.known.warnings.length > 0 ? `Partial graph (${g.known.warnings.join(', ')}).` : null}
        </div>
      )}
      <div className="topo-body">{body}</div>
      {!compact && view.nodes.length > 0 && (
        <div className="ap-statusbar topo-status">
          {nodeCount} resource{nodeCount === 1 ? '' : 's'} · {view.edges.length} link{view.edges.length === 1 ? '' : 's'}
          {mode?.kind === 'focus' ? ' · + to expand, double-click to refocus' : ''}
          {g.updatedAt ? ' · live' : ''}
        </div>
      )}
    </div>
  );
};

export default TopologyView;
