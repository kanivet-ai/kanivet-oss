import { describe, expect, it } from 'vitest';
import { gitopsOwnerOf } from './gitopsOwner';

describe('gitopsOwnerOf', () => {
  it('reads a Flux Kustomization owner', () => {
    const o = gitopsOwnerOf({ labels: { 'kustomize.toolkit.fluxcd.io/name': 'apps', 'kustomize.toolkit.fluxcd.io/namespace': 'flux-system' } });
    expect(o).toMatchObject({ tool: 'flux', kind: 'Kustomization', name: 'apps', namespace: 'flux-system' });
  });

  it('prefers the HelmRelease over the Kustomization that applied it', () => {
    const o = gitopsOwnerOf({ labels: { 'helm.toolkit.fluxcd.io/name': 'redis', 'helm.toolkit.fluxcd.io/namespace': 'db', 'kustomize.toolkit.fluxcd.io/name': 'apps' } });
    expect(o).toMatchObject({ kind: 'HelmRelease', name: 'redis', namespace: 'db' });
  });

  it('reads an Argo tracking id, with and without an app namespace', () => {
    expect(gitopsOwnerOf({ annotations: { 'argocd.argoproj.io/tracking-id': 'guestbook:apps/Deployment:default/web' } }, 'gitops'))
      .toMatchObject({ tool: 'argocd', name: 'guestbook', namespace: 'gitops' });
    expect(gitopsOwnerOf({ annotations: { 'argocd.argoproj.io/tracking-id': 'team-a_guestbook:apps/Deployment:default/web' } }))
      .toMatchObject({ name: 'guestbook', namespace: 'team-a' });
  });

  it('does not trust the Helm instance label', () => {
    expect(gitopsOwnerOf({ labels: { 'app.kubernetes.io/instance': 'redis' } })).toBeNull();
    expect(gitopsOwnerOf(undefined)).toBeNull();
  });
});
