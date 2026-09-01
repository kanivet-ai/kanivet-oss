import { StateCreator } from 'zustand';
import cloudService from '../services/cloudService';
import { SSOSession, SSOSlice } from './types';

const normalizeStartUrl = (url: string): string => {
  try {
    const parsed = new URL(url);
    parsed.hash = '';
    return parsed.href.replace(/\/+$/, '');
  } catch {
    return url.replace(/#.*$/, '').replace(/\/+$/, '');
  }
};

export const createSsoSlice: StateCreator<SSOSlice, [], [], SSOSlice> = (set, get) => ({
  ssoSessions: [],
  ssoSessionsLoading: false,
  ssoSessionsLoaded: false,
  activeSsoSession: null,

  setSsoSessions: (sessions) => {
    set({ ssoSessions: sessions });
  },

  addSsoSession: async (session) => {
    const sessions = get().ssoSessions;
    const exists = sessions.some(s => normalizeStartUrl(s.startUrl) === normalizeStartUrl(session.startUrl));
    if (!exists) {
      const updated = [...sessions, session];
      set({ ssoSessions: updated });
      try {
        await cloudService.saveSSOSession(session.startUrl, session.region, session.label || '', session.expiresAt);
      } catch (err) {
        console.error('Failed to save SSO session to backend:', err);
      }
    }
  },

  removeSsoSession: async (startUrl) => {
    const normalized = normalizeStartUrl(startUrl);
    const updated = get().ssoSessions.filter(s => normalizeStartUrl(s.startUrl) !== normalized);
    const activeSession = get().activeSsoSession;
    const newActive = activeSession && normalizeStartUrl(activeSession) === normalized ? null : activeSession;
    set({ ssoSessions: updated, activeSsoSession: newActive });
    try {
      await cloudService.deleteSSOSession(startUrl);
    } catch (err) {
      console.error('Failed to delete SSO session from backend:', err);
    }
  },

  updateSsoSessionLabel: async (startUrl, label) => {
    const normalized = normalizeStartUrl(startUrl);
    const updated = get().ssoSessions.map(s =>
      normalizeStartUrl(s.startUrl) === normalized ? { ...s, label } : s
    );
    set({ ssoSessions: updated });
    try {
      await cloudService.updateSSOSessionLabel(startUrl, label);
    } catch (err) {
      console.error('Failed to update SSO session label:', err);
    }
  },

  setActiveSsoSession: (startUrl) => {
    set({ activeSsoSession: startUrl });
  },

  loadSsoSessions: async () => {
    if (get().ssoSessionsLoaded) return;
    set({ ssoSessionsLoading: true });
    const start = performance.now();
    try {
      const backendSessions = await cloudService.getAWSSSOSessions();
      console.log(`[SSO] getAWSSSOSessions took ${(performance.now() - start).toFixed(0)}ms`);
      const sessions: SSOSession[] = backendSessions.map(s => ({
        startUrl: s.startUrl,
        region: s.region,
        expiresAt: s.expiresAt,
        label: s.label || new URL(s.startUrl).hostname.split('.')[0],
      }));
      const activeAccount = await cloudService.getSSOActiveAccount();
      console.log(`[SSO] Total load took ${(performance.now() - start).toFixed(0)}ms`);
      set({
        ssoSessions: sessions,
        activeSsoSession: activeAccount?.startUrl || null,
        ssoSessionsLoaded: true,
      });
    } catch (err) {
      console.error('Failed to load SSO sessions:', err);
      set({ ssoSessionsLoaded: true });
    } finally {
      set({ ssoSessionsLoading: false });
    }
  },

  refreshSsoSession: async (startUrl: string, region: string) => {
    try {
      const response = await cloudService.startAWSSSOLogin(startUrl, region);
      if (response.expiresIn) {
        const expiresAt = Date.now() + response.expiresIn * 1000;
        const sessions = get().ssoSessions;
        const normalized = normalizeStartUrl(startUrl);
        const updated = sessions.map(s =>
          normalizeStartUrl(s.startUrl) === normalized
            ? { ...s, expiresAt }
            : s
        );
        set({ ssoSessions: updated });
        await cloudService.saveSSOSession(startUrl, region, '', expiresAt);
        console.log('[SSO] Session refreshed, triggering connection refresh');
        window.dispatchEvent(new CustomEvent('sso:refreshed', { detail: { startUrl, expiresAt } }));
      }
    } catch (err) {
      console.error('Failed to refresh SSO session:', err);
      throw err;
    }
  },
});
