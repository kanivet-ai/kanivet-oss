import type { HopStatus, NetRef, Target, Verdict } from '../../types/network';

/** Short spellings people type in the palette. */
const KIND_ALIASES: Record<string, string> = {
  pod: 'Pod', pods: 'Pod', po: 'Pod',
  deploy: 'Deployment', deployment: 'Deployment', deployments: 'Deployment',
  sts: 'StatefulSet', statefulset: 'StatefulSet', statefulsets: 'StatefulSet',
  ds: 'DaemonSet', daemonset: 'DaemonSet', daemonsets: 'DaemonSet',
  rs: 'ReplicaSet', replicaset: 'ReplicaSet',
  job: 'Job', jobs: 'Job',
  svc: 'Service', service: 'Service', services: 'Service',
  ip: 'IP',
};

export const normalizeKind = (k: string): string => KIND_ALIASES[k.toLowerCase()] || k;

export function refKey(r: NetRef | null | undefined): string {
  if (!r) return '';
  if (r.kind === 'IP') return `IP/${r.ip || ''}`;
  return `${r.kind}/${r.namespace || ''}/${r.name || ''}`;
}

export function refLabel(r: NetRef): string {
  if (r.kind === 'IP') return r.ip || '';
  return `${r.namespace}/${r.name}`;
}

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
const looksLikeIP = (s: string) => IPV4.test(s) || (s.includes(':') && /^[0-9a-f:]+$/i.test(s) && s.split(':').length > 2);

/** A loosely typed endpoint: `kind/ns/name`, `ns/name`, `name` or an IP, with an optional `:port`. */
export interface ParsedEndpoint {
  kind?: string;
  namespace?: string;
  name: string;
  ip?: string;
  port?: number;
}

export function parseEndpoint(raw: string): ParsedEndpoint | null {
  let s = raw.trim().replace(/[?.,]+$/, '');
  if (!s) return null;
  if (looksLikeIP(s)) return { name: s, ip: s, kind: 'IP' };
  let port: number | undefined;
  const portMatch = s.match(/^(.*?):(\d{1,5})$/);
  if (portMatch) {
    s = portMatch[1];
    port = Number(portMatch[2]);
    if (looksLikeIP(s)) return { name: s, ip: s, kind: 'IP', port };
  }
  const parts = s.split('/').filter(Boolean);
  if (parts.length === 0) return null;
  if (parts.length >= 3) return { kind: normalizeKind(parts[0]), namespace: parts[1], name: parts.slice(2).join('/'), port };
  if (parts.length === 2) {
    if (KIND_ALIASES[parts[0].toLowerCase()]) return { kind: normalizeKind(parts[0]), name: parts[1], port };
    return { namespace: parts[0], name: parts[1], port };
  }
  // "name.namespace" (Service DNS style)
  const dns = parts[0].match(/^([a-z0-9-]+)\.([a-z0-9-]+)(\.svc(\.cluster\.local)?)?$/);
  if (dns) return { kind: 'Service', namespace: dns[2], name: dns[1], port };
  return { name: parts[0], port };
}

/** "can web reach api:80", "can frontend reach svc/prod/api on port 80?" */
export interface ReachQuery {
  source: ParsedEndpoint;
  destination: ParsedEndpoint;
  port?: number;
}

export function parseReachQuery(text: string): ReachQuery | null {
  const m = text.trim().replace(/^>\s*/, '').match(/^can\s+(\S+)\s+(?:reach|talk to|connect to|access)\s+(\S+)(?:\s+(?:on\s+)?(?:port\s+)?(\d{1,5}))?\s*\??$/i);
  if (!m) return null;
  const source = parseEndpoint(m[1]);
  const destination = parseEndpoint(m[2]);
  if (!source || !destination) return null;
  const port = m[3] ? Number(m[3]) : destination.port;
  return { source, destination, port };
}

