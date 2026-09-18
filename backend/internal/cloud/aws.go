package cloud

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	jsonv2 "encoding/json/v2"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/kanivet/backend/internal/faults"
	"gopkg.in/ini.v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func normalizeStartURL(startURL string) string {
	parsed, err := url.Parse(startURL)
	if err != nil {
		return strings.TrimSuffix(strings.TrimSuffix(startURL, "#"), "/")
	}
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}

type AWSProvider struct {
	configs        map[string]aws.Config
	ssoCredentials map[string]*ssoCredCache
	mu             sync.RWMutex
	kubeconfigMu   sync.Mutex
	awsConfigMu    sync.Mutex
	accessTestSem  chan struct{}
}

type ssoCredCache struct {
	accessToken string
	region      string
	expiresAt   time.Time
}

type ssoTokenCache struct {
	StartURL              string `json:"startUrl"`
	Region                string `json:"region"`
	AccessToken           string `json:"accessToken"`
	ExpiresAt             string `json:"expiresAt"`
	RefreshToken          string `json:"refreshToken,omitempty"`
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"`
	RegistrationExpiresAt string `json:"registrationExpiresAt,omitempty"`
}

type ssoClientRegistration struct {
	ClientID              string
	ClientSecret          string
	RegistrationExpiresAt int64
}

type legacySSOProfile struct {
	name, startURL, region, accountID, roleName string
}

const kanivetSSORegistrationScope = "sso:account:access"

func kanivetSSOSessionName(startURL string) string {
	return fmt.Sprintf("kanivet-sso-%x", sha1.Sum([]byte(normalizeStartURL(startURL))))
}

func buildSSOTokenCache(now time.Time, startURL, region string, registration ssoClientRegistration, accessToken string, refreshToken *string, expiresIn int32) (ssoTokenCache, time.Time) {
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second).UTC()
	token := ssoTokenCache{
		StartURL:     startURL,
		Region:       region,
		AccessToken:  accessToken,
		ExpiresAt:    expiresAt.Format(time.RFC3339),
		ClientID:     registration.ClientID,
		ClientSecret: registration.ClientSecret,
	}
	if refreshToken != nil {
		token.RefreshToken = *refreshToken
	}
	if registration.RegistrationExpiresAt > 0 {
		token.RegistrationExpiresAt = time.Unix(registration.RegistrationExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return token, expiresAt
}

func loadINIFile(path string) (*ini.File, error) {
	cfg, err := ini.LooseLoad(path)
	if err == nil {
		return cfg, nil
	}
	if os.IsNotExist(err) {
		return ini.Empty(), nil
	}
	return nil, err
}

func fileModeOrDefault(path string, fallback os.FileMode) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return fallback
	}
	return info.Mode().Perm()
}

func writeFileAtomically(path string, perm os.FileMode, write func(*os.File) error) (err error) {
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := write(tmp); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func saveINIFileAtomically(path string, cfg *ini.File, fallbackPerm os.FileMode) error {
	return writeFileAtomically(path, fileModeOrDefault(path, fallbackPerm), func(f *os.File) error {
		_, err := cfg.WriteTo(f)
		return err
	})
}

func legacyKanivetInlineProfileCacheKeys(cfg *ini.File, ssoStartURL string) []string {
	normalizedURL := normalizeStartURL(ssoStartURL)
	keys := make([]string, 0, 1)
	for _, profile := range legacyKanivetInlineProfiles(cfg) {
		if normalizeStartURL(profile.startURL) == normalizedURL {
			keys = append(keys, profile.startURL)
		}
	}
	return keys
}

func legacyKanivetInlineProfiles(cfg *ini.File) []legacySSOProfile {
	var profiles []legacySSOProfile
	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == "DEFAULT" || strings.HasPrefix(name, "sso-session ") {
			continue
		}

		profileName := strings.TrimPrefix(name, "profile ")
		if !strings.HasPrefix(profileName, "kanivet-sso-") {
			continue
		}
		if section.Key("sso_session").String() != "" {
			continue
		}

		startURL := section.Key("sso_start_url").String()
		if startURL == "" {
			continue
		}
		profiles = append(profiles, legacySSOProfile{
			name:      profileName,
			startURL:  startURL,
			region:    section.Key("sso_region").String(),
			accountID: section.Key("sso_account_id").String(),
			roleName:  section.Key("sso_role_name").String(),
		})
	}
	return profiles
}

func NewAWSProvider() *AWSProvider {
	p := &AWSProvider{
		configs:        make(map[string]aws.Config),
		ssoCredentials: make(map[string]*ssoCredCache),
		accessTestSem:  make(chan struct{}, 3),
	}
	p.loadCachedSSOTokens()
	p.migrateLegacySSOProfiles()
	p.migrateImportedEKSExecAuth()
	return p
}

func (p *AWSProvider) loadCachedSSOTokens() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	cacheDir := filepath.Join(home, ".aws", "sso", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if err != nil {
			continue
		}

		var cached ssoTokenCache
		if err := jsonv2.Unmarshal(data, &cached); err != nil || cached.StartURL == "" || cached.AccessToken == "" {
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, cached.ExpiresAt)
		if err != nil || time.Now().After(expiresAt) {
			continue
		}

		normalizedURL := normalizeStartURL(cached.StartURL)
		p.ssoCredentials[normalizedURL] = &ssoCredCache{
			accessToken: cached.AccessToken,
			region:      cached.Region,
			expiresAt:   expiresAt,
		}
		log.Printf("Loaded cached SSO token for %s (expires %s)", normalizedURL, expiresAt.Format(time.RFC3339))
	}
}

func getAllAWSRegions() []string {
	return []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"ca-central-1", "ca-west-1",
		"sa-east-1",
		"eu-west-1", "eu-west-2", "eu-west-3",
		"eu-central-1", "eu-central-2",
		"eu-north-1", "eu-south-1", "eu-south-2",
		"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
		"ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4",
		"ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
		"ap-south-1", "ap-south-2",
		"ap-east-1", "ap-east-2",
		"me-south-1", "me-central-1",
		"af-south-1",
		"il-central-1",
		"mx-central-1",
	}
}

