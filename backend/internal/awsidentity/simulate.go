package awsidentity

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type policyIndex struct {
	byArn  map[string]*Policy
	byName map[string]*Policy
}

func indexPolicies(policies []Policy) *policyIndex {
	idx := &policyIndex{byArn: map[string]*Policy{}, byName: map[string]*Policy{}}
	for i := range policies {
		p := &policies[i]
		if p.Arn != "" {
			idx.byArn[strings.ToLower(p.Arn)] = p
		}
		idx.byName[strings.ToLower(p.Name)] = p
	}
	return idx
}

func (idx *policyIndex) lookup(id string) *Policy {
	if idx == nil {
		return nil
	}
	key := strings.ToLower(id)
	if p, ok := idx.byArn[key]; ok {
		return p
	}
	if p, ok := idx.byName[key]; ok {
		return p
	}
	if p, ok := idx.byName[strings.ToLower(roleNameFromPolicyArn(id))]; ok {
		return p
	}
	return nil
}

func simulateChecks(ctx context.Context, client *iam.Client, roleArn string, checks []Check, policies []Policy, boundaryArn string) []Check {
	if len(checks) == 0 {
		return checks
	}
	idx := indexPolicies(policies)

	groups := map[string][]int{}
	var order []string
	for i, c := range checks {
		if _, ok := groups[c.Resource]; !ok {
			order = append(order, c.Resource)
		}
		groups[c.Resource] = append(groups[c.Resource], i)
	}

	for _, resource := range order {
		members := groups[resource]
		actions := make([]string, 0, len(members))
		seen := map[string]bool{}
		for _, i := range members {
			key := strings.ToLower(checks[i].Action)
			if !seen[key] {
				seen[key] = true
				actions = append(actions, checks[i].Action)
			}
		}

		results, err := runSimulation(ctx, client, roleArn, actions, resource)
		if err != nil {
			msg := apiErrorMessage(err)
			if isAccessDenied(err) {
				msg = "Kanivet's AWS credentials are not allowed to call iam:SimulatePrincipalPolicy: " + msg
			}
			for _, i := range members {
				checks[i].Decision = DecisionError
				checks[i].Error = msg
			}
			continue
		}

		byAction := map[string]iamtypes.EvaluationResult{}
		for _, r := range results {
			byAction[strings.ToLower(aws.ToString(r.EvalActionName))] = r
		}
		for _, i := range members {
			r, ok := byAction[strings.ToLower(checks[i].Action)]
			if !ok {
				checks[i].Decision = DecisionError
				checks[i].Error = "No simulation result returned for this action"
				continue
			}
			applyEvaluation(&checks[i], r, idx, boundaryArn)
		}
	}
	return checks
}

func runSimulation(ctx context.Context, client *iam.Client, roleArn string, actions []string, resource string) ([]iamtypes.EvaluationResult, error) {
	input := &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(roleArn),
		ActionNames:     actions,
		ResourceArns:    []string{resource},
	}
	var results []iamtypes.EvaluationResult
	for {
		out, err := client.SimulatePrincipalPolicy(ctx, input)
		if err != nil {
			return nil, err
		}
		results = append(results, out.EvaluationResults...)
		if !out.IsTruncated || out.Marker == nil {
			return results, nil
		}
		input.Marker = out.Marker
	}
}

func applyEvaluation(check *Check, r iamtypes.EvaluationResult, idx *policyIndex, boundaryArn string) {
	switch r.EvalDecision {
	case iamtypes.PolicyEvaluationDecisionTypeAllowed:
		check.Decision = DecisionAllowed
	case iamtypes.PolicyEvaluationDecisionTypeExplicitDeny:
		check.Decision = DecisionExplicitDeny
	default:
		check.Decision = DecisionImplicitDeny
	}
	check.MissingContextKeys = r.MissingContextValues
	if r.OrganizationsDecisionDetail != nil {
		check.OrgDecision = boolDecision(r.OrganizationsDecisionDetail.AllowedByOrganizations)
	}
	if r.PermissionsBoundaryDecisionDetail != nil {
		check.BoundaryDecision = boolDecision(r.PermissionsBoundaryDecisionDetail.AllowedByPermissionsBoundary)
	}
	check.MatchedStatements = nil
	for _, s := range r.MatchedStatements {
		id := aws.ToString(s.SourcePolicyId)
		matched := MatchedStatement{
			PolicyName:     id,
			PolicyType:     policyTypeFromSource(s.SourcePolicyType, id, boundaryArn),
			StatementIndex: -1,
		}
		if p := idx.lookup(id); p != nil {
			matched.PolicyName = p.Name
			matched.PolicyArn = p.Arn
			matched.PolicyType = p.Type
		}
		if s.StartPosition != nil {
			matched.StartLine = int(s.StartPosition.Line)
		}
		if s.EndPosition != nil {
			matched.EndLine = int(s.EndPosition.Line)
		}
		check.MatchedStatements = append(check.MatchedStatements, matched)
	}
}

func policyTypeFromSource(source iamtypes.PolicySourceType, id, boundaryArn string) PolicyType {
	if boundaryArn != "" && strings.EqualFold(id, boundaryArn) {
		return PolicyTypeBoundary
	}
	switch source {
	case iamtypes.PolicySourceTypeAwsManaged, iamtypes.PolicySourceTypeUserManaged:
		return PolicyTypeManaged
	default:
		return PolicyTypeInline
	}
}

func boolDecision(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "denied"
}
