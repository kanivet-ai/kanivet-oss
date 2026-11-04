import { apiClient } from './client';
import type { CertFilter, CertReport } from '../../types/certs';

/** The cluster's TLS certificate report, optionally narrowed. */
export async function getCertificates(cluster: string, filter: CertFilter = {}): Promise<CertReport> {
  const params: Record<string, string> = { cluster };
  if (filter.namespace) params.namespace = filter.namespace;
  if (filter.status?.length) params.status = filter.status.join(',');
  if (filter.expiringWithin) params.expiringWithin = String(filter.expiringWithin);
  if (filter.search) params.search = filter.search;
  if (filter.source) params.source = filter.source;
  if (filter.kind && filter.name) {
    params.kind = filter.kind;
    params.name = filter.name;
    if (filter.resourceNamespace) params.resourceNamespace = filter.resourceNamespace;
  }
  if (filter.refresh) params.refresh = '1';
  const response = await apiClient.getAxios().get('/certs', { params });
  return response.data.data;
}
