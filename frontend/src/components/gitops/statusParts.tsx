/* Status vocabulary shared by the Argo CD applications page, the unified
   GitOps page and the Flux/CNPG inspector sections. The visuals (pivots,
   pills) are the Argo page's, styled by ArgoApplicationsPage.css. */
import '../ArgoApplicationsPage.css';

/* Missing is amber rather than red: Argo itself shows it as a warning, and a
   missing app is usually a pending sync, not an outage. */
export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

export const syncTone = (status?: string): Tone => {
  switch ((status || '').toLowerCase()) {
    case 'synced': return 'success';
    case 'outofsync': return 'warning';
    default: return 'neutral';
  }
};

export const healthTone = (health?: string): Tone => {
  switch ((health || '').toLowerCase()) {
    case 'healthy': case 'ready': return 'success';
    case 'degraded': case 'failing': case 'failed': return 'danger';
    case 'progressing': return 'info';
    case 'missing': case 'suspended': return 'warning';
    default: return 'neutral';
  }
};

/** Argo's status identifiers in sentence case ("OutOfSync" → "Out of sync"). */
export const statusLabel = (status?: string): string => {
  if (!status) return 'Unknown';
  if (status === 'OutOfSync') return 'Out of sync';
  return status;
};

export const StatusBadge = ({ tone, label, title }: { tone: Tone; label: string; title?: string }) => (
  <span className={`ap-badge argo-apps-pill${tone === 'neutral' ? '' : ` ap-badge--${tone}`}`} title={title}>{label}</span>
);

export const Pivot = ({ value, label, tone, active, onClick }: { value: number; label: string; tone?: Tone; active: boolean; onClick?: () => void }) => (
  <button
    type="button"
    className={`argo-apps-pivot${tone ? ` pivot-${tone}` : ''}${active ? ' active' : ''}${onClick ? '' : ' static'}`}
    onClick={onClick}
    disabled={!onClick}
    aria-pressed={onClick ? active : undefined}
  >
    <span className="argo-apps-pivot-value" data-zero={value === 0 ? 'true' : undefined}>{value}</span>
    <span className="argo-apps-pivot-label">{label}</span>
  </button>
);

/** Shortens a Flux/Argo revision ("main@sha1:abcdef0123…" → "main@abcdef0"). */
export const shortRevision = (rev?: string): string => {
  if (!rev) return '';
  const m = rev.match(/^(.*?)@?(?:sha1|sha256):([0-9a-f]+)$/i);
  if (m) return `${m[1] ? `${m[1]}@` : ''}${m[2].slice(0, 7)}`;
  if (/^[0-9a-f]{40}$/i.test(rev)) return rev.slice(0, 7);
  return rev;
};
