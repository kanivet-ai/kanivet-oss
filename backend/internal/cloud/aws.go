package cloud

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/kanivet/backend/internal/faults"
	"gopkg.in/ini.v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func normalizeStartURL(startURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(startURL))
	if err != nil {
		return strings.TrimSuffix(strings.TrimSuffix(startURL, "#"), "/")
	}
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

// AWSProvider talks to EKS/IAM Identity Center. It keeps no private credential
// state: SSO tokens live in ~/.aws/sso/cache (shared with the AWS CLI, Leapp,
// granted, …) and profiles live in ~/.aws/config, so whatever the user does in
// a terminal is what Kanivet sees, and vice-versa.
type AWSProvider struct {
	configs       map[string]cachedAWSConfig
	mu            sync.RWMutex
	kubeconfigMu  sync.Mutex
	awsConfigMu   sync.Mutex
	accessTestSem chan struct{}

	tokens *ssoTokenStore
	logins *ssoLoginManager
	cli    *cliLoginManager
}

type cachedAWSConfig struct {
	cfg      aws.Config
	loadedAt time.Time
}

const awsConfigCacheTTL = 10 * time.Minute

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

func kanivetSSOProfileName(accountID, roleName string) string {
	return fmt.Sprintf("kanivet-sso-%s-%s", accountID, roleName)
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
		configs:       make(map[string]cachedAWSConfig),
		accessTestSem: make(chan struct{}, 3),
		tokens:        newSSOTokenStore(),
		logins:        newSSOLoginManager(),
		cli:           newCLILoginManager(),
	}
	p.migrateLegacySSOProfiles()
	p.migrateImportedEKSExecAuth()
	p.restoreOriginalDefaultProfile()
	return p
}

// SetOnAuthChanged registers a callback fired whenever SSO state changes
// (sign-in, silent refresh, sign-out, CLI login finished).
func (p *AWSProvider) SetOnAuthChanged(fn func()) {
	p.tokens.onChange = fn
	p.cli.onDone = func(job *CLILoginJob) {
		p.ResetConfigCache()
		if fn != nil {
			fn()
		}
	}
}

