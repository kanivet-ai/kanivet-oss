import logger from '../../utils/logger';
import { apiClient } from './client';
import { PortForward } from './types';

export async function getResourceDetails(
  cluster: string, group: string, version: string, kind: string, namespace: string, name: string, signal?: AbortSignal
): Promise<any> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/resource/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}`;
  return await apiClient.request(endpoint, { cluster }, false, signal);
}

export async function getResourceEvents(
  cluster: string, group: string, version: string, kind: string, namespace: string, name: string, signal?: AbortSignal
): Promise<any[]> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/resource-events/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}`;
  const events = await apiClient.request(endpoint, { cluster }, false, signal);
  return Array.isArray(events) ? events : [];
}

export async function updateResource(cluster: string, yamlContent: string): Promise<any> {
  try {
    const response = await apiClient.getAxios().put(
      `/cluster/resources?cluster=${encodeURIComponent(cluster)}`,
      yamlContent,
      { headers: { 'Content-Type': 'application/yaml' } }
    );
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to update resource`, { error: error.message });
    throw error;
  }
}

export async function createResource(cluster: string, yamlContent: string): Promise<any> {
  try {
    const response = await apiClient.getAxios().post(
      `/cluster/resources?cluster=${encodeURIComponent(cluster)}`,
      yamlContent,
      { headers: { 'Content-Type': 'application/yaml' } }
    );
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to create resource`, { error: error.message });
    throw error;
  }
}

export async function getResourceSchema(cluster: string, group: string, version: string, kind: string): Promise<{ template: string; schema?: any }> {
  const encodedGroup = group || '_';
  const endpoint = `/cluster/resource-schema/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}`;
  try {
    const response = await apiClient.getAxios().get(endpoint, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to get resource schema`, { error: error.message });
    throw error;
  }
}

export async function deleteResources(
  cluster: string, group: string, version: string, kind: string, items: any[]
): Promise<{ succeeded: number; failed: string[] }> {
  const encodedGroup = group || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}`;
  const results = await Promise.allSettled(
    items.map((item) => apiClient.getAxios().delete(endpoint, { params: { cluster }, data: { items: [item] } }))
  );
  const failed: string[] = [];
  let succeeded = 0;
  results.forEach((result, i) => {
    if (result.status === 'rejected') {
      const name = items[i].namespace ? `${items[i].namespace}/${items[i].name}` : items[i].name;
      failed.push(name);
      logger.error(`Failed to delete ${name}`, { error: result.reason });
    } else {
      succeeded++;
    }
  });
  return { succeeded, failed };
}

export async function removeFinalizers(
  cluster: string, group: string, version: string, kind: string, items: any[]
): Promise<{ succeeded: number; failed: string[] }> {
  const encodedGroup = group || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/remove-finalizers`;
  const results = await Promise.allSettled(
    items.map((item) => apiClient.getAxios().post(endpoint, { items: [item] }, { params: { cluster } }))
  );
  const failed: string[] = [];
  let succeeded = 0;
  results.forEach((result, i) => {
    if (result.status === 'rejected') {
      const name = items[i].namespace ? `${items[i].namespace}/${items[i].name}` : items[i].name;
      failed.push(name);
      logger.error(`Failed to remove finalizers for ${name}`, { error: result.reason });
    } else {
      succeeded++;
    }
  });
  return { succeeded, failed };
}

export async function restartResource(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<void> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/restart`;
  try {
    await apiClient.getAxios().post(endpoint, null, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to restart resource`, { error: error.message });
    throw error;
  }
}

export async function bulkRestartResources(
  cluster: string,
  resources: Array<{ group: string; version: string; kind: string; namespace: string; name: string }>
): Promise<void> {
  const endpoint = `/cluster/resources/bulk-restart`;
  const payload = {
    resources: resources.map((r) => ({
      group: r.group || '_',
      version: r.version,
      kind: r.kind,
      namespace: r.namespace || '_',
      name: r.name,
    })),
  };
  try {
    await apiClient.getAxios().post(endpoint, payload, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to bulk restart resources`, { error: error.message });
    throw error;
  }
}

export async function forceRefreshResources(
  cluster: string, group: string, version: string, kind: string, items: Array<{ name: string; namespace?: string }>
): Promise<void> {
  const encodedGroup = group || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/force-refresh`;
  try {
    await apiClient.getAxios().post(endpoint, { items }, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to force refresh resources`, { error: error.message });
    throw error;
  }
}

