import { apiClient } from './client';
import type {
  HealthAuditReport,
  HealthIgnore,
  HealthIssuesReport,
  HealthRef,
  HealthSeverity,
  ResourceHealth,
} from '../../types/health';

const BASE = '/cluster-health';

export interface IssueQuery {
  namespaces?: string[];
  severities?: HealthSeverity[];
  kinds?: string[];
  search?: string;
  refresh?: boolean;
}

export async function getHealthIssues(cluster: string, q: IssueQuery = {}, signal?: AbortSignal): Promise<HealthIssuesReport> {
  const params: Record<string, string> = { cluster };
  if (q.namespaces?.length) params.namespaces = q.namespaces.join(',');
  if (q.severities?.length) params.severities = q.severities.join(',');
  if (q.kinds?.length) params.kinds = q.kinds.join(',');
  if (q.search) params.search = q.search;
  if (q.refresh) params.refresh = '1';
  const { data } = await apiClient.getAxios().get(`${BASE}/issues`, { params, signal });
  return data;
}

export async function getHealthAudit(
  cluster: string,
  q: { namespaces?: string[]; includeSystem?: boolean; refresh?: boolean } = {},
  signal?: AbortSignal,
): Promise<HealthAuditReport> {
  const params: Record<string, string> = { cluster };
  if (q.namespaces?.length) params.namespaces = q.namespaces.join(',');
  if (q.includeSystem) params.includeSystem = 'true';
  if (q.refresh) params.refresh = '1';
  const { data } = await apiClient.getAxios().get(`${BASE}/audit`, { params, signal });
  return data;
}

export async function getResourceHealth(cluster: string, ref: HealthRef, signal?: AbortSignal): Promise<ResourceHealth> {
  const params: Record<string, string> = { cluster, kind: ref.kind, name: ref.name };
  if (ref.namespace) params.namespace = ref.namespace;
  if (ref.apiVersion) params.apiVersion = ref.apiVersion;
  const { data } = await apiClient.getAxios().get(`${BASE}/resource`, { params, signal });
  return data;
}

export async function listHealthIgnores(cluster: string): Promise<HealthIgnore[]> {
  const { data } = await apiClient.getAxios().get(`${BASE}/ignores`, { params: { cluster } });
  return data || [];
}

export async function addHealthIgnore(ig: HealthIgnore): Promise<HealthIgnore[]> {
  const { data } = await apiClient.getAxios().post(`${BASE}/ignores`, ig);
  return data || [];
}

export async function removeHealthIgnore(ig: HealthIgnore): Promise<HealthIgnore[]> {
  const { data } = await apiClient.getAxios().delete(`${BASE}/ignores`, { data: ig });
  return data || [];
}
