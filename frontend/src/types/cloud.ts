export type CloudProvider = 'aws' | 'gcp' | 'azure';
export type AuthMethod = 'profile' | 'sso' | 'service_account' | 'cli' | 'default';
export type AWSProfileSource = 'config' | 'credentials';

export interface AWSProfile {
  name: string;
  region?: string;
  accountId?: string;
  roleArn?: string;
  isSso: boolean;
  ssoSession?: string;
  ssoStartUrl?: string;
  source: AWSProfileSource;
}

export interface GCPProject {
  id: string;
  name: string;
  number?: string;
}

export interface AzureSubscription {
  id: string;
  name: string;
  tenantId: string;
  state: string;
}

export interface DiscoveredCluster {
  id: string;
  name: string;
  provider: CloudProvider;
  region: string;
  accountId?: string;
  projectId?: string;
  resourceGroup?: string;
  endpoint: string;
  version?: string;
  status: string;
  nodeCount?: number;
  isImported: boolean;
  hasAccess: boolean;
  accessChecked: boolean;
  accessError?: string;
  tags?: Record<string, string>;
  ssoStartUrl?: string;
  profile?: string;
  availableRoles?: string[];
}

export interface CloudAuthStatus {
  aws: boolean;
  gcp: boolean;
  azure: boolean;
}

export interface DiscoverRequest {
  provider: CloudProvider;
  accountId?: string;
  accountIds?: string[];
  profile?: string;
  region?: string;
  allRegions?: boolean;
  projectId?: string;
  subscription?: string;
  ssoStartUrl?: string;
}

export interface SSOAccount {
  accountId: string;
  accountName: string;
  emailAddress: string;
}

export interface ImportRequest {
  provider: CloudProvider;
  clusterId: string;
  name: string;
  region: string;
  accountId?: string;
  projectId?: string;
  resourceGroup?: string;
  profile?: string;
  ssoStartUrl?: string;
  ssoRoleName?: string;
}

export interface BatchImportResult {
  clusterId: string;
  name: string;
  success: boolean;
  error?: string;
}

export interface BatchImportResponse {
  jobId?: string;
  results: BatchImportResult[];
  successful: number;
  failed: number;
}

export interface BatchImportJob {
  id: string;
  total: number;
  completed: number;
  successful: number;
  failed: number;
  inProgress: boolean;
  results: BatchImportResult[];
  startedAt: number;
}

export interface SSOLoginResponse {
  deviceCode: string;
  userCode: string;
  verificationUrl: string;
  expiresIn: number;
}

export type DiscoveryEventType = 'cluster' | 'status_update' | 'progress' | 'complete' | 'error';

export interface DiscoveryProgress {
  status?: string;
  region?: string;
  accountId?: string;
  regionsScanned: number;
  totalRegions: number;
  clustersFound: number;
}

export interface DiscoveryEvent {
  type: DiscoveryEventType;
  cluster?: DiscoveredCluster;
  progress?: DiscoveryProgress;
  error?: string;
}