func (p *AWSProvider) ListProfiles() ([]AWSProfile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var profiles []AWSProfile
	configPath := filepath.Join(home, ".aws", "config")
	credPath := filepath.Join(home, ".aws", "credentials")
	ssoSessions := make(map[string]struct {
		startURL string
		region   string
	})

	if cfg, err := ini.Load(configPath); err == nil {
		for _, section := range cfg.Sections() {
			name := section.Name()
			if strings.HasPrefix(name, "sso-session ") {
				sessionName := strings.TrimPrefix(name, "sso-session ")
				ssoSessions[sessionName] = struct {
					startURL string
					region   string
				}{
					startURL: section.Key("sso_start_url").String(),
					region:   section.Key("sso_region").String(),
				}
			}
		}

		for _, section := range cfg.Sections() {
			name := section.Name()
			if name == "DEFAULT" || strings.HasPrefix(name, "sso-session ") {
				continue
			}
			name = strings.TrimPrefix(name, "profile ")
			if strings.HasPrefix(name, "kanivet-") {
				continue
			}
			profile := AWSProfile{Name: name, Source: ProfileSourceConfig}
			if region := section.Key("region").String(); region != "" {
				profile.Region = region
			}
			if ssoSession := section.Key("sso_session").String(); ssoSession != "" {
				profile.IsSSO = true
				profile.SSOSession = ssoSession
				if sess, ok := ssoSessions[ssoSession]; ok {
					profile.SSOStartURL = sess.startURL
					if profile.Region == "" {
						profile.Region = sess.region
					}
				}
			} else if startURL := section.Key("sso_start_url").String(); startURL != "" {
				profile.IsSSO = true
				profile.SSOStartURL = startURL
				if profile.Region == "" {
					profile.Region = section.Key("sso_region").String()
				}
			}
			if roleArn := section.Key("role_arn").String(); roleArn != "" {
				profile.RoleArn = roleArn
			}
			if accountID := section.Key("aws_account_id").String(); accountID != "" {
				profile.AccountID = accountID
			} else if ssoAccountID := section.Key("sso_account_id").String(); ssoAccountID != "" {
				profile.AccountID = ssoAccountID
			}
			profiles = append(profiles, profile)
		}
	}

	if creds, err := ini.Load(credPath); err == nil {
		for _, section := range creds.Sections() {
			name := section.Name()
			if name == "DEFAULT" || strings.HasPrefix(name, "kanivet-") {
				continue
			}
			found := false
			for i := range profiles {
				if profiles[i].Name == name {
					found = true
					break
				}
			}
			if !found {
				profiles = append(profiles, AWSProfile{Name: name, Source: ProfileSourceCredentials})
			}
		}
	}

	if len(profiles) == 0 {
		profiles = append(profiles, AWSProfile{Name: "default"})
	}

	return profiles, nil
}

func (p *AWSProvider) GetProfileRegion(profile string) string {
	profiles, err := p.ListProfiles()
	if err != nil {
		return "us-east-1"
	}
	for _, pr := range profiles {
		if pr.Name == profile && pr.Region != "" {
			return pr.Region
		}
	}
	return "us-east-1"
}

func (p *AWSProvider) GetConfig(ctx context.Context, profile, region string) (aws.Config, error) {
	key := fmt.Sprintf("%s:%s", profile, region)
	p.mu.RLock()
	if cfg, ok := p.configs[key]; ok {
		p.mu.RUnlock()
		return cfg, nil
	}
	p.mu.RUnlock()

	opts := []func(*config.LoadOptions) error{config.WithSharedConfigProfile(profile)}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS config for profile %s: %w", profile, err)
	}

	p.mu.Lock()
	p.configs[key] = cfg
	p.mu.Unlock()

	return cfg, nil
}

func (p *AWSProvider) GetAccountID(ctx context.Context, profile string) (string, error) {
	region := p.GetProfileRegion(profile)
	cfg, err := p.GetConfig(ctx, profile, region)
	if err != nil {
		return "", err
	}
	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get caller identity: %w", err)
	}
	return *identity.Account, nil
}

func (p *AWSProvider) StartSSOLogin(ctx context.Context, startURL, region string) (*SSOLoginResponse, error) {
	normalizedURL := normalizeStartURL(startURL)
	p.mu.Lock()
	delete(p.ssoCredentials, normalizedURL)
	p.mu.Unlock()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	oidcClient := ssooidc.NewFromConfig(cfg)
	registerResp, err := oidcClient.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String("kanivet"),
		ClientType: aws.String("public"),
		Scopes:     []string{kanivetSSORegistrationScope},
	})
	if err != nil {
		faults.CaptureExceptionWithContext(err, map[string]any{
			"operation": "sso_register_client",
			"region":    region,
		})
		return nil, fmt.Errorf("failed to register OIDC client: %w", err)
	}

	startAuthResp, err := oidcClient.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     registerResp.ClientId,
		ClientSecret: registerResp.ClientSecret,
		StartUrl:     aws.String(startURL),
	})
	if err != nil {
		faults.CaptureExceptionWithContext(err, map[string]any{
			"operation": "sso_device_authorization",
			"region":    region,
		})
		return nil, fmt.Errorf("failed to start device authorization: %w", err)
	}

	verifyURL := aws.ToString(startAuthResp.VerificationUriComplete)
	if err := exec.Command("open", verifyURL).Start(); err != nil {
		log.Printf("Failed to open browser: %v", err)
	}

	interval := time.Duration(startAuthResp.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(startAuthResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		tokenResp, err := oidcClient.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     registerResp.ClientId,
			ClientSecret: registerResp.ClientSecret,
			DeviceCode:   startAuthResp.DeviceCode,
			GrantType:    aws.String("urn:ietf:params:oauth:grant-type:device_code"),
		})
		if err != nil {
			if strings.Contains(err.Error(), "AuthorizationPendingException") ||
				strings.Contains(err.Error(), "SlowDownException") {
				time.Sleep(interval)
				continue
			}
			faults.CaptureExceptionWithContext(err, map[string]any{
				"operation": "sso_create_token",
				"region":    region,
			})
			return nil, fmt.Errorf("failed to create token: %w", err)
		}

		accessToken := aws.ToString(tokenResp.AccessToken)
		tokenCache, expiresAt := buildSSOTokenCache(
			time.Now(),
			startURL,
			region,
			ssoClientRegistration{
				ClientID:              aws.ToString(registerResp.ClientId),
				ClientSecret:          aws.ToString(registerResp.ClientSecret),
				RegistrationExpiresAt: registerResp.ClientSecretExpiresAt,
			},
			accessToken,
			tokenResp.RefreshToken,
			tokenResp.ExpiresIn,
		)
		if err := p.cacheSSOToken(tokenCache); err != nil {
			log.Printf("Failed to cache SSO token: %v", err)
		}

		normalizedURL := normalizeStartURL(startURL)
		p.mu.Lock()
		p.ssoCredentials[normalizedURL] = &ssoCredCache{
			accessToken: accessToken,
			region:      region,
			expiresAt:   expiresAt,
		}
		p.mu.Unlock()

		return &SSOLoginResponse{
			DeviceCode:      *startAuthResp.DeviceCode,
			UserCode:        *startAuthResp.UserCode,
			VerificationURL: verifyURL,
			ExpiresIn:       int(tokenResp.ExpiresIn),
		}, nil
	}

	return nil, fmt.Errorf("SSO login timed out - please complete authentication in your browser")
}

func writeSSOTokenCache(key string, token ssoTokenCache) error {
	data, err := jsonv2.Marshal(token)
	if err != nil {
		return err
	}

	cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		return err
	}
	return writeFileAtomically(cachePath, 0600, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

func (p *AWSProvider) cacheSSOToken(token ssoTokenCache) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cacheDir := filepath.Join(home, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return err
	}

	keys := []string{kanivetSSOSessionName(token.StartURL)}
	if cfg, err := loadINIFile(filepath.Join(home, ".aws", "config")); err == nil {
		keys = append(keys, legacyKanivetInlineProfileCacheKeys(cfg, token.StartURL)...)
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := writeSSOTokenCache(key, token); err != nil {
			return err
		}
	}
	return nil
}

func readSSOTokenCache(key string) (ssoTokenCache, error) {
	cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		return ssoTokenCache{}, err
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return ssoTokenCache{}, err
	}
	var token ssoTokenCache
	if err := jsonv2.Unmarshal(data, &token); err != nil {
		return ssoTokenCache{}, err
	}
	if token.StartURL == "" || token.AccessToken == "" {
		return ssoTokenCache{}, fmt.Errorf("invalid SSO token cache")
	}
	return token, nil
}

