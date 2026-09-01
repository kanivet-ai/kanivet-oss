import { ClusterStatus } from '../../types';
import { ClusterInfo } from './types';
import { apiClient } from './client';
import { streamJsonLines } from './sseFetch';

export async function getClusters(): Promise<ClusterInfo[]> {
  const result = await apiClient.request('/clusters', undefined, false);
  return result.clusters || [];
}

export async function releaseCluster(cluster: string): Promise<void> {
  await apiClient.getAxios().post('/clusters/release', { cluster });
}

export async function refreshClusters(): Promise<ClusterInfo[]> {
  const result = await apiClient.getAxios().post('/clusters/refresh');
  return result.data?.clusters || [];
}

export async function getClustersStatus(): Promise<ClusterStatus[]> {
  const result = await apiClient.request('/clusters/status');
  return result.clusters || [];
}

export async function getClusterStatus(cluster: string, force = false): Promise<ClusterStatus> {
  const params: Record<string, string> = { cluster };
  if (force) params.force = 'true';
  return await apiClient.request('/cluster/status', params);
}

export async function getBatchClusterStatus(clusters: string[], force = false): Promise<Record<string, ClusterStatus>> {
  const result = await apiClient.getAxios().post('/clusters/status/batch', { clusters, force });
  return result.data?.statuses || {};
}

export async function getCachedBatchClusterStatus(clusters: string[]): Promise<Record<string, ClusterStatus>> {
  const result = await apiClient.getAxios().post('/clusters/status/batch', { clusters, cachedOnly: true });
  return result.data?.statuses || {};
}

export async function getClusterDashboard(cluster: string): Promise<any> {
  return await apiClient.request('/cluster/dashboard', { cluster }, false);
}

export async function streamClusterDashboard(
  cluster: string,
  onChunk: (type: string, data: any) => void,
  signal?: AbortSignal,
): Promise<void> {
  return streamJsonLines(`/cluster/dashboard/stream?cluster=${encodeURIComponent(cluster)}`, 'dashboard', onChunk, signal);
}

export async function getCategories(cluster: string): Promise<any[]> {
  const result = await apiClient.request('/resources/categories', { cluster });
  return result.categories || [];
}

export async function getResources(cluster: string, category: string, skipCounts: boolean = false): Promise<any[]> {
  const params: any = { cluster, category };
  if (skipCounts) params.skipCounts = 'true';
  const result = await apiClient.request('/resources/list', params);
  return result.resources || [];
}

export async function getNamespaces(cluster: string): Promise<string[]> {
  const result = await apiClient.request('/cluster/namespaces', { cluster });
  return result.namespaces || [];
}
