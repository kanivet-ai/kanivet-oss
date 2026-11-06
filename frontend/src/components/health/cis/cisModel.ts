import type {
  CisControl,
  CisReport,
  CisStatus,
  ProviderInfo,
} from '../../../types/cis';
import type { HealthRef } from '../../../types/health';
import {
  clusterChip,
  refLabel,
  resourceChip,
  textChip,
  type AskSeed,
} from '../../ai/aiContext';

/** Display order of statuses: what needs attention first. */
export const STATUS_ORDER: CisStatus[] = [
  'automated-fail',
  'automated-pass',
  'manual',
  'not-visible',
  'provider-responsibility',
  'error',
];

export const STATUS_LABEL: Record<CisStatus, string> = {
  'automated-pass': 'Pass',
  'automated-fail': 'Fail',
  manual: 'Manual',
  'provider-responsibility': 'Provider',
  'not-visible': 'Not visible',
  error: 'Error',
};

export const STATUS_BADGE: Record<CisStatus, string> = {
  'automated-pass': 'ap-badge--success',
  'automated-fail': 'ap-badge--danger',
  manual: 'ap-badge--info',
  'provider-responsibility': 'ap-badge--purple',
  'not-visible': '',
  error: 'ap-badge--warning',
};

export const SOURCE_LABEL: Record<string, string> = {
  'built-in': 'Kanivet',
  'trivy-operator': 'Trivy Operator',
  kubescape: 'Kubescape',
  'kube-bench': 'kube-bench',
};

/** "EKS 1.36 · managed control plane". */
export function providerChipLabel(p: ProviderInfo | undefined): string {
  if (!p) return '';
  const name = [p.distribution, p.version].filter(Boolean).join(' ');
  const plane = p.managedControlPlane
    ? 'managed control plane'
    : p.controlPlaneVisible
      ? 'control plane visible'
      : 'self-managed';
  return `${name} · ${plane}`;
}

export interface CisFilter {
  statuses: CisStatus[];
  section: string;
  scoredOnly: boolean;
  search: string;
}

export const EMPTY_FILTER: CisFilter = {
  statuses: [],
  section: '',
  scoredOnly: false,
  search: '',
};

export function filterControls(cs: CisControl[], f: CisFilter): CisControl[] {
  const q = f.search.trim().toLowerCase();
  return cs.filter((c) => {
    if (f.statuses.length && !f.statuses.includes(c.status)) return false;
    if (f.section && c.section !== f.section) return false;
    if (f.scoredOnly && !c.scored) return false;
    if (
      q &&
      !`${c.id} ${c.title} ${c.description} ${c.message || ''}`
        .toLowerCase()
        .includes(q)
    )
      return false;
    return true;
  });
}

export interface ControlGroup {
  id: string;
  title: string;
  controls: CisControl[];
  counts: Partial<Record<CisStatus, number>>;
}

/** Groups controls by subsection ("5.1 RBAC and service accounts"), in catalog order. */
export function groupControls(cs: CisControl[]): ControlGroup[] {
  const out: ControlGroup[] = [];
  const idx = new Map<string, ControlGroup>();
  for (const c of cs) {
    let g = idx.get(c.subsection);
    if (!g) {
      g = {
        id: c.subsection,
        title: c.subsectionTitle || c.sectionTitle,
        controls: [],
        counts: {},
      };
      idx.set(c.subsection, g);
      out.push(g);
    }
    g.controls.push(c);
    g.counts[c.status] = (g.counts[c.status] || 0) + 1;
  }
  return out;
}

/** A group with nothing to act on (all not visible or the provider's) starts collapsed. */
export function quietGroup(g: ControlGroup): boolean {
  return g.controls.every(
    (c) => c.status === 'not-visible' || c.status === 'provider-responsibility',
  );
}

/** Score over automated controls only; null when nothing is automated. */
export function scoreOf(cs: CisControl[]): number | null {
  let pass = 0;
  let fail = 0;
  for (const c of cs) {
    if (c.status === 'automated-pass') pass++;
    else if (c.status === 'automated-fail') fail++;
  }
  return pass + fail === 0 ? null : Math.round((pass * 100) / (pass + fail));
}

export function countOf(r: CisReport | null | undefined, s: CisStatus): number {
  return r?.counts?.[s] || 0;
}

/** "2h ago" for scanner timestamps. */
export function agoLabel(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= 0) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

/** Who decided: "from Trivy Operator, scanned 2h ago". */
export function sourceLabel(c: CisControl, now = Date.now()): string {
  if (c.source === 'built-in') return '';
  const f = c.scanners?.find((s) => s.source === c.source);
  const ago = agoLabel(f?.scannedAt, now);
  return `from ${SOURCE_LABEL[c.source] || c.source}${ago ? `, scanned ${ago}` : ''}`;
}

