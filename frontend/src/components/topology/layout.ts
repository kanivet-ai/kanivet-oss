// Layout for the topology canvas. Split in two so the interesting part is
// testable without dagre: `buildLayoutInput` decides what is laid out where
// (lanes, card sizes, satellites under their host, which edges steer the
// ranks), `layoutGraph` runs dagre per lane and packs the lanes on a shelf.
import dagre from '@dagrejs/dagre';
import type { ViewGraph } from './graphModel';
import type { AppView } from './appModel';

export const NODE_W = 236;
export const NODE_H = 60;
export const SAT_W = 168;
export const SAT_H = 26;
export const SAT_GAP = 6;
/** Satellites sit indented under their host, joined by a short rail. */
export const SAT_INDENT = 18;
/** ID of the "+N more" chip under a host with too many satellites. */
export const moreIdFor = (hostId: string) => `more:${hostId}`;
/** Satellites per row under a host card. */
export const SAT_PER_ROW = 1;
export const SAT_MAX_ROWS = 4;
export const LANE_PAD = 20;
export const LANE_HEADER = 34;
export const LANE_GAP = 28;
/** Lanes are packed on shelves no wider than this (or the widest lane). */
export const SHELF_W = 2200;

export interface Positioned {
  x: number;
  y: number;
}

export interface LayoutBox {
  id: string;
  width: number;
  height: number;
}

export interface LayoutLaneInput {
  id: string;
  /** Main-flow nodes; a host's height includes its satellite rows. */
  nodes: LayoutBox[];
  edges: { source: string; target: string; weight: number }[];
  /** Host ID -> satellites stacked under its card. */
  satellites: Map<string, string[]>;
  /** Satellites folded into a "+N more" chip under the host. */
  overflow: Map<string, number>;
  /** Draw a container (app view) or just the nodes (focus view). */
  framed: boolean;
}

export interface LayoutInput {
  lanes: LayoutLaneInput[];
}

export interface LaneRect {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface LayoutResult {
  positions: Map<string, Positioned>;
  lanes: LaneRect[];
  /** Satellites that did not fit, per host (drawn as a "+N" chip). */
  overflow: Map<string, number>;
}

/** Height a host card needs for `n` satellites under it. */
export function hostHeight(n: number): number {
  const rows = Math.min(SAT_MAX_ROWS, Math.ceil(n / SAT_PER_ROW));
  return NODE_H + (rows > 0 ? SAT_GAP + rows * (SAT_H + SAT_GAP) : 0);
}

const RANK_WEIGHT: Record<string, number> = { owns: 4, exposes: 3, routes: 3, attaches: 3, selects: 1 };

/**
 * What to lay out. Without an app view (focus mode) everything is one
 * unframed lane. With one, each application is a framed lane; satellites are
 * not ranked themselves but make their host taller; only edges inside a lane
 * steer the ranks (a cross-app edge still draws, it just doesn't pull).
 */
export function buildLayoutInput(view: ViewGraph, app?: AppView): LayoutInput {
  if (!app) {
    return {
      lanes: [{
        id: 'all',
        nodes: view.nodes.map((v) => ({ id: v.id, width: NODE_W, height: NODE_H })),
        edges: view.edges.map((e) => ({ source: e.source, target: e.target, weight: RANK_WEIGHT[e.relation] || 1 })),
        satellites: new Map(),
        overflow: new Map(),
        framed: false,
      }],
    };
  }
  const laneOf = new Map<string, string>();
  for (const lane of app.lanes) for (const id of lane.nodeIds) laneOf.set(id, lane.id);
  const maxSats = SAT_MAX_ROWS * SAT_PER_ROW;
  return {
    lanes: app.lanes.map((lane) => {
      const satellites = new Map<string, string[]>();
      const overflow = new Map<string, number>();
      const nodes: LayoutBox[] = [];
      for (const id of lane.nodeIds) {
        if (app.hostOf.has(id)) continue;
        const sats = app.satellitesOf.get(id) || [];
        let shown = sats;
        if (sats.length > maxSats) {
          shown = sats.slice(0, maxSats - 1);
          overflow.set(id, sats.length - shown.length);
        }
        if (shown.length) satellites.set(id, shown);
        const rows = shown.length + (overflow.has(id) ? 1 : 0);
        nodes.push({ id, width: NODE_W, height: hostHeight(rows) });
      }
      const ids = new Set(nodes.map((n) => n.id));
      const edges = app.edges
        .filter((e) => ids.has(e.source) && ids.has(e.target) && laneOf.get(e.source) === lane.id && laneOf.get(e.target) === lane.id)
        .map((e) => ({ source: e.source, target: e.target, weight: RANK_WEIGHT[e.relation] || 1 }));
      return { id: lane.id, nodes, edges, satellites, overflow, framed: true };
    }),
  };
}

interface LaidOutLane {
  input: LayoutLaneInput;
  pos: Map<string, Positioned>;
  width: number;
  height: number;
}

function layoutLane(lane: LayoutLaneInput, compact: boolean): LaidOutLane {
  const g = new dagre.graphlib.Graph({ multigraph: true });
  g.setGraph({ rankdir: 'LR', nodesep: compact ? 14 : 20, ranksep: compact ? 56 : 76, marginx: 0, marginy: 0, ranker: 'network-simplex' });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of lane.nodes) g.setNode(n.id, { width: n.width, height: n.height });
  lane.edges.forEach((e, i) => g.setEdge(e.source, e.target, { weight: e.weight, minlen: 1 }, `e${i}`));
  dagre.layout(g);
  const pos = new Map<string, Positioned>();
  let maxX = 0;
  let maxY = 0;
  for (const n of lane.nodes) {
    const p = g.node(n.id);
    const x = p.x - n.width / 2;
    const y = p.y - n.height / 2;
    pos.set(n.id, { x, y });
    maxX = Math.max(maxX, x + n.width);
    maxY = Math.max(maxY, y + n.height);
    // Satellites stack under the host card, left-aligned with it.
    const sats = lane.satellites.get(n.id) || [];
    sats.forEach((sid, i) => {
      pos.set(sid, { x: x + SAT_INDENT, y: y + NODE_H + SAT_GAP + i * (SAT_H + SAT_GAP) });
    });
    if (lane.overflow.has(n.id)) {
      pos.set(moreIdFor(n.id), { x: x + SAT_INDENT, y: y + NODE_H + SAT_GAP + sats.length * (SAT_H + SAT_GAP) });
    }
  }
  return { input: lane, pos, width: maxX, height: maxY };
}

