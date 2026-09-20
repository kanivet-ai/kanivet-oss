package cloud

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/google/uuid"
	"github.com/kanivet/backend/internal/faults"
)

// SSOLoginState is the lifecycle of one interactive device-code login.
type SSOLoginState string

const (
	SSOLoginPending    SSOLoginState = "pending"
	SSOLoginAuthorized SSOLoginState = "authorized"
	SSOLoginFailed     SSOLoginState = "failed"
	SSOLoginCancelled  SSOLoginState = "cancelled"
	SSOLoginExpired    SSOLoginState = "expired"
)

func (s SSOLoginState) terminal() bool { return s != SSOLoginPending }

// SSOLoginSession is a non-blocking device authorization. The HTTP handler
// returns it immediately; the UI polls it (or waits for the auth-changed
// broadcast) while the user approves the request in the browser.
type SSOLoginSession struct {
	ID                      string        `json:"id"`
	StartURL                string        `json:"startUrl"`
	Region                  string        `json:"region"`
	UserCode                string        `json:"userCode"`
	VerificationURL         string        `json:"verificationUrl"`
	VerificationURLComplete string        `json:"verificationUrlComplete"`
	ExpiresAt               int64         `json:"expiresAt"`
	State                   SSOLoginState `json:"state"`
	Error                   string        `json:"error,omitempty"`
	TokenExpiresAt          int64         `json:"tokenExpiresAt,omitempty"`
	StartedAt               int64         `json:"startedAt"`
	BrowserOpened           bool          `json:"browserOpened"`

	cancel context.CancelFunc
}

func (s *SSOLoginSession) snapshot() *SSOLoginSession {
	c := *s
	c.cancel = nil
	return &c
}

type ssoLoginManager struct {
	mu    sync.Mutex
	byID  map[string]*SSOLoginSession
	byURL map[string]string
}

func newSSOLoginManager() *ssoLoginManager {
	return &ssoLoginManager{byID: make(map[string]*SSOLoginSession), byURL: make(map[string]string)}
}

func (m *ssoLoginManager) get(id string) (*SSOLoginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return nil, false
	}
	return s.snapshot(), true
}

func (m *ssoLoginManager) pendingFor(startURL string) (*SSOLoginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byURL[normalizeStartURL(startURL)]
	if !ok {
		return nil, false
	}
	s := m.byID[id]
	if s == nil || s.State != SSOLoginPending {
		return nil, false
	}
	return s.snapshot(), true
}

func (m *ssoLoginManager) cancel(id string) bool {
	m.mu.Lock()
	s, ok := m.byID[id]
	m.mu.Unlock()
	if !ok || s.State != SSOLoginPending {
		return ok
	}
	if s.cancel != nil {
		s.cancel()
	}
	return true
}

func (m *ssoLoginManager) finish(id string, state SSOLoginState, errMsg string, tokenExpiresAt int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return
	}
	s.State = state
	s.Error = errMsg
	s.TokenExpiresAt = tokenExpiresAt
	// Drop finished sessions after a while so ids do not accumulate forever.
	go func() {
		time.Sleep(15 * time.Minute)
		m.mu.Lock()
		if cur, ok := m.byID[id]; ok && cur.State.terminal() {
			delete(m.byID, id)
			if m.byURL[normalizeStartURL(cur.StartURL)] == id {
				delete(m.byURL, normalizeStartURL(cur.StartURL))
			}
		}
		m.mu.Unlock()
	}()
}

