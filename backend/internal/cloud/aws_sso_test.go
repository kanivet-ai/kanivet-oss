package cloud

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/bytedance/sonic"
	"gopkg.in/ini.v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestBuildSSOTokenCacheUsesTokenExpiryAndAllowsMissingRefreshToken(t *testing.T) {
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	registration := ssoClientRegistration{
		ClientID:              "client-id",
		ClientSecret:          "client-secret",
		RegistrationExpiresAt: now.Add(24 * time.Hour).Unix(),
	}

	cached, expiresAt := buildSSOTokenCache(
		now,
		"https://example.awsapps.com/start",
		"eu-west-1",
		registration,
		"access-token",
		nil,
		3600,
	)

	wantExpiresAt := now.Add(time.Hour).UTC()
	if !expiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expiresAt=%s, want %s", expiresAt.Format(time.RFC3339), wantExpiresAt.Format(time.RFC3339))
	}
	if got := cached.ExpiresAt; got != wantExpiresAt.Format(time.RFC3339) {
		t.Fatalf("cached expiresAt=%q, want %q", got, wantExpiresAt.Format(time.RFC3339))
	}
	if cached.RefreshToken != "" {
		t.Fatal("expected refresh token to remain empty when CreateToken does not return one")
	}
	if got := cached.RegistrationExpiresAt; got != time.Unix(registration.RegistrationExpiresAt, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("registrationExpiresAt=%q, want %q", got, time.Unix(registration.RegistrationExpiresAt, 0).UTC().Format(time.RFC3339))
	}
}

func TestEnsureSSOProfileCreatesRefreshableSessionProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	p := NewAWSProvider()
	startURL := "https://example.awsapps.com/start/"
	profileName := "kanivet-sso-123456789012-Admin"
	if err := p.ensureSSOProfile(profileName, startURL, "eu-west-1", "123456789012", "Admin"); err != nil {
		t.Fatalf("ensureSSOProfile: %v", err)
	}

	configPath := filepath.Join(home, ".aws", "config")
	cfg, err := ini.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	profile := cfg.Section("profile " + profileName)
	sessionName := kanivetSSOSessionName(startURL)
	if got := profile.Key("sso_session").String(); got != sessionName {
		t.Fatalf("sso_session=%q, want %q", got, sessionName)
	}
	if profile.HasKey("sso_start_url") || profile.HasKey("sso_region") {
		t.Fatal("legacy inline SSO keys should not remain on kanivet profile")
	}

	session := cfg.Section("sso-session " + sessionName)
	if got := session.Key("sso_start_url").String(); got != startURL {
		t.Fatalf("start_url=%q, want %q", got, startURL)
	}
	if got := session.Key("sso_registration_scopes").String(); got != kanivetSSORegistrationScope {
		t.Fatalf("registration scopes=%q, want %q", got, kanivetSSORegistrationScope)
	}

	if info, err := os.Stat(configPath); err != nil {
		t.Fatalf("stat config: %v", err)
	} else if perms := info.Mode().Perm(); runtime.GOOS != "windows" && perms != 0o600 {
		t.Fatalf("config perms=%#o, want 0600", perms)
	}
}