// ResetConfigCache drops cached aws.Config values so rotated credentials in
// ~/.aws/credentials (Leapp, aws-vault, …) are picked up.
func (p *AWSProvider) ResetConfigCache() {
	p.mu.Lock()
	clear(p.configs)
	p.mu.Unlock()
	p.tokens.invalidate()
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

// preferredSSORole picks the role Kanivet uses when the user has not chosen
// one: something read-only or admin-ish first, otherwise the first role.
func preferredSSORole(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	for _, r := range roles {
		lower := strings.ToLower(r)
		if strings.Contains(lower, "readonly") || strings.Contains(lower, "read-only") || strings.Contains(lower, "viewer") {
			return r
		}
	}
	for _, r := range roles {
		if strings.Contains(strings.ToLower(r), "admin") {
			return r
		}
	}
	return roles[0]
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
			if name == "DEFAULT" || strings.HasPrefix(name, "sso-session ") || strings.HasPrefix(name, "services ") || name == "plugins" || name == "preview" {
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
			if section.HasKey("credential_process") {
				profile.CredentialProcess = true
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
					if section.HasKey("aws_access_key_id") {
						profiles[i].HasStaticCredentials = true
					}
					break
				}
			}
			if !found {
				profiles = append(profiles, AWSProfile{Name: name, Source: ProfileSourceCredentials, HasStaticCredentials: section.HasKey("aws_access_key_id")})
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
	if cached, ok := p.configs[key]; ok && time.Since(cached.loadedAt) < awsConfigCacheTTL {
		p.mu.RUnlock()
		return cached.cfg, nil
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
	p.configs[key] = cachedAWSConfig{cfg: cfg, loadedAt: time.Now()}
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

// cacheSSOToken persists a token under every cache key that refers to its
// portal: Kanivet's session, compatible user sso-sessions, and legacy keys.
func (p *AWSProvider) cacheSSOToken(token ssoTokenCache) error {
	return p.tokens.store(token)
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

// kanivetBackupProfile is where older Kanivet builds stashed the user's real
// [default] profile before overwriting it with SSO role credentials. Kanivet no
// longer touches [default]; on startup we put the original back if a backup is
// still there.
const kanivetBackupProfile = "kanivet-backup-original-default"

func restoreDefaultFromBackup(path, backupSection string) (bool, error) {
	cfg, err := loadINIFile(path)
	if err != nil {
		return false, err
	}
	if !cfg.HasSection(backupSection) {
		return false, nil
	}
	backup := cfg.Section(backupSection)
	defaultSection := cfg.Section("default")
	for _, key := range defaultSection.Keys() {
		defaultSection.DeleteKey(key.Name())
	}
	for _, key := range backup.Keys() {
		defaultSection.Key(key.Name()).SetValue(key.Value())
	}
	cfg.DeleteSection(backupSection)
	if len(defaultSection.Keys()) == 0 {
		cfg.DeleteSection("default")
	}
	return true, saveINIFileAtomically(path, cfg, 0600)
}

func (p *AWSProvider) restoreOriginalDefaultProfile() {
	p.awsConfigMu.Lock()
	defer p.awsConfigMu.Unlock()
	credPath, err := awsCredentialsPath()
	if err != nil {
		return
	}
	if restored, err := restoreDefaultFromBackup(credPath, kanivetBackupProfile); err != nil {
		log.Printf("Warning: failed to restore original [default] credentials: %v", err)
	} else if restored {
		log.Printf("[AWS] Restored the original [default] credentials profile that an earlier Kanivet version had replaced")
	}
	configPath, err := awsConfigPath()
	if err != nil {
		return
	}
	if restored, err := restoreDefaultFromBackup(configPath, "profile "+kanivetBackupProfile); err != nil {
		log.Printf("Warning: failed to restore original [default] config: %v", err)
	} else if restored {
		log.Printf("[AWS] Restored the original [default] config profile")
	}
}

// knownSSOSessions lists the sso-session blocks in ~/.aws/config.
func (p *AWSProvider) knownSSOSessions() []awsSSOSessionConfig {
	sessions, _ := loadAWSSSOConfig()
	return sessions
}

func (p *AWSProvider) ssoClient(ctx context.Context, startURL string) (*sso.Client, *ssoCachedToken, error) {
	tok, err := p.tokens.resolve(ctx, startURL)
	if err != nil {
		return nil, nil, err
	}
	region := tok.Token.Region
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(aws.AnonymousCredentials{}),
	)
	if err != nil {
		return nil, nil, err
	}
	return sso.NewFromConfig(cfg), tok, nil
}

func isSSOUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UnauthorizedException") || strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "InvalidRequestException: Session token not found or invalid")
}

// ssoAPIError turns a rejected access token into a sign-in-required error and
// drops Kanivet's copy of the dead token so the session shows as expired.
func (p *AWSProvider) ssoAPIError(startURL string, tok *ssoCachedToken, operation string, err error) error {
	if isSSOUnauthorized(err) {
		if tok != nil && tok.FromKanivet && tok.Path != "" {
			_ = os.Remove(tok.Path)
			p.tokens.invalidate()
			p.tokens.notify()
		}
		return loginRequired(startURL, "the portal rejected the cached token")
	}
	faults.CaptureExceptionWithContext(err, map[string]any{"operation": operation})
	return fmt.Errorf("%s failed: %w", operation, err)
}

func (p *AWSProvider) GetSSOAccounts(ctx context.Context, startURL string) ([]SSOAccount, error) {
	ssoClient, tok, err := p.ssoClient(ctx, startURL)
	if err != nil {
		return nil, err
	}
	var accounts []SSOAccount
	paginator := sso.NewListAccountsPaginator(ssoClient, &sso.ListAccountsInput{AccessToken: aws.String(tok.Token.AccessToken)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, p.ssoAPIError(startURL, tok, "sso_list_accounts", err)
		}
		for _, acc := range page.AccountList {
			accounts = append(accounts, SSOAccount{
				AccountID:   aws.ToString(acc.AccountId),
				AccountName: aws.ToString(acc.AccountName),
				EmailAddr:   aws.ToString(acc.EmailAddress),
			})
		}
	}
	sort.Slice(accounts, func(i, j int) bool {
		return strings.ToLower(accounts[i].AccountName) < strings.ToLower(accounts[j].AccountName)
	})
	return accounts, nil
}

func (p *AWSProvider) GetSSORoles(ctx context.Context, startURL, accountID string) ([]string, error) {
	ssoClient, tok, err := p.ssoClient(ctx, startURL)
	if err != nil {
		return nil, err
	}
	var roles []string
	paginator := sso.NewListAccountRolesPaginator(ssoClient, &sso.ListAccountRolesInput{
		AccessToken: aws.String(tok.Token.AccessToken),
		AccountId:   aws.String(accountID),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, p.ssoAPIError(startURL, tok, "sso_list_roles", err)
		}
		for _, role := range page.RoleList {
			roles = append(roles, aws.ToString(role.RoleName))
		}
	}
	return roles, nil
}

func (p *AWSProvider) GetSSOCredentials(ctx context.Context, startURL, accountID, roleName string) (aws.Config, error) {
	ssoClient, tok, err := p.ssoClient(ctx, startURL)
	if err != nil {
		return aws.Config{}, err
	}
	roleCredsResp, err := ssoClient.GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: aws.String(tok.Token.AccessToken),
		AccountId:   aws.String(accountID),
		RoleName:    aws.String(roleName),
	})
	if err != nil {
		return aws.Config{}, p.ssoAPIError(startURL, tok, "sso_get_credentials", err)
	}
	region := tok.Token.Region
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithCredentialsProvider(aws.AnonymousCredentials{}))
	if err != nil {
		return aws.Config{}, err
	}
	creds := roleCredsResp.RoleCredentials
	cfg.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(creds.AccessKeyId),
		aws.ToString(creds.SecretAccessKey),
		aws.ToString(creds.SessionToken),
	)
	return cfg, nil
}

