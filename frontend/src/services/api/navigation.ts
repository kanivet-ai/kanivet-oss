import { NavigationEntry } from './types';
import { apiClient } from './client';

export async function addNavigationEntry(tabId: string, clusterId: string, entry: NavigationEntry): Promise<any> {
  return await apiClient.getAxios().post(`/navigation/add/${encodeURIComponent(tabId)}`, { clusterId, entry });
}

export async function getNavigationHistory(tabId: string): Promise<any[]> {
  const response = await apiClient.getAxios().get(`/navigation/history/${encodeURIComponent(tabId)}`);
  return response.data.history;
}

export async function navigateBack(tabId: string): Promise<NavigationEntry | null> {
  const response = await apiClient.getAxios().post(`/navigation/back/${encodeURIComponent(tabId)}`);
  return response.data.entry;
}

export async function navigateForward(tabId: string): Promise<NavigationEntry | null> {
  const response = await apiClient.getAxios().post(`/navigation/forward/${encodeURIComponent(tabId)}`);
  return response.data.entry;
}

export async function clearNavigationHistory(tabId: string): Promise<void> {
  await apiClient.getAxios().delete(`/navigation/clear/${encodeURIComponent(tabId)}`);
}
