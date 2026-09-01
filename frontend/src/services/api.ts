import { ClusterStatus } from '../types';
import { SearchResult, SearchOptions } from '../types/search';
import { HelmRelease, HelmReleaseDetail, HelmHistoryResponse } from '../types/helm';
import { wsManager } from './api/websocket';
import { apiClient } from './api/client';
import { ClusterGroup, ClusterInfo, NavigationEntry, PortForward } from './api/types';
import * as clusters from './api/clusters';
import * as resources from './api/resources';
import * as navigation from './api/navigation';
import * as groups from './api/groups';
import * as searchApi from './api/search';
import * as helm from './api/helm';
import * as metrics from './api/metrics';
import * as finops from './api/finops';
import * as incidents from './api/incidents';
import * as vclusters from './api/vclusters';
import { IncidentTimelineFilters, IncidentTimelineResponse } from '../types/incidents';

export type { ClusterGroup, ClusterInfo } from './api/types';

class API {
  getCacheKey(endpoint: string, params?: any): string { return apiClient.getCacheKey(endpoint, params); }
  clearCache() { apiClient.clearCache(); }
  invalidateCachePattern(pattern: string) { apiClient.invalidateCachePattern(pattern); }
  invalidateCache(pattern?: string): void {
    apiClient.invalidateCache(pattern);
    const cacheMap: Map<string, any> | undefined = (window as any).__kanivetItemsCache;
    if (!cacheMap) return;
    for (const topic of [...cacheMap.keys()]) {
      if (!pattern || topic.startsWith(pattern)) cacheMap.delete(topic);
    }
  }
  async request(endpoint: string, params?: any, useCache: boolean = true, signal?: AbortSignal): Promise<any> {
    return apiClient.request(endpoint, params, useCache, signal);
  }

  reconnectWebSocket() { wsManager.reconnectWebSocket(); }
  registerActiveCluster(clusterId: string) { wsManager.registerActiveCluster(clusterId); }
  unregisterActiveCluster(clusterId: string) { wsManager.unregisterActiveCluster(clusterId); }
  releaseCluster(cluster: string): Promise<void> { return clusters.releaseCluster(cluster); }
  async waitForBackend(maxWaitMs: number = 30000): Promise<boolean> { return wsManager.waitForBackend(maxWaitMs); }
  isReady(): boolean { return wsManager.isReady(); }
  public __sendWS(payload: any) { wsManager.sendWS(payload); }
  public get wsHandlers() { return wsManager.getHandlers(); }

  subscribeToCounts(cluster: string, handler: (msg: { group: string; resource: string; count: number }) => void): () => void {
    return wsManager.subscribeToCounts(cluster, handler);
  }
  subscribeToDashboard(cluster: string, handler: (msg: any) => void): () => void {
    return wsManager.subscribeToDashboard(cluster, handler);
  }
  subscribeToItems(cluster: string, group: string, version: string, kind: string, namespace?: string, onEvent?: (event: any) => void, sortBy?: string, sortOrder?: 'asc' | 'desc'): string {
    return wsManager.subscribeToItems(cluster, group, version, kind, namespace, onEvent, sortBy, sortOrder);
  }
  unsubscribe(topic: string, onEvent?: (event: any) => void, clearAll: boolean = false) {
    wsManager.unsubscribe(topic, onEvent, clearAll);
  }

  async getClusters(): Promise<ClusterInfo[]> { return clusters.getClusters(); }
  async refreshClusters(): Promise<ClusterInfo[]> { return clusters.refreshClusters(); }
  async getClustersStatus(): Promise<ClusterStatus[]> { return clusters.getClustersStatus(); }
  async getClusterStatus(cluster: string, force = false): Promise<ClusterStatus> { return clusters.getClusterStatus(cluster, force); }
  async getBatchClusterStatus(clusterList: string[], force = false): Promise<Record<string, ClusterStatus>> { return clusters.getBatchClusterStatus(clusterList, force); }
  async getClusterDashboard(cluster: string): Promise<any> { return clusters.getClusterDashboard(cluster); }
  async streamClusterDashboard(cluster: string, onChunk: (type: string, data: any) => void, signal?: AbortSignal): Promise<void> {
    return clusters.streamClusterDashboard(cluster, onChunk, signal);
  }
  async getCategories(cluster: string): Promise<any[]> { return clusters.getCategories(cluster); }
  async getResources(cluster: string, category: string, skipCounts: boolean = false): Promise<any[]> { return clusters.getResources(cluster, category, skipCounts); }
  async getNamespaces(cluster: string): Promise<string[]> { return clusters.getNamespaces(cluster); }

