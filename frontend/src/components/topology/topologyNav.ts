// Entry points into the topology view, shared by the inspector, the sidebar
// and the command palette. The page itself is a resource-list tab of kind
// TopologyView; what it should focus on travels through this small store so
// re-opening the (single) Topology tab for another resource refocuses it.
import { create } from 'zustand';
import { useStore } from '../../store';
import { kindToResource, parseApiVersion } from '../../utils/resourceUtils';
import type { TopologyRef } from '../../services/api/topology';

export const TOPOLOGY_PAGE_KIND = 'TopologyView';

export const topologyPageResource = {
  name: 'topology',
  group: '',
  version: 'v1',
  kind: TOPOLOGY_PAGE_KIND,
  namespaced: false,
};

export interface TopologyRequest {
  /** Focus resource; absent for a whole-namespace view. */
  focus?: TopologyRef;
  namespace?: string;
  /** Bumped on every request so the page refocuses even for the same ref. */
  nonce: number;
}

interface TopologyNavState {
  requests: Record<string, TopologyRequest>; // by cluster
  request: (cluster: string, r: Omit<TopologyRequest, 'nonce'>) => void;
}

export const useTopologyNav = create<TopologyNavState>((set) => ({
  requests: {},
  request: (cluster, r) =>
    set((s) => ({ requests: { ...s.requests, [cluster]: { ...r, nonce: (s.requests[cluster]?.nonce || 0) + 1 } } })),
}));

/** Kinds the topology engine relates (the ones worth a "Topology" action). */
export const TOPOLOGY_KINDS = new Set([
  'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod',
  'Service', 'Ingress', 'HTTPRoute', 'Gateway',
  'ConfigMap', 'Secret', 'ServiceAccount', 'PersistentVolumeClaim',
  'HorizontalPodAutoscaler', 'PodDisruptionBudget', 'NetworkPolicy',
]);

/** Kinds that get the topology section open in the inspector by default. */
export const TOPOLOGY_SECTION_KINDS = new Set([
  'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod',
  'Service', 'Ingress', 'HTTPRoute', 'Gateway',
]);

export function refFromItem(item: any): TopologyRef | null {
  const kind = item?.kind;
  const name = item?.metadata?.name || item?.name;
  if (!kind || !name) return null;
  const { group, version } = parseApiVersion(item.apiVersion || 'v1');
  return { group, version, kind, namespace: item?.metadata?.namespace || item?.namespace, name };
}

/** Opens (or refocuses) the Topology tab on `focus`, or on a namespace. */
export async function openTopology(cluster: string, target: { focus?: TopologyRef; namespace?: string }) {
  if (!cluster) return;
  useTopologyNav.getState().request(cluster, {
    focus: target.focus,
    namespace: target.namespace || target.focus?.namespace,
  });
  await useStore.getState().openResourceListTab(topologyPageResource, cluster, true);
}

/** Opens a graph node in the real inspector (a detail tab), leaving the graph in place. */
export async function openInInspector(cluster: string, n: { group: string; version: string; kind: string; namespace?: string; name: string }) {
  const apiVersion = n.group ? `${n.group}/${n.version}` : n.version || 'v1';
  const resource = { name: kindToResource(n.kind), group: n.group, version: n.version || 'v1', kind: n.kind, namespaced: !!n.namespace };
  const item = { name: n.name, namespace: n.namespace, uid: `${n.namespace || 'cluster'}-${n.name}`, kind: n.kind, apiVersion };
  const { openDetailTab, loadDetails, getCurrentTabState, updateCurrentTabState } = useStore.getState() as any;
  openDetailTab(resource, item, cluster, false);
  if (getCurrentTabState?.()?.isDetailsPanelCollapsed) updateCurrentTabState?.({ isDetailsPanelCollapsed: false });
  await loadDetails(cluster, resource, item);
}
