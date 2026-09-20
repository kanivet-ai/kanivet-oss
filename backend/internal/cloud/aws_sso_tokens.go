package cloud

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"gopkg.in/ini.v1"
)

// ErrSSOLoginRequired is returned when no usable IAM Identity Center token
// exists for a start URL and it cannot be refreshed silently. Callers surface
// it as an actionable "sign in" state; nothing in the backend ever opens a
// browser on its own in response to it.
var ErrSSOLoginRequired = errors.New("AWS SSO sign-in required")

// SSOLoginRequiredError carries the start URL that needs an interactive login.
type SSOLoginRequiredError struct {
	StartURL string
	Reason   string
}

func (e *SSOLoginRequiredError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("AWS SSO sign-in required for %s", e.StartURL)
	}
	return fmt.Sprintf("AWS SSO sign-in required for %s: %s", e.StartURL, e.Reason)
}

func (e *SSOLoginRequiredError) Is(target error) bool { return target == ErrSSOLoginRequired }

func loginRequired(startURL, reason string) error {
	return &SSOLoginRequiredError{StartURL: normalizeStartURL(startURL), Reason: reason}
}

// errRefreshRejected means the portal answered InvalidGrant (or similar) for a
// refresh token: only an interactive login can recover.
var errRefreshRejected = errors.New("refresh token no longer valid")

// ssoRefreshLeeway is how far ahead of access-token expiry we refresh. The AWS
// CLI uses five minutes; we match it so both tools agree on when a token is
// "still good".
const ssoRefreshLeeway = 5 * time.Minute

// ssoCachedToken is one file in ~/.aws/sso/cache, whoever wrote it (Kanivet,
// the AWS CLI, Leapp, granted, …). The cache directory is the single source of
// truth for SSO state: Kanivet keeps no private copy, so a terminal
// `aws sso login` or `aws sso logout` is visible immediately.
type ssoCachedToken struct {
	Path                  string
	Token                 ssoTokenCache
	ExpiresAt             time.Time
	RegistrationExpiresAt time.Time
	FromKanivet           bool
	ModTime               time.Time
	// RefreshError is set when the store already tried this file's refresh
	// token and the portal rejected it, so the session must not keep showing
	// as "renewing" and the monitor must not hammer the OIDC endpoint.
	RefreshError string
}

func (t ssoCachedToken) valid(now time.Time) bool {
	return t.Token.AccessToken != "" && now.Before(t.ExpiresAt)
}

// freshEnough reports whether the token is valid and not inside the refresh
// leeway window.
func (t ssoCachedToken) freshEnough(now time.Time) bool {
	return t.Token.AccessToken != "" && now.Add(ssoRefreshLeeway).Before(t.ExpiresAt)
}

func (t ssoCachedToken) refreshable(now time.Time) bool {
	if t.Token.RefreshToken == "" || t.Token.ClientID == "" || t.Token.ClientSecret == "" || t.RefreshError != "" {
		return false
	}
	if !t.RegistrationExpiresAt.IsZero() && !now.Before(t.RegistrationExpiresAt) {
		return false
	}
	return true
}

func ssoCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "sso", "cache"), nil
}

func parseSSOTokenFile(path string) (ssoCachedToken, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ssoCachedToken{}, false
	}
	var cached ssoTokenCache
	if err := jsonv2.Unmarshal(data, &cached); err != nil || cached.StartURL == "" || cached.AccessToken == "" {
		return ssoCachedToken{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339, cached.ExpiresAt)
	if err != nil {
		return ssoCachedToken{}, false
	}
	tok := ssoCachedToken{Path: path, Token: cached, ExpiresAt: expiresAt}
	if info, err := os.Stat(path); err == nil {
		tok.ModTime = info.ModTime()
	}
	if cached.RegistrationExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, cached.RegistrationExpiresAt); err == nil {
			tok.RegistrationExpiresAt = t
		}
	}
	if kanivetPath, err := ssocreds.StandardCachedTokenFilepath(kanivetSSOSessionName(cached.StartURL)); err == nil {
		tok.FromKanivet = filepath.Clean(kanivetPath) == filepath.Clean(path)
	}
	return tok, true
}

