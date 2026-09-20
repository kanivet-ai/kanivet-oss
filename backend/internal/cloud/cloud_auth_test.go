package cloud

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func writeTestToken(t *testing.T, key string, tok ssoTokenCache) string {
	t.Helper()
	if err := writeSSOTokenCache(key, tok); err != nil {
		t.Fatalf("write token %q: %v", key, err)
	}
	path, _ := ssocreds.StandardCachedTokenFilepath(key)
	return path
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func TestPickSSOTokenPrefersValidThenRefreshable(t *testing.T) {
	now := time.Now()
	expiredPlain := ssoCachedToken{Path: "a", Token: ssoTokenCache{AccessToken: "x"}, ExpiresAt: now.Add(-2 * time.Hour)}
	expiredRefreshable := ssoCachedToken{Path: "b", Token: ssoTokenCache{AccessToken: "x", RefreshToken: "r", ClientID: "c", ClientSecret: "s"}, ExpiresAt: now.Add(-time.Hour), RegistrationExpiresAt: now.Add(24 * time.Hour)}
	valid := ssoCachedToken{Path: "c", Token: ssoTokenCache{AccessToken: "x"}, ExpiresAt: now.Add(time.Hour)}

	if got := pickSSOToken([]ssoCachedToken{expiredPlain, expiredRefreshable, valid}, now); got == nil || got.Path != "c" {
		t.Fatalf("expected valid token, got %+v", got)
	}
	if got := pickSSOToken([]ssoCachedToken{expiredPlain, expiredRefreshable}, now); got == nil || got.Path != "b" {
		t.Fatalf("expected refreshable token, got %+v", got)
	}
	if got := pickSSOToken([]ssoCachedToken{expiredPlain}, now); got == nil || got.Path != "a" {
		t.Fatalf("expected expired token as last resort, got %+v", got)
	}
	if got := pickSSOToken(nil, now); got != nil {
		t.Fatal("expected nil for no tokens")
	}
}

func TestTokenStoreSeesTerminalLoginAndLogout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store := newSSOTokenStore()
	startURL := "https://example.awsapps.com/start"
	if tok := store.best(startURL, time.Now()); tok != nil {
		t.Fatalf("expected no token before login, got %+v", tok)
	}

	// Simulate `aws sso login --sso-session my-sso` from a terminal.
	path := writeTestToken(t, "my-sso", ssoTokenCache{StartURL: startURL + "/", Region: "eu-west-1", AccessToken: "cli-token", ExpiresAt: rfc3339(time.Now().Add(time.Hour))})
	store.invalidate()
	tok := store.best(startURL, time.Now())
	if tok == nil || tok.Token.AccessToken != "cli-token" {
		t.Fatalf("expected terminal token to be visible, got %+v", tok)
	}
	if tok.FromKanivet {
		t.Fatal("terminal token must not be attributed to Kanivet")
	}

	// Simulate `aws sso logout`.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store.invalidate()
	if tok := store.best(startURL, time.Now()); tok != nil {
		t.Fatalf("expected no token after logout, got %+v", tok)
	}
}

func TestSSOCacheKeysIncludeCompatibleUserSessionsOnly(t *testing.T) {
	startURL := "https://example.awsapps.com/start"
	sessions := []awsSSOSessionConfig{
		{Name: "team", StartURL: startURL + "/", Region: "eu-west-1"},
		{Name: "q-dev", StartURL: startURL, Region: "eu-west-1", Scopes: "sso:account:access,codewhisperer:completions"},
		{Name: "other", StartURL: "https://other.awsapps.com/start", Region: "us-east-1"},
	}
	profiles := []awsSSOProfileConfig{
		{Name: "legacy", StartURL: startURL + "/", Legacy: true},
	}
	keys := ssoCacheKeysFor(startURL, sessions, profiles)
	want := map[string]bool{kanivetSSOSessionName(startURL): true, "team": true, startURL + "/": true}
	if len(keys) != len(want) {
		t.Fatalf("keys=%v, want %v", keys, want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("unexpected key %q in %v", k, keys)
		}
	}
}

