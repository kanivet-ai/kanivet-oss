package k8s

import "testing"

func TestClassifyClusterErrorCloudAuthCases(t *testing.T) {
	cases := []struct {
		msg  string
		code string
	}{
		{"Error loading SSO Token: Token for kanivet-sso-abc does not exist", "aws_sso_expired"},
		{"The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login with the corresponding profile.", "aws_sso_expired"},
		{"Unable to locate credentials. You can configure credentials by running \"aws configure\".", "aws_credentials_missing"},
		{"The config profile (leapp-prod) could not be found", "aws_credentials_missing"},
		{"ExpiredTokenException: The security token included in the request is expired", "aws_token_expired"},
		{"exec: executable aws not found\n\nIt looks like you are trying to use a client-go credential plugin", "exec_missing"},
		{"getting credentials: exec: executable gke-gcloud-auth-plugin not found", "gcp_plugin_missing"},
		{"ERROR: (gcloud.auth.print-access-token) Reauthentication required.", "gcp_auth_expired"},
		{"AADSTS700082: The refresh token has expired due to inactivity.", "azure_auth_expired"},
		{"ERROR: Please run 'az login' to setup account.", "azure_auth_expired"},
		{"getting credentials: exec: executable kubelogin failed with exit code 1", "azure_kubelogin"},
		{"Get \"https://abc.eks.amazonaws.com/version?timeout=5s\": dial tcp: lookup abc: no such host", "connection_failed"},
	}
	for _, tc := range cases {
		code, _, ok := ClassifyClusterError(tc.msg)
		if !ok || code != tc.code {
			t.Errorf("%q → %q (ok=%v), want %q", tc.msg, code, ok, tc.code)
		}
	}
	for _, code := range []string{"aws_sso_expired", "aws_credentials_missing", "azure_auth_expired", "gcp_auth_expired", "exec_missing"} {
		if !IsAuthErrorCode(code) {
			t.Errorf("%s should be an auth error code", code)
		}
	}
	if IsAuthErrorCode("timeout") || IsAuthErrorCode("connection_failed") {
		t.Error("network codes must not count as auth errors")
	}
}