// scanSSOTokenCache reads every token file in ~/.aws/sso/cache. Client
// registration files (botocore-client-id-*.json) carry no startUrl and are
// skipped by parseSSOTokenFile.
func scanSSOTokenCache() []ssoCachedToken {
	dir, err := ssoCacheDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var tokens []ssoCachedToken
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if tok, ok := parseSSOTokenFile(filepath.Join(dir, entry.Name())); ok {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

func tokensForStartURL(tokens []ssoCachedToken, startURL string) []ssoCachedToken {
	normalized := normalizeStartURL(startURL)
	var out []ssoCachedToken
	for _, tok := range tokens {
		if normalizeStartURL(tok.Token.StartURL) == normalized {
			out = append(out, tok)
		}
	}
	return out
}

// pickSSOToken chooses the most useful token for a start URL: the valid one
// expiring last (Kanivet's own file wins ties), otherwise the refreshable one
// with the freshest registration, otherwise the most recently expired one so
// callers can still report when it expired.
func pickSSOToken(tokens []ssoCachedToken, now time.Time) *ssoCachedToken {
	if len(tokens) == 0 {
		return nil
	}
	sorted := make([]ssoCachedToken, len(tokens))
	copy(sorted, tokens)
	rank := func(t ssoCachedToken) int {
		switch {
		case t.valid(now):
			return 0
		case t.refreshable(now):
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rank(sorted[i]), rank(sorted[j])
		if ri != rj {
			return ri < rj
		}
		if !sorted[i].ExpiresAt.Equal(sorted[j].ExpiresAt) {
			return sorted[i].ExpiresAt.After(sorted[j].ExpiresAt)
		}
		return sorted[i].FromKanivet && !sorted[j].FromKanivet
	})
	best := sorted[0]
	return &best
}

// awsSSOSessionConfig is an `[sso-session name]` block from ~/.aws/config.
type awsSSOSessionConfig struct {
	Name     string
	StartURL string
	Region   string
	Scopes   string
}

// awsSSOProfileConfig is a `[profile name]` block that authenticates through
// IAM Identity Center, either via an sso-session or the legacy inline keys.
type awsSSOProfileConfig struct {
	Name        string
	SessionName string
	StartURL    string
	Region      string
	AccountID   string
	RoleName    string
	Legacy      bool
}

func awsConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "config"), nil
}

func awsCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "credentials"), nil
}

func parseAWSSSOConfig(cfg *ini.File) ([]awsSSOSessionConfig, []awsSSOProfileConfig) {
	var sessions []awsSSOSessionConfig
	sessionByName := make(map[string]awsSSOSessionConfig)
	for _, section := range cfg.Sections() {
		name := section.Name()
		if !strings.HasPrefix(name, "sso-session ") {
			continue
		}
		sess := awsSSOSessionConfig{
			Name:     strings.TrimPrefix(name, "sso-session "),
			StartURL: section.Key("sso_start_url").String(),
			Region:   section.Key("sso_region").String(),
			Scopes:   section.Key("sso_registration_scopes").String(),
		}
		if sess.StartURL == "" {
			continue
		}
		sessions = append(sessions, sess)
		sessionByName[sess.Name] = sess
	}

	var profiles []awsSSOProfileConfig
	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == "DEFAULT" || strings.HasPrefix(name, "sso-session ") {
			continue
		}
		profileName := strings.TrimPrefix(name, "profile ")
		prof := awsSSOProfileConfig{
			Name:      profileName,
			AccountID: section.Key("sso_account_id").String(),
			RoleName:  section.Key("sso_role_name").String(),
			Region:    section.Key("region").String(),
		}
		if sessionName := section.Key("sso_session").String(); sessionName != "" {
			prof.SessionName = sessionName
			if sess, ok := sessionByName[sessionName]; ok {
				prof.StartURL = sess.StartURL
				if prof.Region == "" {
					prof.Region = sess.Region
				}
			}
		} else if startURL := section.Key("sso_start_url").String(); startURL != "" {
			prof.Legacy = true
			prof.StartURL = startURL
			if prof.Region == "" {
				prof.Region = section.Key("sso_region").String()
			}
		} else {
			continue
		}
		profiles = append(profiles, prof)
	}
	return sessions, profiles
}

func loadAWSSSOConfig() ([]awsSSOSessionConfig, []awsSSOProfileConfig) {
	path, err := awsConfigPath()
	if err != nil {
		return nil, nil
	}
	cfg, err := loadINIFile(path)
	if err != nil {
		return nil, nil
	}
	return parseAWSSSOConfig(cfg)
}

