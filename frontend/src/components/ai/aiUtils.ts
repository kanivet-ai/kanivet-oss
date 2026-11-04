import type { FindingResource, Investigation, InvestigationTarget } from '../../services/api/ai';

/** Merge an incremental snapshot (events after `after`) into what we hold. */
export function mergeInvestigation(prev: Investigation | undefined, snap: Investigation): Investigation {
  if (!prev) return snap;
  const known = new Set(prev.events.map((e) => e.seq));
  const events = prev.events.concat(snap.events.filter((e) => !known.has(e.seq)));
  events.sort((a, b) => a.seq - b.seq);
  return { ...snap, events };
}

const CORE_API: Record<string, string> = {
  Pod: 'v1', Service: 'v1', ConfigMap: 'v1', Secret: 'v1', Node: 'v1', Namespace: 'v1', Event: 'v1',
  PersistentVolumeClaim: 'v1', PersistentVolume: 'v1', ServiceAccount: 'v1', Endpoints: 'v1',
  Deployment: 'apps/v1', StatefulSet: 'apps/v1', DaemonSet: 'apps/v1', ReplicaSet: 'apps/v1',
  Job: 'batch/v1', CronJob: 'batch/v1',
  Ingress: 'networking.k8s.io/v1', NetworkPolicy: 'networking.k8s.io/v1',
  HorizontalPodAutoscaler: 'autoscaling/v2', PodDisruptionBudget: 'policy/v1',
  Role: 'rbac.authorization.k8s.io/v1', RoleBinding: 'rbac.authorization.k8s.io/v1',
  ClusterRole: 'rbac.authorization.k8s.io/v1', ClusterRoleBinding: 'rbac.authorization.k8s.io/v1',
  StorageClass: 'storage.k8s.io/v1', Application: 'argoproj.io/v1alpha1',
};

/** The apiVersion to open a resource with when the agent left it out. */
export function apiVersionFor(r: Pick<FindingResource, 'kind' | 'apiVersion'>): string {
  return r.apiVersion || CORE_API[r.kind] || 'v1';
}

/** Unique resources across the findings and the top-level list. */
export function collectResources(inv: Investigation): FindingResource[] {
  const out: FindingResource[] = [];
  const seen = new Set<string>();
  const push = (r?: FindingResource) => {
    if (!r || !r.kind || !r.name) return;
    const k = `${r.kind}/${r.namespace || ''}/${r.name}`;
    if (seen.has(k)) return;
    seen.add(k);
    out.push({ ...r, cluster: r.cluster || inv.target.cluster });
  };
  inv.findings?.resources?.forEach(push);
  inv.findings?.findings?.forEach((f) => f.resources?.forEach(push));
  return out;
}

export function targetLabel(t: InvestigationTarget): string {
  if (!t.name) return 'Cluster overview';
  return t.namespace ? `${t.kind} ${t.namespace}/${t.name}` : `${t.kind} ${t.name}`;
}

/** "3s", "2m 05s", "1h 4m". */
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