func saveKubeconfigAtomically(path string, cfg *clientcmdapi.Config) error {
	data, err := clientcmd.Write(*cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeFileAtomically(path, fileModeOrDefault(path, 0600), func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

func (p *AWSProvider) migrateLegacySSOProfiles() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	cfg, err := loadINIFile(filepath.Join(home, ".aws", "config"))
	if err != nil {
		log.Printf("Warning: failed to inspect legacy Kanivet SSO profiles: %v", err)
		return
	}

	for _, profile := range legacyKanivetInlineProfiles(cfg) {
		if profile.region == "" || profile.accountID == "" || profile.roleName == "" {
			continue
		}
		legacyToken, legacyTokenErr := readSSOTokenCache(profile.startURL)
		if err := p.ensureSSOProfile(profile.name, profile.startURL, profile.region, profile.accountID, profile.roleName); err != nil {
			log.Printf("Warning: failed to migrate legacy Kanivet SSO profile %s: %v", profile.name, err)
			continue
		}

		sessionName := kanivetSSOSessionName(profile.startURL)
		if _, err := readSSOTokenCache(sessionName); err == nil || legacyTokenErr != nil {
			continue
		}
		if err := writeSSOTokenCache(sessionName, legacyToken); err != nil {
			log.Printf("Warning: failed to migrate legacy Kanivet SSO token cache for %s: %v", profile.name, err)
		}
	}
}

func isImportedEKSContext(name string, ctx *clientcmdapi.Context) bool {
	return ctx != nil && (strings.HasPrefix(name, "arn:aws:eks:") || strings.HasPrefix(ctx.Cluster, "arn:aws:eks:"))
}

func needsNonInteractiveImportedEKSExec(authInfo *clientcmdapi.AuthInfo) bool {
	if authInfo == nil || authInfo.Exec == nil || authInfo.Exec.InteractiveMode == clientcmdapi.NeverExecInteractiveMode || filepath.Base(authInfo.Exec.Command) != "aws" {
		return false
	}
	args := authInfo.Exec.Args
	return len(args) >= 2 && args[0] == "eks" && args[1] == "get-token"
}

func (p *AWSProvider) migrateImportedEKSExecAuth() {
	kubeconfigPath := KanivetKubeconfigPath()
	kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to inspect imported EKS kubeconfig entries: %v", err)
		}
		return
	}

	changed := false
	seen := make(map[string]struct{}, len(kubeconfig.Contexts))
	for name, ctx := range kubeconfig.Contexts {
		if !isImportedEKSContext(name, ctx) || ctx.AuthInfo == "" {
			continue
		}
		if _, ok := seen[ctx.AuthInfo]; ok {
			continue
		}
		seen[ctx.AuthInfo] = struct{}{}
		authInfo := kubeconfig.AuthInfos[ctx.AuthInfo]
		if !needsNonInteractiveImportedEKSExec(authInfo) {
			continue
		}
		authInfo.Exec.InteractiveMode = clientcmdapi.NeverExecInteractiveMode
		changed = true
	}
	if !changed {
		return
	}
	if err := saveKubeconfigAtomically(kubeconfigPath, kubeconfig); err != nil {
		log.Printf("Warning: failed to migrate imported EKS kubeconfig auth: %v", err)
	}
}

func (p *AWSProvider) GetSSOAccounts(ctx context.Context, startURL string) ([]SSOAccount, error) {
	normalizedURL := normalizeStartURL(startURL)
	p.mu.RLock()
	cred, ok := p.ssoCredentials[normalizedURL]
	p.mu.RUnlock()
	if !ok || time.Now().After(cred.expiresAt) {
		return nil, fmt.Errorf("SSO session expired or not found - please login again")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cred.region))
	if err != nil {
		return nil, err
	}

	ssoClient := sso.NewFromConfig(cfg)
	var accounts []SSOAccount
	paginator := sso.NewListAccountsPaginator(ssoClient, &sso.ListAccountsInput{
		AccessToken: aws.String(cred.accessToken),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if !strings.Contains(err.Error(), "ExpiredToken") && !strings.Contains(err.Error(), "UnauthorizedException") {
				faults.CaptureExceptionWithContext(err, map[string]any{
					"operation": "sso_list_accounts",
				})
			}
			return nil, fmt.Errorf("failed to list SSO accounts: %w", err)
		}
		for _, acc := range page.AccountList {
			accounts = append(accounts, SSOAccount{
				AccountID:   aws.ToString(acc.AccountId),
				AccountName: aws.ToString(acc.AccountName),
				EmailAddr:   aws.ToString(acc.EmailAddress),
			})
		}
	}
	return accounts, nil
}

func (p *AWSProvider) GetSSORoles(ctx context.Context, startURL, accountID string) ([]string, error) {
	normalizedURL := normalizeStartURL(startURL)
	p.mu.RLock()
	cred, ok := p.ssoCredentials[normalizedURL]
	p.mu.RUnlock()
	if !ok || time.Now().After(cred.expiresAt) {
		return nil, fmt.Errorf("SSO session expired or not found - please login again")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cred.region))
	if err != nil {
		return nil, err
	}

	ssoClient := sso.NewFromConfig(cfg)
	var roles []string
	paginator := sso.NewListAccountRolesPaginator(ssoClient, &sso.ListAccountRolesInput{
		AccessToken: aws.String(cred.accessToken),
		AccountId:   aws.String(accountID),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if !strings.Contains(err.Error(), "ExpiredToken") && !strings.Contains(err.Error(), "UnauthorizedException") {
				faults.CaptureExceptionWithContext(err, map[string]any{
					"operation": "sso_list_roles",
					"accountId": accountID,
				})
			}
			return nil, fmt.Errorf("failed to list SSO roles: %w", err)
		}
		for _, role := range page.RoleList {
			roles = append(roles, aws.ToString(role.RoleName))
		}
	}
	return roles, nil
}

func (p *AWSProvider) GetSSOCredentials(ctx context.Context, startURL, accountID, roleName string) (aws.Config, error) {
	normalizedURL := normalizeStartURL(startURL)
	p.mu.RLock()
	cred, ok := p.ssoCredentials[normalizedURL]
	p.mu.RUnlock()
	if !ok || time.Now().After(cred.expiresAt) {
		return aws.Config{}, fmt.Errorf("SSO session expired or not found - please login again")
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cred.region))
	if err != nil {
		return aws.Config{}, err
	}

	ssoClient := sso.NewFromConfig(cfg)
	roleCredsResp, err := ssoClient.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: aws.String(cred.accessToken),
		AccountId:   aws.String(accountID),
		RoleName:    aws.String(roleName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ExpiredToken") && !strings.Contains(err.Error(), "UnauthorizedException") {
			faults.CaptureExceptionWithContext(err, map[string]any{
				"operation": "sso_get_credentials",
				"accountId": accountID,
				"roleName":  roleName,
			})
		}
		return aws.Config{}, fmt.Errorf("failed to get role credentials: %w", err)
	}

	creds := roleCredsResp.RoleCredentials
	cfg.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(creds.AccessKeyId),
		aws.ToString(creds.SecretAccessKey),
		aws.ToString(creds.SessionToken),
	)

	return cfg, nil
}

