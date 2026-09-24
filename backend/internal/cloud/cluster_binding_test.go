package cloud

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/kanivet/backend/internal/db"
)

const (
	bindingTestCluster  = "arn:aws:eks:eu-north-1:243517631187:cluster/sbx"
	bindingTestStartURL = "https://corp.awsapps.com/start"
)

// newBindingTestService builds a Service over a temp HOME holding a Leapp-style
// default profile, one user SSO profile, and a kubeconfig whose EKS context
// names no profile.
func newBindingTestService(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = eu-west-1

[profile sandbox-admin]
sso_session = corp
sso_account_id = 243517631187
sso_role_name = AdministratorAccess
`
	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := "[default]\naws_access_key_id = ASIA\naws_secret_access_key = secret\naws_session_token = token\n"
	if err := os.WriteFile(filepath.Join(awsDir, "credentials"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	kubeconfigPath := filepath.Join(home, "kubeconfig")
	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters[bindingTestCluster] = &clientcmdapi.Cluster{Server: "https://abc.gr7.eu-north-1.eks.amazonaws.com"}
	kubeconfig.AuthInfos[bindingTestCluster] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token", "--cluster-name", "sbx"}}}
	kubeconfig.Contexts[bindingTestCluster] = &clientcmdapi.Context{Cluster: bindingTestCluster, AuthInfo: bindingTestCluster}
	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}

	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&db.ClusterAWSBinding{}); err != nil {
		t.Fatal(err)
	}
	s := NewService(&db.DB{DB: gdb})
	s.SetKubeconfigResolver(func(string) string { return kubeconfigPath })
	// Every profile reaches the cluster's account unless a test says otherwise.
	s.credentials = newCredentialChecker(func(context.Context, string) (string, error) { return "243517631187", nil })
	return s, kubeconfigPath
}

func TestUnboundLeappContextReportsItsAccount(t *testing.T) {
	s, _ := newBindingTestService(t)

	info, err := s.DescribeClusterAuth(context.Background(), bindingTestCluster)
	if err != nil {
		t.Fatal(err)
	}

	if info.Method != "aws-static" || info.AccountID != "243517631187" || info.SSOBinding != nil {
		t.Fatalf("unexpected info: %+v", info)
	}
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("unbound cluster resolved to profile %q", got)
	}
}

func TestBindClusterSSOReusesTheUsersProfile(t *testing.T) {
	s, kubeconfigPath := newBindingTestService(t)
	before, _ := os.ReadFile(kubeconfigPath)
	var notified []Provider
	s.SetOnAuthChanged(func(p Provider) { notified = append(notified, p) })

	binding, err := s.BindClusterSSO(context.Background(), bindingTestCluster, bindingTestStartURL, "243517631187", "AdministratorAccess")
	if err != nil {
		t.Fatal(err)
	}

	if binding.Profile != "sandbox-admin" {
		t.Errorf("profile = %q, want the user's own sandbox-admin", binding.Profile)
	}
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("AWSProfileForCluster = %q", got)
	}
	if len(notified) != 1 || notified[0] != ProviderAWS {
		t.Errorf("auth-changed notifications = %v, want one for aws so clients reconnect", notified)
	}
	after, _ := os.ReadFile(kubeconfigPath)
	if string(before) != string(after) {
		t.Error("kubeconfig was modified")
	}
}

func TestBindClusterSSOCreatesAProfileForANewRoleAndSwitches(t *testing.T) {
	s, _ := newBindingTestService(t)
	ctx := context.Background()
	if _, err := s.BindClusterSSO(ctx, bindingTestCluster, bindingTestStartURL, "243517631187", "AdministratorAccess"); err != nil {
		t.Fatal(err)
	}

	binding, err := s.BindClusterSSO(ctx, bindingTestCluster, bindingTestStartURL, "243517631187", "ReadOnlyAccess")
	if err != nil {
		t.Fatal(err)
	}

	if binding.Profile != "kanivet-sso-243517631187-ReadOnlyAccess" {
		t.Errorf("profile = %q", binding.Profile)
	}
	if got := s.AWSProfileForCluster(bindingTestCluster); got != binding.Profile {
		t.Errorf("AWSProfileForCluster = %q after switching role", got)
	}
	home, _ := os.UserHomeDir()
	written, _ := os.ReadFile(filepath.Join(home, ".aws", "config"))
	if !strings.Contains(string(written), "[profile kanivet-sso-243517631187-ReadOnlyAccess]") {
		t.Errorf("profile not written to ~/.aws/config:\n%s", written)
	}
}

func TestBindClusterSSORejectsAnotherAccount(t *testing.T) {
	s, _ := newBindingTestService(t)

	_, err := s.BindClusterSSO(context.Background(), bindingTestCluster, bindingTestStartURL, "585768152950", "AdministratorAccess")

	if err == nil {
		t.Fatal("bound an EKS cluster in 243517631187 to account 585768152950")
	}
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("rejected binding still resolves to %q", got)
	}
}

func TestDescribeClusterAuthFollowsTheBinding(t *testing.T) {
	s, _ := newBindingTestService(t)
	ctx := context.Background()
	if _, err := s.BindClusterSSO(ctx, bindingTestCluster, bindingTestStartURL, "243517631187", "AdministratorAccess"); err != nil {
		t.Fatal(err)
	}

	info, err := s.DescribeClusterAuth(ctx, bindingTestCluster)
	if err != nil {
		t.Fatal(err)
	}

	// No longer "refresh Leapp": the pane must offer the portal sign-in.
	if info.Method != "aws-sso" || info.SignIn != "aws-sso" || info.ExternalTool {
		t.Fatalf("unexpected info: %+v", info)
	}
	if info.Profile != "sandbox-admin" || info.SSOStartURL != bindingTestStartURL {
		t.Errorf("profile %q, portal %q", info.Profile, info.SSOStartURL)
	}
	if info.SSOBinding == nil || info.SSOBinding.RoleName != "AdministratorAccess" || info.SSOBinding.AccountID != "243517631187" {
		t.Errorf("binding = %+v", info.SSOBinding)
	}
}

func TestUnbindClusterSSORestoresTheKubeconfigBehaviour(t *testing.T) {
	s, _ := newBindingTestService(t)
	ctx := context.Background()
	if _, err := s.BindClusterSSO(ctx, bindingTestCluster, bindingTestStartURL, "243517631187", "AdministratorAccess"); err != nil {
		t.Fatal(err)
	}

	if err := s.UnbindClusterSSO(bindingTestCluster); err != nil {
		t.Fatal(err)
	}

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("AWSProfileForCluster = %q after unbind", got)
	}
	info, err := s.DescribeClusterAuth(ctx, bindingTestCluster)
	if err != nil {
		t.Fatal(err)
	}
	if info.Method != "aws-static" || info.SSOBinding != nil {
		t.Errorf("unexpected info after unbind: %+v", info)
	}
}
