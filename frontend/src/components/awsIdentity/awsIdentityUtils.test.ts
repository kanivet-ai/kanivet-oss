import { describe, expect, it } from 'vitest';
import {
  filterIdentities,
  formatRelativeTime,
  hasVisibleAWSIdentity,
  identityTargetFromResource,
  roleNameFromArn,
  sortIdentities,
} from './awsIdentityUtils';
import { AWSIdentitySummary } from '../../types/awsIdentity';

const identity = (
  overrides: Partial<AWSIdentitySummary>,
): AWSIdentitySummary => ({
  namespace: 'default',
  serviceAccount: 'api',
  mechanism: 'irsa',
  roleArn: 'arn:aws:iam::123456789012:role/api',
  roleName: 'api',
  crossAccount: false,
  trustStatus: 'ok',
  podCount: 1,
  ...overrides,
});

describe('identityTargetFromResource', () => {
  it('reads the service account name from a Pod spec', () => {
    const target = identityTargetFromResource({
      kind: 'Pod',
      metadata: { name: 'api-0', namespace: 'payments' },
      spec: { serviceAccountName: 'api' },
    });
    expect(target).toEqual({
      namespace: 'payments',
      serviceAccount: 'api',
      podName: 'api-0',
      displayName: 'api-0',
    });
  });

  it('uses the ServiceAccount itself as the target', () => {
    const target = identityTargetFromResource({
      kind: 'ServiceAccount',
      metadata: { name: 'api', namespace: 'payments' },
    });
    expect(target).toEqual({
      namespace: 'payments',
      serviceAccount: 'api',
      displayName: 'api',
    });
  });

  it('ignores unrelated kinds', () => {
    expect(
      identityTargetFromResource({ kind: 'Service', metadata: { name: 'x' } }),
    ).toBeNull();
  });
});

describe('hasVisibleAWSIdentity', () => {
  it('detects the IRSA annotation on a ServiceAccount', () => {
    expect(
      hasVisibleAWSIdentity({
        kind: 'ServiceAccount',
        metadata: {
          annotations: {
            'eks.amazonaws.com/role-arn': 'arn:aws:iam::1:role/r',
          },
        },
      }),
    ).toBe(true);
  });

  it('detects injected env on a Pod container', () => {
    expect(
      hasVisibleAWSIdentity({
        kind: 'Pod',
        spec: {
          containers: [{ env: [{ name: 'AWS_ROLE_ARN', value: 'x' }] }],
        },
      }),
    ).toBe(true);
    expect(
      hasVisibleAWSIdentity({
        kind: 'Pod',
        spec: { containers: [{ env: [{ name: 'FOO', value: 'x' }] }] },
      }),
    ).toBe(false);
  });
});

describe('roleNameFromArn', () => {
  it('returns the last path segment', () => {
    expect(roleNameFromArn('arn:aws:iam::1:role/team/api')).toBe('api');
    expect(roleNameFromArn(undefined)).toBe('');
  });
});

describe('formatRelativeTime', () => {
  const now = new Date('2026-09-14T12:00:00Z').getTime();
  it('formats never, minutes, days and months', () => {
    expect(formatRelativeTime(undefined, now)).toBe('never');
    expect(formatRelativeTime('2026-09-14T11:55:00Z', now)).toBe('5m ago');
    expect(formatRelativeTime('2026-09-10T12:00:00Z', now)).toBe('4d ago');
    expect(formatRelativeTime('2026-05-14T12:00:00Z', now)).toBe('4mo ago');
  });
});

describe('filterIdentities', () => {
  const list = [
    identity({ serviceAccount: 'api', trustStatus: 'ok' }),
    identity({
      serviceAccount: 'worker',
      mechanism: 'pod-identity',
      trustStatus: 'error',
    }),
  ];

  it('applies broken-only, mechanism and search filters', () => {
    expect(
      filterIdentities(list, {
        search: '',
        mechanism: 'all',
        brokenOnly: true,
      }).map((i) => i.serviceAccount),
    ).toEqual(['worker']);
    expect(
      filterIdentities(list, {
        search: '',
        mechanism: 'irsa',
        brokenOnly: false,
      }).map((i) => i.serviceAccount),
    ).toEqual(['api']);
    expect(
      filterIdentities(list, {
        search: 'WORK',
        mechanism: 'all',
        brokenOnly: false,
      }).map((i) => i.serviceAccount),
    ).toEqual(['worker']);
  });
});

describe('sortIdentities', () => {
  it('puts broken identities first by default', () => {
    const list = [
      identity({ namespace: 'a', serviceAccount: 'ok', trustStatus: 'ok' }),
      identity({ namespace: 'z', serviceAccount: 'bad', trustStatus: 'error' }),
      identity({
        namespace: 'b',
        serviceAccount: 'warn',
        trustStatus: 'warning',
      }),
    ];
    expect(
      sortIdentities(list, null, 'asc').map((i) => i.serviceAccount),
    ).toEqual(['bad', 'warn', 'ok']);
  });

  it('sorts by an explicit column and direction', () => {
    const list = [
      identity({ serviceAccount: 'a', podCount: 3 }),
      identity({ serviceAccount: 'b', podCount: 1 }),
    ];
    expect(
      sortIdentities(list, 'podCount', 'asc').map((i) => i.serviceAccount),
    ).toEqual(['b', 'a']);
    expect(
      sortIdentities(list, 'podCount', 'desc').map((i) => i.serviceAccount),
    ).toEqual(['a', 'b']);
  });
});
