import { describe, expect, it } from 'vitest';
import { buildView, knownFrom, groupIdFor, type TopoEdge, type TopoGraph, type TopoNode } from './graphModel';
import { buildAppView, OTHER_LANE } from './appModel';
import { buildLayoutInput, hostHeight, layoutGraph, moreIdFor, NODE_H, SAT_MAX_ROWS } from './layout';

const n = (kind: string, name: string, extra: Partial<TopoNode> = {}): TopoNode => ({
  id: `${kind}/ns/${name}`, group: '', version: 'v1', kind, namespace: 'ns', name, health: 'healthy', ...extra,
});
const e = (s: TopoNode, rel: string, t: TopoNode): TopoEdge => ({ id: `${s.id}|${rel}|${t.id}`, source: s.id, target: t.id, relation: rel });

// shop: Ingress -> Service -> Deployment -> RS -> 3 pods, with config.
const ing = n('Ingress', 'shop');
const svc = n('Service', 'shop');
const dep = n('Deployment', 'shop', { app: 'shop' });
const rs = n('ReplicaSet', 'shop-1', { owner: dep.id, app: 'shop' });
const pods = [1, 2, 3].map((i) => n('Pod', `shop-1-${i}`, { owner: rs.id, app: 'shop', health: i === 3 ? 'error' : 'healthy' }));
const cm = n('ConfigMap', 'shop-cfg');
const sa = n('ServiceAccount', 'shop-sa');
const hpa = n('HorizontalPodAutoscaler', 'shop');
// worker: no labels at all, grouped by owner.
const job = n('Job', 'batch');
const jpod = n('Pod', 'batch-x', { owner: job.id });
// stray: nothing relates it.
const lone = n('ConfigMap', 'kube-root-ca.crt');

const graph: TopoGraph = {
  nodes: [ing, svc, dep, rs, ...pods, cm, sa, hpa, job, jpod, lone],
  edges: [
    e(ing, 'routes', svc), e(svc, 'exposes', dep), e(dep, 'owns', rs),
    ...pods.map((p) => e(rs, 'owns', p)), ...pods.map((p) => e(svc, 'selects', p)),
    e(dep, 'mounts', cm), e(dep, 'runs-as', sa), e(hpa, 'scales', dep),
    e(job, 'owns', jpod),
  ],
};

const known = knownFrom(graph);
const view = buildView(known, known.nodes.keys());
const app = buildAppView(known, view);
const lane = (id: string) => app.lanes.find((l) => l.nodeIds.includes(id));

describe('buildAppView', () => {
  it('groups by app label and carries entry points along the flow', () => {
    const shop = lane(dep.id)!;
    expect(shop.id).toBe('app:shop');
    expect(shop.source).toBe('app');
    for (const id of [ing.id, svc.id, groupIdFor(rs.id), cm.id, sa.id, hpa.id]) expect(lane(id)?.id).toBe('app:shop');
  });

  it('falls back to the top owner without labels', () => {
    expect(lane(job.id)?.id).toBe(`owner:${job.id}`);
    expect(lane(jpod.id)?.id).toBe(`owner:${job.id}`);
    expect(lane(job.id)?.label).toBe('batch');
  });

  it('puts unrelated resources in Other, last', () => {
    expect(lane(lone.id)?.id).toBe(OTHER_LANE);
    expect(app.lanes[app.lanes.length - 1].id).toBe(OTHER_LANE);
  });

  it('rolls health up and sorts failing apps first', () => {
    expect(lane(dep.id)!.health).toBe('error');
    expect(app.lanes[0].id).toBe('app:shop');
  });

  it('hangs config, identity and scaling under the workload', () => {
    expect(app.hostOf.get(cm.id)).toBe(dep.id);
    expect(app.hostOf.get(sa.id)).toBe(dep.id);
    expect(app.hostOf.get(hpa.id)).toBe(dep.id);
    expect(app.satellitesOf.get(dep.id)).toEqual([cm.id, sa.id, hpa.id]);
    expect(app.hostOf.has(lone.id)).toBe(false);
  });

  it('draws one line per relation', () => {
    const rels = app.edges.map((x) => `${x.source}>${x.relation}>${x.target}`);
    // selects to the pods is implied by exposes -> Deployment -> RS -> pods
    expect(rels.some((r) => r.includes('>selects>'))).toBe(false);
    // no satellite edges
    expect(app.edges.some((x) => x.source === hpa.id || x.target === cm.id || x.target === sa.id)).toBe(false);
    expect(rels).toContain(`${ing.id}>routes>${svc.id}`);
    expect(rels).toContain(`${svc.id}>exposes>${dep.id}`);
    expect(rels).toContain(`${dep.id}>owns>${groupIdFor(rs.id)}`);
    expect(new Set(app.edges.map((x) => x.id)).size).toBe(app.edges.length);
  });

  it('folds the only ReplicaSet into it, but not a rollout', () => {
    expect(app.folded.get(rs.id)).toBe(dep.id);
    expect(app.view.nodes.some((v) => v.id === rs.id)).toBe(false);
    const old = n('ReplicaSet', 'shop-0', { owner: dep.id, app: 'shop' });
    const k = knownFrom({ nodes: [...graph.nodes, old], edges: [...graph.edges, e(dep, 'owns', old)] });
    const a = buildAppView(k, buildView(k, k.nodes.keys()));
    expect(a.folded.size).toBe(0);
    expect(a.view.nodes.some((v) => v.id === old.id)).toBe(true);
  });

  it('keeps selects when the Service exposes nothing', () => {
    const p = n('Pod', 'bare', { app: 'bare' });
    const s = n('Service', 'bare');
    const k = knownFrom({ nodes: [p, s], edges: [e(s, 'selects', p)] });
    const a = buildAppView(k, buildView(k, k.nodes.keys()));
    expect(a.edges.map((x) => x.relation)).toEqual(['selects']);
    expect(a.lanes).toHaveLength(1);
    expect(a.lanes[0].nodeIds.sort()).toEqual([p.id, s.id].sort());
  });
});

