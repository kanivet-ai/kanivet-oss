import { StateCreator } from 'zustand';
import cloudService, { SSOLoginRequiredError } from '../services/cloudService';
import { CloudAuthSlice, StoreState } from './types';
import {
  CloudAuthSummary,
  CloudLoginJob,
  SSOLoginSession,
  SSOSessionStatus,
} from '../types/cloud';

export const normalizeStartUrl = (url: string): string => {
  const trimmed = url.trim();
  try {
    const parsed = new URL(
      trimmed.startsWith('http') ? trimmed : `https://${trimmed}`,
    );
    parsed.hash = '';
    parsed.hostname = parsed.hostname.toLowerCase();
    return parsed.href.replace(/\/+$/, '');
  } catch {
    return trimmed.replace(/#.*$/, '').replace(/\/+$/, '');
  }
};

/** Fires the app-wide "credentials changed, refresh clusters" signal. */
const announceAuthChanged = (provider: string, startUrl?: string) => {
  window.dispatchEvent(
    new CustomEvent('cloud:auth-changed', {
      detail: { provider, startUrl, local: true },
    }),
  );
  if (provider === 'aws') {
    window.dispatchEvent(
      new CustomEvent('sso:refreshed', { detail: { startUrl } }),
    );
  }
};

const summaryToSessions = (
  summary: CloudAuthSummary | null,
): SSOSessionStatus[] => summary?.aws?.sessions ?? [];

export const createCloudAuthSlice: StateCreator<
  StoreState,
  [],
  [],
  CloudAuthSlice
> = (set, get) => {
  let inflightLoad: Promise<void> | null = null;
  const ssoWaiters = new Map<string, Promise<boolean>>();
  const providerWaiters = new Map<string, Promise<boolean>>();

  const setLogin = (startUrl: string, login: SSOLoginSession) =>
    set((state) => ({
      ssoLogins: { ...state.ssoLogins, [normalizeStartUrl(startUrl)]: login },
    }));

  const setProviderLogin = (
    provider: 'gcp' | 'azure',
    job: CloudLoginJob | undefined,
  ) =>
    set((state) => {
      const next = { ...state.providerLogins };
      if (job) next[provider] = job;
      else delete next[provider];
      return { providerLogins: next };
    });

  return {
    authSummary: null,
    authLoading: false,
    authLoaded: false,
    ssoSessions: [],
    ssoLogins: {},
    providerLogins: {},
    profileLogins: {},

    loadAuthSummary: async (force = false) => {
      if (inflightLoad && !force) return inflightLoad;
      set({ authLoading: true });
      inflightLoad = (async () => {
        try {
          const summary = await cloudService.getAuthSummary(force);
          set({
            authSummary: summary,
            ssoSessions: summaryToSessions(summary),
            authLoaded: true,
          });
        } catch (err) {
          console.error('[CloudAuth] failed to load auth summary:', err);
          set({ authLoaded: true });
        } finally {
          set({ authLoading: false });
          inflightLoad = null;
        }
      })();
      return inflightLoad;
    },

    signInSSO: async (startUrl, region) => {
      const key = normalizeStartUrl(startUrl);
      const existing = ssoWaiters.get(key);
      if (existing) return existing;
      const session = get().ssoSessions.find(
        (s) => normalizeStartUrl(s.startUrl) === key,
      );
      const run = (async () => {
        try {
          const login = await cloudService.beginAWSSSOLogin(
            startUrl,
            region || session?.region,
          );
          setLogin(startUrl, login);
          const final = await cloudService.waitForSSOLogin(login, (update) =>
            setLogin(startUrl, update),
          );
          setLogin(startUrl, final);
          await get().loadAuthSummary(true);
          if (final.state === 'authorized') {
            announceAuthChanged('aws', key);
            return true;
          }
          return false;
        } catch (err: any) {
          const message =
            err?.response?.data?.error || err?.message || 'Sign-in failed';
          setLogin(startUrl, {
            id: '',
            startUrl,
            region: region || session?.region || '',
            userCode: '',
            verificationUrl: '',
            verificationUrlComplete: '',
            expiresAt: 0,
            state: 'failed',
            error: message,
            startedAt: Date.now(),
            browserOpened: false,
          });
          return false;
        } finally {
          ssoWaiters.delete(key);
        }
      })();
      ssoWaiters.set(key, run);
      return run;
    },

    cancelSSOLogin: async (startUrl) => {
      const key = normalizeStartUrl(startUrl);
      const login = get().ssoLogins[key];
      if (login?.id && login.state === 'pending') {
        try {
          await cloudService.cancelSSOLogin(login.id);
        } catch (err) {
          console.error('[CloudAuth] cancel login failed:', err);
        }
      }
      set((state) => {
        const next = { ...state.ssoLogins };
        delete next[key];
        return { ssoLogins: next };
      });
    },

    refreshSSO: async (startUrl) => {
      try {
        await cloudService.refreshAWSSSOSession(startUrl);
        await get().loadAuthSummary(true);
        announceAuthChanged('aws', normalizeStartUrl(startUrl));
        return true;
      } catch (err) {
        if (!(err instanceof SSOLoginRequiredError)) {
          console.error('[CloudAuth] refresh failed:', err);
        }
        await get().loadAuthSummary(true);
        return false;
      }
    },

    signOutSSO: async (startUrl) => {
      try {
        await cloudService.signOutAWSSSO(startUrl);
      } catch (err) {
        console.error('[CloudAuth] sign-out failed:', err);
      }
      await get().cancelSSOLogin(startUrl);
      await get().loadAuthSummary(true);
      announceAuthChanged('aws', normalizeStartUrl(startUrl));
    },

    addSsoSession: async (startUrl, region, label) => {
      const normalized = normalizeStartUrl(startUrl);
      try {
        await cloudService.saveSSOSession(normalized, region, label || '');
      } catch (err) {
        console.error('[CloudAuth] failed to save SSO session:', err);
      }
      await get().loadAuthSummary(true);
      return get().signInSSO(normalized, region);
    },

    removeSsoSession: async (startUrl) => {
      await get().cancelSSOLogin(startUrl);
      try {
        await cloudService.deleteSSOSession(startUrl);
      } catch (err) {
        console.error('[CloudAuth] failed to delete SSO session:', err);
      }
      await get().loadAuthSummary(true);
    },

    updateSsoSessionLabel: async (startUrl, label) => {
      const normalized = normalizeStartUrl(startUrl);
      set((state) => ({
        ssoSessions: state.ssoSessions.map((s) =>
          normalizeStartUrl(s.startUrl) === normalized ? { ...s, label } : s,
        ),
      }));
      try {
        await cloudService.updateSSOSessionLabel(startUrl, label);
      } catch (err) {
        console.error('[CloudAuth] failed to update label:', err);
      }
    },

    signInProvider: async (provider) => {
      const existing = providerWaiters.get(provider);
      if (existing) return existing;
      const run = (async () => {
        try {
          const job =
            provider === 'gcp'
              ? await cloudService.loginGCP()
              : await cloudService.loginAzure();
          setProviderLogin(provider, job);
          const final = await cloudService.waitForLoginJob(job, (update) =>
            setProviderLogin(provider, update),
          );
          setProviderLogin(provider, final);
          await get().loadAuthSummary(true);
          if (final.state === 'succeeded') {
            announceAuthChanged(provider);
            return true;
          }
          return false;
        } catch (err: any) {
          setProviderLogin(provider, {
            id: '',
            provider,
            label: provider,
            command: '',
            state: 'failed',
            error: err?.installHint
              ? `${err.message}. ${err.installHint}`
              : err?.response?.data?.error || err?.message || 'Sign-in failed',
            startedAt: Date.now(),
          });
          return false;
        } finally {
          providerWaiters.delete(provider);
        }
      })();
      providerWaiters.set(provider, run);
      return run;
    },

    signInAWSProfile: async (profile) => {
      const key = `profile:${profile}`;
      const existing = providerWaiters.get(key);
      if (existing) return existing;
      const setProfileJob = (job: CloudLoginJob) =>
        set((state) => ({
          profileLogins: { ...state.profileLogins, [profile]: job },
        }));
      const run = (async () => {
        try {
          const job = await cloudService.loginAWSProfile(profile);
          setProfileJob(job);
          const final =
            job.state === 'running'
              ? await cloudService.waitForLoginJob(job, setProfileJob)
              : job;
          setProfileJob(final);
          await get().loadAuthSummary(true);
          if (final.state === 'succeeded') {
            announceAuthChanged('aws');
            return true;
          }
          return false;
        } catch (err: any) {
          setProfileJob({
            id: '',
            provider: 'aws',
            label: profile,
            command: '',
            state: 'failed',
            error: err?.installHint
              ? `${err.message}. ${err.installHint}`
              : err?.response?.data?.error || err?.message || 'Sign-in failed',
            startedAt: Date.now(),
          });
          return false;
        } finally {
          providerWaiters.delete(key);
        }
      })();
      providerWaiters.set(key, run);
      return run;
    },

    cancelProviderLogin: async (provider) => {
      const job = get().providerLogins[provider];
      if (job?.id && job.state === 'running') {
        try {
          await cloudService.cancelLoginJob(job.id);
        } catch (err) {
          console.error('[CloudAuth] cancel provider login failed:', err);
        }
      }
      setProviderLogin(provider, undefined);
    },
  };
};
