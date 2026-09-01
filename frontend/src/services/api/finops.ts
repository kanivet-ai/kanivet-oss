import { apiClient } from './client';
import { streamJsonLines } from './sseFetch';

export async function getFinOpsDashboard(cluster: string): Promise<any> {
  const response = await apiClient.getAxios().get('/finops/dashboard', { params: { cluster } });
  return response.data.data;
}

export async function streamFinOpsDashboard(
  cluster: string,
  onChunk: (type: string, data: any) => void,
  signal?: AbortSignal,
): Promise<void> {
  return streamJsonLines(`/finops/dashboard/stream?cluster=${encodeURIComponent(cluster)}`, 'finops', onChunk, signal);
}

export async function getFinOpsSummary(cluster: string): Promise<any> {
  const response = await apiClient.getAxios().get('/finops/summary', { params: { cluster } });
  return response.data.data;
}

export async function getFinOpsNodeCosts(cluster: string): Promise<any[]> {
  const response = await apiClient.getAxios().get('/finops/nodes', { params: { cluster } });
  return response.data.data || [];
}

export async function getFinOpsPodCosts(cluster: string, namespace?: string): Promise<any[]> {
  const params: any = { cluster };
  if (namespace) params.namespace = namespace;
  const response = await apiClient.getAxios().get('/finops/pods', { params });
  return response.data.data || [];
}

export async function getFinOpsNamespaceCosts(cluster: string): Promise<any[]> {
  const response = await apiClient.getAxios().get('/finops/namespaces', { params: { cluster } });
  return response.data.data || [];
}

export async function getFinOpsWorkloadCosts(cluster: string, namespace?: string): Promise<any[]> {
  const params: any = { cluster };
  if (namespace) params.namespace = namespace;
  const response = await apiClient.getAxios().get('/finops/workloads', { params });
  return response.data.data || [];
}

export async function getFinOpsRecommendations(cluster: string): Promise<any[]> {
  const response = await apiClient.getAxios().get('/finops/recommendations', { params: { cluster } });
  return response.data.data || [];
}

export async function getFinOpsResourceCost(cluster: string, kind: string, namespace: string, name: string): Promise<any> {
  const response = await apiClient.getAxios().get(
    `/finops/resource/${encodeURIComponent(kind)}/${encodeURIComponent(namespace || '_')}/${encodeURIComponent(name)}`,
    { params: { cluster } }
  );
  return response.data.data;
}

export async function getFinOpsPricingDebug(cluster: string): Promise<any> {
  const response = await apiClient.getAxios().get('/finops/pricing-debug', { params: { cluster } });
  return response.data.data;
}

export async function preloadFinOpsPricing(cluster: string): Promise<void> {
  await apiClient.getAxios().post('/finops/preload-pricing', null, { params: { cluster } });
}
