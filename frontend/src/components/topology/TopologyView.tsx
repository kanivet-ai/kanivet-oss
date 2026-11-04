import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useStore } from '../../store';
import api from '../../services/api';
import AnchoredPopover from '../common/AnchoredPopover';
import { lazyView } from '../../utils/lazyView';
import {
  buildView,
  hiddenNeighbourCount,
  type KindFamily,
  type TopoNode,
  type ViewNode,
} from './graphModel';
import { buildAppView, pruneEdges, unreferencedConfig } from './appModel';
import { useTopologyGraph, type TopologyMode } from './useTopologyGraph';
import { openInInspector } from './topologyNav';
import NodeSummary from './NodeSummary';
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

/** Below this toolbar width the actions fold into one overflow menu. */
const NARROW_PX = 720;

export interface TopologyViewProps {
  cluster: string;
  mode: TopologyMode;
  /** Make a resource the focus (summary "Focus", F, related rows). */
  onRefocus: (n: TopoNode) => void;
  /** Start of the toolbar (the view switcher). */
  leading: React.ReactNode;
  /** The header's resource picker, given what is on screen. */
  picker: (onScreen: ViewNode[], selectNode: (id: string) => void) => React.ReactNode;
}

function useOutsideClose(open: boolean, refs: React.RefObject<HTMLElement | null>[], close: () => void) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (refs.some((r) => r.current?.contains(t))) return;
      if ((t as HTMLElement).closest?.('.topo-filter-pop')) return;
      close();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey); };
  }, [open, refs, close]);
}