func (p *AWSProvider) ActivateSSOAccount(ctx context.Context, startURL, accountID, roleName, region string) (*SSOActivateResponse, error) {
	normalizedURL := normalizeStartURL(startURL)
	p.mu.RLock()
	cred, ok := p.ssoCredentials[normalizedURL]
	p.mu.RUnlock()
	if !ok || time.Now().After(cred.expiresAt) {
		return nil, fmt.Errorf("SSO session expired or not found - please login again")
	}

	if roleName == "" {
		roles, err := p.GetSSORoles(ctx, startURL, accountID)
		if err != nil {
			return nil, fmt.Errorf("failed to get roles: %w", err)
		}
		if len(roles) == 0 {
			return nil, fmt.Errorf("no roles available for account %s", accountID)
		}
		roleName = roles[0]
		for _, r := range roles {
			if strings.Contains(strings.ToLower(r), "admin") || strings.Contains(strings.ToLower(r), "readonly") {
				roleName = r
				break
			}
		}
	}

	if region == "" {
		region = cred.region
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	ssoClient := sso.NewFromConfig(cfg)
	roleCredsResp, err := ssoClient.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: aws.String(cred.accessToken),
		AccountId:   aws.String(accountID),
		RoleName:    aws.String(roleName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get role credentials: %w", err)
	}

	creds := roleCredsResp.RoleCredentials
	accessKeyID := aws.ToString(creds.AccessKeyId)
	secretAccessKey := aws.ToString(creds.SecretAccessKey)
	sessionToken := aws.ToString(creds.SessionToken)
	expiresAt := creds.Expiration

	profileName := "default"
	if err := p.writeCredentialsFile(profileName, accessKeyID, secretAccessKey, sessionToken, region); err != nil {
		return nil, fmt.Errorf("failed to write credentials: %w", err)
	}

	return &SSOActivateResponse{
		ProfileName: profileName,
		AccountID:   accountID,
		RoleName:    roleName,
		Region:      region,
		AccessKeyID: accessKeyID,
		ExpiresAt:   expiresAt,
	}, nil
}

const kanivetBackupProfile = "kanivet-backup-original-default"

func (p *AWSProvider) writeCredentialsFile(profileName, accessKeyID, secretAccessKey, sessionToken, region string) error {
	p.awsConfigMu.Lock()
	defer p.awsConfigMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	awsDir := filepath.Join(home, ".aws")
	if err := os.MkdirAll(awsDir, 0700); err != nil {
		return err
	}

	credPath := filepath.Join(awsDir, "credentials")
	configPath := filepath.Join(awsDir, "config")

	credFile, err := ini.LooseLoad(credPath)
	if err != nil {
		credFile = ini.Empty()
	}

	if profileName == "default" {
		existingDefault := credFile.Section("default")
		if existingDefault != nil && existingDefault.HasKey("aws_access_key_id") {
			backupSection := credFile.Section(kanivetBackupProfile)
			if !backupSection.HasKey("aws_access_key_id") {
				for _, key := range existingDefault.Keys() {
					backupSection.Key(key.Name()).SetValue(key.Value())
				}
			}
		}
	}

	credSection := credFile.Section(profileName)
	credSection.Key("aws_access_key_id").SetValue(accessKeyID)
	credSection.Key("aws_secret_access_key").SetValue(secretAccessKey)
	credSection.Key("aws_session_token").SetValue(sessionToken)

	if err := credFile.SaveTo(credPath); err != nil {
		return fmt.Errorf("failed to save credentials file: %w", err)
	}

	configFile, err := ini.LooseLoad(configPath)
	if err != nil {
		configFile = ini.Empty()
	}

	if profileName == "default" {
		existingDefault := configFile.Section("default")
		if existingDefault != nil && existingDefault.HasKey("region") {
			backupSection := configFile.Section("profile " + kanivetBackupProfile)
			if !backupSection.HasKey("region") {
				for _, key := range existingDefault.Keys() {
					backupSection.Key(key.Name()).SetValue(key.Value())
				}
			}
		}
	}

	configSectionName := profileName
	if profileName != "default" {
		configSectionName = "profile " + profileName
	}
	configSection := configFile.Section(configSectionName)
	configSection.Key("region").SetValue(region)

	if err := configFile.SaveTo(configPath); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	return nil
}

func (p *AWSProvider) DeactivateSSOAccount(ctx context.Context) error {
	p.awsConfigMu.Lock()
	defer p.awsConfigMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	credPath := filepath.Join(home, ".aws", "credentials")
	configPath := filepath.Join(home, ".aws", "config")

	credFile, err := ini.LooseLoad(credPath)
	if err != nil {
		return nil
	}

	backupCreds := credFile.Section(kanivetBackupProfile)
	if backupCreds.HasKey("aws_access_key_id") {
		defaultSection := credFile.Section("default")
		for _, key := range defaultSection.Keys() {
			defaultSection.DeleteKey(key.Name())
		}
		for _, key := range backupCreds.Keys() {
			defaultSection.Key(key.Name()).SetValue(key.Value())
		}
		credFile.DeleteSection(kanivetBackupProfile)
	} else {
		credFile.DeleteSection("default")
	}

	if err := credFile.SaveTo(credPath); err != nil {
		return fmt.Errorf("failed to save credentials file: %w", err)
	}

	configFile, err := ini.LooseLoad(configPath)
	if err != nil {
		return nil
	}

	backupConfig := configFile.Section("profile " + kanivetBackupProfile)
	if backupConfig.HasKey("region") {
		defaultSection := configFile.Section("default")
		for _, key := range defaultSection.Keys() {
			defaultSection.DeleteKey(key.Name())
		}
		for _, key := range backupConfig.Keys() {
			defaultSection.Key(key.Name()).SetValue(key.Value())
		}
		configFile.DeleteSection("profile " + kanivetBackupProfile)
	}

	if err := configFile.SaveTo(configPath); err != nil {
		return fmt.Errorf("failed to save config file: %w", err)
	}

	return nil
}

func (p *AWSProvider) InvalidateSSOSession(startURL string) error {
	normalizedURL := normalizeStartURL(startURL)

	p.mu.Lock()
	delete(p.ssoCredentials, normalizedURL)
	p.mu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	keys := []string{startURL, normalizedURL, kanivetSSOSessionName(startURL)}
	if cfg, err := loadINIFile(filepath.Join(home, ".aws", "config")); err == nil {
		keys = append(keys, legacyKanivetInlineProfileCacheKeys(cfg, startURL)...)
	}

	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		cachePath, err := ssocreds.StandardCachedTokenFilepath(key)
		if err != nil {
			log.Printf("Warning: failed to resolve SSO cache path: %v", err)
			continue
		}
		if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove SSO cache file: %v", err)
		}
	}

	return nil
}