// RefreshSSOSession refreshes the access token for startURL using the stored
// refresh token. It returns ErrSSOLoginRequired when that is not possible.
func (p *AWSProvider) RefreshSSOSession(ctx context.Context, startURL string) (*SSOSessionStatus, error) {
	if _, err := p.tokens.resolve(ctx, startURL); err != nil {
		return nil, err
	}
	for _, s := range p.GetSSOSessions() {
		if normalizeStartURL(s.StartURL) == normalizeStartURL(startURL) {
			return &s, nil
		}
	}
	return nil, nil
}

// SignOutSSO removes every cached token for the portal, exactly like
// `aws sso logout` would for that start URL. Profiles are left untouched.
func (p *AWSProvider) SignOutSSO(startURL string) error {
	if pending, ok := p.logins.pendingFor(startURL); ok {
		p.logins.cancel(pending.ID)
	}
	return p.tokens.removeTokensFor(startURL, false)
}

// ForgetSSOSession removes Kanivet's own cached tokens for a portal when the
// user deletes a Kanivet-added session; tokens the CLI created stay.
func (p *AWSProvider) ForgetSSOSession(startURL string) error {
	if pending, ok := p.logins.pendingFor(startURL); ok {
		p.logins.cancel(pending.ID)
	}
	return p.tokens.removeTokensFor(startURL, true)
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
		roleName := preferredSSORole(roles)

		cfg, err := p.GetSSOCredentials(ctx, startURL, account.AccountID, roleName)
		if err != nil {
			log.Printf("Failed to get credentials for %s/%s: %v", account.AccountID, roleName, err)
			continue
		}

		for _, region := range regions {
			wg.Add(1)
			go func(acc SSOAccount, baseCfg aws.Config, r string, availableRoles []string) {
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
						ID:             fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", r, acc.AccountID, name),
						Name:           name,
						Provider:       ProviderAWS,
						Region:         r,
						AccountID:      acc.AccountID,
						Endpoint:       aws.ToString(c.Endpoint),
						Status:         string(c.Status),
						SSOStartURL:    startURL,
						AvailableRoles: availableRoles,
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
			}(account, cfg, region, roles)
		}
	}

	wg.Wait()
	return allClusters, nil
}

