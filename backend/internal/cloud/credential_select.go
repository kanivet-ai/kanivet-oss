package cloud

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"golang.org/x/sync/singleflight"
)

// An EKS context can often be reached through more than one AWS credential
// source: the Identity Center role Kanivet was told to use, the profile its
// kubeconfig names (often the default profile a tool like Leapp keeps fresh),
// and any other profile for the same account. Whichever the user is signed in
// with right now should just work, so Kanivet checks the candidates in order
// of preference and runs the exec plugin under the first one that reaches the
// cluster's account.

const (
	credentialCheckOKTTL     = 2 * time.Minute
	credentialCheckFailedTTL = 15 * time.Second
	credentialCheckTimeout   = 6 * time.Second
)

// awsIdentityFunc returns the account a profile's credentials belong to.
type awsIdentityFunc func(ctx context.Context, profile string) (string, error)

type credentialCheck struct {
	account   string
	err       error
	checkedAt time.Time
}

type credentialChecker struct {
	identity awsIdentityFunc
	now      func() time.Time
	mu       sync.Mutex
	results  map[string]credentialCheck
	flight   singleflight.Group
}

func newCredentialChecker(identity awsIdentityFunc) *credentialChecker {
	return &credentialChecker{identity: identity, now: time.Now, results: make(map[string]credentialCheck)}
}

// reaches reports whether profile currently has credentials for account.
func (c *credentialChecker) reaches(ctx context.Context, profile, account string) bool {
	c.mu.Lock()
	res, ok := c.results[profile]
	c.mu.Unlock()
	if ok {
		ttl := credentialCheckOKTTL
		if res.err != nil {
			ttl = credentialCheckFailedTTL
		}
		if c.now().Sub(res.checkedAt) < ttl {
			return res.err == nil && res.account == account
		}
	}
	v, _, _ := c.flight.Do(profile, func() (any, error) {
		checkCtx, cancel := context.WithTimeout(ctx, credentialCheckTimeout)
		defer cancel()
		acct, err := c.identity(checkCtx, profile)
		res := credentialCheck{account: acct, err: err, checkedAt: c.now()}
		c.mu.Lock()
		c.results[profile] = res
		c.mu.Unlock()
		return res, nil
	})
	res = v.(credentialCheck)
	return res.err == nil && res.account == account
}

// forget drops cached results so the next choice checks again.
func (c *credentialChecker) forget() {
	c.mu.Lock()
	clear(c.results)
	c.mu.Unlock()
}

// awsIdentity asks STS who a profile's credentials belong to. The profile is
// set programmatically, so, as with `aws --profile`, it wins over any AWS keys
// in the process environment. Files are read afresh on every call, so keys a
// tool just rotated are the ones checked.
func awsIdentity(ctx context.Context, profile string) (string, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion("us-east-1")}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return "", err
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	if out.Account == nil {
		return "", fmt.Errorf("no account in caller identity")
	}
	return *out.Account, nil
}

// awsCredentialCandidates lists the profiles that may reach a cluster, most
// preferred first: the bound Identity Center role, the profile the kubeconfig
// itself uses, then every other profile configured for the same account.
func awsCredentialCandidates(bindingProfile, kubeconfigProfile string, accountProfiles []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(bindingProfile)
	add(kubeconfigProfile)
	for _, p := range accountProfiles {
		add(p)
	}
	return out
}

// profilesForAccount returns the ~/.aws/config profiles that sign in to
// account through IAM Identity Center.
func profilesForAccount(account string) []string {
	configPath, err := awsConfigPath()
	if err != nil {
		return nil
	}
	cfg, err := loadINIFile(configPath)
	if err != nil || cfg == nil {
		return nil
	}
	_, ssoProfiles := parseAWSSSOConfig(cfg)
	var out []string
	for _, prof := range ssoProfiles {
		if prof.AccountID == account {
			out = append(out, prof.Name)
		}
	}
	return out
}

// AWSProfileForCluster is the k8s client's resolver: the AWS profile Kanivet
// should run this context's exec plugin under, or "" to leave the kubeconfig
// as written. It prefers the bound Identity Center role, then the kubeconfig's
// own profile, and moves to another source only when the preferred one cannot
// reach the cluster's account.
func (s *Service) AWSProfileForCluster(cluster string) string {
	binding := s.clusterSSOBinding(cluster)
	path := ""
	if s.kubeconfigResolver != nil {
		path = s.kubeconfigResolver(cluster)
	}
	unbound, err := s.aws.describeBoundClusterAuth(cluster, path, nil)
	// Keys in the exec block's own environment leave no profile to choose from.
	keysInExec := unbound != nil && unbound.Method == "aws-static" && unbound.Profile == ""
	if err != nil || unbound.Provider != "aws" || keysInExec {
		if binding != nil {
			return binding.Profile
		}
		return ""
	}
	account := unbound.AccountID
	if binding != nil && binding.AccountID != "" {
		account = binding.AccountID
	}
	bindingProfile := ""
	if binding != nil {
		bindingProfile = binding.Profile
	}
	kubeconfigProfile := unbound.Profile
	preferred := bindingProfile
	if preferred == "" {
		preferred = kubeconfigProfile
	}
	if account == "" {
		return bindingProfile
	}

	candidates := awsCredentialCandidates(bindingProfile, kubeconfigProfile, profilesForAccount(account))
	if len(candidates) <= 1 {
		return bindingProfile
	}
	ctx := context.Background()
	for _, profile := range candidates {
		if !s.credentials.reaches(ctx, profile, account) {
			continue
		}
		if profile != preferred {
			log.Printf("[ClusterAuth] %s: %s cannot reach account %s; using profile %s", cluster, preferred, account, profile)
		}
		if profile == kubeconfigProfile && binding == nil {
			return ""
		}
		return profile
	}
	return bindingProfile
}

// ForgetCredentialChecks makes the next profile choice check credentials
// again. Called whenever cached cluster clients are dropped, which happens on
// auth errors, retries and sign-in changes.
func (s *Service) ForgetCredentialChecks() {
	s.credentials.forget()
}
