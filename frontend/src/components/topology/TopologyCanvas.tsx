// The React Flow canvas. Lazy-loaded (React Flow + dagre live in this chunk
// only), so nothing is paid until a topology is first shown.
import React, { memo, useCallback, useEffect, useMemo, useRef } from 'react';
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  Handle,
  Position,
  MarkerType,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { getResourceIcon } from '../../utils/resourceIcons';
import { layoutView, NODE_H, NODE_W } from './layout';
import { RELATION_LABEL, type Health, type ViewGraph, type ViewNode } from './graphModel';
import './Topology.css';

export interface TopologyCanvasProps {
  view: ViewGraph;
  focusId?: string;
  /** Focus mode: nodes can be expanded and collapsed. */
  expandable: boolean;
  expanded: Set<string>;
  /** Known-but-hidden neighbour counts, for the "+N" badge. */
  hiddenCounts: Map<string, number>;
  loadingIds?: Set<string>;
  matches: Set<string>;
  searchActive: boolean;
  /** Bump to centre on `centerOn`. */
  centerOn?: string;
  centerSignal?: number;
  /** Bump to fit the whole graph. */
  fitSignal?: number;
  compact?: boolean;
  onExpand: (id: string) => void;
  onCollapse: (id: string) => void;
  onOpen: (v: ViewNode) => void;
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
  onExpand: (id: string) => void;
  onCollapse: (id: string) => void;
  onOpen: (v: ViewNode) => void;
  onUngroup: (ownerId: string) => void;
}

const HEALTH_LABEL: Record<Health, string> = {
  healthy: 'Healthy',
  warning: 'Degraded',
  error: 'Failing',
  unknown: 'No status',
};

const TopologyCard = memo(({ data }: NodeProps<Node<CardData>>) => {
  const { v, focus, expandable, expanded, hidden, loading, dim, match } = data;
  const n = v.node;
  const group = !!v.members;
  const cls = [
    'topo-node',
    `topo-node--${n.health}`,
    focus && 'topo-node--focus',
    group && 'topo-node--group',
    n.missing && 'topo-node--missing',
    dim && 'topo-node--dim',
    match && 'topo-node--match',
  ].filter(Boolean).join(' ');
  const canExpand = expandable && !group && (!expanded ? hidden > 0 || !n.missing : true);
  return (
    <div
      className={cls}
      onClick={() => data.onOpen(v)}
      title={`${n.kind} ${n.namespace ? `${n.namespace}/` : ''}${n.name}${n.missing ? ' (not found)' : ''}`}
    >
      <Handle type="target" position={Position.Left} className="topo-handle" isConnectable={false} />
      <span className="topo-node-icon" aria-hidden>{getResourceIcon(n.kind)}</span>
      <span className="topo-node-text">
        <span className="topo-node-name">{group ? `${v.members!.length} pods` : n.name}</span>
        <span className="topo-node-meta">
          {group ? (
            <span className="topo-node-counts">
              {(['healthy', 'warning', 'error', 'unknown'] as Health[]).filter((h) => v.counts![h] > 0).map((h) => (
                <span key={h} className="topo-count"><span className={`topo-dot topo-dot--${h}`} />{v.counts![h]}</span>
              ))}
            </span>
          ) : (
            <>
              <span className="topo-node-kind">{n.kind}</span>
              {(n.missing || n.status) && <span className="topo-node-status">{n.missing ? 'not found' : n.status}</span>}
            </>
          )}
        </span>
      </span>
      {!group && <span className={`topo-dot topo-dot--${n.health} topo-node-health`} aria-label={HEALTH_LABEL[n.health]} />}
      {group && (
        <button
          type="button"
          className="topo-node-btn nodrag"
          aria-label="Show each pod"
          title="Show each pod"
          onClick={(e) => { e.stopPropagation(); data.onUngroup(n.owner!); }}
        >
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="M2 3.5 5 6.5 8 3.5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
        </button>
      )}
      {canExpand && (
        <button
          type="button"
          className={`topo-node-btn topo-node-expand nodrag ${expanded ? 'is-expanded' : ''}`}
          aria-label={expanded ? `Collapse ${n.name}` : `Expand ${n.name}`}
          title={expanded ? 'Collapse' : hidden > 0 ? `Show ${hidden} more` : 'Show related'}
          onClick={(e) => { e.stopPropagation(); (expanded ? data.onCollapse : data.onExpand)(n.id); }}
        >
          {loading ? <span className="topo-spinner" /> : expanded ? '−' : '+'}
          {!expanded && hidden > 0 && !loading && <span className="topo-node-badge">{hidden}</span>}
        </button>
      )}
      <Handle type="source" position={Position.Right} className="topo-handle" isConnectable={false} />
    </div>
  );
});
TopologyCard.displayName = 'TopologyCard';

const nodeTypes = { card: TopologyCard };

const MINIMAP_COLOR: Record<Health, string> = {
  healthy: 'var(--green)',
  warning: 'var(--orange)',
  error: 'var(--red)',
  unknown: 'var(--gray)',
};

