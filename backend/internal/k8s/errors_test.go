package k8s

import "testing"

func TestClassifyClusterError(t *testing.T) {
	cases := []struct {
		name     string
		err      string
		wantCode string
		wantOK   bool
	}{
		{
			name:     "k8s 401 prose response is unauthorized",
			err:      "Failed to get server version: the server has asked for the client to provide credentials",
			wantCode: "unauthorized",
			wantOK:   true,
		},
		{
			name:     "URL timeout query param does not trigger timeout match",
			err:      `Failed to get server version: Get "https://example.eks.amazonaws.com/version?timeout=5s": getting credentials: exec: executable aws failed with exit code 255`,
			wantCode: "exec_failed",
			wantOK:   true,
		},
		{
			name:     "context deadline exceeded is timeout",
			err:      `Get "https://api.example.com/version": context deadline exceeded`,
			wantCode: "timeout",
			wantOK:   true,
		},
		{
			name:     "i/o timeout is timeout",
			err:      `dial tcp 1.2.3.4:443: i/o timeout`,
			wantCode: "timeout",
			wantOK:   true,
		},
		{
			name:     "no such host is connection_failed",
			err:      `Get "https://nope.example.com:6443/version": dial tcp: lookup nope.example.com: no such host`,
			wantCode: "connection_failed",
			wantOK:   true,
		},
		{
			name:     "forbidden RBAC error",
			err:      `nodes is forbidden: User "x" cannot list resource "nodes" in API group ""`,
			wantCode: "forbidden",
			wantOK:   true,
		},
		{
			name:     "AWS SSO expired",
			err:      "the SSO session associated with this profile has expired",
			wantCode: "aws_sso_expired",
			wantOK:   true,
		},
		{
			name:     "x509 cert error",
			err:      `Get "https://api.example.com": x509: certificate signed by unknown authority`,
			wantCode: "cert_error",
			wantOK:   true,
		},
		{
			name:     "unknown error",
			err:      "some other internal error",
			wantCode: "",
			wantOK:   false,
		},
		{
			name:     "empty",
			err:      "",
			wantCode: "",
			wantOK:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, ok := ClassifyClusterError(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v (code=%q)", ok, tc.wantOK, code)
			}
			if code != tc.wantCode {
				t.Fatalf("code=%q, want %q", code, tc.wantCode)
			}
		})
	}
}
