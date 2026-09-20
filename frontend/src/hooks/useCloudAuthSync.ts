import { useEffect, useRef } from 'react';
import { useStore } from '../store';
import { islandNotify } from '../services/islandNotifications';
import type { SSOSessionStatus } from '../types/cloud';

/** Fallback poll when no WebSocket push arrives; the backend pushes on every change. */
const FALLBACK_REFRESH_MS = 5 * 60 * 1000;

const needsSignIn = (state: SSOSessionStatus['state']) =>
  state === 'expired' || state === 'signed_out';

/**
 * Keeps the cloud sign-in picture current without ever prompting on its own:
 * loads /cloud/auth once, re-reads it when the backend announces a change
 * (sign-in, silent refresh, terminal `aws sso login`, sign-out), and raises a
 * single quiet notice when a session that was usable stops being usable.
 * Interactive sign-in only ever happens from a button the user clicks.
 */
export function useCloudAuthSync() {
  const loadAuthSummary = useStore((s) => s.loadAuthSummary);
  const authLoaded = useStore((s) => s.authLoaded);
  const ssoSessions = useStore((s) => s.ssoSessions);
  const previousRef = useRef<Map<string, SSOSessionStatus['state']> | null>(
    null,
  );

  useEffect(() => {
    if (!authLoaded) {
      loadAuthSummary();
    }
  }, [authLoaded, loadAuthSummary]);

  useEffect(() => {
    const onChanged = () => {
      loadAuthSummary(true);
    };
    window.addEventListener('cloud:auth-changed', onChanged);
    const interval = setInterval(
      () => loadAuthSummary(true),
      FALLBACK_REFRESH_MS,
    );
    return () => {
      window.removeEventListener('cloud:auth-changed', onChanged);
      clearInterval(interval);
    };
  }, [loadAuthSummary]);

  useEffect(() => {
    if (!authLoaded) return;
    const current = new Map<string, SSOSessionStatus['state']>();
    ssoSessions.forEach((s) => current.set(s.startUrl, s.state));
    const previous = previousRef.current;
    previousRef.current = current;
    if (!previous) return;
    ssoSessions.forEach((s) => {
      const before = previous.get(s.startUrl);
      if (before && !needsSignIn(before) && needsSignIn(s.state)) {
        islandNotify(
          `sso-expired:${s.startUrl}`,
          {
            type: 'warning',
            message: `AWS SSO session “${s.label || s.startUrl}” needs a sign-in`,
            icon: 'key',
            duration: 5000,
          },
          { cooldownMs: 30 * 60 * 1000 },
        );
      }
    });
  }, [ssoSessions, authLoaded]);
}
