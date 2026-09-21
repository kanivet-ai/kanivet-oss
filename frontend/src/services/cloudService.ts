import axios from 'axios';
import {
  CloudProvider,
  AWSProfile,
  GCPProject,
  AzureSubscription,
  DiscoveredCluster,
  CloudAuthStatus,
  CloudAuthSummary,
  ClusterAuthInfo,
  ClusterSSOBinding,
  CloudLoginJob,
  DiscoverRequest,
  ImportRequest,
  SSOLoginSession,
  SSOSessionStatus,
  BatchImportJob,
  SSOAccount,
  DiscoveryEvent,
} from '../types/cloud';
import api from './api';
import { wsManager } from './api/websocket';
import { getApiBase } from './api/types';

/** Error thrown when the backend says an interactive AWS SSO sign-in is needed. */
export class SSOLoginRequiredError extends Error {
  readonly startUrl: string;
  constructor(message: string, startUrl: string) {
    super(message);
    this.name = 'SSOLoginRequiredError';
    this.startUrl = startUrl;
  }
}

/** Error thrown when a cloud CLI Kanivet needs is not installed. */
export class CLIMissingError extends Error {
  readonly binary: string;
  readonly installHint?: string;
  constructor(message: string, binary: string, installHint?: string) {
    super(message);
    this.name = 'CLIMissingError';
    this.binary = binary;
    this.installHint = installHint;
  }
}

export const describeCloudError = (err: unknown, fallback = 'Something went wrong'): string => {
  if (err instanceof SSOLoginRequiredError) return 'AWS SSO sign-in required';
  if (err instanceof CLIMissingError) return err.installHint ? `${err.message}. ${err.installHint}` : err.message;
  const anyErr = err as any;
  return anyErr?.response?.data?.error || anyErr?.message || fallback;
};

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

class CloudService {
  private client = axios.create();
  private discoveryHandlers: Map<string, (event: DiscoveryEvent) => void> = new Map();

  constructor() {
    this.client.interceptors.request.use(async (config) => {
      config.baseURL = getApiBase();
      await wsManager.waitForSessionSecret();
      const sessionSecret = wsManager.getSessionSecret();
      if (sessionSecret) {
        config.headers['X-Session-Secret'] = sessionSecret;
      }
      return config;
    });

    this.client.interceptors.response.use(
      (response) => response,
      async (error) => {
        const errorData = error.response?.data;
        const refreshRequired = error.response?.headers?.['x-session-refresh-required'] === 'true';
        const sessionSecretError =
          error.response?.status === 403 &&
          (errorData?.error === 'session_secret_mismatch' ||
            errorData?.error === 'session_secret_missing' ||
            refreshRequired);

        if (sessionSecretError && error.config && !error.config.__sessionRetried) {
          const refreshed = await wsManager.refreshSessionSecret();
          if (refreshed) {
            error.config.__sessionRetried = true;
            const sessionSecret = wsManager.getSessionSecret();
            if (sessionSecret) {
              error.config.headers = error.config.headers || {};
              error.config.headers['X-Session-Secret'] = sessionSecret;
            }
            return this.client.request(error.config);
          }
        }

        if (errorData?.code === 'sso_login_required') {
          return Promise.reject(new SSOLoginRequiredError(errorData.error, errorData.startUrl));
        }
        if (errorData?.code === 'cli_missing') {
          return Promise.reject(new CLIMissingError(errorData.error, errorData.binary, errorData.installHint));
        }
        return Promise.reject(error);
      }
    );
  }

  // ---- Auth overview -------------------------------------------------------

  async getAuthStatus(): Promise<CloudAuthStatus> {
    const response = await this.client.get('/cloud/status');
    return response.data.status;
  }

  async getAuthSummary(force = false): Promise<CloudAuthSummary> {
    const response = await this.client.get('/cloud/auth', { params: force ? { force: 1 } : {} });
    return response.data;
  }

  async describeClusterAuth(cluster: string): Promise<ClusterAuthInfo> {
    const response = await this.client.get('/cloud/cluster-auth', { params: { cluster } });
    return response.data;
  }

  /** Reach a context through an Identity Center account and role; the kubeconfig is not touched. */
  async bindClusterSSO(cluster: string, startUrl: string, accountId: string, roleName: string): Promise<ClusterSSOBinding> {
    const response = await this.client.put('/cloud/cluster-auth/sso', { cluster, startUrl, accountId, roleName });
    return response.data;
  }

  async unbindClusterSSO(cluster: string): Promise<void> {
    await this.client.delete('/cloud/cluster-auth/sso', { params: { cluster } });
  }

