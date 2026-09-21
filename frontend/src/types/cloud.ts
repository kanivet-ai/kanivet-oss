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
  credentialProcess?: boolean;
  hasStaticCredentials?: boolean;
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

/** Lifecycle of one interactive IAM Identity Center device-code login. */
export type SSOLoginState = 'pending' | 'authorized' | 'failed' | 'cancelled' | 'expired';

export interface SSOLoginSession {
  id: string;
  startUrl: string;
  region: string;
  userCode: string;
  verificationUrl: string;
  verificationUrlComplete: string;
  /** Device-code expiry, epoch ms. */
  expiresAt: number;
  state: SSOLoginState;
  error?: string;
  tokenExpiresAt?: number;
  startedAt: number;
  browserOpened: boolean;
}

/**
 * How usable a portal's cached token is. `refreshable` means the access token
 * lapsed but the backend can renew it silently with the refresh token.
 */
export type SSOSessionState = 'active' | 'refreshable' | 'expired' | 'signed_out';

export interface SSOSessionStatus {
  startUrl: string;
  region: string;
  label?: string;
  state: SSOSessionState;
  isValid: boolean;
  refreshable: boolean;
  /** Access-token expiry, epoch ms; 0 when there is no token. */
  expiresAt: number;
  /** Who defined this portal: Kanivet, the user's ~/.aws/config, or only the token cache. */
  source: 'kanivet' | 'config' | 'cache';
  /** True when Kanivet added it (removable from the UI). */
  managed: boolean;
  /** User-defined `[sso-session …]` names that point at this portal. */
  sessionNames?: string[];
  /** Number of user profiles (non-Kanivet) that use this portal. */
  profileCount: number;
  /** In-flight interactive login, if any. */
  login?: SSOLoginSession | null;
  /** Why the session is expired when a person must act, e.g. a rejected refresh token. */
  error?: string;
}

export type CloudLoginJobState = 'running' | 'succeeded' | 'failed' | 'cancelled';

/** A `gcloud auth login` / `az login` / `aws sso login --profile` the backend spawned. */
export interface CloudLoginJob {
  id: string;
  provider: CloudProvider;
  label: string;
  command: string;
  state: CloudLoginJobState;
  url?: string;
  code?: string;
  output?: string;
  error?: string;
  startedAt: number;
  finishedAt?: number;
}

export type ProviderAuthState = 'active' | 'expired' | 'signed_out' | 'unavailable';

export interface ProviderAuthSummary {
  provider: CloudProvider;
  state: ProviderAuthState;
  signedIn: boolean;
  identity?: string;
  detail?: string;
  expiresAt?: number;
  cliName: string;
  cliInstalled: boolean;
  cliInstallHint?: string;
  pluginName?: string;
  pluginInstalled: boolean;
  pluginInstallHint?: string;
  error?: string;
  login?: CloudLoginJob | null;
  checkedAt: number;
}

export interface AWSAuthSummary {
  sessions: SSOSessionStatus[];
  cliInstalled: boolean;
  cliInstallHint?: string;
  profileCount: number;
  profileLogins?: CloudLoginJob[];
}

export interface CloudAuthSummary {
  aws: AWSAuthSummary;
  gcp: ProviderAuthSummary;
  azure: ProviderAuthSummary;
  updatedAt: number;
}

export type ClusterSignIn = 'aws-sso' | 'aws-profile' | 'gcp' | 'azure' | '';

/** How a kubeconfig context authenticates, resolved down to the sign-in that fixes it. */
/** Kanivet state, not kubeconfig: the identity used to reach one context. */
export interface ClusterSSOBinding {
  startUrl: string;
  accountId: string;
  roleName: string;
  profile: string;
}

export interface ClusterAuthInfo {
  cluster: string;
  provider: 'aws' | 'gcp' | 'azure' | 'other';
  method: string;
  command?: string;
  commandInstalled: boolean;
  profile?: string;
  ssoStartUrl?: string;
  ssoRegion?: string;
  ssoSessionName?: string;
  ssoState?: SSOSessionState;
  signIn?: ClusterSignIn;
  /** Profiles in ~/.aws/config for this EKS cluster's account, when the context names none. */
  matchingProfiles?: string[];
  /** AWS account of an EKS context, read from its ARN. */
  accountId?: string;
  /** Identity Center account and role chosen in Kanivet for this context. */
  ssoBinding?: ClusterSSOBinding;
  externalTool: boolean;
  hint?: string;
  installHint?: string;
  kubeconfigPath?: string;
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
