/**
 * Human-readable message from a failed API call. Prefers the backend's
 * `{ error }` body, then the transport error, then the caller's fallback.
 */
export const getErrorMessage = (err: unknown, fallback: string): string => {
  const e = err as any;
  return e?.response?.data?.error || e?.message || fallback;
};

/**
 * "Failed to restart api: deployments.apps \"api\" not found", or just the
 * action when the error carries no usable detail.
 */
export const failureMessage = (action: string, err: unknown): string => {
  const detail = getErrorMessage(err, '');
  return detail ? `${action}: ${detail}` : action;
};