func (p *AWSProvider) DiscoverClustersWithSSO(ctx context.Context, startURL string, regions []string, accountIDs []string) ([]DiscoveredCluster, error) {
	accounts, err := p.GetSSOAccounts(ctx, startURL)
	if err != nil {
		return nil, err
	}

	if len(accountIDs) > 0 {
		filterSet := make(map[string]bool, len(accountIDs))
		for _, id := range accountIDs {
			filterSet[id] = true
		}
		filtered := make([]SSOAccount, 0, len(accountIDs))
		for _, acc := range accounts {
			if filterSet[acc.AccountID] {
				filtered = append(filtered, acc)
			}
		}
		accounts = filtered
	}

	if len(regions) == 0 {
		regions = getAllAWSRegions()
	}

	var allClusters []DiscoveredCluster
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, account := range accounts {
		roles, err := p.GetSSORoles(ctx, startURL, account.AccountID)
		if err != nil {
			log.Printf("Failed to get roles for account %s: %v", account.AccountID, err)
			continue
		}
		if len(roles) == 0 {
			continue
		}

		roleName := roles[0]
		for _, r := range roles {
			if strings.Contains(strings.ToLower(r), "admin") || strings.Contains(strings.ToLower(r), "readonly") {
				roleName = r
				break
			}
		}

		cfg, err := p.GetSSOCredentials(ctx, startURL, account.AccountID, roleName)
		if err != nil {
			log.Printf("Failed to get credentials for %s/%s: %v", account.AccountID, roleName, err)
			continue
		}

		for _, region := range regions {
			wg.Add(1)
			go func(acc SSOAccount, baseCfg aws.Config, r string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				regionCfg := baseCfg.Copy()
				regionCfg.Region = r
				eksClient := eks.NewFromConfig(regionCfg)

				var clusterNames []string
				var nextToken *string
				for {
					listResp, err := eksClient.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
					if err != nil {
						return
					}
					clusterNames = append(clusterNames, listResp.Clusters...)
					if listResp.NextToken == nil {
						break
					}
					nextToken = listResp.NextToken
				}

				for _, name := range clusterNames {
					descResp, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
					if err != nil {
						continue
					}
					c := descResp.Cluster

					cluster := DiscoveredCluster{
						ID:        fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", r, acc.AccountID, name),
						Name:      name,
						Provider:  ProviderAWS,
						Region:    r,
						AccountID: acc.AccountID,
						Endpoint:  aws.ToString(c.Endpoint),
						Status:    string(c.Status),
					}
					if c.Version != nil {
						cluster.Version = *c.Version
					}
					if c.Tags != nil {
						cluster.Tags = c.Tags
					}
					mu.Lock()
					allClusters = append(allClusters, cluster)
					mu.Unlock()
				}
			}(account, cfg, region)
		}
	}

	wg.Wait()
	return allClusters, nil
}

func (p *AWSProvider) LoginWithProfile(ctx context.Context, profile string) error {
	profiles, err := p.ListProfiles()
	if err != nil {
		return err
	}

	var targetProfile *AWSProfile
	for _, pr := range profiles {
		if pr.Name == profile {
			targetProfile = &pr
			break
		}
	}
	if targetProfile == nil {
		return fmt.Errorf("profile %s not found", profile)
	}

	if targetProfile.IsSSO {
		cmd := exec.CommandContext(ctx, "aws", "sso", "login", "--profile", profile)
		cmd.Env = os.Environ()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("SSO login failed: %w", err)
		}
	}

	_, err = p.GetAccountID(ctx, profile)
	return err
}

func (p *AWSProvider) DiscoverClusters(ctx context.Context, profile string, regions []string) ([]DiscoveredCluster, error) {
	if len(regions) == 0 {
		regions = getAllAWSRegions()
	}

	accountID, err := p.GetAccountID(ctx, profile)
	if err != nil {
		log.Printf("Warning: could not get account ID: %v", err)
	}

	var clusters []DiscoveredCluster
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			regionClusters, err := p.discoverClustersInRegion(ctx, profile, r, accountID)
			if err != nil {
				log.Printf("Error discovering clusters in %s: %v", r, err)
				return
			}
			mu.Lock()
			clusters = append(clusters, regionClusters...)
			mu.Unlock()
		}(region)
	}
	wg.Wait()

	return clusters, nil
}