// LoginWithProfile runs `aws sso login --profile <profile>` in the background
// (for profiles the user manages themselves) and returns the job to poll.
func (p *AWSProvider) LoginWithProfile(ctx context.Context, profile string) (*CLILoginJob, error) {
	profiles, err := p.ListProfiles()
	if err != nil {
		return nil, err
	}
	var target *AWSProfile
	for i := range profiles {
		if profiles[i].Name == profile {
			target = &profiles[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("profile %s not found", profile)
	}
	if !target.IsSSO {
		return nil, fmt.Errorf("profile %s does not use IAM Identity Center; its credentials are managed outside Kanivet (static keys, credential_process or a tool such as Leapp)", profile)
	}
	if target.SSOStartURL != "" {
		// Try the shared token cache first: if any tool already signed in to
		// this portal there is nothing to do.
		if _, err := p.tokens.resolve(ctx, target.SSOStartURL); err == nil {
			return &CLILoginJob{ID: "", Provider: ProviderAWS, Label: profile, State: cliJobSucceeded, StartedAt: time.Now().UnixMilli(), FinishedAt: time.Now().UnixMilli()}, nil
		}
	}
	return p.cli.start("aws:"+profile, ProviderAWS, profile, "aws", []string{"sso", "login", "--profile", profile}, cliEnv())
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
			Profile:   profile,
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
		cluster.AccessChecked = true
		cluster.AccessError = accessErr

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// findUserSSOProfile returns the name of a profile the user already has in
// ~/.aws/config for this portal/account/role, so an imported cluster reuses it
// (and `aws sso login --profile <theirs>` in a terminal keeps it working).
func findUserSSOProfile(startURL, accountID, roleName string) (string, bool) {
	_, profiles := loadAWSSSOConfig()
	normalized := normalizeStartURL(startURL)
	for _, prof := range profiles {
		if strings.HasPrefix(prof.Name, "kanivet-") {
			continue
		}
		if normalizeStartURL(prof.StartURL) == normalized && prof.AccountID == accountID && prof.RoleName == roleName {
			return prof.Name, true
		}
	}
	return "", false
}

// ssoProfileForImport resolves (or creates) the AWS profile an imported EKS
// cluster's exec plugin should use.
func (p *AWSProvider) ssoProfileForImport(ctx context.Context, startURL, accountID, roleName, fallbackRegion string) (string, error) {
	if name, ok := findUserSSOProfile(startURL, accountID, roleName); ok {
		return name, nil
	}
	ssoRegion := fallbackRegion
	if tok, err := p.tokens.resolve(ctx, startURL); err == nil && tok.Token.Region != "" {
		ssoRegion = tok.Token.Region
	} else if tok := p.tokens.best(startURL, time.Now()); tok != nil && tok.Token.Region != "" {
		ssoRegion = tok.Token.Region
	} else {
		for _, sess := range p.knownSSOSessions() {
			if normalizeStartURL(sess.StartURL) == normalizeStartURL(startURL) && sess.Region != "" {
				ssoRegion = sess.Region
				break
			}
		}
	}
	profileName := kanivetSSOProfileName(accountID, roleName)
	if err := p.ensureSSOProfile(profileName, startURL, ssoRegion, accountID, roleName); err != nil {
		return "", err
	}
	return profileName, nil
}

func (p *AWSProvider) ImportCluster(ctx context.Context, req ImportRequest) error {
	log.Printf("[ImportCluster] Starting import: name=%s, region=%s, accountId=%s, ssoStartUrl=%s, profile=%s, ssoRoleName=%s",
		req.Name, req.Region, req.AccountID, req.SSOStartURL, req.Profile, req.SSORoleName)

	var cfg aws.Config
	var err error
	var ssoRoleName string

	if req.SSOStartURL != "" {
		if req.SSORoleName != "" {
			ssoRoleName = req.SSORoleName
		} else {
			roles, rolesErr := p.GetSSORoles(ctx, req.SSOStartURL, req.AccountID)
			if rolesErr != nil {
				return fmt.Errorf("failed to get SSO roles for account %s: %w", req.AccountID, rolesErr)
			}
			if len(roles) == 0 {
				return fmt.Errorf("no SSO roles available for account %s", req.AccountID)
			}
			ssoRoleName = preferredSSORole(roles)
		}
		cfg, err = p.GetSSOCredentials(ctx, req.SSOStartURL, req.AccountID, ssoRoleName)
		if err != nil {
			return fmt.Errorf("failed to get SSO credentials: %w", err)
		}
		cfg.Region = req.Region
	} else if req.Profile != "" {
		cfg, err = p.GetConfig(ctx, req.Profile, req.Region)
		if err != nil {
			return err
		}
	} else {
		cfg, err = p.GetConfig(ctx, "default", req.Region)
		if err != nil {
			return err
		}
	}

	eksClient := eks.NewFromConfig(cfg)
	descResp, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(req.Name)})
	if err != nil {
		return fmt.Errorf("failed to describe cluster: %w", err)
	}
	cluster := descResp.Cluster

	kubeconfigPath := KanivetKubeconfigPath()

	p.kubeconfigMu.Lock()
	defer p.kubeconfigMu.Unlock()

	var kubeconfig *clientcmdapi.Config
	if _, statErr := os.Stat(kubeconfigPath); os.IsNotExist(statErr) {
		kubeconfig = clientcmdapi.NewConfig()
	} else {
		var loadErr error
		kubeconfig, loadErr = clientcmd.LoadFromFile(kubeconfigPath)
		if loadErr != nil {
			return fmt.Errorf("failed to load existing kubeconfig: %w", loadErr)
		}
	}

	clusterName := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", req.Region, req.AccountID, req.Name)
	contextName := clusterName
	caData, _ := base64.StdEncoding.DecodeString(aws.ToString(cluster.CertificateAuthority.Data))

	kubeconfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   aws.ToString(cluster.Endpoint),
		CertificateAuthorityData: caData,
	}

	authInfo := &clientcmdapi.AuthInfo{
		Exec: &clientcmdapi.ExecConfig{
			APIVersion:      "client.authentication.k8s.io/v1beta1",
			Command:         "aws",
			Args:            []string{"eks", "get-token", "--cluster-name", req.Name, "--region", req.Region, "--output", "json"},
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
			InstallHint:     cliInstallHint("aws"),
		},
	}
	if req.SSOStartURL != "" {
		profileName, err := p.ssoProfileForImport(ctx, req.SSOStartURL, req.AccountID, ssoRoleName, req.Region)
		if err != nil {
			return fmt.Errorf("failed to prepare SSO profile: %w", err)
		}
		authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: profileName}}
	} else if req.Profile != "" {
		authInfo.Exec.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: req.Profile}}
	}
	kubeconfig.AuthInfos[clusterName] = authInfo

	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: clusterName,
	}

	if err := saveKubeconfigAtomically(kubeconfigPath, kubeconfig); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}
	log.Printf("[ImportCluster] Cluster %s imported", req.Name)
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