export async function getRolloutStatus(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<any> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/rollout-status`;
  try {
    const response = await apiClient.getAxios().get(endpoint, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to get rollout status`, { error: error.message });
    throw error;
  }
}

export async function getBatchRolloutStatus(cluster: string, items: any[]): Promise<any> {
  const endpoint = '/cluster/batch-rollout-status';
  try {
    const response = await apiClient.getAxios().post(endpoint, { items }, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to get batch rollout status`, { error: error.message });
    throw error;
  }
}

export async function scaleResource(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, replicas: number): Promise<void> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/resources/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/scale`;
  try {
    await apiClient.getAxios().post(endpoint, { replicas }, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to scale resource`, { error: error.message });
    throw error;
  }
}

export async function triggerCronJob(cluster: string, namespace: string, name: string): Promise<{ jobName: string }> {
  const endpoint = `/cluster/cronjobs/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/trigger`;
  try {
    const response = await apiClient.getAxios().post(endpoint, null, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to trigger cronjob`, { error: error.message });
    throw error;
  }
}

export async function createPortForward(cluster: string, namespace: string, podName: string, remotePort: number): Promise<PortForward> {
  const endpoint = `/cluster/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(podName)}/portforward`;
  try {
    const response = await apiClient.getAxios().post(endpoint, { remotePort }, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error('Failed to create port forward:', { error: error.message });
    throw error;
  }
}

export async function createServicePortForward(cluster: string, namespace: string, serviceName: string, port: number): Promise<PortForward> {
  const endpoint = `/cluster/services/${encodeURIComponent(namespace)}/${encodeURIComponent(serviceName)}/portforward`;
  try {
    const response = await apiClient.getAxios().post(endpoint, { port }, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error('Failed to create service port forward:', { error: error.message });
    throw error;
  }
}

export async function stopPortForward(id: string): Promise<void> {
  const endpoint = `/cluster/portforward/${encodeURIComponent(id)}`;
  logger.info('Stopping port forward:', { id, endpoint });
  try {
    await apiClient.getAxios().delete(endpoint);
  } catch (error: any) {
    logger.error('Failed to stop port forward:', { id, error: error.message });
    throw error;
  }
}

export async function listPortForwards(cluster?: string): Promise<PortForward[]> {
  const endpoint = '/cluster/portforwards';
  const params = cluster ? { cluster } : {};
  try {
    const response = await apiClient.getAxios().get(endpoint, { params });
    return response.data.portForwards || [];
  } catch (error: any) {
    logger.error('Failed to list port forwards:', { error: error.message });
    throw error;
  }
}

export async function taintNode(cluster: string, nodeName: string, key: string, value: string, effect: string): Promise<void> {
  const endpoint = `/cluster/nodes/${encodeURIComponent(nodeName)}/taint`;
  try {
    await apiClient.getAxios().post(endpoint, { key, value, effect }, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to taint node`, { error: error.message });
    throw error;
  }
}

export async function removeTaint(cluster: string, nodeName: string, key: string): Promise<void> {
  const endpoint = `/cluster/nodes/${encodeURIComponent(nodeName)}/taint`;
  try {
    await apiClient.getAxios().delete(endpoint, { params: { cluster }, data: { key } });
  } catch (error: any) {
    logger.error(`Failed to remove taint`, { error: error.message });
    throw error;
  }
}

export async function drainNode(cluster: string, nodeName: string, options: { ignoreDaemonsets?: boolean; deleteEmptyDir?: boolean; gracePeriod?: number } = {}): Promise<any> {
  const endpoint = `/cluster/nodes/${encodeURIComponent(nodeName)}/drain`;
  try {
    const response = await apiClient.getAxios().post(endpoint, {
      ignoreDaemonsets: options.ignoreDaemonsets ?? true,
      deleteEmptyDir: options.deleteEmptyDir ?? false,
      gracePeriod: options.gracePeriod ?? 30,
    }, { params: { cluster } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to drain node`, { error: error.message });
    throw error;
  }
}

export async function cordonNode(cluster: string, nodeName: string, unschedulable: boolean = true): Promise<void> {
  const endpoint = `/cluster/nodes/${encodeURIComponent(nodeName)}/cordon`;
  try {
    await apiClient.getAxios().post(endpoint, { unschedulable }, { params: { cluster } });
  } catch (error: any) {
    logger.error(`Failed to cordon/uncordon node`, { error: error.message });
    throw error;
  }
}

export async function getDetailTabState(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<{ activeTab: string }> {
  const endpoint = `/detail-tab-state`;
  try {
    const response = await apiClient.getAxios().get(endpoint, { params: { cluster, group, version, kind, namespace, name } });
    return response.data;
  } catch (error: any) {
    logger.error(`Failed to get detail tab state`, { error: error.message });
    return { activeTab: 'pretty' };
  }
}

export async function setDetailTabState(cluster: string, group: string, version: string, kind: string, namespace: string, name: string, activeTab: string): Promise<void> {
  const endpoint = `/detail-tab-state`;
  try {
    await apiClient.getAxios().post(endpoint, { cluster, group, version, kind, namespace, name, activeTab });
  } catch (error: any) {
    logger.error(`Failed to set detail tab state`, { error: error.message });
  }
}

export async function checkCrossplaneResource(cluster: string, group: string, version: string, kind: string): Promise<boolean> {
  const encodedGroup = group || '_';
  const endpoint = `/cluster/crossplane/check/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}`;
  try {
    const result = await apiClient.request(endpoint, { cluster }, true);
    return result.isCrossplane || false;
  } catch (error: any) {
    logger.error(`Failed to check if resource is Crossplane`, { error: error.message });
    return false;
  }
}

export async function getCrossplaneTrace(cluster: string, group: string, version: string, kind: string, namespace: string, name: string): Promise<any> {
  const encodedGroup = group || '_';
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/crossplane/trace/${encodeURIComponent(encodedGroup)}/${encodeURIComponent(version)}/${encodeURIComponent(kind)}/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}`;
  try {
    const result = await apiClient.request(endpoint, { cluster }, false);
    return result.trace || null;
  } catch (error: any) {
    logger.error(`Failed to get Crossplane trace`, { error: error.message });
    throw error;
  }
}

export interface ArgoDestination {
  kind: 'local' | 'vcluster' | 'external';
  server?: string;
  name?: string;
  vcluster?: { namespace: string; name: string };
}

export interface ArgoDestinationMap {
  byName: Record<string, ArgoDestination>;
  byServer: Record<string, ArgoDestination>;
}

export async function getArgoDestinations(cluster: string): Promise<ArgoDestinationMap> {
  try {
    const result = await apiClient.request('/cluster/argo/destinations', { cluster }, true);
    return result.destinations || { byName: {}, byServer: {} };
  } catch (error: any) {
    logger.error('Failed to fetch Argo destinations', { error: error.message });
    return { byName: {}, byServer: {} };
  }
}

export async function getArgoDetection(cluster: string): Promise<{ installed: boolean; namespace?: string; hasApi?: boolean; serverName?: string }> {
  try {
    const result = await apiClient.request('/cluster/argo/detect', { cluster }, true);
    return result.detection || { installed: false };
  } catch (error: any) {
    logger.error('Failed to detect Argo CD', { error: error.message });
    return { installed: false };
  }
}

export interface ArgoAppListEntry {
  name: string;
  namespace: string;
  project?: string;
  syncStatus: string;
  health: string;
  destNamespace?: string;
  destName?: string;
  destServer?: string;
  repoUrl?: string;
  path?: string;
  revision?: string;
  targetRevision?: string;
  reconciledAt?: string;
  lastSyncedAt?: string;
  lastSyncPhase?: string;
  createdAt?: string;
  operationPhase?: string;
}

export interface ArgoStats {
  total: number;
  bySync: Record<string, number>;
  byHealth: Record<string, number>;
  byProject: Record<string, number>;
  byNamespace: Record<string, number>;
  syncing: number;
  recentSync1h: number;
  recentSync24h: number;
  topUnhealthy?: ArgoAppListEntry[];
}

export async function getArgoStats(cluster: string): Promise<ArgoStats> {
  try {
    const result = await apiClient.request('/cluster/argo/stats', { cluster }, true);
    return result.stats || { total: 0, bySync: {}, byHealth: {}, byProject: {}, byNamespace: {}, syncing: 0, recentSync1h: 0, recentSync24h: 0 };
  } catch (error: any) {
    logger.error('Failed to fetch Argo stats', { error: error.message });
    throw error;
  }
}

export async function getArgoApplicationsSummary(cluster: string): Promise<ArgoAppListEntry[]> {
  try {
    const result = await apiClient.request('/cluster/argo/applications-summary', { cluster }, true);
    return result.items || [];
  } catch (error: any) {
    logger.error('Failed to fetch Argo applications summary', { error: error.message });
    throw error;
  }
}

export async function getArgoApplicationTopology(cluster: string, namespace: string, name: string): Promise<any> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/topology`;
  try {
    const result = await apiClient.request(endpoint, { cluster }, false);
    return { topology: result.topology || [], summary: result.summary || null };
  } catch (error: any) {
    logger.error('Failed to get Argo application topology', { error: error.message });
    throw error;
  }
}

export async function getArgoApplicationTree(cluster: string, namespace: string, name: string): Promise<any> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/tree`;
  try {
    const result = await apiClient.request(endpoint, { cluster }, false);
    return result.summary || null;
  } catch (error: any) {
    logger.error('Failed to get Argo application tree', { error: error.message });
    throw error;
  }
}

export interface ArgoManagedResource {
  group: string;
  version: string;
  kind: string;
  namespace: string;
  name: string;
  targetState: string;
  liveState: string;
  normalizedLiveState?: string;
  predictedLiveState?: string;
  diff?: string;
}

export async function getArgoManagedResources(cluster: string, namespace: string, name: string): Promise<ArgoManagedResource[]> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/managed-resources`;
  try {
    const result = await apiClient.request(endpoint, { cluster }, false);
    return result.items || [];
  } catch (error: any) {
    logger.error('Failed to fetch Argo managed-resources', { error: error.message });
    throw error;
  }
}

export interface ArgoSyncOptions {
  prune?: boolean;
  dryRun?: boolean;
  force?: boolean;
  replace?: boolean;
  strategy?: 'apply' | 'hook';
  revision?: string;
  resources?: { group: string; kind: string; namespace?: string; name: string }[];
}

export async function syncArgoApplication(
  cluster: string, namespace: string, name: string, opts?: ArgoSyncOptions,
): Promise<void> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/sync`;
  await apiClient.getAxios().post(endpoint, opts || {}, { params: { cluster } });
}

export async function refreshArgoApplication(
  cluster: string, namespace: string, name: string, hard?: boolean,
): Promise<void> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/refresh`;
  await apiClient.getAxios().post(endpoint, null, { params: { cluster, hard: hard ? 'true' : 'false' } });
}

export async function rollbackArgoApplication(
  cluster: string, namespace: string, name: string, id: number, prune?: boolean,
): Promise<void> {
  const encodedNamespace = namespace || '_';
  const endpoint = `/cluster/argo/applications/${encodeURIComponent(encodedNamespace)}/${encodeURIComponent(name)}/rollback`;
  await apiClient.getAxios().post(endpoint, { id, prune: !!prune }, { params: { cluster } });
}

export async function getWorkloadPods(
  cluster: string, kind: string, namespace: string, name: string
): Promise<{
  pods: Array<{
    name: string;
    status: string;
    node: string;
    containers: string[];
    ready: boolean;
    restarts: number;
    age: string;
    resourceLimits: { cpu: number; memory: number };
    resourceRequests: { cpu: number; memory: number };
  }>;
  count: number;
}> {
  try {
    const response = await apiClient.getAxios().get(
      `/cluster/workloads/${kind}/${namespace}/${name}/pods`,
      { params: { cluster } }
    );
    return response.data;
  } catch (error) {
    logger.error('Failed to get workload pods', { error, cluster, kind, namespace, name });
    throw error;
  }
}