describe('buildLayoutInput', () => {
  it('is one unframed lane without an app view', () => {
    const input = buildLayoutInput(view);
    expect(input.lanes).toHaveLength(1);
    expect(input.lanes[0].framed).toBe(false);
    expect(input.lanes[0].nodes).toHaveLength(view.nodes.length);
  });

  it('makes framed lanes, keeps satellites out of the ranking, and grows hosts', () => {
    const input = buildLayoutInput(view, app);
    const shop = input.lanes.find((l) => l.id === 'app:shop')!;
    expect(shop.framed).toBe(true);
    const ids = shop.nodes.map((x) => x.id);
    expect(ids).not.toContain(cm.id);
    expect(shop.nodes.find((x) => x.id === dep.id)!.height).toBe(hostHeight(3));
    expect(shop.nodes.find((x) => x.id === svc.id)!.height).toBe(NODE_H);
    expect(shop.satellites.get(dep.id)).toEqual([cm.id, sa.id, hpa.id]);
    // only intra-lane edges steer the layout
    expect(shop.edges.every((x) => ids.includes(x.source) && ids.includes(x.target))).toBe(true);
  });

  it('folds too many satellites into a "+N" chip', () => {
    const d = n('Deployment', 'many', { app: 'many' });
    const cms = Array.from({ length: 9 }, (_, i) => n('ConfigMap', `c${i}`));
    const k = knownFrom({ nodes: [d, ...cms], edges: cms.map((c) => e(d, 'mounts', c)) });
    const a = buildAppView(k, buildView(k, k.nodes.keys()));
    const input = buildLayoutInput(buildView(k, k.nodes.keys()), a);
    const l = input.lanes[0];
    expect(l.satellites.get(d.id)).toHaveLength(SAT_MAX_ROWS - 1);
    expect(l.overflow.get(d.id)).toBe(9 - (SAT_MAX_ROWS - 1));
    const out = layoutGraph(input);
    expect(out.positions.has(moreIdFor(d.id))).toBe(true);
    expect(out.overflow.get(d.id)).toBe(9 - (SAT_MAX_ROWS - 1));
  });

  it('lays out left to right and keeps lanes apart', () => {
    const out = layoutGraph(buildLayoutInput(view, app));
    const p = out.positions;
    expect(p.get(ing.id)!.x).toBeLessThan(p.get(svc.id)!.x);
    expect(p.get(svc.id)!.x).toBeLessThan(p.get(dep.id)!.x);
    expect(p.get(dep.id)!.x).toBeLessThan(p.get(groupIdFor(rs.id))!.x);
    expect(p.has(rs.id)).toBe(false);
    // satellite directly under its host
    expect(p.get(cm.id)!.y).toBeGreaterThan(p.get(dep.id)!.y + NODE_H - 1);
    // lanes never overlap
    for (const a of out.lanes) for (const b of out.lanes) {
      if (a === b) continue;
      const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
      expect(overlap).toBe(false);
    }
    // every main node sits inside its lane
    const shop = out.lanes.find((l) => l.id === 'app:shop')!;
    const d = p.get(dep.id)!;
    expect(d.x).toBeGreaterThanOrEqual(shop.x);
    expect(d.y).toBeGreaterThanOrEqual(shop.y);
  });
});
