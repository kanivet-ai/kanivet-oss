import { apiClient } from './client';
import type {
  GraphInfo,
  PermissionMatrix,
  RbacRisk,
  SelfAccess,
  SubjectRef,
  SubjectSummary,
  WhoCanQuery,
  WhoCanResult,
} from '../../types/rbac';

// RBAC answers change only when bindings change; they are fetched fresh on
// each view (the backend caches the graph for 30 s).

function queryParams(cluster: string, q: WhoCanQuery): Record<string, string> {
  const p: Record<string, string> = { cluster, verb: q.verb, resource: q.resource };
  if (q.group !== undefined) p.group = q.group === '' ? 'core' : q.group;
  if (q.subresource) p.subresource = q.subresource;
  if (q.name) p.name = q.name;
  if (q.namespace) p.namespace = q.namespace;
  return p;
}

export function getRbacSummary(cluster: string): Promise<{ subjects: SubjectSummary[]; info: GraphInfo }> {
  return apiClient.request('/rbac/summary', { cluster }, false);
}

export function whoCan(cluster: string, q: WhoCanQuery): Promise<WhoCanResult> {
  return apiClient.request('/rbac/who-can', queryParams(cluster, q), false);
}

export function getSubjectPermissions(cluster: string, subject: SubjectRef, namespace?: string): Promise<PermissionMatrix> {
  const p: Record<string, string> = { cluster, kind: subject.kind, name: subject.name };
  if (subject.namespace) p.subjectNamespace = subject.namespace;
  if (namespace) p.namespace = namespace;
  return apiClient.request('/rbac/subject', p, false);
}

export function getRolePermissions(cluster: string, kind: string, name: string, namespace?: string): Promise<PermissionMatrix> {
  return apiClient.request('/rbac/role', { cluster, kind, name, namespace: namespace || '' }, false);
}

export function getBindingPermissions(
  cluster: string,
  kind: string,
  name: string,
  namespace?: string,
): Promise<{ matrix: PermissionMatrix; subjects: SubjectRef[] }> {
  return apiClient.request('/rbac/binding', { cluster, kind, name, namespace: namespace || '' }, false);
}

export async function getRiskyGrants(cluster: string, opts: { includeSystem?: boolean; namespace?: string } = {}): Promise<RbacRisk[]> {
  const p: Record<string, string> = { cluster };
  if (opts.includeSystem) p.includeSystem = 'true';
  if (opts.namespace) p.namespace = opts.namespace;
  const res = await apiClient.request('/rbac/risks', p, false);
  return res?.risks || [];
}

export function getSelfAccess(cluster: string, namespace?: string): Promise<SelfAccess> {
  return apiClient.request('/rbac/self', { cluster, namespace: namespace || '' }, false);
}

export function canI(cluster: string, q: WhoCanQuery): Promise<{ allowed: boolean; denied?: boolean; reason?: string }> {
  return apiClient.request('/rbac/can-i', queryParams(cluster, q), false);
}
