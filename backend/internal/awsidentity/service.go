package awsidentity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
)

const (
	identityListConcurrency = 8
	roleCacheTTL            = 60 * time.Second
	identitiesCacheTTL      = 60 * time.Second
	oidcIssuerCacheTTL      = 10 * time.Minute
	unusedRoleThreshold     = 90 * 24 * time.Hour
)

var ErrBadRequest = errors.New("bad request")

type Service struct {
	k8s      k8s.Interface
	cloud    *cloud.Service
	cache    *cache.Cache
	db       *db.DB
	resolver *Resolver
}

func NewService(k k8s.Interface, cloudSvc *cloud.Service, c *cache.Cache, database *db.DB) *Service {
	return &Service{
		k8s:      k,
		cloud:    cloudSvc,
		cache:    c,
		db:       database,
		resolver: NewResolver(k, cloudSvc, c, database),
	}
}

func (s *Service) Status(ctx context.Context, cluster string) ClusterStatus {
	return s.resolver.Status(ctx, cluster)
}

func (s *Service) GetCredentials(ctx context.Context, cluster string) Credentials {
	creds, _, err := s.resolver.Resolve(ctx, cluster)
	if err != nil && creds.Error == "" {
		creds.Error = err.Error()
	}
	return creds
}

func (s *Service) SetCredentialsOverride(ctx context.Context, cluster string, override CredentialsOverride) (Credentials, error) {
	if strings.TrimSpace(override.Profile) == "" {
		return Credentials{}, fmt.Errorf("%w: profile is required", ErrBadRequest)
	}
	if err := s.db.SetAWSIdentityCredentials(&db.AWSIdentityCredentials{Cluster: cluster, Profile: override.Profile, Region: override.Region}); err != nil {
		return Credentials{}, err
	}
	s.invalidate()
	return s.GetCredentials(ctx, cluster), nil
}

func (s *Service) ClearCredentialsOverride(ctx context.Context, cluster string) (Credentials, error) {
	if err := s.db.ClearAWSIdentityCredentials(cluster); err != nil {
		return Credentials{}, err
	}
	s.invalidate()
	return s.GetCredentials(ctx, cluster), nil
}

func (s *Service) invalidate() {
	s.cache.DeleteByPrefix("awsidentity-")
}

type roleBundle struct {
	Info     *roleInfo
	Policies []Policy
}

