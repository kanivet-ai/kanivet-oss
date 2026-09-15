import {
  AWSAccessCheck,
  AWSAccessDecision,
  AWSCredentials,
  AWSIdentityMechanism,
  AWSIdentityStatus,
  AWSIdentityStep,
  AWSIdentitySummary,
} from '../../types/awsIdentity';

export const IRSA_ROLE_ANNOTATION = 'eks.amazonaws.com/role-arn';

const INJECTED_ENV = new Set([
  'AWS_ROLE_ARN',
  'AWS_WEB_IDENTITY_TOKEN_FILE',
  'AWS_CONTAINER_CREDENTIALS_FULL_URI',
  'AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE',
]);

export interface AWSIdentityTargetInfo {
  namespace: string;
  serviceAccount?: string;
  podName?: string;
  displayName: string;
}

export function identityTargetFromResource(
  resource: any,
): AWSIdentityTargetInfo | null {
  if (!resource?.metadata?.name) return null;
  const namespace = resource.metadata.namespace || 'default';
  const name = resource.metadata.name as string;
  if (resource.kind === 'ServiceAccount') {
    return { namespace, serviceAccount: name, displayName: name };
  }
  if (resource.kind === 'Pod') {
    const serviceAccount =
      resource.spec?.serviceAccountName || resource.spec?.serviceAccount;
    return { namespace, serviceAccount, podName: name, displayName: name };
  }
  return null;
}

export function hasVisibleAWSIdentity(resource: any): boolean {
  if (!resource) return false;
  if (resource.kind === 'ServiceAccount') {
    return !!resource.metadata?.annotations?.[IRSA_ROLE_ANNOTATION];
  }
  if (resource.kind === 'Pod') {
    const containers = [
      ...(resource.spec?.containers || []),
      ...(resource.spec?.initContainers || []),
    ];
    return containers.some((c: any) =>
      (c.env || []).some((e: any) => INJECTED_ENV.has(e?.name)),
    );
  }
  return false;
}

export function roleNameFromArn(arn: string | undefined): string {
  if (!arn) return '';
  const idx = arn.lastIndexOf('/');
  return idx >= 0 ? arn.slice(idx + 1) : arn;
}

export function accountIdFromArn(arn: string | undefined): string {
  if (!arn) return '';
  const parts = arn.split(':');
  return parts.length > 4 ? parts[4] : '';
}

export function mechanismLabel(mechanism: AWSIdentityMechanism): string {
  switch (mechanism) {
    case 'irsa':
      return 'IRSA';
    case 'pod-identity':
      return 'Pod Identity';
    default:
      return 'No AWS identity';
  }
}

export function statusLabel(status: AWSIdentityStatus): string {
  switch (status) {
    case 'ok':
      return 'Healthy';
    case 'warning':
      return 'Needs attention';
    case 'error':
      return 'Broken';
    case 'skipped':
      return 'Skipped';
    default:
      return 'Unknown';
  }
}

export function decisionLabel(decision: AWSAccessDecision): string {
  switch (decision) {
    case 'allowed':
      return 'Allowed';
    case 'explicitDeny':
      return 'Explicit deny';
    case 'implicitDeny':
      return 'No statement allows this';
    default:
      return 'Error';
  }
}

export function decisionStatus(decision: AWSAccessDecision): AWSIdentityStatus {
  switch (decision) {
    case 'allowed':
      return 'ok';
    case 'explicitDeny':
      return 'error';
    case 'implicitDeny':
      return 'warning';
    default:
      return 'unknown';
  }
}

export function credentialSourceLabel(credentials: AWSCredentials): string {
  switch (credentials.source) {
    case 'exec':
      return 'from kubeconfig';
    case 'override':
      return 'override';
    case 'sso':
      return 'SSO';
    default:
      return 'no credentials';
  }
}

export function checkSourceLabel(check: AWSAccessCheck): string {
  const { kind, name, container } = check.source;
  if (kind === 'manual') return 'manual';
  const base = name ? `${kind} ${name}` : kind;
  return container ? `${base} · ${container}` : base;
}

export function firstProblemStep(
  steps: AWSIdentityStep[],
): AWSIdentityStep | undefined {
  return (
    steps.find((s) => s.status === 'error') ||
    steps.find((s) => s.status === 'warning')
  );
}

export function formatRelativeTime(
  iso: string | undefined,
  now: number = Date.now(),
): string {
  if (!iso) return 'never';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return 'never';
  const diff = Math.max(0, now - then);
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(days / 365)}y ago`;
}

export type IdentitySortKey =
  | 'namespace'
  | 'serviceAccount'
  | 'mechanism'
  | 'roleName'
  | 'trustStatus'
  | 'podCount'
  | 'lastUsedAt';

export interface IdentityFilters {
  search: string;
  mechanism: AWSIdentityMechanism | 'all';
  brokenOnly: boolean;
}

const STATUS_RANK: Record<AWSIdentityStatus, number> = {
  error: 0,
  warning: 1,
  unknown: 2,
  skipped: 3,
  ok: 4,
};

export function filterIdentities(
  identities: AWSIdentitySummary[],
  filters: IdentityFilters,
): AWSIdentitySummary[] {
  const q = filters.search.trim().toLowerCase();
  return identities.filter((id) => {
    if (filters.brokenOnly && id.trustStatus === 'ok') return false;
    if (filters.mechanism !== 'all' && id.mechanism !== filters.mechanism) {
      return false;
    }
    if (!q) return true;
    return (
      id.namespace.toLowerCase().includes(q) ||
      id.serviceAccount.toLowerCase().includes(q) ||
      id.roleName.toLowerCase().includes(q) ||
      id.roleArn.toLowerCase().includes(q)
    );
  });
}

export function sortIdentities(
  identities: AWSIdentitySummary[],
  key: IdentitySortKey | null,
  order: 'asc' | 'desc',
): AWSIdentitySummary[] {
  const dir = order === 'asc' ? 1 : -1;
  const byDefault = (a: AWSIdentitySummary, b: AWSIdentitySummary) =>
    STATUS_RANK[a.trustStatus] - STATUS_RANK[b.trustStatus] ||
    a.namespace.localeCompare(b.namespace) ||
    a.serviceAccount.localeCompare(b.serviceAccount);
  if (!key) return [...identities].sort(byDefault);
  return [...identities].sort((a, b) => {
    let cmp = 0;
    switch (key) {
      case 'trustStatus':
        cmp = STATUS_RANK[a.trustStatus] - STATUS_RANK[b.trustStatus];
        break;
      case 'podCount':
        cmp = a.podCount - b.podCount;
        break;
      case 'lastUsedAt': {
        const ta = a.lastUsedAt ? new Date(a.lastUsedAt).getTime() : 0;
        const tb = b.lastUsedAt ? new Date(b.lastUsedAt).getTime() : 0;
        cmp = ta - tb;
        break;
      }
      default:
        cmp = String(a[key]).localeCompare(String(b[key]));
    }
    return cmp !== 0 ? cmp * dir : byDefault(a, b);
  });
}

export function requiredPermissionsPolicy(permissions: string[]): string {
  return JSON.stringify(
    {
      Version: '2012-10-17',
      Statement: [
        {
          Sid: 'KanivetAWSIdentityReadOnly',
          Effect: 'Allow',
          Action: permissions,
          Resource: '*',
        },
      ],
    },
    null,
    2,
  );
}

export function prettyJson(document: string): string {
  try {
    return JSON.stringify(JSON.parse(document), null, 2);
  } catch {
    return document;
  }
}
