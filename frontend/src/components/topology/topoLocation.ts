// Where the Topology page is looking, and the back/forward history of it.
// Pure; the page keeps one of these in state.
import type { TopologyRef } from '../../services/api/topology';
import { nodeIdFor } from './graphModel';

export type TopoLocation =
  | { view: 'cluster' }
  | { view: 'app'; namespace: string }
  | { view: 'focus'; ref: TopologyRef; namespace: string };

export interface TopoHistory {
  past: TopoLocation[];
  present: TopoLocation;
  future: TopoLocation[];
}

export const locationKey = (l: TopoLocation): string =>
  l.view === 'cluster' ? 'cluster' : l.view === 'app' ? `app:${l.namespace}` : `focus:${nodeIdFor(l.ref.kind, l.ref.namespace, l.ref.name)}`;

export const historyAt = (present: TopoLocation): TopoHistory => ({ past: [], present, future: [] });

/** Goes somewhere new; a no-op when already there. Keeps the last 50 steps. */
export function navigate(h: TopoHistory, to: TopoLocation): TopoHistory {
  if (locationKey(to) === locationKey(h.present)) return h;
  return { past: [...h.past, h.present].slice(-50), present: to, future: [] };
}

export function back(h: TopoHistory): TopoHistory {
  if (!h.past.length) return h;
  return { past: h.past.slice(0, -1), present: h.past[h.past.length - 1], future: [h.present, ...h.future] };
}

export function forward(h: TopoHistory): TopoHistory {
  if (!h.future.length) return h;
  return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1) };
}

/** The page's default: the current namespace's applications, else the cluster. */
export function defaultLocation(opts: { focus?: TopologyRef; namespace?: string; selectedNamespace?: string }): TopoLocation {
  if (opts.focus) return { view: 'focus', ref: opts.focus, namespace: opts.focus.namespace || opts.namespace || opts.selectedNamespace || 'default' };
  const ns = opts.namespace || (opts.selectedNamespace && opts.selectedNamespace !== 'all' ? opts.selectedNamespace : '');
  return ns ? { view: 'app', namespace: ns } : { view: 'cluster' };
}

export interface Crumb {
  label: string;
  kind?: string;
  to?: TopoLocation;
}

/** cluster › namespace › Kind name; every part but the last is a link. */
export function breadcrumb(cluster: string, l: TopoLocation): Crumb[] {
  const out: Crumb[] = [{ label: shortCluster(cluster), kind: 'Cluster', to: { view: 'cluster' } }];
  if (l.view === 'app') out.push({ label: l.namespace, kind: 'Namespace' });
  if (l.view === 'focus') {
    if (l.ref.namespace || l.namespace) {
      const ns = l.ref.namespace || l.namespace;
      out.push({ label: ns, kind: 'Namespace', to: { view: 'app', namespace: ns } });
    }
    out.push({ label: l.ref.name, kind: l.ref.kind });
  }
  if (l.view === 'cluster') delete out[0].to;
  return out;
}

/** The readable part of a cluster ID (an EKS ARN ends in cluster/<name>). */
export function shortCluster(cluster: string): string {
  const i = cluster.lastIndexOf('/');
  return i >= 0 && i < cluster.length - 1 ? cluster.slice(i + 1) : cluster;
}
