import { useEffect, useRef } from 'react';
import { useStore } from '../store';

const SSO_CHECK_INTERVAL = 60 * 1000;
const REFRESH_THRESHOLD = 5 * 60 * 1000;

export function useSSOAutoRefresh() {
  const { ssoSessions, loadSsoSessions, refreshSsoSession, ssoSessionsLoaded } = useStore();
  const refreshingRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!ssoSessionsLoaded) {
      loadSsoSessions();
    }
  }, [ssoSessionsLoaded, loadSsoSessions]);

  useEffect(() => {
    if (!ssoSessionsLoaded || ssoSessions.length === 0) return;

    const checkAndRefreshSessions = async () => {
      const now = Date.now();
      for (const session of ssoSessions) {
        const timeUntilExpiry = session.expiresAt - now;
        const isExpiredOrExpiringSoon = timeUntilExpiry <= REFRESH_THRESHOLD;
        if (isExpiredOrExpiringSoon && !refreshingRef.current.has(session.startUrl)) {
          refreshingRef.current.add(session.startUrl);
          console.log(`[SSO Auto-Refresh] Session expired or expiring soon: ${session.label || session.startUrl}`);
          try {
            await refreshSsoSession(session.startUrl, session.region);
            console.log(`[SSO Auto-Refresh] Session refreshed: ${session.label || session.startUrl}`);
          } catch (err) {
            console.error(`[SSO Auto-Refresh] Failed to start login for ${session.startUrl}:`, err);
          } finally {
            setTimeout(() => refreshingRef.current.delete(session.startUrl), 5 * 60 * 1000);
          }
        }
      }
    };

    checkAndRefreshSessions();
    const interval = setInterval(checkAndRefreshSessions, SSO_CHECK_INTERVAL);
    return () => clearInterval(interval);
  }, [ssoSessions, ssoSessionsLoaded]);
}