/** Runs the layout: dagre per lane, lanes packed left to right on shelves. */
/** Lowest zoom at which card text is still readable. */
export const READABLE_ZOOM = 0.6;

/**
 * How wide a shelf of lanes may get. With the canvas size known, lanes pack
 * to the canvas's shape, but never wider than what fits across at a readable
 * zoom: a big namespace then scrolls down instead of shrinking to dots.
 */
export function shelfWidth(lanes: { width: number; height: number }[], viewport?: { width: number; height: number }): number {
  const widest = Math.max(0, ...lanes.map((l) => l.width));
  if (!viewport || viewport.width <= 0 || viewport.height <= 0) return Math.max(SHELF_W, widest);
  const area = lanes.reduce((a, l) => a + (l.width + LANE_GAP) * (l.height + LANE_GAP), 0);
  const ideal = Math.sqrt(area * (viewport.width / viewport.height)) * 1.08;
  return Math.max(widest, Math.min(ideal, viewport.width / READABLE_ZOOM));
}

export function layoutGraph(input: LayoutInput, compact = false, viewport?: { width: number; height: number }): LayoutResult {
  const laid = input.lanes.filter((l) => l.nodes.length).map((l) => layoutLane(l, compact));
  const positions = new Map<string, Positioned>();
  const lanes: LaneRect[] = [];
  const overflow = new Map<string, number>();
  const boxes = laid.map((l) => {
    const pad = l.input.framed ? LANE_PAD * 2 : 0;
    return { width: l.width + pad, height: l.height + pad + (l.input.framed ? LANE_HEADER : 0) };
  });
  const shelfW = shelfWidth(boxes, viewport);
  let x = 0;
  let y = 0;
  let shelfH = 0;
  for (const l of laid) {
    const pad = l.input.framed ? LANE_PAD : 0;
    const head = l.input.framed ? LANE_HEADER : 0;
    const w = l.width + pad * 2;
    const h = l.height + pad * 2 + head;
    if (x > 0 && x + w > shelfW) {
      x = 0;
      y += shelfH + LANE_GAP;
      shelfH = 0;
    }
    for (const [id, p] of l.pos) positions.set(id, { x: x + pad + p.x, y: y + head + pad + p.y });
    if (l.input.framed) lanes.push({ id: l.input.id, x, y, width: w, height: h });
    for (const [id, n] of l.input.overflow) overflow.set(id, n);
    x += w + LANE_GAP;
    shelfH = Math.max(shelfH, h);
  }
  return { positions, lanes, overflow };
}

/** Back-compat helper: lays out a view as one unframed lane. */
export function layoutView(view: ViewGraph, compact = false): Map<string, Positioned> {
  return layoutGraph(buildLayoutInput(view), compact).positions;
}
