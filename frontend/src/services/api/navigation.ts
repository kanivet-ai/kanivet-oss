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
  try {
    const response = await apiClient.getAxios().post(`/navigation/back/${encodeURIComponent(tabId)}`);
    return response.data.entry;
  } catch (error: any) {
    if (error.response?.status === 404 && error.response?.data?.error === 'No previous navigation entry') return null;
    throw error;
  }
}

export async function navigateForward(tabId: string): Promise<NavigationEntry | null> {
  try {
    const response = await apiClient.getAxios().post(`/navigation/forward/${encodeURIComponent(tabId)}`);
    return response.data.entry;
  } catch (error: any) {
    if (error.response?.status === 404 && error.response?.data?.error === 'No forward navigation entry') return null;
    throw error;
  }
}

export async function clearNavigationHistory(tabId: string): Promise<void> {
  await apiClient.getAxios().delete(`/navigation/clear/${encodeURIComponent(tabId)}`);
}
