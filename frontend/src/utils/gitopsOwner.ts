/* Which GitOps object manages a resource, read from its labels and
   annotations. Mirrors backend/internal/gitops/owner.go (OwnerOf). */

export interface GitOpsOwner {
  tool: 'flux' | 'argocd';
  apiVersion: string;
  kind: 'Kustomization' | 'HelmRelease' | 'Application';
  namespace: string;
  name: string;
  via: string;
}

const argoRef = (value: string, defaultNs: string): { namespace: string; name: string } | null => {
  const v = value.trim();
  if (!v) return null;
  const i = v.indexOf('_');
  if (i > 0 && i < v.length - 1) return { namespace: v.slice(0, i), name: v.slice(i + 1) };
  return { namespace: defaultNs, name: v };
};

/**
 * Flux labels win (the controllers stamp them on every applied object), then
 * Argo's tracking-id annotation. The app.kubernetes.io/instance label is not
 * trusted: Helm charts set it too.
 */
export function gitopsOwnerOf(
  metadata: { labels?: Record<string, string>; annotations?: Record<string, string> } | undefined,
  argoNamespace = 'argocd',
): GitOpsOwner | null {
  const labels = metadata?.labels || {};
  const annotations = metadata?.annotations || {};
  const helm = labels['helm.toolkit.fluxcd.io/name'];
  if (helm) {
    return { tool: 'flux', apiVersion: 'helm.toolkit.fluxcd.io/v2', kind: 'HelmRelease', name: helm, namespace: labels['helm.toolkit.fluxcd.io/namespace'] || '', via: 'helm.toolkit.fluxcd.io/name' };
  }
  const ks = labels['kustomize.toolkit.fluxcd.io/name'];
  if (ks) {
    return { tool: 'flux', apiVersion: 'kustomize.toolkit.fluxcd.io/v1', kind: 'Kustomization', name: ks, namespace: labels['kustomize.toolkit.fluxcd.io/namespace'] || '', via: 'kustomize.toolkit.fluxcd.io/name' };
  }
  const ns = argoNamespace || 'argocd';
  const tracking = annotations['argocd.argoproj.io/tracking-id'];
  if (tracking) {
    const ref = argoRef(tracking.split(':')[0], ns);
    if (ref) return { tool: 'argocd', apiVersion: 'argoproj.io/v1alpha1', kind: 'Application', ...ref, via: 'argocd.argoproj.io/tracking-id' };
  }
  const instance = annotations['argocd.argoproj.io/instance'];
  if (instance) {
    const ref = argoRef(instance, ns);
    if (ref) return { tool: 'argocd', apiVersion: 'argoproj.io/v1alpha1', kind: 'Application', ...ref, via: 'argocd.argoproj.io/instance' };
  }
  return null;
}
