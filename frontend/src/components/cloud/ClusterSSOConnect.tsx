import { useEffect, useMemo, useState } from 'react';
import { useStore } from '../../store';
import cloudService from '../../services/cloudService';
import { ClusterSSOBinding, SSOSessionStatus } from '../../types/cloud';
import { normalizeStartUrl } from '../../store/cloudAuthSlice';
import { defaultRole, signedInPortals } from '../../utils/clusterSsoConnect';
import './ClusterSSOConnect.css';

interface Props {
  cluster: string;
  accountId: string;
  binding?: ClusterSSOBinding;
  /** Called after the identity used for this cluster changed. */
  onChanged: () => void;
}

interface Option {
  portal: SSOSessionStatus;
  accountName: string;
  roles: string[];
}

/**
 * Lets an EKS context be reached through IAM Identity Center with a chosen
 * role, for kubeconfigs whose exec block follows the default profile (Leapp,
 * aws-vault, static keys). The choice is Kanivet state: the kubeconfig and the
 * terminal keep their own behaviour.
 */
const ClusterSSOConnect = ({ cluster, accountId, binding, onChanged }: Props) => {
  const ssoSessions = useStore((s) => s.ssoSessions);
  const portals = useMemo(() => signedInPortals(ssoSessions), [ssoSessions]);
  const portalKey = portals.map((p) => p.startUrl).join('|');

  const [options, setOptions] = useState<Option[] | null>(null);
  const [startUrl, setStartUrl] = useState('');
  const [role, setRole] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Find which signed-in portals grant this account, and with which roles.
  useEffect(() => {
    let cancelled = false;
    setOptions(null);
    setError(null);
    Promise.all(
      portals.map(async (portal): Promise<Option | null> => {
        try {
          const accounts = await cloudService.getAWSSSOAccounts(portal.startUrl);
          const account = accounts.find((a) => a.accountId === accountId);
          if (!account) return null;
          const roles = await cloudService.getAWSSSOAccountRoles(portal.startUrl, accountId);
          return roles.length > 0 ? { portal, accountName: account.accountName, roles } : null;
        } catch {
          return null;
        }
      }),
    ).then((found) => {
      if (cancelled) return;
      const usable = found.filter((o): o is Option => o !== null);
      setOptions(usable);
      const bound = binding && usable.find((o) => normalizeStartUrl(o.portal.startUrl) === normalizeStartUrl(binding.startUrl));
      const chosen = bound || usable[0];
      setStartUrl(chosen ? chosen.portal.startUrl : '');
      setRole(chosen ? defaultRole(chosen.roles, bound ? binding?.roleName : undefined) : '');
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [portalKey, accountId, binding?.startUrl, binding?.roleName]);

  if (portals.length === 0) {
    return (
      <div className="cluster-sso-connect-muted">
        Sign in to an AWS access portal from Cloud accounts to reach account <code>{accountId}</code> without another tool.
      </div>
    );
  }
  if (options === null) {
    return (
      <div className="cluster-sso-connect-muted">
        <span className="ap-spinner" /> Checking which roles you have in <code>{accountId}</code>…
      </div>
    );
  }
  if (options.length === 0) {
    return (
      <div className="cluster-sso-connect-muted">
        None of your signed-in AWS access portals grants account <code>{accountId}</code>.
      </div>
    );
  }

  const selected = options.find((o) => o.portal.startUrl === startUrl) || options[0];
  const unchanged =
    binding !== undefined &&
    binding.roleName === role &&
    normalizeStartUrl(binding.startUrl) === normalizeStartUrl(selected.portal.startUrl);

  const run = async (action: () => Promise<unknown>, fallback: string) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      onChanged();
    } catch (err: any) {
      setError(err.response?.data?.error || err.message || fallback);
    } finally {
      setBusy(false);
    }
  };

  const handleConnect = () =>
    run(() => cloudService.bindClusterSSO(cluster, selected.portal.startUrl, accountId, role), 'Could not connect with AWS SSO');
  const handleDisconnect = () =>
    run(() => cloudService.unbindClusterSSO(cluster), 'Could not stop using AWS SSO');

  return (
    <div className="cluster-sso-connect">
      <div className="cluster-sso-connect-text">
        {binding ? (
          <>
            Kanivet reaches this cluster as <code>{binding.roleName}</code> in{' '}
            <code>{selected.accountName}</code> through IAM Identity Center.
          </>
        ) : (
          <>
            Your AWS access portal grants <code>{selected.accountName}</code> (<code>{accountId}</code>). Kanivet can
            use it for this cluster instead of the active profile.
          </>
        )}{' '}
        Your kubeconfig and terminal are not changed.
      </div>
      <div className="cluster-sso-connect-row">
        {options.length > 1 && (
          <select
            className="ap-select cluster-sso-connect-select"
            aria-label="AWS access portal"
            value={selected.portal.startUrl}
            disabled={busy}
            onChange={(e) => {
              const next = options.find((o) => o.portal.startUrl === e.target.value);
              setStartUrl(e.target.value);
              if (next) setRole(defaultRole(next.roles));
            }}
          >
            {options.map((o) => (
              <option key={o.portal.startUrl} value={o.portal.startUrl}>
                {o.portal.label || o.portal.startUrl}
              </option>
            ))}
          </select>
        )}
        <select
          className="ap-select cluster-sso-connect-select"
          aria-label="Role"
          value={role}
          disabled={busy}
          onChange={(e) => setRole(e.target.value)}
        >
          {selected.roles.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
        <button className="ap-btn ap-btn--primary ap-btn--sm" onClick={handleConnect} disabled={busy || !role || unchanged}>
          {busy ? 'Connecting…' : binding ? 'Switch role' : 'Connect with AWS SSO'}
        </button>
        {binding && (
          <button className="ap-btn ap-btn--ghost ap-btn--sm" onClick={handleDisconnect} disabled={busy}>
            Use kubeconfig credentials
          </button>
        )}
      </div>
      {error && <div className="cluster-sso-connect-error">{error}</div>}
    </div>
  );
};

export default ClusterSSOConnect;