/** Kinds tried first when a bare name could be several things. */
const SOURCE_ORDER = ['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'ReplicaSet', 'Pod'];
const DEST_ORDER = ['Service', 'Deployment', 'StatefulSet', 'DaemonSet', 'Pod', 'Job', 'ReplicaSet'];

/** Resolves a typed endpoint against the pickable targets, preferring `preferNamespace`. */
export function matchTarget(targets: Target[], ep: ParsedEndpoint, role: 'source' | 'destination', preferNamespace?: string): NetRef | null {
  if (ep.kind === 'IP' && ep.ip) return { kind: 'IP', ip: ep.ip };
  const order = role === 'source' ? SOURCE_ORDER : DEST_ORDER;
  const candidates = targets.filter((t) => {
    if (role === 'source' && t.ref.kind === 'Service') return false;
    if (t.ref.name !== ep.name) return false;
    if (ep.namespace && t.ref.namespace !== ep.namespace) return false;
    if (ep.kind && t.ref.kind !== ep.kind) return false;
    return true;
  });
  if (candidates.length === 0) return null;
  candidates.sort((a, b) => {
    const na = a.ref.namespace === preferNamespace ? 0 : 1;
    const nb = b.ref.namespace === preferNamespace ? 0 : 1;
    if (na !== nb) return na - nb;
    return order.indexOf(a.ref.kind) - order.indexOf(b.ref.kind);
  });
  return candidates[0].ref;
}

/** Filters the picker list: every whitespace-separated term must match kind, namespace or name. */
export function filterTargets(targets: Target[], query: string, role: 'source' | 'destination', limit = 60): Target[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  const out: Target[] = [];
  for (const t of targets) {
    if (role === 'source' && t.ref.kind === 'Service') continue;
    const hay = `${t.ref.kind} ${t.ref.namespace}/${t.ref.name}`.toLowerCase();
    if (terms.every((term) => hay.includes(term))) {
      out.push(t);
      if (out.length >= limit) break;
    }
  }
  return out;
}

export const hopTone = (s: HopStatus): 'success' | 'danger' | 'warning' | 'muted' =>
  s === 'pass' ? 'success' : s === 'fail' ? 'danger' : s === 'warn' ? 'warning' : 'muted';

export const verdictText = (v: Verdict): string =>
  v === 'allowed' ? 'Reachable' : v === 'blocked' ? 'Blocked' : 'Reachable with caveats';

export function formatRate(rps: number): string {
  if (rps >= 100) return `${Math.round(rps)}/s`;
  if (rps >= 1) return `${rps.toFixed(1)}/s`;
  if (rps > 0) return `${(rps * 60).toFixed(1)}/min`;
  return '0/s';
}

export function formatPct(r: number): string {
  if (r <= 0) return '0%';
  if (r < 0.001) return '<0.1%';
  return `${(r * 100).toFixed(r < 0.1 ? 1 : 0)}%`;
}

/** apiVersion for a kind, for ResourceLink. */
export function apiVersionOf(kind: string): string {
  switch (kind) {
    case 'Deployment': case 'StatefulSet': case 'DaemonSet': case 'ReplicaSet': return 'apps/v1';
    case 'Job': case 'CronJob': return 'batch/v1';
    case 'NetworkPolicy': return 'networking.k8s.io/v1';
    default: return 'v1';
  }
}

// --- Intent hand-off: the palette and inspector ask the Network page to open on a path. ---

export interface NetworkIntent {
  view: 'path' | 'coverage' | 'traffic';
  source?: NetRef;
  destination?: NetRef;
  /** Unresolved text from the palette ("can web reach api:80"). */
  query?: string;
  port?: number;
  namespace?: string;
}

let pending: NetworkIntent | null = null;
const listeners = new Set<(i: NetworkIntent) => void>();

export function setNetworkIntent(intent: NetworkIntent) {
  pending = intent;
  listeners.forEach((l) => l(intent));
}

/** Returns and clears the pending intent. */
export function takeNetworkIntent(): NetworkIntent | null {
  const i = pending;
  pending = null;
  return i;
}

export function onNetworkIntent(fn: (i: NetworkIntent) => void): () => void {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}
