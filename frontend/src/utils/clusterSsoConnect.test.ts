import { describe, expect, it } from 'vitest';
import { defaultRole, offersSsoConnect, signedInPortals } from './clusterSsoConnect';
import { ClusterAuthInfo, SSOSessionStatus } from '../types/cloud';

const session = (startUrl: string, state: SSOSessionStatus['state']): SSOSessionStatus => ({
  startUrl,
  region: 'eu-west-1',
  state,
  isValid: state === 'active',
  refreshable: state !== 'signed_out',
  expiresAt: 0,
  source: 'config',
  managed: false,
  profileCount: 0,
});

const auth = (over: Partial<ClusterAuthInfo>): ClusterAuthInfo => ({
  cluster: 'arn:aws:eks:eu-north-1:243517631187:cluster/sbx',
  provider: 'aws',
  method: 'aws-static',
  commandInstalled: true,
  externalTool: true,
  accountId: '243517631187',
  ...over,
});

describe('signedInPortals', () => {
  it('keeps portals whose token works now or renews silently', () => {
    const portals = signedInPortals([
      session('https://a/start', 'active'),
      session('https://b/start', 'refreshable'),
      session('https://c/start', 'expired'),
      session('https://d/start', 'signed_out'),
    ]);
    expect(portals.map((p) => p.startUrl)).toEqual(['https://a/start', 'https://b/start']);
  });
});

describe('defaultRole', () => {
  it('keeps the role the cluster is already bound to', () => {
    expect(defaultRole(['AdministratorAccess', 'ReadOnlyAccess'], 'AdministratorAccess')).toBe('AdministratorAccess');
  });

  it('ignores a bound role the portal no longer grants', () => {
    expect(defaultRole(['AdministratorAccess'], 'Gone')).toBe('AdministratorAccess');
  });

  it('prefers read-only, then admin, then the first role, like discovery does', () => {
    expect(defaultRole(['AdministratorAccess', 'ReadOnlyAccess'])).toBe('ReadOnlyAccess');
    expect(defaultRole(['px-infra', 'AdministratorAccess'])).toBe('AdministratorAccess');
    expect(defaultRole(['px-infra', 'eks-ops'])).toBe('px-infra');
    expect(defaultRole([])).toBe('');
  });
});

describe('offersSsoConnect', () => {
  it('offers it for an EKS context that leans on another tool or the default chain', () => {
    expect(offersSsoConnect(auth({}))).toBe(true);
    expect(offersSsoConnect(auth({ method: 'aws-default-chain' }))).toBe(true);
  });

  it('offers it for a bound cluster so the role can be switched', () => {
    expect(
      offersSsoConnect(
        auth({
          method: 'aws-sso',
          signIn: 'aws-sso',
          externalTool: false,
          ssoBinding: { startUrl: 'https://a/start', accountId: '243517631187', roleName: 'ReadOnlyAccess', profile: 'p' },
        }),
      ),
    ).toBe(true);
  });

  it('stays out of the way when the context already names an SSO profile, or is not EKS', () => {
    expect(offersSsoConnect(auth({ method: 'aws-sso', signIn: 'aws-sso', externalTool: false }))).toBe(false);
    expect(offersSsoConnect(auth({ provider: 'gcp', accountId: undefined }))).toBe(false);
    expect(offersSsoConnect(auth({ accountId: undefined }))).toBe(false);
    expect(offersSsoConnect(null)).toBe(false);
  });
});
