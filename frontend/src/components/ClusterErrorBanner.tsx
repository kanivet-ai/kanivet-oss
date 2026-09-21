import { useEffect, useRef, useState } from 'react';
import { ExclamationTriangleIcon, LockClosedIcon, ReloadIcon } from '@radix-ui/react-icons';
import { useStore, ClusterError } from '../store';
import { useShallow } from 'zustand/react/shallow';
import cloudService from '../services/cloudService';
import { ClusterAuthInfo } from '../types/cloud';
import { normalizeStartUrl } from '../store/cloudAuthSlice';
import { isAuthErrorCode } from '../utils/clusterAuthErrors';
import { offersSsoConnect } from '../utils/clusterSsoConnect';
import LoginProgress from './cloud/LoginProgress';
import ClusterSSOConnect from './cloud/ClusterSSOConnect';
import './ClusterErrorBanner.css';

const getErrorTitle = (errorCode: string) => {
  switch (errorCode) {
    case 'aws_sso_expired': return 'AWS SSO Sign-in Required';
    case 'aws_token_expired': return 'AWS Credentials Expired';
    case 'aws_credentials_missing': return 'AWS Credentials Unavailable';
    case 'azure_auth_expired': return 'Azure Sign-in Required';
    case 'azure_kubelogin': return 'Azure Authentication Failed';
    case 'gcp_auth_expired': return 'Google Cloud Sign-in Required';
    case 'gcp_plugin_missing': return 'GKE Auth Plugin Missing';
    case 'exec_missing': return 'Credential Helper Missing';
    case 'sso_expired': return 'SSO Session Expired';
    case 'token_expired': return 'Token Expired';
    case 'unauthorized': return 'Authentication Failed';
    case 'forbidden': return 'Access Denied';
    case 'exec_failed': return 'Credential Error';
    case 'connection_failed': return 'Connection Failed';
    case 'timeout': return 'Connection Timeout';
    case 'cert_error': return 'Certificate Error';
    default: return 'Cluster Error';
  }
};

/** The terminal equivalent of the button we offer, so terminal-first users can use that instead. */
const suggestedCommand = (auth: ClusterAuthInfo | null): string | null => {
  if (!auth) return null;
  switch (auth.signIn) {
    case 'aws-sso':
      if (auth.ssoSessionName) return `aws sso login --sso-session ${auth.ssoSessionName}`;
      if (auth.profile) return `aws sso login --profile ${auth.profile}`;
      return 'aws sso login';
    case 'gcp':
      return 'gcloud auth login';
    case 'azure':
      return auth.method === 'azure-kubelogin' && auth.hint ? 'az login && kubelogin convert-kubeconfig -l azurecli' : 'az login';
    default:
      return null;
  }
};

