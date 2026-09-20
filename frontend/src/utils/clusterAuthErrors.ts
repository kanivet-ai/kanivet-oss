/**
 * Cluster error codes (from the backend's ClassifyClusterError) that mean the
 * user's cloud credentials need attention, as opposed to network or TLS
 * problems. Kept in sync with k8s.IsAuthErrorCode on the Go side.
 */
export const AUTH_ERROR_CODES: ReadonlySet<string> = new Set([
  'aws_sso_expired',
  'aws_token_expired',
  'aws_credentials_missing',
  'azure_auth_expired',
  'azure_kubelogin',
  'gcp_auth_expired',
  'gcp_plugin_missing',
  'unauthorized',
  'token_expired',
  'exec_missing',
  'sso_expired',
]);

export const isAuthErrorCode = (code?: string | null): boolean =>
  Boolean(code && AUTH_ERROR_CODES.has(code));