// scopesCompatible reports whether a user-defined sso-session's registration
// scopes are satisfied by a token Kanivet minted with sso:account:access. If
// the user asked for extra scopes (Amazon Q, CodeCatalyst, …) we must not
// overwrite their token with a narrower one.
func scopesCompatible(scopes string) bool {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		return true
	}
	for _, s := range strings.Split(scopes, ",") {
		if strings.TrimSpace(s) != kanivetSSORegistrationScope {
			return false
		}
	}
	return true
}

// ssoCacheKeysFor lists every cache key a token for startURL should be written
// under: Kanivet's deterministic session, each user sso-session that points at
// the same portal (so `aws --profile theirs` is signed in too), and the legacy
// start-URL key when any legacy profile still uses it.
func ssoCacheKeysFor(startURL string, sessions []awsSSOSessionConfig, profiles []awsSSOProfileConfig) []string {
	normalized := normalizeStartURL(startURL)
	keys := []string{kanivetSSOSessionName(startURL)}
	seen := map[string]struct{}{keys[0]: {}}
	add := func(key string) {
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, sess := range sessions {
		if normalizeStartURL(sess.StartURL) == normalized && scopesCompatible(sess.Scopes) {
			add(sess.Name)
		}
	}
	for _, prof := range profiles {
		if prof.Legacy && normalizeStartURL(prof.StartURL) == normalized {
			add(prof.StartURL)
		}
	}
	return keys
}

// writeSSOTokenToKeys persists token under every key, never replacing a file
// that already holds a token valid for longer than ours.
func writeSSOTokenToKeys(token ssoTokenCache, keys []string) error {
	ours, err := time.Parse(time.RFC3339, token.ExpiresAt)
	if err != nil {
		return fmt.Errorf("invalid token expiry %q: %w", token.ExpiresAt, err)
	}
	var firstErr error
	for _, key := range keys {
		path, err := ssocreds.StandardCachedTokenFilepath(key)
		if err != nil {
			continue
		}
		if existing, ok := parseSSOTokenFile(path); ok && existing.ExpiresAt.After(ours) && existing.valid(time.Now()) {
			continue
		}
		if err := writeSSOTokenCache(key, token); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ssoTokenStore serialises refreshes per start URL and memoises directory
// scans for a moment so a burst of role lookups during discovery does not
// re-read the cache directory hundreds of times.
type ssoTokenStore struct {
	mu        sync.Mutex
	scanned   []ssoCachedToken
	scannedAt time.Time
	inflight  map[string]*sync.Mutex
	onChange  func()
	// failures remembers refresh tokens the portal rejected, keyed by cache
	// file path and pinned to that file's mtime so a fresh login (new file
	// contents) clears the mark automatically.
	failures map[string]refreshFailure
}

type refreshFailure struct {
	modTime time.Time
	reason  string
}

const ssoScanMemo = 750 * time.Millisecond

func newSSOTokenStore() *ssoTokenStore {
	return &ssoTokenStore{inflight: make(map[string]*sync.Mutex), failures: make(map[string]refreshFailure)}
}

func (s *ssoTokenStore) markRefreshFailed(tok ssoCachedToken, reason string) {
	if tok.Path == "" {
		return
	}
	s.mu.Lock()
	s.failures[tok.Path] = refreshFailure{modTime: tok.ModTime, reason: reason}
	s.mu.Unlock()
}

// applyFailures annotates scanned tokens with remembered refresh failures and
// forgets marks whose file has since changed.
func (s *ssoTokenStore) applyFailures(tokens []ssoCachedToken) {
	if len(s.failures) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(tokens))
	for i := range tokens {
		seen[tokens[i].Path] = struct{}{}
		if f, ok := s.failures[tokens[i].Path]; ok {
			if f.modTime.Equal(tokens[i].ModTime) {
				tokens[i].RefreshError = f.reason
			} else {
				delete(s.failures, tokens[i].Path)
			}
		}
	}
	for path := range s.failures {
		if _, ok := seen[path]; !ok {
			delete(s.failures, path)
		}
	}
}

func (s *ssoTokenStore) invalidate() {
	s.mu.Lock()
	s.scanned = nil
	s.scannedAt = time.Time{}
	s.mu.Unlock()
}

func (s *ssoTokenStore) tokens() []ssoCachedToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanned != nil && time.Since(s.scannedAt) < ssoScanMemo {
		return s.scanned
	}
	s.scanned = scanSSOTokenCache()
	if s.scanned == nil {
		s.scanned = []ssoCachedToken{}
	}
	s.applyFailures(s.scanned)
	s.scannedAt = time.Now()
	return s.scanned
}

func (s *ssoTokenStore) lockURL(startURL string) func() {
	key := normalizeStartURL(startURL)
	s.mu.Lock()
	m, ok := s.inflight[key]
	if !ok {
		m = &sync.Mutex{}
		s.inflight[key] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *ssoTokenStore) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

// best returns the most useful cached token for startURL without refreshing.
func (s *ssoTokenStore) best(startURL string, now time.Time) *ssoCachedToken {
	return pickSSOToken(tokensForStartURL(s.tokens(), startURL), now)
}

// resolve returns a valid access token for startURL, refreshing it with the
// stored refresh token when needed. It never starts an interactive login.
func (s *ssoTokenStore) resolve(ctx context.Context, startURL string) (*ssoCachedToken, error) {
	now := time.Now()
	if tok := s.best(startURL, now); tok != nil && tok.freshEnough(now) {
		return tok, nil
	}

	unlock := s.lockURL(startURL)
	defer unlock()

	s.invalidate()
	now = time.Now()
	tok := s.best(startURL, now)
	if tok == nil {
		return nil, loginRequired(startURL, "no cached token")
	}
	if tok.freshEnough(now) {
		return tok, nil
	}
	if tok.refreshable(now) {
		refreshed, err := s.refresh(ctx, *tok)
		if err == nil {
			return refreshed, nil
		}
		log.Printf("[SSO] silent refresh for %s failed: %v", normalizeStartURL(startURL), err)
		if errors.Is(err, errRefreshRejected) {
			// Remember the rejection so the UI shows "expired" instead of
			// "renewing" and the monitor stops retrying until a new login
			// rewrites the file.
			s.markRefreshFailed(*tok, "the portal rejected the refresh token; sign in again")
			s.invalidate()
			s.notify()
		}
		if tok.valid(now) {
			return tok, nil
		}
		return nil, loginRequired(startURL, "refresh token rejected")
	}
	if tok.valid(now) {
		return tok, nil
	}
	return nil, loginRequired(startURL, "token expired")
}

func ssoOIDCClient(ctx context.Context, region string) (*ssooidc.Client, error) {
	if region == "" {
		region = "us-east-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(aws.AnonymousCredentials{}),
	)
	if err != nil {
		return nil, err
	}
	return ssooidc.NewFromConfig(cfg), nil
}

func isSSOInvalidGrant(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "InvalidGrantException") ||
		strings.Contains(msg, "ExpiredTokenException") ||
		strings.Contains(msg, "InvalidClientException") ||
		strings.Contains(msg, "UnauthorizedClientException") ||
		strings.Contains(msg, "AccessDeniedException")
}

// refresh exchanges the stored refresh token for a new access token and writes
// the result back to every cache key for the portal (mirroring what the AWS
// CLI would do in place).
func (s *ssoTokenStore) refresh(ctx context.Context, tok ssoCachedToken) (*ssoCachedToken, error) {
	region := tok.Token.Region
	if region == "" {
		if sessions, _ := loadAWSSSOConfig(); len(sessions) > 0 {
			for _, sess := range sessions {
				if normalizeStartURL(sess.StartURL) == normalizeStartURL(tok.Token.StartURL) && sess.Region != "" {
					region = sess.Region
					break
				}
			}
		}
	}
	client, err := ssoOIDCClient(ctx, region)
	if err != nil {
		return nil, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := client.CreateToken(refreshCtx, &ssooidc.CreateTokenInput{
		ClientId:     aws.String(tok.Token.ClientID),
		ClientSecret: aws.String(tok.Token.ClientSecret),
		GrantType:    aws.String("refresh_token"),
		RefreshToken: aws.String(tok.Token.RefreshToken),
	})
	if err != nil {
		if isSSOInvalidGrant(err) {
			return nil, fmt.Errorf("%w: %v", errRefreshRejected, err)
		}
		return nil, fmt.Errorf("refresh failed: %w", err)
	}

	registrationExpires := int64(0)
	if !tok.RegistrationExpiresAt.IsZero() {
		registrationExpires = tok.RegistrationExpiresAt.Unix()
	}
	refreshToken := out.RefreshToken
	if refreshToken == nil || *refreshToken == "" {
		existing := tok.Token.RefreshToken
		refreshToken = &existing
	}
	updated, expiresAt := buildSSOTokenCache(
		time.Now(),
		tok.Token.StartURL,
		region,
		ssoClientRegistration{ClientID: tok.Token.ClientID, ClientSecret: tok.Token.ClientSecret, RegistrationExpiresAt: registrationExpires},
		aws.ToString(out.AccessToken),
		refreshToken,
		out.ExpiresIn,
	)

	sessions, profiles := loadAWSSSOConfig()
	keys := ssoCacheKeysFor(tok.Token.StartURL, sessions, profiles)
	if err := writeSSOTokenToKeys(updated, keys); err != nil {
		log.Printf("[SSO] failed to persist refreshed token: %v", err)
	}
	// Also update the file we refreshed from, even if it is under a key we do
	// not otherwise manage (for example a legacy profile with a differently
	// spelled start URL), so that tool keeps working too.
	if tok.Path != "" {
		if data, err := jsonv2.Marshal(updated); err == nil {
			_ = writeFileAtomically(tok.Path, 0600, func(f *os.File) error {
				_, err := f.Write(data)
				return err
			})
		}
	}
	s.invalidate()
	s.notify()
	log.Printf("[SSO] silently refreshed token for %s (expires %s)", normalizeStartURL(tok.Token.StartURL), expiresAt.Format(time.RFC3339))
	result := ssoCachedToken{Path: tok.Path, Token: updated, ExpiresAt: expiresAt, RegistrationExpiresAt: tok.RegistrationExpiresAt, FromKanivet: tok.FromKanivet}
	return &result, nil
}

// removeTokensFor deletes every cache file holding a token for startURL. When
// kanivetOnly is set, files written by other tools are left alone.
func (s *ssoTokenStore) removeTokensFor(startURL string, kanivetOnly bool) error {
	unlock := s.lockURL(startURL)
	defer unlock()
	s.invalidate()
	var firstErr error
	for _, tok := range tokensForStartURL(scanSSOTokenCache(), startURL) {
		if kanivetOnly && !tok.FromKanivet {
			continue
		}
		if err := os.Remove(tok.Path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	if !kanivetOnly {
		// Legacy Kanivet profiles cached under the raw start URL.
		for _, key := range []string{startURL, normalizeStartURL(startURL), kanivetSSOSessionName(startURL)} {
			if path, err := ssocreds.StandardCachedTokenFilepath(key); err == nil {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	s.invalidate()
	s.notify()
	return firstErr
}

// store writes a freshly minted token under every key for its portal.
func (s *ssoTokenStore) store(token ssoTokenCache) error {
	unlock := s.lockURL(token.StartURL)
	defer unlock()
	sessions, profiles := loadAWSSSOConfig()
	keys := ssoCacheKeysFor(token.StartURL, sessions, profiles)
	err := writeSSOTokenToKeys(token, keys)
	s.invalidate()
	s.notify()
	return err
}

// registrationFor finds a still-valid OIDC client registration for the portal
// so an interactive login can skip RegisterClient and keep the same client ID
// the cached refresh tokens were issued to.
func (s *ssoTokenStore) registrationFor(startURL string, now time.Time) (ssoClientRegistration, string, bool) {
	var best *ssoCachedToken
	for _, tok := range tokensForStartURL(s.tokens(), startURL) {
		if tok.Token.ClientID == "" || tok.Token.ClientSecret == "" {
			continue
		}
		if !tok.RegistrationExpiresAt.IsZero() && !now.Add(time.Hour).Before(tok.RegistrationExpiresAt) {
			continue
		}
		if tok.RegistrationExpiresAt.IsZero() {
			continue
		}
		if best == nil || tok.RegistrationExpiresAt.After(best.RegistrationExpiresAt) {
			t := tok
			best = &t
		}
	}
	if best == nil {
		return ssoClientRegistration{}, "", false
	}
	return ssoClientRegistration{
		ClientID:              best.Token.ClientID,
		ClientSecret:          best.Token.ClientSecret,
		RegistrationExpiresAt: best.RegistrationExpiresAt.Unix(),
	}, best.Token.Region, true
}