/** "just now", "12s ago", "4m ago", "2h ago". */
export function timeAgo(iso: string | number | undefined, now = Date.now()): string {
  if (iso === undefined || iso === '') return 'never';
  const t = typeof iso === 'number' ? iso : Date.parse(iso);
  const s = Math.floor((now - t) / 1000);
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** A friendly agent name from an MCP clientInfo string ("claude-code 2.1.3"). */
export function prettyClient(client: string): string {
  const name = client.split(' ')[0].toLowerCase();
  const map: Record<string, string> = {
    'claude-code': 'Claude Code', 'claude-ai': 'Claude Desktop', 'claude': 'Claude', codex: 'Codex',
    'codex-mcp-client': 'Codex', cursor: 'Cursor', 'cursor-vscode': 'Cursor', 'visual-studio-code': 'VS Code',
    vscode: 'VS Code', 'mcp-remote': 'mcp-remote', mcpcheck: 'mcpcheck', unknown: 'Unknown agent',
  };
  return map[name] || client.split(' ')[0];
}

/** Mask a token for display: first 9 and last 4 characters. */
export function maskToken(token: string): string {
  if (token.length <= 16) return '•'.repeat(token.length);
  return `${token.slice(0, 9)}${'•'.repeat(12)}${token.slice(-4)}`;
}

export type MdBlock =
  | { type: 'h'; level: number; text: string }
  | { type: 'p'; text: string }
  | { type: 'ul'; items: string[] }
  | { type: 'ol'; items: string[] }
  | { type: 'code'; text: string };

/** A deliberately small markdown reader for agent reports. */
export function parseMarkdown(md: string): MdBlock[] {
  const blocks: MdBlock[] = [];
  const lines = md.replace(/\r/g, '').split('\n');
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith('```')) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith('```')) body.push(lines[i++]);
      i++;
      blocks.push({ type: 'code', text: body.join('\n') });
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      blocks.push({ type: 'h', level: h[1].length, text: h[2].trim() });
      i++;
      continue;
    }
    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*[-*]\s+/, ''));
      blocks.push({ type: 'ul', items });
      continue;
    }
    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*\d+[.)]\s+/, ''));
      blocks.push({ type: 'ol', items });
      continue;
    }
    if (line.trim() === '') {
      i++;
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== '' && !/^(#{1,6}\s|```|\s*[-*]\s|\s*\d+[.)]\s)/.test(lines[i])) para.push(lines[i++]);
    blocks.push({ type: 'p', text: para.join(' ') });
  }
  return blocks;
}

export type InlinePart = { type: 'text' | 'code' | 'bold'; text: string };

/** Split inline `code` and **bold** spans. */
export function parseInline(text: string): InlinePart[] {
  const parts: InlinePart[] = [];
  const re = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push({ type: 'text', text: text.slice(last, m.index) });
    const tok = m[0];
    parts.push(tok.startsWith('`') ? { type: 'code', text: tok.slice(1, -1) } : { type: 'bold', text: tok.slice(2, -2) });
    last = m.index + tok.length;
  }
  if (last < text.length) parts.push({ type: 'text', text: text.slice(last) });
  return parts;
}

/** A `Kind/ns/name` or `Kind/name` code span names a resource we can link. */
export function refFromCode(code: string): FindingResource | null {
  const m = /^([A-Z][A-Za-z]+)\/(?:([a-z0-9]([-a-z0-9]*[a-z0-9])?)\/)?([a-z0-9]([-.a-z0-9]*[a-z0-9])?)$/.exec(code.trim());
  if (!m) return null;
  return { kind: m[1], namespace: m[2] || undefined, name: m[4] };
}

const FEATURE_LABELS: Record<string, string> = {
  core: 'Core', actions: 'Actions', health: 'Health', changes: 'Changes', topology: 'Topology', certs: 'Certificates',
  rbac: 'Access (RBAC)', upgrade: 'Upgrade', network: 'Network', compare: 'Compare', images: 'Images',
  gitops: 'GitOps & CloudNativePG', helm: 'Helm', argo: 'Argo', metrics: 'Metrics & Prometheus', finops: 'Cost',
  crossplane: 'Crossplane', vcluster: 'vcluster',
};
export const featureLabel = (f: string) => FEATURE_LABELS[f] || f;

/** Tools grouped by feature, core first, read tools before write tools. */
export function groupTools<T extends { feature: string; name: string; write: boolean }>(tools: T[]): [string, T[]][] {
  const by = new Map<string, T[]>();
  for (const t of tools) by.set(t.feature || 'core', [...(by.get(t.feature || 'core') || []), t]);
  const order = Object.keys(FEATURE_LABELS);
  return [...by.entries()]
    .sort(([a], [b]) => (order.indexOf(a) + 1 || 99) - (order.indexOf(b) + 1 || 99) || a.localeCompare(b))
    .map(([f, ts]) => [f, ts.sort((a, b) => Number(a.write) - Number(b.write) || a.name.localeCompare(b.name))]);
}

