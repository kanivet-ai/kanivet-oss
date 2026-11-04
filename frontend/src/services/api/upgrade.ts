import { apiClient } from './client';
import type { UpgradeCatalog, UpgradeReport } from '../../types/upgrade';

/** The cluster's readiness for target ("" = next minor). */
export async function getUpgradeReport(cluster: string, target: string, refresh = false): Promise<UpgradeReport> {
  const params: Record<string, string> = { cluster };
  if (target) params.target = target;
  if (refresh) params.refresh = '1';
  return apiClient.request('/upgrade/report', params, false);
}

let catalogPromise: Promise<UpgradeCatalog> | null = null;

/** The embedded deprecation table; fetched once per session. */
export function getUpgradeCatalog(): Promise<UpgradeCatalog> {
  if (!catalogPromise) {
    catalogPromise = apiClient.request('/upgrade/catalog', undefined, false).catch((err: unknown) => {
      catalogPromise = null;
      throw err;
    });
  }
  return catalogPromise!;
}