func (p *AWSProvider) ensureSSOProfile(profileName, ssoStartURL, ssoRegion, accountID, roleName string) error {
	p.awsConfigMu.Lock()
	defer p.awsConfigMu.Unlock()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if ssoRegion == "" {
		ssoRegion = "us-east-1"
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

// SSOSessionState describes how usable a portal's cached token is.
const (
	SSOStateActive      = "active"      // access token valid
	SSOStateRefreshable = "refreshable" // access token expired but a refresh token can renew it silently
	SSOStateExpired     = "expired"     // token expired, interactive sign-in needed
	SSOStateSignedOut   = "signed_out"  // no token at all
)

// SSOSessionStatus is one IAM Identity Center portal as the UI sees it.
type SSOSessionStatus struct {
	StartURL     string           `json:"startUrl"`
	Region       string           `json:"region"`
	Label        string           `json:"label,omitempty"`
	State        string           `json:"state"`
	IsValid      bool             `json:"isValid"`
	Refreshable  bool             `json:"refreshable"`
	ExpiresAt    int64            `json:"expiresAt"`
	Source       string           `json:"source"` // kanivet | config | cache
	Managed      bool             `json:"managed"`
	SessionNames []string         `json:"sessionNames,omitempty"`
	ProfileCount int              `json:"profileCount"`
	Login        *SSOLoginSession `json:"login,omitempty"`
	// Error explains an expired state that needs a person, e.g. a rejected
	// refresh token.
	Error string `json:"error,omitempty"`
}

// staleCacheSessionAge is how long an expired token that only exists as a
// leftover cache file (no profile, no Kanivet entry) is still worth listing.
const staleCacheSessionAge = 7 * 24 * time.Hour

func (p *AWSProvider) sessionStatus(startURL, region string, now time.Time) SSOSessionStatus {
	status := SSOSessionStatus{StartURL: normalizeStartURL(startURL), Region: region, State: SSOStateSignedOut}
	if tok := p.tokens.best(startURL, now); tok != nil {
		status.ExpiresAt = tok.ExpiresAt.UnixMilli()
		if status.Region == "" {
			status.Region = tok.Token.Region
		}
		status.Refreshable = tok.refreshable(now)
		switch {
		case tok.valid(now):
			status.State = SSOStateActive
			status.IsValid = true
		case status.Refreshable:
			status.State = SSOStateRefreshable
		default:
			status.State = SSOStateExpired
			status.Error = tok.RefreshError
		}
	}
	if login, ok := p.logins.pendingFor(startURL); ok {
		status.Login = login
	}
	return status
}

// GetSSOSessions lists every portal known from ~/.aws/config and the token
// cache. The Service layer merges in Kanivet-added sessions and labels.
func (p *AWSProvider) GetSSOSessions() []SSOSessionStatus {
	now := time.Now()
	sessions, profiles := loadAWSSSOConfig()
	byURL := make(map[string]*SSOSessionStatus)
	order := []string{}

	ensure := func(startURL, region, source string) *SSOSessionStatus {
		key := normalizeStartURL(startURL)
		if s, ok := byURL[key]; ok {
			if s.Region == "" {
				s.Region = region
			}
			return s
		}
		s := p.sessionStatus(startURL, region, now)
		s.Source = source
		byURL[key] = &s
		order = append(order, key)
		return &s
	}

	for _, sess := range sessions {
		if strings.HasPrefix(sess.Name, "kanivet-sso-") {
			s := ensure(sess.StartURL, sess.Region, "kanivet")
			s.Managed = true
			continue
		}
		s := ensure(sess.StartURL, sess.Region, "config")
		s.SessionNames = append(s.SessionNames, sess.Name)
	}
	for _, prof := range profiles {
		if prof.StartURL == "" {
			continue
		}
		s := ensure(prof.StartURL, prof.Region, "config")
		if !strings.HasPrefix(prof.Name, "kanivet-") {
			s.ProfileCount++
		}
	}
	for _, tok := range p.tokens.tokens() {
		src := "cache"
		if tok.FromKanivet {
			src = "kanivet"
		}
		s := ensure(tok.Token.StartURL, tok.Token.Region, src)
		if tok.FromKanivet {
			s.Managed = true
		}
	}

	out := make([]SSOSessionStatus, 0, len(order))
	for _, key := range order {
		s := byURL[key]
		// Old cache files from portals nobody configured any more are noise.
		if s.Source == "cache" && s.State == SSOStateExpired && s.ExpiresAt > 0 &&
			now.Sub(time.UnixMilli(s.ExpiresAt)) > staleCacheSessionAge {
			continue
		}
		sort.Strings(s.SessionNames)
		out = append(out, *s)
	}
	return out
}

// RefreshExpiringSSOTokens silently renews every portal token that has a
// refresh token and is expired or about to expire. Nothing interactive happens.
func (p *AWSProvider) RefreshExpiringSSOTokens(ctx context.Context) {
	now := time.Now()
	seen := make(map[string]struct{})
	for _, tok := range p.tokens.tokens() {
		key := normalizeStartURL(tok.Token.StartURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		best := p.tokens.best(tok.Token.StartURL, now)
		if best == nil || best.freshEnough(now.Add(ssoRefreshLeeway)) || !best.refreshable(now) {
			continue
		}
		if _, err := p.tokens.resolve(ctx, tok.Token.StartURL); err != nil {
			log.Printf("[SSO] background refresh for %s: %v", key, err)
		}
	}
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
		log.Printf("Warning: could not get account ID for profile %s: %v", profile, err)
		eventCh <- DiscoveryEvent{Type: DiscoveryEventError, Error: fmt.Sprintf("Profile %s: %s", profile, humanizeAWSCredentialError(err))}
		return
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

// humanizeAWSCredentialError rewrites the SDK's credential-chain errors into
// something a person can act on.
func humanizeAWSCredentialError(err error) string {
	if err == nil {
		return ""
	}
	var loginReq *SSOLoginRequiredError
	if errors.As(err, &loginReq) {
		return "AWS SSO sign-in required"
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "sso session") && (strings.Contains(lower, "expired") || strings.Contains(lower, "invalid")),
		strings.Contains(lower, "token for") && strings.Contains(lower, "does not exist"),
		strings.Contains(lower, "error loading sso token"),
		strings.Contains(lower, "invalid_grant"):
		return "the SSO session for this profile has expired; sign in again"
	case strings.Contains(lower, "no ec2 imds role"), strings.Contains(lower, "failed to refresh cached credentials"), strings.Contains(lower, "nocredentialproviders"), strings.Contains(lower, "unable to locate credentials"):
		return "no valid credentials were found for this profile; start its session in your credential tool (Leapp, aws-vault, …) or run aws sso login"
	case strings.Contains(lower, "expiredtoken"):
		return "the credentials for this profile have expired"
	}
	return msg
}

func (p *AWSProvider) DiscoverClustersWithSSOStreaming(ctx context.Context, startURL string, regions []string, accountIDs []string, eventCh chan<- DiscoveryEvent) {
	defer close(eventCh)

	eventCh <- DiscoveryEvent{Type: DiscoveryEventProgress, Progress: &DiscoveryProgress{Status: "Fetching accounts..."}}

	accounts, err := p.GetSSOAccounts(ctx, startURL)
	if err != nil {
		eventCh <- DiscoveryEvent{Type: DiscoveryEventError, Error: humanizeAWSCredentialError(err)}
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
			roleName := preferredSSORole(roles)
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
				roleName := preferredSSORole(j.availableRoles)
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

	caBytes, err := base64.StdEncoding.DecodeString(caData)
	if err != nil {
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
			Args:            []string{"eks", "get-token", "--cluster-name", clusterName, "--region", region, "--output", "json"},
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
			Env:             []clientcmdapi.ExecEnvVar{{Name: "PATH", Value: augmentedPATH()}},
		},
	}
	if ssoStartURL != "" && accountID != "" && roleName != "" {
		profileName, err := p.ssoProfileForImport(ctx, ssoStartURL, accountID, roleName, region)
		if err != nil {
			return false, fmt.Sprintf("failed to setup SSO profile: %v", err)
		}
		authInfo.Exec.Env = append(authInfo.Exec.Env, clientcmdapi.ExecEnvVar{Name: "AWS_PROFILE", Value: profileName})
	} else if profile != "" {
		authInfo.Exec.Env = append(authInfo.Exec.Env, clientcmdapi.ExecEnvVar{Name: "AWS_PROFILE", Value: profile})
	} else {
		return false, "no authentication configured"
	}
	kubeconfig.AuthInfos["test"] = authInfo
	kubeconfig.Contexts["test"] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	kubeconfig.CurrentContext = "test"

	restConfig, err := clientcmd.NewNonInteractiveClientConfig(*kubeconfig, "test", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return false, fmt.Sprintf("config error: %v", err)
	}
	restConfig.Timeout = 8 * time.Second

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
