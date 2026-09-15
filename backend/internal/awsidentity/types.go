package awsidentity

import "time"

type Mechanism string

const (
	MechanismIRSA        Mechanism = "irsa"
	MechanismPodIdentity Mechanism = "pod-identity"
	MechanismNone        Mechanism = "none"
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
	StatusUnknown Status = "unknown"
	StatusSkipped Status = "skipped"
)

type CredentialSource string

const (
	CredentialSourceExec     CredentialSource = "exec"
	CredentialSourceOverride CredentialSource = "override"
	CredentialSourceSSO      CredentialSource = "sso"
	CredentialSourceNone     CredentialSource = "none"
)

type Credentials struct {
	Source         CredentialSource `json:"source"`
	Profile        string           `json:"profile,omitempty"`
	Region         string           `json:"region,omitempty"`
	AccountID      string           `json:"accountId,omitempty"`
	EKSClusterName string           `json:"eksClusterName,omitempty"`
	Error          string           `json:"error,omitempty"`
}

type ClusterStatus struct {
	Available   bool        `json:"available"`
	Reason      string      `json:"reason,omitempty"`
	Credentials Credentials `json:"credentials"`
}

type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Mono  bool   `json:"mono,omitempty"`
	Link  string `json:"link,omitempty"`
}

type Problem struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

const (
	StepPod            = "pod"
	StepServiceAccount = "serviceAccount"
	StepBinding        = "binding"
	StepRole           = "role"
	StepTrust          = "trust"
	StepPermissions    = "permissions"
)

type Step struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Status   Status    `json:"status"`
	Summary  string    `json:"summary"`
	Details  []Detail  `json:"details,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
}

type PodEvidence struct {
	Name           string    `json:"name"`
	Mechanism      Mechanism `json:"mechanism"`
	RoleArn        string    `json:"roleArn,omitempty"`
	TokenVolume    string    `json:"tokenVolume,omitempty"`
	InjectedEnv    []string  `json:"injectedEnv,omitempty"`
	Containers     []string  `json:"containers,omitempty"`
	ServiceAccount string    `json:"serviceAccount"`
	Phase          string    `json:"phase,omitempty"`
}

type Binding struct {
	Mechanism      Mechanism         `json:"mechanism"`
	RoleArn        string            `json:"roleArn,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	AssociationID  string            `json:"associationId,omitempty"`
	AssociationArn string            `json:"associationArn,omitempty"`
	TargetRoleArn  string            `json:"targetRoleArn,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

type Role struct {
	Arn                    string     `json:"arn"`
	Name                   string     `json:"name"`
	Path                   string     `json:"path,omitempty"`
	AccountID              string     `json:"accountId"`
	Description            string     `json:"description,omitempty"`
	MaxSessionDuration     int32      `json:"maxSessionDuration,omitempty"`
	LastUsedAt             *time.Time `json:"lastUsedAt,omitempty"`
	LastUsedRegion         string     `json:"lastUsedRegion,omitempty"`
	PermissionsBoundaryArn string     `json:"permissionsBoundaryArn,omitempty"`
	CreatedAt              *time.Time `json:"createdAt,omitempty"`
	ConsoleURL             string     `json:"consoleUrl,omitempty"`
	CrossAccount           bool       `json:"crossAccount"`
}

type TrustStatement struct {
	Index      int                            `json:"index"`
	Sid        string                         `json:"sid,omitempty"`
	Effect     string                         `json:"effect"`
	Principal  map[string][]string            `json:"principal,omitempty"`
	Actions    []string                       `json:"actions"`
	Conditions map[string]map[string][]string `json:"conditions,omitempty"`
	Matches    bool                           `json:"matches"`
}

type TrustPolicy struct {
	Document              string           `json:"document"`
	Verdict               Status           `json:"verdict"`
	MatchedStatementIndex int              `json:"matchedStatementIndex"`
	Statements            []TrustStatement `json:"statements"`
	OIDCIssuer            string           `json:"oidcIssuer,omitempty"`
	OIDCProviderArn       string           `json:"oidcProviderArn,omitempty"`
	OIDCProviderExists    *bool            `json:"oidcProviderExists,omitempty"`
	Problems              []Problem        `json:"problems,omitempty"`
}

type PolicyType string

const (
	PolicyTypeManaged  PolicyType = "managed"
	PolicyTypeInline   PolicyType = "inline"
	PolicyTypeBoundary PolicyType = "boundary"
)

type PolicyStatement struct {
	Index        int      `json:"index"`
	Sid          string   `json:"sid,omitempty"`
	Effect       string   `json:"effect"`
	Actions      []string `json:"actions,omitempty"`
	NotActions   []string `json:"notActions,omitempty"`
	Resources    []string `json:"resources,omitempty"`
	NotResources []string `json:"notResources,omitempty"`
	HasCondition bool     `json:"hasCondition"`
}

type Policy struct {
	Name       string            `json:"name"`
	Arn        string            `json:"arn,omitempty"`
	Type       PolicyType        `json:"type"`
	AWSManaged bool              `json:"awsManaged"`
	VersionID  string            `json:"versionId,omitempty"`
	Document   string            `json:"document"`
	Statements []PolicyStatement `json:"statements"`
	ConsoleURL string            `json:"consoleUrl,omitempty"`
}

type Decision string

const (
	DecisionAllowed      Decision = "allowed"
	DecisionExplicitDeny Decision = "explicitDeny"
	DecisionImplicitDeny Decision = "implicitDeny"
	DecisionError        Decision = "error"
)

type MatchedStatement struct {
	PolicyName     string     `json:"policyName"`
	PolicyType     PolicyType `json:"policyType"`
	PolicyArn      string     `json:"policyArn,omitempty"`
	StatementIndex int        `json:"statementIndex"`
	Sid            string     `json:"sid,omitempty"`
	StartLine      int        `json:"startLine,omitempty"`
	EndLine        int        `json:"endLine,omitempty"`
}

type CheckSource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Container string `json:"container,omitempty"`
}

type Check struct {
	ID                 string             `json:"id"`
	Source             CheckSource        `json:"source"`
	Service            string             `json:"service"`
	Action             string             `json:"action"`
	Resource           string             `json:"resource"`
	Decision           Decision           `json:"decision"`
	MatchedStatements  []MatchedStatement `json:"matchedStatements,omitempty"`
	MissingContextKeys []string           `json:"missingContextKeys,omitempty"`
	OrgDecision        string             `json:"orgDecision,omitempty"`
	BoundaryDecision   string             `json:"boundaryDecision,omitempty"`
	Error              string             `json:"error,omitempty"`
}

type Explanation struct {
	Cluster             string       `json:"cluster"`
	Namespace           string       `json:"namespace"`
	ServiceAccount      string       `json:"serviceAccount"`
	PodName             string       `json:"podName,omitempty"`
	Mechanism           Mechanism    `json:"mechanism"`
	Overall             Status       `json:"overall"`
	Credentials         Credentials  `json:"credentials"`
	Steps               []Step       `json:"steps"`
	Pod                 *PodEvidence `json:"pod,omitempty"`
	Binding             *Binding     `json:"binding,omitempty"`
	Role                *Role        `json:"role,omitempty"`
	Trust               *TrustPolicy `json:"trust,omitempty"`
	Policies            []Policy     `json:"policies,omitempty"`
	Checks              []Check      `json:"checks,omitempty"`
	PermissionError     string       `json:"permissionError,omitempty"`
	RequiredPermissions []string     `json:"requiredPermissions,omitempty"`
	GeneratedAt         time.Time    `json:"generatedAt"`
}

type IdentitySummary struct {
	Namespace      string     `json:"namespace"`
	ServiceAccount string     `json:"serviceAccount"`
	Mechanism      Mechanism  `json:"mechanism"`
	RoleArn        string     `json:"roleArn"`
	RoleName       string     `json:"roleName"`
	AccountID      string     `json:"accountId,omitempty"`
	CrossAccount   bool       `json:"crossAccount"`
	TrustStatus    Status     `json:"trustStatus"`
	TrustMessage   string     `json:"trustMessage,omitempty"`
	PodCount       int        `json:"podCount"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
}

