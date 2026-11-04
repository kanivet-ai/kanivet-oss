// Mirrors backend/internal/rbac/types.go.

export type SubjectKind = 'User' | 'Group' | 'ServiceAccount';

export interface SubjectRef {
  kind: SubjectKind | string;
  name: string;
  namespace?: string;
}

export interface ObjectRef {
  kind: string;
  name: string;
  namespace?: string;
}

export interface RbacRule {
  verbs: string[];
  apiGroups?: string[];
  resources?: string[];
  resourceNames?: string[];
  nonResourceURLs?: string[];
  source?: string;
}

export interface RbacPath {
  binding: ObjectRef;
  role: ObjectRef;
  scope: string;
  rule: RbacRule;
  via?: string;
  roleMissing?: boolean;
}

export interface WhoCanQuery {
  verb: string;
  /** undefined: any group; '' : core group. */
  group?: string;
  resource: string;
  subresource?: string;
  name?: string;
  namespace?: string;
}

export interface WhoCanSubject {
  subject: SubjectRef;
  clusterWide: boolean;
  paths: RbacPath[];
}

export interface WhoCanResult {
  query: WhoCanQuery;
  subjects: WhoCanSubject[];
  warnings?: string[];
}

export interface MatrixCell {
  allowed: boolean;
  limited?: boolean;
  paths?: RbacPath[];
}

export interface MatrixRow {
  group: string;
  resource: string;
  cells: Record<string, MatrixCell>;
}

export interface NonResRow {
  url: string;
  verbs: string[];
  paths: RbacPath[];
}

export interface BindingUse {
  binding: ObjectRef;
  role: ObjectRef;
  scope: string;
  via?: string;
  roleMissing?: boolean;
}

export type RiskSeverity = 'critical' | 'high' | 'medium';

export interface RbacRisk {
  id: string;
  severity: RiskSeverity;
  category: string;
  title: string;
  detail: string;
  binding: ObjectRef;
  role: ObjectRef;
  scope: string;
  subjects: SubjectRef[];
  system?: boolean;
}

export interface PermissionMatrix {
  subject?: SubjectRef;
  namespace?: string;
  verbs: string[];
  rows: MatrixRow[];
  nonResourceURLs?: NonResRow[];
  bindings: BindingUse[];
  risks?: RbacRisk[];
  warnings?: string[];
}

export interface SubjectSummary {
  subject: SubjectRef;
  bindings: number;
  exists: boolean;
}

export interface GraphInfo {
  roles: number;
  clusterRoles: number;
  roleBindings: number;
  clusterRoleBindings: number;
  loadedAt: string;
  warnings?: string[];
}

export interface SelfAccess {
  namespace: string;
  incomplete: boolean;
  matrix: PermissionMatrix;
  evaluationError?: string;
}
