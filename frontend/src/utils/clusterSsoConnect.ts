import { ClusterAuthInfo, SSOSessionStatus } from '../types/cloud';

/** Portals that can hand out role credentials right now. */
export const signedInPortals = (sessions: SSOSessionStatus[]) =>
  sessions.filter((s) => s.state === 'active' || s.state === 'refreshable');

/**
 * The role to preselect: the one already in use, else the same preference
 * discovery applies (read-only, then admin, then whatever comes first).
 */
export const defaultRole = (roles: string[], current?: string) => {
  if (current && roles.includes(current)) return current;
  const has = (...needles: string[]) =>
    roles.find((r) => needles.some((n) => r.toLowerCase().includes(n)));
  return has('readonly', 'read-only', 'viewer') ?? has('admin') ?? roles[0] ?? '';
};

/**
 * Whether to offer reaching this context through IAM Identity Center: an EKS
 * context whose credentials come from somewhere else, or one already bound in
 * Kanivet (to switch role). A context that names its own SSO profile is left
 * alone.
 */
export const offersSsoConnect = (auth: ClusterAuthInfo | null | undefined) =>
  Boolean(
    auth &&
      auth.provider === 'aws' &&
      auth.accountId &&
      (auth.ssoBinding || auth.signIn !== 'aws-sso'),
  );