  // ---- CLI login jobs (gcloud / az / aws --profile) -------------------------

  async getLoginJob(id: string): Promise<CloudLoginJob> {
    const response = await this.client.get(`/cloud/login-jobs/${encodeURIComponent(id)}`);
    return response.data;
  }

  async cancelLoginJob(id: string): Promise<void> {
    await this.client.delete(`/cloud/login-jobs/${encodeURIComponent(id)}`);
  }

  /** Polls a CLI login job until it leaves the running state. */
  async waitForLoginJob(
    job: CloudLoginJob,
    onUpdate?: (job: CloudLoginJob) => void,
    intervalMs = 1500
  ): Promise<CloudLoginJob> {
    let current = job;
    while (current.state === 'running') {
      await sleep(intervalMs);
      try {
        current = await this.getLoginJob(current.id);
        onUpdate?.(current);
      } catch (err: any) {
        if (err?.response?.status === 404) {
          return { ...current, state: 'failed', error: 'The sign-in process disappeared.' };
        }
      }
    }
    return current;
  }

  // ---- AWS ------------------------------------------------------------------

  async listAWSProfiles(): Promise<AWSProfile[]> {
    const response = await this.client.get('/cloud/aws/profiles');
    return response.data.profiles || [];
  }

  /** Runs `aws sso login --profile <profile>` in the background. */
  async loginAWSProfile(profile: string): Promise<CloudLoginJob> {
    const response = await this.client.post('/cloud/aws/login', { profile });
    return response.data;
  }

  async getAWSSSOSessions(): Promise<SSOSessionStatus[]> {
    const response = await this.client.get('/cloud/aws/sso/sessions');
    return response.data.sessions || [];
  }

  /** Starts a device-code login and returns immediately; poll with getSSOLogin. */
  async beginAWSSSOLogin(startUrl: string, region?: string, openBrowser = true): Promise<SSOLoginSession> {
    const response = await this.client.post('/cloud/aws/sso/login', { startUrl, region, openBrowser });
    return response.data;
  }

  async getSSOLogin(id: string): Promise<SSOLoginSession> {
    const response = await this.client.get(`/cloud/aws/sso/login/${encodeURIComponent(id)}`);
    return response.data;
  }

  async cancelSSOLogin(id: string): Promise<void> {
    await this.client.delete(`/cloud/aws/sso/login/${encodeURIComponent(id)}`);
  }

  /** Polls an SSO login until the user approves, cancels, or it expires. */
  async waitForSSOLogin(
    session: SSOLoginSession,
    onUpdate?: (session: SSOLoginSession) => void,
    intervalMs = 1500
  ): Promise<SSOLoginSession> {
    let current = session;
    while (current.state === 'pending') {
      await sleep(intervalMs);
      try {
        current = await this.getSSOLogin(current.id);
        onUpdate?.(current);
      } catch (err: any) {
        if (err?.response?.status === 404) {
          return { ...current, state: 'failed', error: 'The sign-in request disappeared.' };
        }
      }
    }
    return current;
  }

  /** Silently renews the access token with the stored refresh token. Throws SSOLoginRequiredError when impossible. */
  async refreshAWSSSOSession(startUrl: string): Promise<SSOSessionStatus | null> {
    const response = await this.client.post('/cloud/aws/sso/refresh', { startUrl });
    return response.data.session || null;
  }

  /** Removes every cached token for the portal (same effect as `aws sso logout` for it). */
  async signOutAWSSSO(startUrl: string): Promise<void> {
    await this.client.post('/cloud/aws/sso/signout', { startUrl });
  }

  async saveSSOSession(startUrl: string, region: string, label: string): Promise<void> {
    await this.client.post('/cloud/aws/sso/session', { startUrl, region, label });
  }

  async updateSSOSessionLabel(startUrl: string, label: string): Promise<void> {
    await this.client.put('/cloud/aws/sso/session/label', { startUrl, label });
  }

  async deleteSSOSession(startUrl: string): Promise<void> {
    await this.client.delete('/cloud/aws/sso/session', { params: { startUrl } });
  }

  async getAWSSSOAccounts(startUrl: string): Promise<SSOAccount[]> {
    const response = await this.client.get('/cloud/aws/sso/accounts', { params: { startUrl } });
    return response.data.accounts || [];
  }

  async getAWSSSOAccountRoles(startUrl: string, accountId: string): Promise<string[]> {
    const response = await this.client.get('/cloud/aws/sso/roles', { params: { startUrl, accountId } });
    return response.data.roles || [];
  }

