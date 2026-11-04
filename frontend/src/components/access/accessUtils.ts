import type { MatrixCell, MatrixRow, ObjectRef, RbacPath, RbacRisk, RiskSeverity, SubjectRef } from '../../types/rbac';

export const RBAC_API_VERSION = 'rbac.authorization.k8s.io/v1';

export const subjectKey = (s: SubjectRef): string =>
  s.kind === 'ServiceAccount' ? `ServiceAccount:${s.namespace || ''}/${s.name}` : `${s.kind}:${s.name}`;

export const parseSubjectKey = (key: string): SubjectRef | null => {
  const i = key.indexOf(':');
  if (i < 0) return null;
  const kind = key.slice(0, i);
  const rest = key.slice(i + 1);
  if (kind === 'ServiceAccount') {
    const j = rest.indexOf('/');
    if (j < 0) return null;
    return { kind, namespace: rest.slice(0, j), name: rest.slice(j + 1) };
  }
  if (kind !== 'User' && kind !== 'Group') return null;
  return rest ? { kind, name: rest } : null;
};

export const subjectLabel = (s: SubjectRef): string =>
  s.kind === 'ServiceAccount' ? `${s.namespace}/${s.name}` : s.name;

/** "deployments.apps", "pods", "*" — the kubectl spelling of a row. */
export const resourceLabel = (row: Pick<MatrixRow, 'group' | 'resource'>): string => {
  if (row.resource === '*' && row.group === '*') return '*';
  if (!row.group) return row.resource;
  return `${row.resource}.${row.group}`;
};

export type CellTone = 'none' | 'full' | 'limited' | 'wildcard';

/** How a matrix cell is coloured: granted outright, only for named objects,
 * through a wildcard verb or resource, or not at all. */
export const cellTone = (row: MatrixRow, cell: MatrixCell | undefined): CellTone => {
  if (!cell?.allowed) return 'none';
  if (cell.limited) return 'limited';
  const viaWildcard = row.resource === '*' || (cell.paths || []).every((p) => p.rule.verbs.includes('*'));
  return viaWildcard ? 'wildcard' : 'full';
};

export const scopeLabel = (scope: string): string => (scope ? `in ${scope}` : 'cluster-wide');

/** "RoleBinding team/ci-exec → Role exec (cluster-wide)". */
export const pathText = (p: RbacPath): string => {
  const b = p.binding.kind ? `${p.binding.kind} ${p.binding.namespace ? `${p.binding.namespace}/` : ''}${p.binding.name}` : '';
  const r = `${p.role.kind} ${p.role.name}`;
  const src = p.rule.source ? ` via ${p.rule.source}` : '';
  const names = p.rule.resourceNames?.length ? ` [${p.rule.resourceNames.join(', ')}]` : '';
  const via = p.via ? ` as ${p.via}` : '';
  return `${b ? `${b} → ` : ''}${r}${src}${names} (${scopeLabel(p.scope)})${via}`;
};

/** Parses "pods", "pods/exec", "deployments.apps", "deployments.apps/scale". */
export const parseResourceInput = (input: string): { resource: string; group?: string; subresource?: string } => {
  const t = input.trim().toLowerCase();
  const [main, sub] = t.split('/', 2);
  const dot = main.indexOf('.');
  const out: { resource: string; group?: string; subresource?: string } = { resource: dot < 0 ? main : main.slice(0, dot) };
  if (dot >= 0) out.group = main.slice(dot + 1);
  if (sub) out.subresource = sub;
  return out;
};

export interface ResourceOption {
  label: string;
  resource: string;
  group: string;
}

// Common targets for the "Who can…" picker; free text covers the rest.
export const COMMON_RESOURCES: ResourceOption[] = [
  ['pods', ''], ['pods/exec', ''], ['pods/log', ''], ['pods/portforward', ''], ['secrets', ''], ['configmaps', ''],
  ['services', ''], ['serviceaccounts', ''], ['serviceaccounts/token', ''], ['namespaces', ''], ['nodes', ''],
  ['nodes/proxy', ''], ['persistentvolumeclaims', ''], ['persistentvolumes', ''], ['events', ''],
  ['deployments', 'apps'], ['statefulsets', 'apps'], ['daemonsets', 'apps'], ['replicasets', 'apps'],
  ['jobs', 'batch'], ['cronjobs', 'batch'], ['ingresses', 'networking.k8s.io'], ['networkpolicies', 'networking.k8s.io'],
  ['roles', 'rbac.authorization.k8s.io'], ['rolebindings', 'rbac.authorization.k8s.io'],
  ['clusterroles', 'rbac.authorization.k8s.io'], ['clusterrolebindings', 'rbac.authorization.k8s.io'],
  ['customresourcedefinitions', 'apiextensions.k8s.io'],
].map(([resource, group]) => ({ resource, group, label: group ? `${resource.split('/')[0]}.${group}${resource.includes('/') ? `/${resource.split('/')[1]}` : ''}` : resource }));

/** COMMON_RESOURCES as combobox rows, grouped by API group; subresources last. */
export const RESOURCE_SUGGESTIONS = (() => {
  const groupName = (o: ResourceOption) =>
    o.resource.includes('/') ? 'Subresources' : o.group ? o.group.split('.')[0] : 'core';
  const rows = COMMON_RESOURCES.map((o) => ({ value: o.label, group: groupName(o), hint: o.group || undefined }));
  const order = ['core', 'apps', 'batch', 'networking', 'rbac', 'apiextensions', 'Subresources'];
  return rows.sort((a, b) => order.indexOf(a.group) - order.indexOf(b.group));
})();

export const VERBS = ['get', 'list', 'watch', 'create', 'update', 'patch', 'delete', 'deletecollection', 'escalate', 'bind', 'impersonate', '*'];

export const filterRows = (rows: MatrixRow[], text: string, onlyAllowedVerbs?: string[]): MatrixRow[] => {
  const t = text.trim().toLowerCase();
  return rows.filter((r) => {
    if (t && !resourceLabel(r).toLowerCase().includes(t)) return false;
    if (onlyAllowedVerbs?.length) return onlyAllowedVerbs.some((v) => r.cells[v]?.allowed);
    return true;
  });
};

export const SEVERITY_ORDER: RiskSeverity[] = ['critical', 'high', 'medium'];

export const countBySeverity = (risks: RbacRisk[]): Record<RiskSeverity, number> => {
  const out: Record<RiskSeverity, number> = { critical: 0, high: 0, medium: 0 };
  for (const r of risks) out[r.severity] = (out[r.severity] || 0) + 1;
  return out;
};

export const severityBadge = (s: RiskSeverity): string =>
  s === 'critical' ? 'ap-badge--danger' : s === 'high' ? 'ap-badge--warning' : 'ap-badge--info';

/** Kinds that open the inspector; Users and Groups are not API objects. */
export const isLinkable = (ref: ObjectRef | SubjectRef): boolean =>
  ['ServiceAccount', 'Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding'].includes(ref.kind);

export const apiVersionOf = (kind: string): string => (kind === 'ServiceAccount' ? 'v1' : RBAC_API_VERSION);
