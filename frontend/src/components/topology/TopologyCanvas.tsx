// The React Flow canvas. Lazy-loaded (React Flow + dagre live in this chunk
// only), so nothing is paid until a topology is first shown.
import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  MiniMap,
  Handle,
  Position,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { getResourceIcon } from '../../utils/resourceIcons';
import { buildLayoutInput, layoutGraph, moreIdFor, NODE_H, NODE_W, READABLE_ZOOM, SAT_H, SAT_W } from './layout';
import { RELATION_LABEL, type Health, type ViewGraph, type ViewNode } from './graphModel';
import type { AppLane, AppView } from './appModel';
import './Topology.css';

export interface TopologyCanvasProps {
  view: ViewGraph;
  /** Application view: lanes, satellites and pruned edges. */
  app?: AppView;
  focusId?: string;
  /** Focus mode: nodes can be expanded and collapsed. */
  expandable: boolean;
  expanded: Set<string>;
  /** Known-but-hidden neighbour counts, for the "+N" badge. */
  hiddenCounts: Map<string, number>;
  loadingIds?: Set<string>;
  matches: Set<string>;
  searchActive: boolean;
  selectedId?: string;
  /** Bump to centre on `centerOn`. */
  centerOn?: string;
  centerSignal?: number;
  /** Bump to fit the whole graph. */
  fitSignal?: number;
  minimap: boolean;
  onSelect: (id: string | null) => void;
  /** Double-click / Enter: open in the inspector. */
  onActivate: (v: ViewNode) => void;
  onExpand: (id: string) => void;
  onCollapse: (id: string) => void;
  onUngroup: (ownerId: string) => void;
  onRefocus?: (id: string) => void;
}

interface CardData extends Record<string, unknown> {
  v: ViewNode;
  focus: boolean;
  expandable: boolean;
  expanded: boolean;
  hidden: number;
  loading: boolean;
  dim: boolean;
  match: boolean;
  selected: boolean;
  onExpand: (id: string) => void;
  onCollapse: (id: string) => void;
}

interface SatData extends Record<string, unknown> {
  v: ViewNode;
  dim: boolean;
  selected: boolean;
}

interface MoreData extends Record<string, unknown> {
  count: number;
  hostId: string;
}

interface LaneData extends Record<string, unknown> {
  lane: AppLane;
  size: number;
}

const HEALTH_LABEL: Record<Health, string> = {
  healthy: 'Healthy',
  warning: 'Degraded',
  error: 'Failing',
  unknown: 'No status',
};

const HEALTH_ORDER: Health[] = ['error', 'warning', 'unknown', 'healthy'];

/** A pod group's health as one bar, worst first. */
const HealthBar: React.FC<{ counts: Record<Health, number> }> = ({ counts }) => {
  const total = HEALTH_ORDER.reduce((a, h) => a + counts[h], 0) || 1;
  return (
    <span className="topo-healthbar" aria-hidden>
      {HEALTH_ORDER.filter((h) => counts[h] > 0).map((h) => (
        <span key={h} className={`topo-healthbar-seg topo-healthbar-seg--${h}`} style={{ flexGrow: counts[h] / total }} />
      ))}
    </span>
  );
};

const LEAF_KINDS = new Set(['Node', 'StorageClass', 'PersistentVolume']);

