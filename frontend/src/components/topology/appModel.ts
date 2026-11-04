// The Application view model: which application each drawn node belongs to
// (lanes), which small config/storage/policy nodes ride along under their
// workload (satellites) instead of joining the main flow, and which edges
// are redundant once that is done. Pure, so it is unit-tested directly.
import { worstHealth, type Health, type KnownGraph, type ViewEdge, type ViewGraph, type ViewNode } from './graphModel';

/** Kinds drawn as small chips under the workload that uses them. */
export const SATELLITE_KINDS = new Set([
  'ConfigMap', 'Secret', 'ServiceAccount', 'PersistentVolumeClaim',
  'HorizontalPodAutoscaler', 'PodDisruptionBudget', 'NetworkPolicy',
]);

const HOST_KINDS = new Set(['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod']);

export const OTHER_LANE = 'other';

export interface AppLane {
  id: string;
  label: string;
  /** How the lane was found: an app label / Helm release, an owner chain, or leftovers. */
  source: 'app' | 'owner' | 'other';
  nodeIds: string[];
  health: Health;
  counts: Record<Health, number>;
}

export interface AppView {
  lanes: AppLane[];
  /** Satellite view-node ID -> the view node it hangs under. */
  hostOf: Map<string, string>;
  /** Host view-node ID -> its satellites, in display order. */
  satellitesOf: Map<string, string[]>;
  /** The edges still drawn. */
  edges: ViewEdge[];
  /** The view as drawn: a Deployment's only ReplicaSet folded into it. */
  view: ViewGraph;
  /** Folded node ID -> the node now drawing it. */
  folded: Map<string, string>;
}

/** Follows controller owners to the top (ReplicaSet -> Deployment). */
export function topOwner(known: KnownGraph, id: string): string {
  let cur = id;
  for (let i = 0; i < 8; i++) {
    const n = known.nodes.get(cur);
    if (!n?.owner || !known.nodes.has(n.owner)) break;
    cur = n.owner;
  }
  return cur;
}

/** The owner chain of a node, itself first. */
function ownerChain(known: KnownGraph, id: string): string[] {
  const out = [id];
  let cur = id;
  for (let i = 0; i < 8; i++) {
    const o = known.nodes.get(cur)?.owner;
    if (!o || out.includes(o)) break;
    out.push(o);
    cur = o;
  }
  return out;
}

const SAT_OUTGOING = new Set(['scales', 'protects', 'applies-to']);

/**
 * Groups the drawn nodes into application lanes. A node's lane is, in order:
 * its own app (labels / Helm release), the app of its top owner, its top
 * owner; a Service takes the lane of what it exposes, an Ingress or route the
 * lane of its Service, a Gateway that of its route, a satellite that of its
 * host. Whatever is left goes to "Other".
 */
/**
 * Config nothing refers to (Helm release records, kube-root-ca.crt, the
 * default ServiceAccount): noise in an application view, hidden by default.
 */
export function unreferencedConfig(known: KnownGraph): Set<string> {
  const touched = new Set<string>();
  for (const e of known.edges.values()) { touched.add(e.source); touched.add(e.target); }
  const out = new Set<string>();
  for (const n of known.nodes.values()) {
    if (SATELLITE_KINDS.has(n.kind) && !touched.has(n.id)) out.add(n.id);
  }
  return out;
}

/**
 * Folds a Deployment's single drawn ReplicaSet into the Deployment, so the
 * flow reads Service -> Deployment -> pods. With several (a rollout under
 * way, old revisions shown) they stay, since then they say something.
 */
export function foldReplicaSets(known: KnownGraph, view: ViewGraph): { view: ViewGraph; folded: Map<string, string> } {
  const ids = new Set(view.nodes.map((v) => v.id));
  const rsOf = new Map<string, string[]>();
  for (const v of view.nodes) {
    if (v.node.kind !== 'ReplicaSet' || v.members) continue;
    const owner = v.node.owner;
    if (!owner || !ids.has(owner) || known.nodes.get(owner)?.kind !== 'Deployment') continue;
    (rsOf.get(owner) || rsOf.set(owner, []).get(owner)!).push(v.id);
  }
  const folded = new Map<string, string>();
  for (const [dep, list] of rsOf) if (list.length === 1) folded.set(list[0], dep);
  if (!folded.size) return { view, folded };
  const edges = new Map<string, ViewEdge>();
  for (const e of view.edges) {
    const source = folded.get(e.source) || e.source;
    const target = folded.get(e.target) || e.target;
    if (source === target) continue;
    const id = `${source}|${e.relation}|${target}`;
    const prev = edges.get(id);
    if (prev) prev.weight += e.weight;
    else edges.set(id, { ...e, id, source, target });
  }
  return {
    view: { nodes: view.nodes.filter((v) => !folded.has(v.id)), edges: [...edges.values()].sort((a, b) => a.id.localeCompare(b.id)) },
    folded,
  };
}