func TestWriteSSOTokenToKeysDoesNotClobberLongerLivedToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	startURL := "https://example.awsapps.com/start"
	longer := ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "longer", ExpiresAt: rfc3339(time.Now().Add(6 * time.Hour))}
	writeTestToken(t, "team", longer)

	ours := ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "ours", ExpiresAt: rfc3339(time.Now().Add(time.Hour))}
	if err := writeSSOTokenToKeys(ours, []string{kanivetSSOSessionName(startURL), "team"}); err != nil {
		t.Fatalf("writeSSOTokenToKeys: %v", err)
	}
	if got := readCachedSSOToken(t, "team"); got.AccessToken != "longer" {
		t.Fatalf("user's longer-lived token was overwritten: %+v", got)
	}
	if got := readCachedSSOToken(t, kanivetSSOSessionName(startURL)); got.AccessToken != "ours" {
		t.Fatalf("kanivet key not written: %+v", got)
	}
}

func TestCacheSSOTokenSharesWithUserSessionsForTheSamePortal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session shared]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1

[sso-session with-extra-scopes]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_registration_scopes = sso:account:access,codewhisperer:completions

[profile legacy-kanivet]
sso_start_url = https://example.awsapps.com/start/
sso_region = eu-west-1
sso_account_id = 123456789012
sso_role_name = Admin
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewAWSProvider()
	token := ssoTokenCache{
		StartURL: "https://example.awsapps.com/start", Region: "eu-west-1", AccessToken: "access-token",
		ExpiresAt: rfc3339(time.Now().Add(time.Hour)), RefreshToken: "refresh", ClientID: "cid", ClientSecret: "sec",
	}
	if err := p.cacheSSOToken(token); err != nil {
		t.Fatalf("cacheSSOToken: %v", err)
	}
	if got := readCachedSSOToken(t, kanivetSSOSessionName(token.StartURL)); got.RefreshToken != "refresh" {
		t.Fatal("kanivet session cache missing refresh token")
	}
	if got := readCachedSSOToken(t, "shared"); got.AccessToken != "access-token" {
		t.Fatal("user's sso-session for the same portal should be signed in too")
	}
	if got := readCachedSSOToken(t, "https://example.awsapps.com/start/"); got.AccessToken != "access-token" {
		t.Fatal("legacy start-url key should be written for legacy profiles")
	}
	assertMissingCachedSSOToken(t, "with-extra-scopes")
}

func TestSessionStatusStates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := NewAWSProvider()
	now := time.Now()
	startURL := "https://example.awsapps.com/start"

	if s := p.sessionStatus(startURL, "", now); s.State != SSOStateSignedOut {
		t.Fatalf("state=%s, want signed_out", s.State)
	}
	writeTestToken(t, kanivetSSOSessionName(startURL), ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "a", ExpiresAt: rfc3339(now.Add(-time.Minute)), RefreshToken: "r", ClientID: "c", ClientSecret: "s", RegistrationExpiresAt: rfc3339(now.Add(48 * time.Hour))})
	p.tokens.invalidate()
	if s := p.sessionStatus(startURL, "", now); s.State != SSOStateRefreshable || !s.Refreshable {
		t.Fatalf("state=%s refreshable=%v, want refreshable", s.State, s.Refreshable)
	}
	writeTestToken(t, kanivetSSOSessionName(startURL), ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "a", ExpiresAt: rfc3339(now.Add(-time.Minute))})
	p.tokens.invalidate()
	if s := p.sessionStatus(startURL, "", now); s.State != SSOStateExpired {
		t.Fatalf("state=%s, want expired", s.State)
	}
	writeTestToken(t, kanivetSSOSessionName(startURL), ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "a", ExpiresAt: rfc3339(now.Add(time.Hour))})
	p.tokens.invalidate()
	if s := p.sessionStatus(startURL, "", now); s.State != SSOStateActive || !s.IsValid {
		t.Fatalf("state=%s, want active", s.State)
	}
}

func TestGetSSOSessionsMergesConfigAndCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = us-east-1

[profile dev]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Dev

[profile leapp-prod]
region = eu-west-1
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestToken(t, "some-other-tool", ssoTokenCache{StartURL: "https://other.awsapps.com/start", Region: "eu-west-1", AccessToken: "t", ExpiresAt: rfc3339(time.Now().Add(time.Hour))})

	p := NewAWSProvider()
	sessions := p.GetSSOSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 portals, got %+v", sessions)
	}
	byURL := map[string]SSOSessionStatus{}
	for _, s := range sessions {
		byURL[s.StartURL] = s
	}
	corp := byURL["https://corp.awsapps.com/start"]
	if corp.Source != "config" || len(corp.SessionNames) != 1 || corp.SessionNames[0] != "corp" || corp.ProfileCount != 1 || corp.State != SSOStateSignedOut {
		t.Fatalf("unexpected corp session: %+v", corp)
	}
	other := byURL["https://other.awsapps.com/start"]
	if other.Source != "cache" || other.State != SSOStateActive || other.Managed {
		t.Fatalf("unexpected other session: %+v", other)
	}
}

func TestRestoreDefaultFromBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	content := `[default]
aws_access_key_id = KANIVET
aws_secret_access_key = TEMP
aws_session_token = TOKEN

[kanivet-backup-original-default]
aws_access_key_id = ORIGINAL
aws_secret_access_key = ORIGINALSECRET

[leapp-prod]
aws_access_key_id = LEAPP
aws_secret_access_key = LEAPPSECRET
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreDefaultFromBackup(path, kanivetBackupProfile)
	if err != nil || !restored {
		t.Fatalf("restored=%v err=%v", restored, err)
	}
	cfg, err := loadINIFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Section("default").Key("aws_access_key_id").String(); got != "ORIGINAL" {
		t.Fatalf("default key=%q, want ORIGINAL", got)
	}
	if cfg.Section("default").HasKey("aws_session_token") {
		t.Fatal("stale session token left on restored default")
	}
	if cfg.HasSection(kanivetBackupProfile) {
		t.Fatal("backup section should be removed")
	}
	if got := cfg.Section("leapp-prod").Key("aws_access_key_id").String(); got != "LEAPP" {
		t.Fatal("unrelated Leapp profile was modified")
	}
	if restored, _ := restoreDefaultFromBackup(path, kanivetBackupProfile); restored {
		t.Fatal("second restore should be a no-op")
	}
}

func TestPreferredSSORole(t *testing.T) {
	if got := preferredSSORole([]string{"AdministratorAccess", "ReadOnlyAccess", "PowerUser"}); got != "ReadOnlyAccess" {
		t.Fatalf("got %q", got)
	}
	if got := preferredSSORole([]string{"PowerUser", "AdministratorAccess"}); got != "AdministratorAccess" {
		t.Fatalf("got %q", got)
	}
	if got := preferredSSORole([]string{"Ops"}); got != "Ops" {
		t.Fatalf("got %q", got)
	}
	if got := preferredSSORole(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestFindUserSSOProfileReusesExistingProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start/
sso_region = us-east-1

[profile prod-admin]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Admin

[profile kanivet-sso-111111111111-Admin]
sso_session = kanivet-sso-x
sso_account_id = 111111111111
sso_role_name = Admin
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	name, ok := findUserSSOProfile("https://corp.awsapps.com/start", "111111111111", "Admin")
	if !ok || name != "prod-admin" {
		t.Fatalf("got %q ok=%v, want prod-admin", name, ok)
	}
	if _, ok := findUserSSOProfile("https://corp.awsapps.com/start", "222222222222", "Admin"); ok {
		t.Fatal("should not match a different account")
	}
}

func TestDescribeClusterAuthResolvesAWSProfileKinds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = us-east-1

[profile sso-prod]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Admin

[profile vault]
credential_process = aws-vault exec prod --json

[profile assumed]
role_arn = arn:aws:iam::222222222222:role/x
source_profile = sso-prod
`
	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := `[leapp]
aws_access_key_id = ASIA
aws_secret_access_key = secret
aws_session_token = token
`
	if err := os.WriteFile(filepath.Join(awsDir, "credentials"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	kubeconfigPath := filepath.Join(home, "kubeconfig")
	kubeconfig := clientcmdapi.NewConfig()
	add := func(name, profile string, envProfile bool) {
		kubeconfig.Clusters[name] = &clientcmdapi.Cluster{Server: "https://abc." + "eu-west-1.eks.amazonaws.com"}
		exec := &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1beta1", Command: "aws", Args: []string{"eks", "get-token", "--cluster-name", name}}
		if envProfile {
			exec.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: profile}}
		} else if profile != "" {
			exec.Args = append(exec.Args, "--profile", profile)
		}
		kubeconfig.AuthInfos[name] = &clientcmdapi.AuthInfo{Exec: exec}
		kubeconfig.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	add("sso-cluster", "sso-prod", true)
	add("leapp-cluster", "leapp", false)
	add("vault-cluster", "vault", true)
	add("assumed-cluster", "assumed", true)
	add("missing-cluster", "nope", true)
	kubeconfig.Clusters["aks"] = &clientcmdapi.Cluster{Server: "https://x.hcp.westeurope.azmk8s.io"}
	kubeconfig.AuthInfos["aks"] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "kubelogin", Args: []string{"get-token", "--login", "devicecode", "--server-id", "abc"}}}
	kubeconfig.Contexts["aks"] = &clientcmdapi.Context{Cluster: "aks", AuthInfo: "aks"}
	kubeconfig.Clusters["gke"] = &clientcmdapi.Cluster{Server: "https://1.2.3.4"}
	kubeconfig.AuthInfos["gke"] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "gke-gcloud-auth-plugin"}}
	kubeconfig.Contexts["gke_proj_region_name"] = &clientcmdapi.Context{Cluster: "gke", AuthInfo: "gke"}
	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}

	p := NewAWSProvider()
	cases := []struct {
		cluster, method, signIn, profile, startURL string
		external                                   bool
	}{
		{"sso-cluster", "aws-sso", "aws-sso", "sso-prod", "https://corp.awsapps.com/start", false},
		{"leapp-cluster", "aws-static", "", "leapp", "", true},
		{"vault-cluster", "aws-credential-process", "", "vault", "", true},
		{"assumed-cluster", "aws-sso", "aws-sso", "assumed", "https://corp.awsapps.com/start", false},
		{"missing-cluster", "aws-profile-missing", "", "nope", "", false},
		{"aks", "azure-kubelogin", "azure", "", "", false},
		{"gke_proj_region_name", "gcp-plugin", "gcp", "", "", false},
	}
	for _, tc := range cases {
		info, err := p.describeClusterAuth(tc.cluster, kubeconfigPath)
		if err != nil {
			t.Fatalf("%s: %v", tc.cluster, err)
		}
		if info.Method != tc.method || info.SignIn != tc.signIn || info.Profile != tc.profile || info.SSOStartURL != tc.startURL || info.ExternalTool != tc.external {
			t.Fatalf("%s: got %+v", tc.cluster, info)
		}
		if tc.cluster == "sso-cluster" && info.SSOState != SSOStateSignedOut {
			t.Fatalf("sso-cluster state=%q", info.SSOState)
		}
		if tc.cluster == "aks" && info.Hint == "" {
			t.Fatal("devicecode kubelogin should carry a conversion hint")
		}
	}
}

