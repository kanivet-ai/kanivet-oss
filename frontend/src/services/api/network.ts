import { apiClient } from './client';
import type {
  CoverageReport,
  FlowsResponse,
  NetRef,
  PathRequest,
  PathResult,
  PolicyDetail,
  ProbeRequest,
  ProbeResult,
  ResourceNetwork,
  Targets,
} from '../../types/network';

const http = () => apiClient.getAxios();

export async function getNetworkTargets(cluster: string): Promise<Targets> {
  const r = await http().get('/network/targets', { params: { cluster } });
  return r.data.data;
}

export async function checkNetworkPath(cluster: string, req: PathRequest): Promise<PathResult> {
  const r = await http().post('/network/path', req, { params: { cluster } });
  return r.data.data;
}

export async function getNetworkCoverage(cluster: string, namespace = ''): Promise<CoverageReport> {
  const r = await http().get('/network/coverage', { params: { cluster, namespace } });
  return r.data.data;
}

export async function getResourceNetwork(cluster: string, ref: NetRef): Promise<ResourceNetwork> {
  const r = await http().get('/network/resource', {
    params: { cluster, kind: ref.kind, namespace: ref.namespace || '', name: ref.name || '' },
  });
  return r.data.data;
}

export async function getNetworkPolicyDetail(cluster: string, namespace: string, name: string): Promise<PolicyDetail> {
  const r = await http().get('/network/policy', { params: { cluster, namespace, name } });
  return r.data.data;
}

export async function getNetworkFlows(cluster: string, namespace = '', windowMinutes = 5): Promise<FlowsResponse> {
  const r = await http().get('/network/flows', { params: { cluster, namespace, windowMinutes } });
  return r.data.data;
}

/** Creates a short-lived pod in the cluster. Only call after the user confirmed. */
export async function runNetworkProbe(cluster: string, req: ProbeRequest): Promise<ProbeResult> {
  const r = await http().post('/network/probe', req, { params: { cluster }, timeout: 120_000 });
  return r.data.data;
}