const ClusterErrorBanner = () => {
  const { currentTab, clusterErrors, clearClusterError } = useStore(useShallow((s) => ({ currentTab: s.currentTab, clusterErrors: s.clusterErrors, clearClusterError: s.clearClusterError })));
  const { signInSSO, signInProvider, cancelSSOLogin, cancelProviderLogin, ssoLogins, providerLogins, authSummary } = useStore(useShallow((s) => ({
    signInSSO: s.signInSSO,
    signInProvider: s.signInProvider,
    cancelSSOLogin: s.cancelSSOLogin,
    cancelProviderLogin: s.cancelProviderLogin,
    ssoLogins: s.ssoLogins,
    providerLogins: s.providerLogins,
    authSummary: s.authSummary,
  })));
  const vclusterStatuses = useStore((s) => s.vclusterStatuses);
  const [isRetrying, setIsRetrying] = useState(false);
  const [auth, setAuth] = useState<ClusterAuthInfo | null>(null);
  const retryStartRef = useRef<number>(0);
  // Bumped when the identity used for this cluster changes, to re-read it.
  const [authVersion, setAuthVersion] = useState(0);
  const refreshAuth = () => setAuthVersion((v) => v + 1);

  const error: ClusterError | undefined = currentTab ? clusterErrors[currentTab] : undefined;
  const errorCode = error?.errorCode;

  useEffect(() => {
    setIsRetrying(false);
  }, [currentTab]);

  useEffect(() => {
    const handleRetryDone = (event: CustomEvent<{ cluster: string; healthy: boolean }>) => {
      if (event.detail.cluster !== currentTab) return;
      setIsRetrying(false);
      if (event.detail.healthy) clearClusterError(event.detail.cluster);
    };
    window.addEventListener('cluster:retry-done', handleRetryDone as EventListener);
    return () => window.removeEventListener('cluster:retry-done', handleRetryDone as EventListener);
  }, [currentTab, clearClusterError]);

  // Ask the backend how this context authenticates so we can offer the exact
  // sign-in that fixes it instead of a generic command.
  useEffect(() => {
    setAuth(null);
    if (!currentTab || !errorCode) return;
    let cancelled = false;
    cloudService
      .describeClusterAuth(currentTab)
      .then((info) => {
        if (!cancelled) setAuth(info);
      })
      .catch(() => {
        if (!cancelled) setAuth(null);
      });
    return () => {
      cancelled = true;
    };
  }, [currentTab, errorCode, authVersion]);

  if (!currentTab) return null;
  const vcStatus = vclusterStatuses?.[currentTab];
  const transient = vcStatus && (vcStatus.state === 'connecting' || vcStatus.state === 'reconnecting');
  if (transient) {
    return (
      <div className="cluster-error-pane">
        <div className="cluster-error-pane-card vcluster-transient">
          <div className="cluster-error-pane-header">
            <div className="cluster-error-pane-icon">
              <ReloadIcon width={20} height={20} className="cluster-error-spinning" />
            </div>
            <div className="cluster-error-pane-titles">
              <div className="cluster-error-pane-title">
                {vcStatus.state === 'connecting' ? 'Connecting to virtual cluster…' : 'Reconnecting to virtual cluster…'}
              </div>
              <div className="cluster-error-pane-cluster">{currentTab}</div>
            </div>
          </div>
          {vcStatus.detail && <div className="cluster-error-pane-message">{vcStatus.detail}</div>}
        </div>
      </div>
    );
  }
  if (!error) return null;

  const cluster = currentTab;
  const handleRetry = () => {
    if (isRetrying) return;
    setIsRetrying(true);
    retryStartRef.current = Date.now();
    window.dispatchEvent(new CustomEvent('cluster:retry', { detail: { cluster } }));
  };

  const authRelated = isAuthErrorCode(errorCode);
  const ssoStartUrl = auth?.signIn === 'aws-sso' ? auth.ssoStartUrl : undefined;
  const ssoLogin = ssoStartUrl ? ssoLogins[normalizeStartUrl(ssoStartUrl)] : undefined;
  const ssoPending = ssoLogin?.state === 'pending';
  const providerSignIn = auth?.signIn === 'gcp' || auth?.signIn === 'azure' ? auth.signIn : null;
  const providerLogin = providerSignIn ? providerLogins[providerSignIn] : undefined;
  const providerPending = providerLogin?.state === 'running';
  const providerSummary = providerSignIn ? (providerSignIn === 'gcp' ? authSummary?.gcp : authSummary?.azure) : undefined;
  const providerCliName = providerSignIn === 'gcp' ? 'gcloud' : 'Azure CLI';
  const canSignInProvider = Boolean(providerSignIn && (providerSummary ? providerSummary.cliInstalled : true));
  const hasSignIn = Boolean(ssoStartUrl) || canSignInProvider;
  const command = suggestedCommand(auth);

  const handleSignInSSO = async () => {
    if (!ssoStartUrl) return;
    const ok = await signInSSO(ssoStartUrl, auth?.ssoRegion);
    if (ok) handleRetry();
  };

  const handleSignInProvider = async () => {
    if (!providerSignIn) return;
    const ok = await signInProvider(providerSignIn);
    if (ok) handleRetry();
  };

  const showSsoProgress = ssoLogin && ssoLogin.state !== 'authorized' && ssoLogin.state !== 'cancelled';
  const showProviderProgress = providerLogin && providerLogin.state !== 'succeeded' && providerLogin.state !== 'cancelled';

  return (
    <div className="cluster-error-pane">
      <div className="cluster-error-pane-card">
        <div className="cluster-error-pane-header">
          <div className="cluster-error-pane-icon">
            {authRelated ? <LockClosedIcon width={26} height={26} /> : <ExclamationTriangleIcon width={28} height={28} />}
          </div>
          <div className="cluster-error-pane-titles">
            <div className="cluster-error-pane-title">{getErrorTitle(error.errorCode)}</div>
            <div className="cluster-error-pane-cluster">{currentTab}</div>
          </div>
        </div>

        <div className="cluster-error-pane-message">{error.errorMessage}</div>

        {auth && ssoStartUrl && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">How this cluster signs in</div>
            <div className="cluster-error-pane-auth">
              {auth.profile ? (
                <>
                  Profile <code>{auth.profile}</code> uses IAM Identity Center at <code>{ssoStartUrl}</code>
                  {auth.ssoState === 'active' ? '. The session is currently valid, so retrying should reconnect.' : '. Sign in once and every cluster on this portal reconnects.'}
                </>
              ) : (
                <>Signs in through IAM Identity Center at <code>{ssoStartUrl}</code>.</>
              )}
            </div>
            {showSsoProgress && (
              <LoginProgress
                state={ssoLogin.state === 'pending' ? 'pending' : ssoLogin.state === 'expired' ? 'expired' : 'failed'}
                code={ssoLogin.userCode}
                url={ssoLogin.verificationUrlComplete}
                error={ssoLogin.error}
                onCancel={() => cancelSSOLogin(ssoStartUrl)}
                onRetry={handleSignInSSO}
                onDismiss={() => cancelSSOLogin(ssoStartUrl)}
                compact
              />
            )}
          </div>
        )}

        {auth && providerSignIn && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">How this cluster signs in</div>
            <div className="cluster-error-pane-auth">
              {providerSignIn === 'gcp'
                ? <>Credentials come from the <code>gcloud</code> account{providerSummary?.identity ? <> <code>{providerSummary.identity}</code></> : null}. Signing in with gcloud renews them for kubectl and Kanivet alike.</>
                : <>Credentials come from your Azure CLI sign-in{providerSummary?.identity ? <> as <code>{providerSummary.identity}</code></> : null}. Signing in with <code>az login</code> renews them for kubectl and Kanivet alike.</>}
            </div>
            {providerSummary && !providerSummary.cliInstalled && (
              <div className="cluster-error-pane-auth cluster-error-pane-auth--warn">{providerSummary.cliInstallHint}</div>
            )}
            {showProviderProgress && (
              <LoginProgress
                state={providerLogin.state === 'running' ? 'pending' : 'failed'}
                title={`Finish signing in with ${providerCliName}`}
                waitingText="Waiting for the browser sign-in to complete…"
                code={providerLogin.code}
                url={providerLogin.url}
                error={providerLogin.error}
                onCancel={() => cancelProviderLogin(providerSignIn)}
                onRetry={handleSignInProvider}
                onDismiss={() => cancelProviderLogin(providerSignIn)}
                compact
              />
            )}
          </div>
        )}

        {auth && auth.externalTool && auth.hint && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Credentials come from another tool</div>
            <div className="cluster-error-pane-auth">{auth.hint}</div>
          </div>
        )}

        {auth && auth.accountId && offersSsoConnect(auth) && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">
              {auth.ssoBinding ? 'AWS role used by Kanivet' : 'Use AWS SSO instead'}
            </div>
            <ClusterSSOConnect
              cluster={cluster}
              accountId={auth.accountId}
              binding={auth.ssoBinding}
              onChanged={() => {
                refreshAuth();
                handleRetry();
              }}
            />
          </div>
        )}

        {auth && auth.command && !auth.commandInstalled && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Missing tool</div>
            <div className="cluster-error-pane-auth cluster-error-pane-auth--warn">
              <code>{auth.command}</code> is not installed or not on PATH.{auth.installHint ? ` ${auth.installHint}` : ''}
            </div>
          </div>
        )}

        {auth && auth.hint && !auth.externalTool && auth.commandInstalled && auth.signIn !== 'aws-sso' && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Note</div>
            <div className="cluster-error-pane-auth">{auth.hint}</div>
          </div>
        )}

        {command && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Prefer the terminal?</div>
            <code className="cluster-error-pane-command">{command}</code>
            <div className="cluster-error-pane-hint">Kanivet notices terminal sign-ins automatically and reconnects.</div>
          </div>
        )}

        {error.details && error.details !== error.errorMessage && (
          <div className="cluster-error-pane-section">
            <div className="cluster-error-pane-section-label">Details</div>
            <pre className="cluster-error-pane-details">{error.details}</pre>
          </div>
        )}

        <div className="cluster-error-pane-actions">
          {ssoStartUrl && (
            <button
              className="cluster-error-pane-btn cluster-error-pane-btn-primary"
              onClick={handleSignInSSO}
              disabled={ssoPending}
            >
              <LockClosedIcon width={14} height={14} />
              {ssoPending ? 'Waiting for approval…' : 'Sign in to AWS SSO'}
            </button>
          )}
          {canSignInProvider && (
            <button
              className="cluster-error-pane-btn cluster-error-pane-btn-primary"
              onClick={handleSignInProvider}
              disabled={providerPending}
            >
              <LockClosedIcon width={14} height={14} />
              {providerPending ? 'Waiting for sign-in…' : `Sign in with ${providerCliName}`}
            </button>
          )}
          {error.recoverable && (
            <button
              className={`cluster-error-pane-btn ${hasSignIn ? '' : 'cluster-error-pane-btn-primary'}`}
              onClick={handleRetry}
              disabled={isRetrying}
            >
              <ReloadIcon width={14} height={14} className={isRetrying ? 'cluster-error-spinning' : ''} />
              {isRetrying ? 'Retrying…' : 'Retry Connection'}
            </button>
          )}
        </div>
      </div>
    </div>
  );
};

export default ClusterErrorBanner;