/** Plain-text evidence for AI seeds and exports. */
export function evidenceText(c: CisControl, limit = 20): string {
  const lines: string[] = [];
  if (c.message) lines.push(c.message);
  for (const v of c.values || [])
    lines.push(
      `${v.name} = ${v.value}${v.source ? ` (${v.source})` : ''}${v.ok ? '' : ' [not compliant]'}`,
    );
  for (const g of c.nodeGroups || []) {
    lines.push(
      `pool ${g.pool} (${g.nodes.length} of ${g.totalNodes} nodes read): ${g.error ? `error: ${g.error}` : `${g.value}${g.ok ? '' : ' [not compliant]'}`}`,
    );
  }
  const res = c.resources || [];
  for (const r of res.slice(0, limit)) lines.push(`- ${refLabel(r)}`);
  const total = c.resourcesTotal || res.length;
  if (total > Math.min(limit, res.length))
    lines.push(`... and ${total - Math.min(limit, res.length)} more`);
  for (const s of c.scanners || [])
    lines.push(
      `${SOURCE_LABEL[s.source] || s.source}: ${STATUS_LABEL[s.status]}${s.detail ? ` (${s.detail})` : ''}`,
    );
  if (c.manualCheck) lines.push(`To check by hand: ${c.manualCheck}`);
  return lines.join('\n');
}

function controlChipText(
  c: CisControl,
  provider?: ProviderInfo,
  profile?: string,
): string {
  return [
    `CIS control ${c.id}: ${c.title} (${profile || 'CIS'}${provider ? `, ${providerChipLabel(provider)}` : ''})`,
    `What it checks: ${c.description}`,
    `Status: ${STATUS_LABEL[c.status]}${c.source !== 'built-in' ? ` (${SOURCE_LABEL[c.source] || c.source})` : ''}`,
    `Evidence:\n${evidenceText(c)}`,
    c.remediation ? `Suggested remediation: ${c.remediation}` : '',
    c.snippet ? `Snippet:\n${c.snippet}` : '',
  ]
    .filter(Boolean)
    .join('\n');
}

function firstResource(c: CisControl): HealthRef | undefined {
  return c.resources?.find((r) => r.kind !== 'Namespace') || c.resources?.[0];
}

export function cisExplainSeed(
  cluster: string,
  c: CisControl,
  provider?: ProviderInfo,
  profile?: string,
): AskSeed {
  const r = firstResource(c);
  return {
    source: 'cis',
    intent: 'ask',
    cluster,
    title: `CIS ${c.id}`,
    text: `Explain CIS control ${c.id} ("${c.title}") for this ${provider?.distribution || ''} cluster: what the risk is, why it is ${STATUS_LABEL[c.status].toLowerCase()} here given the evidence, and whether it is worth fixing.`,
    chips: [
      clusterChip(cluster),
      ...(r ? [resourceChip(cluster, r, false)] : []),
      textChip(`CIS ${c.id}`, controlChipText(c, provider, profile)),
    ],
  };
}

export function cisFixSeed(
  cluster: string,
  c: CisControl,
  provider?: ProviderInfo,
  profile?: string,
): AskSeed {
  const r = firstResource(c);
  const managed = provider?.managedControlPlane;
  return {
    source: 'cis',
    intent: 'fix',
    cluster,
    title: `Fix CIS ${c.id}`,
    resource: r ? { ...r, cluster } : undefined,
    text: `Make this cluster pass CIS control ${c.id} ("${c.title}"). Use the evidence to target the failing ${c.nodeGroups?.length ? 'node pools' : c.values?.length ? 'flags' : 'resources'}${managed ? '; the control plane is managed, so only change what the cluster owner controls' : ''}. Prefer one consistent change over many one-off edits.`,
    chips: [
      clusterChip(cluster),
      ...(r ? [resourceChip(cluster, r)] : []),
      textChip(`CIS ${c.id}`, controlChipText(c, provider, profile)),
    ],
  };
}

// ---- export ---------------------------------------------------------------

function csvCell(v: string | number | boolean | undefined): string {
  const s = v === undefined ? '' : String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function reportCSV(r: CisReport): string {
  const head = [
    'cluster',
    'provider',
    'profile',
    'generatedAt',
    'id',
    'section',
    'title',
    'scored',
    'level',
    'status',
    'source',
    'disagreement',
    'message',
    'evidence',
  ];
  const rows = r.controls.map((c) => [
    r.cluster,
    providerChipLabel(r.provider),
    r.profile.title,
    r.generatedAt,
    c.id,
    c.subsectionTitle,
    c.title,
    c.scored,
    c.level,
    c.status,
    SOURCE_LABEL[c.source] || c.source,
    !!c.disagreement,
    c.message || '',
    evidenceText(c, 200),
  ]);
  return (
    [head, ...rows].map((row) => row.map(csvCell).join(',')).join('\n') + '\n'
  );
}

export function reportJSON(r: CisReport): string {
  return JSON.stringify(
    {
      cluster: r.cluster,
      provider: r.provider,
      profile: r.profile,
      generatedAt: r.generatedAt,
      score: r.score,
      counts: r.counts,
      sections: r.sections,
      controls: r.controls,
      scanners: r.scanners,
      notes: r.notes,
    },
    null,
    2,
  );
}

export function exportFileName(r: CisReport, ext: 'csv' | 'json'): string {
  const name = (r.cluster.split('/').pop() || r.cluster).replace(
    /[^A-Za-z0-9._-]+/g,
    '-',
  );
  const day = (r.generatedAt || new Date().toISOString()).slice(0, 10);
  return `cis-${r.profile.id}-${name}-${day}.${ext}`;
}
