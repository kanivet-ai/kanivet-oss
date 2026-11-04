import { describe, expect, it } from 'vitest';
import { badgeTone, compareMinor, matchesQuery, objectDeprecations, sourceCounts, sourceLabel, LAST_APPLIED } from './upgradeView';
import type { UpgradeCatalog } from '../../types/upgrade';

const catalog: UpgradeCatalog = {
  source: '',
  reviewed: '2026-11',
  latestRelease: '1.37',
  apis: [
    { group: 'extensions', version: 'v1beta1', kind: 'Ingress', resource: 'ingresses', deprecatedIn: '1.14', removedIn: '1.22', replacement: 'networking.k8s.io/v1', hint: '' },
    { group: 'batch', version: 'v1beta1', kind: 'CronJob', resource: 'cronjobs', deprecatedIn: '1.21', removedIn: '1.25', replacement: 'batch/v1', hint: '' },
    { group: '', version: 'v1', kind: 'Endpoints', resource: 'endpoints', deprecatedIn: '1.33', removedIn: '', replacement: 'discovery.k8s.io/v1', hint: '' },
  ],
};

describe('upgradeView', () => {
  it('compares minors numerically', () => {
    expect(compareMinor('1.9', '1.10')).toBeLessThan(0);
    expect(compareMinor('1.25', '1.25')).toBe(0);
  });

  it('finds deprecated APIs in apiVersion and last-applied', () => {
    const ing = {
      apiVersion: 'networking.k8s.io/v1',
      kind: 'Ingress',
      metadata: { annotations: { [LAST_APPLIED]: JSON.stringify({ apiVersion: 'extensions/v1beta1', kind: 'Ingress' }) } },
    };
    const found = objectDeprecations(catalog, ing);
    expect(found).toHaveLength(1);
    expect(found[0].via).toBe('last-applied');
    expect(objectDeprecations(catalog, { apiVersion: 'batch/v1beta1', kind: 'CronJob' })[0].via).toBe('apiVersion');
    expect(objectDeprecations(catalog, { apiVersion: 'batch/v1', kind: 'CronJob', metadata: { annotations: { [LAST_APPLIED]: '{bad' } } })).toEqual([]);
    expect(objectDeprecations(null, ing)).toEqual([]);
  });

  it('tones badges by removal', () => {
    expect(badgeTone(catalog.apis[1], '1.24')).toBe('warning');
    expect(badgeTone(catalog.apis[1], '1.25')).toBe('danger');
    expect(badgeTone(catalog.apis[2], '1.33')).toBe('info');
  });

  it('labels and counts sources and matches queries', () => {
    expect(sourceLabel({ source: 'helm', helmRelease: 'shop' })).toBe('Helm release shop');
    const res = [
      { apiVersion: 'a', kind: 'K', name: 'x', namespace: 'ns', source: 'helm' as const, helmRelease: 'shop', severity: 'blocker' as const },
      { apiVersion: 'a', kind: 'K', name: 'y', source: 'last-applied' as const, severity: 'warning' as const },
    ];
    expect(sourceCounts(res)).toEqual({ live: 0, 'last-applied': 1, helm: 1 });
    const f = { apiVersion: 'batch/v1beta1', kind: 'CronJob', replacement: 'batch/v1', resources: res };
    expect(matchesQuery(f, 'cronjob')).toBe(true);
    expect(matchesQuery(f, 'shop')).toBe(true);
    expect(matchesQuery(f, 'nothing')).toBe(false);
  });
});