type IdentityTotals struct {
	Total       int `json:"total"`
	IRSA        int `json:"irsa"`
	PodIdentity int `json:"podIdentity"`
	Broken      int `json:"broken"`
	Unused90d   int `json:"unused90d"`
}

type IdentitiesResponse struct {
	Credentials         Credentials       `json:"credentials"`
	Identities          []IdentitySummary `json:"identities"`
	Totals              IdentityTotals    `json:"totals"`
	PermissionError     string            `json:"permissionError,omitempty"`
	RequiredPermissions []string          `json:"requiredPermissions,omitempty"`
	GeneratedAt         time.Time         `json:"generatedAt"`
}

type CheckRequest struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

type SimulateRequest struct {
	RoleArn string         `json:"roleArn"`
	Checks  []CheckRequest `json:"checks"`
}

type SimulateResponse struct {
	Checks []Check `json:"checks"`
}

type CredentialsOverride struct {
	Profile string `json:"profile"`
	Region  string `json:"region,omitempty"`
}

// RequiredIAMPermissions lists what the operator's AWS principal needs for a full explanation.
var RequiredIAMPermissions = []string{
	"sts:GetCallerIdentity",
	"eks:DescribeCluster",
	"eks:ListPodIdentityAssociations",
	"eks:DescribePodIdentityAssociation",
	"iam:GetRole",
	"iam:GetOpenIDConnectProvider",
	"iam:ListAttachedRolePolicies",
	"iam:GetPolicy",
	"iam:GetPolicyVersion",
	"iam:ListRolePolicies",
	"iam:GetRolePolicy",
	"iam:SimulatePrincipalPolicy",
}