  async getAWSAccountId(profile: string): Promise<string> {
    const response = await this.client.get('/cloud/aws/account', { params: { profile } });
    return response.data.accountId;
  }

  // ---- GCP ------------------------------------------------------------------

  async listGCPProjects(): Promise<GCPProject[]> {
    const response = await this.client.get('/cloud/gcp/projects');
    return response.data.projects || [];
  }

  /** Runs `gcloud auth login` in the background; returns the job to poll. */
  async loginGCP(): Promise<CloudLoginJob> {
    const response = await this.client.post('/cloud/gcp/login');
    return response.data;
  }

  async getGCPLocations(projectId: string): Promise<string[]> {
    const response = await this.client.get('/cloud/gcp/locations', { params: { projectId } });
    return response.data.locations || [];
  }

  async useGCPServiceAccount(keyFilePath: string): Promise<void> {
    await this.client.post('/cloud/gcp/service-account', { keyFilePath });
  }

  // ---- Azure ----------------------------------------------------------------

  async listAzureSubscriptions(): Promise<AzureSubscription[]> {
    const response = await this.client.get('/cloud/azure/subscriptions');
    return response.data.subscriptions || [];
  }

  /** Runs `az login` in the background; returns the job to poll. */
  async loginAzure(): Promise<CloudLoginJob> {
    const response = await this.client.post('/cloud/azure/login');
    return response.data;
  }

  // ---- Discovery / import ---------------------------------------------------

  async discoverClusters(req: DiscoverRequest): Promise<DiscoveredCluster[]> {
    const response = await this.client.post('/cloud/discover', req);
    return response.data.clusters || [];
  }

  async discoverAllClusters(): Promise<DiscoveredCluster[]> {
    const response = await this.client.post('/cloud/discover/all');
    return response.data.clusters || [];
  }

  async importCluster(req: ImportRequest): Promise<void> {
    await this.client.post('/cloud/import', req);
  }

  async batchImportClusters(clusters: ImportRequest[]): Promise<{ jobId: string }> {
    const response = await this.client.post('/cloud/import/batch', { clusters });
    return response.data;
  }

  async getBatchImportStatus(jobId?: string): Promise<BatchImportJob | null> {
    const response = await this.client.get('/cloud/import/batch/status', { params: jobId ? { jobId } : {} });
    if (response.data.active === false) return null;
    return response.data;
  }

  async getImportedClusterIDs(): Promise<string[]> {
    const response = await this.client.get('/cloud/imported');
    return response.data.clusters || [];
  }

  getProviderDisplayName(provider: CloudProvider): string {
    switch (provider) {
      case 'aws': return 'Amazon EKS';
      case 'gcp': return 'Google GKE';
      case 'azure': return 'Azure AKS';
      default: return provider;
    }
  }

  discoverClustersStreaming(
    req: DiscoverRequest,
    onEvent: (event: DiscoveryEvent) => void,
  ): () => void {
    const key = `${req.provider}-${req.ssoStartUrl || req.profile || req.projectId || req.subscription || 'default'}-${Date.now()}`;
    this.discoveryHandlers.set(key, onEvent);

    const messageHandler = (msg: any) => {
      if (msg.type === 'cloud.discover' && msg.payload) {
        const payload = msg.payload;
        if (payload.key && payload.key !== key) return;
        const event: DiscoveryEvent = {
          type: payload.type,
          cluster: payload.cluster,
          progress: payload.progress,
          error: payload.error,
        };
        onEvent(event);
      }
    };

    const existing = (api as any).wsHandlers.get('cloud.discover') as Set<(msg: any) => void> | undefined;
    if (existing) {
      existing.add(messageHandler);
    } else {
      (api as any).wsHandlers.set('cloud.discover', new Set([messageHandler]));
    }

    (api as any).__sendWS({
      type: 'cloud.discover',
      payload: {
        action: 'start',
        key,
        provider: req.provider,
        ssoStartUrl: req.ssoStartUrl,
        profile: req.profile,
        accountIds: req.accountIds,
        region: req.region,
        allRegions: req.allRegions,
        projectId: req.projectId,
        subscription: req.subscription,
      },
    });

    return () => {
      this.discoveryHandlers.delete(key);
      (api as any).__sendWS({
        type: 'cloud.discover',
        payload: { action: 'stop', key },
      });
      const handlers = (api as any).wsHandlers.get('cloud.discover');
      if (handlers) {
        handlers.delete(messageHandler);
        if (handlers.size === 0) {
          (api as any).wsHandlers.delete('cloud.discover');
        }
      }
    };
  }
}

export default new CloudService();
