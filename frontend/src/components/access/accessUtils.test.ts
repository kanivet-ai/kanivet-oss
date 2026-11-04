import { describe, expect, it } from 'vitest';
import {
  cellTone,
  COMMON_RESOURCES,
  countBySeverity,
  filterRows,
  parseResourceInput,
  parseSubjectKey,
  pathText,
  resourceLabel,
  subjectKey,
} from './accessUtils';
import type { MatrixRow } from '../../types/rbac';

const path = (verbs: string[], extra: Partial<any> = {}) => ({
  binding: { kind: 'RoleBinding', name: 'ci', namespace: 'team' },
  role: { kind: 'Role', name: 'exec', namespace: 'team' },
  scope: 'team',
  rule: { verbs, resources: ['pods'] },
  ...extra,
});

describe('subject keys', () => {
  it('round-trips every kind', () => {
    for (const s of [
      { kind: 'ServiceAccount', name: 'ci', namespace: 'team' },
      { kind: 'User', name: 'system:serviceaccount:a:b' },
      { kind: 'Group', name: 'ops' },
    ]) {
      expect(parseSubjectKey(subjectKey(s))).toEqual(s);
    }
    expect(parseSubjectKey('Pod:x')).toBeNull();
  });
});

describe('resources', () => {
  it('labels rows the kubectl way', () => {
    expect(resourceLabel({ group: '', resource: 'pods' })).toBe('pods');
    expect(resourceLabel({ group: 'apps', resource: 'deployments' })).toBe('deployments.apps');
    expect(resourceLabel({ group: '*', resource: '*' })).toBe('*');
  });
  it('parses free text', () => {
    expect(parseResourceInput('pods/exec')).toEqual({ resource: 'pods', subresource: 'exec' });
    expect(parseResourceInput('Deployments.apps/scale')).toEqual({ resource: 'deployments', group: 'apps', subresource: 'scale' });
  });
  it('labels common options with their group', () => {
    expect(COMMON_RESOURCES.find((o) => o.resource === 'deployments')?.label).toBe('deployments.apps');
    expect(COMMON_RESOURCES.find((o) => o.resource === 'pods/exec')?.label).toBe('pods/exec');
  });
});

describe('cellTone', () => {
  const row: MatrixRow = { group: '', resource: 'pods', cells: {} };
  it('distinguishes full, limited, wildcard and none', () => {
    expect(cellTone(row, undefined)).toBe('none');
    expect(cellTone(row, { allowed: true, paths: [path(['get'])] as any })).toBe('full');
    expect(cellTone(row, { allowed: true, limited: true, paths: [path(['get'])] as any })).toBe('limited');
    expect(cellTone(row, { allowed: true, paths: [path(['*'])] as any })).toBe('wildcard');
    expect(cellTone({ ...row, resource: '*' }, { allowed: true, paths: [path(['get'])] as any })).toBe('wildcard');
  });
});

describe('pathText', () => {
  it('explains a grant', () => {
    expect(pathText(path(['get'], { via: 'Group system:serviceaccounts' }) as any)).toBe(
      'RoleBinding team/ci → Role exec (in team) as Group system:serviceaccounts',
    );
    expect(pathText(path(['get'], { scope: '', rule: { verbs: ['get'], source: 'view-pods', resourceNames: ['a'] } }) as any)).toBe(
      'RoleBinding team/ci → Role exec via view-pods [a] (cluster-wide)',
    );
  });
});

describe('filterRows', () => {
  const rows: MatrixRow[] = [
    { group: '', resource: 'pods', cells: { get: { allowed: true } } },
    { group: 'apps', resource: 'deployments', cells: { list: { allowed: true } } },
  ];
  it('filters by text and verb', () => {
    expect(filterRows(rows, 'apps').map((r) => r.resource)).toEqual(['deployments']);
    expect(filterRows(rows, '', ['get']).map((r) => r.resource)).toEqual(['pods']);
  });
});

describe('countBySeverity', () => {
  it('counts', () => {
    expect(countBySeverity([{ severity: 'high' }, { severity: 'high' }, { severity: 'critical' }] as any)).toEqual({ critical: 1, high: 2, medium: 0 });
  });
});
