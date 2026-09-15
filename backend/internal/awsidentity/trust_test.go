package awsidentity

import (
	"net/url"
	"testing"
)

const (
	testIssuer   = "oidc.eks.eu-west-1.amazonaws.com/id/ABCDEF1234567890"
	testProvider = "arn:aws:iam::123456789012:oidc-provider/" + testIssuer
)

func irsaTrustDoc(provider, sub, op string, withAud bool) string {
	aud := ""
	if withAud {
		aud = `, "` + testIssuer + `:aud": "sts.amazonaws.com"`
	}
	return `{
	  "Version": "2012-10-17",
	  "Statement": [{
	    "Effect": "Allow",
	    "Principal": {"Federated": "` + provider + `"},
	    "Action": "sts:AssumeRoleWithWebIdentity",
	    "Condition": {"` + op + `": {"` + testIssuer + `:sub": "` + sub + `"` + aud + `}}
	  }]
	}`
}

func mustParse(t *testing.T, raw string) *PolicyDocument {
	t.Helper()
	doc, err := ParsePolicyDocument(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func firstProblemCode(tp *TrustPolicy) string {
	if len(tp.Problems) == 0 {
		return ""
	}
	return tp.Problems[0].Code
}

func TestEvaluateIRSATrust(t *testing.T) {
	tests := []struct {
		name         string
		doc          string
		namespace    string
		sa           string
		wantVerdict  Status
		wantCode     string
		wantExpected string
		wantActual   string
		wantMatched  int
	}{
		{
			name:        "exact match ok",
			doc:         irsaTrustDoc(testProvider, "system:serviceaccount:payments:api", "StringEquals", true),
			namespace:   "payments",
			sa:          "api",
			wantVerdict: StatusOK,
			wantMatched: 0,
		},
		{
			name:         "sub mismatch",
			doc:          irsaTrustDoc(testProvider, "system:serviceaccount:payments:api", "StringEquals", true),
			namespace:    "payments",
			sa:           "api-v2",
			wantVerdict:  StatusError,
			wantCode:     "sub_mismatch",
			wantExpected: "system:serviceaccount:payments:api-v2",
			wantActual:   "system:serviceaccount:payments:api",
			wantMatched:  -1,
		},
		{
			name:        "StringLike wildcard ok",
			doc:         irsaTrustDoc(testProvider, "system:serviceaccount:payments:*", "StringLike", true),
			namespace:   "payments",
			sa:          "anything",
			wantVerdict: StatusOK,
			wantMatched: 0,
		},
		{
			name:         "wrong OIDC provider",
			doc:          irsaTrustDoc("arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/OTHERCLUSTER", "system:serviceaccount:payments:api", "StringEquals", true),
			namespace:    "payments",
			sa:           "api",
			wantVerdict:  StatusError,
			wantCode:     "oidc_provider_mismatch",
			wantExpected: testProvider,
			wantActual:   "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/OTHERCLUSTER",
			wantMatched:  -1,
		},
		{
			name:        "aud missing is a warning",
			doc:         irsaTrustDoc(testProvider, "system:serviceaccount:payments:api", "StringEquals", false),
			namespace:   "payments",
			sa:          "api",
			wantVerdict: StatusWarning,
			wantCode:    "aud_missing",
			wantMatched: 0,
		},
		{
			name:        "no federated statement",
			doc:         `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}}`,
			namespace:   "payments",
			sa:          "api",
			wantVerdict: StatusError,
			wantCode:    "no_federated_statement",
			wantMatched: -1,
		},
		{
			name:        "action missing",
			doc:         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Federated":"` + testProvider + `"},"Action":"sts:AssumeRole"}]}`,
			namespace:   "payments",
			sa:          "api",
			wantVerdict: StatusError,
			wantCode:    "action_missing",
			wantMatched: -1,
		},
		{
			name:        "url encoded document with array conditions",
			doc:         url.QueryEscape(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Federated":["` + testProvider + `"]},"Action":["sts:AssumeRoleWithWebIdentity"],"Condition":{"StringEquals":{"` + testIssuer + `:sub":["system:serviceaccount:a:b","system:serviceaccount:payments:api"],"` + testIssuer + `:aud":"sts.amazonaws.com"}}}]}`),
			namespace:   "payments",
			sa:          "api",
			wantVerdict: StatusOK,
			wantMatched: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParse(t, tc.doc)
			result := EvaluateIRSATrust(doc, testProvider, testIssuer, tc.namespace, tc.sa, "")
			if result.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %s, want %s (problems: %+v)", result.Verdict, tc.wantVerdict, result.Problems)
			}
			if result.MatchedStatementIndex != tc.wantMatched {
				t.Fatalf("matched = %d, want %d", result.MatchedStatementIndex, tc.wantMatched)
			}
			if tc.wantCode != "" {
				if got := firstProblemCode(result); got != tc.wantCode {
					t.Fatalf("problem code = %q, want %q", got, tc.wantCode)
				}
				if tc.wantExpected != "" && result.Problems[0].Expected != tc.wantExpected {
					t.Fatalf("expected = %q, want %q", result.Problems[0].Expected, tc.wantExpected)
				}
				if tc.wantActual != "" && result.Problems[0].Actual != tc.wantActual {
					t.Fatalf("actual = %q, want %q", result.Problems[0].Actual, tc.wantActual)
				}
			}
			if tc.wantMatched >= 0 && !result.Statements[tc.wantMatched].Matches {
				t.Fatalf("statement %d not flagged as matching", tc.wantMatched)
			}
		})
	}
}

func TestEvaluatePodIdentityTrust(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		wantVerdict Status
		wantCode    string
	}{
		{
			name:        "ok",
			doc:         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"pods.eks.amazonaws.com"},"Action":["sts:AssumeRole","sts:TagSession"]}]}`,
			wantVerdict: StatusOK,
		},
		{
			name:        "tag session missing",
			doc:         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"pods.eks.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
			wantVerdict: StatusError,
			wantCode:    "tag_session_missing",
		},
		{
			name:        "no pod identity statement",
			doc:         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
			wantVerdict: StatusError,
			wantCode:    "no_pod_identity_statement",
		},
		{
			name:        "wildcard action ok",
			doc:         `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":["pods.eks.amazonaws.com"]},"Action":"sts:*"}]}`,
			wantVerdict: StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := EvaluatePodIdentityTrust(mustParse(t, tc.doc))
			if result.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %s, want %s (problems: %+v)", result.Verdict, tc.wantVerdict, result.Problems)
			}
			if got := firstProblemCode(result); got != tc.wantCode {
				t.Fatalf("problem code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestMatchesIAMPattern(t *testing.T) {
	tests := []struct {
		pattern, value string
		want           bool
	}{
		{"sts:AssumeRoleWithWebIdentity", "sts:assumerolewithwebidentity", true},
		{"sts:*", "sts:TagSession", true},
		{"*", "anything", true},
		{"system:serviceaccount:payments:*", "system:serviceaccount:payments:api", true},
		{"system:serviceaccount:payments:*", "system:serviceaccount:billing:api", false},
		{"arn:aws:s3:::bucket/*", "arn:aws:s3:::bucket/a/b/c", true},
		{"system:serviceaccount:payments:api-?", "system:serviceaccount:payments:api-1", true},
		{"system:serviceaccount:payments:api-?", "system:serviceaccount:payments:api-10", false},
	}
	for _, tc := range tests {
		if got := matchesIAMPattern(tc.pattern, tc.value); got != tc.want {
			t.Errorf("matchesIAMPattern(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}
