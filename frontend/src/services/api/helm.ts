import logger from '../../utils/logger';
import { HelmRelease, HelmReleaseDetail, HelmHistoryResponse } from '../../types/helm';
import { apiClient } from './client';

export async function getHelmReleases(cluster: string, namespace?: string): Promise<HelmRelease[]> {
  try {
    const params: Record<string, string> = { cluster };
    if (namespace) params.namespace = namespace;
    else params.allNamespaces = 'true';
    const response = await apiClient.getAxios().get('/helm/releases', { params });
    return response.data;
  } catch (error) {
    logger.error('Failed to get Helm releases', { error, cluster, namespace });
    throw error;
  }
}

export async function getHelmRelease(cluster: string, namespace: string, name: string): Promise<HelmReleaseDetail> {
  try {
    const response = await apiClient.getAxios().get(`/helm/releases/${namespace}/${name}`, { params: { cluster } });
    return response.data;
  } catch (error) {
    logger.error('Failed to get Helm release', { error, cluster, namespace, name });
    throw error;
  }
}

export async function getHelmReleaseValues(cluster: string, namespace: string, name: string, allValues = false): Promise<Record<string, any>> {
  try {
    const response = await apiClient.getAxios().get(`/helm/releases/${namespace}/${name}/values`, {
      params: { cluster, all: allValues ? 'true' : 'false' },
    });
    return response.data;
  } catch (error) {
    logger.error('Failed to get Helm release values', { error, cluster, namespace, name });
    throw error;
  }
}

export async function getHelmReleaseManifest(cluster: string, namespace: string, name: string): Promise<string> {
  try {
    const response = await apiClient.getAxios().get(`/helm/releases/${namespace}/${name}/manifest`, { params: { cluster } });
    return response.data.manifest;
  } catch (error) {
    logger.error('Failed to get Helm release manifest', { error, cluster, namespace, name });
    throw error;
  }
}

export async function getHelmReleaseHistory(cluster: string, namespace: string, name: string, limit?: number): Promise<HelmHistoryResponse> {
  try {
    const params: Record<string, any> = { cluster };
    if (limit) params.limit = limit;
    const response = await apiClient.getAxios().get(`/helm/releases/${namespace}/${name}/history`, { params });
    if (Array.isArray(response.data)) {
      return { entries: response.data, hasMore: false };
    }
    return { entries: response.data?.entries ?? [], hasMore: response.data?.hasMore ?? false };
  } catch (error) {
    logger.error('Failed to get Helm release history', { error, cluster, namespace, name });
    throw error;
  }
}

export async function rollbackHelmRelease(cluster: string, namespace: string, name: string, revision: number): Promise<void> {
  try {
    await apiClient.getAxios().post(`/helm/releases/${namespace}/${name}/rollback`, { revision }, { params: { cluster } });
  } catch (error) {
    logger.error('Failed to rollback Helm release', { error, cluster, namespace, name, revision });
    throw error;
  }
}

export async function uninstallHelmRelease(cluster: string, namespace: string, name: string, keepHistory = false): Promise<void> {
  try {
    await apiClient.getAxios().delete(`/helm/releases/${namespace}/${name}`, {
      params: { cluster, keepHistory: keepHistory ? 'true' : 'false' },
    });
  } catch (error) {
    logger.error('Failed to uninstall Helm release', { error, cluster, namespace, name });
    throw error;
  }
}

export async function upgradeHelmRelease(
  cluster: string, namespace: string, name: string, values: Record<string, any>, dryRun = false
): Promise<{ release: any; manifest: string; dryRun: boolean }> {
  try {
    const response = await apiClient.getAxios().post(
      `/helm/releases/${namespace}/${name}/upgrade`,
      { values, dryRun },
      { params: { cluster } }
    );
    return response.data;
  } catch (error) {
    logger.error('Failed to upgrade Helm release', { error, cluster, namespace, name });
    throw error;
  }
}