func (p *AWSProvider) discoverClustersInRegion(ctx context.Context, profile, region, accountID string) ([]DiscoveredCluster, error) {
	cfg, err := p.GetConfig(ctx, profile, region)
	if err != nil {
		return nil, err
	}

	eksClient := eks.NewFromConfig(cfg)

	var clusterNames []string
	var nextToken *string
	for {
		listResp, err := eksClient.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
		if err != nil {
			return nil, err
		}
		clusterNames = append(clusterNames, listResp.Clusters...)
		if listResp.NextToken == nil {
			break
		}
		nextToken = listResp.NextToken
	}

	var clusters []DiscoveredCluster
	for _, name := range clusterNames {
		descResp, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
		if err != nil {
			log.Printf("Error describing cluster %s: %v", name, err)
			continue
		}
		c := descResp.Cluster

		cluster := DiscoveredCluster{
			ID:        fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", region, accountID, name),
			Name:      name,
			Provider:  ProviderAWS,
			Region:    region,
			AccountID: accountID,
			Endpoint:  aws.ToString(c.Endpoint),
			Status:    string(c.Status),
		}
		if c.Version != nil {
			cluster.Version = *c.Version
		}
		if c.Tags != nil {
			cluster.Tags = c.Tags
		}

		caData := aws.ToString(c.CertificateAuthority.Data)
		hasAccess, accessErr := p.testClusterAccess(ctx, name, cluster.Endpoint, caData, region, profile, "", "", "")
		cluster.HasAccess = hasAccess
		cluster.AccessError = accessErr

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

func (p *AWSProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	log.Printf("[ImportCluster] Starting import: name=%s, region=%s, accountId=%s, ssoStartUrl=%s, profile=%s, ssoRoleName=%s",
		req.Name, req.Region, req.AccountID, req.SSOStartURL, req.Profile, req.SSORoleName)

	var cfg aws.Config
	var err error
	var ssoRoleName string

	if req.SSOStartURL != "" {
		log.Printf("[ImportCluster] Using SSO authentication with startUrl=%s", req.SSOStartURL)
		if req.SSORoleName != "" {
			ssoRoleName = req.SSORoleName
			log.Printf("[ImportCluster] Using user-specified role: %s", ssoRoleName)
		} else {
			roles, rolesErr := p.GetSSORoles(ctx, req.SSOStartURL, req.AccountID)
			if rolesErr != nil || len(roles) == 0 {
				log.Printf("[ImportCluster] ERROR: Failed to get SSO roles: %v", rolesErr)
				return fmt.Errorf("failed to get SSO roles for account %s: %v", req.AccountID, rolesErr)
			}
			log.Printf("[ImportCluster] Found %d roles: %v", len(roles), roles)
			ssoRoleName = roles[0]
			for _, r := range roles {
				if strings.Contains(strings.ToLower(r), "admin") || strings.Contains(strings.ToLower(r), "readonly") {
					ssoRoleName = r
					break
				}
			}
			log.Printf("[ImportCluster] Auto-selected role: %s", ssoRoleName)
		}
		cfg, err = p.GetSSOCredentials(ctx, req.SSOStartURL, req.AccountID, ssoRoleName)
		if err != nil {
			log.Printf("[ImportCluster] ERROR: Failed to get SSO credentials: %v", err)
			return fmt.Errorf("failed to get SSO credentials: %w", err)
		}
		cfg.Region = req.Region
	} else if req.Profile != "" {
		log.Printf("[ImportCluster] Using profile authentication with profile=%s", req.Profile)
		cfg, err = p.GetConfig(ctx, req.Profile, req.Region)
		if err != nil {
			log.Printf("[ImportCluster] ERROR: Failed to get config for profile: %v", err)
			return err
		}
	} else {
		log.Printf("[ImportCluster] WARNING: No SSO URL or profile specified")
		cfg, err = p.GetConfig(ctx, "default", req.Region)
		if err != nil {
			log.Printf("[ImportCluster] ERROR: Failed to get default config: %v", err)
			return err
		}
	}

	log.Printf("[ImportCluster] Describing cluster %s in region %s", req.Name, req.Region)
	eksClient := eks.NewFromConfig(cfg)
	descResp, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(req.Name)})
	if err != nil {
		log.Printf("[ImportCluster] ERROR: Failed to describe cluster: %v", err)
		return fmt.Errorf("failed to describe cluster: %w", err)
	}
	cluster := descResp.Cluster
	log.Printf("[ImportCluster] Cluster described successfully: endpoint=%s", aws.ToString(cluster.Endpoint))

	kubeconfigPath := KanivetKubeconfigPath()
	log.Printf("[ImportCluster] Loading kubeconfig from %s", kubeconfigPath)

	p.kubeconfigMu.Lock()
	defer p.kubeconfigMu.Unlock()

	var kubeconfig *clientcmdapi.Config

	if _, statErr := os.Stat(kubeconfigPath); os.IsNotExist(statErr) {
		log.Printf("[ImportCluster] Kubeconfig does not exist, creating new one")
		kubeconfig = clientcmdapi.NewConfig()
	} else {
		var loadErr error
		kubeconfig, loadErr = clientcmd.LoadFromFile(kubeconfigPath)
		if loadErr != nil {
			log.Printf("[ImportCluster] ERROR: Failed to load existing kubeconfig: %v", loadErr)
			return fmt.Errorf("failed to load existing kubeconfig: %w", loadErr)
		}
	}

	clusterName := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", req.Region, req.AccountID, req.Name)
	contextName := clusterName
	caData, _ := base64.StdEncoding.DecodeString(aws.ToString(cluster.CertificateAuthority.Data))
	log.Printf("[ImportCluster] Using cluster/context name: %s", clusterName)

	kubeconfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   aws.ToString(cluster.Endpoint),
		CertificateAuthorityData: caData,
	}

	authInfo := &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:      "client.authentication.k8s.io/v1beta1",
			Command:         "aws",
			Args:            []string{"eks", "get-token", "--cluster-name", req.Name, "--region", req.Region},
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		},
	}
	if req.SSOStartURL != "" {
		profileName := fmt.Sprintf("kanivet-sso-%s-%s", req.AccountID, ssoRoleName)
		log.Printf("[ImportCluster] Creating SSO profile: %s", profileName)
		normalizedURL := normalizeStartURL(req.SSOStartURL)
		p.mu.RLock()
		cred, ok := p.ssoCredentials[normalizedURL]
		p.mu.RUnlock()
		ssoRegion := req.Region
		if ok && cred.region != "" {
			ssoRegion = cred.region
		}
		if err := p.ensureSSOProfile(profileName, req.SSOStartURL, ssoRegion, req.AccountID, ssoRoleName); err != nil {
			log.Printf("[ImportCluster] WARNING: Failed to create SSO profile: %v", err)
		} else {
			log.Printf("[ImportCluster] SSO profile created/verified successfully")
		}
		authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{
			{Name: "AWS_PROFILE", Value: profileName},
		}
	} else if req.Profile != "" {
		log.Printf("[ImportCluster] Using existing profile: %s", req.Profile)
		authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{
			{Name: "AWS_PROFILE", Value: req.Profile},
		}
	} else {
		log.Printf("[ImportCluster] WARNING: No profile or SSO URL, cluster may not authenticate properly")
	}
	kubeconfig.AuthInfos[clusterName] = authInfo

	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}

	log.Printf("[ImportCluster] Writing kubeconfig to %s", kubeconfigPath)
	if err := clientcmd.WriteToFile(*kubeconfig, kubeconfigPath); err != nil {
		log.Printf("[ImportCluster] ERROR: Failed to write kubeconfig: %v", err)
		return err
	}
	log.Printf("[ImportCluster] SUCCESS: Cluster %s imported successfully", req.Name)
	return nil
}

func (p *AWSProvider) AssumeRole(ctx context.Context, profile, roleArn, sessionName string) (aws.Config, error) {
	cfg, err := p.GetConfig(ctx, profile, "")
	if err != nil {
		return aws.Config{}, err
	}

	stsClient := sts.NewFromConfig(cfg)
	creds := stscreds.NewAssumeRoleProvider(stsClient, roleArn, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = sessionName
		o.Duration = time.Hour
	})

	cfg.Credentials = aws.NewCredentialsCache(creds)
	return cfg, nil
}

func (p *AWSProvider) RefreshSSOCredentials(ctx context.Context, profile string) error {
	return p.LoginWithProfile(ctx, profile)
}

func (p *AWSProvider) ensureSSOProfile(profileName, ssoStartURL, ssoRegion, accountID, roleName string) error {
	p.awsConfigMu.Lock()
	defer p.awsConfigMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(home, ".aws", "config")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	cfg, err := loadINIFile(configPath)
	if err != nil {
		return err
	}
	ssoSession := kanivetSSOSessionName(ssoStartURL)
	sessionSection := cfg.Section("sso-session " + ssoSession)
	sessionSection.Key("sso_start_url").SetValue(ssoStartURL)
	sessionSection.Key("sso_region").SetValue(ssoRegion)
	sessionSection.Key("sso_registration_scopes").SetValue(kanivetSSORegistrationScope)
	profileSection := cfg.Section("profile " + profileName)
	profileSection.Key("sso_session").SetValue(ssoSession)
	profileSection.Key("sso_account_id").SetValue(accountID)
	profileSection.Key("sso_role_name").SetValue(roleName)
	profileSection.Key("region").SetValue(ssoRegion)
	profileSection.DeleteKey("sso_start_url")
	profileSection.DeleteKey("sso_region")
	profileSection.DeleteKey("sso_registration_scopes")
	return saveINIFileAtomically(configPath, cfg, 0600)
}

type SSOSessionStatus struct {
	StartURL  string `json:"startUrl"`
	Region    string `json:"region"`
	ExpiresAt int64  `json:"expiresAt"`
	IsValid   bool   `json:"isValid"`
	Label     string `json:"label,omitempty"`
}

func (p *AWSProvider) GetSSOSessions() []SSOSessionStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var sessions []SSOSessionStatus
	now := time.Now()
	for startURL, cred := range p.ssoCredentials {
		sessions = append(sessions, SSOSessionStatus{
			StartURL:  startURL,
			Region:    cred.region,
			ExpiresAt: cred.expiresAt.UnixMilli(),
			IsValid:   now.Before(cred.expiresAt),
		})
	}
	return sessions
}

func (p *AWSProvider) GetImportedClusterIDs() []string {
	kubeconfig, err := clientcmd.LoadFromFile(KanivetKubeconfigPath())
	if err != nil {
		return nil
	}
	var ids []string
	for contextName := range kubeconfig.Contexts {
		ids = append(ids, contextName)
	}
	return ids
}