const TopologyView: React.FC<TopologyViewProps> = ({ cluster, mode, onRefocus, leading, picker }) => {
  const isApp = mode.kind === 'namespace';
  const [includeIdle, setIncludeIdle] = useState(false);
  const g = useTopologyGraph(cluster, mode, { includeIdle, poll: true });
  const [hiddenFamilies, setHiddenFamilies] = useState<Set<KindFamily>>(() => new Set<KindFamily>(['nodes']));
  const [showEachPod, setShowEachPod] = useState(false);
  const [showUnused, setShowUnused] = useState(false);
  const [ungrouped, setUngrouped] = useState<Set<string>>(new Set());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [centerSignal, setCenterSignal] = useState(0);
  const [centerOn, setCenterOn] = useState<string | undefined>();
  const [fitSignal, setFitSignal] = useState(0);
  const [minimap, setMinimap] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [narrow, setNarrow] = useState(false);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const menuBtnRef = useRef<HTMLButtonElement>(null);
  const closeMenu = useCallback(() => setMenuOpen(false), []);
  const menuRefs = useMemo(() => [menuBtnRef], []);
  useOutsideClose(menuOpen, menuRefs, closeMenu);

  // Selection belongs to what is being looked at.
  useEffect(() => { setSelectedId(null); setUngrouped(new Set()); }, [cluster, mode.kind === 'focus' ? mode.ref.name + mode.ref.kind : mode.namespace]); // eslint-disable-line react-hooks/exhaustive-deps

  useLayoutEffect(() => {
    const el = toolbarRef.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => setNarrow(entry.contentRect.width < NARROW_PX));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const allUngrouped = useMemo(() => {
    if (!showEachPod) return ungrouped;
    const s = new Set(ungrouped);
    for (const n of g.known.nodes.values()) if (n.kind === 'Pod' && n.owner) s.add(n.owner);
    return s;
  }, [showEachPod, ungrouped, g.known]);

  // The app view leaves out config nothing uses (Helm release records, ...).
  const unused = useMemo(() => (isApp && !showUnused ? unreferencedConfig(g.known) : null), [isApp, showUnused, g.known]);
  const visible = useMemo(() => {
    if (!unused?.size) return g.visible;
    return new Set([...g.visible].filter((id) => !unused.has(id)));
  }, [g.visible, unused]);
  const view = useMemo(
    () => buildView(g.known, visible, { hiddenFamilies, ungrouped: allUngrouped, pinned: g.focusId }),
    [g.known, visible, hiddenFamilies, allUngrouped, g.focusId],
  );
  const app = useMemo(() => (isApp ? buildAppView(g.known, view) : undefined), [isApp, g.known, view]);
  // Focus view: the same one-line-per-relation pruning, no lanes.
  const focusView = useMemo(
    () => (isApp ? view : { nodes: view.nodes, edges: pruneEdges(g.known, view, new Map()) }),
    [isApp, view, g.known],
  );
  const hiddenCounts = useMemo(() => {
    const m = new Map<string, number>();
    if (mode.kind !== 'focus') return m;
    for (const v of view.nodes) if (!v.members) m.set(v.id, hiddenNeighbourCount(g.known, v.id, g.visible));
    return m;
  }, [view, g.known, g.visible, mode.kind]);

  const drawn = app ? app.view : focusView;
  const byId = useMemo(() => new Map(drawn.nodes.map((v) => [v.id, v])), [drawn]);
  const drawnAs = useMemo(() => {
    const m = new Map<string, string>();
    for (const v of drawn.nodes) { m.set(v.id, v.id); for (const p of v.members || []) m.set(p.id, v.id); }
    for (const [from, to] of app?.folded || []) m.set(from, to);
    return m;
  }, [drawn, app]);
  // A selection whose node went away (collapsed, filtered) clears itself.
  const selected = selectedId ? byId.get(selectedId) : undefined;

  const selectNode = useCallback((id: string) => {
    setSelectedId(id);
    setCenterOn(id);
    setCenterSignal((n) => n + 1);
  }, []);

  const inspect = useCallback((n: TopoNode) => { if (!n.missing) void openInInspector(cluster, n); }, [cluster]);
  const activate = useCallback((v: ViewNode) => {
    if (v.members) { const o = g.known.nodes.get(v.node.owner!); if (o) inspect(o); return; }
    inspect(v.node);
  }, [g.known, inspect]);

  const openBottomTab = useStore((s) => s.openBottomTab);
  const logs = useCallback(async (n: TopoNode) => {
    try {
      const item = await api.getResourceDetails(cluster, n.group, n.version || 'v1', n.kind, n.namespace || '', n.name);
      const obj = item?.metadata ? item : { ...item, metadata: { name: n.name, namespace: n.namespace } };
      openBottomTab(n.kind === 'Pod' ? 'logs' : 'deployment-logs', { ...obj, kind: n.kind, apiVersion: n.group ? `${n.group}/${n.version}` : n.version }, cluster);
    } catch {
      void openInInspector(cluster, n);
    }
  }, [cluster, openBottomTab]);

  const refocusId = useCallback((id: string) => {
    const n = g.known.nodes.get(id);
    if (n) onRefocus(n);
  }, [g.known, onRefocus]);
  const onUngroup = useCallback((owner: string) => setUngrouped((s) => new Set(s).add(owner)), []);

  const toggleFamily = (f: KindFamily) =>
    setHiddenFamilies((s) => { const next = new Set(s); if (next.has(f)) next.delete(f); else next.add(f); return next; });

  const nodeCount = drawn.nodes.reduce((acc, v) => acc + (v.members ? v.members.length : 1), 0);
  const edgeCount = (app ? app.edges : focusView.edges).length;
  const hiddenCount = hiddenFamilies.size - (hiddenFamilies.has('nodes') ? 1 : 0) + (showEachPod ? 1 : 0) + (includeIdle ? 1 : 0) + (showUnused ? 1 : 0);
  const canExpandAll = mode.kind === 'focus';

  let body: React.ReactNode;
  if (g.loading && g.known.nodes.size === 0) {
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
        {g.known.nodes.size === 0 ? (isApp ? 'This namespace has no workloads yet.' : 'Nothing relates to this resource.') : 'Every resource is hidden by the filters.'}
        {g.known.nodes.size > 0 && <button type="button" className="ap-btn" onClick={() => setHiddenFamilies(new Set(['nodes']))}>Reset filters</button>}
      </div>
    );
  } else {
    body = (
      <TopologyCanvas
        view={focusView}
        app={app}
        focusId={g.focusId}
        expandable={mode.kind === 'focus'}
        expanded={g.expanded}
        hiddenCounts={hiddenCounts}
        loadingIds={g.loadingIds}
        matches={EMPTY}
        searchActive={false}
        selectedId={selected?.id}
        centerOn={centerOn}
        centerSignal={centerSignal}
        fitSignal={fitSignal}
        minimap={minimap}
        onSelect={(id) => setSelectedId(id)}
        onActivate={activate}
        onExpand={g.expand}
        onCollapse={g.collapse}
        onUngroup={onUngroup}
        onRefocus={refocusId}
      />
    );
  }

  const filterItems = (
    <>
      <div className="ap-combobox-group-label">Show</div>
      {FAMILIES.map((f) => (
        <label key={f.id} className="ap-menu-item topo-check-row">
          <input type="checkbox" checked={!hiddenFamilies.has(f.id)} onChange={() => toggleFamily(f.id)} />
          <span>{f.label}</span>
        </label>
      ))}
      <div className="topo-menu-sep" role="separator" />
      <label className="ap-menu-item topo-check-row">
        <input type="checkbox" checked={showEachPod} onChange={(e) => { setShowEachPod(e.target.checked); if (!e.target.checked) setUngrouped(new Set()); }} />
        <span>Show each pod</span>
      </label>
      {isApp && (
        <label className="ap-menu-item topo-check-row" title="ConfigMaps, Secrets and ServiceAccounts nothing refers to">
          <input type="checkbox" checked={showUnused} onChange={(e) => setShowUnused(e.target.checked)} />
          <span>Unused config{unused?.size ? ` (${unused.size})` : ''}</span>
        </label>
      )}
      <label className="ap-menu-item topo-check-row" title="ReplicaSets of earlier rollouts, scaled to zero">
        <input type="checkbox" checked={includeIdle} onChange={(e) => setIncludeIdle(e.target.checked)} />
        <span>Old revisions</span>
      </label>
      {narrow && (
        <>
          <div className="topo-menu-sep" role="separator" />
          {canExpandAll && <button type="button" className="ap-menu-item" onClick={() => { closeMenu(); void g.expandAll(); }}>Expand all</button>}
          <button type="button" className="ap-menu-item" onClick={() => { closeMenu(); setFitSignal((n) => n + 1); }}>Fit to view</button>
          <label className="ap-menu-item topo-check-row">
            <input type="checkbox" checked={minimap} onChange={(e) => setMinimap(e.target.checked)} />
            <span>Minimap</span>
          </label>
        </>
      )}
    </>
  );

  return (
    <div className="topo-view">
      <div className="topo-toolbar" ref={toolbarRef}>
        <div className="topo-toolbar-start">
          {leading}
          {picker(drawn.nodes, selectNode)}
        </div>
        <div className="topo-toolbar-end">
          <button
            ref={menuBtnRef}
            type="button"
            className={`ap-btn ap-btn--ghost topo-tool${menuOpen ? ' is-active' : ''}`}
            aria-haspopup="true"
            aria-expanded={menuOpen}
            aria-label={narrow ? 'Filters and actions' : 'Filter'}
            onClick={() => setMenuOpen((o) => !o)}
          >
            {narrow ? (
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden><circle cx="3.5" cy="8" r="1.3" fill="currentColor" /><circle cx="8" cy="8" r="1.3" fill="currentColor" /><circle cx="12.5" cy="8" r="1.3" fill="currentColor" /></svg>
            ) : (
              <>
                <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden><path d="M2.5 4h11M4.5 8h7M6.5 12h3" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
                Filter
              </>
            )}
            {hiddenCount > 0 && <span className="topo-tool-badge">{hiddenCount}</span>}
          </button>
          {!narrow && canExpandAll && (
            <button type="button" className="ap-btn ap-btn--ghost topo-tool" onClick={() => void g.expandAll()} title="Expand every node on screen">Expand all</button>
          )}
          {!narrow && (
            <>
              <button type="button" className="ap-btn ap-btn--ghost topo-tool" onClick={() => setFitSignal((n) => n + 1)} title="Fit to view">Fit</button>
              <button
                type="button"
                className="ap-icon-btn ap-icon-btn--sm topo-tool-icon"
                aria-pressed={minimap}
                aria-label="Minimap"
                title={minimap ? 'Hide minimap' : 'Show minimap'}
                onClick={() => setMinimap((m) => !m)}
              >
                <svg viewBox="0 0 16 16" aria-hidden><rect x="2" y="3" width="12" height="10" rx="2" fill="none" stroke="currentColor" strokeWidth="1.5" /><rect x="8" y="8" width="4" height="3" rx="0.8" fill="currentColor" /></svg>
              </button>
            </>
          )}
        </div>
        <AnchoredPopover anchor={menuBtnRef} open={menuOpen} role="menu" className="topo-filter-pop" minWidth={200} maxHeight={420} aria-label="Filter">
          {filterItems}
        </AnchoredPopover>
      </div>
      {(g.known.warnings.length > 0 || g.known.truncated || (g.error && g.known.nodes.size > 0)) && (
        <div className="topo-notice" role="status">
          {g.error && g.known.nodes.size > 0 ? `${g.error}. Showing the last graph. ` : null}
          {g.known.truncated ? 'Large namespace: showing the first resources by importance. ' : null}
          {g.known.warnings.length > 0 ? `Partial graph (${g.known.warnings.join(', ')}).` : null}
        </div>
      )}
      <div className="topo-body">
        {body}
        {selected && (
          <NodeSummary
            key={selected.id}
            v={selected}
            known={g.known}
            drawnAs={drawnAs}
            appLabel={app?.lanes.find((l) => l.nodeIds.includes(selected.id) && l.source !== 'other')?.label}
            expandable={mode.kind === 'focus'}
            expanded={g.expanded.has(selected.id)}
            hidden={hiddenCounts.get(selected.id) || 0}
            isFocus={selected.id === g.focusId}
            onClose={() => setSelectedId(null)}
            onInspect={inspect}
            onLogs={logs}
            onRefocus={onRefocus}
            onExpand={() => void g.expand(selected.id)}
            onCollapse={() => g.collapse(selected.id)}
            onUngroup={selected.members ? () => onUngroup(selected.node.owner!) : undefined}
            onSelect={selectNode}
          />
        )}
      </div>
      {view.nodes.length > 0 && (
        <div className="ap-statusbar topo-status">
          {isApp && app ? `${app.lanes.length} application${app.lanes.length === 1 ? '' : 's'} · ` : ''}
          {nodeCount} resource{nodeCount === 1 ? '' : 's'} · {edgeCount} link{edgeCount === 1 ? '' : 's'}
          {' · click to select, double-click to inspect'}
          {g.updatedAt ? ' · live' : ''}
        </div>
      )}
    </div>
  );
};

const EMPTY = new Set<string>();

export default TopologyView;
