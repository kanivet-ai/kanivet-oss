package cloud

import "time"

type Provider string

const (
	ProviderAWS   Provider = "aws"
	ProviderGCP   Provider = "gcp"
	ProviderAzure Provider = "azure"
)

type AuthMethod string

const (
	AuthMethodProfile        AuthMethod = "profile"
	AuthMethodSSO            AuthMethod = "sso"
	AuthMethodServiceAccount AuthMethod = "service_account"
	AuthMethodCLI            AuthMethod = "cli"
	AuthMethodDefault        AuthMethod = "default"
)

type CloudAccount struct {
	ID          string     `json:"id" gorm:"primaryKey"`
	Provider    Provider   `json:"provider" gorm:"index"`
	Name        string     `json:"name"`
	AuthMethod  AuthMethod `json:"authMethod"`
	AccountID   string     `json:"accountId,omitempty"`
	Region      string     `json:"region,omitempty"`
	ProfileName string     `json:"profileName,omitempty"`
	SSOStartURL string     `json:"ssoStartUrl,omitempty"`
	ProjectID   string     `json:"projectId,omitempty"`
	TenantID    string     `json:"tenantId,omitempty"`
	IsActive    bool       `json:"isActive" gorm:"default:true"`
	LastSynced  *time.Time `json:"lastSynced,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type DiscoveredCluster struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Provider       Provider          `json:"provider"`
	Region         string            `json:"region"`
	AccountID      string            `json:"accountId,omitempty"`
	ProjectID      string            `json:"projectId,omitempty"`
	ResourceGroup  string            `json:"resourceGroup,omitempty"`
	Endpoint       string            `json:"endpoint"`
	Version        string            `json:"version,omitempty"`
	Status         string            `json:"status"`
	NodeCount      int               `json:"nodeCount,omitempty"`
	IsImported     bool              `json:"isImported"`
	HasAccess      bool              `json:"hasAccess"`
	AccessChecked  bool              `json:"accessChecked"`
	AccessError    string            `json:"accessError,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	SSOStartURL    string            `json:"ssoStartUrl,omitempty"`
	Profile        string            `json:"profile,omitempty"`
	AvailableRoles []string          `json:"availableRoles,omitempty"`
}

type AWSProfileSource string

const (
	ProfileSourceConfig      AWSProfileSource = "config"
	ProfileSourceCredentials AWSProfileSource = "credentials"
)

type AWSProfile struct {
	Name                 string           `json:"name"`
	Region               string           `json:"region,omitempty"`
	AccountID            string           `json:"accountId,omitempty"`
	RoleArn              string           `json:"roleArn,omitempty"`
	IsSSO                bool             `json:"isSso"`
	SSOSession           string           `json:"ssoSession,omitempty"`
	SSOStartURL          string           `json:"ssoStartUrl,omitempty"`
	CredentialProcess    bool             `json:"credentialProcess,omitempty"`
	HasStaticCredentials bool             `json:"hasStaticCredentials,omitempty"`
	Source               AWSProfileSource `json:"source"`
}

type GCPProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
}

type AzureSubscription struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	State    string `json:"state"`
}

type DiscoverRequest struct {
	Provider     Provider `json:"provider"`
	AccountID    string   `json:"accountId,omitempty"`
	AccountIDs   []string `json:"accountIds,omitempty"`
	Profile      string   `json:"profile,omitempty"`
	Region       string   `json:"region,omitempty"`
	AllRegions   bool     `json:"allRegions,omitempty"`
	ProjectID    string   `json:"projectId,omitempty"`
	Subscription string   `json:"subscription,omitempty"`
	SSOStartURL  string   `json:"ssoStartUrl,omitempty"`
}

type ImportRequest struct {
	Provider      Provider `json:"provider"`
	ClusterID     string   `json:"clusterId"`
	Name          string   `json:"name"`
	Region        string   `json:"region"`
	AccountID     string   `json:"accountId,omitempty"`
	ProjectID     string   `json:"projectId,omitempty"`
	ResourceGroup string   `json:"resourceGroup,omitempty"`
	Profile       string   `json:"profile,omitempty"`
	SSOStartURL   string   `json:"ssoStartUrl,omitempty"`
	SSORoleName   string   `json:"ssoRoleName,omitempty"`
}

type BatchImportRequest struct {
	Clusters []ImportRequest `json:"clusters"`
}

