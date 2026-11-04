import type { UpgradeAffectedResource, UpgradeCatalog, UpgradeDeprecation, UpgradeSeverity, UpgradeSource } from '../../types/upgrade';

export const LAST_APPLIED = 'kubectl.kubernetes.io/last-applied-configuration';

const apiVersionOf = (d: UpgradeDeprecation) => (d.group ? `${d.group}/${d.version}` : d.version);

/** Compares "1.29" style minors. */
export function compareMinor(a: string, b: string): number {
  const [am, an] = a.split('.').map((n) => parseInt(n, 10) || 0);
  const [bm, bn] = b.split('.').map((n) => parseInt(n, 10) || 0);
  return am !== bm ? am - bm : an - bn;
}

export interface ObjectDeprecation {
  deprecation: UpgradeDeprecation;
  /** Where the deprecated apiVersion was seen on this object. */
  via: 'apiVersion' | 'last-applied';
  apiVersion: string;
}

/**
 * Deprecated API versions an object uses: its own apiVersion, or the one in
 * its last-applied-configuration annotation (the manifest someone applies).
 */
export function objectDeprecations(catalog: UpgradeCatalog | null, obj: any): ObjectDeprecation[] {
  if (!catalog || !obj?.kind) return [];
  const out: ObjectDeprecation[] = [];
  const check = (apiVersion: string | undefined, via: ObjectDeprecation['via']) => {
    if (!apiVersion) return;
    const d = catalog.apis.find((x) => x.kind === obj.kind && apiVersionOf(x) === apiVersion);
    if (d && !out.some((o) => o.deprecation === d)) out.push({ deprecation: d, via, apiVersion });
  };
  check(obj.apiVersion, 'apiVersion');
  const raw = obj.metadata?.annotations?.[LAST_APPLIED];
  if (typeof raw === 'string' && raw) {
    try {
      check(JSON.parse(raw)?.apiVersion, 'last-applied');
    } catch {
      // Not JSON: nothing to read.
    }
  }
  return out;
}

/** The badge tone for a deprecation on a cluster at currentVersion. */
export function badgeTone(d: UpgradeDeprecation, currentVersion?: string): 'danger' | 'warning' | 'info' {
  if (!d.removedIn) return 'info';
  if (currentVersion && compareMinor(currentVersion, d.removedIn) >= 0) return 'danger';
  return 'warning';
}

export function sourceLabel(r: Pick<UpgradeAffectedResource, 'source' | 'helmRelease'>): string {
  switch (r.source) {
    case 'live':
      return 'Live';
    case 'last-applied':
      return 'last-applied';
    case 'helm':
      return r.helmRelease ? `Helm release ${r.helmRelease}` : 'Helm release';
  }
}

export const SEVERITY_LABEL: Record<UpgradeSeverity, string> = { blocker: 'Blocker', warning: 'Warning', info: 'Info' };
export const SEVERITY_TONE: Record<UpgradeSeverity, 'danger' | 'warning' | 'info'> = { blocker: 'danger', warning: 'warning', info: 'info' };

/** Counts a finding's resources by source, for the group header. */
export function sourceCounts(resources: UpgradeAffectedResource[]): Record<UpgradeSource, number> {
  const out: Record<UpgradeSource, number> = { live: 0, 'last-applied': 0, helm: 0 };
  for (const r of resources) out[r.source] += 1;
  return out;
}

export type SeverityFilter = 'all' | UpgradeSeverity;

/** Text match over a finding's API, kind, replacement and resources. */
export function matchesQuery(f: { apiVersion: string; kind: string; replacement?: string; resources: UpgradeAffectedResource[] }, q: string): boolean {
  const t = q.trim().toLowerCase();
  if (!t) return true;
  if (`${f.apiVersion} ${f.kind} ${f.replacement || ''}`.toLowerCase().includes(t)) return true;
  return f.resources.some((r) => `${r.namespace || ''}/${r.name} ${r.helmRelease || ''}`.toLowerCase().includes(t));
}
