import axios from 'axios';
import {
  CloudProvider,
  AWSProfile,
  GCPProject,
  AzureSubscription,
  DiscoveredCluster,
  CloudAuthStatus,
  DiscoverRequest,
  ImportRequest,
  SSOLoginResponse,
  BatchImportJob,
  SSOAccount,
  DiscoveryEvent,
} from '../types/cloud';
import api from './api';
import { wsManager } from './api/websocket';
import { getApiBase } from './api/types';

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

        return Promise.reject(error);
      }
    );
  }

  async getAuthStatus(): Promise<CloudAuthStatus> {
    const response = await this.client.get('/cloud/status');
    return response.data.status;
  }

  async listAWSProfiles(): Promise<AWSProfile[]> {
    const response = await this.client.get('/cloud/aws/profiles');
    return response.data.profiles || [];
  }

  async loginAWS(profile: string): Promise<void> {
    await this.client.post('/cloud/aws/login', { profile });
  }

  async startAWSSSOLogin(startUrl: string, region: string): Promise<SSOLoginResponse> {
    const response = await this.client.post('/cloud/aws/sso/start', { startUrl, region });
    return response.data;
  }

  async getAWSSSOSessions(): Promise<{ startUrl: string; region: string; expiresAt: number; isValid: boolean; label?: string }[]> {
    const response = await this.client.get('/cloud/aws/sso/sessions');
    return response.data.sessions || [];
  }

  async saveSSOSession(startUrl: string, region: string, label: string, expiresAt: number): Promise<void> {
    await this.client.post('/cloud/aws/sso/session', { startUrl, region, label, expiresAt });
  }

  async updateSSOSessionLabel(startUrl: string, label: string): Promise<void> {
    await this.client.put('/cloud/aws/sso/session/label', { startUrl, label });
  }

  async deleteSSOSession(startUrl: string): Promise<void> {
    await this.client.delete('/cloud/aws/sso/session', { params: { startUrl } });
  }

  async getSSOActiveAccount(): Promise<{
    startUrl: string;
    accountId: string;
    accountName: string;
    profileName: string;
    roleName: string;
    expiresAt: number;
  } | null> {
    const response = await this.client.get('/cloud/aws/sso/active-account');
    return response.data.account || null;
  }

  async setSSOActiveAccount(
    startUrl: string,
    accountId: string,
    accountName: string,
    profileName: string,
    roleName: string,
    expiresAt: number
  ): Promise<void> {
    await this.client.post('/cloud/aws/sso/active-account', {
      startUrl, accountId, accountName, profileName, roleName, expiresAt
    });
  }

  async clearSSOActiveAccount(): Promise<void> {
    await this.client.delete('/cloud/aws/sso/active-account');
  }

  async getAWSSSOAccounts(startUrl: string): Promise<SSOAccount[]> {
    const response = await this.client.get('/cloud/aws/sso/accounts', { params: { startUrl } });
    return response.data.accounts || [];
  }

  async getAWSSSOAccountRoles(startUrl: string, accountId: string): Promise<string[]> {
    const response = await this.client.get('/cloud/aws/sso/roles', { params: { startUrl, accountId } });
    return response.data.roles || [];
  }

  async activateAWSSSOAccount(startUrl: string, accountId: string, roleName?: string, region?: string): Promise<{
    profileName: string;
    accountId: string;
    roleName: string;
    region: string;
    accessKeyId: string;
    expiresAt: number;
  }> {
    const response = await this.client.post('/cloud/aws/sso/activate', { startUrl, accountId, roleName, region });
    return response.data;
  }

  async deactivateAWSSSOAccount(): Promise<void> {
    await this.client.post('/cloud/aws/sso/deactivate');
  }

  async getAWSAccountId(profile: string): Promise<string> {
    const response = await this.client.get('/cloud/aws/account', { params: { profile } });
    return response.data.accountId;
  }

  async refreshAWSCredentials(profile: string): Promise<void> {
    await this.client.post('/cloud/aws/refresh', { profile });
  }

  async listGCPProjects(): Promise<GCPProject[]> {
    const response = await this.client.get('/cloud/gcp/projects');
    return response.data.projects || [];
  }

  async loginGCP(): Promise<void> {
    await this.client.post('/cloud/gcp/login');
  }

  async getGCPLocations(projectId: string): Promise<string[]> {
    const response = await this.client.get('/cloud/gcp/locations', { params: { projectId } });
    return response.data.locations || [];
  }

  async useGCPServiceAccount(keyFilePath: string): Promise<void> {
    await this.client.post('/cloud/gcp/service-account', { keyFilePath });
  }

  async listAzureSubscriptions(): Promise<AzureSubscription[]> {
    const response = await this.client.get('/cloud/azure/subscriptions');
    return response.data.subscriptions || [];
  }

  async loginAzure(): Promise<void> {
    await this.client.post('/cloud/azure/login');
  }

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

  getProviderIcon(provider: CloudProvider): string {
    switch (provider) {
      case 'aws': return '☁️';
      case 'gcp': return '🌐';
      case 'azure': return '⬡';
      default: return '📦';
    }
  }

  discoverClustersStreaming(
    req: DiscoverRequest,
    onEvent: (event: DiscoveryEvent) => void,
  ): () => void {
    const key = `${req.provider}-${req.ssoStartUrl || req.profile || 'default'}-${Date.now()}`;
    this.discoveryHandlers.set(key, onEvent);

    const messageHandler = (msg: any) => {
      if (msg.type === 'cloud.discover' && msg.payload) {
        const payload = msg.payload;
        const event: DiscoveryEvent = {
          type: payload.type,
          cluster: payload.cluster,
          progress: payload.progress,
          error: payload.error,
        };
        onEvent(event);
      }
    };

    (api as any).wsHandlers.set('cloud.discover', new Set([messageHandler]));

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
