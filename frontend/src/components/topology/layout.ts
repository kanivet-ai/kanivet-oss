import dagre from '@dagrejs/dagre';
import type { ViewGraph } from './graphModel';

export const NODE_W = 232;
export const NODE_H = 58;

export interface Positioned {
  x: number;
  y: number;
}

/** Left-to-right layered layout: entry points left, pods and storage right. */
export function layoutView(view: ViewGraph, compact = false): Map<string, Positioned> {
  const g = new dagre.graphlib.Graph({ multigraph: true });
  g.setGraph({
    rankdir: 'LR',
    nodesep: compact ? 14 : 22,
    ranksep: compact ? 56 : 84,
    marginx: 16,
    marginy: 16,
  });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of view.nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  for (const e of view.edges) g.setEdge(e.source, e.target, { weight: e.relation === 'owns' ? 3 : 1 }, e.id);
  dagre.layout(g);
  const out = new Map<string, Positioned>();
  for (const n of view.nodes) {
    const p = g.node(n.id);
    if (p) out.set(n.id, { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 });
  }
  return out;
}