func (p *AWSProvider) DiscoverClustersStreaming(ctx context.Context, profile string, regions []string, eventCh chan<- DiscoveryEvent) {
	defer close(eventCh)
	if len(regions) == 0 {
		regions = getAllAWSRegions()
	}

	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{
		Status: fmt.Sprintf("Preparing profile %s...", profile), TotalRegions: len(regions),
	}}

	accountID, err := p.GetAccountID(ctx, profile)
	if err != nil {
		log.Printf("Warning: could not get account ID: %v", err)
	}

	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{
		Status: "Scanning regions...", TotalRegions: len(regions),
	}}

	var wg sync.WaitGroup
	var accessWg sync.WaitGroup
	var scannedCount int
	var clustersFound int
	var mu sync.Mutex
	sem := make(chan struct{}, 5)
	totalRegions := len(regions)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cfg, err := p.GetConfig(ctx, profile, r)
			if err != nil {
				mu.Lock()
				scannedCount++
				currentScanned := scannedCount
				currentClusters := clustersFound
				mu.Unlock()
				eventCh <- DiscoveryEvent{
					Type: DiscoveryEventProgress,
					Progress: &DiscoveryProgress{
						Region: r, AccountID: accountID, RegionsScanned: currentScanned, TotalRegions: totalRegions, ClustersFound: currentClusters,
					},
				}
				return
			}

			eksClient := eks.NewFromConfig(cfg)
			var clusterNames []string
			var nextToken *string
			for {
				listResp, listErr := eksClient.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
				if listErr != nil {
					break
				}
				clusterNames = append(clusterNames, listResp.Clusters...)
				if listResp.NextToken == nil {
					break
				}
				nextToken = listResp.NextToken
			}

			for _, name := range clusterNames {
				descResp, descErr := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
				if descErr != nil {
					continue
				}
				c := descResp.Cluster
				cluster := DiscoveredCluster{
					ID:            fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", r, accountID, name),
					Name:          name,
					Provider:      ProviderAWS,
					Region:        r,
					AccountID:     accountID,
					Endpoint:      aws.ToString(c.Endpoint),
					Status:        string(c.Status),
					Profile:       profile,
					AccessChecked: false,
				}
				if c.Version != nil {
					cluster.Version = *c.Version
				}
				if c.Tags != nil {
					cluster.Tags = c.Tags
				}

				mu.Lock()
				clustersFound++
				mu.Unlock()
				eventCh <- DiscoveryEvent{Type: DiscoveryEventCluster, Cluster: &cluster}

				caData := aws.ToString(c.CertificateAuthority.Data)
				accessWg.Add(1)
				go func(clusterID, clusterName, endpoint, ca, region, prof string) {
					defer accessWg.Done()
					hasAccess, accessErr := p.testClusterAccess(ctx, clusterName, endpoint, ca, region, prof, "", "", "")
					eventCh <- DiscoveryEvent{
						Type: DiscoveryEventStatusUpdate,
						Cluster: &DiscoveredCluster{
							ID:            clusterID,
							HasAccess:     hasAccess,
							AccessChecked: true,
							AccessError:   accessErr,
						},
					}
				}(cluster.ID, name, cluster.Endpoint, caData, r, profile)
			}

			mu.Lock()
			scannedCount++
			currentScanned := scannedCount
			currentClusters := clustersFound
			mu.Unlock()
			eventCh <- DiscoveryEvent{
				Type: DiscoveryEventProgress,
				Progress: &DiscoveryProgress{
					Region: r, AccountID: accountID, RegionsScanned: currentScanned, TotalRegions: totalRegions, ClustersFound: currentClusters,
				},
			}
		}(region)
	}
	wg.Wait()
	accessWg.Wait()
	eventCh <- DiscoveryEvent{Type: DiscoveryEventComplete}
}

func (p *AWSProvider) DiscoverClustersWithSSOStreaming(ctx context.Context, startURL string, regions []string, accountIDs []string, eventCh chan<- DiscoveryEvent) {
	defer close(eventCh)

	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{Status: "Fetching accounts..."}}

	accounts, err := p.GetSSOAccounts(ctx, startURL)
	if err != nil {
		eventCh <- DiscoveryEvent{Type: DiscoveryEventError, Error: err.Error()}
		return
	}

	if len(accountIDs) > 0 {
		filterSet := make(map[string]bool, len(accountIDs))
		for _, id := range accountIDs {
			filterSet[id] = true
		}
		filtered := make([]SSOAccount, 0, len(accountIDs))
		for _, acc := range accounts {
			if filterSet[acc.AccountID] {
				filtered = append(filtered, acc)
			}
		}
		accounts = filtered
	}

	if len(regions) == 0 {
		regions = getAllAWSRegions()
	}

	type regionJob struct {
		account        SSOAccount
		region         string
		cfg            aws.Config
		availableRoles []string
	}

	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{
		Status: fmt.Sprintf("Preparing %d account(s)...", len(accounts)),
	}}

	type accountCreds struct {
		account        SSOAccount
		cfg            aws.Config
		availableRoles []string
	}
	credsCh := make(chan accountCreds, len(accounts))
	var credsWg sync.WaitGroup
	credsSem := make(chan struct{}, 5)

	for _, account := range accounts {
		credsWg.Add(1)
		go func(acc SSOAccount) {
			defer credsWg.Done()
			credsSem <- struct{}{}
			defer func() { <-credsSem }()

			roles, err := p.GetSSORoles(ctx, startURL, acc.AccountID)
			if err != nil || len(roles) == 0 {
				return
			}
			roleName := roles[0]
			for _, r := range roles {
				if strings.Contains(strings.ToLower(r), "admin") || strings.Contains(strings.ToLower(r), "readonly") {
					roleName = r
					break
				}
			}
			cfg, err := p.GetSSOCredentials(ctx, startURL, acc.AccountID, roleName)
			if err != nil {
				return
			}
			credsCh <- accountCreds{account: acc, cfg: cfg, availableRoles: roles}
		}(account)
	}
	credsWg.Wait()
	close(credsCh)

	var jobs []regionJob
	for creds := range credsCh {
		for _, region := range regions {
			jobs = append(jobs, regionJob{account: creds.account, region: region, cfg: creds.cfg, availableRoles: creds.availableRoles})
		}
	}

	totalRegions := len(jobs)
	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{
		Status: "Scanning regions...", TotalRegions: totalRegions,
	}}

	var wg sync.WaitGroup
	var accessWg sync.WaitGroup
	var scannedCount int
	var clustersFound int
	var mu sync.Mutex
	sem := make(chan struct{}, 10)

	for _, job := range jobs {
		wg.Add(1)
		go func(j regionJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			regionCfg := j.cfg.Copy()
			regionCfg.Region = j.region
			eksClient := eks.NewFromConfig(regionCfg)

			var clusterNames []string
			var nextToken *string
			for {
				listResp, err := eksClient.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
				if err != nil {
					break
				}
				clusterNames = append(clusterNames, listResp.Clusters...)
				if listResp.NextToken == nil {
					break
				}
				nextToken = listResp.NextToken
			}

			for _, name := range clusterNames {
				descResp, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
				if err != nil {
					continue
				}
				c := descResp.Cluster
				cluster := DiscoveredCluster{
					ID:             fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", j.region, j.account.AccountID, name),
					Name:           name,
					Provider:       ProviderAWS,
					Region:         j.region,
					AccountID:      j.account.AccountID,
					Endpoint:       aws.ToString(c.Endpoint),
					Status:         string(c.Status),
					SSOStartURL:    startURL,
					AvailableRoles: j.availableRoles,
					AccessChecked:  false,
				}
				if c.Version != nil {
					cluster.Version = *c.Version
				}
				if c.Tags != nil {
					cluster.Tags = c.Tags
				}

				mu.Lock()
				clustersFound++
				mu.Unlock()
				eventCh <- DiscoveryEvent{Type: DiscoveryEventCluster, Cluster: &cluster}

				caData := aws.ToString(c.CertificateAuthority.Data)
				roleName := j.availableRoles[0]
				for _, r := range j.availableRoles {
					if strings.Contains(strings.ToLower(r), "admin") || strings.Contains(strings.ToLower(r), "readonly") {
						roleName = r
						break
					}
				}
				accessWg.Add(1)
				go func(clusterID, clusterName, endpoint, ca, region, accountID, role string) {
					defer accessWg.Done()
					hasAccess, accessErr := p.testClusterAccess(ctx, clusterName, endpoint, ca, region, "", startURL, accountID, role)
					eventCh <- DiscoveryEvent{
						Type: DiscoveryEventStatusUpdate,
						Cluster: &DiscoveredCluster{
							ID:            clusterID,
							HasAccess:     hasAccess,
							AccessChecked: true,
							AccessError:   accessErr,
						},
					}
				}(cluster.ID, name, cluster.Endpoint, caData, j.region, j.account.AccountID, roleName)
			}

			mu.Lock()
			scannedCount++
			currentScanned := scannedCount
			currentClusters := clustersFound
			mu.Unlock()
			eventCh <- DiscoveryEvent{
				Type: DiscoveryEventProgress,
				Progress: &DiscoveryProgress{
					Region: j.region, AccountID: j.account.AccountID, RegionsScanned: currentScanned, TotalRegions: totalRegions, ClustersFound: currentClusters,
				},
			}
		}(job)
	}
	wg.Wait()
	accessWg.Wait()
	eventCh <- DiscoveryEvent{Type: DiscoveryEventComplete}
}

