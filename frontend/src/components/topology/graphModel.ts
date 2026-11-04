// Pure graph model behind the topology view: merging backend graphs, which
// nodes are visible given what the user expanded, grouping pods under their
// owner, filtering and search. No React here, so it is unit-tested directly.

export type Health = 'healthy' | 'warning' | 'error' | 'unknown';

export interface TopoNode {
  id: string;
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
  uid?: string;
  health: Health;
  status?: string;
  owner?: string;
  missing?: boolean;
  /** Application from labels or Helm release (backend), when set. */
  app?: string;
}

export interface TopoEdge {
  id: string;
  source: string;
  target: string;
  relation: string;
}

export interface TopoGraph {
  nodes: TopoNode[];
  edges: TopoEdge[];
  focus?: string;
  truncated?: boolean;
  warnings?: string[];
}

/** Everything the client has learned so far, keyed by ID. */
export interface KnownGraph {
  nodes: Map<string, TopoNode>;
  edges: Map<string, TopoEdge>;
  warnings: string[];
  truncated: boolean;
}

export const emptyKnown = (): KnownGraph => ({ nodes: new Map(), edges: new Map(), warnings: [], truncated: false });

/**
 * Folds a fresh response into what is known. Nodes and edges are replaced by
 * their latest version (live status). With `authoritativeFor`, edges touching
 * those node IDs that the response no longer has are dropped, so a pod that
 * went away disappears on the next poll.
 */
export function mergeGraph(known: KnownGraph, g: TopoGraph, authoritativeFor?: Set<string>): KnownGraph {
  const nodes = new Map(known.nodes);
  const edges = new Map(known.edges);
  if (authoritativeFor) {
    const fresh = new Set(g.edges.map((e) => e.id));
    for (const [id, e] of edges) {
      if ((authoritativeFor.has(e.source) || authoritativeFor.has(e.target)) && !fresh.has(id)) edges.delete(id);
    }
  }
  for (const n of g.nodes) nodes.set(n.id, n);
  for (const e of g.edges) edges.set(e.id, e);
  // Drop nodes nothing points at any more, other than the ones the caller
  // was authoritative for (those were in the response or are gone).
  if (authoritativeFor) {
    const inResponse = new Set(g.nodes.map((n) => n.id));
    const touched = new Set<string>();
    for (const e of edges.values()) { touched.add(e.source); touched.add(e.target); }
    for (const id of nodes.keys()) {
      if (!inResponse.has(id) && !touched.has(id) && !authoritativeFor.has(id)) nodes.delete(id);
    }
  }
  return {
    nodes,
    edges,
    warnings: Array.from(new Set([...(g.warnings || [])])),
    truncated: known.truncated || !!g.truncated,
  };
}

/** Replaces the known graph wholesale (namespace mode). */
export function knownFrom(g: TopoGraph): KnownGraph {
  return mergeGraph(emptyKnown(), g);
}

function adjacency(edges: Iterable<TopoEdge>): Map<string, Set<string>> {
  const adj = new Map<string, Set<string>>();
  const add = (a: string, b: string) => {
    let s = adj.get(a);
    if (!s) adj.set(a, (s = new Set()));
    s.add(b);
  };
  for (const e of edges) { add(e.source, e.target); add(e.target, e.source); }
  return adj;
}

/**
 * The nodes shown in focus mode: the focus, plus the neighbours of every
 * expanded node reachable from it. Collapsing a node therefore also hides
 * whatever was only visible through it.
 */
export function visibleFromFocus(known: KnownGraph, focus: string, expanded: Set<string>): Set<string> {
  const adj = adjacency(known.edges.values());
  const visible = new Set<string>([focus]);
  const queue = [focus];
  while (queue.length) {
    const id = queue.shift()!;
    if (!expanded.has(id)) continue;
    for (const n of adj.get(id) || []) {
      if (!visible.has(n) && known.nodes.has(n)) {
        visible.add(n);
        queue.push(n);
      }
    }
  }
  return visible;
}

/** How many known neighbours of `id` are not on screen. */
export function hiddenNeighbourCount(known: KnownGraph, id: string, visible: Set<string>): number {
  let count = 0;
  const seen = new Set<string>();
  for (const e of known.edges.values()) {
    const other = e.source === id ? e.target : e.target === id ? e.source : null;
    if (other && !seen.has(other) && !visible.has(other)) { seen.add(other); count++; }
  }
  return count;
}

export type KindFamily = 'network' | 'workloads' | 'pods' | 'config' | 'storage' | 'policy' | 'nodes';

export const KIND_FAMILY: Record<string, KindFamily> = {
  Gateway: 'network', HTTPRoute: 'network', Ingress: 'network', Service: 'network',
  Deployment: 'workloads', StatefulSet: 'workloads', DaemonSet: 'workloads', ReplicaSet: 'workloads',
  Job: 'workloads', CronJob: 'workloads',
  Pod: 'pods',
  ConfigMap: 'config', Secret: 'config', ServiceAccount: 'config',
  PersistentVolumeClaim: 'storage', PersistentVolume: 'storage', StorageClass: 'storage',
  HorizontalPodAutoscaler: 'policy', PodDisruptionBudget: 'policy', NetworkPolicy: 'policy',
  Node: 'nodes',
};

export const familyOf = (kind: string): KindFamily => KIND_FAMILY[kind] || 'workloads';

