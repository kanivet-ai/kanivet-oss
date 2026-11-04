import { describe, expect, it } from 'vitest';
import { countByHealth, filterGitOps } from './gitopsFilter';
import { shortRevision } from './statusParts';
import type { GitOpsItem } from '../../services/api/integrations';

const row = (p: Partial<GitOpsItem>): GitOpsItem => ({ tool: 'flux', kind: 'Kustomization', apiVersion: 'x/v1', namespace: 'flux-system', name: 'a', health: 'Healthy', suspended: false, ...p });

const items = [
  row({ name: 'apps', health: 'Failing' }),
  row({ name: 'redis', kind: 'HelmRelease' }),
  row({ name: 'repo', kind: 'GitRepository', source: 'https://github.com/org/fleet' }),
  row({ tool: 'argocd', kind: 'Application', name: 'guestbook', namespace: 'argocd', health: 'Progressing' }),
];
const base = { tool: 'all' as const, kind: 'all' as const, health: null, search: '' };

describe('filterGitOps', () => {
  it('filters by tool, kind group, health and search', () => {
    expect(filterGitOps(items, { ...base, tool: 'flux' })).toHaveLength(3);
    expect(filterGitOps(items, { ...base, kind: 'sources' }).map((i) => i.name)).toEqual(['repo']);
    expect(filterGitOps(items, { ...base, health: 'Failing' }).map((i) => i.name)).toEqual(['apps']);
    expect(filterGitOps(items, { ...base, search: 'fleet' }).map((i) => i.name)).toEqual(['repo']);
  });

  it('counts health within scope, ignoring the health filter itself', () => {
    expect(countByHealth(items, { ...base, tool: 'flux', health: 'Failing' })).toEqual({ Failing: 1, Healthy: 2 });
  });
});

describe('shortRevision', () => {
  it('shortens Flux and git revisions', () => {
    expect(shortRevision('main@sha1:0123456789abcdef0123456789abcdef01234567')).toBe('main@0123456');
    expect(shortRevision('sha256:abcdef0123456789')).toBe('abcdef0');
    expect(shortRevision('0123456789abcdef0123456789abcdef01234567')).toBe('0123456');
    expect(shortRevision('6.5.4')).toBe('6.5.4');
  });
});
