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
	Name        string           `json:"name"`
	Region      string           `json:"region,omitempty"`
	AccountID   string           `json:"accountId,omitempty"`
	RoleArn     string           `json:"roleArn,omitempty"`
	IsSSO       bool             `json:"isSso"`
	SSOSession  string           `json:"ssoSession,omitempty"`
	SSOStartURL string           `json:"ssoStartUrl,omitempty"`
	Source      AWSProfileSource `json:"source"`
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

type SSOLoginRequest struct {
	StartURL  string `json:"startUrl"`
	Region    string `json:"region"`
	AccountID string `json:"accountId,omitempty"`
	RoleName  string `json:"roleName,omitempty"`
}

type SSOLoginResponse struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"`
}

type SSOActivateResponse struct {
	ProfileName string `json:"profileName"`
	AccountID   string `json:"accountId"`
	RoleName    string `json:"roleName"`
	Region      string `json:"region"`
	AccessKeyID string `json:"accessKeyId"`
	ExpiresAt   int64  `json:"expiresAt"`
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
