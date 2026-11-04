package images

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/google/go-containerregistry/pkg/authn"
)

// Cloud keychains only act on their own registry hosts and answer Anonymous
// on any failure, so a missing cloud login never blocks other sources.

const cloudAuthTimeout = 10 * time.Second

type cachedCred struct {
	cfg     authn.AuthConfig
	expires time.Time
}

var (
	cloudCredMu sync.Mutex
	cloudCreds  = map[string]cachedCred{}
)

func cachedCloudCred(host string) (authn.AuthConfig, bool) {
	cloudCredMu.Lock()
	defer cloudCredMu.Unlock()
	c, ok := cloudCreds[host]
	if !ok || time.Now().After(c.expires) {
		return authn.AuthConfig{}, false
	}
	return c.cfg, true
}

func storeCloudCred(host string, cfg authn.AuthConfig, ttl time.Duration) {
	cloudCredMu.Lock()
	cloudCreds[host] = cachedCred{cfg: cfg, expires: time.Now().Add(ttl)}
	cloudCredMu.Unlock()
}

// ecrHost matches <account>.dkr.ecr[-fips].<region>.amazonaws.com[.cn].
var ecrHost = regexp.MustCompile(`^(\d{12})\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)

type ecrKeychain struct{ ctx context.Context }

func (k ecrKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	host := r.RegistryStr()
	m := ecrHost.FindStringSubmatch(host)
	if m == nil {
		return authn.Anonymous, nil
	}
	if c, ok := cachedCloudCred(host); ok {
		return authn.FromConfig(c), nil
	}
	ctx, cancel := context.WithTimeout(k.ctx, cloudAuthTimeout)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(m[3]))
	if err != nil {
		return authn.Anonymous, nil
	}
	out, err := ecr.NewFromConfig(cfg).GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil || len(out.AuthorizationData) == 0 || out.AuthorizationData[0].AuthorizationToken == nil {
		return authn.Anonymous, nil
	}
	dec, err := base64.StdEncoding.DecodeString(*out.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return authn.Anonymous, nil
	}
	user, pass, ok := strings.Cut(string(dec), ":")
	if !ok {
		return authn.Anonymous, nil
	}
	ac := authn.AuthConfig{Username: user, Password: pass}
	storeCloudCred(host, ac, 6*time.Hour)
	return authn.FromConfig(ac), nil
}

type acrKeychain struct{ ctx context.Context }

func (k acrKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	host := r.RegistryStr()
	if !strings.HasSuffix(host, ".azurecr.io") && !strings.HasSuffix(host, ".azurecr.cn") && !strings.HasSuffix(host, ".azurecr.us") {
		return authn.Anonymous, nil
	}
	if c, ok := cachedCloudCred(host); ok {
		return authn.FromConfig(c), nil
	}
	ctx, cancel := context.WithTimeout(k.ctx, cloudAuthTimeout)
	defer cancel()
	refresh, err := acrRefreshToken(ctx, host)
	if err != nil {
		return authn.Anonymous, nil
	}
	// ACR accepts this fixed username with an ACR refresh token.
	ac := authn.AuthConfig{Username: "00000000-0000-0000-0000-000000000000", Password: refresh}
	storeCloudCred(host, ac, 2*time.Hour)
	return authn.FromConfig(ac), nil
}

// acrRefreshToken exchanges an Azure AD token from the local Azure login for
// an ACR refresh token.
func acrRefreshToken(ctx context.Context, host string) (string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", err
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"access_token"}, "service": {host}, "access_token": {tok.Token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/oauth2/exchange", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("acr exchange: %s", resp.Status)
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		return "", fmt.Errorf("acr exchange: no token")
	}
	return body.RefreshToken, nil
}
