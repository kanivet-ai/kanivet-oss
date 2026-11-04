import { apiClient } from './client';
import type { CompareBaselines, CompareOptions, CompareRef, CompareResult } from '../../types/compare';

/** Diff two resources (POST /compare). A side that cannot be read comes back
 * with `error`/`status` set on it rather than as a failed request. */
export async function compareResources(
  a: CompareRef,
  b: CompareRef,
  options: CompareOptions = {},
  signal?: AbortSignal,
): Promise<CompareResult> {
  const response = await apiClient.getAxios().post<CompareResult>('/compare', { a, b, options }, { signal });
  return response.data;
}

/** Which declared versions (last-applied, Helm manifest) exist for a live object. */
export async function getCompareBaselines(ref: CompareRef, signal?: AbortSignal): Promise<CompareBaselines> {
  const response = await apiClient.getAxios().get<CompareBaselines>('/compare/baselines', {
    params: {
      cluster: ref.cluster,
      group: ref.group,
      version: ref.version,
      kind: ref.kind,
      namespace: ref.namespace || '',
      name: ref.name,
    },
    signal,
  });
  return response.data;
}