const TopologyCard = memo(({ data }: NodeProps<Node<CardData>>) => {
  const { v, focus, expandable, expanded, hidden, loading, dim, match, selected } = data;
  const n = v.node;
  const group = !!v.members;
  const cls = [
    'topo-node',
    `topo-node--${n.missing ? 'missing' : n.health}`,
    focus && 'topo-node--focus',
    group && 'topo-node--group',
    dim && 'topo-node--dim',
    match && 'topo-node--match',
    selected && 'is-selected',
  ].filter(Boolean).join(' ');
  // Unexpanded nodes can always be asked for more: their neighbours may not
  // be known yet. Pods and cluster-scoped leaves are left alone.
  const canExpand = expandable && !group && !n.missing && (expanded || hidden > 0 || !LEAF_KINDS.has(n.kind));
  return (
    <div className={cls} title={`${n.kind} ${n.namespace ? `${n.namespace}/` : ''}${n.name}${n.missing ? ' (not found)' : ''}`}>
      <Handle type="target" position={Position.Left} className="topo-handle" isConnectable={false} />
      <span className="topo-node-icon" aria-hidden>{getResourceIcon(n.kind)}</span>
      <span className="topo-node-text">
        <span className="topo-node-name">{group ? `${v.members!.length} pods` : n.name}</span>
        {group ? (
          <span className="topo-node-meta">
            <HealthBar counts={v.counts!} />
            <span className="topo-node-status">{n.status}</span>
          </span>
        ) : (
          <span className="topo-node-meta">
            <span className="topo-node-kind">{n.kind}</span>
            {(n.missing || n.status) && <span className="topo-node-status">{n.missing ? 'not found' : n.status}</span>}
          </span>
        )}
      </span>
      {canExpand ? (
        <button
          type="button"
          className={`topo-node-expand nodrag ${expanded ? 'is-expanded' : ''}`}
          aria-label={expanded ? `Collapse ${n.name}` : `Expand ${n.name}`}
          title={expanded ? 'Collapse' : hidden > 0 ? `Show ${hidden} related` : 'Show related'}
          onClick={(e) => { e.stopPropagation(); (expanded ? data.onCollapse : data.onExpand)(n.id); }}
          onDoubleClick={(e) => e.stopPropagation()}
        >
          {loading ? <span className="topo-spinner" /> : expanded ? '−' : hidden > 0 ? `+${hidden}` : '+'}
        </button>
      ) : (
        <span className={`topo-dot topo-dot--${n.missing ? 'unknown' : n.health} topo-node-health`} role="img" aria-label={HEALTH_LABEL[n.health]} />
      )}
      <Handle type="source" position={Position.Right} className="topo-handle" isConnectable={false} />
    </div>
  );
});
TopologyCard.displayName = 'TopologyCard';

const SatelliteChip = memo(({ data }: NodeProps<Node<SatData>>) => {
  const n = data.v.node;
  return (
    <div
      className={`topo-sat topo-sat--${n.missing ? 'missing' : n.health}${data.dim ? ' topo-node--dim' : ''}${data.selected ? ' is-selected' : ''}`}
      title={`${n.kind} ${n.name}${n.missing ? ' (not found)' : ''}`}
    >
      <span className="topo-sat-icon" aria-hidden>{getResourceIcon(n.kind)}</span>
      <span className="topo-sat-name">{n.name}</span>
      {n.missing && <span className="topo-sat-flag">missing</span>}
    </div>
  );
});
SatelliteChip.displayName = 'SatelliteChip';

const MoreChip = memo(({ data }: NodeProps<Node<MoreData>>) => (
  <div className="topo-sat topo-sat--more">+{data.count} more</div>
));
MoreChip.displayName = 'MoreChip';

const LaneFrame = memo(({ data }: NodeProps<Node<LaneData>>) => {
  const { lane, size } = data;
  const bad = lane.counts.error + lane.counts.warning;
  return (
    <div className={`topo-lane topo-lane--${lane.health}`}>
      <div className="topo-lane-head">
        <span className={`topo-dot topo-dot--${lane.health}`} aria-hidden />
        <span className="topo-lane-title">{lane.label}</span>
        <span className="topo-lane-meta">
          {size} resource{size === 1 ? '' : 's'}
          {lane.counts.error ? ` · ${lane.counts.error} failing` : ''}
          {lane.counts.warning ? ` · ${lane.counts.warning} degraded` : ''}
          {!bad && lane.counts.healthy ? ' · healthy' : ''}
        </span>
      </div>
    </div>
  );
});
LaneFrame.displayName = 'LaneFrame';

