export type HealthSeverity = 'critical' | 'warning' | 'info';

export interface HealthRef {
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
}

export interface HealthIssue {
  id: string;
  rule: string;
  severity: HealthSeverity;
  title: string;
  reason: string;
  message: string;
  resource: HealthRef;
  affected?: HealthRef[];
  count: number;
  since: string;
  source: 'state' | 'event';
  hint?: string;
}

export interface HealthIssueSummary {
  total: number;
  critical: number;
  warning: number;
  info: number;
}

export interface HealthIssuesReport {
  issues: HealthIssue[];
  summary: HealthIssueSummary;
  generatedAt: string;
  unavailable?: string[];
}

export type HealthCategory = 'security' | 'reliability' | 'efficiency';

export interface HealthCheck {
  id: string;
  title: string;
  category: HealthCategory;
  severity: HealthSeverity;
  explanation: string;
  fix: string;
  fixPath?: string;
}

export interface HealthFinding {
  checkId: string;
  severity: HealthSeverity;
  category: HealthCategory;
  resource: HealthRef;
  container?: string;
  message: string;
  ignored?: boolean;
}

export interface HealthCheckResult extends HealthCheck {
  failed: number;
  passed: number;
  ignored: number;
}

export interface HealthNamespaceScore {
  namespace: string;
  score: number;
  resources: number;
  critical: number;
  warning: number;
  info: number;
}

export interface HealthAuditReport {
  score: number;
  resources: number;
  namespaces: HealthNamespaceScore[];
  checks: HealthCheckResult[];
  findings: HealthFinding[];
  categories: Partial<Record<HealthCategory, number>>;
  generatedAt: string;
  unavailable?: string[];
}

export interface HealthIgnore {
  cluster: string;
  checkId: string;
  namespace?: string;
  kind?: string;
  name?: string;
  createdAt?: string;
}

export interface ResourceHealth {
  issues: HealthIssue[];
  findings: HealthFinding[];
  checks: HealthCheck[];
}

/** Detail of the `kanivet:investigate` window event. */
export interface InvestigateRequest {
  cluster: string;
  resource: HealthRef;
  /** What prompted it: the issue's title and message. */
  context?: string;
  issue?: HealthIssue;
}