func (s *Service) roleBundle(ctx context.Context, client *iam.Client, roleArn, profile, callerAccount string) (*roleBundle, error) {
	key := s.cache.BuildKey("awsidentity-role", roleArn, profile)
	value, err := s.cache.GetOrSet(key, roleCacheTTL, func() (interface{}, error) {
		info, err := fetchRole(ctx, client, roleArn, callerAccount)
		if err != nil {
			return nil, err
		}
		return &roleBundle{Info: info}, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*roleBundle), nil
}

func (s *Service) rolePolicies(ctx context.Context, client *iam.Client, bundle *roleBundle, profile string) ([]Policy, error) {
	key := s.cache.BuildKey("awsidentity-policies", bundle.Info.Role.Arn, profile)
	value, err := s.cache.GetOrSet(key, roleCacheTTL, func() (interface{}, error) {
		return fetchPolicies(ctx, client, bundle.Info.Role.Name, bundle.Info.Role.PermissionsBoundaryArn)
	})
	if err != nil {
		return nil, err
	}
	return value.([]Policy), nil
}

func (s *Service) oidcIssuer(ctx context.Context, cfg aws.Config, cluster, clusterName, profile string) (string, error) {
	key := s.cache.BuildKey("awsidentity-oidc", cluster, clusterName, profile)
	value, err := s.cache.GetOrSet(key, oidcIssuerCacheTTL, func() (interface{}, error) {
		out, err := eks.NewFromConfig(cfg).DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(clusterName)})
		if err != nil {
			return nil, err
		}
		if out.Cluster == nil || out.Cluster.Identity == nil || out.Cluster.Identity.Oidc == nil {
			return "", nil
		}
		return aws.ToString(out.Cluster.Identity.Oidc.Issuer), nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

type explainContext struct {
	exp         *Explanation
	creds       Credentials
	cfg         aws.Config
	credErr     error
	roleCfg     aws.Config
	roleProfile string
	roleAccount string
	bundle      *roleBundle
	pod         *corev1.Pod
}

func (s *Service) Explain(ctx context.Context, cluster, namespace, serviceAccount, podName string) (*Explanation, error) {
	if namespace == "" {
		return nil, fmt.Errorf("%w: namespace is required", ErrBadRequest)
	}
	if serviceAccount == "" && podName == "" {
		return nil, fmt.Errorf("%w: serviceAccount or pod is required", ErrBadRequest)
	}
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	ec := &explainContext{exp: &Explanation{
		Cluster:        cluster,
		Namespace:      namespace,
		ServiceAccount: serviceAccount,
		PodName:        podName,
		Mechanism:      MechanismNone,
		GeneratedAt:    time.Now(),
	}}

	if podName != "" {
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		ec.pod = pod
		ec.exp.Pod = podEvidence(pod)
		if ec.exp.ServiceAccount == "" {
			ec.exp.ServiceAccount = ec.exp.Pod.ServiceAccount
		}
	}

	ec.creds, ec.cfg, ec.credErr = s.resolver.Resolve(ctx, cluster)
	ec.exp.Credentials = ec.creds

	sa := s.serviceAccountStep(ctx, clientset, ec)
	s.bindingStep(ctx, sa, ec)
	if ec.pod != nil {
		s.podStep(ec)
	}
	s.roleStep(ctx, ec)
	s.trustStep(ctx, ec)
	s.permissionsStep(ctx, ec)

	ec.exp.Overall = overallStatus(ec.exp.Steps)
	return ec.exp, nil
}

func (s *Service) serviceAccountStep(ctx context.Context, clientset kubernetes.Interface, ec *explainContext) *corev1.ServiceAccount {
	exp := ec.exp
	sa, err := clientset.CoreV1().ServiceAccounts(exp.Namespace).Get(ctx, exp.ServiceAccount, metav1.GetOptions{})
	step := Step{ID: StepServiceAccount, Title: "Service account"}
	if err != nil {
		step.Status = StatusError
		step.Summary = fmt.Sprintf("ServiceAccount %s/%s not found", exp.Namespace, exp.ServiceAccount)
		step.Problems = []Problem{{
			Code:    "service_account_not_found",
			Message: err.Error(),
			Hint:    "Check the pod's serviceAccountName and that the ServiceAccount exists in this namespace.",
		}}
		exp.Steps = append(exp.Steps, step)
		return nil
	}
	automount := "Yes"
	if sa.AutomountServiceAccountToken != nil && !*sa.AutomountServiceAccountToken {
		automount = "No"
	}
	step.Status = StatusOK
	step.Summary = fmt.Sprintf("%s/%s", sa.Namespace, sa.Name)
	step.Details = []Detail{
		{Label: "Name", Value: sa.Name, Mono: true},
		{Label: "Namespace", Value: sa.Namespace, Mono: true},
		{Label: "Automount token", Value: automount},
	}
	exp.Steps = append(exp.Steps, step)
	return sa
}

func (s *Service) bindingStep(ctx context.Context, sa *corev1.ServiceAccount, ec *explainContext) {
	exp := ec.exp
	step := Step{ID: StepBinding, Title: "Identity binding"}
	if sa == nil {
		step.Status = StatusSkipped
		step.Summary = "Skipped: service account not found"
		exp.Steps = append(exp.Steps, step)
		return
	}

	binding := bindingFromServiceAccount(sa)
	var lookupProblem *Problem
	if binding == nil {
		switch {
		case ec.credErr != nil:
			lookupProblem = &Problem{
				Code:    "pod_identity_unchecked",
				Message: "EKS Pod Identity associations could not be checked: " + ec.creds.Error,
				Hint:    "Choose an AWS profile for this cluster from the credentials menu.",
			}
		case ec.creds.EKSClusterName == "":
			lookupProblem = &Problem{
				Code:    "pod_identity_unchecked",
				Message: "EKS Pod Identity associations could not be checked because the EKS cluster name is unknown for this context.",
				Hint:    "Import the cluster through Kanivet or use a kubeconfig generated by `aws eks update-kubeconfig`.",
			}
		default:
			found, err := podIdentityBinding(ctx, eks.NewFromConfig(ec.cfg), ec.creds.EKSClusterName, sa.Namespace, sa.Name)
			switch {
			case err == nil:
				binding = found
			case isAccessDenied(err):
				s.notePermissionError(exp, err)
				lookupProblem = &Problem{Code: "operator_permission_denied", Message: "EKS Pod Identity associations could not be listed: " + apiErrorMessage(err)}
			default:
				lookupProblem = &Problem{Code: "pod_identity_unchecked", Message: "EKS Pod Identity associations could not be checked: " + apiErrorMessage(err)}
			}
		}
	}

	if binding == nil {
		step.Status = StatusWarning
		step.Summary = "No AWS identity bound to this service account"
		step.Problems = []Problem{{
			Code:    "no_identity",
			Message: "Pods using this service account fall back to the node's instance role or have no AWS credentials at all.",
			Hint:    "Annotate the ServiceAccount with eks.amazonaws.com/role-arn (IRSA) or create an EKS Pod Identity association.",
		}}
		if lookupProblem != nil {
			step.Problems = append(step.Problems, *lookupProblem)
			if lookupProblem.Code == "operator_permission_denied" {
				step.Status = StatusUnknown
			}
		}
		exp.Steps = append(exp.Steps, step)
		return
	}

	exp.Binding = binding
	exp.Mechanism = binding.Mechanism
	step.Status = StatusOK
	switch binding.Mechanism {
	case MechanismIRSA:
		step.Summary = "IRSA annotation → " + roleNameFromArn(binding.RoleArn)
		step.Details = []Detail{{Label: annotationRoleArn, Value: binding.RoleArn, Mono: true}}
		for _, key := range irsaAnnotationKeys[1:] {
			if v, ok := binding.Annotations[key]; ok {
				step.Details = append(step.Details, Detail{Label: key, Value: v, Mono: true})
			}
		}
	case MechanismPodIdentity:
		step.Summary = "EKS Pod Identity association → " + roleNameFromArn(binding.RoleArn)
		step.Details = []Detail{
			{Label: "Role ARN", Value: binding.RoleArn, Mono: true},
			{Label: "Association ID", Value: binding.AssociationID, Mono: true},
			{Label: "Association ARN", Value: binding.AssociationArn, Mono: true},
		}
	}
	exp.Steps = append(exp.Steps, step)
}

func (s *Service) podStep(ec *explainContext) {
	exp := ec.exp
	ev := exp.Pod
	step := Step{ID: StepPod, Title: "Pod", Details: []Detail{
		{Label: "Name", Value: ev.Name, Mono: true},
		{Label: "Phase", Value: ev.Phase},
		{Label: "Service account", Value: ev.ServiceAccount, Mono: true},
	}}
	if ev.TokenVolume != "" {
		step.Details = append(step.Details, Detail{Label: "Token volume", Value: ev.TokenVolume, Mono: true})
	}
	if len(ev.InjectedEnv) > 0 {
		step.Details = append(step.Details, Detail{Label: "Injected env", Value: strings.Join(ev.InjectedEnv, ", "), Mono: true})
	}
	if len(ev.Containers) > 0 {
		step.Details = append(step.Details, Detail{Label: "Containers", Value: strings.Join(ev.Containers, ", "), Mono: true})
	}

	binding := exp.Binding
	switch {
	case binding == nil && ev.Mechanism == MechanismNone:
		step.Status = StatusSkipped
		step.Summary = "No AWS credentials injected"
	case binding == nil:
		step.Status = StatusWarning
		step.Summary = fmt.Sprintf("Pod carries %s credentials but the service account is no longer bound", mechanismLabel(ev.Mechanism))
		step.Problems = []Problem{{
			Code:    "stale_injection",
			Message: "The pod was created while the service account still had an AWS identity. New pods will not receive credentials.",
			Actual:  ev.RoleArn,
			Hint:    "Restore the binding or restart the workload to converge on the current configuration.",
		}}
	case ev.Mechanism == MechanismNone:
		step.Status = StatusError
		step.Summary = "AWS credentials were not injected into this pod"
		hint := "Restart the pod so the webhook/agent can inject credentials."
		if binding.Mechanism == MechanismPodIdentity {
			hint = "Restart the pod; make sure the eks-pod-identity-agent add-on is installed on the cluster."
		}
		step.Problems = []Problem{{
			Code:     "pod_not_injected",
			Message:  "The service account is bound to a role, but this pod predates the binding or the injector was not running when it started.",
			Expected: binding.RoleArn,
			Hint:     hint,
		}}
	case ev.Mechanism != binding.Mechanism:
		step.Status = StatusWarning
		step.Summary = fmt.Sprintf("Pod uses %s but the service account is bound via %s", mechanismLabel(ev.Mechanism), mechanismLabel(binding.Mechanism))
		step.Problems = []Problem{{
			Code:     "mechanism_mismatch",
			Message:  "The credentials injected into the pod come from a different mechanism than the current binding.",
			Expected: mechanismLabel(binding.Mechanism),
			Actual:   mechanismLabel(ev.Mechanism),
			Hint:     "Restart the pod so it picks up the current binding.",
		}}
	case binding.Mechanism == MechanismIRSA && ev.RoleArn != "" && !strings.EqualFold(ev.RoleArn, binding.RoleArn):
		step.Status = StatusWarning
		step.Summary = "Pod was injected with a different role than the annotation"
		step.Problems = []Problem{{
			Code:     "role_mismatch",
			Message:  "The role annotation changed after this pod started; the pod still assumes the old role.",
			Expected: binding.RoleArn,
			Actual:   ev.RoleArn,
			Hint:     "Restart the pod to pick up the new role annotation.",
		}}
	default:
		step.Status = StatusOK
		step.Summary = fmt.Sprintf("%s credentials injected", mechanismLabel(ev.Mechanism))
	}
	exp.Steps = append(exp.Steps, step)
}

func (s *Service) roleStep(ctx context.Context, ec *explainContext) {
	exp := ec.exp
	step := Step{ID: StepRole, Title: "IAM role"}
	if exp.Binding == nil {
		step.Status = StatusSkipped
		step.Summary = "No role to inspect"
		exp.Steps = append(exp.Steps, step)
		return
	}
	roleArn := exp.Binding.RoleArn
	ec.roleAccount = accountFromArn(roleArn)
	step.Details = []Detail{
		{Label: "ARN", Value: roleArn, Mono: true, Link: roleConsoleURL(roleNameFromArn(roleArn))},
		{Label: "Account", Value: ec.roleAccount, Mono: true},
	}

	if ec.credErr != nil {
		step.Status = StatusUnknown
		step.Summary = "AWS credentials unavailable"
		step.Problems = []Problem{{
			Code:    "credentials_unavailable",
			Message: ec.creds.Error,
			Hint:    "Choose an AWS profile for this cluster from the credentials menu.",
		}}
		exp.Steps = append(exp.Steps, step)
		return
	}

	cfg, profile, err := s.resolver.ConfigForAccount(ctx, ec.cfg, ec.creds.AccountID, ec.roleAccount, ec.creds.Region)
	if err != nil {
		step.Status = StatusWarning
		step.Summary = fmt.Sprintf("Role lives in account %s, no credentials for it", ec.roleAccount)
		step.Problems = []Problem{{
			Code:     "cross_account_credentials",
			Message:  fmt.Sprintf("The role is in account %s but Kanivet only has credentials for account %s.", ec.roleAccount, ec.creds.AccountID),
			Expected: ec.roleAccount,
			Actual:   ec.creds.AccountID,
			Hint:     fmt.Sprintf("Set an AWS profile for account %s in the credentials menu", ec.roleAccount),
		}}
		exp.Steps = append(exp.Steps, step)
		return
	}
	ec.roleCfg = cfg
	ec.roleProfile = firstNonEmpty(profile, ec.creds.Profile)

	bundle, err := s.roleBundle(ctx, iam.NewFromConfig(cfg), roleArn, ec.roleProfile, ec.creds.AccountID)
	if err != nil {
		switch {
		case isNoSuchEntity(err):
			step.Status = StatusError
			step.Summary = "IAM role does not exist"
			step.Problems = []Problem{{
				Code:     "role_not_found",
				Message:  fmt.Sprintf("IAM role %s does not exist in account %s.", roleNameFromArn(roleArn), ec.roleAccount),
				Expected: roleArn,
				Hint:     "Check the ARN for typos, or create the role.",
			}}
		case isAccessDenied(err):
			s.notePermissionError(exp, err)
			step.Status = StatusUnknown
			step.Summary = "Not allowed to read the role"
			step.Problems = []Problem{{Code: "operator_permission_denied", Message: apiErrorMessage(err), Hint: "Grant Kanivet's AWS principal iam:GetRole."}}
		default:
			step.Status = StatusError
			step.Summary = "Failed to read the role"
			step.Problems = []Problem{{Code: "aws_error", Message: apiErrorMessage(err)}}
		}
		exp.Steps = append(exp.Steps, step)
		return
	}

	ec.bundle = bundle
	role := bundle.Info.Role
	exp.Role = &role
	step.Status = StatusOK
	step.Summary = role.Name
	if role.CrossAccount {
		step.Summary += fmt.Sprintf(" (account %s)", role.AccountID)
	}
	step.Details = []Detail{
		{Label: "ARN", Value: role.Arn, Mono: true, Link: role.ConsoleURL},
		{Label: "Account", Value: role.AccountID, Mono: true},
	}
	if role.Path != "" && role.Path != "/" {
		step.Details = append(step.Details, Detail{Label: "Path", Value: role.Path, Mono: true})
	}
	if role.MaxSessionDuration > 0 {
		step.Details = append(step.Details, Detail{Label: "Max session", Value: (time.Duration(role.MaxSessionDuration) * time.Second).String()})
	}
	if role.LastUsedAt != nil {
		value := role.LastUsedAt.UTC().Format(time.RFC3339)
		if role.LastUsedRegion != "" {
			value += " in " + role.LastUsedRegion
		}
		step.Details = append(step.Details, Detail{Label: "Last used", Value: value})
	} else {
		step.Details = append(step.Details, Detail{Label: "Last used", Value: "Never (within tracking period)"})
	}
	if role.PermissionsBoundaryArn != "" {
		step.Details = append(step.Details, Detail{Label: "Permissions boundary", Value: role.PermissionsBoundaryArn, Mono: true, Link: policyConsoleURL(role.PermissionsBoundaryArn)})
	}
	if role.CreatedAt != nil {
		step.Details = append(step.Details, Detail{Label: "Created", Value: role.CreatedAt.UTC().Format(time.RFC3339)})
	}
	exp.Steps = append(exp.Steps, step)
}

func (s *Service) trustStep(ctx context.Context, ec *explainContext) {
	exp := ec.exp
	step := Step{ID: StepTrust, Title: "Trust policy"}
	if ec.bundle == nil {
		step.Status = StatusSkipped
		step.Summary = "Skipped: role not available"
		exp.Steps = append(exp.Steps, step)
		return
	}

	trust := &TrustPolicy{Document: PrettyPolicyDocument(ec.bundle.Info.TrustDoc), MatchedStatementIndex: -1, Verdict: StatusUnknown}
	exp.Trust = trust
	doc, err := ParsePolicyDocument(ec.bundle.Info.TrustDoc)
	if err != nil {
		trust.Verdict = StatusError
		trust.Problems = []Problem{{Code: "trust_policy_invalid", Message: err.Error()}}
		finishTrustStep(exp, &step, trust, "Trust policy could not be parsed")
		return
	}

	switch exp.Mechanism {
	case MechanismPodIdentity:
		result := EvaluatePodIdentityTrust(doc)
		result.Document = trust.Document
		exp.Trust = result
		finishTrustStep(exp, &step, result, "Trusts pods.eks.amazonaws.com")
		return
	case MechanismIRSA:
		s.irsaTrust(ctx, ec, &step, trust, doc)
		return
	}
	finishTrustStep(exp, &step, trust, "Unknown identity mechanism")
}

func (s *Service) irsaTrust(ctx context.Context, ec *explainContext, step *Step, trust *TrustPolicy, doc *PolicyDocument) {
	exp := ec.exp
	trust.Statements = toTrustStatements(doc)

	if ec.creds.EKSClusterName == "" {
		trust.Problems = []Problem{{
			Code:    "eks_cluster_unknown",
			Message: "The EKS cluster name for this context is unknown, so the expected OIDC provider cannot be derived.",
			Hint:    "Import the cluster through Kanivet or use a kubeconfig generated by `aws eks update-kubeconfig`.",
		}}
		finishTrustStep(exp, step, trust, "Cannot derive the cluster's OIDC provider")
		return
	}

	issuer, err := s.oidcIssuer(ctx, ec.cfg, exp.Cluster, ec.creds.EKSClusterName, ec.creds.Profile)
	if err != nil {
		var notFound *ekstypes.ResourceNotFoundException
		switch {
		case errors.As(err, &notFound):
			trust.Problems = []Problem{{
				Code:     "eks_cluster_not_found",
				Message:  fmt.Sprintf("EKS cluster %q was not found in account %s / %s.", ec.creds.EKSClusterName, ec.creds.AccountID, ec.creds.Region),
				Expected: ec.creds.EKSClusterName,
				Hint:     "Check the AWS profile and region configured for this cluster.",
			}}
		case isAccessDenied(err):
			s.notePermissionError(exp, err)
			trust.Problems = []Problem{{Code: "operator_permission_denied", Message: apiErrorMessage(err), Hint: "Grant Kanivet's AWS principal eks:DescribeCluster."}}
		default:
			trust.Problems = []Problem{{Code: "aws_error", Message: apiErrorMessage(err)}}
		}
		finishTrustStep(exp, step, trust, "Could not look up the cluster's OIDC issuer")
		return
	}
	if issuer == "" {
		trust.Problems = []Problem{{
			Code:    "oidc_issuer_missing",
			Message: "The EKS cluster has no OIDC issuer; IRSA cannot work on it.",
			Hint:    "Enable the OIDC identity provider on the cluster.",
		}}
		finishTrustStep(exp, step, trust, "Cluster has no OIDC issuer")
		return
	}

	issuerHostPath := strings.TrimPrefix(strings.TrimPrefix(issuer, "https://"), "http://")
	partition := "aws"
	if parts := strings.SplitN(exp.Binding.RoleArn, ":", 3); len(parts) >= 2 && parts[1] != "" {
		partition = parts[1]
	}
	providerArn := fmt.Sprintf("arn:%s:iam::%s:oidc-provider/%s", partition, ec.roleAccount, issuerHostPath)

	result := EvaluateIRSATrust(doc, providerArn, issuerHostPath, exp.Namespace, exp.ServiceAccount, exp.Binding.Annotations[annotationAudience])
	result.Document = trust.Document
	result.OIDCIssuer = issuer
	result.OIDCProviderArn = providerArn

	iamClient := iam.NewFromConfig(ec.roleCfg)
	exists, err := oidcProviderExists(ctx, iamClient, providerArn)
	switch {
	case err == nil:
		result.OIDCProviderExists = exists
		if exists != nil && !*exists {
			result.Verdict = StatusError
			result.Problems = append([]Problem{{
				Code:     "oidc_provider_not_registered",
				Message:  fmt.Sprintf("This cluster's OIDC provider is not registered in IAM account %s, so no role in that account can trust it.", ec.roleAccount),
				Expected: providerArn,
				Hint:     fmt.Sprintf("Run `eksctl utils associate-iam-oidc-provider --cluster %s --approve` or create the provider in IAM.", ec.creds.EKSClusterName),
			}}, result.Problems...)
		}
	case isAccessDenied(err):
		s.notePermissionError(exp, err)
	}

	exp.Trust = result
	summary := "Trust policy allows this service account"
	if result.MatchedStatementIndex >= 0 {
		summary = fmt.Sprintf("Statement #%d trusts system:serviceaccount:%s:%s", result.MatchedStatementIndex+1, exp.Namespace, exp.ServiceAccount)
	}
	step.Details = []Detail{
		{Label: "OIDC issuer", Value: issuer, Mono: true},
		{Label: "OIDC provider", Value: providerArn, Mono: true},
	}
	finishTrustStep(exp, step, result, summary)
}

func finishTrustStep(exp *Explanation, step *Step, trust *TrustPolicy, okSummary string) {
	step.Status = trust.Verdict
	step.Problems = trust.Problems
	if len(trust.Problems) > 0 && trust.Verdict != StatusOK {
		step.Summary = trust.Problems[0].Message
	} else {
		step.Summary = okSummary
	}
	exp.Steps = append(exp.Steps, *step)
}

func (s *Service) permissionsStep(ctx context.Context, ec *explainContext) {
	exp := ec.exp
	step := Step{ID: StepPermissions, Title: "Permissions"}
	if ec.bundle == nil {
		step.Status = StatusSkipped
		step.Summary = "Skipped: role not available"
		exp.Steps = append(exp.Steps, step)
		return
	}

	iamClient := iam.NewFromConfig(ec.roleCfg)
	policies, err := s.rolePolicies(ctx, iamClient, ec.bundle, ec.roleProfile)
	if err != nil {
		if isAccessDenied(err) {
			s.notePermissionError(exp, err)
			step.Status = StatusUnknown
			step.Summary = "Not allowed to read the role's policies"
			step.Problems = []Problem{{Code: "operator_permission_denied", Message: apiErrorMessage(err), Hint: "Grant Kanivet's AWS principal the iam:List*/Get* policy permissions."}}
		} else {
			step.Status = StatusError
			step.Summary = "Failed to read the role's policies"
			step.Problems = []Problem{{Code: "aws_error", Message: apiErrorMessage(err)}}
		}
		exp.Steps = append(exp.Steps, step)
		return
	}
	exp.Policies = policies

	var managed, inline int
	hasBoundary := false
	for _, p := range policies {
		switch p.Type {
		case PolicyTypeManaged:
			managed++
		case PolicyTypeInline:
			inline++
		case PolicyTypeBoundary:
			hasBoundary = true
		}
	}
	step.Status = StatusOK
	step.Summary = fmt.Sprintf("%d managed, %d inline", managed, inline)
	if hasBoundary {
		step.Summary += ", permissions boundary"
	}
	step.Details = []Detail{
		{Label: "Managed policies", Value: fmt.Sprint(managed)},
		{Label: "Inline policies", Value: fmt.Sprint(inline)},
	}
	if managed+inline == 0 {
		step.Status = StatusWarning
		step.Problems = append(step.Problems, Problem{
			Code:    "no_policies",
			Message: "The role has no permission policies; every AWS call from this pod will be denied.",
			Hint:    "Attach a managed policy or add an inline policy to the role.",
		})
	}

	if ec.pod != nil {
		checks := DeriveChecks(ec.pod, ec.creds.Region, ec.roleAccount)
		if len(checks) > 0 {
			checks = simulateChecks(ctx, iamClient, ec.bundle.Info.Role.Arn, checks, policies, ec.bundle.Info.Role.PermissionsBoundaryArn)
			exp.Checks = checks
			denied, errored := 0, 0
			for _, c := range checks {
				switch c.Decision {
				case DecisionExplicitDeny, DecisionImplicitDeny:
					denied++
				case DecisionError:
					errored++
				}
			}
			step.Details = append(step.Details, Detail{Label: "Inferred checks", Value: fmt.Sprintf("%d (%d denied)", len(checks), denied)})
			if denied > 0 {
				if step.Status == StatusOK {
					step.Status = StatusWarning
				}
				step.Summary += fmt.Sprintf(" · %d of %d inferred checks denied", denied, len(checks))
			}
			if errored == len(checks) && strings.Contains(checks[0].Error, "SimulatePrincipalPolicy") {
				exp.PermissionError = firstNonEmpty(exp.PermissionError, checks[0].Error)
				exp.RequiredPermissions = RequiredIAMPermissions
			}
		}
	}
	exp.Steps = append(exp.Steps, step)
}

func (s *Service) notePermissionError(exp *Explanation, err error) {
	if exp.PermissionError == "" {
		exp.PermissionError = apiErrorMessage(err)
	}
	exp.RequiredPermissions = RequiredIAMPermissions
}

func overallStatus(steps []Step) Status {
	overall := StatusOK
	rank := map[Status]int{StatusOK: 0, StatusUnknown: 1, StatusWarning: 2, StatusError: 3}
	for _, step := range steps {
		if step.Status == StatusSkipped {
			continue
		}
		if rank[step.Status] > rank[overall] {
			overall = step.Status
		}
	}
	return overall
}

func mechanismLabel(m Mechanism) string {
	switch m {
	case MechanismIRSA:
		return "IRSA"
	case MechanismPodIdentity:
		return "EKS Pod Identity"
	default:
		return "none"
	}
}

func (s *Service) Simulate(ctx context.Context, cluster string, req SimulateRequest) (*SimulateResponse, error) {
	if strings.TrimSpace(req.RoleArn) == "" {
		return nil, fmt.Errorf("%w: roleArn is required", ErrBadRequest)
	}
	if len(req.Checks) == 0 {
		return nil, fmt.Errorf("%w: at least one check is required", ErrBadRequest)
	}
	creds, cfg, err := s.resolver.Resolve(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("aws credentials: %s", creds.Error)
	}
	roleAccount := accountFromArn(req.RoleArn)
	roleCfg, profile, err := s.resolver.ConfigForAccount(ctx, cfg, creds.AccountID, roleAccount, creds.Region)
	if err != nil {
		return nil, err
	}
	iamClient := iam.NewFromConfig(roleCfg)
	bundle, err := s.roleBundle(ctx, iamClient, req.RoleArn, firstNonEmpty(profile, creds.Profile), creds.AccountID)
	if err != nil {
		return nil, fmt.Errorf("iam role: %s", apiErrorMessage(err))
	}
	policies, _ := s.rolePolicies(ctx, iamClient, bundle, firstNonEmpty(profile, creds.Profile))

	checks := make([]Check, 0, len(req.Checks))
	for _, c := range req.Checks {
		if strings.TrimSpace(c.Action) == "" {
			return nil, fmt.Errorf("%w: check action is required", ErrBadRequest)
		}
		checks = append(checks, manualCheck(c.Action, c.Resource))
	}
	checks = simulateChecks(ctx, iamClient, bundle.Info.Role.Arn, checks, policies, bundle.Info.Role.PermissionsBoundaryArn)
	return &SimulateResponse{Checks: checks}, nil
}

func (s *Service) ListIdentities(ctx context.Context, cluster string) (*IdentitiesResponse, error) {
	creds, cfg, credErr := s.resolver.Resolve(ctx, cluster)
	key := s.cache.BuildKey("awsidentity-list", cluster, creds.Profile)
	value, err := s.cache.GetOrSet(key, identitiesCacheTTL, func() (interface{}, error) {
		return s.buildIdentities(ctx, cluster, creds, cfg, credErr)
	})
	if err != nil {
		return nil, err
	}
	return value.(*IdentitiesResponse), nil
}

func (s *Service) buildIdentities(ctx context.Context, cluster string, creds Credentials, cfg aws.Config, credErr error) (*IdentitiesResponse, error) {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	resp := &IdentitiesResponse{Credentials: creds, Identities: []IdentitySummary{}, GeneratedAt: time.Now()}
	if credErr != nil && resp.Credentials.Error == "" {
		resp.Credentials.Error = credErr.Error()
	}

	saList, err := clientset.CoreV1().ServiceAccounts("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range saList.Items {
		if binding := bindingFromServiceAccount(&saList.Items[i]); binding != nil {
			resp.Identities = append(resp.Identities, IdentitySummary{
				Namespace:      saList.Items[i].Namespace,
				ServiceAccount: saList.Items[i].Name,
				Mechanism:      MechanismIRSA,
				RoleArn:        binding.RoleArn,
				RoleName:       roleNameFromArn(binding.RoleArn),
				AccountID:      accountFromArn(binding.RoleArn),
				TrustStatus:    StatusUnknown,
			})
		}
	}

	podCounts := map[string]int{}
	if pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, pod := range pods.Items {
			podCounts[pod.Namespace+"/"+firstNonEmpty(pod.Spec.ServiceAccountName, "default")]++
		}
	}

	if credErr == nil && creds.EKSClusterName != "" {
		associations, err := s.listPodIdentityAssociations(ctx, cfg, creds.EKSClusterName)
		switch {
		case err == nil:
			resp.Identities = append(resp.Identities, associations...)
		case isAccessDenied(err):
			resp.PermissionError = apiErrorMessage(err)
			resp.RequiredPermissions = RequiredIAMPermissions
		}
	}

	for i := range resp.Identities {
		id := &resp.Identities[i]
		id.PodCount = podCounts[id.Namespace+"/"+id.ServiceAccount]
		id.CrossAccount = creds.AccountID != "" && id.AccountID != "" && id.AccountID != creds.AccountID
	}

	if credErr != nil {
		for i := range resp.Identities {
			resp.Identities[i].TrustMessage = "AWS credentials unavailable: " + creds.Error
		}
	} else {
		s.evaluateIdentities(ctx, cluster, creds, cfg, resp)
	}

	sort.SliceStable(resp.Identities, func(i, j int) bool {
		a, b := resp.Identities[i], resp.Identities[j]
		if ra, rb := trustRank(a.TrustStatus), trustRank(b.TrustStatus); ra != rb {
			return ra < rb
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.ServiceAccount < b.ServiceAccount
	})

	for _, id := range resp.Identities {
		resp.Totals.Total++
		switch id.Mechanism {
		case MechanismIRSA:
			resp.Totals.IRSA++
		case MechanismPodIdentity:
			resp.Totals.PodIdentity++
		}
		if id.TrustStatus == StatusError {
			resp.Totals.Broken++
		}
		if id.TrustStatus != StatusUnknown && (id.LastUsedAt == nil || time.Since(*id.LastUsedAt) > unusedRoleThreshold) {
			resp.Totals.Unused90d++
		}
	}
	return resp, nil
}

func trustRank(status Status) int {
	switch status {
	case StatusError:
		return 0
	case StatusWarning:
		return 1
	case StatusUnknown:
		return 2
	default:
		return 3
	}
}

func (s *Service) listPodIdentityAssociations(ctx context.Context, cfg aws.Config, clusterName string) ([]IdentitySummary, error) {
	client := eks.NewFromConfig(cfg)
	var summaries []ekstypes.PodIdentityAssociationSummary
	paginator := eks.NewListPodIdentityAssociationsPaginator(client, &eks.ListPodIdentityAssociationsInput{ClusterName: aws.String(clusterName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, page.Associations...)
	}

	results := make([]IdentitySummary, len(summaries))
	errs := make([]error, len(summaries))
	sem := make(chan struct{}, identityListConcurrency)
	var wg sync.WaitGroup
	for i, summary := range summaries {
		wg.Add(1)
		go func(i int, summary ekstypes.PodIdentityAssociationSummary) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := client.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{
				ClusterName:   aws.String(clusterName),
				AssociationId: summary.AssociationId,
			})
			if err != nil {
				errs[i] = err
				return
			}
			roleArn := ""
			if out.Association != nil {
				roleArn = aws.ToString(out.Association.RoleArn)
			}
			results[i] = IdentitySummary{
				Namespace:      aws.ToString(summary.Namespace),
				ServiceAccount: aws.ToString(summary.ServiceAccount),
				Mechanism:      MechanismPodIdentity,
				RoleArn:        roleArn,
				RoleName:       roleNameFromArn(roleArn),
				AccountID:      accountFromArn(roleArn),
				TrustStatus:    StatusUnknown,
			}
		}(i, summary)
	}
	wg.Wait()

	out := make([]IdentitySummary, 0, len(results))
	for i, r := range results {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Service) evaluateIdentities(ctx context.Context, cluster string, creds Credentials, cfg aws.Config, resp *IdentitiesResponse) {
	var issuer string
	var issuerErr error
	if creds.EKSClusterName != "" {
		issuer, issuerErr = s.oidcIssuer(ctx, cfg, cluster, creds.EKSClusterName, creds.Profile)
	} else {
		issuerErr = errors.New("EKS cluster name unknown for this context")
	}
	if issuerErr != nil && isAccessDenied(issuerErr) {
		resp.PermissionError = firstNonEmpty(resp.PermissionError, apiErrorMessage(issuerErr))
		resp.RequiredPermissions = RequiredIAMPermissions
	}
	issuerHostPath := strings.TrimPrefix(strings.TrimPrefix(issuer, "https://"), "http://")

	type accountClient struct {
		client  *iam.Client
		profile string
		err     error
	}
	clients := map[string]*accountClient{}
	var clientsMu sync.Mutex
	clientFor := func(account string) *accountClient {
		clientsMu.Lock()
		defer clientsMu.Unlock()
		if c, ok := clients[account]; ok {
			return c
		}
		roleCfg, profile, err := s.resolver.ConfigForAccount(ctx, cfg, creds.AccountID, account, creds.Region)
		c := &accountClient{profile: firstNonEmpty(profile, creds.Profile), err: err}
		if err == nil {
			c.client = iam.NewFromConfig(roleCfg)
		}
		clients[account] = c
		return c
	}

	var permMu sync.Mutex
	notePermission := func(err error) {
		permMu.Lock()
		defer permMu.Unlock()
		resp.PermissionError = firstNonEmpty(resp.PermissionError, apiErrorMessage(err))
		resp.RequiredPermissions = RequiredIAMPermissions
	}

	sem := make(chan struct{}, identityListConcurrency)
	var wg sync.WaitGroup
	for i := range resp.Identities {
		wg.Add(1)
		go func(id *IdentitySummary) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ac := clientFor(id.AccountID)
			if ac.err != nil {
				id.TrustStatus = StatusUnknown
				id.TrustMessage = fmt.Sprintf("No AWS credentials for account %s", id.AccountID)
				return
			}
			bundle, err := s.roleBundle(ctx, ac.client, id.RoleArn, ac.profile, creds.AccountID)
			if err != nil {
				switch {
				case isNoSuchEntity(err):
					id.TrustStatus = StatusError
					id.TrustMessage = "IAM role does not exist"
				case isAccessDenied(err):
					notePermission(err)
					id.TrustStatus = StatusUnknown
					id.TrustMessage = "Not allowed to read the role"
				default:
					id.TrustStatus = StatusUnknown
					id.TrustMessage = apiErrorMessage(err)
				}
				return
			}
			id.LastUsedAt = bundle.Info.Role.LastUsedAt

			doc, err := ParsePolicyDocument(bundle.Info.TrustDoc)
			if err != nil {
				id.TrustStatus = StatusError
				id.TrustMessage = "Trust policy could not be parsed"
				return
			}
			var result *TrustPolicy
			switch id.Mechanism {
			case MechanismPodIdentity:
				result = EvaluatePodIdentityTrust(doc)
			default:
				if issuerErr != nil {
					id.TrustStatus = StatusUnknown
					id.TrustMessage = "Could not resolve the cluster's OIDC issuer: " + apiErrorMessage(issuerErr)
					return
				}
				partition := "aws"
				if parts := strings.SplitN(id.RoleArn, ":", 3); len(parts) >= 2 && parts[1] != "" {
					partition = parts[1]
				}
				providerArn := fmt.Sprintf("arn:%s:iam::%s:oidc-provider/%s", partition, id.AccountID, issuerHostPath)
				result = EvaluateIRSATrust(doc, providerArn, issuerHostPath, id.Namespace, id.ServiceAccount, "")
			}
			id.TrustStatus = result.Verdict
			if len(result.Problems) > 0 {
				id.TrustMessage = result.Problems[0].Message
			}
		}(&resp.Identities[i])
	}
	wg.Wait()
}
