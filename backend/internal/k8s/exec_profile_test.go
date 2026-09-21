package k8s

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func execEnv(exec *clientcmdapi.ExecConfig, name string) (string, bool) {
	for _, e := range exec.Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestApplyAWSProfilePinsDefaultChainContext(t *testing.T) {
	exec := &clientcmdapi.ExecConfig{
		Command: "/opt/homebrew/bin/aws",
		Args:    []string{"--region", "eu-north-1", "eks", "get-token", "--cluster-name", "sbx", "--output", "json"},
	}

	applyAWSProfile(exec, "kanivet-sso-243517631187-AdministratorAccess")

	if got, _ := execEnv(exec, "AWS_PROFILE"); got != "kanivet-sso-243517631187-AdministratorAccess" {
		t.Errorf("AWS_PROFILE = %q", got)
	}
	// --profile makes the AWS CLI ignore AWS_ACCESS_KEY_ID inherited from the
	// process environment, which AWS_PROFILE alone does not.
	want := []string{"--region", "eu-north-1", "eks", "get-token", "--cluster-name", "sbx", "--output", "json", "--profile", "kanivet-sso-243517631187-AdministratorAccess"}
	if !reflect.DeepEqual(exec.Args, want) {
		t.Errorf("args = %v", exec.Args)
	}
}

func TestApplyAWSProfileReplacesExistingProfile(t *testing.T) {
	exec := &clientcmdapi.ExecConfig{
		Command: "aws",
		Args:    []string{"eks", "get-token", "--cluster-name", "sbx", "--profile", "old"},
		Env: []clientcmdapi.ExecEnvVar{
			{Name: "AWS_PROFILE", Value: "old"},
			{Name: "AWS_DEFAULT_PROFILE", Value: "old"},
			{Name: "AWS_REGION", Value: "eu-north-1"},
		},
	}

	applyAWSProfile(exec, "new")

	if got, _ := execEnv(exec, "AWS_PROFILE"); got != "new" {
		t.Errorf("AWS_PROFILE = %q", got)
	}
	if _, ok := execEnv(exec, "AWS_DEFAULT_PROFILE"); ok {
		t.Error("AWS_DEFAULT_PROFILE should be dropped")
	}
	if got, _ := execEnv(exec, "AWS_REGION"); got != "eu-north-1" {
		t.Errorf("AWS_REGION = %q, unrelated env must survive", got)
	}
	want := []string{"eks", "get-token", "--cluster-name", "sbx", "--profile", "new"}
	if !reflect.DeepEqual(exec.Args, want) {
		t.Errorf("args = %v", exec.Args)
	}
}

func TestApplyAWSProfileLeavesNonAWSCommandsArgsAlone(t *testing.T) {
	exec := &clientcmdapi.ExecConfig{
		Command: "aws-iam-authenticator",
		Args:    []string{"token", "-i", "sbx"},
	}

	applyAWSProfile(exec, "new")

	if got, _ := execEnv(exec, "AWS_PROFILE"); got != "new" {
		t.Errorf("AWS_PROFILE = %q", got)
	}
	if !reflect.DeepEqual(exec.Args, []string{"token", "-i", "sbx"}) {
		t.Errorf("args = %v", exec.Args)
	}
}

func TestApplyAWSProfileIgnoresEmptyProfile(t *testing.T) {
	exec := &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token"}}

	applyAWSProfile(exec, "")

	if len(exec.Env) != 0 || len(exec.Args) != 2 {
		t.Errorf("exec changed: env=%v args=%v", exec.Env, exec.Args)
	}
}

const eksDefaultChainKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: arn:aws:eks:eu-north-1:243517631187:cluster/sbx
  cluster:
    server: https://ABC.gr7.eu-north-1.eks.amazonaws.com
contexts:
- name: arn:aws:eks:eu-north-1:243517631187:cluster/sbx
  context:
    cluster: arn:aws:eks:eu-north-1:243517631187:cluster/sbx
    user: arn:aws:eks:eu-north-1:243517631187:cluster/sbx
users:
- name: arn:aws:eks:eu-north-1:243517631187:cluster/sbx
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args: [--region, eu-north-1, eks, get-token, --cluster-name, sbx, --output, json]
      interactiveMode: Never
`

func TestGetConfigForClusterUsesResolvedAWSProfile(t *testing.T) {
	const cluster = "arn:aws:eks:eu-north-1:243517631187:cluster/sbx"
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(eksDefaultChainKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newTestClient()
	c.kubeconfigs = []string{path}
	c.SetAWSProfileResolver(func(name string) string {
		if name == cluster {
			return "kanivet-sso-243517631187-ReadOnlyAccess"
		}
		return ""
	})

	cfg, err := c.getConfigForCluster(cluster)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ExecProvider == nil {
		t.Fatal("no exec provider on the built config")
	}
	if got, _ := execEnv(cfg.ExecProvider, "AWS_PROFILE"); got != "kanivet-sso-243517631187-ReadOnlyAccess" {
		t.Errorf("AWS_PROFILE = %q", got)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != eksDefaultChainKubeconfig {
		t.Error("kubeconfig on disk was modified")
	}
}
