import { useEffect, useMemo, useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import {
  ChevronRightIcon,
  Cross2Icon,
  ExitIcon,
  IdCardIcon,
  Pencil1Icon,
  PlusIcon,
  ReloadIcon,
  TrashIcon,
} from '@radix-ui/react-icons';
import { useStore } from '../store';
import { useShallow } from 'zustand/react/shallow';
import cloudService from '../services/cloudService';
import { normalizeStartUrl } from '../store/cloudAuthSlice';
import { ssoSessionValidity } from '../utils/ssoSessionLabel';
import { offersSsoConnect } from '../utils/clusterSsoConnect';
import {
  ClusterAuthInfo,
  ProviderAuthSummary,
  SSOAccount,
  SSOSessionStatus,
} from '../types/cloud';
import AWSIcon from './AWSIcon';
import GCPIcon from './GCPIcon';
import AzureIcon from './AzureIcon';
import LoginProgress from './cloud/LoginProgress';
import AWSRegionSelect from './cloud/AWSRegionSelect';
import ClusterSSOConnect from './cloud/ClusterSSOConnect';
import './CloudAccountsMenu.css';

const needsSignIn = (s: SSOSessionStatus) =>
  s.state === 'expired' || s.state === 'signed_out';

export const describeSession = (
  s: SSOSessionStatus,
): { text: string; tone: 'ok' | 'info' | 'attention' } => {
  switch (s.state) {
    case 'active':
      return { text: `Signed in · ${ssoSessionValidity(s)}`, tone: 'ok' };
    case 'refreshable':
      return { text: 'Signed in · renewing', tone: 'info' };
    case 'expired':
      return {
        text: s.error
          ? 'Expired · refresh rejected, sign in required'
          : 'Expired · sign in required',
        tone: 'attention',
      };
    default:
      return { text: 'Not signed in', tone: 'attention' };
  }
};

const describeProvider = (
  p?: ProviderAuthSummary,
): { text: string; tone: 'ok' | 'info' | 'attention' | 'muted' } => {
  if (!p) return { text: 'Checking…', tone: 'muted' };
  switch (p.state) {
    case 'active':
      return {
        text: p.detail ? `Signed in · ${p.detail}` : 'Signed in',
        tone: 'ok',
      };
    case 'expired':
      return { text: 'Sign-in expired', tone: 'attention' };
    case 'signed_out':
      return { text: 'Not signed in', tone: 'attention' };
    default:
      return { text: `${p.cliName} not installed`, tone: 'muted' };
  }
};

const toneDot: Record<'ok' | 'info' | 'attention' | 'muted', string> = {
  ok: 'ap-dot--success',
  info: 'ap-dot--info',
  attention: 'ap-dot--warning',
  muted: 'ap-dot--muted',
};

/**
 * Toolbar popover listing every cloud identity Kanivet can use — AWS IAM
 * Identity Center portals, the gcloud account and the Azure CLI account — with
 * one-click sign-in. Nothing here happens automatically: the backend renews
 * tokens silently, and only a button press ever opens a browser.
 */
const CloudAccountsMenu = () => {
  const {
    ssoSessions,
    ssoLogins,
    authSummary,
    authLoaded,
    providerLogins,
    loadAuthSummary,
    signInSSO,
    cancelSSOLogin,
    signOutSSO,
    addSsoSession,
    removeSsoSession,
    updateSsoSessionLabel,
    signInProvider,
    cancelProviderLogin,
  } = useStore(
    useShallow((s) => ({
      ssoSessions: s.ssoSessions,
      ssoLogins: s.ssoLogins,
      authSummary: s.authSummary,
      authLoaded: s.authLoaded,
      providerLogins: s.providerLogins,
      loadAuthSummary: s.loadAuthSummary,
      signInSSO: s.signInSSO,
      cancelSSOLogin: s.cancelSSOLogin,
      signOutSSO: s.signOutSSO,
      addSsoSession: s.addSsoSession,
      removeSsoSession: s.removeSsoSession,
      updateSsoSessionLabel: s.updateSsoSessionLabel,
      signInProvider: s.signInProvider,
      cancelProviderLogin: s.cancelProviderLogin,
    })),
  );

  const currentTab = useStore((s) => s.currentTab);
  const [isOpen, setIsOpen] = useState(false);
  // How the open cluster authenticates, to offer its AWS account and role here.
  const [tabAuth, setTabAuth] = useState<ClusterAuthInfo | null>(null);
  const [tabAuthVersion, setTabAuthVersion] = useState(0);
  const [showAddForm, setShowAddForm] = useState(false);
  const [newUrl, setNewUrl] = useState('');
  const [newRegion, setNewRegion] = useState('us-east-1');
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [accounts, setAccounts] = useState<Map<string, SSOAccount[]>>(
    new Map(),
  );
  const [accountsLoading, setAccountsLoading] = useState<Set<string>>(
    new Set(),
  );
  const [accountsError, setAccountsError] = useState<Map<string, string>>(
    new Map(),
  );
  const [editing, setEditing] = useState<{ url: string; label: string } | null>(
    null,
  );
  const [, setTick] = useState(0);

  useEffect(() => {
    if (!isOpen) return;
    loadAuthSummary(true);
    const timer = setInterval(() => setTick((t) => t + 1), 60_000);
    return () => clearInterval(timer);
  }, [isOpen, loadAuthSummary]);

  useEffect(() => {
    setTabAuth(null);
    if (!isOpen || !currentTab) return;
    let cancelled = false;
    cloudService
      .describeClusterAuth(currentTab)
      .then((info) => {
        if (!cancelled) setTabAuth(info);
      })
      .catch(() => {
        if (!cancelled) setTabAuth(null);
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, currentTab, tabAuthVersion]);

  useEffect(() => {
    const open = () => {
      setIsOpen(true);
    };
    const openAdd = () => {
      setIsOpen(true);
      setShowAddForm(true);
    };
    window.addEventListener('cloud:openAccounts', open);
    window.addEventListener('cloud:addAwsPortal', openAdd);
    return () => {
      window.removeEventListener('cloud:openAccounts', open);
      window.removeEventListener('cloud:addAwsPortal', openAdd);
    };
  }, []);

  const attention = useMemo(() => {
    const aws = ssoSessions.filter(needsSignIn).length;
    const gcp = authSummary?.gcp?.state === 'expired' ? 1 : 0;
    const azure = authSummary?.azure?.state === 'expired' ? 1 : 0;
    return aws + gcp + azure;
  }, [ssoSessions, authSummary]);
  const signedIn =
    ssoSessions.some((s) => !needsSignIn(s)) ||
    Boolean(authSummary?.gcp?.signedIn) ||
    Boolean(authSummary?.azure?.signedIn);

  const loadAccounts = async (startUrl: string) => {
    if (accounts.has(startUrl) || accountsLoading.has(startUrl)) return;
    setAccountsLoading((prev) => new Set(prev).add(startUrl));
    try {
      const list = await cloudService.getAWSSSOAccounts(startUrl);
      setAccounts((prev) => new Map(prev).set(startUrl, list));
      setAccountsError((prev) => {
        const next = new Map(prev);
        next.delete(startUrl);
        return next;
      });
    } catch (err: any) {
      setAccountsError((prev) =>
        new Map(prev).set(
          startUrl,
          err?.response?.data?.error ||
            err?.message ||
            'Could not list accounts',
        ),
      );
    } finally {
      setAccountsLoading((prev) => {
        const next = new Set(prev);
        next.delete(startUrl);
        return next;
      });
    }
  };

  const toggleExpanded = (session: SSOSessionStatus) => {
    if (needsSignIn(session)) return;
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(session.startUrl)) next.delete(session.startUrl);
      else {
        next.add(session.startUrl);
        loadAccounts(session.startUrl);
      }
      return next;
    });
  };

  const handleAdd = async () => {
    const url = newUrl.trim();
    if (!url) return;
    setAdding(true);
    setAddError(null);
    try {
      const ok = await addSsoSession(url, newRegion);
      if (ok) {
        setNewUrl('');
        setShowAddForm(false);
      }
    } catch (err: any) {
      setAddError(err?.message || 'Could not start sign-in');
    } finally {
      setAdding(false);
    }
  };

  const handleSignIn = async (session: SSOSessionStatus) => {
    const ok = await signInSSO(session.startUrl, session.region);
    if (ok) {
      setAccounts((prev) => {
        const next = new Map(prev);
        next.delete(session.startUrl);
        return next;
      });
    }
  };

  const saveLabel = () => {
    if (!editing) return;
    const label = editing.label.trim();
    if (label) updateSsoSessionLabel(editing.url, label);
    setEditing(null);
  };

  const renderSession = (session: SSOSessionStatus) => {
    const key = normalizeStartUrl(session.startUrl);
    const login = ssoLogins[key];
    const pending = login?.state === 'pending';
    const showLogin =
      login && login.state !== 'authorized' && login.state !== 'cancelled';
    const { text, tone } = describeSession(session);
    const attentionNeeded = needsSignIn(session);
    const isExpanded = expanded.has(session.startUrl) && !attentionNeeded;
    const list = accounts.get(session.startUrl) || [];
    const isEditing = editing?.url === session.startUrl;

    return (
      <div
        key={session.startUrl}
        className={`cloud-account ${attentionNeeded ? 'attention' : ''}`}
      >
        <div className="cloud-account-row">
          <button
            className="cloud-account-expand"
            onClick={() => toggleExpanded(session)}
            disabled={attentionNeeded}
            aria-label={isExpanded ? 'Hide accounts' : 'Show accounts'}
          >
            <ChevronRightIcon
              style={{ transform: isExpanded ? 'rotate(90deg)' : 'none' }}
            />
          </button>
          <span className="cloud-account-icon">
            <AWSIcon />
          </span>
          <div
            className="cloud-account-text"
            onClick={() => !isEditing && toggleExpanded(session)}
          >
            {isEditing ? (
              <input
                className="ap-input ap-input--sm cloud-account-rename"
                value={editing.label}
                autoFocus
                onChange={(e) =>
                  setEditing({ url: session.startUrl, label: e.target.value })
                }
                onKeyDown={(e) => {
                  e.stopPropagation();
                  if (e.key === 'Enter') saveLabel();
                  if (e.key === 'Escape') setEditing(null);
                }}
                onBlur={saveLabel}
                onClick={(e) => e.stopPropagation()}
                placeholder="Portal name"
              />
            ) : (
              <span className="cloud-account-name" title={session.startUrl}>
                {session.label || session.startUrl}
              </span>
            )}
            <span className={`cloud-account-status tone-${tone}`}>
              <span className={`ap-dot ${toneDot[tone]}`} />
              {text}
              {session.source === 'config' && (
                <span
                  className="ap-badge ap-badge--sm"
                  title="Defined in ~/.aws/config"
                >
                  ~/.aws/config
                </span>
              )}
            </span>
          </div>
          <div className="cloud-account-actions">
            {attentionNeeded && !pending && (
              <button
                className="ap-btn ap-btn--primary ap-btn--sm"
                onClick={() => handleSignIn(session)}
              >
                Sign in
              </button>
            )}
            {!attentionNeeded && (
              <button
                className="ap-icon-btn ap-icon-btn--sm"
                title="Sign in again"
                onClick={() => handleSignIn(session)}
                disabled={pending}
              >
                <ReloadIcon />
              </button>
            )}
            <button
              className="ap-icon-btn ap-icon-btn--sm"
              title="Rename"
              onClick={() =>
                setEditing({
                  url: session.startUrl,
                  label: session.label || '',
                })
              }
            >
              <Pencil1Icon />
            </button>
            {!attentionNeeded && (
              <button
                className="ap-icon-btn ap-icon-btn--sm"
                title="Sign out (also signs the AWS CLI out of this portal)"
                onClick={() => signOutSSO(session.startUrl)}
              >
                <ExitIcon />
              </button>
            )}
            {session.managed && (
              <button
                className="ap-icon-btn ap-icon-btn--sm danger"
                title="Remove from Kanivet"
                onClick={() => removeSsoSession(session.startUrl)}
              >
                <TrashIcon />
              </button>
            )}
          </div>
        </div>
        {showLogin && (
          <div className="cloud-account-login">
            <LoginProgress
              state={
                login.state === 'pending'
                  ? 'pending'
                  : login.state === 'expired'
                    ? 'expired'
                    : 'failed'
              }
              code={login.userCode}
              url={login.verificationUrlComplete}
              error={login.error}
              onCancel={() => cancelSSOLogin(session.startUrl)}
              onRetry={() => handleSignIn(session)}
              onDismiss={() => cancelSSOLogin(session.startUrl)}
              compact
            />
          </div>
        )}
        {isExpanded && (
          <div className="cloud-account-children">
            {accountsLoading.has(session.startUrl) ? (
              <div className="cloud-account-muted">
                <span className="ap-spinner" /> Loading accounts…
              </div>
            ) : accountsError.get(session.startUrl) ? (
              <div className="cloud-account-muted">
                {accountsError.get(session.startUrl)}
              </div>
            ) : list.length === 0 ? (
              <div className="cloud-account-muted">
                No accounts assigned to you.
              </div>
            ) : (
              list.map((acc) => (
                <div key={acc.accountId} className="cloud-account-child">
                  <span className="cloud-account-child-name">
                    {acc.accountName}
                  </span>
                  <span className="cloud-account-child-id ap-mono">
                    {acc.accountId}
                  </span>
                </div>
              ))
            )}
          </div>
        )}
      </div>
    );
  };

  const renderProvider = (provider: 'gcp' | 'azure') => {
    const summary = provider === 'gcp' ? authSummary?.gcp : authSummary?.azure;
    const login = providerLogins[provider];
    const running = login?.state === 'running';
    const showLogin =
      login && login.state !== 'succeeded' && login.state !== 'cancelled';
    const { text, tone } = describeProvider(summary);
    const Icon = provider === 'gcp' ? GCPIcon : AzureIcon;
    const cli = provider === 'gcp' ? 'gcloud' : 'Azure CLI';
    const title = provider === 'gcp' ? 'Google Cloud' : 'Azure';
    const canSignIn = summary?.cliInstalled ?? false;

    return (
      <div
        className={`cloud-account ${tone === 'attention' ? 'attention' : ''}`}
      >
        <div className="cloud-account-row">
          <span className="cloud-account-expand placeholder" />
          <span className="cloud-account-icon">
            <Icon />
          </span>
          <div className="cloud-account-text">
            <span className="cloud-account-name">
              {summary?.identity || title}
            </span>
            <span className={`cloud-account-status tone-${tone}`}>
              <span className={`ap-dot ${toneDot[tone]}`} />
              {text}
            </span>
          </div>
          <div className="cloud-account-actions always">
            {canSignIn && (
              <button
                className={`ap-btn ap-btn--sm ${summary?.signedIn ? '' : 'ap-btn--primary'}`}
                onClick={() => signInProvider(provider)}
                disabled={running}
              >
                {running
                  ? 'Waiting…'
                  : summary?.signedIn
                    ? 'Switch'
                    : `Sign in with ${cli}`}
              </button>
            )}
          </div>
        </div>
        {summary && !summary.cliInstalled && summary.cliInstallHint && (
          <div className="cloud-account-muted indent">
            {summary.cliInstallHint}
          </div>
        )}
        {summary &&
          summary.cliInstalled &&
          !summary.pluginInstalled &&
          summary.pluginInstallHint && (
            <div className="cloud-account-muted indent">
              Clusters need {summary.pluginName}. {summary.pluginInstallHint}
            </div>
          )}
        {summary?.error && summary.state === 'expired' && (
          <div className="cloud-account-muted indent">{summary.error}</div>
        )}
        {showLogin && (
          <div className="cloud-account-login">
            <LoginProgress
              state={login.state === 'running' ? 'pending' : 'failed'}
              title={`Finish signing in with ${cli}`}
              waitingText="Waiting for the browser sign-in to complete…"
              code={login.code}
              url={login.url}
              error={login.error}
              onCancel={() => cancelProviderLogin(provider)}
              onRetry={() => signInProvider(provider)}
              onDismiss={() => cancelProviderLogin(provider)}
              compact
            />
          </div>
        )}
      </div>
    );
  };

  const newUrlLogin = newUrl ? ssoLogins[normalizeStartUrl(newUrl)] : undefined;

  return (
    <DropdownMenu.Root open={isOpen} onOpenChange={setIsOpen}>
      <DropdownMenu.Trigger asChild>
        <button
          className={`theme-toggle cloud-accounts-trigger ${signedIn ? 'signed-in' : ''} ${attention > 0 ? 'attention' : ''}`}
          title={
            attention > 0
              ? `${attention} cloud account${attention === 1 ? '' : 's'} need sign-in`
              : 'Cloud accounts'
          }
          aria-label="Cloud accounts"
        >
          <IdCardIcon width={16} height={16} />
          {attention > 0 && (
            <span className="cloud-accounts-trigger-dot" aria-hidden />
          )}
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="cloud-accounts-content ap-menu"
          sideOffset={6}
          align="end"
          onCloseAutoFocus={(e) => e.preventDefault()}
        >
          <div className="cloud-accounts-header">
            <span>Cloud accounts</span>
            <button
              className="ap-icon-btn ap-icon-btn--sm"
              onClick={() => {
                setShowAddForm((v) => !v);
                setAddError(null);
              }}
              title="Add an AWS access portal"
              aria-label="Add an AWS access portal"
            >
              {showAddForm ? <Cross2Icon /> : <PlusIcon />}
            </button>
          </div>

          <div className="cloud-accounts-body">
            {currentTab && tabAuth?.accountId && offersSsoConnect(tabAuth) && (
              <>
                <div className="ap-group-label">Current cluster</div>
                <div className="cloud-accounts-current">
                  <ClusterSSOConnect
                    cluster={currentTab}
                    accountId={tabAuth.accountId}
                    binding={tabAuth.ssoBinding}
                    onChanged={() => {
                      setTabAuthVersion((v) => v + 1);
                      window.dispatchEvent(
                        new CustomEvent('cluster:retry', { detail: { cluster: currentTab } }),
                      );
                    }}
                  />
                </div>
              </>
            )}

            <div className="ap-group-label">AWS IAM Identity Center</div>

            {showAddForm && (
              <div className="cloud-accounts-add">
                <label className="cloud-accounts-field">
                  <span>Access portal URL</span>
                  <input
                    className="ap-input ap-input--sm"
                    placeholder="https://my-org.awsapps.com/start"
                    value={newUrl}
                    onChange={(e) => setNewUrl(e.target.value)}
                    onKeyDown={(e) => {
                      e.stopPropagation();
                      if (e.key === 'Enter') handleAdd();
                      if (e.key === 'Escape') setShowAddForm(false);
                    }}
                    autoFocus
                  />
                </label>
                <label className="cloud-accounts-field">
                  <span>Region</span>
                  <AWSRegionSelect
                    value={newRegion}
                    onChange={setNewRegion}
                    className="ap-select cloud-accounts-select"
                  />
                </label>
                <div className="cloud-accounts-add-actions">
                  <button
                    className="ap-btn ap-btn--primary ap-btn--sm"
                    onClick={handleAdd}
                    disabled={adding || !newUrl.trim()}
                  >
                    {adding ? 'Waiting for approval…' : 'Sign in'}
                  </button>
                  <button
                    className="ap-btn ap-btn--sm"
                    onClick={() => setShowAddForm(false)}
                  >
                    Cancel
                  </button>
                </div>
                {addError && (
                  <div className="cloud-account-muted">{addError}</div>
                )}
                {newUrlLogin && newUrlLogin.state === 'pending' && (
                  <LoginProgress
                    state="pending"
                    code={newUrlLogin.userCode}
                    url={newUrlLogin.verificationUrlComplete}
                    onCancel={() => cancelSSOLogin(newUrl)}
                    compact
                  />
                )}
              </div>
            )}

            {ssoSessions.length === 0 ? (
              <div className="cloud-account-muted empty">
                {authLoaded
                  ? 'No AWS access portals yet. Add one with +, or run aws configure sso in a terminal — portals in ~/.aws/config show up here automatically.'
                  : 'Loading…'}
              </div>
            ) : (
              ssoSessions.map(renderSession)
            )}

            <div className="ap-group-label">Google Cloud</div>
            {renderProvider('gcp')}

            <div className="ap-group-label">Azure</div>
            {renderProvider('azure')}
          </div>

          <div className="cloud-accounts-footer">
            Kanivet shares sign-ins with the AWS CLI, gcloud and az: signing in
            here signs your terminal in too, and terminal sign-ins (or Leapp
            sessions) appear here.
          </div>
          <DropdownMenu.Arrow className="cloud-accounts-arrow" />
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
};

export default CloudAccountsMenu;