const nodeTypes = { card: TopologyCard, sat: SatelliteChip, more: MoreChip, lane: LaneFrame };

const MINIMAP_CLASS: Record<Health, string> = {
  healthy: 'topo-mm--healthy',
  warning: 'topo-mm--warning',
  error: 'topo-mm--error',
  unknown: 'topo-mm--unknown',
};

const FIT = { padding: 0.12, maxZoom: 1 };
const EDGE_PAD = 28;
/** Room the side summary takes on the right (its width plus margins). */
const SUMMARY_W = 336;

/** The graph's extent, from the layout (every node has a known size). */
function extent(nodes: Node[]) {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const x = n.position.x, y = n.position.y;
    minX = Math.min(minX, x); minY = Math.min(minY, y);
    maxX = Math.max(maxX, x + (n.width || NODE_W)); maxY = Math.max(maxY, y + (n.height || NODE_H));
  }
  return { minX, minY, width: maxX - minX, height: maxY - minY };
}

function Canvas(props: TopologyCanvasProps) {
  const { app } = props;
  const view = app ? app.view : props.view;
  const rf = useReactFlow();
  const [hoverEdge, setHoverEdge] = useState<string | null>(null);
  const structureKey = useMemo(
    () => view.nodes.map((n) => n.id).join(',') + '#' + (app ? app.edges : view.edges).map((e) => e.id).join(',') + (app ? '#app' : ''),
    [view, app],
  );
  // Layout only when the structure changes; status updates keep positions.
  // The canvas size steers how lanes pack; bucketed so a resize of a few
  // pixels doesn't re-run the layout.
  const boxRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<{ width: number; height: number } | undefined>();
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => {
      const b = (v: number) => Math.max(200, Math.round(v / 160) * 160);
      const next = { width: b(e.contentRect.width), height: b(e.contentRect.height) };
      setSize((prev) => (prev && prev.width === next.width && prev.height === next.height ? prev : next));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const layout = useMemo(() => layoutGraph(buildLayoutInput(view, app), false, size), [structureKey, size]); // eslint-disable-line react-hooks/exhaustive-deps
  const drawnEdges = app ? app.edges : view.edges;

  const nodesRef = useRef<Node[]>([]);
  const nodes: Node[] = useMemo(() => {
    const out: Node[] = [];
    if (app) {
      for (const r of layout.lanes) {
        const lane = app.lanes.find((l) => l.id === r.id);
        if (!lane) continue;
        out.push({
          id: `lane:${r.id}`, type: 'lane', position: { x: r.x, y: r.y }, width: r.width, height: r.height,
          selectable: false, focusable: false, draggable: false, zIndex: -1,
          data: { lane, size: lane.nodeIds.length } satisfies LaneData,
        });
      }
    }
    for (const v of view.nodes) {
      const position = layout.positions.get(v.id) || { x: 0, y: 0 };
      const dim = props.searchActive && !props.matches.has(v.id);
      if (app?.hostOf.has(v.id)) {
        out.push({
          id: v.id, type: 'sat', position, width: SAT_W, height: SAT_H, ariaLabel: `${v.node.kind} ${v.node.name}`,
          data: { v, dim, selected: props.selectedId === v.id } satisfies SatData,
        });
        continue;
      }
      out.push({
        id: v.id,
        type: 'card',
        position,
        width: NODE_W,
        height: NODE_H,
        ariaLabel: `${v.node.kind} ${v.node.name}`,
        data: {
          v,
          focus: v.id === props.focusId,
          expandable: props.expandable,
          expanded: props.expanded.has(v.id),
          hidden: props.hiddenCounts.get(v.id) || 0,
          loading: !!props.loadingIds?.has(v.id),
          dim,
          match: props.searchActive && props.matches.has(v.id),
          selected: props.selectedId === v.id,
          onExpand: props.onExpand,
          onCollapse: props.onCollapse,
        } satisfies CardData,
      });
    }
    for (const [host, count] of layout.overflow) {
      const position = layout.positions.get(moreIdFor(host));
      if (position) out.push({ id: moreIdFor(host), type: 'more', position, width: SAT_W, height: SAT_H, selectable: false, data: { count, hostId: host } satisfies MoreData });
    }
    return out;
  }, [view, app, layout, props.focusId, props.expandable, props.expanded, props.hiddenCounts, props.loadingIds, props.searchActive, props.matches, props.selectedId, props.onExpand, props.onCollapse]);

  nodesRef.current = nodes;

  const edges: Edge[] = useMemo(
    () =>
      drawnEdges.map((e) => {
        const dim = props.searchActive && !(props.matches.has(e.source) || props.matches.has(e.target));
        const active = hoverEdge === e.id || (!!props.selectedId && (e.source === props.selectedId || e.target === props.selectedId));
        const label = RELATION_LABEL[e.relation] || e.relation;
        return {
          id: e.id,
          source: e.source,
          target: e.target,
          type: 'smoothstep',
          pathOptions: { borderRadius: 10, offset: 18 },
          label: active ? (e.weight > 1 ? `${label} ×${e.weight}` : label) : undefined,
          className: `topo-edge topo-edge--${e.relation}${dim ? ' topo-edge--dim' : ''}${active ? ' is-active' : ''}`,
          markerEnd: active ? 'topo-arrow-active' : 'topo-arrow',
          labelBgPadding: [6, 2] as [number, number],
          labelBgBorderRadius: 6,
          labelBgClassName: 'topo-edge-label-bg',
          labelClassName: 'topo-edge-label',
          focusable: false,
          zIndex: active ? 1 : 0,
        };
      }),
    [drawnEdges, props.searchActive, props.matches, props.selectedId, hoverEdge],
  );

  // Fit once the cards are measured, and again (animated) whenever nodes are
  // added or removed; status updates and selection leave the viewport alone.
  // Every node has an explicit size, so fitting does not wait on the DOM;
  // a short delay lets React Flow take the new nodes in first.
  // Fit the whole graph, unless that would shrink cards past reading: then
  // start at the top left at a readable zoom (failing apps sort first).
  const fit = useCallback((duration: number) => {
    const el = boxRef.current;
    const list = nodesRef.current;
    if (!el || !list.length) { void rf.fitView({ ...FIT, duration }); return; }
    const { minX, minY, width, height } = extent(list);
    const W = el.clientWidth, H = el.clientHeight;
    const z = Math.min(W / (width * (1 + FIT.padding)), H / (height * (1 + FIT.padding)), FIT.maxZoom);
    if (z >= READABLE_ZOOM) { void rf.fitView({ ...FIT, duration }); return; }
    const zoom = READABLE_ZOOM;
    const x = width * zoom < W ? (W - width * zoom) / 2 - minX * zoom : EDGE_PAD - minX * zoom;
    void rf.setViewport({ x, y: EDGE_PAD - minY * zoom, zoom }, { duration });
  }, [rf, app]);
  const fitted = useRef('');
  const [ready, setReady] = useState(false);
  const fitKey = size ? `${structureKey}@${size.width}x${size.height}` : '';
  useEffect(() => {
    if (!fitKey || fitted.current === fitKey) return;
    const first = fitted.current === '';
    // A timer, not rAF: frames don't run in a background window.
    const t = window.setTimeout(() => {
      fitted.current = fitKey;
      fit(first ? 0 : 360);
      setReady(true);
    }, 40);
    return () => window.clearTimeout(t);
  }, [fitKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (props.fitSignal) fit(280);
  }, [props.fitSignal]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!props.centerSignal || !props.centerOn) return;
    const p = layout.positions.get(props.centerOn);
    if (p) void rf.setCenter(p.x + NODE_W / 2, p.y + NODE_H / 2, { zoom: Math.max(rf.getZoom(), 0.9), duration: 280 });
  }, [props.centerSignal]); // eslint-disable-line react-hooks/exhaustive-deps

  const byId = useMemo(() => new Map(view.nodes.map((v) => [v.id, v])), [view]);

  // Keep the selected node clear of the summary panel on the right.
  useEffect(() => {
    const el = boxRef.current;
    const p = props.selectedId ? layout.positions.get(props.selectedId) : undefined;
    if (!el || !p) return;
    const { x, y, zoom } = rf.getViewport();
    const right = (p.x + NODE_W) * zoom + x;
    const limit = el.clientWidth - SUMMARY_W;
    if (right > limit) void rf.setViewport({ x: x - (right - limit), y, zoom }, { duration: 240 });
  }, [props.selectedId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Keyboard: Tab moves between nodes; arrows select, Enter opens the
  // inspector, + / - expand and collapse, F refocuses, Esc clears.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const el = (document.activeElement as HTMLElement | null)?.closest?.('.react-flow__node') as HTMLElement | null;
      const id = el?.dataset.id;
      if (e.key === 'Escape') { props.onSelect(null); return; }
      if (!id) return;
      const v = byId.get(id);
      if (!v) return;
      if (e.key === 'Enter') { e.preventDefault(); props.onActivate(v); }
      else if (e.key === ' ') { e.preventDefault(); props.onSelect(id); }
      else if ((e.key === '+' || e.key === '=') && props.expandable && !v.members) { e.preventDefault(); props.onExpand(id); }
      else if ((e.key === '-' || e.key === '_') && props.expandable) { e.preventDefault(); props.onCollapse(id); }
      else if ((e.key === 'f' || e.key === 'F') && props.onRefocus && !v.members && !e.metaKey && !e.ctrlKey) { e.preventDefault(); props.onRefocus(id); }
    },
    [byId, props],
  );

  const many = view.nodes.length > 120;

  return (
    <div ref={boxRef} className={`topo-canvas${ready ? ' is-ready' : ''}`} onKeyDown={onKeyDown}>
      <svg className="topo-defs" aria-hidden width="0" height="0">
        <defs>
          <marker id="topo-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M1 1 9 5 1 9z" className="topo-arrow-path" />
          </marker>
          <marker id="topo-arrow-active" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M1 1 9 5 1 9z" className="topo-arrow-path topo-arrow-path--active" />
          </marker>
        </defs>
      </svg>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        nodesConnectable={false}
        nodesDraggable={false}
        nodesFocusable
        edgesFocusable={false}
        elementsSelectable={false}
        onlyRenderVisibleElements={many}
        minZoom={0.1}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_, n) => {
          if (n.type === 'lane') { props.onSelect(null); return; }
          if (n.type === 'more') { props.onSelect((n.data as MoreData).hostId); return; }
          props.onSelect(n.id);
        }}
        onNodeDoubleClick={(_, n) => {
          const v = byId.get(n.id);
          if (v) props.onActivate(v);
        }}
        onPaneClick={() => props.onSelect(null)}
        onEdgeMouseEnter={(_, e) => setHoverEdge(e.id)}
        onEdgeMouseLeave={() => setHoverEdge(null)}
        panOnScroll
        zoomOnPinch
        zoomOnDoubleClick={false}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} className="topo-bg" />
        {props.minimap && (
          <MiniMap
            pannable
            zoomable
            position="bottom-right"
            className="topo-minimap"
            style={{ width: 168, height: 112 }}
            nodeClassName={(n) => (n.type === 'lane' ? 'topo-mm--lane' : MINIMAP_CLASS[((n.data as CardData)?.v?.node?.health || 'unknown') as Health])}
            nodeBorderRadius={6}
            nodeStrokeWidth={0}
            ariaLabel="Topology overview"
          />
        )}
      </ReactFlow>
    </div>
  );
}

const TopologyCanvas: React.FC<TopologyCanvasProps> = (props) => (
  <ReactFlowProvider>
    <Canvas {...props} />
  </ReactFlowProvider>
);

export default TopologyCanvas;