function Canvas(props: TopologyCanvasProps) {
  const { view, compact } = props;
  const rf = useReactFlow();
  const structureKey = useMemo(
    () => view.nodes.map((n) => n.id).join(',') + '#' + view.edges.map((e) => e.id).join(','),
    [view],
  );
  // Layout only when the structure changes; status updates keep positions.
  const positions = useMemo(() => layoutView(view, compact), [structureKey, compact]); // eslint-disable-line react-hooks/exhaustive-deps

  const nodes: Node<CardData>[] = useMemo(
    () =>
      view.nodes.map((v) => ({
        id: v.id,
        type: 'card',
        position: positions.get(v.id) || { x: 0, y: 0 },
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
          dim: props.searchActive && !props.matches.has(v.id),
          match: props.searchActive && props.matches.has(v.id),
          onExpand: props.onExpand,
          onCollapse: props.onCollapse,
          onOpen: props.onOpen,
          onUngroup: props.onUngroup,
        },
      })),
    [view, positions, props.focusId, props.expandable, props.expanded, props.hiddenCounts, props.loadingIds, props.searchActive, props.matches, props.onExpand, props.onCollapse, props.onOpen, props.onUngroup],
  );

  const edges: Edge[] = useMemo(
    () =>
      view.edges.map((e) => {
        const dim = props.searchActive && !(props.matches.has(e.source) || props.matches.has(e.target));
        const label = RELATION_LABEL[e.relation] || e.relation;
        return {
          id: e.id,
          source: e.source,
          target: e.target,
          type: 'smoothstep',
          label: e.weight > 1 ? `${label} ×${e.weight}` : label,
          className: `topo-edge topo-edge--${e.relation}${dim ? ' topo-edge--dim' : ''}`,
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: 'var(--text4)' },
          labelBgPadding: [4, 2] as [number, number],
          labelBgBorderRadius: 4,
          focusable: false,
        };
      }),
    [view, props.searchActive, props.matches],
  );

  // Fit on first paint and when nodes are added or removed.
  const lastKey = useRef('');
  useEffect(() => {
    if (lastKey.current === structureKey) return;
    const first = lastKey.current === '';
    lastKey.current = structureKey;
    const t = window.setTimeout(() => rf.fitView({ padding: 0.18, duration: first ? 0 : 320, maxZoom: 1.1 }), 30);
    return () => window.clearTimeout(t);
  }, [structureKey, rf]);

  useEffect(() => {
    if (props.fitSignal) rf.fitView({ padding: 0.18, duration: 280, maxZoom: 1.1 });
  }, [props.fitSignal, rf]);

  useEffect(() => {
    if (!props.centerSignal || !props.centerOn) return;
    const p = positions.get(props.centerOn);
    if (p) rf.setCenter(p.x + NODE_W / 2, p.y + NODE_H / 2, { zoom: Math.max(rf.getZoom(), 0.9), duration: 280 });
  }, [props.centerSignal]); // eslint-disable-line react-hooks/exhaustive-deps

  // Keyboard: Tab moves between nodes (React Flow makes them focusable);
  // Enter opens, + / - expand and collapse, F refocuses on the node.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      const el = (document.activeElement as HTMLElement | null)?.closest?.('.react-flow__node') as HTMLElement | null;
      const id = el?.dataset.id;
      if (!id) return;
      const v = view.nodes.find((n) => n.id === id);
      if (!v) return;
      if (e.key === 'Enter') { e.preventDefault(); props.onOpen(v); }
      else if ((e.key === '+' || e.key === '=') && props.expandable && !v.members) { e.preventDefault(); props.onExpand(id); }
      else if ((e.key === '-' || e.key === '_') && props.expandable) { e.preventDefault(); props.onCollapse(id); }
      else if (e.key === ' ' && v.members) { e.preventDefault(); props.onUngroup(v.node.owner!); }
      else if ((e.key === 'f' || e.key === 'F') && props.onRefocus && !v.members && !e.metaKey && !e.ctrlKey) { e.preventDefault(); props.onRefocus(id); }
    },
    [view, props],
  );

  return (
    <div className={`topo-canvas ${compact ? 'topo-canvas--compact' : ''}`} onKeyDown={onKeyDown}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        nodesConnectable={false}
        nodesDraggable={!compact}
        nodesFocusable
        edgesFocusable={false}
        elementsSelectable={false}
        minZoom={0.15}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        onNodeDoubleClick={(_, n) => props.onRefocus && !(n.data as CardData).v.members && props.onRefocus(n.id)}
        panOnScroll={!compact}
        zoomOnScroll={compact}
        zoomOnDoubleClick={false}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="topo-bg" />
        <Controls showInteractive={false} position="bottom-left" className="topo-controls" fitViewOptions={{ padding: 0.18, duration: 280 }} />
        {!compact && (
          <MiniMap
            pannable
            zoomable
            position="bottom-right"
            className="topo-minimap"
            nodeColor={(n) => MINIMAP_COLOR[((n.data as CardData)?.v?.node?.health || 'unknown') as Health]}
            nodeBorderRadius={4}
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