type BatchImportResult struct {
	ClusterID string `json:"clusterId"`
	Name      string `json:"name"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

type BatchImportResponse struct {
	JobID      string              `json:"jobId,omitempty"`
	Results    []BatchImportResult `json:"results"`
	Successful int                 `json:"successful"`
	Failed     int                 `json:"failed"`
}

type BatchImportJob struct {
	ID         string              `json:"id"`
	Total      int                 `json:"total"`
	Completed  int                 `json:"completed"`
	Successful int                 `json:"successful"`
	Failed     int                 `json:"failed"`
	InProgress bool                `json:"inProgress"`
	Results    []BatchImportResult `json:"results"`
	StartedAt  int64               `json:"startedAt"`
}

type SSOAccount struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	EmailAddr   string `json:"emailAddress"`
}

type DiscoveryEventType string

const (
	DiscoveryEventCluster      DiscoveryEventType = "cluster"
	DiscoveryEventStatusUpdate DiscoveryEventType = "status_update"
	DiscoveryEventProgress     DiscoveryEventType = "progress"
	DiscoveryEventComplete     DiscoveryEventType = "complete"
	DiscoveryEventError        DiscoveryEventType = "error"
)

type DiscoveryEvent struct {
	Type     DiscoveryEventType `json:"type"`
	Cluster  *DiscoveredCluster `json:"cluster,omitempty"`
	Progress *DiscoveryProgress `json:"progress,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type DiscoveryProgress struct {
	Status         string `json:"status,omitempty"`
	Region         string `json:"region,omitempty"`
	AccountID      string `json:"accountId,omitempty"`
	RegionsScanned int    `json:"regionsScanned"`
	TotalRegions   int    `json:"totalRegions"`
	ClustersFound  int    `json:"clustersFound"`
}

// ProviderAuthSummary is the sign-in state of a CLI-backed provider (gcloud, az).
type ProviderAuthSummary struct {
	Provider          Provider     `json:"provider"`
	State             string       `json:"state"` // active | expired | signed_out | unavailable
	SignedIn          bool         `json:"signedIn"`
	Identity          string       `json:"identity,omitempty"`
	Detail            string       `json:"detail,omitempty"`
	ExpiresAt         int64        `json:"expiresAt,omitempty"`
	CLIName           string       `json:"cliName"`
	CLIInstalled      bool         `json:"cliInstalled"`
	CLIInstallHint    string       `json:"cliInstallHint,omitempty"`
	PluginName        string       `json:"pluginName,omitempty"`
	PluginInstalled   bool         `json:"pluginInstalled"`
	PluginInstallHint string       `json:"pluginInstallHint,omitempty"`
	Error             string       `json:"error,omitempty"`
	Login             *CLILoginJob `json:"login,omitempty"`
	CheckedAt         int64        `json:"checkedAt"`
}

// AWSAuthSummary groups every IAM Identity Center portal plus CLI availability.
type AWSAuthSummary struct {
	Sessions       []SSOSessionStatus `json:"sessions"`
	CLIInstalled   bool               `json:"cliInstalled"`
	CLIInstallHint string             `json:"cliInstallHint,omitempty"`
	ProfileCount   int                `json:"profileCount"`
	ProfileLogins  []*CLILoginJob     `json:"profileLogins,omitempty"`
}

// CloudAuthSummary is what the toolbar account menu and the cluster error pane
// render from.
type CloudAuthSummary struct {
	AWS       AWSAuthSummary      `json:"aws"`
	GCP       ProviderAuthSummary `json:"gcp"`
	Azure     ProviderAuthSummary `json:"azure"`
	UpdatedAt int64               `json:"updatedAt"`
}

// ClusterAuthInfo explains how a kubeconfig context authenticates so the UI
// can offer the right sign-in action instead of a generic error.
// ClusterSSOBinding is the Identity Center identity Kanivet uses for one
// kubeconfig context. It is Kanivet state; the kubeconfig is not rewritten.
type ClusterSSOBinding struct {
	StartURL  string `json:"startUrl"`
	AccountID string `json:"accountId"`
	RoleName  string `json:"roleName"`
	Profile   string `json:"profile"`
}

type ClusterAuthInfo struct {
	Cluster          string `json:"cluster"`
	Provider         string `json:"provider"` // aws | gcp | azure | other
	Method           string `json:"method"`
	Command          string `json:"command,omitempty"`
	CommandInstalled bool   `json:"commandInstalled"`
	Profile          string `json:"profile,omitempty"`
	SSOStartURL      string `json:"ssoStartUrl,omitempty"`
	SSORegion        string `json:"ssoRegion,omitempty"`
	SSOSessionName   string `json:"ssoSessionName,omitempty"`
	SSOState         string `json:"ssoState,omitempty"`
	SignIn           string `json:"signIn,omitempty"` // aws-sso | aws-profile | gcp | azure
	// MatchingProfiles lists ~/.aws/config profiles whose sso_account_id is
	// this EKS cluster's account, for contexts that name no profile.
	MatchingProfiles []string `json:"matchingProfiles,omitempty"`
	// AccountID is the AWS account of an EKS context, read from its ARN.
	AccountID string `json:"accountId,omitempty"`
	// SSOBinding is the Identity Center account and role chosen in Kanivet
	// for this context, overriding the profile its exec block resolves to.
	SSOBinding     *ClusterSSOBinding `json:"ssoBinding,omitempty"`
	ExternalTool   bool               `json:"externalTool"`
	Hint           string             `json:"hint,omitempty"`
	InstallHint    string             `json:"installHint,omitempty"`
	KubeconfigPath string             `json:"kubeconfigPath,omitempty"`
}
