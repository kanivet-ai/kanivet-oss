import { apiClient } from './client';
import { IncidentTimelineFilters, IncidentTimelineResponse } from '../../types/incidents';

export async function getIncidentTimeline(
  cluster: string,
  filters: IncidentTimelineFilters = {},
): Promise<IncidentTimelineResponse> {
  const params: Record<string, string> = { cluster };
  if (filters.namespaces && filters.namespaces.length) params.namespaces = filters.namespaces.join(',');
  if (filters.kinds && filters.kinds.length) params.kinds = filters.kinds.join(',');
  if (filters.severities && filters.severities.length) params.severities = filters.severities.join(',');
  if (filters.search) params.search = filters.search;
  if (filters.includeRoutine) params.includeRoutine = 'true';
  if (filters.since) params.since = filters.since.toISOString();
  if (filters.until) params.until = filters.until.toISOString();
  if (filters.limit) params.limit = String(filters.limit);
  return await apiClient.request('/incidents/timeline', params, false);
}
