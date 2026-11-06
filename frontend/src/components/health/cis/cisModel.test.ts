import { describe, expect, it } from 'vitest';
import type { CisControl, CisReport, ProviderInfo } from '../../../types/cis';
import {
  EMPTY_FILTER,
  agoLabel,
  cisExplainSeed,
  cisFixSeed,
  evidenceText,
  exportFileName,
  filterControls,
  groupControls,
  providerChipLabel,
  reportCSV,
  reportJSON,
  scoreOf,
  sourceLabel,
} from './cisModel';

const ctl = (
  id: string,
  status: CisControl['status'],
  extra: Partial<CisControl> = {},
): CisControl => {
  const [a, b] = id.split('.');
  return {
    id,
    section: a,
    sectionTitle: `Section ${a}`,
    subsection: `${a}.${b}`,
    subsectionTitle: `Sub ${a}.${b}`,
    title: `Control ${id}`,
    description: 'desc',
    scored: true,
    level: 1,
    status,
    source: 'built-in',
    ...extra,
  };
};

const eks: ProviderInfo = {
  provider: 'eks',
  distribution: 'EKS',
  version: '1.36',
  managedControlPlane: true,
  controlPlaneVisible: false,
  evidence: [],
  confidence: 1,
};

const report = (controls: CisControl[]): CisReport => ({
  cluster: 'arn:aws:eks:eu-west-2:1:cluster/sre-benchmarking',
  provider: eks,
  profile: {
    id: 'cis-eks',
    title: 'CIS Amazon EKS',
    version: '1.5',
    description: '',
  },
  profiles: [],
  autoProfile: 'cis-eks',
  generatedAt: '2026-11-05T10:00:00Z',
  score: 50,
  counts: {},
  sections: [],
  controls,
  scanners: [],
  notes: [],
});

describe('cis view model', () => {
  it('labels the provider chip', () => {
    expect(providerChipLabel(eks)).toBe('EKS 1.36 · managed control plane');
    expect(
      providerChipLabel({
        ...eks,
        distribution: 'kind',
        version: '1.31',
        managedControlPlane: false,
        controlPlaneVisible: true,
      }),
    ).toBe('kind 1.31 · control plane visible');
  });

  it('scores automated controls only', () => {
    const cs = [
      ctl('5.1.1', 'automated-pass'),
      ctl('5.1.2', 'automated-pass'),
      ctl('5.1.3', 'automated-pass'),
      ctl('5.1.4', 'automated-fail'),
      ctl('5.1.5', 'manual'),
      ctl('1.1.1', 'not-visible'),
      ctl('1.2.1', 'provider-responsibility'),
    ];
    expect(scoreOf(cs)).toBe(75);
    expect(scoreOf([ctl('5.1.5', 'manual')])).toBeNull();
  });

  it('filters by status, section, scored and text', () => {
    const cs = [
      ctl('5.1.1', 'automated-fail', { scored: false }),
      ctl('5.2.1', 'automated-pass'),
      ctl('4.2.1', 'automated-fail', { title: 'Kubelet anonymous auth' }),
    ];
    expect(
      filterControls(cs, { ...EMPTY_FILTER, statuses: ['automated-fail'] }).map(
        (c) => c.id,
      ),
    ).toEqual(['5.1.1', '4.2.1']);
    expect(
      filterControls(cs, { ...EMPTY_FILTER, section: '5' }).map((c) => c.id),
    ).toEqual(['5.1.1', '5.2.1']);
    expect(
      filterControls(cs, { ...EMPTY_FILTER, scoredOnly: true }).map(
        (c) => c.id,
      ),
    ).toEqual(['5.2.1', '4.2.1']);
    expect(
      filterControls(cs, { ...EMPTY_FILTER, search: 'anonymous' }).map(
        (c) => c.id,
      ),
    ).toEqual(['4.2.1']);
  });

  it('groups by subsection in order with counts', () => {
    const g = groupControls([
      ctl('5.1.1', 'automated-fail'),
      ctl('5.1.2', 'automated-pass'),
      ctl('5.2.1', 'automated-fail'),
    ]);
    expect(g.map((x) => x.id)).toEqual(['5.1', '5.2']);
    expect(g[0].counts).toEqual({ 'automated-fail': 1, 'automated-pass': 1 });
  });

  it('names scanner sources with their age', () => {
    const now = Date.parse('2026-11-05T10:00:00Z');
    const c = ctl('1.1.1', 'automated-fail', {
      source: 'trivy-operator',
      scanners: [
        {
          source: 'trivy-operator',
          status: 'automated-fail',
          scannedAt: '2026-11-05T08:00:00Z',
        },
      ],
    });
    expect(sourceLabel(c, now)).toBe('from Trivy Operator, scanned 2h ago');
    expect(sourceLabel(ctl('1.1.1', 'automated-pass'), now)).toBe('');
    expect(agoLabel('0001-01-01T00:00:00Z', now)).toBe('');
  });

  it('builds evidence text and AI seeds with the control, evidence and provider', () => {
    const c = ctl('3.2.8', 'automated-fail', {
      message: '1 of 1 node configuration group(s) do not comply.',
      nodeGroups: [
        {
          pool: 'ng-1',
          nodes: ['n1'],
          totalNodes: 3,
          value: 'rotateCertificates=false',
          ok: false,
        },
      ],
      resources: [
        { apiVersion: 'v1', kind: 'Namespace', name: 'app' },
        {
          apiVersion: 'apps/v1',
          kind: 'Deployment',
          namespace: 'app',
          name: 'web',
        },
      ],
      remediation: 'Set rotateCertificates: true.',
      snippet: 'rotateCertificates: true',
    });
    const ev = evidenceText(c);
    expect(ev).toContain(
      'pool ng-1 (1 of 3 nodes read): rotateCertificates=false [not compliant]',
    );
    expect(ev).toContain('- Deployment app/web');
    const fix = cisFixSeed('ctx', c, eks, 'CIS Amazon EKS');
    expect(fix.intent).toBe('fix');
    expect(fix.text).toContain('3.2.8');
    expect(fix.text).toContain('managed');
    expect(fix.resource?.name).toBe('web');
    const chip = fix.chips?.find((x) => x.label === 'CIS 3.2.8') as any;
    expect(chip.text).toContain('EKS 1.36 · managed control plane');
    expect(chip.text).toContain('rotateCertificates: true');
    const ask = cisExplainSeed('ctx', c, eks);
    expect(ask.intent).toBe('ask');
    expect(ask.source).toBe('cis');
  });

  it('exports CSV and JSON with cluster, provider, profile and timestamp', () => {
    const r = report([
      ctl('4.1.1', 'automated-fail', { message: 'has "quotes", and commas' }),
    ]);
    const csv = reportCSV(r).split('\n');
    expect(csv[0]).toContain('cluster,provider,profile,generatedAt,id');
    expect(csv[1]).toContain('CIS Amazon EKS');
    expect(csv[1]).toContain('"has ""quotes"", and commas"');
    const j = JSON.parse(reportJSON(r));
    expect(j.controls[0].id).toBe('4.1.1');
    expect(j.provider.provider).toBe('eks');
    expect(exportFileName(r, 'csv')).toBe(
      'cis-cis-eks-sre-benchmarking-2026-11-05.csv',
    );
  });
});
