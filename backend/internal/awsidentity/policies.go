package awsidentity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
)

const policyFetchConcurrency = 5

type roleInfo struct {
	Role     Role
	TrustDoc string
}

func roleNameFromArn(arn string) string {
	idx := strings.Index(arn, ":role/")
	if idx < 0 {
		return arn
	}
	rest := arn[idx+len(":role/"):]
	if slash := strings.LastIndex(rest, "/"); slash >= 0 {
		return rest[slash+1:]
	}
	return rest
}

func accountFromArn(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 5 {
		return ""
	}
	return parts[4]
}

func roleConsoleURL(name string) string {
	return "https://console.aws.amazon.com/iam/home#/roles/details/" + url.PathEscape(name)
}

func policyConsoleURL(arn string) string {
	return "https://console.aws.amazon.com/iam/home#/policies/details/" + url.PathEscape(arn)
}

func fetchRole(ctx context.Context, client *iam.Client, roleArn, callerAccount string) (*roleInfo, error) {
	out, err := client.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleNameFromArn(roleArn))})
	if err != nil {
		return nil, err
	}
	r := out.Role
	if r == nil {
		return nil, fmt.Errorf("role %s returned no data", roleArn)
	}
	account := accountFromArn(aws.ToString(r.Arn))
	info := &roleInfo{
		Role: Role{
			Arn:                aws.ToString(r.Arn),
			Name:               aws.ToString(r.RoleName),
			Path:               aws.ToString(r.Path),
			AccountID:          account,
			Description:        aws.ToString(r.Description),
			MaxSessionDuration: aws.ToInt32(r.MaxSessionDuration),
			CreatedAt:          r.CreateDate,
			ConsoleURL:         roleConsoleURL(aws.ToString(r.RoleName)),
			CrossAccount:       callerAccount != "" && account != "" && account != callerAccount,
		},
		TrustDoc: aws.ToString(r.AssumeRolePolicyDocument),
	}
	if r.RoleLastUsed != nil {
		info.Role.LastUsedAt = r.RoleLastUsed.LastUsedDate
		info.Role.LastUsedRegion = aws.ToString(r.RoleLastUsed.Region)
	}
	if r.PermissionsBoundary != nil {
		info.Role.PermissionsBoundaryArn = aws.ToString(r.PermissionsBoundary.PermissionsBoundaryArn)
	}
	return info, nil
}

func fetchPolicies(ctx context.Context, client *iam.Client, roleName, boundaryArn string) ([]Policy, error) {
	type job func() (*Policy, error)
	var jobs []job

	attached, err := listAttachedPolicies(ctx, client, roleName)
	if err != nil {
		return nil, err
	}
	for _, a := range attached {
		arn := aws.ToString(a.PolicyArn)
		name := aws.ToString(a.PolicyName)
		jobs = append(jobs, func() (*Policy, error) { return fetchManagedPolicy(ctx, client, arn, name, PolicyTypeManaged) })
	}

	inline, err := listInlinePolicies(ctx, client, roleName)
	if err != nil {
		return nil, err
	}
	for _, name := range inline {
		name := name
		jobs = append(jobs, func() (*Policy, error) {
			out, err := client.GetRolePolicy(ctx, &iam.GetRolePolicyInput{RoleName: aws.String(roleName), PolicyName: aws.String(name)})
			if err != nil {
				return nil, err
			}
			return buildPolicy(name, "", PolicyTypeInline, "", aws.ToString(out.PolicyDocument)), nil
		})
	}

	if boundaryArn != "" {
		jobs = append(jobs, func() (*Policy, error) {
			return fetchManagedPolicy(ctx, client, boundaryArn, roleNameFromPolicyArn(boundaryArn), PolicyTypeBoundary)
		})
	}

	results := make([]*Policy, len(jobs))
	errs := make([]error, len(jobs))
	sem := make(chan struct{}, policyFetchConcurrency)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], errs[i] = j()
		}(i, j)
	}
	wg.Wait()

	policies := make([]Policy, 0, len(results))
	for i, p := range results {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if p != nil {
			policies = append(policies, *p)
		}
	}
	sort.SliceStable(policies, func(i, j int) bool {
		if policies[i].Type != policies[j].Type {
			return policyTypeOrder(policies[i].Type) < policyTypeOrder(policies[j].Type)
		}
		return policies[i].Name < policies[j].Name
	})
	return policies, nil
}