export function buildAppView(known: KnownGraph, fullView: ViewGraph): AppView {
  const { view, folded } = foldReplicaSets(known, fullView);
  const byId = new Map(view.nodes.map((v) => [v.id, v]));
  // Underlying resource ID -> the view node drawing it (pods fold into groups).
  const drawnAs = new Map<string, string>();
  for (const v of view.nodes) {
    drawnAs.set(v.id, v.id);
    for (const m of v.members || []) drawnAs.set(m.id, v.id);
  }
  for (const [from, to] of folded) drawnAs.set(from, to);
  const resolve = (id: string) => drawnAs.get(id);

  const laneOf = new Map<string, string>();
  const labels = new Map<string, { label: string; source: AppLane['source'] }>();
  const setLane = (vid: string, key: string, label: string, source: AppLane['source']) => {
    laneOf.set(vid, key);
    if (!labels.has(key)) labels.set(key, { label, source });
  };

  const ownKey = (v: ViewNode): [string, string, AppLane['source']] | null => {
    const n = v.members ? known.nodes.get(v.node.owner!) || v.node : v.node;
    if (n.app) return [`app:${n.app}`, n.app, 'app'];
    if (!HOST_KINDS.has(n.kind)) return null;
    const top = known.nodes.get(topOwner(known, n.id)) || n;
    if (top.app) return [`app:${top.app}`, top.app, 'app'];
    return [`owner:${top.id}`, top.name, 'owner'];
  };

  // 1. Everything that names its app, and workloads by their owner chain.
  for (const v of view.nodes) {
    if (SATELLITE_KINDS.has(v.node.kind) && !v.node.app) continue;
    const k = ownKey(v);
    if (k) setLane(v.id, ...k);
  }

  // 2. Entry points follow what they lead to: Service -> workload, route ->
  //    Service, Gateway -> route. Two passes settle the chain.
  const outgoing = new Map<string, ViewEdge[]>();
  const incoming = new Map<string, ViewEdge[]>();
  for (const e of view.edges) {
    (outgoing.get(e.source) || outgoing.set(e.source, []).get(e.source)!).push(e);
    (incoming.get(e.target) || incoming.set(e.target, []).get(e.target)!).push(e);
  }
  for (let pass = 0; pass < 3; pass++) {
    for (const v of view.nodes) {
      if (laneOf.has(v.id) || SATELLITE_KINDS.has(v.node.kind)) continue;
      const next = (outgoing.get(v.id) || [])
        .filter((e) => e.relation !== 'runs-on')
        .map((e) => laneOf.get(e.target))
        .find(Boolean);
      if (next) laneOf.set(v.id, next);
    }
  }

  // 3. Satellites hang under the workload (top of the owner chain) using them.
  const hostOf = new Map<string, string>();
  const hostRank = (vid: string) => {
    const k = byId.get(vid)?.node.kind || '';
    return k === 'Pod' ? 2 : k === 'ReplicaSet' ? 1 : 0;
  };
  for (const v of view.nodes) {
    if (!SATELLITE_KINDS.has(v.node.kind)) continue;
    const candidates = new Set<string>();
    const consider = (otherId: string) => {
      const ov = resolve(otherId);
      if (!ov || ov === v.id) return;
      const ovNode = byId.get(ov);
      if (!ovNode || !HOST_KINDS.has(ovNode.node.kind)) return;
      // Prefer the top drawn owner: a ConfigMap used by pods of a
      // Deployment hangs under the Deployment.
      const chain = ownerChain(known, ovNode.members ? ovNode.node.owner! : ov);
      const top = [...chain].reverse().map(resolve).find((id) => id && byId.has(id) && HOST_KINDS.has(byId.get(id)!.node.kind));
      candidates.add(top || ov);
    };
    for (const e of known.edges.values()) {
      if (e.target === v.node.id && !SAT_OUTGOING.has(e.relation)) consider(e.source);
      if (e.source === v.node.id && SAT_OUTGOING.has(e.relation)) consider(e.target);
    }
    const host = [...candidates].sort((a, b) => hostRank(a) - hostRank(b) || a.localeCompare(b))[0];
    if (host && !SATELLITE_KINDS.has(byId.get(host)!.node.kind)) hostOf.set(v.id, host);
  }
  // A satellite always rides with its host, whatever its own labels say
  // (a chart's ConfigMap is often labelled with the chart, not the app).
  for (const [sat, host] of hostOf) {
    const hl = laneOf.get(host);
    if (hl) laneOf.set(sat, hl);
  }

  // Whatever only something else points at (a PV, a StorageClass) joins that.
  for (const v of view.nodes) {
    if (laneOf.has(v.id)) continue;
    const prev = (incoming.get(v.id) || []).map((e) => laneOf.get(e.source)).find(Boolean);
    if (prev) laneOf.set(v.id, prev);
  }

  // 4. Lanes, healthiest last so trouble is on top; "Other" always last.
  const lanes = new Map<string, AppLane>();
  for (const v of view.nodes) {
    const key = laneOf.get(v.id) || OTHER_LANE;
    let lane = lanes.get(key);
    if (!lane) {
      const meta = labels.get(key) || { label: 'Other resources', source: 'other' as const };
      lane = { id: key, label: meta.label, source: key === OTHER_LANE ? 'other' : meta.source, nodeIds: [], health: 'unknown', counts: { healthy: 0, warning: 0, error: 0, unknown: 0 } };
      lanes.set(key, lane);
    }
    lane.nodeIds.push(v.id);
    if (hostOf.has(v.id)) continue; // satellites don't vote on lane health
    if (v.counts) for (const h of Object.keys(v.counts) as Health[]) lane.counts[h] += v.counts[h];
    else if (!v.node.missing) lane.counts[v.node.health]++;
  }
  for (const lane of lanes.values()) lane.health = worstHealth(lane.counts);
  const severity: Record<Health, number> = { error: 0, warning: 1, unknown: 2, healthy: 2 };
  const ordered = [...lanes.values()].sort((a, b) => {
    if ((a.id === OTHER_LANE) !== (b.id === OTHER_LANE)) return a.id === OTHER_LANE ? 1 : -1;
    return severity[a.health] - severity[b.health] || a.label.localeCompare(b.label);
  });

  const satellitesOf = new Map<string, string[]>();
  for (const [sat, host] of hostOf) {
    if (laneOf.get(sat) !== laneOf.get(host)) { hostOf.delete(sat); continue; }
    (satellitesOf.get(host) || satellitesOf.set(host, []).get(host)!).push(sat);
  }
  const kindOrder = [...SATELLITE_KINDS];
  for (const list of satellitesOf.values()) {
    list.sort((a, b) => {
      const ka = kindOrder.indexOf(byId.get(a)!.node.kind);
      const kb = kindOrder.indexOf(byId.get(b)!.node.kind);
      return ka - kb || byId.get(a)!.node.name.localeCompare(byId.get(b)!.node.name);
    });
  }

  return { lanes: ordered, hostOf, satellitesOf, edges: pruneEdges(known, view, hostOf), view, folded };
}

/**
 * One clean line per relation: drops edges into satellites (their place shows
 * the relation) and a Service's per-pod "selects" lines when
 * it already "exposes" the workload owning those pods.
 */
export function pruneEdges(known: KnownGraph, view: ViewGraph, hostOf: Map<string, string>): ViewEdge[] {
  const byId = new Map(view.nodes.map((v) => [v.id, v]));
  const exposes = new Map<string, Set<string>>();
  for (const e of view.edges) {
    if (e.relation !== 'exposes') continue;
    (exposes.get(e.source) || exposes.set(e.source, new Set()).get(e.source)!).add(e.target);
  }
  return view.edges.filter((e) => {
    if (hostOf.has(e.source) || hostOf.has(e.target)) return false;
    if (e.relation === 'selects') {
      const exposed = exposes.get(e.source);
      if (exposed) {
        const t = byId.get(e.target);
        const start = t?.members ? t.node.owner! : e.target;
        if (ownerChain(known, start).some((id) => exposed.has(id))) return false;
      }
    }
    return true;
  });
}

/** Drawn-node count of a lane's main flow (satellites excluded). */
export const laneSize = (lane: AppLane, app: AppView) => lane.nodeIds.filter((id) => !app.hostOf.has(id)).length;
