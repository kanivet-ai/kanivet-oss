const SPECIAL_ACCOUNT_LABELS: Record<string, string> = {
  $G: 'Global Account',
  $SYS: 'System Account',
};

export interface FormattedAccountName {
  /** Full label to show in the UI - never truncated. */
  label: string;
}

/**
 * Under decentralized JWT/operator auth, NATS identifies an account only by
 * its NKey public key - there's no separate friendly name the server can show
 * (the JWT's own "name" claim is tooling-only; nats-server doesn't read it).
 * The two built-in accounts get a plain-English label; anything else is
 * shown in full, since a shortened key isn't something anyone can act on.
 */
export const formatNatsAccountName = (name: string): FormattedAccountName => ({
  label: SPECIAL_ACCOUNT_LABELS[name] ?? name,
});
