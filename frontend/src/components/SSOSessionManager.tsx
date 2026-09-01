import { useState, useEffect, useRef, useCallback } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import {
  IdCardIcon,
  PlusIcon,
  TrashIcon,
  ReloadIcon,
  Cross2Icon,
  ClockIcon,
  ChevronRightIcon,
  PlayIcon,
  CheckCircledIcon,
} from '@radix-ui/react-icons';
import { useStore } from '../store';
import cloudService from '../services/cloudService';
import { SSOAccount } from '../types/cloud';
import AWSIcon from './AWSIcon';
import './SSOSessionManager.css';

interface ActiveAccount {
  startUrl: string;
  accountId: string;
  accountName: string;
  profileName?: string;
  roleName?: string;
  expiresAt?: number;
}

const SSOSessionManager = () => {
  const {
    ssoSessions,
    ssoSessionsLoading,
    loadSsoSessions,
    addSsoSession,
    removeSsoSession,
    refreshSsoSession,
    refreshAllClusterStatuses,
  } = useStore();

  const [isOpen, setIsOpen] = useState(false);
  const [showAddForm, setShowAddForm] = useState(false);
  const [newSsoUrl, setNewSsoUrl] = useState('');
  const [newSsoRegion, setNewSsoRegion] = useState('us-east-1');
  const [addingSession, setAddingSession] = useState(false);
  const [refreshingSession, setRefreshingSession] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expandedSessions, setExpandedSessions] = useState<Set<string>>(new Set());
  const [accountsMap, setAccountsMap] = useState<Map<string, SSOAccount[]>>(new Map());
  const [loadingAccounts, setLoadingAccounts] = useState<Set<string>>(new Set());
  const [activeAccount, setActiveAccount] = useState<ActiveAccount | null>(null);
  const [activeAccountLoaded, setActiveAccountLoaded] = useState(false);

  useEffect(() => {
    if (!activeAccountLoaded) {
      cloudService.getSSOActiveAccount().then(account => {
        if (account) {
          setActiveAccount({
            startUrl: account.startUrl,
            accountId: account.accountId,
            accountName: account.accountName,
            profileName: account.profileName,
            roleName: account.roleName,
            expiresAt: account.expiresAt,
          });
        }
        setActiveAccountLoaded(true);
      }).catch(() => setActiveAccountLoaded(true));
    }
  }, [activeAccountLoaded]);

  useEffect(() => {
    if (!ssoSessionsLoading) {
      loadSsoSessions();
    }
  }, [loadSsoSessions, ssoSessionsLoading]);

  useEffect(() => {
    const handleOpenManager = () => {
      setIsOpen(true);
      setShowAddForm(true);
    };
    window.addEventListener('sso:openManager', handleOpenManager);
    return () => window.removeEventListener('sso:openManager', handleOpenManager);
  }, []);

  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (!electronAPI?.tray?.updateSSOAccounts) return;
    const allAccounts: { startUrl: string; accountId: string; accountName: string; region: string; expiresAt: number }[] = [];
    ssoSessions.forEach(session => {
      const accounts = accountsMap.get(session.startUrl) || [];
      accounts.forEach(acc => {
        allAccounts.push({
          startUrl: session.startUrl,
          accountId: acc.accountId,
          accountName: acc.accountName,
          region: session.region,
          expiresAt: session.expiresAt,
        });
      });
    });
    electronAPI.tray.updateSSOAccounts(allAccounts, activeAccount);
  }, [ssoSessions, accountsMap, activeAccount]);

  const handleTrayActivateRef = useRef<((account: any) => void) | null>(null);

  const handleTrayActivate = useCallback(async (account: { startUrl: string; accountId: string; accountName: string; region: string; expiresAt: number }) => {
    setActivatingAccount(`${account.startUrl}:${account.accountId}`);
    setError(null);
    try {
      const result = await cloudService.activateAWSSSOAccount(account.startUrl, account.accountId, undefined, account.region);
      const newActiveAccount = {
        startUrl: account.startUrl,
        accountId: account.accountId,
        accountName: account.accountName,
        profileName: result.profileName,
        roleName: result.roleName,
        expiresAt: result.expiresAt,
      };
      setActiveAccount(newActiveAccount);
      await cloudService.setSSOActiveAccount(
        account.startUrl, account.accountId, account.accountName,
        result.profileName, result.roleName, result.expiresAt
      );
    } catch (err: any) {
      if (err.response?.data?.error?.includes('expired') || err.response?.data?.error?.includes('not found')) {
        try {
          await cloudService.startAWSSSOLogin(account.startUrl, account.region);
          const result = await cloudService.activateAWSSSOAccount(account.startUrl, account.accountId, undefined, account.region);
          const newActiveAccount = {
            startUrl: account.startUrl,
            accountId: account.accountId,
            accountName: account.accountName,
            profileName: result.profileName,
            roleName: result.roleName,
            expiresAt: result.expiresAt,
          };
          setActiveAccount(newActiveAccount);
          await cloudService.setSSOActiveAccount(
            account.startUrl, account.accountId, account.accountName,
            result.profileName, result.roleName, result.expiresAt
          );
          addSsoSession({
            startUrl: account.startUrl,
            region: account.region,
            expiresAt: Date.now() + 8 * 60 * 60 * 1000,
            label: undefined,
          });
          refreshAllClusterStatuses();
        } catch (retryErr: any) {
          setError(retryErr.response?.data?.error || retryErr.message || 'Failed to activate account');
        }
      } else {
        setError(err.response?.data?.error || err.message || 'Failed to activate account');
      }
    } finally {
      setActivatingAccount(null);
    }
  }, [addSsoSession, refreshAllClusterStatuses]);

  handleTrayActivateRef.current = handleTrayActivate;

  useEffect(() => {
    const electronAPI = (window as any).electronAPI;
    if (!electronAPI?.tray?.onActivateAccount) return;
    const handler = (account: any) => handleTrayActivateRef.current?.(account);
    const cleanup = electronAPI.tray.onActivateAccount(handler);
    return cleanup;
  }, []);

  const isSessionExpired = (session: { expiresAt: number }) => Date.now() > session.expiresAt;

  const isSessionExpiringSoon = (session: { expiresAt: number }) => {
    const thirtyMinutes = 30 * 60 * 1000;
    return !isSessionExpired(session) && session.expiresAt - Date.now() < thirtyMinutes;
  };

  const formatTimeLeft = (expiresAt: number) => {
    const now = Date.now();
    if (now > expiresAt) return 'Expired';
    const diff = expiresAt - now;
    const hours = Math.floor(diff / (1000 * 60 * 60));
    const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  };

  const loadAccounts = async (startUrl: string) => {
    if (accountsMap.has(startUrl) || loadingAccounts.has(startUrl)) return;
    setLoadingAccounts(prev => new Set([...prev, startUrl]));
    try {
      const accounts = await cloudService.getAWSSSOAccounts(startUrl);
      setAccountsMap(prev => new Map(prev).set(startUrl, accounts));
    } catch (err) {
      console.error('Failed to load accounts:', err);
    } finally {
      setLoadingAccounts(prev => {
        const next = new Set(prev);
        next.delete(startUrl);
        return next;
      });
    }
  };

  useEffect(() => {
    ssoSessions.forEach(session => {
      if (!accountsMap.has(session.startUrl) && !loadingAccounts.has(session.startUrl) && Date.now() < session.expiresAt) {
        loadAccounts(session.startUrl);
      }
    });
  }, [ssoSessions, accountsMap, loadingAccounts]);

  const toggleSessionExpanded = (startUrl: string) => {
    setExpandedSessions(prev => {
      const next = new Set(prev);
      if (next.has(startUrl)) {
        next.delete(startUrl);
      } else {
        next.add(startUrl);
        loadAccounts(startUrl);
      }
      return next;
    });
  };

  const handleAddSession = async () => {
    if (!newSsoUrl.trim()) return;
    setAddingSession(true);
    setError(null);
    try {
      const response = await cloudService.startAWSSSOLogin(newSsoUrl.trim(), newSsoRegion);
      if (response.expiresIn) {
        const expiresAt = Date.now() + response.expiresIn * 1000;
        addSsoSession({
          startUrl: newSsoUrl.trim(),
          region: newSsoRegion,
          expiresAt,
          label: new URL(newSsoUrl.trim()).hostname.split('.')[0],
        });
        setNewSsoUrl('');
        setNewSsoRegion('us-east-1');
        setShowAddForm(false);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to add SSO session');
    } finally {
      setAddingSession(false);
    }
  };

  const handleRefreshSession = async (startUrl: string, region: string) => {
    setRefreshingSession(startUrl);
    setError(null);
    try {
      await refreshSsoSession(startUrl, region);
      setAccountsMap(prev => {
        const next = new Map(prev);
        next.delete(startUrl);
        return next;
      });
      if (expandedSessions.has(startUrl)) {
        loadAccounts(startUrl);
      }
      refreshAllClusterStatuses();
    } catch (err: any) {
      setError(err.message || 'Failed to refresh session');
    } finally {
      setRefreshingSession(null);
    }
  };

  const [activatingAccount, setActivatingAccount] = useState<string | null>(null);

  const handleActivateAccount = async (session: { startUrl: string; region: string; expiresAt: number }, account: SSOAccount) => {
    const isCurrentlyActive = activeAccount?.startUrl === session.startUrl && activeAccount?.accountId === account.accountId;
    if (isCurrentlyActive) {
      handleDeactivate();
      return;
    }

    const accountKey = `${session.startUrl}:${account.accountId}`;
    setActivatingAccount(accountKey);
    setError(null);

    try {
      const result = await cloudService.activateAWSSSOAccount(session.startUrl, account.accountId, undefined, session.region);
      const newActiveAccount = {
        startUrl: session.startUrl,
        accountId: account.accountId,
        accountName: account.accountName,
        profileName: result.profileName,
        roleName: result.roleName,
        expiresAt: result.expiresAt,
      };
      setActiveAccount(newActiveAccount);
      await cloudService.setSSOActiveAccount(
        session.startUrl, account.accountId, account.accountName,
        result.profileName, result.roleName, result.expiresAt
      );
    } catch (err: any) {
      if (err.response?.data?.error?.includes('expired') || err.response?.data?.error?.includes('not found')) {
        try {
          await cloudService.startAWSSSOLogin(session.startUrl, session.region);
          const result = await cloudService.activateAWSSSOAccount(session.startUrl, account.accountId, undefined, session.region);
          const newActiveAccount = {
            startUrl: session.startUrl,
            accountId: account.accountId,
            accountName: account.accountName,
            profileName: result.profileName,
            roleName: result.roleName,
            expiresAt: result.expiresAt,
          };
          setActiveAccount(newActiveAccount);
          await cloudService.setSSOActiveAccount(
            session.startUrl, account.accountId, account.accountName,
            result.profileName, result.roleName, result.expiresAt
          );
          addSsoSession({
            startUrl: session.startUrl,
            region: session.region,
            expiresAt: Date.now() + 8 * 60 * 60 * 1000,
            label: ssoSessions.find(s => s.startUrl === session.startUrl)?.label,
          });
          refreshAllClusterStatuses();
        } catch (retryErr: any) {
          setError(retryErr.response?.data?.error || retryErr.message || 'Failed to activate account');
        }
      } else {
        setError(err.response?.data?.error || err.message || 'Failed to activate account');
      }
    } finally {
      setActivatingAccount(null);
    }
  };

  const handleDeactivate = async () => {
    try {
      await cloudService.deactivateAWSSSOAccount();
      await cloudService.clearSSOActiveAccount();
      setActiveAccount(null);
    } catch (err: any) {
      setError(err.message || 'Failed to deactivate');
    }
  };

  const hasValidSessions = ssoSessions.some(s => !isSessionExpired(s));

  return (
    <DropdownMenu.Root open={isOpen} onOpenChange={setIsOpen}>
      <DropdownMenu.Trigger asChild>
        <button
          className={`theme-toggle sso-manager-trigger ${hasValidSessions ? 'has-sessions' : ''} ${activeAccount ? 'active' : ''}`}
          title={activeAccount ? `Active: ${activeAccount.accountName}` : 'AWS SSO Accounts'}
          aria-label="AWS SSO Accounts"
        >
          <IdCardIcon width={16} height={16} />
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content className="sso-manager-content" sideOffset={5} align="end">
          <div className="sso-manager-header">
            <AWSIcon />
            <span>SSO Accounts</span>
            <button
              className="sso-manager-add-btn"
              onClick={(e) => { e.preventDefault(); setShowAddForm(!showAddForm); }}
              title="Add SSO Session"
            >
              <PlusIcon />
            </button>
          </div>

          {activeAccount && (
            <div className="sso-manager-active-account">
              <CheckCircledIcon />
              <div className="sso-active-info">
                <span className="sso-active-name">{activeAccount.accountName}</span>
                <span className="sso-active-profile">Using default profile</span>
              </div>
              <button onClick={handleDeactivate} title="Deactivate (restores original default profile)">
                <Cross2Icon />
              </button>
            </div>
          )}

          {showAddForm && (
            <div className="sso-manager-add-form">
              <input
                type="text"
                placeholder="SSO Start URL"
                value={newSsoUrl}
                onChange={(e) => setNewSsoUrl(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleAddSession();
                  if (e.key === 'Escape') setShowAddForm(false);
                }}
                autoFocus
              />
              <select
                value={newSsoRegion}
                onChange={(e) => setNewSsoRegion(e.target.value)}
              >
                <option value="us-east-1">us-east-1</option>
                <option value="us-east-2">us-east-2</option>
                <option value="us-west-1">us-west-1</option>
                <option value="us-west-2">us-west-2</option>
                <option value="eu-west-1">eu-west-1</option>
                <option value="eu-west-2">eu-west-2</option>
                <option value="eu-central-1">eu-central-1</option>
                <option value="ap-northeast-1">ap-northeast-1</option>
                <option value="ap-southeast-1">ap-southeast-1</option>
                <option value="ap-southeast-2">ap-southeast-2</option>
              </select>
              <div className="sso-manager-add-actions">
                <button
                  onClick={handleAddSession}
                  disabled={addingSession || !newSsoUrl.trim()}
                  className="sso-manager-btn primary"
                >
                  {addingSession ? 'Connecting...' : 'Connect'}
                </button>
                <button onClick={() => setShowAddForm(false)} className="sso-manager-btn">
                  Cancel
                </button>
              </div>
            </div>
          )}

          {error && (
            <div className="sso-manager-error">
              {error}
              <button onClick={() => setError(null)}><Cross2Icon /></button>
            </div>
          )}

          <div className="sso-manager-sessions">
            {ssoSessionsLoading ? (
              <div className="sso-manager-loading">Loading sessions...</div>
            ) : ssoSessions.length === 0 ? (
              <div className="sso-manager-empty">
                No SSO sessions configured.
                <br />
                Click + to add one.
              </div>
            ) : (
              ssoSessions.map((session) => {
                const expired = isSessionExpired(session);
                const expiringSoon = isSessionExpiringSoon(session);
                const isExpanded = expandedSessions.has(session.startUrl);
                const accounts = accountsMap.get(session.startUrl) || [];
                const isLoadingAccounts = loadingAccounts.has(session.startUrl);
                const isRefreshing = refreshingSession === session.startUrl;

                return (
                  <div key={session.startUrl} className={`sso-manager-session-wrapper ${expired ? 'expired' : ''}`}>
                    <div className={`sso-manager-session ${expiringSoon ? 'expiring' : ''} ${isExpanded ? 'expanded' : ''}`}>
                      <button
                        className="sso-session-expand"
                        onClick={() => !expired && toggleSessionExpanded(session.startUrl)}
                        disabled={expired}
                      >
                        <ChevronRightIcon style={{ transform: isExpanded ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s' }} />
                      </button>
                      <div className="sso-session-info" onClick={() => !expired && toggleSessionExpanded(session.startUrl)}>
                        <span className="sso-session-label">{session.label || 'SSO'}</span>
                        <span className={`sso-session-time ${expired ? 'expired' : ''} ${expiringSoon ? 'expiring' : ''}`}>
                          <ClockIcon />
                          {formatTimeLeft(session.expiresAt)}
                        </span>
                      </div>
                      <div className="sso-session-actions">
                        <button
                          onClick={(e) => { e.stopPropagation(); handleRefreshSession(session.startUrl, session.region); }}
                          disabled={isRefreshing}
                          title="Refresh session"
                        >
                          <ReloadIcon className={isRefreshing ? 'spinning' : ''} />
                        </button>
                        <button
                          onClick={(e) => { e.stopPropagation(); removeSsoSession(session.startUrl); }}
                          className="danger"
                          title="Remove session"
                        >
                          <TrashIcon />
                        </button>
                      </div>
                    </div>
                    {isExpanded && !expired && (
                      <div className="sso-manager-accounts">
                        {isLoadingAccounts ? (
                          <div className="sso-accounts-loading">
                            <div className="sso-spinner"></div>
                            <span>Loading accounts...</span>
                          </div>
                        ) : accounts.length === 0 ? (
                          <div className="sso-accounts-empty">No accounts found</div>
                        ) : (
                          accounts.sort((a, b) => a.accountName.localeCompare(b.accountName)).map(account => {
                            const isActive = activeAccount?.startUrl === session.startUrl && activeAccount?.accountId === account.accountId;
                            const accountKey = `${session.startUrl}:${account.accountId}`;
                            const isActivating = activatingAccount === accountKey;
                            return (
                              <div
                                key={account.accountId}
                                className={`sso-account-item ${isActive ? 'active' : ''}`}
                              >
                                <div className="sso-account-info">
                                  <span className="sso-account-name">{account.accountName}</span>
                                  <span className="sso-account-id">{account.accountId}</span>
                                </div>
                                <button
                                  className={`sso-account-activate ${isActive ? 'active' : ''} ${isActivating ? 'loading' : ''}`}
                                  onClick={() => handleActivateAccount(session, account)}
                                  disabled={isActivating}
                                  title={isActive ? 'Deactivate' : expired ? 'Authenticate & activate' : 'Activate for discovery'}
                                >
                                  {isActivating ? <ReloadIcon className="spinning" /> : isActive ? <CheckCircledIcon /> : <PlayIcon />}
                                </button>
                              </div>
                            );
                          })
                        )}
                      </div>
                    )}
                  </div>
                );
              })
            )}
          </div>

          <DropdownMenu.Arrow className="sso-manager-arrow" />
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
};

export default SSOSessionManager;