func TestConvertKubeloginToAzureCLI(t *testing.T) {
	authInfo := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		Command: "kubelogin",
		Args:    []string{"get-token", "--environment", "AzurePublicCloud", "--server-id", "6dae42f8", "--client-id", "80faf920", "--tenant-id", "tenant", "--login", "devicecode"},
		Env:     []clientcmdapi.ExecEnvVar{{Name: "AAD_LOGIN_METHOD", Value: "devicecode"}},
	}}
	if !convertKubeloginToAzureCLI(authInfo) {
		t.Fatal("expected conversion")
	}
	want := []string{"get-token", "--environment", "AzurePublicCloud", "--server-id", "6dae42f8", "--tenant-id", "tenant", "--login", "azurecli"}
	if got := authInfo.Exec.Args; len(got) != len(want) {
		t.Fatalf("args=%v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("args=%v, want %v", got, want)
			}
		}
	}
	if authInfo.Exec.InteractiveMode != clientcmdapi.NeverExecInteractiveMode || authInfo.Exec.Env != nil {
		t.Fatalf("exec not made non-interactive: %+v", authInfo.Exec)
	}
	if convertKubeloginToAzureCLI(&clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws"}}) {
		t.Fatal("non-kubelogin exec must be left alone")
	}
}

func TestCLILoginOutputParsing(t *testing.T) {
	m := newCLILoginManager()
	job := &CLILoginJob{ID: "j", State: cliJobRunning}
	m.byID["j"] = job
	m.appendLine("j", "To sign in, use a web browser to open the page https://microsoft.com/devicelogin and enter the code ABCD1234 to authenticate.")
	if job.URL != "https://microsoft.com/devicelogin" || job.Code != "ABCD1234" {
		t.Fatalf("az parse failed: %+v", job)
	}

	job2 := &CLILoginJob{ID: "k", State: cliJobRunning}
	m.byID["k"] = job2
	for _, line := range []string{
		"Attempting to automatically open the SSO authorization page in your default browser.",
		"If the browser does not open or you wish to use a different device to authorize this request, open the following URL:",
		"",
		"https://device.sso.us-east-1.amazonaws.com/",
		"",
		"Then enter the code:",
		"",
		"QWER-TYUI",
	} {
		m.appendLine("k", line)
	}
	if job2.URL != "https://device.sso.us-east-1.amazonaws.com/" || job2.Code != "QWER-TYUI" {
		t.Fatalf("aws parse failed: %+v", job2)
	}
}