  async getResourceDetails(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, signal?: AbortSignal): Promise<any> {
    return resources.getResourceDetails(cluster, group, version, kind, namespace, name, signal);
  }
  async getResourceEvents(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, signal?: AbortSignal): Promise<any[]> {
    return resources.getResourceEvents(cluster, group, version, kind, namespace, name, signal);
  }
  async updateResource(cluster: string, yamlContent: string): Promise<any> { return resources.updateResource(cluster, yamlContent); }
  async createResource(cluster: string, yamlContent: string): Promise<any> { return resources.createResource(cluster, yamlContent); }
  async getResourceSchema(cluster: string, group: string, version: string, kind: string): Promise<{ template: string; schema?: any }> {
    return resources.getResourceSchema(cluster, group, version, kind);
  }
  async deleteResources(cluster: string, group: string, version: string, kind: string, items: any[]): Promise<{ succeeded: number; failed: string[] }> {
    return resources.deleteResources(cluster, group, version, kind, items);
  }
  async removeFinalizers(cluster: string, group: string, version: string, kind: string, items: any[]): Promise<{ succeeded: number; failed: string[] }> {
    return resources.removeFinalizers(cluster, group, version, kind, items);
  }
  async restartResource(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<void> {
    return resources.restartResource(cluster, group, version, kind, namespace, name);
  }
  async bulkRestartResources(cluster: string, resourceList: Array<{ group: string; version: string; kind: string; namespace: string; name: string }>): Promise<void> {
    return resources.bulkRestartResources(cluster, resourceList);
  }
  async forceRefreshResources(cluster: string, group: string, version: string, kind: string, items: Array<{ name: string; namespace?: string }>): Promise<void> {
    return resources.forceRefreshResources(cluster, group, version, kind, items);
  }
  async getRolloutStatus(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<any> {
    return resources.getRolloutStatus(cluster, group, version, kind, namespace, name);
  }
  async getBatchRolloutStatus(cluster: string, items: any[]): Promise<any> { return resources.getBatchRolloutStatus(cluster, items); }
  async scaleResource(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, replicas: number): Promise<void> {
    return resources.scaleResource(cluster, group, version, kind, namespace, name, replicas);
  }
  async triggerCronJob(cluster: string, namespace: string, name: string): Promise<{ jobName: string }> {
    return resources.triggerCronJob(cluster, namespace, name);
  }
  async createPortForward(cluster: string, namespace: string, podName: string, remotePort: number): Promise<PortForward> {
    return resources.createPortForward(cluster, namespace, podName, remotePort);
  }
  async createServicePortForward(cluster: string, namespace: string, serviceName: string, port: number): Promise<PortForward> {
    return resources.createServicePortForward(cluster, namespace, serviceName, port);
  }
  async stopPortForward(id: string): Promise<void> { return resources.stopPortForward(id); }
  async listPortForwards(cluster?: string): Promise<PortForward[]> { return resources.listPortForwards(cluster); }
  async taintNode(cluster: string, nodeName: string, key: string, value: string, effect: string): Promise<void> {
    return resources.taintNode(cluster, nodeName, key, value, effect);
  }
  async removeTaint(cluster: string, nodeName: string, key: string): Promise<void> { return resources.removeTaint(cluster, nodeName, key); }
  async drainNode(cluster: string, nodeName: string, options: { ignoreDaemonsets?: boolean; deleteEmptyDir?: boolean; gracePeriod?: number } = {}): Promise<any> {
    return resources.drainNode(cluster, nodeName, options);
  }
  async cordonNode(cluster: string, nodeName: string, unschedulable: boolean = true): Promise<void> {
    return resources.cordonNode(cluster, nodeName, unschedulable);
  }
  async getDetailTabState(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<{ activeTab: string }> {
    return resources.getDetailTabState(cluster, group, version, kind, namespace, name);
  }
  async setDetailTabState(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, activeTab: string): Promise<void> {
    return resources.setDetailTabState(cluster, group, version, kind, namespace, name, activeTab);
  }
  async checkCrossplaneResource(cluster: string, group: string, version: string, kind: string): Promise<boolean> {
    return resources.checkCrossplaneResource(cluster, group, version, kind);
  }
  async getCrossplaneTrace(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<any> {
    return resources.getCrossplaneTrace(cluster, group, version, kind, namespace, name);
  }
  async getArgoDetection(cluster: string): Promise<{ installed: boolean; namespace?: string; hasApi?: boolean; serverName?: string }> {
    return resources.getArgoDetection(cluster);
  }
  async getArgoApplicationTree(cluster: string, namespace: string, name: string): Promise<any> {
    return resources.getArgoApplicationTree(cluster, namespace, name);
  }
  async getArgoApplicationTopology(cluster: string, namespace: string, name: string): Promise<any> {
    return resources.getArgoApplicationTopology(cluster, namespace, name);
  }
  async getArgoManagedResources(cluster: string, namespace: string, name: string) {
    return resources.getArgoManagedResources(cluster, namespace, name);
  }
  async getArgoDestinations(cluster: string) {
    return resources.getArgoDestinations(cluster);
  }
  async getArgoStats(cluster: string) {
    return resources.getArgoStats(cluster);
  }
  async getArgoApplicationsSummary(cluster: string) {
    return resources.getArgoApplicationsSummary(cluster);
  }
  async syncArgoApplication(cluster: string, namespace: string, name: string, opts?: resources.ArgoSyncOptions): Promise<void> {
    return resources.syncArgoApplication(cluster, namespace, name, opts);
  }
  async refreshArgoApplication(cluster: string, namespace: string, name: string, hard?: boolean): Promise<void> {
    return resources.refreshArgoApplication(cluster, namespace, name, hard);
  }
  async rollbackArgoApplication(cluster: string, namespace: string, name: string, id: number, prune?: boolean): Promise<void> {
    return resources.rollbackArgoApplication(cluster, namespace, name, id, prune);
  }
  async getWorkloadPods(cluster: string, kind: string, namespace: string, name: string) {
    return resources.getWorkloadPods(cluster, kind, namespace, name);
  }

  async addNavigationEntry(tabId: string, clusterId: string, entry: NavigationEntry): Promise<any> {
    return navigation.addNavigationEntry(tabId, clusterId, entry);
  }
  async getNavigationHistory(tabId: string): Promise<any[]> { return navigation.getNavigationHistory(tabId); }
  async navigateBack(tabId: string): Promise<NavigationEntry | null> { return navigation.navigateBack(tabId); }
  async navigateForward(tabId: string): Promise<NavigationEntry | null> { return navigation.navigateForward(tabId); }
  async clearNavigationHistory(tabId: string): Promise<void> { return navigation.clearNavigationHistory(tabId); }

  async getClusterGroups(): Promise<ClusterGroup[]> { return groups.getClusterGroups(); }
  async createClusterGroup(name: string, description: string): Promise<ClusterGroup> { return groups.createClusterGroup(name, description); }
  async updateClusterGroup(id: number, name: string, description: string): Promise<void> { return groups.updateClusterGroup(id, name, description); }
  async updateGroupOrder(groupOrders: Array<{ id: number; order: number }>): Promise<void> { return groups.updateGroupOrder(groupOrders); }
  async deleteClusterGroup(id: number): Promise<void> { return groups.deleteClusterGroup(id); }
  async assignClusterToGroup(clusterName: string, groupId: number): Promise<void> { return groups.assignClusterToGroup(clusterName, groupId); }
  async removeClusterFromGroup(clusterName: string): Promise<void> { return groups.removeClusterFromGroup(clusterName); }
  async getClustersByGroup(): Promise<Record<string, string[]>> { return groups.getClustersByGroup(); }
  async getClusterAssignments(): Promise<Record<string, number>> { return groups.getClusterAssignments(); }
  async getClusterAliases(): Promise<Record<string, string>> { return groups.getClusterAliases(); }
  async setClusterAlias(clusterName: string, alias: string): Promise<void> { return groups.setClusterAlias(clusterName, alias); }
  async deleteClusterAlias(clusterName: string): Promise<void> { return groups.deleteClusterAlias(clusterName); }

  async search(query: string, options: SearchOptions = {}): Promise<SearchResult[]> { return searchApi.search(query, options); }
  async getSearchSuggestions(prefix: string, limit: number = 10): Promise<string[]> { return searchApi.getSearchSuggestions(prefix, limit); }
  async getRecentSearches(limit: number = 10) { return searchApi.getRecentSearches(limit); }
  async saveSearchHistory(resource: { name: string; kind: string; namespace?: string; cluster: string; apiVersion?: string; category?: string }): Promise<void> {
    return searchApi.saveSearchHistory(resource);
  }
  async indexCluster(cluster: string): Promise<void> { return searchApi.indexCluster(cluster); }
  async indexAllClusters(): Promise<void> { return searchApi.indexAllClusters(); }
  async getSearchIndexingStatus(cluster: string): Promise<any> { return searchApi.getSearchIndexingStatus(cluster); }

  async getHelmReleases(cluster: string, namespace?: string): Promise<HelmRelease[]> { return helm.getHelmReleases(cluster, namespace); }
  async getHelmRelease(cluster: string, namespace: string, name: string): Promise<HelmReleaseDetail> { return helm.getHelmRelease(cluster, namespace, name); }
  async getHelmReleaseValues(cluster: string, namespace: string, name: string, allValues = false): Promise<Record<string, any>> {
    return helm.getHelmReleaseValues(cluster, namespace, name, allValues);
  }
  async getHelmReleaseManifest(cluster: string, namespace: string, name: string): Promise<string> {
    return helm.getHelmReleaseManifest(cluster, namespace, name);
  }
  async getHelmReleaseHistory(cluster: string, namespace: string, name: string, limit?: number): Promise<HelmHistoryResponse> {
    return helm.getHelmReleaseHistory(cluster, namespace, name, limit);
  }
  async rollbackHelmRelease(cluster: string, namespace: string, name: string, revision: number): Promise<void> {
    return helm.rollbackHelmRelease(cluster, namespace, name, revision);
  }
  async uninstallHelmRelease(cluster: string, namespace: string, name: string, keepHistory = false): Promise<void> {
    return helm.uninstallHelmRelease(cluster, namespace, name, keepHistory);
  }
  async upgradeHelmRelease(cluster: string, namespace: string, name: string, values: Record<string, any>, dryRun = false) {
    return helm.upgradeHelmRelease(cluster, namespace, name, values, dryRun);
  }

  async getAvailableMetricProviders(): Promise<string[]> { return metrics.getAvailableMetricProviders(); }
  async detectMetricsProvider(cluster: string): Promise<any> { return metrics.detectMetricsProvider(cluster); }
  async installMetricsProvider(cluster: string, provider: string, namespace?: string): Promise<void> {
    return metrics.installMetricsProvider(cluster, provider, namespace);
  }
  async getClusterMetricsSettings(cluster: string) { return metrics.getClusterMetricsSettings(cluster); }
  async setClusterMetricsSettings(cluster: string, settings: { mimirTenant?: string; mimirService?: string; mimirNamespace?: string }) {
    return metrics.setClusterMetricsSettings(cluster, settings);
  }
  async discoverMimirTenants(cluster: string, hints: string[] = []): Promise<string[]> {
    return metrics.discoverMimirTenants(cluster, hints);
  }
  async listMimirServices(cluster: string) { return metrics.listMimirServices(cluster); }
  markMetricsProviderUnavailable(cluster: string, reason?: string): any {
    return metrics.markMetricsProviderUnavailable(cluster, reason);
  }
  clearMetricsProviderAvailabilityCache(cluster?: string): void {
    metrics.clearMetricsProviderAvailabilityCache(cluster);
  }
  isMetricsProviderUnavailableError(error: string): boolean {
    return metrics.isMetricsProviderUnavailableError(error);
  }
  isMetricsProviderUnavailableCached(cluster: string): boolean {
    return metrics.isMetricsProviderUnavailableCached(cluster);
  }
  getCachedMetricsProviderStatus(cluster: string): any | null {
    return metrics.getCachedMetricsProviderStatus(cluster);
  }
  emitMetricsSettingsChanged(cluster: string): void { metrics.emitMetricsSettingsChanged(cluster); }
  startMetricsStream(
    cluster: string, namespace: string, pod: string, metricType: string, timeRange: string,
    onData: (data: { labels: string[]; values: number[]; unit?: string }) => void,
    onError: (error: string) => void,
    containerName?: string, provider?: string, streamingRate: number = 2, nodeName?: string
  ): () => void {
    return metrics.startMetricsStream(cluster, namespace, pod, metricType, timeRange, onData, onError, containerName, provider, streamingRate, nodeName);
  }
  async queryPodMetrics(cluster: string, namespace: string, pod: string, metricType: string, timeRange: string, containerName?: string, provider?: string) {
    return metrics.queryPodMetrics(cluster, namespace, pod, metricType, timeRange, containerName, provider);
  }

  async getFinOpsDashboard(cluster: string): Promise<any> { return finops.getFinOpsDashboard(cluster); }
  async streamFinOpsDashboard(cluster: string, onChunk: (type: string, data: any) => void, signal?: AbortSignal): Promise<void> {
    return finops.streamFinOpsDashboard(cluster, onChunk, signal);
  }
  async getFinOpsSummary(cluster: string): Promise<any> { return finops.getFinOpsSummary(cluster); }
  async getFinOpsNodeCosts(cluster: string): Promise<any[]> { return finops.getFinOpsNodeCosts(cluster); }
  async getFinOpsPodCosts(cluster: string, namespace?: string): Promise<any[]> { return finops.getFinOpsPodCosts(cluster, namespace); }
  async getFinOpsNamespaceCosts(cluster: string): Promise<any[]> { return finops.getFinOpsNamespaceCosts(cluster); }
  async getFinOpsWorkloadCosts(cluster: string, namespace?: string): Promise<any[]> { return finops.getFinOpsWorkloadCosts(cluster, namespace); }
  async getFinOpsRecommendations(cluster: string): Promise<any[]> { return finops.getFinOpsRecommendations(cluster); }
  async getFinOpsResourceCost(cluster: string, kind: string, namespace: string, name: string): Promise<any> {
    return finops.getFinOpsResourceCost(cluster, kind, namespace, name);
  }
  async getFinOpsPricingDebug(cluster: string): Promise<any> { return finops.getFinOpsPricingDebug(cluster); }
  async preloadFinOpsPricing(cluster: string): Promise<void> { return finops.preloadFinOpsPricing(cluster); }

  async getIncidentTimeline(cluster: string, filters: IncidentTimelineFilters = {}): Promise<IncidentTimelineResponse> {
    return incidents.getIncidentTimeline(cluster, filters);
  }

  async listVClusters(host: string): Promise<vclusters.VClusterInfo[]> { return vclusters.listVClusters(host); }
  async connectVCluster(host: string, namespace: string, name: string): Promise<vclusters.VClusterConnection> {
    return vclusters.connectVCluster(host, namespace, name);
  }
  async disconnectVCluster(id: string): Promise<void> { return vclusters.disconnectVCluster(id); }
}

const apiInstance = new API();
export default apiInstance;
