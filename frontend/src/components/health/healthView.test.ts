import { describe, expect, it } from 'vitest';
import { ageLabel, badgeFor, findingsByResource, groupBySeverity, ignoreFor, matchIssue, scoreTone } from './healthView';
import type { HealthFinding, HealthIssue } from '../../types/health';

const issue = (severity: HealthIssue['severity'], name: string, ns = 'app'): HealthIssue => ({
  id: name, rule: 'r', severity, title: 'Container crash-looping', reason: 'CrashLoopBackOff', message: 'exit 1',
  resource: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: ns, name }, count: 1, since: '', source: 'state',
});

const finding = (checkId: string, severity: HealthFinding['severity'], name: string, ignored = false): HealthFinding => ({
  checkId, severity, category: 'security', resource: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'app', name }, message: '', ignored,
});

describe('health view', () => {
  it('groups by severity in order and drops empty groups', () => {
    const g = groupBySeverity([issue('info', 'a'), issue('critical', 'b'), issue('critical', 'c')]);
    expect(g.map((x) => x.severity)).toEqual(['critical', 'info']);
    expect(g[0].items).toHaveLength(2);
  });

  it('formats ages and ignores zero times', () => {
    const now = Date.parse('2026-11-04T12:00:00Z');
    expect(ageLabel('2026-11-04T11:55:00Z', now)).toBe('5m');
    expect(ageLabel('2026-11-01T12:00:00Z', now)).toBe('3d');
    expect(ageLabel('0001-01-01T00:00:00Z', now)).toBe('');
    expect(ageLabel(undefined, now)).toBe('');
  });

  it('tones scores', () => {
    expect(scoreTone(90)).toBe('good');
    expect(scoreTone(70)).toBe('fair');
    expect(scoreTone(10)).toBe('poor');
  });

  it('groups findings per resource, worst first, ignored last', () => {
    const g = findingsByResource([
      finding('image-tag', 'warning', 'a'),
      finding('privileged', 'critical', 'b', true),
      finding('pdb', 'info', 'b'),
      finding('privileged', 'critical', 'c'),
    ]);
    expect(g.map((x) => x.resource.name)).toEqual(['c', 'a', 'b']);
    expect(g[2].findings[1].ignored).toBe(true);
  });

  it('builds ignores at both scopes', () => {
    const f = finding('pdb', 'info', 'web');
    expect(ignoreFor('c', f, 'namespace')).toEqual({ cluster: 'c', checkId: 'pdb', namespace: 'app' });
    expect(ignoreFor('c', f, 'resource')).toMatchObject({ kind: 'Deployment', name: 'web' });
  });

  it('matches issues by text and namespace', () => {
    expect(matchIssue(issue('critical', 'web'), 'crash', '')).toBe(true);
    expect(matchIssue(issue('critical', 'web'), 'oom', '')).toBe(false);
    expect(matchIssue(issue('critical', 'web', 'x'), '', 'app')).toBe(false);
  });

  it('badges critical first', () => {
    expect(badgeFor({ critical: 2, warning: 5, total: 7 })).toEqual({ count: 2, tone: 'danger' });
    expect(badgeFor({ critical: 0, warning: 5, total: 5 })).toEqual({ count: 5, tone: 'warning' });
    expect(badgeFor({ critical: 0, warning: 0, total: 1 })).toBeNull();
  });
});