func TestEnsureSSOProfileMigratesLegacyProfileAndPreservesUnrelatedSections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir aws dir: %v", err)
	}

	profileName := "kanivet-sso-123456789012-Admin"
	legacyStartURL := "https://example.awsapps.com/start/"
	config := `[profile kanivet-sso-123456789012-Admin]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = Admin
region = eu-west-1

[sso-session shared]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_registration_scopes = user:managed:scope

[profile unrelated]
region = us-east-1
output = json

[plugins]
cli_legacy_plugin_path = /tmp/aws-cli
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	p := NewAWSProvider()
	migratedStartURL := "https://example.awsapps.com/start"
	if err := p.ensureSSOProfile(profileName, migratedStartURL, "eu-west-1", "123456789012", "Admin"); err != nil {
		t.Fatalf("ensureSSOProfile: %v", err)
	}

	cfg, err := ini.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	profile := cfg.Section("profile " + profileName)
	sessionName := kanivetSSOSessionName(migratedStartURL)
	if got := profile.Key("sso_session").String(); got != sessionName {
		t.Fatalf("sso_session=%q, want %q", got, sessionName)
	}
	if profile.HasKey("sso_start_url") || profile.HasKey("sso_region") {
		t.Fatal("legacy inline keys should be removed during migration")
	}

	session := cfg.Section("sso-session " + sessionName)
	if got := session.Key("sso_start_url").String(); got != migratedStartURL {
		t.Fatalf("migrated session start_url=%q, want %q", got, migratedStartURL)
	}
	if got := session.Key("sso_registration_scopes").String(); got != kanivetSSORegistrationScope {
		t.Fatalf("migrated session scopes=%q, want %q", got, kanivetSSORegistrationScope)
	}

	sharedSession := cfg.Section("sso-session shared")
	if got := sharedSession.Key("sso_start_url").String(); got != legacyStartURL {
		t.Fatalf("shared session start_url=%q, want %q", got, legacyStartURL)
	}
	if got := sharedSession.Key("sso_registration_scopes").String(); got != "user:managed:scope" {
		t.Fatalf("shared session scopes=%q, want %q", got, "user:managed:scope")
	}

	unrelated := cfg.Section("profile unrelated")
	if got := unrelated.Key("region").String(); got != "us-east-1" {
		t.Fatalf("unrelated profile region=%q, want %q", got, "us-east-1")
	}
	if got := unrelated.Key("output").String(); got != "json" {
		t.Fatalf("unrelated profile output=%q, want %q", got, "json")
	}

	plugins := cfg.Section("plugins")
	if got := plugins.Key("cli_legacy_plugin_path").String(); got != "/tmp/aws-cli" {
		t.Fatalf("plugins section value=%q, want %q", got, "/tmp/aws-cli")
	}
}

func TestNewAWSProviderMigratesExistingImportedClusterProfileAndCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir aws dir: %v", err)
	}
	startURL := "https://example.awsapps.com/start/"
	config := `[profile kanivet-sso-123456789012-Admin]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = Admin
region = eu-west-1

[profile unrelated]
region = us-east-1
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	legacyToken := ssoTokenCache{
		StartURL:    startURL,
		Region:      "eu-west-1",
		AccessToken: "existing-access-token",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	if err := writeSSOTokenCache(startURL, legacyToken); err != nil {
		t.Fatalf("write legacy cache: %v", err)
	}

	NewAWSProvider()

	cfg, err := ini.Load(configPath)
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	profile := cfg.Section("profile kanivet-sso-123456789012-Admin")
	sessionName := kanivetSSOSessionName(startURL)
	if got := profile.Key("sso_session").String(); got != sessionName {
		t.Fatalf("sso_session=%q, want %q", got, sessionName)
	}
	if profile.HasKey("sso_start_url") || profile.HasKey("sso_region") {
		t.Fatal("startup migration left legacy inline SSO keys")
	}
	if got := cfg.Section("profile unrelated").Key("region").String(); got != "us-east-1" {
		t.Fatalf("unrelated profile region=%q, want us-east-1", got)
	}
	migratedToken := readCachedSSOToken(t, sessionName)
	if migratedToken.AccessToken != legacyToken.AccessToken {
		t.Fatal("startup migration did not preserve the existing access token")
	}
}

func TestEnsureSSOProfilePreservesAWSConfigSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatalf("mkdir aws dir: %v", err)
	}
	targetPath := filepath.Join(home, "managed-aws-config")
	if err := os.WriteFile(targetPath, []byte("[profile unrelated]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatalf("write managed config: %v", err)
	}
	configPath := filepath.Join(awsDir, "config")
	if err := os.Symlink(targetPath, configPath); err != nil {
		t.Fatalf("symlink config: %v", err)
	}

	p := &AWSProvider{}
	if err := p.ensureSSOProfile("kanivet-sso-123456789012-Admin", "https://example.awsapps.com/start", "eu-west-1", "123456789012", "Admin"); err != nil {
		t.Fatalf("ensureSSOProfile: %v", err)
	}

	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("lstat config: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("atomic config update replaced the managed symlink")
	}
	cfg, err := ini.Load(targetPath)
	if err != nil {
		t.Fatalf("load managed config target: %v", err)
	}
	if got := cfg.Section("profile unrelated").Key("region").String(); got != "us-east-1" {
		t.Fatalf("unrelated profile region=%q, want us-east-1", got)
	}
	if got := cfg.Section("profile kanivet-sso-123456789012-Admin").Key("sso_session").String(); got == "" {
		t.Fatal("managed config target did not receive the generated profile")
	}
}