func policyTypeOrder(t PolicyType) int {
	switch t {
	case PolicyTypeManaged:
		return 0
	case PolicyTypeInline:
		return 1
	default:
		return 2
	}
}

func listAttachedPolicies(ctx context.Context, client *iam.Client, roleName string) ([]iamtypes.AttachedPolicy, error) {
	var out []iamtypes.AttachedPolicy
	paginator := iam.NewListAttachedRolePoliciesPaginator(client, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(roleName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, page.AttachedPolicies...)
	}
	return out, nil
}

func listInlinePolicies(ctx context.Context, client *iam.Client, roleName string) ([]string, error) {
	var out []string
	paginator := iam.NewListRolePoliciesPaginator(client, &iam.ListRolePoliciesInput{RoleName: aws.String(roleName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, page.PolicyNames...)
	}
	return out, nil
}

func fetchManagedPolicy(ctx context.Context, client *iam.Client, arn, name string, policyType PolicyType) (*Policy, error) {
	meta, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(arn)})
	if err != nil {
		return nil, err
	}
	versionID := ""
	if meta.Policy != nil {
		versionID = aws.ToString(meta.Policy.DefaultVersionId)
		if name == "" {
			name = aws.ToString(meta.Policy.PolicyName)
		}
	}
	version, err := client.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{PolicyArn: aws.String(arn), VersionId: aws.String(versionID)})
	if err != nil {
		return nil, err
	}
	doc := ""
	if version.PolicyVersion != nil {
		doc = aws.ToString(version.PolicyVersion.Document)
	}
	return buildPolicy(name, arn, policyType, versionID, doc), nil
}

func roleNameFromPolicyArn(arn string) string {
	if idx := strings.LastIndex(arn, "/"); idx >= 0 {
		return arn[idx+1:]
	}
	return arn
}

func buildPolicy(name, arn string, policyType PolicyType, versionID, rawDoc string) *Policy {
	p := &Policy{
		Name:       name,
		Arn:        arn,
		Type:       policyType,
		AWSManaged: accountFromArn(arn) == "aws",
		VersionID:  versionID,
		Document:   PrettyPolicyDocument(rawDoc),
	}
	if arn != "" {
		p.ConsoleURL = policyConsoleURL(arn)
	}
	if doc, err := ParsePolicyDocument(rawDoc); err == nil {
		p.Statements = summarizeStatements(doc)
	}
	if p.Statements == nil {
		p.Statements = []PolicyStatement{}
	}
	return p
}

func summarizeStatements(doc *PolicyDocument) []PolicyStatement {
	out := make([]PolicyStatement, 0, len(doc.Statements))
	for i, s := range doc.Statements {
		out = append(out, PolicyStatement{
			Index:        i,
			Sid:          s.Sid,
			Effect:       s.Effect,
			Actions:      s.Action,
			NotActions:   s.NotAction,
			Resources:    s.Resource,
			NotResources: s.NotResource,
			HasCondition: len(s.Condition) > 0,
		})
	}
	return out
}

func oidcProviderExists(ctx context.Context, client *iam.Client, providerArn string) (*bool, error) {
	_, err := client.GetOpenIDConnectProvider(ctx, &iam.GetOpenIDConnectProviderInput{OpenIDConnectProviderArn: aws.String(providerArn)})
	if err == nil {
		return aws.Bool(true), nil
	}
	var notFound *iamtypes.NoSuchEntityException
	if errors.As(err, &notFound) {
		return aws.Bool(false), nil
	}
	return nil, err
}

func isNoSuchEntity(err error) bool {
	var notFound *iamtypes.NoSuchEntityException
	return errors.As(err, &notFound)
}

func isAccessDenied(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "UnauthorizedAccess", "UnauthorizedException":
		return true
	}
	return false
}

func apiErrorMessage(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorMessage()
	}
	return err.Error()
}
