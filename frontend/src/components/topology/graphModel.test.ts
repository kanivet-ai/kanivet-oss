import { describe, expect, it } from 'vitest';
import {
  buildView,
  groupIdFor,
  hiddenNeighbourCount,
  knownFrom,
  mergeGraph,
  searchMatches,
  visibleFromFocus,
  type TopoEdge,
  type TopoGraph,
  type TopoNode,
} from './graphModel';

const n = (kind: string, name: string, extra: Partial<TopoNode> = {}): TopoNode => ({
  id: `${kind}/ns/${name}`, group: '', version: 'v1', kind, namespace: 'ns', name, health: 'healthy', ...extra,
});
const e = (s: TopoNode, rel: string, t: TopoNode): TopoEdge => ({ id: `${s.id}|${rel}|${t.id}`, source: s.id, target: t.id, relation: rel });

const svc = n('Service', 'web');
const dep = n('Deployment', 'web', { group: 'apps' });
const rs = n('ReplicaSet', 'web-1', { owner: dep.id });
const pods = [1, 2, 3].map((i) => n('Pod', `web-1-${i}`, { owner: rs.id, health: i === 3 ? 'error' : 'healthy' }));
const cm = n('ConfigMap', 'cfg');

const full: TopoGraph = {
  nodes: [svc, dep, rs, cm, ...pods],
  edges: [e(svc, 'exposes', dep), e(dep, 'owns', rs), e(dep, 'mounts', cm), ...pods.map((p) => e(rs, 'owns', p)), ...pods.map((p) => e(svc, 'selects', p))],
};

describe('visibleFromFocus', () => {
  it('shows the focus and the neighbours of expanded nodes only', () => {
    const k = knownFrom(full);
    expect([...visibleFromFocus(k, dep.id, new Set([dep.id]))].sort()).toEqual([cm.id, dep.id, rs.id, svc.id].sort());
    const deeper = visibleFromFocus(k, dep.id, new Set([dep.id, rs.id]));
    expect(deeper.has(pods[0].id)).toBe(true);
  });

  it('collapsing a node hides what was reachable only through it', () => {
    const k = knownFrom(full);
    const vis = visibleFromFocus(k, dep.id, new Set([dep.id, rs.id]));
    expect(vis.has(pods[0].id)).toBe(true);
    // rs collapsed: pods only reachable through rs (and svc, which is not expanded)
    const after = visibleFromFocus(k, dep.id, new Set([dep.id]));
    expect(after.has(pods[0].id)).toBe(false);
    // expanded but no longer reachable nodes don't leak
    const orphan = visibleFromFocus(k, dep.id, new Set([rs.id]));
    expect([...orphan]).toEqual([dep.id]);
  });

  it('counts hidden neighbours for the + badge', () => {
    const k = knownFrom(full);
    const vis = visibleFromFocus(k, dep.id, new Set([dep.id]));
    expect(hiddenNeighbourCount(k, rs.id, vis)).toBe(3);
    expect(hiddenNeighbourCount(k, dep.id, vis)).toBe(0);
  });
});

describe('buildView', () => {
  it('groups sibling pods under their owner and merges their edges', () => {
    const k = knownFrom(full);
    const v = buildView(k, k.nodes.keys());
    const g = v.nodes.find((x) => x.id === groupIdFor(rs.id))!;
    expect(g.members).toHaveLength(3);
    expect(g.counts).toEqual({ healthy: 2, warning: 0, error: 1, unknown: 0 });
    expect(g.node.health).toBe('error');
    expect(v.nodes.some((x) => x.id === pods[0].id)).toBe(false);
    const sel = v.edges.find((x) => x.source === svc.id && x.target === g.id && x.relation === 'selects')!;
    expect(sel.weight).toBe(3);
    const owns = v.edges.filter((x) => x.target === g.id && x.relation === 'owns');
    expect(owns).toHaveLength(1);
  });

  it('shows pods individually when ungrouped or below the threshold', () => {
    const k = knownFrom(full);
    const v = buildView(k, k.nodes.keys(), { ungrouped: new Set([rs.id]) });
    expect(v.nodes.filter((x) => x.node.kind === 'Pod')).toHaveLength(3);
    const v2 = buildView(k, k.nodes.keys(), { groupThreshold: 4 });
    expect(v2.nodes.some((x) => x.members)).toBe(false);
  });

  it('hides filtered families but never the pinned focus', () => {
    const k = knownFrom(full);
    const v = buildView(k, k.nodes.keys(), { hiddenFamilies: new Set(['config', 'network']), pinned: svc.id });
    expect(v.nodes.some((x) => x.id === cm.id)).toBe(false);
    expect(v.nodes.some((x) => x.id === svc.id)).toBe(true);
  });
});

describe('mergeGraph', () => {
  it('drops edges and nodes that a fresh response for a node no longer has', () => {
    let k = knownFrom(full);
    const fresh: TopoGraph = {
      nodes: [rs, pods[0], pods[1], dep],
      edges: [e(dep, 'owns', rs), e(rs, 'owns', pods[0]), e(rs, 'owns', pods[1])],
    };
    k = mergeGraph(k, fresh, new Set([rs.id]));
    expect(k.edges.has(e(rs, 'owns', pods[2]).id)).toBe(false);
    // pod 3 is still selected by the service, so it stays known
    expect(k.nodes.has(pods[2].id)).toBe(true);
    const fresh2: TopoGraph = { nodes: [svc, dep, pods[0], pods[1]], edges: [e(svc, 'exposes', dep), e(svc, 'selects', pods[0]), e(svc, 'selects', pods[1])] };
    k = mergeGraph(k, fresh2, new Set([svc.id]));
    expect(k.nodes.has(pods[2].id)).toBe(false);
  });

  it('updates node status in place', () => {
    const k = mergeGraph(knownFrom(full), { nodes: [{ ...dep, health: 'warning', status: '1/2 ready' }], edges: [] });
    expect(k.nodes.get(dep.id)!.status).toBe('1/2 ready');
    expect(k.edges.size).toBe(full.edges.length);
  });
});

describe('searchMatches', () => {
  it('matches names, kinds, and grouped members', () => {
    const k = knownFrom(full);
    const v = buildView(k, k.nodes.keys());
    expect(searchMatches(v, 'web-1-2')).toEqual([groupIdFor(rs.id)]);
    expect(searchMatches(v, 'configmap')).toEqual([cm.id]);
    expect(searchMatches(v, '  ')).toEqual([]);
  });
});
