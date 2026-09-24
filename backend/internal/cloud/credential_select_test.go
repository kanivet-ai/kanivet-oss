package cloud

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// fakeIdentities answers STS for the test: which account each profile's
// credentials belong to right now, and how often each was asked.
type fakeIdentities struct {
	mu       sync.Mutex
	accounts map[string]string
	calls    map[string]int
}

func newFakeIdentities(accounts map[string]string) *fakeIdentities {
	return &fakeIdentities{accounts: accounts, calls: map[string]int{}}
}

func (f *fakeIdentities) set(profile, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[profile] = account
}

func (f *fakeIdentities) identity(_ context.Context, profile string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[profile]++
	if acct, ok := f.accounts[profile]; ok && acct != "" {
		return acct, nil
	}
	return "", errors.New("Token has expired and refresh failed")
}

func withIdentities(s *Service, f *fakeIdentities) {
	s.credentials = newCredentialChecker(f.identity)
}

func bindSandbox(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.BindClusterSSO(context.Background(), bindingTestCluster, bindingTestStartURL, "243517631187", "AdministratorAccess"); err != nil {
		t.Fatal(err)
	}
}

func TestBoundClusterFallsBackToLeappWhenTheSSOSessionExpired(t *testing.T) {
	s, _ := newBindingTestService(t)
	bindSandbox(t, s)
	withIdentities(s, newFakeIdentities(map[string]string{"default": "243517631187"}))

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "default" {
		t.Errorf("profile = %q, want the Leapp default profile while SSO is signed out", got)
	}
}

func TestBoundClusterKeepsSSOWhileItWorks(t *testing.T) {
	s, _ := newBindingTestService(t)
	bindSandbox(t, s)
	ids := newFakeIdentities(map[string]string{"default": "243517631187", "sandbox-admin": "243517631187"})
	withIdentities(s, ids)

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("profile = %q, want the bound role", got)
	}
	if ids.calls["default"] != 0 {
		t.Error("checked the fallback although the bound role works")
	}
}

func TestUnboundClusterUsesSSOWhenLeappIsSignedOut(t *testing.T) {
	s, _ := newBindingTestService(t)
	withIdentities(s, newFakeIdentities(map[string]string{"sandbox-admin": "243517631187"}))

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("profile = %q, want the SSO profile for the account", got)
	}
}

func TestUnboundClusterLeavesTheKubeconfigAloneWhenItWorks(t *testing.T) {
	s, _ := newBindingTestService(t)
	withIdentities(s, newFakeIdentities(map[string]string{"default": "243517631187", "sandbox-admin": "243517631187"}))

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("profile = %q, want the kubeconfig as written", got)
	}
}

func TestCredentialsForAnotherAccountAreNotUsed(t *testing.T) {
	s, _ := newBindingTestService(t)
	bindSandbox(t, s)
	// Leapp is signed in, but to a different account.
	withIdentities(s, newFakeIdentities(map[string]string{"default": "585768152950"}))

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("profile = %q, want the bound role kept when nothing reaches the account", got)
	}
}

func TestNothingSignedInKeepsThePreferredProfile(t *testing.T) {
	s, _ := newBindingTestService(t)
	withIdentities(s, newFakeIdentities(map[string]string{}))
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("unbound: profile = %q, want the kubeconfig as written", got)
	}

	bindSandbox(t, s)
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("bound: profile = %q, want the bound role so the error offers its sign-in", got)
	}
}

func TestChoiceFollowsSignInsBothWays(t *testing.T) {
	s, _ := newBindingTestService(t)
	bindSandbox(t, s)
	ids := newFakeIdentities(map[string]string{"default": "243517631187"})
	withIdentities(s, ids)

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "default" {
		t.Fatalf("SSO signed out: profile = %q", got)
	}

	// The user signs in to SSO again and Leapp's session ends.
	ids.set("sandbox-admin", "243517631187")
	ids.set("default", "")
	s.ForgetCredentialChecks()
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Fatalf("SSO back: profile = %q", got)
	}

	// SSO expires again while Leapp is running.
	ids.set("sandbox-admin", "")
	ids.set("default", "243517631187")
	s.ForgetCredentialChecks()
	if got := s.AWSProfileForCluster(bindingTestCluster); got != "default" {
		t.Fatalf("SSO expired again: profile = %q", got)
	}
}

func TestAWSSignInChangeForgetsCredentialChecks(t *testing.T) {
	s, _ := newBindingTestService(t)
	bindSandbox(t, s)
	ids := newFakeIdentities(map[string]string{"default": "243517631187"})
	withIdentities(s, ids)
	_ = s.AWSProfileForCluster(bindingTestCluster)

	ids.set("sandbox-admin", "243517631187")
	s.NotifyExternalConfigChange(filepath.Join(t.TempDir(), ".aws", "sso", "cache", "token.json"))

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "sandbox-admin" {
		t.Errorf("profile = %q after an AWS sign-in change, want the bound role again", got)
	}
}

func TestCredentialChecksAreCachedBriefly(t *testing.T) {
	ids := newFakeIdentities(map[string]string{"default": "243517631187"})
	c := newCredentialChecker(ids.identity)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		c.reaches(ctx, "default", "243517631187")
		c.reaches(ctx, "sandbox-admin", "243517631187")
	}
	if ids.calls["default"] != 1 || ids.calls["sandbox-admin"] != 1 {
		t.Fatalf("calls = %v, want one check per profile", ids.calls)
	}

	now = now.Add(credentialCheckFailedTTL + time.Second)
	c.reaches(ctx, "default", "243517631187")
	c.reaches(ctx, "sandbox-admin", "243517631187")
	if ids.calls["default"] != 1 || ids.calls["sandbox-admin"] != 2 {
		t.Fatalf("calls = %v, want only the failed check repeated", ids.calls)
	}
}

func TestExecBlockWithItsOwnKeysIsLeftAlone(t *testing.T) {
	s, kubeconfigPath := newBindingTestService(t)
	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	exec := kubeconfig.AuthInfos[bindingTestCluster].Exec
	exec.Env = append(exec.Env, execEnv("AWS_ACCESS_KEY_ID", "AKIA"), execEnv("AWS_SECRET_ACCESS_KEY", "secret"))
	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}
	ids := newFakeIdentities(map[string]string{"sandbox-admin": "243517631187"})
	withIdentities(s, ids)

	if got := s.AWSProfileForCluster(bindingTestCluster); got != "" {
		t.Errorf("profile = %q, want the exec block's own keys kept", got)
	}
	if len(ids.calls) != 0 {
		t.Errorf("checked credentials %v for a context that carries its own keys", ids.calls)
	}
}

func execEnv(name, value string) clientcmdapi.ExecEnvVar {
	return clientcmdapi.ExecEnvVar{Name: name, Value: value}
}
