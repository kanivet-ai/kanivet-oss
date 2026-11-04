import type { CertificateInfo, CertStatus } from '../../types/certs';

export const STATUS_ORDER: CertStatus[] = ['expired', 'critical', 'warning', 'ok', 'invalid'];

export const STATUS_LABEL: Record<CertStatus, string> = {
  expired: 'Expired',
  critical: 'Under 14 days',
  warning: 'Under 30 days',
  ok: 'Valid',
  invalid: 'Invalid',
};

/** The .ap-dot / badge modifier for a status. */
export const STATUS_TONE: Record<CertStatus, 'danger' | 'warning' | 'success' | 'muted'> = {
  expired: 'danger',
  critical: 'danger',
  warning: 'warning',
  ok: 'success',
  invalid: 'muted',
};

export function formatDaysLeft(c: Pick<CertificateInfo, 'status' | 'daysLeft' | 'notAfter'>): string {
  if (!c.notAfter) return 'Not issued';
  const d = c.daysLeft;
  if (c.status === 'expired') return d === -1 ? 'Expired yesterday' : `Expired ${Math.abs(d)} days ago`;
  if (d === 0) return 'Expires today';
  if (d === 1) return '1 day left';
  return `${d} days left`;
}

export function displayName(c: CertificateInfo): string {
  return c.commonName || c.sans[0] || c.owner.name;
}

/** Who issued the cert, for grouping: the cert-manager issuer when known. */
export function issuerLabel(c: CertificateInfo): string {
  if (c.certManager?.issuerRef?.name) return `${c.certManager.issuerRef.kind} ${c.certManager.issuerRef.name}`;
  if (c.selfSigned) return 'Self-signed';
  return c.issuerOrganization || c.issuerCommonName || c.issuer || 'Unknown issuer';
}

/** Elapsed fraction of the validity window, clamped to [0, 1]. */
export function validityFraction(c: Pick<CertificateInfo, 'notBefore' | 'notAfter'>, now = Date.now()): number {
  if (!c.notBefore || !c.notAfter) return 0;
  const a = Date.parse(c.notBefore);
  const b = Date.parse(c.notAfter);
  if (!(b > a)) return 1;
  return Math.min(1, Math.max(0, (now - a) / (b - a)));
}

export type SortKey = 'status' | 'name' | 'namespace' | 'issuer' | 'expires' | 'key' | 'usedBy';

export function sortCerts(certs: CertificateInfo[], key: SortKey, dir: 'asc' | 'desc'): CertificateInfo[] {
  const rank = (s: CertStatus) => STATUS_ORDER.indexOf(s);
  const expiry = (c: CertificateInfo) => (c.notAfter ? Date.parse(c.notAfter) : Number.MAX_SAFE_INTEGER);
  const cmp = (a: CertificateInfo, b: CertificateInfo): number => {
    switch (key) {
      case 'status':
        return rank(a.status) - rank(b.status) || expiry(a) - expiry(b);
      case 'name':
        return displayName(a).localeCompare(displayName(b));
      case 'namespace':
        return (a.owner.namespace || '').localeCompare(b.owner.namespace || '');
      case 'issuer':
        return issuerLabel(a).localeCompare(issuerLabel(b));
      case 'expires':
        return expiry(a) - expiry(b);
      case 'key':
        return `${a.keyAlgorithm || ''}${String(a.keySize || 0).padStart(5, '0')}`.localeCompare(
          `${b.keyAlgorithm || ''}${String(b.keySize || 0).padStart(5, '0')}`,
        );
      case 'usedBy':
        return a.usedBy.length - b.usedBy.length;
    }
  };
  const out = [...certs].sort((a, b) => cmp(a, b) || a.id.localeCompare(b.id));
  return dir === 'desc' ? out.reverse() : out;
}

export interface IssuerGroup {
  issuer: string;
  certs: CertificateInfo[];
  worst: CertStatus;
}

export function groupByIssuer(certs: CertificateInfo[]): IssuerGroup[] {
  const m = new Map<string, CertificateInfo[]>();
  for (const c of certs) {
    const k = issuerLabel(c);
    m.set(k, [...(m.get(k) || []), c]);
  }
  const groups = [...m.entries()].map(([issuer, cs]) => ({
    issuer,
    certs: cs,
    worst: cs.reduce<CertStatus>((w, c) => (STATUS_ORDER.indexOf(c.status) < STATUS_ORDER.indexOf(w) ? c.status : w), 'invalid'),
  }));
  return groups.sort(
    (a, b) => STATUS_ORDER.indexOf(a.worst) - STATUS_ORDER.indexOf(b.worst) || a.issuer.localeCompare(b.issuer),
  );
}

export interface TimelineBin {
  /** Inclusive day offset range from now; the first bin holds expired certs. */
  label: string;
  fromDay: number;
  toDay: number;
  certs: CertificateInfo[];
  tone: CertStatus;
}

/**
 * Buckets certificates into an expiry heat strip: one "expired" bin, then
 * weekly bins for the next `weeks` weeks, then a "later" bin.
 */
export function timelineBins(certs: CertificateInfo[], weeks = 13): TimelineBin[] {
  const bins: TimelineBin[] = [{ label: 'Expired', fromDay: -Infinity, toDay: -1, certs: [], tone: 'expired' }];
  for (let w = 0; w < weeks; w++) {
    const from = w * 7;
    const to = from + 6;
    const tone: CertStatus = to < 14 ? 'critical' : from < 30 ? 'warning' : 'ok';
    bins.push({ label: w === 0 ? 'This week' : `In ${w} wk`, fromDay: from, toDay: to, certs: [], tone });
  }
  bins.push({ label: 'Later', fromDay: weeks * 7, toDay: Infinity, certs: [], tone: 'ok' });
  for (const c of certs) {
    if (!c.notAfter) continue;
    const d = c.status === 'expired' ? -1 : Math.max(0, c.daysLeft);
    const bin = bins.find((b) => d >= b.fromDay && d <= b.toDay);
    bin?.certs.push(c);
  }
  return bins;
}

/** Certificates relevant to a given resource's inspector. */
export function inspectorScope(resource: any): { kind: string; namespace?: string; name: string } | null {
  const kind = resource?.kind;
  const name = resource?.metadata?.name;
  if (!kind || !name) return null;
  const apiVersion: string = resource?.apiVersion || '';
  const namespace = resource?.metadata?.namespace;
  if (kind === 'Secret') return resource?.type === 'kubernetes.io/tls' ? { kind, namespace, name } : null;
  if (kind === 'Ingress' && (resource?.spec?.tls?.length ?? 0) > 0) return { kind, namespace, name };
  if (kind === 'Gateway' && apiVersion.startsWith('gateway.networking.k8s.io')) return { kind, namespace, name };
  if (kind === 'Certificate' && apiVersion.startsWith('cert-manager.io')) return { kind, namespace, name };
  if ((kind === 'MutatingWebhookConfiguration' || kind === 'ValidatingWebhookConfiguration')) return { kind, name };
  return null;
}
