package awsidentity

import (
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestParseExecHint(t *testing.T) {
	tests := []struct {
		name string
		exec *clientcmdapi.ExecConfig
		want ExecHint
	}{
		{
			name: "kanivet import with AWS_PROFILE env",
			exec: &clientcmdapi.ExecConfig{
				Command: "aws",
				Args:    []string{"eks", "get-token", "--cluster-name", "prod-eu", "--region", "eu-west-1"},
				Env:     []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: "kanivet-sso-123456789012-Admin"}},
			},
			want: ExecHint{IsEKS: true, ClusterName: "prod-eu", Region: "eu-west-1", Profile: "kanivet-sso-123456789012-Admin"},
		},
		{
			name: "aws eks update-kubeconfig with --profile",
			exec: &clientcmdapi.ExecConfig{
				Command: "/usr/local/bin/aws",
				Args:    []string{"--region", "us-east-1", "eks", "get-token", "--cluster-name", "staging", "--output", "json", "--profile", "staging-admin"},
			},
			want: ExecHint{IsEKS: true, ClusterName: "staging", Region: "us-east-1", Profile: "staging-admin"},
		},
		{
			name: "flag=value form",
			exec: &clientcmdapi.ExecConfig{
				Command: "aws",
				Args:    []string{"eks", "get-token", "--cluster-name=dev", "--region=eu-west-1"},
				Env:     []clientcmdapi.ExecEnvVar{{Name: "AWS_DEFAULT_REGION", Value: "ignored-because-flag-wins"}},
			},
			want: ExecHint{IsEKS: true, ClusterName: "dev", Region: "eu-west-1"},
		},
		{
			name: "aws-iam-authenticator",
			exec: &clientcmdapi.ExecConfig{
				Command: "aws-iam-authenticator",
				Args:    []string{"token", "-i", "legacy"},
				Env:     []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: "legacy"}, {Name: "AWS_REGION", Value: "ap-southeast-2"}},
			},
			want: ExecHint{IsEKS: true, ClusterName: "legacy", Region: "ap-southeast-2", Profile: "legacy"},
		},
		{
			name: "non-AWS exec",
			exec: &clientcmdapi.ExecConfig{Command: "gke-gcloud-auth-plugin"},
			want: ExecHint{},
		},
		{
			name: "aws but not eks",
			exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"sts", "get-caller-identity"}},
			want: ExecHint{},
		},
		{
			name: "nil exec",
			exec: nil,
			want: ExecHint{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseExecHint(tc.exec); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestHintFromClusterName(t *testing.T) {
	got := hintFromClusterName("arn:aws:eks:eu-west-1:123456789012:cluster/prod-eu")
	want := ExecHint{IsEKS: true, Region: "eu-west-1", AccountID: "123456789012", ClusterName: "prod-eu"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := hintFromClusterName("my-kind-cluster"); got.IsEKS {
		t.Fatalf("expected non-EKS hint, got %+v", got)
	}
}

func TestRoleAndAccountFromArn(t *testing.T) {
	if got := roleNameFromArn("arn:aws:iam::123456789012:role/service-role/payments-api"); got != "payments-api" {
		t.Fatalf("roleNameFromArn = %q", got)
	}
	if got := roleNameFromArn("arn:aws:iam::123456789012:role/payments-api"); got != "payments-api" {
		t.Fatalf("roleNameFromArn = %q", got)
	}
	if got := accountFromArn("arn:aws:iam::123456789012:role/x"); got != "123456789012" {
		t.Fatalf("accountFromArn = %q", got)
	}
	if got := accountFromArn("arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"); got != "aws" {
		t.Fatalf("accountFromArn = %q", got)
	}
}
