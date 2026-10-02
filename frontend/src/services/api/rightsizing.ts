import { apiClient } from './client';
import type {
  Evidence,
  RightsizingProfile,
  RightsizingReport,
  RightsizingWindow,
} from '../../types/rightsizing';

/**
 * Returns the cluster's rightsizing report without waiting: ready, stale while
 * a fresh one computes, or progress only. Poll while it carries `progress`.
 * Pass the `version` of the report already held as `known`: if its workloads
 * haven't changed, the answer has `unchanged` set and no workloads.
 */
export async function getRightsizingReport(
  cluster: string,
  profile: RightsizingProfile,
  window: RightsizingWindow,
  refresh = false,
  known?: string,
  provider?: string,
): Promise<RightsizingReport> {
  const params: Record<string, string> = { cluster, profile, window };
  if (provider) params.provider = provider;
  if (refresh) params.refresh = '1';
  if (known) params.known = known;
  const response = await apiClient
    .getAxios()
    .get('/rightsizing/report', { params });
  return response.data.data;
}

export interface WorkloadRef {
  namespace: string;
  kind: string;
  name: string;
  vclusterNamespace?: string;
}

export async function getRightsizingWorkload(
  cluster: string,
  ref: WorkloadRef,
  profile: RightsizingProfile,
  window: RightsizingWindow,
  provider?: string,
): Promise<Evidence> {
  const response = await apiClient.getAxios().get('/rightsizing/workload', {
    params: { cluster, profile, window, provider, ...ref },
    timeout: 120_000,
  });
  return response.data.data;
}

export interface DismissInput extends WorkloadRef {
  container?: string;
  reason: string;
  snoozeDays?: number;
}

export async function dismissRightsizing(
  cluster: string,
  input: DismissInput,
): Promise<void> {
  await apiClient
    .getAxios()
    .post('/rightsizing/dismissals', { cluster, ...input });
}

export async function undismissRightsizing(
  cluster: string,
  input: WorkloadRef & { container?: string },
): Promise<void> {
  await apiClient
    .getAxios()
    .delete('/rightsizing/dismissals', { data: { cluster, ...input } });
}
