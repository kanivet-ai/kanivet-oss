import { SSOSessionStatus } from '../types/cloud';

export const formatTimeLeft = (expiresAt: number, now = Date.now()) => {
  const diff = expiresAt - now;
  if (diff <= 0) return 'expired';
  const hours = Math.floor(diff / 3_600_000);
  const minutes = Math.floor((diff % 3_600_000) / 60_000);
  if (hours >= 24) return `${Math.floor(hours / 24)}d ${hours % 24}h left`;
  if (hours > 0) return `${hours}h ${minutes}m left`;
  return `${Math.max(minutes, 1)}m left`;
};

/**
 * How long an active portal session stays usable. `expiresAt` is the access
 * token's expiry, which AWS caps at about an hour whatever session length the
 * administrator allows. With a refresh token that expiry passes unnoticed, and
 * AWS does not report when the session itself ends, so only count down tokens
 * that cannot be renewed.
 */
export const ssoSessionValidity = (
  s: Pick<SSOSessionStatus, 'refreshable' | 'expiresAt'>,
  now = Date.now(),
) => (s.refreshable ? 'renews automatically' : formatTimeLeft(s.expiresAt, now));
