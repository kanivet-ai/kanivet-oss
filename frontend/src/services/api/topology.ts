import { apiClient } from './client';
import type { TopoGraph } from '../../components/topology/graphModel';

export interface TopologyRef {
  group?: string;
  version?: string;
  kind: string;
  namespace?: string;
  name: string;
}

const seg = (v: string | undefined) => encodeURIComponent(v || '_');

/** Resources within `depth` hops of `ref`. `context` names the namespace to relate a cluster-scoped ref to. */
export async function getTopologyNeighbors(
  cluster: string,
  ref: TopologyRef,
  opts: { depth?: number; context?: string; includeIdle?: boolean } = {},
  signal?: AbortSignal,
): Promise<TopoGraph> {
  const path = `/cluster/topology/neighbors/${seg(ref.group)}/${seg(ref.version || 'v1')}/${seg(ref.kind)}/${seg(ref.namespace)}/${encodeURIComponent(ref.name)}`;
  const params: Record<string, string> = { cluster, depth: String(opts.depth ?? 1) };
  if (opts.context) params.context = opts.context;
  if (opts.includeIdle) params.includeIdle = 'true';
  const res = await apiClient.getAxios().get(path, { params, signal });
  return res.data as TopoGraph;
}

/** Every resource in a namespace and how they relate. */
export async function getNamespaceTopology(
  cluster: string,
  namespace: string,
  opts: { kinds?: string[]; includeIdle?: boolean } = {},
  signal?: AbortSignal,
): Promise<TopoGraph> {
  const params: Record<string, string> = { cluster };
  if (opts.kinds?.length) params.kinds = opts.kinds.join(',');
  if (opts.includeIdle) params.includeIdle = 'true';
  const res = await apiClient.getAxios().get(`/cluster/topology/namespace/${encodeURIComponent(namespace)}`, { params, signal });
  return res.data as TopoGraph;
}

export type OverviewHealth = 'healthy' | 'warning' | 'error' | 'unknown';
export interface HealthCounts { healthy: number; warning: number; error: number; unknown: number }

export interface NamespaceSummary {
  namespace: string;
  health: OverviewHealth;
  pods: HealthCounts;
  workloads: HealthCounts;
  apps?: string[];
  appCount: number;
}

export interface TopologyOverview {
  namespaces: NamespaceSummary[];
  warnings?: string[];
}

/** Per-namespace health rollups for the cluster view. */
export async function getTopologyOverview(cluster: string, signal?: AbortSignal): Promise<TopologyOverview> {
  const res = await apiClient.getAxios().get('/cluster/topology/overview', { params: { cluster }, signal });
  return res.data as TopologyOverview;
}