func TestNewAWSProviderMigratesImportedEKSExecAuthToNeverInteractive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	kubeconfigPath := KanivetKubeconfigPath()
	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0o700); err != nil {
		t.Fatalf("mkdir kube dir: %v", err)
	}

	eksContext := "arn:aws:eks:eu-west-1:123456789012:cluster/demo"
	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters[eksContext] = &clientcmdapi.Cluster{Server: "https://demo.eks.amazonaws.com"}
	kubeconfig.AuthInfos[eksContext] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    "aws",
			Args:       []string{"eks", "get-token", "--cluster-name", "demo", "--region", "eu-west-1"},
		},
	}
	kubeconfig.Contexts[eksContext] = &clientcmdapi.Context{Cluster: eksContext, AuthInfo: eksContext}

	kubeconfig.Clusters["gke"] = &clientcmdapi.Cluster{Server: "https://gke.example.com"}
	kubeconfig.AuthInfos["gke"] = &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    "gke-gcloud-auth-plugin",
		},
	}
	kubeconfig.Contexts["gke"] = &clientcmdapi.Context{Cluster: "gke", AuthInfo: "gke"}

	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	NewAWSProvider()

	migrated, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("load migrated kubeconfig: %v", err)
	}
	if got := migrated.AuthInfos[eksContext].Exec.InteractiveMode; got != clientcmdapi.NeverExecInteractiveMode {
		t.Fatalf("eks interactiveMode=%q, want %q", got, clientcmdapi.NeverExecInteractiveMode)
	}
	if got := migrated.AuthInfos["gke"].Exec.Command; got != "gke-gcloud-auth-plugin" {
		t.Fatalf("gke exec command=%q, want gke-gcloud-auth-plugin", got)
	}
}

func TestCacheSSOTokenWritesDeterministicSessionAndLegacyKanivetCompatibilityOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir aws dir: %v", err)
	}

	config := `[profile kanivet-sso-123456789012-Admin]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = Admin
region = eu-west-1

[sso-session shared]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	p := &AWSProvider{}
	token := ssoTokenCache{
		StartURL:              "https://example.awsapps.com/start",
		Region:                "eu-west-1",
		AccessToken:           "access-token",
		ExpiresAt:             "2026-08-04T12:00:00Z",
		RefreshToken:          "refresh-token",
		ClientID:              "client-id",
		ClientSecret:          "client-secret",
		RegistrationExpiresAt: "2026-09-04T12:00:00Z",
	}
	if err := p.cacheSSOToken(token); err != nil {
		t.Fatalf("cacheSSOToken: %v", err)
	}

	deterministic := readCachedSSOToken(t, kanivetSSOSessionName(token.StartURL))
	assertCachedSSOTokenMode(t, kanivetSSOSessionName(token.StartURL))
	if deterministic.RefreshToken != token.RefreshToken {
		t.Fatal("deterministic session cache did not persist the refresh token")
	}
	if deterministic.ClientID != token.ClientID {
		t.Fatal("deterministic session cache did not persist the client ID")
	}
	if deterministic.ClientSecret != token.ClientSecret {
		t.Fatal("deterministic session cache did not persist the client secret")
	}

	legacy := readCachedSSOToken(t, "https://example.awsapps.com/start/")
	assertCachedSSOTokenMode(t, "https://example.awsapps.com/start/")
	if legacy.AccessToken != token.AccessToken {
		t.Fatal("legacy kanivet cache did not persist the access token")
	}

	assertMissingCachedSSOToken(t, "shared")
	assertMissingCachedSSOToken(t, token.StartURL)
}

func readCachedSSOToken(t *testing.T, key string) ssoTokenCache {
	t.Helper()

	cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		t.Fatalf("cache path for %q: %v", key, err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache for %q: %v", key, err)
	}

	var cached ssoTokenCache
	if err := sonic.Unmarshal(data, &cached); err != nil {
		t.Fatalf("unmarshal cache for %q: %v", key, err)
	}
	return cached
}

func assertMissingCachedSSOToken(t *testing.T, key string) {
	t.Helper()

	cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		t.Fatalf("cache path for %q: %v", key, err)
	}
	if _, err := os.Stat(cachePath); err == nil {
		t.Fatalf("unexpected cache file for %q", key)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat cache for %q: %v", key, err)
	}
}

func assertCachedSSOTokenMode(t *testing.T, key string) {
	t.Helper()

	cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		t.Fatalf("cache path for %q: %v", key, err)
	}
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache for %q: %v", key, err)
	}
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
		t.Fatalf("cache mode for %q=%#o, want 0600", key, mode)
	}
}