func (p *AWSProvider) testClusterAccess(ctx context.Context, clusterName, endpoint, caData, region, profile, ssoStartURL, accountID, roleName string) (bool, string) {
	select {
	case p.accessTestSem <- struct{}{}:
		defer func() { <-p.accessTestSem }()
	case <-ctx.Done():
		return false, "context cancelled"
	}
	log.Printf("[testClusterAccess] Testing access for cluster=%s region=%s profile=%s ssoStartURL=%s accountID=%s roleName=%s",
		clusterName, region, profile, ssoStartURL, accountID, roleName)

	caBytes, err := base64.StdEncoding.DecodeString(caData)
	if err != nil {
		log.Printf("[testClusterAccess] Invalid CA data: %v", err)
		return false, fmt.Sprintf("invalid CA data: %v", err)
	}

	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters["test"] = &clientcmdapi.Cluster{
		Server:                   endpoint,
		CertificateAuthorityData: caBytes,
	}
	authInfo := &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:      "client.authentication.k8s.io/v1beta1",
			Command:         "aws",
			Args:            []string{"eks", "get-token", "--cluster-name", clusterName, "--region", region},
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		},
	}
	if ssoStartURL != "" && accountID != "" && roleName != "" {
		profileName := fmt.Sprintf("kanivet-sso-%s-%s", accountID, roleName)
		normalizedURL := normalizeStartURL(ssoStartURL)
		p.mu.RLock()
		cred, ok := p.ssoCredentials[normalizedURL]
		p.mu.RUnlock()
		log.Printf("[testClusterAccess] SSO credentials found in cache: %v (url=%s)", ok, normalizedURL)
		if ok {
			if err := p.ensureSSOProfile(profileName, ssoStartURL, cred.region, accountID, roleName); err == nil {
				log.Printf("[testClusterAccess] Using SSO profile: %s", profileName)
				authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: profileName}}
			} else {
				log.Printf("[testClusterAccess] Failed to ensure SSO profile: %v", err)
				return false, fmt.Sprintf("failed to setup SSO profile: %v", err)
			}
		} else {
			log.Printf("[testClusterAccess] SSO credentials not in cache, cannot test access")
			return false, "SSO session not found"
		}
	} else if profile != "" {
		log.Printf("[testClusterAccess] Using profile: %s", profile)
		authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: profile}}
	} else {
		log.Printf("[testClusterAccess] No auth method provided")
		return false, "no authentication configured"
	}
	kubeconfig.AuthInfos["test"] = authInfo
	kubeconfig.Contexts["test"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	kubeconfig.CurrentContext = "test"

	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*kubeconfig, "test", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		log.Printf("[testClusterAccess] Config error: %v", err)
		return false, fmt.Sprintf("config error: %v", err)
	}
	restConfig.Timeout = 5 * time.Second

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Printf("[testClusterAccess] Client error: %v", err)
		return false, fmt.Sprintf("client error: %v", err)
	}

	log.Printf("[testClusterAccess] Calling ServerVersion for %s...", clusterName)
	_, err = client.Discovery().ServerVersion()
	if err != nil {
		errStr := err.Error()
		log.Printf("[testClusterAccess] ServerVersion error for %s: %v", clusterName, err)
		if strings.Contains(errStr, "Unauthorized") || strings.Contains(errStr, "forbidden") {
			return false, "unauthorized access"
		}
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
			return false, "connection timeout"
		}
		return false, errStr
	}
	log.Printf("[testClusterAccess] Access verified for %s", clusterName)
	return true, ""
}

func (p *AWSProvider) testClusterAccessWithProfile(ctx context.Context, clusterName, endpoint, caData, region, profile string) (bool, string) {
	token, err := p.getEKSTokenWithCLI(ctx, clusterName, region, profile)
	if err != nil {
		return false, fmt.Sprintf("token error: %v", err)
	}

	caBytes, err := base64.StdEncoding.DecodeString(caData)
	if err != nil {
		return false, fmt.Sprintf("invalid CA data: %v", err)
	}

	restConfig := &rest.Config{
		Host:        endpoint,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caBytes,
		},
		Timeout: 10 * time.Second,
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return false, fmt.Sprintf("client error: %v", err)
	}

	_, err = client.Discovery().ServerVersion()
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Unauthorized") || strings.Contains(errStr, "forbidden") {
			return false, "unauthorized access"
		}
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
			return false, "connection timeout"
		}
		return false, errStr
	}
	return true, ""
}

func (p *AWSProvider) getEKSTokenWithCLI(ctx context.Context, clusterName, region, profile string) (string, error) {
	args := []string{"eks", "get-token", "--cluster-name", clusterName, "--region", region, "--output", "json"}
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("AWS_PROFILE=%s", profile))
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("aws eks get-token failed: %w", err)
	}
	var result struct {
		Status struct {
			Token string `json:"token"`
		} `json:"status"`
	}
	if err := jsonv2.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}
	return result.Status.Token, nil
}
