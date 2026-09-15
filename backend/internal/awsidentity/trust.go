package awsidentity

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const (
	actionAssumeRoleWithWebIdentity = "sts:AssumeRoleWithWebIdentity"
	actionAssumeRole                = "sts:AssumeRole"
	actionTagSession                = "sts:TagSession"
	podIdentityServicePrincipal     = "pods.eks.amazonaws.com"
	defaultIRSAAudience             = "sts.amazonaws.com"
)

type PolicyDocument struct {
	Version    string
	Statements []RawStatement
}

type RawStatement struct {
	Sid          string
	Effect       string
	Principal    map[string][]string
	NotPrincipal map[string][]string
	Action       []string
	NotAction    []string
	Resource     []string
	NotResource  []string
	Condition    map[string]map[string][]string
}

type rawPolicyDocument struct {
	Version   string          `json:"Version"`
	Statement json.RawMessage `json:"Statement"`
}

type rawStatement struct {
	Sid          string          `json:"Sid"`
	Effect       string          `json:"Effect"`
	Principal    json.RawMessage `json:"Principal"`
	NotPrincipal json.RawMessage `json:"NotPrincipal"`
	Action       json.RawMessage `json:"Action"`
	NotAction    json.RawMessage `json:"NotAction"`
	Resource     json.RawMessage `json:"Resource"`
	NotResource  json.RawMessage `json:"NotResource"`
	Condition    json.RawMessage `json:"Condition"`
}

func ParsePolicyDocument(raw string) (*PolicyDocument, error) {
	decoded := DecodePolicyDocument(raw)
	var doc rawPolicyDocument
	if err := json.Unmarshal([]byte(decoded), &doc); err != nil {
		return nil, fmt.Errorf("invalid policy document: %w", err)
	}
	statements, err := parseStatements(doc.Statement)
	if err != nil {
		return nil, err
	}
	return &PolicyDocument{Version: doc.Version, Statements: statements}, nil
}

// IAM returns role trust documents URL-encoded; policy versions are usually plain JSON.
func DecodePolicyDocument(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	if decoded, err := url.QueryUnescape(trimmed); err == nil {
		return decoded
	}
	return trimmed
}

func PrettyPolicyDocument(raw string) string {
	decoded := DecodePolicyDocument(raw)
	var value interface{}
	if err := json.Unmarshal([]byte(decoded), &value); err != nil {
		return decoded
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return decoded
	}
	return string(pretty)
}

func parseStatements(raw json.RawMessage) ([]RawStatement, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var list []rawStatement
	if err := json.Unmarshal(raw, &list); err != nil {
		var single rawStatement
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, fmt.Errorf("invalid Statement: %w", err)
		}
		list = []rawStatement{single}
	}
	statements := make([]RawStatement, 0, len(list))
	for _, item := range list {
		statements = append(statements, RawStatement{
			Sid:          item.Sid,
			Effect:       item.Effect,
			Principal:    parsePrincipal(item.Principal),
			NotPrincipal: parsePrincipal(item.NotPrincipal),
			Action:       parseStringList(item.Action),
			NotAction:    parseStringList(item.NotAction),
			Resource:     parseStringList(item.Resource),
			NotResource:  parseStringList(item.NotResource),
			Condition:    parseCondition(item.Condition),
		})
	}
	return statements, nil
}

func parseStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var anyList []interface{}
	if err := json.Unmarshal(raw, &anyList); err == nil {
		out := make([]string, 0, len(anyList))
		for _, v := range anyList {
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	var anyValue interface{}
	if err := json.Unmarshal(raw, &anyValue); err == nil && anyValue != nil {
		return []string{fmt.Sprint(anyValue)}
	}
	return nil
}

func parsePrincipal(raw json.RawMessage) map[string][]string {
	if len(raw) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return map[string][]string{"*": {single}}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	out := make(map[string][]string, len(object))
	for key, value := range object {
		out[key] = parseStringList(value)
	}
	return out
}

func parseCondition(raw json.RawMessage) map[string]map[string][]string {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	out := make(map[string]map[string][]string, len(object))
	for operator, keys := range object {
		values := make(map[string][]string, len(keys))
		for key, value := range keys {
			values[key] = parseStringList(value)
		}
		out[operator] = values
	}
	return out
}

func (s RawStatement) allows() bool {
	return strings.EqualFold(s.Effect, "Allow")
}

func (s RawStatement) hasAction(action string) bool {
	for _, candidate := range s.Action {
		if matchesIAMPattern(candidate, action) {
			return true
		}
	}
	return false
}

func (s RawStatement) principals(kind string) []string {
	for key, values := range s.Principal {
		if strings.EqualFold(key, kind) {
			return values
		}
	}
	return nil
}

func (s RawStatement) conditionValues(operator, key string) []string {
	var out []string
	for op, keys := range s.Condition {
		if !strings.EqualFold(op, operator) {
			continue
		}
		for k, values := range keys {
			if strings.EqualFold(k, key) {
				out = append(out, values...)
			}
		}
	}
	return out
}

func (s RawStatement) hasConditionKey(key string) bool {
	for _, keys := range s.Condition {
		for k := range keys {
			if strings.EqualFold(k, key) {
				return true
			}
		}
	}
	return false
}

// IAM wildcards: '*' matches any run of characters (including '/'), '?' one character.
func matchesIAMPattern(pattern, value string) bool {
	p := []rune(strings.ToLower(pattern))
	v := []rune(strings.ToLower(value))
	pi, vi := 0, 0
	starP, starV := -1, -1
	for vi < len(v) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == v[vi]):
			pi++
			vi++
		case pi < len(p) && p[pi] == '*':
			starP, starV = pi, vi
			pi++
		case starP >= 0:
			starV++
			pi, vi = starP+1, starV
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func toTrustStatements(doc *PolicyDocument) []TrustStatement {
	out := make([]TrustStatement, 0, len(doc.Statements))
	for i, s := range doc.Statements {
		out = append(out, TrustStatement{
			Index:      i,
			Sid:        s.Sid,
			Effect:     s.Effect,
			Principal:  s.Principal,
			Actions:    s.Action,
			Conditions: s.Condition,
		})
	}
	return out
}

func EvaluateIRSATrust(doc *PolicyDocument, oidcProviderArn, issuerHostPath, namespace, serviceAccount, audience string) *TrustPolicy {
	if audience == "" {
		audience = defaultIRSAAudience
	}
	expectedSub := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, serviceAccount)
	subKey := issuerHostPath + ":sub"
	audKey := issuerHostPath + ":aud"

	result := &TrustPolicy{
		Statements:            toTrustStatements(doc),
		MatchedStatementIndex: -1,
		Verdict:               StatusError,
	}

	var (
		federatedSeen    []string
		providerMatched  bool
		actionMatched    bool
		subMismatches    []string
		subUnrestricted  bool
		audMissing       bool
		matchedStatement = -1
	)

	for i, s := range doc.Statements {
		federated := s.principals("Federated")
		if !s.allows() || len(federated) == 0 {
			continue
		}
		federatedSeen = appendUnique(federatedSeen, federated...)

		if !anyEqualFold(federated, oidcProviderArn) {
			continue
		}
		providerMatched = true

		if !s.hasAction(actionAssumeRoleWithWebIdentity) {
			continue
		}
		actionMatched = true

		equalsSubs := s.conditionValues("StringEquals", subKey)
		likeSubs := s.conditionValues("StringLike", subKey)
		subRestricted := len(equalsSubs) > 0 || len(likeSubs) > 0
		subOK := !subRestricted || anyEqualFold(equalsSubs, expectedSub) || anyGlobMatch(likeSubs, expectedSub)
		if !subOK {
			subMismatches = appendUnique(subMismatches, append(equalsSubs, likeSubs...)...)
			continue
		}

		result.Statements[i].Matches = true
		if matchedStatement == -1 {
			matchedStatement = i
			subUnrestricted = !subRestricted
			audMissing = !s.hasConditionKey(audKey)
		}
	}

	if matchedStatement >= 0 {
		result.MatchedStatementIndex = matchedStatement
		result.Verdict = StatusOK
		if subUnrestricted {
			result.Verdict = StatusWarning
			result.Problems = append(result.Problems, Problem{
				Code:     "sub_unrestricted",
				Message:  "The trust policy does not restrict which service account may assume this role; any pod in the cluster can use it.",
				Expected: fmt.Sprintf(`"%s": "%s"`, subKey, expectedSub),
				Hint:     "Add a StringEquals condition on the OIDC :sub claim scoped to this namespace and service account.",
			})
		}
		if audMissing {
			result.Verdict = StatusWarning
			result.Problems = append(result.Problems, Problem{
				Code:     "aud_missing",
				Message:  "The trust policy does not check the token audience.",
				Expected: fmt.Sprintf(`"%s": "%s"`, audKey, audience),
				Hint:     "Add a StringEquals condition on the OIDC :aud claim.",
			})
		}
		return result
	}

	switch {
	case len(federatedSeen) == 0:
		result.Problems = append(result.Problems, Problem{
			Code:     "no_federated_statement",
			Message:  "The trust policy has no statement allowing a federated (OIDC) principal to assume this role.",
			Expected: oidcProviderArn,
			Hint:     "Add a statement with Principal.Federated set to the cluster's OIDC provider and Action sts:AssumeRoleWithWebIdentity.",
		})
	case !providerMatched:
		result.Problems = append(result.Problems, Problem{
			Code:     "oidc_provider_mismatch",
			Message:  "The trust policy trusts a different OIDC provider than this cluster's. The role was likely created for another cluster.",
			Expected: oidcProviderArn,
			Actual:   strings.Join(federatedSeen, ", "),
			Hint:     "Update Principal.Federated to this cluster's OIDC provider ARN.",
		})
	case !actionMatched:
		result.Problems = append(result.Problems, Problem{
			Code:     "action_missing",
			Message:  "The statement trusting this cluster's OIDC provider does not allow sts:AssumeRoleWithWebIdentity.",
			Expected: actionAssumeRoleWithWebIdentity,
			Hint:     "Set Action to sts:AssumeRoleWithWebIdentity on the federated statement.",
		})
	default:
		sort.Strings(subMismatches)
		result.Problems = append(result.Problems, Problem{
			Code:     "sub_mismatch",
			Message:  "The trust policy allows a different service account than the one bound to this role.",
			Expected: expectedSub,
			Actual:   strings.Join(subMismatches, ", "),
			Hint:     "Fix the namespace/service account in the :sub condition, or annotate the correct service account.",
		})
	}
	return result
}

