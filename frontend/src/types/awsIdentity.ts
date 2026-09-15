export type AWSIdentityMechanism = 'irsa' | 'pod-identity' | 'none';

export type AWSIdentityStatus =
  | 'ok'
  | 'warning'
  | 'error'
  | 'unknown'
  | 'skipped';

export type AWSCredentialSource = 'exec' | 'override' | 'sso' | 'none';

export interface AWSCredentials {
  source: AWSCredentialSource;
  profile?: string;
  region?: string;
  accountId?: string;
  eksClusterName?: string;
  error?: string;
}

export interface AWSIdentityClusterStatus {
  available: boolean;
  reason?: string;
  credentials: AWSCredentials;
}

export interface AWSIdentityDetail {
  label: string;
  value: string;
  mono?: boolean;
  link?: string;
}

export interface AWSIdentityProblem {
  code: string;
  message: string;
  expected?: string;
  actual?: string;
  hint?: string;
}

export type AWSIdentityStepId =
  | 'pod'
  | 'serviceAccount'
  | 'binding'
  | 'role'
  | 'trust'
  | 'permissions';

export interface AWSIdentityStep {
  id: AWSIdentityStepId;
  title: string;
  status: AWSIdentityStatus;
  summary: string;
  details?: AWSIdentityDetail[];
  problems?: AWSIdentityProblem[];
}

export interface AWSPodEvidence {
  name: string;
  mechanism: AWSIdentityMechanism;
  roleArn?: string;
  tokenVolume?: string;
  injectedEnv?: string[];
  containers?: string[];
  serviceAccount: string;
  phase?: string;
}

export interface AWSIdentityBinding {
  mechanism: AWSIdentityMechanism;
  roleArn?: string;
  annotations?: Record<string, string>;
  associationId?: string;
  associationArn?: string;
  targetRoleArn?: string;
  tags?: Record<string, string>;
}

export interface AWSIAMRole {
  arn: string;
  name: string;
  path?: string;
  accountId: string;
  description?: string;
  maxSessionDuration?: number;
  lastUsedAt?: string;
  lastUsedRegion?: string;
  permissionsBoundaryArn?: string;
  createdAt?: string;
  consoleUrl?: string;
  crossAccount: boolean;
}

export interface AWSTrustStatement {
  index: number;
  sid?: string;
  effect: string;
  principal?: Record<string, string[]>;
  actions: string[];
  conditions?: Record<string, Record<string, string[]>>;
  matches: boolean;
}

export interface AWSTrustPolicy {
  document: string;
  verdict: AWSIdentityStatus;
  matchedStatementIndex: number;
  statements: AWSTrustStatement[];
  oidcIssuer?: string;
  oidcProviderArn?: string;
  oidcProviderExists?: boolean;
  problems?: AWSIdentityProblem[];
}

export type AWSPolicyType = 'managed' | 'inline' | 'boundary';

export interface AWSPolicyStatement {
  index: number;
  sid?: string;
  effect: string;
  actions?: string[];
  notActions?: string[];
  resources?: string[];
  notResources?: string[];
  hasCondition: boolean;
}

export interface AWSIAMPolicy {
  name: string;
  arn?: string;
  type: AWSPolicyType;
  awsManaged: boolean;
  versionId?: string;
  document: string;
  statements: AWSPolicyStatement[];
  consoleUrl?: string;
}

export type AWSAccessDecision =
  | 'allowed'
  | 'explicitDeny'
  | 'implicitDeny'
  | 'error';

export interface AWSMatchedStatement {
  policyName: string;
  policyType: AWSPolicyType;
  policyArn?: string;
  statementIndex: number;
  sid?: string;
  startLine?: number;
  endLine?: number;
}

export interface AWSCheckSource {
  kind: string;
  name?: string;
  container?: string;
}

export interface AWSAccessCheck {
  id: string;
  source: AWSCheckSource;
  service: string;
  action: string;
  resource: string;
  decision: AWSAccessDecision;
  matchedStatements?: AWSMatchedStatement[];
  missingContextKeys?: string[];
  orgDecision?: string;
  boundaryDecision?: string;
  error?: string;
}

export interface AWSIdentityExplanation {
  cluster: string;
  namespace: string;
  serviceAccount: string;
  podName?: string;
  mechanism: AWSIdentityMechanism;
  overall: AWSIdentityStatus;
  credentials: AWSCredentials;
  steps: AWSIdentityStep[];
  pod?: AWSPodEvidence;
  binding?: AWSIdentityBinding;
  role?: AWSIAMRole;
  trust?: AWSTrustPolicy;
  policies?: AWSIAMPolicy[];
  checks?: AWSAccessCheck[];
  permissionError?: string;
  requiredPermissions?: string[];
  generatedAt: string;
}

export interface AWSIdentitySummary {
  namespace: string;
  serviceAccount: string;
  mechanism: AWSIdentityMechanism;
  roleArn: string;
  roleName: string;
  accountId?: string;
  crossAccount: boolean;
  trustStatus: AWSIdentityStatus;
  trustMessage?: string;
  podCount: number;
  lastUsedAt?: string;
}

export interface AWSIdentityTotals {
  total: number;
  irsa: number;
  podIdentity: number;
  broken: number;
  unused90d: number;
}

export interface AWSIdentitiesResponse {
  credentials: AWSCredentials;
  identities: AWSIdentitySummary[];
  totals: AWSIdentityTotals;
  permissionError?: string;
  requiredPermissions?: string[];
  generatedAt: string;
}

export interface AWSCheckRequest {
  action: string;
  resource: string;
}

export interface AWSSimulateRequest {
  roleArn: string;
  checks: AWSCheckRequest[];
}

export interface AWSSimulateResponse {
  checks: AWSAccessCheck[];
}

export interface AWSCredentialsOverride {
  profile: string;
  region?: string;
}
