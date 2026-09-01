import logger from '../../utils/logger';
import { SearchResult, SearchOptions, SearchResponse } from '../../types/search';
import { apiClient } from './client';

export async function search(query: string, options: SearchOptions = {}): Promise<SearchResult[]> {
  const endpoint = '/search';
  try {
    const params = new URLSearchParams();
    params.append('q', query);
    if (options.limit) params.append('limit', options.limit.toString());
    if (options.offset) params.append('offset', options.offset.toString());
    if (options.clusters) options.clusters.forEach((c: string) => params.append('clusters', c));
    if (options.namespaces) options.namespaces.forEach((n: string) => params.append('namespaces', n));
    if (options.kinds) options.kinds.forEach((k: string) => params.append('kinds', k));
    const response = await apiClient.getAxios().get<SearchResponse>(endpoint, { params });
    return response.data.results || [];
  } catch (error: any) {
    logger.error('Search failed:', { query, error: error.message });
    throw error;
  }
}

export async function getSearchSuggestions(prefix: string, limit: number = 10): Promise<string[]> {
  const endpoint = '/search/suggestions';
  try {
    const response = await apiClient.getAxios().get(endpoint, { params: { prefix, limit } });
    return response.data.suggestions || [];
  } catch (error: any) {
    logger.error('Failed to get search suggestions:', { error: error.message });
    return [];
  }
}

export async function getRecentSearches(limit: number = 10): Promise<{ name: string; kind: string; namespace?: string; cluster: string; apiVersion?: string; category?: string }[]> {
  try {
    const response = await apiClient.getAxios().get('/search/recent', { params: { limit } });
    return response.data.resources || [];
  } catch (error: any) {
    logger.error('Failed to get recent searches:', { error: error.message });
    return [];
  }
}

export async function saveSearchHistory(resource: { name: string; kind: string; namespace?: string; cluster: string; apiVersion?: string; category?: string }): Promise<void> {
  if (!resource.name) return;
  try {
    await apiClient.getAxios().post('/search/recent', resource);
  } catch (error: any) {
    logger.error('Failed to save search history:', { error: error.message });
  }
}

export async function indexCluster(cluster: string): Promise<void> {
  const endpoint = '/search/index/cluster';
  try {
    await apiClient.getAxios().post(endpoint, null, { params: { cluster } });
  } catch (error: any) {
    logger.error('Failed to index cluster:', { cluster, error: error.message });
    throw error;
  }
}

export async function indexAllClusters(): Promise<void> {
  const endpoint = '/search/index/all';
  try {
    await apiClient.getAxios().post(endpoint);
  } catch (error: any) {
    logger.error('Failed to index all clusters:', { error: error.message });
    throw error;
  }
}

export async function getSearchIndexingStatus(cluster: string): Promise<any> {
  const endpoint = '/search/index/status';
  try {
    const response = await apiClient.getAxios().get(endpoint, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error('Failed to get indexing status:', { cluster, error: error.message });
    return null;
  }
}
