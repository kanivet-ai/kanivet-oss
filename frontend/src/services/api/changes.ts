import { apiClient } from './client';
import type { ChangeFeed, ChangeFeedFilters, ChangeHistory } from '../../types/changes';

export interface HistoryTarget {
  cluster: string;
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
}

export async function getResourceHistory(t: HistoryTarget): Promise<ChangeHistory> {
  const params: Record<string, string> = {
    cluster: t.cluster,
    group: t.group,
    version: t.version,
    kind: t.kind,
    name: t.name,
  };
  if (t.namespace) params.namespace = t.namespace;
  return await apiClient.request('/changes/resource', params, false);
}

export async function getChangeSnapshot(cluster: string, id: string): Promise<string> {
  const resp = await apiClient.request('/changes/snapshot', { cluster, id }, true);
  return resp?.snapshot || '';
}

export async function getChangeFeed(cluster: string, f: ChangeFeedFilters = {}): Promise<ChangeFeed> {
  const params: Record<string, string> = { cluster };
  if (f.namespaces?.length) params.namespaces = f.namespaces.join(',');
  if (f.kinds?.length) params.kinds = f.kinds.join(',');
  if (f.categories?.length) params.categories = f.categories.join(',');
  if (f.sources?.length) params.sources = f.sources.join(',');
  if (f.since) params.since = f.since.toISOString();
  if (f.limit) params.limit = String(f.limit);
  return await apiClient.request('/changes/feed', params, false);
}
