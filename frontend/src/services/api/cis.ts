import { apiClient } from './client';
import type { CisOverride, CisReport, ProviderInfo } from '../../types/cis';

const BASE = '/cluster-health/cis';

export async function getCisReport(
  cluster: string,
  q: { profile?: string; refresh?: boolean } = {},
  signal?: AbortSignal,
): Promise<CisReport> {
  const params: Record<string, string> = { cluster };
  if (q.profile) params.profile = q.profile;
  if (q.refresh) params.refresh = '1';
  const { data } = await apiClient.getAxios().get(BASE, { params, signal });
  return data;
}

export async function getProvider(
  cluster: string,
  signal?: AbortSignal,
): Promise<ProviderInfo> {
  const { data } = await apiClient
    .getAxios()
    .get(`${BASE}/provider`, { params: { cluster }, signal });
  return data;
}

export async function getCisOverride(cluster: string): Promise<CisOverride> {
  const { data } = await apiClient
    .getAxios()
    .get(`${BASE}/override`, { params: { cluster } });
  return data || {};
}

export async function setCisOverride(
  cluster: string,
  o: CisOverride,
): Promise<CisOverride> {
  const { data } = await apiClient
    .getAxios()
    .put(`${BASE}/override`, o, { params: { cluster } });
  return data || {};
}
