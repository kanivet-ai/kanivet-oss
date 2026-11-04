import type { GitOpsItem } from '../../services/api/integrations';

export type ToolFilter = 'all' | 'argocd' | 'flux';
export type KindFilter = 'all' | 'Kustomization' | 'HelmRelease' | 'sources' | 'Application';

export interface GitOpsFilter {
  tool: ToolFilter;
  kind: KindFilter;
  health: string | null;
  search: string;
}

const SOURCE_KINDS = new Set(['GitRepository', 'OCIRepository', 'HelmRepository', 'Bucket']);

export const isSourceKind = (kind: string) => SOURCE_KINDS.has(kind);

/** Filters unified GitOps rows; order is kept (the backend sorts failing first). */
export function filterGitOps(items: GitOpsItem[], f: GitOpsFilter): GitOpsItem[] {
  const q = f.search.trim().toLowerCase();
  return items.filter((it) => {
    if (f.tool !== 'all' && it.tool !== f.tool) return false;
    if (f.kind === 'sources' ? !isSourceKind(it.kind) : f.kind !== 'all' && it.kind !== f.kind) return false;
    if (f.health && it.health !== f.health) return false;
    if (!q) return true;
    return [it.name, it.namespace, it.kind, it.source, it.revision, it.detail]
      .some((v) => (v || '').toLowerCase().includes(q));
  });
}

/** Counts rows per unified health within the current tool/kind/search scope. */
export function countByHealth(items: GitOpsItem[], f: GitOpsFilter): Record<string, number> {
  const out: Record<string, number> = {};
  for (const it of filterGitOps(items, { ...f, health: null })) out[it.health] = (out[it.health] || 0) + 1;
  return out;
}
