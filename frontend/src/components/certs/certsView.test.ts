import { describe, expect, it } from 'vitest';
import type { CertificateInfo } from '../../types/certs';
import {
  formatDaysLeft,
  groupByIssuer,
  inspectorScope,
  issuerLabel,
  sortCerts,
  timelineBins,
  validityFraction,
} from './certsView';

const cert = (p: Partial<CertificateInfo>): CertificateInfo => ({
  id: p.id || 'secret/ns/x',
  source: 'secret',
  owner: { apiVersion: 'v1', kind: 'Secret', namespace: 'ns', name: 'x' },
  subject: '',
  sans: [],
  issuer: '',
  chain: [],
  daysLeft: 100,
  status: 'ok',
  selfSigned: false,
  isCA: false,
  usedBy: [],
  notBefore: '2026-01-01T00:00:00Z',
  notAfter: '2027-01-01T00:00:00Z',
  ...p,
});

describe('certsView', () => {
  it('formats days left', () => {
    expect(formatDaysLeft(cert({ status: 'expired', daysLeft: -3 }))).toBe('Expired 3 days ago');
    expect(formatDaysLeft(cert({ daysLeft: 1, status: 'critical' }))).toBe('1 day left');
    expect(formatDaysLeft(cert({ notAfter: undefined, status: 'invalid' }))).toBe('Not issued');
  });

  it('computes validity fraction', () => {
    const c = cert({ notBefore: '2026-01-01T00:00:00Z', notAfter: '2026-01-11T00:00:00Z' });
    expect(validityFraction(c, Date.parse('2026-01-06T00:00:00Z'))).toBeCloseTo(0.5);
    expect(validityFraction(c, Date.parse('2027-01-01T00:00:00Z'))).toBe(1);
  });

  it('sorts by status then expiry', () => {
    const list = [
      cert({ id: 'a', status: 'ok', notAfter: '2027-03-01T00:00:00Z' }),
      cert({ id: 'b', status: 'expired', notAfter: '2026-01-01T00:00:00Z' }),
      cert({ id: 'c', status: 'warning', notAfter: '2026-12-01T00:00:00Z' }),
    ];
    expect(sortCerts(list, 'status', 'asc').map((c) => c.id)).toEqual(['b', 'c', 'a']);
    expect(sortCerts(list, 'expires', 'desc').map((c) => c.id)).toEqual(['a', 'c', 'b']);
  });

  it('groups by issuer, worst group first', () => {
    const list = [
      cert({ id: 'a', issuerOrganization: "Let's Encrypt" }),
      cert({ id: 'b', selfSigned: true, status: 'expired' }),
      cert({ id: 'c', certManager: { issuerRef: { apiVersion: 'cert-manager.io/v1', kind: 'ClusterIssuer', name: 'le' } } as any }),
    ];
    expect(issuerLabel(list[2])).toBe('ClusterIssuer le');
    const g = groupByIssuer(list);
    expect(g[0].issuer).toBe('Self-signed');
    expect(g.map((x) => x.issuer)).toContain("Let's Encrypt");
  });

  it('bins certificates on the heat strip', () => {
    const bins = timelineBins([
      cert({ id: 'e', status: 'expired', daysLeft: -2 }),
      cert({ id: 'w0', status: 'critical', daysLeft: 3 }),
      cert({ id: 'w3', status: 'warning', daysLeft: 22 }),
      cert({ id: 'late', daysLeft: 400 }),
      cert({ id: 'none', notAfter: undefined, status: 'invalid' }),
    ]);
    expect(bins).toHaveLength(15);
    expect(bins[0].certs.map((c) => c.id)).toEqual(['e']);
    expect(bins[1].certs.map((c) => c.id)).toEqual(['w0']);
    expect(bins[4].certs.map((c) => c.id)).toEqual(['w3']);
    expect(bins[14].certs.map((c) => c.id)).toEqual(['late']);
    expect(bins[1].tone).toBe('critical');
  });

  it('scopes the inspector card to TLS resources', () => {
    expect(inspectorScope({ kind: 'Secret', type: 'kubernetes.io/tls', metadata: { name: 's', namespace: 'n' } })).toEqual({ kind: 'Secret', namespace: 'n', name: 's' });
    expect(inspectorScope({ kind: 'Secret', type: 'Opaque', metadata: { name: 's' } })).toBeNull();
    expect(inspectorScope({ kind: 'Ingress', spec: {}, metadata: { name: 'i' } })).toBeNull();
    expect(inspectorScope({ kind: 'Certificate', apiVersion: 'cert-manager.io/v1', metadata: { name: 'c', namespace: 'n' } })).not.toBeNull();
  });
});
