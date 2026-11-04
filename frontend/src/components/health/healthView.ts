import type {
  HealthFinding,
  HealthIgnore,
  HealthIssue,
  HealthRef,
  HealthSeverity,
} from '../../types/health';

export const SEVERITIES: HealthSeverity[] = ['critical', 'warning', 'info'];

export const SEVERITY_LABEL: Record<HealthSeverity, string> = {
  critical: 'Critical',
  warning: 'Warning',
  info: 'Info',
};

export const SEVERITY_DOT: Record<HealthSeverity, string> = {
  critical: 'ap-dot--danger',
  warning: 'ap-dot--warning',
  info: 'ap-dot--info',
};

export const SEVERITY_BADGE: Record<HealthSeverity, string> = {
  critical: 'ap-badge--danger',
  warning: 'ap-badge--warning',
  info: 'ap-badge--info',
};

export interface SeverityGroup<T> {
  severity: HealthSeverity;
  items: T[];
}

/** Splits items into severity groups in critical-first order, dropping empty ones. */
export function groupBySeverity<T extends { severity: HealthSeverity }>(items: T[]): SeverityGroup<T>[] {
  return SEVERITIES.map((severity) => ({ severity, items: items.filter((i) => i.severity === severity) })).filter(
    (g) => g.items.length > 0,
  );
}

/** "3m", "5h", "2d" since an RFC 3339 time; empty for a zero or bad time. */
export function ageLabel(since: string | undefined, now: number = Date.now()): string {
  if (!since) return '';
  const t = Date.parse(since);
  if (!Number.isFinite(t) || t <= 0 || new Date(t).getUTCFullYear() < 1980) return '';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 172800) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export type ScoreTone = 'good' | 'fair' | 'poor';

export function scoreTone(score: number): ScoreTone {
  if (score >= 85) return 'good';
  if (score >= 60) return 'fair';
  return 'poor';
}

export const refKey = (r: HealthRef) => `${r.kind}/${r.namespace || ''}/${r.name}`;

export function sameRef(a: HealthRef, b: HealthRef): boolean {
  return a.kind === b.kind && a.name === b.name && (a.namespace || '') === (b.namespace || '');
}

export interface ResourceFindings {
  resource: HealthRef;
  findings: HealthFinding[];
  worst: HealthSeverity;
}

const rank = (s: HealthSeverity) => (s === 'critical' ? 3 : s === 'warning' ? 2 : 1);

/** Groups findings per resource, worst resources first. Ignored findings sort last within a resource. */
export function findingsByResource(findings: HealthFinding[]): ResourceFindings[] {
  const map = new Map<string, ResourceFindings>();
  for (const f of findings) {
    const k = refKey(f.resource);
    let g = map.get(k);
    if (!g) {
      g = { resource: f.resource, findings: [], worst: 'info' };
      map.set(k, g);
    }
    g.findings.push(f);
    if (!f.ignored && rank(f.severity) > rank(g.worst)) g.worst = f.severity;
  }
  const out = Array.from(map.values());
  for (const g of out) {
    g.findings.sort((a, b) => Number(!!a.ignored) - Number(!!b.ignored) || rank(b.severity) - rank(a.severity));
  }
  const active = (g: ResourceFindings) => g.findings.filter((f) => !f.ignored).length;
  out.sort((a, b) => rank(b.worst) - rank(a.worst) || active(b) - active(a) || refKey(a.resource).localeCompare(refKey(b.resource)));
  return out;
}

/** The ignore that suppresses one finding, at resource or namespace scope. */
export function ignoreFor(cluster: string, f: HealthFinding, scope: 'resource' | 'namespace'): HealthIgnore {
  if (scope === 'namespace') return { cluster, checkId: f.checkId, namespace: f.resource.namespace };
  return { cluster, checkId: f.checkId, namespace: f.resource.namespace, kind: f.resource.kind, name: f.resource.name };
}

/** Case-insensitive match of an issue against a free-text query and namespace. */
export function matchIssue(issue: HealthIssue, query: string, namespace: string): boolean {
  if (namespace && (issue.resource.namespace || '') !== namespace) return false;
  if (!query) return true;
  const q = query.toLowerCase();
  return [issue.title, issue.reason, issue.message, issue.resource.name, issue.resource.kind, issue.resource.namespace || '']
    .some((s) => s.toLowerCase().includes(q));
}

/** The sidebar badge: the count of critical issues, or of all when none is critical. */
export function badgeFor(summary: { critical: number; warning: number; total: number } | undefined):
  { count: number; tone: 'danger' | 'warning' } | null {
  if (!summary || summary.total === 0) return null;
  if (summary.critical > 0) return { count: summary.critical, tone: 'danger' };
  if (summary.warning > 0) return { count: summary.warning, tone: 'warning' };
  return null;
}
