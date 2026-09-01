import { ClusterGroup } from './types';
import { apiClient } from './client';

export async function getClusterGroups(): Promise<ClusterGroup[]> {
  const response = await apiClient.getAxios().get('/cluster-groups');
  return response.data.groups || [];
}

export async function createClusterGroup(name: string, description: string): Promise<ClusterGroup> {
  const response = await apiClient.getAxios().post('/cluster-groups', { name, description });
  return response.data;
}

export async function updateClusterGroup(id: number, name: string, description: string): Promise<void> {
  await apiClient.getAxios().put(`/cluster-groups/${id}`, { name, description });
}

export async function updateGroupOrder(groupOrders: Array<{ id: number; order: number }>): Promise<void> {
  await apiClient.getAxios().put('/cluster-groups/order', groupOrders);
}

export async function deleteClusterGroup(id: number): Promise<void> {
  await apiClient.getAxios().delete(`/cluster-groups/${id}`);
}

export async function assignClusterToGroup(clusterName: string, groupId: number): Promise<void> {
  await apiClient.getAxios().post('/cluster-groups/assign', { clusterName, groupId });
}

export async function removeClusterFromGroup(clusterName: string): Promise<void> {
  await apiClient.getAxios().delete(`/cluster-groups/assign/${encodeURIComponent(clusterName)}`);
}

export async function getClustersByGroup(): Promise<Record<string, string[]>> {
  const response = await apiClient.getAxios().get('/cluster-groups/clusters');
  return response.data.clustersByGroup || {};
}

export async function getClusterAssignments(): Promise<Record<string, number>> {
  const response = await apiClient.getAxios().get('/cluster-groups/assignments');
  return response.data.assignments || {};
}

export async function getClusterAliases(): Promise<Record<string, string>> {
  const response = await apiClient.getAxios().get('/cluster-aliases');
  return response.data.aliases || {};
}

export async function setClusterAlias(clusterName: string, alias: string): Promise<void> {
  await apiClient.getAxios().post('/cluster-aliases', { clusterName, alias });
}

export async function deleteClusterAlias(clusterName: string): Promise<void> {
  await apiClient.getAxios().delete(`/cluster-aliases/${encodeURIComponent(clusterName)}`);
}