func TestGKEContextNameMatchesImportedContext(t *testing.T) {
	if got := gkeContextName("proj", "europe-west1", "demo"); got != "gke_proj_europe-west1_demo" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractResourceGroup(t *testing.T) {
	id := "/subscriptions/sub/resourceGroups/my-rg/providers/Microsoft.ContainerService/managedClusters/aks1"
	if got := extractResourceGroup(id); got != "my-rg" {
		t.Fatalf("got %q", got)
	}
}

func TestRefreshRejectionMarksSessionExpiredUntilFileChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := NewAWSProvider()
	now := time.Now()
	startURL := "https://example.awsapps.com/start"
	key := kanivetSSOSessionName(startURL)
	writeTestToken(t, key, ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "a", ExpiresAt: rfc3339(now.Add(-time.Hour)), RefreshToken: "r", ClientID: "c", ClientSecret: "s", RegistrationExpiresAt: rfc3339(now.Add(48 * time.Hour))})
	p.tokens.invalidate()
	tok := p.tokens.best(startURL, now)
	if tok == nil || !tok.refreshable(now) {
		t.Fatalf("expected refreshable token, got %+v", tok)
	}

	p.tokens.markRefreshFailed(*tok, "rejected")
	p.tokens.invalidate()
	status := p.sessionStatus(startURL, "", now)
	if status.State != SSOStateExpired || status.Error == "" || status.Refreshable {
		t.Fatalf("expected expired with reason after rejection, got %+v", status)
	}

	// A new login rewrites the file; the mark must clear.
	time.Sleep(20 * time.Millisecond)
	path := writeTestToken(t, key, ssoTokenCache{StartURL: startURL, Region: "eu-west-1", AccessToken: "b", ExpiresAt: rfc3339(now.Add(time.Hour)), RefreshToken: "r2", ClientID: "c", ClientSecret: "s"})
	future := now.Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)
	p.tokens.invalidate()
	status = p.sessionStatus(startURL, "", now)
	if status.State != SSOStateActive || status.Error != "" {
		t.Fatalf("expected active after re-login, got %+v", status)
	}
}

func TestGetSSOSessionsHidesStaleCacheOnlyPortals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestToken(t, "old-conference", ssoTokenCache{StartURL: "https://old.awsapps.com/start", Region: "eu-west-1", AccessToken: "t", ExpiresAt: rfc3339(time.Now().Add(-30 * 24 * time.Hour))})
	writeTestToken(t, "recent", ssoTokenCache{StartURL: "https://recent.awsapps.com/start", Region: "eu-west-1", AccessToken: "t", ExpiresAt: rfc3339(time.Now().Add(-2 * time.Hour))})
	p := NewAWSProvider()
	sessions := p.GetSSOSessions()
	if len(sessions) != 1 || sessions[0].StartURL != "https://recent.awsapps.com/start" {
		t.Fatalf("expected only the recent portal, got %+v", sessions)
	}
}

func TestDescribeClusterAuthSuggestsProfilesForProfileLessEKSContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = us-east-1

[profile prod-admin]
sso_session = corp
sso_account_id = 227778349147
sso_role_name = Admin

[profile other]
sso_session = corp
sso_account_id = 111111111111
sso_role_name = Admin
`
	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	kubeconfigPath := filepath.Join(home, "kubeconfig")
	kubeconfig := clientcmdapi.NewConfig()
	name := "arn:aws:eks:eu-west-2:227778349147:cluster/plat-prod"
	kubeconfig.Clusters[name] = &clientcmdapi.Cluster{Server: "https://abc.eu-west-2.eks.amazonaws.com"}
	kubeconfig.AuthInfos[name] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws", Args: []string{"eks", "get-token", "--cluster-name", "plat-prod"}}}
	kubeconfig.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}
	info, err := NewAWSProvider().describeClusterAuth(name, kubeconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Method != "aws-default-chain" || len(info.MatchingProfiles) != 1 || info.MatchingProfiles[0] != "prod-admin" {
		t.Fatalf("unexpected info: %+v", info)
	}
}