func EvaluatePodIdentityTrust(doc *PolicyDocument) *TrustPolicy {
	result := &TrustPolicy{
		Statements:            toTrustStatements(doc),
		MatchedStatementIndex: -1,
		Verdict:               StatusError,
	}

	var (
		serviceSeen       bool
		assumeRoleMissing bool
		tagSessionMissing bool
	)

	for i, s := range doc.Statements {
		if !s.allows() || !anyEqualFold(s.principals("Service"), podIdentityServicePrincipal) {
			continue
		}
		serviceSeen = true
		hasAssume := s.hasAction(actionAssumeRole)
		hasTag := s.hasAction(actionTagSession)
		if hasAssume && hasTag {
			result.Statements[i].Matches = true
			if result.MatchedStatementIndex == -1 {
				result.MatchedStatementIndex = i
			}
			continue
		}
		assumeRoleMissing = assumeRoleMissing || !hasAssume
		tagSessionMissing = tagSessionMissing || !hasTag
	}

	if result.MatchedStatementIndex >= 0 {
		result.Verdict = StatusOK
		return result
	}

	switch {
	case !serviceSeen:
		result.Problems = append(result.Problems, Problem{
			Code:     "no_pod_identity_statement",
			Message:  "The trust policy does not allow the EKS Pod Identity service principal to assume this role.",
			Expected: podIdentityServicePrincipal,
			Hint:     "Add a statement with Principal.Service pods.eks.amazonaws.com and Actions sts:AssumeRole and sts:TagSession.",
		})
	case tagSessionMissing && !assumeRoleMissing:
		result.Problems = append(result.Problems, Problem{
			Code:     "tag_session_missing",
			Message:  "The Pod Identity statement allows sts:AssumeRole but not sts:TagSession, which EKS Pod Identity requires.",
			Expected: actionTagSession,
			Hint:     "Add sts:TagSession to the statement's Action list.",
		})
	default:
		result.Problems = append(result.Problems, Problem{
			Code:     "action_missing",
			Message:  "The Pod Identity statement is missing required actions.",
			Expected: actionAssumeRole + ", " + actionTagSession,
			Hint:     "Set Action to [sts:AssumeRole, sts:TagSession] on the pods.eks.amazonaws.com statement.",
		})
	}
	return result
}

func anyEqualFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}

func anyGlobMatch(patterns []string, target string) bool {
	for _, p := range patterns {
		if matchesIAMPattern(p, target) {
			return true
		}
	}
	return false
}

func appendUnique(list []string, values ...string) []string {
	for _, v := range values {
		if !contains(list, v) {
			list = append(list, v)
		}
	}
	return list
}
