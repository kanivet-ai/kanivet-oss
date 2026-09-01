import { apiClient } from './client';

export interface VClusterInfo {
  name: string;
  namespace: string;
  phase: string;
  ready: boolean;
}

export interface VClusterConnection {
  id: string;
  host: string;
  namespace: string;
  name: string;
  localPort: number;
}

export async function listVClusters(host: string): Promise<VClusterInfo[]> {
  const result = await apiClient.request('/cluster/vclusters', { cluster: host }, false);
  return result.vclusters || [];
}

export async function connectVCluster(host: string, namespace: string, name: string): Promise<VClusterConnection> {
  const result = await apiClient.getAxios().post('/cluster/vclusters/connect', { host, namespace, name });
  return result.data;
}

export async function disconnectVCluster(id: string): Promise<void> {
  await apiClient.getAxios().delete(`/cluster/vclusters/${encodeURIComponent(id)}`);
}