/** A node as drawn: a real resource or a group of sibling pods. */
export interface ViewNode {
  id: string;
  node: TopoNode;
  /** Set for a pod group: the pods it stands for. */
  members?: TopoNode[];
  counts?: Record<Health, number>;
}

export interface ViewEdge {
  id: string;
  source: string;
  target: string;
  relation: string;
  /** How many underlying edges this one stands for (pod groups). */
  weight: number;
}

export interface ViewGraph {
  nodes: ViewNode[];
  edges: ViewEdge[];
}

export interface ViewOptions {
  hiddenFamilies?: Set<KindFamily>;
  /** Owner IDs whose pods the user asked to see individually. */
  ungrouped?: Set<string>;
  /** Group pods once an owner has at least this many on screen. */
  groupThreshold?: number;
  /** Never hide this node (the focus). */
  pinned?: string;
}

export const groupIdFor = (ownerId: string) => `group:${ownerId}`;

const worstFirst: Health[] = ['error', 'warning', 'unknown', 'healthy'];

export function worstHealth(counts: Record<Health, number>): Health {
  return worstFirst.find((h) => counts[h] > 0) || 'unknown';
}

/** Builds what is drawn from the visible node IDs. */
export function buildView(known: KnownGraph, visible: Iterable<string>, opts: ViewOptions = {}): ViewGraph {
  const hidden = opts.hiddenFamilies || new Set<KindFamily>();
  const threshold = opts.groupThreshold ?? 2;
  const shown = new Set<string>();
  for (const id of visible) {
    const n = known.nodes.get(id);
    if (!n) continue;
    if (id !== opts.pinned && hidden.has(familyOf(n.kind))) continue;
    shown.add(id);
  }

  // Pods grouped per visible owner.
  const byOwner = new Map<string, TopoNode[]>();
  for (const id of shown) {
    const n = known.nodes.get(id)!;
    if (n.kind !== 'Pod' || !n.owner || !shown.has(n.owner) || opts.ungrouped?.has(n.owner) || id === opts.pinned) continue;
    const list = byOwner.get(n.owner) || [];
    list.push(n);
    byOwner.set(n.owner, list);
  }
  const alias = new Map<string, string>();
  const nodes: ViewNode[] = [];
  for (const [owner, pods] of byOwner) {
    if (pods.length < threshold) continue;
    const gid = groupIdFor(owner);
    const counts: Record<Health, number> = { healthy: 0, warning: 0, error: 0, unknown: 0 };
    for (const p of pods) { counts[p.health]++; alias.set(p.id, gid); }
    const ownerNode = known.nodes.get(owner)!;
    pods.sort((a, b) => a.name.localeCompare(b.name));
    nodes.push({
      id: gid,
      node: {
        id: gid, group: '', version: 'v1', kind: 'Pod', namespace: ownerNode.namespace,
        name: `${pods.length} pods`, health: worstHealth(counts), owner,
        status: summarizeCounts(counts),
      },
      members: pods,
      counts,
    });
  }
  for (const id of shown) {
    if (alias.has(id)) continue;
    nodes.push({ id, node: known.nodes.get(id)! });
  }

  const edges = new Map<string, ViewEdge>();
  for (const e of known.edges.values()) {
    if (!shown.has(e.source) || !shown.has(e.target)) continue;
    const s = alias.get(e.source) || e.source;
    const t = alias.get(e.target) || e.target;
    if (s === t) continue;
    const id = `${s}|${e.relation}|${t}`;
    const prev = edges.get(id);
    if (prev) prev.weight++;
    else edges.set(id, { id, source: s, target: t, relation: e.relation, weight: 1 });
  }
  nodes.sort((a, b) => a.id.localeCompare(b.id));
  return { nodes, edges: Array.from(edges.values()).sort((a, b) => a.id.localeCompare(b.id)) };
}

export function summarizeCounts(c: Record<Health, number>): string {
  const parts: string[] = [];
  if (c.healthy) parts.push(`${c.healthy} ready`);
  if (c.warning) parts.push(`${c.warning} pending`);
  if (c.error) parts.push(`${c.error} failing`);
  if (c.unknown) parts.push(`${c.unknown} unknown`);
  return parts.join(' · ');
}

/** View-node IDs whose resource (or any grouped member) matches `query`. */
export function searchMatches(view: ViewGraph, query: string): string[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const hit = (n: TopoNode) => n.name.toLowerCase().includes(q) || n.kind.toLowerCase() === q;
  return view.nodes.filter((v) => (v.members ? v.members.some(hit) : hit(v.node))).map((v) => v.id);
}

/** Human label for a relation, as drawn on edges. */
export const RELATION_LABEL: Record<string, string> = {
  owns: 'owns',
  selects: 'selects',
  exposes: 'exposes',
  routes: 'routes to',
  attaches: 'attaches',
  mounts: 'mounts',
  env: 'env from',
  'runs-as': 'runs as',
  tls: 'TLS',
  binds: 'binds',
  class: 'class',
  scales: 'scales',
  protects: 'protects',
  'runs-on': 'runs on',
  'applies-to': 'applies to',
  'pulls-with': 'pull secret',
};

/** Parses a node ID ("Kind/namespace/name") back into its parts. */
export function parseNodeId(id: string): { kind: string; namespace: string; name: string } {
  const [kind, namespace, ...rest] = id.split('/');
  return { kind, namespace, name: rest.join('/') };
}

export const nodeIdFor = (kind: string, namespace: string | undefined, name: string) => `${kind}/${namespace || ''}/${name}`;