// BeginSSOLogin starts a device-code login for startURL and returns at once.
// A second call for the same portal while one is pending returns the pending
// session instead of opening another browser tab.
func (p *AWSProvider) BeginSSOLogin(ctx context.Context, startURL, region string, openBrowser bool) (*SSOLoginSession, error) {
	startURL = strings.TrimSpace(startURL)
	if startURL == "" {
		return nil, fmt.Errorf("startUrl is required")
	}
	if !strings.HasPrefix(startURL, "https://") {
		startURL = "https://" + strings.TrimPrefix(startURL, "http://")
	}
	if existing, ok := p.logins.pendingFor(startURL); ok {
		if openBrowser && !existing.BrowserOpened {
			existing.BrowserOpened = openBrowserURL(existing.VerificationURLComplete) == nil
		}
		return existing, nil
	}

	now := time.Now()
	registration, regRegion, reuse := p.tokens.registrationFor(startURL, now)
	if region == "" {
		region = regRegion
	}
	if region == "" {
		for _, sess := range p.knownSSOSessions() {
			if normalizeStartURL(sess.StartURL) == normalizeStartURL(startURL) && sess.Region != "" {
				region = sess.Region
				break
			}
		}
	}
	if region == "" {
		region = "us-east-1"
	}

	oidcClient, err := ssoOIDCClient(ctx, region)
	if err != nil {
		return nil, err
	}

	if !reuse {
		registerResp, err := oidcClient.RegisterClient(ctx, &ssooidc.RegisterClientInput{
			ClientName: aws.String("kanivet"),
			ClientType: aws.String("public"),
			Scopes:     []string{kanivetSSORegistrationScope},
		})
		if err != nil {
			faults.CaptureExceptionWithContext(err, map[string]any{"operation": "sso_register_client", "region": region})
			return nil, fmt.Errorf("failed to register OIDC client: %w", err)
		}
		registration = ssoClientRegistration{
			ClientID:              aws.ToString(registerResp.ClientId),
			ClientSecret:          aws.ToString(registerResp.ClientSecret),
			RegistrationExpiresAt: registerResp.ClientSecretExpiresAt,
		}
	}

	startAuthResp, err := oidcClient.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     aws.String(registration.ClientID),
		ClientSecret: aws.String(registration.ClientSecret),
		StartUrl:     aws.String(startURL),
	})
	if err != nil && reuse {
		// A stale registration is the most likely culprit; register a fresh
		// client once before giving up.
		registerResp, regErr := oidcClient.RegisterClient(ctx, &ssooidc.RegisterClientInput{
			ClientName: aws.String("kanivet"),
			ClientType: aws.String("public"),
			Scopes:     []string{kanivetSSORegistrationScope},
		})
		if regErr == nil {
			registration = ssoClientRegistration{
				ClientID:              aws.ToString(registerResp.ClientId),
				ClientSecret:          aws.ToString(registerResp.ClientSecret),
				RegistrationExpiresAt: registerResp.ClientSecretExpiresAt,
			}
			startAuthResp, err = oidcClient.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
				ClientId:     aws.String(registration.ClientID),
				ClientSecret: aws.String(registration.ClientSecret),
				StartUrl:     aws.String(startURL),
			})
		}
	}
	if err != nil {
		faults.CaptureExceptionWithContext(err, map[string]any{"operation": "sso_device_authorization", "region": region})
		return nil, fmt.Errorf("failed to start device authorization: %w", err)
	}

	interval := time.Duration(startAuthResp.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deviceTTL := time.Duration(startAuthResp.ExpiresIn) * time.Second
	if deviceTTL <= 0 {
		deviceTTL = 10 * time.Minute
	}

	pollCtx, cancel := context.WithTimeout(context.Background(), deviceTTL+15*time.Second)
	session := &SSOLoginSession{
		ID:                      uuid.NewString(),
		StartURL:                startURL,
		Region:                  region,
		UserCode:                aws.ToString(startAuthResp.UserCode),
		VerificationURL:         aws.ToString(startAuthResp.VerificationUri),
		VerificationURLComplete: aws.ToString(startAuthResp.VerificationUriComplete),
		ExpiresAt:               now.Add(deviceTTL).UnixMilli(),
		State:                   SSOLoginPending,
		StartedAt:               now.UnixMilli(),
		cancel:                  cancel,
	}
	if openBrowser {
		if err := openBrowserURL(session.VerificationURLComplete); err != nil {
			log.Printf("[SSO] could not open browser: %v", err)
		} else {
			session.BrowserOpened = true
		}
	}

	p.logins.mu.Lock()
	p.logins.byID[session.ID] = session
	p.logins.byURL[normalizeStartURL(startURL)] = session.ID
	p.logins.mu.Unlock()

	go p.pollSSOLogin(pollCtx, session.ID, startURL, region, registration, aws.ToString(startAuthResp.DeviceCode), interval, oidcClient)

	return session.snapshot(), nil
}

func (p *AWSProvider) pollSSOLogin(ctx context.Context, id, startURL, region string, registration ssoClientRegistration, deviceCode string, interval time.Duration, oidcClient *ssooidc.Client) {
	defer func() {
		if s, ok := p.logins.get(id); ok && s.cancel != nil {
			s.cancel()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				p.logins.finish(id, SSOLoginExpired, "The sign-in request expired before it was approved.", 0)
			} else {
				p.logins.finish(id, SSOLoginCancelled, "", 0)
			}
			p.tokens.notify()
			return
		case <-time.After(interval):
		}

		tokenResp, err := oidcClient.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     aws.String(registration.ClientID),
			ClientSecret: aws.String(registration.ClientSecret),
			DeviceCode:   aws.String(deviceCode),
			GrantType:    aws.String("urn:ietf:params:oauth:grant-type:device_code"),
		})
		if err != nil {
			msg := err.Error()
			switch {
			case strings.Contains(msg, "AuthorizationPendingException"):
				continue
			case strings.Contains(msg, "SlowDownException"):
				interval += 5 * time.Second
				continue
			case strings.Contains(msg, "ExpiredTokenException"):
				p.logins.finish(id, SSOLoginExpired, "The sign-in request expired before it was approved.", 0)
				p.tokens.notify()
				return
			case strings.Contains(msg, "AccessDeniedException"):
				p.logins.finish(id, SSOLoginFailed, "The sign-in request was denied.", 0)
				p.tokens.notify()
				return
			case ctx.Err() != nil:
				continue
			default:
				faults.CaptureExceptionWithContext(err, map[string]any{"operation": "sso_create_token", "region": region})
				p.logins.finish(id, SSOLoginFailed, fmt.Sprintf("Failed to complete sign-in: %v", err), 0)
				p.tokens.notify()
				return
			}
		}

		tokenCache, expiresAt := buildSSOTokenCache(
			time.Now(), startURL, region, registration,
			aws.ToString(tokenResp.AccessToken), tokenResp.RefreshToken, tokenResp.ExpiresIn,
		)
		if err := p.tokens.store(tokenCache); err != nil {
			log.Printf("[SSO] failed to cache token: %v", err)
		}
		p.logins.finish(id, SSOLoginAuthorized, "", expiresAt.UnixMilli())
		p.tokens.notify()
		log.Printf("[SSO] signed in to %s (token expires %s)", normalizeStartURL(startURL), expiresAt.Format(time.RFC3339))
		return
	}
}

func (p *AWSProvider) GetSSOLogin(id string) (*SSOLoginSession, bool) {
	return p.logins.get(id)
}

func (p *AWSProvider) CancelSSOLogin(id string) bool {
	return p.logins.cancel(id)
}
