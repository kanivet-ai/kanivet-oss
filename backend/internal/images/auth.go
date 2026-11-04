package images

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/google"
	corev1 "k8s.io/api/core/v1"
)

// dockerConfig is the shape of .dockerconfigjson (and, under "auths", of the
// legacy .dockercfg map).
type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

type dockerAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Auth          string `json:"auth"`
	IdentityToken string `json:"identitytoken"`
	RegistryToken string `json:"registrytoken"`
}

// secretKeychain resolves credentials from Kubernetes pull secrets. It holds
// them in memory for one inspection only.
type secretKeychain struct {
	entries map[string]authn.AuthConfig // normalized registry host -> creds
}

// parsePullSecret extracts registry credentials from a dockerconfigjson or
// dockercfg secret. Other secret types yield nothing.
func parsePullSecret(s *corev1.Secret) map[string]authn.AuthConfig {
	out := map[string]authn.AuthConfig{}
	var auths map[string]dockerAuth
	switch s.Type {
	case corev1.SecretTypeDockerConfigJson:
		var cfg dockerConfig
		if json.Unmarshal(s.Data[corev1.DockerConfigJsonKey], &cfg) != nil {
			return out
		}
		auths = cfg.Auths
	case corev1.SecretTypeDockercfg:
		if json.Unmarshal(s.Data[corev1.DockerConfigKey], &auths) != nil {
			return out
		}
	default:
		// Some tools create Opaque secrets with a .dockerconfigjson key.
		if raw, ok := s.Data[corev1.DockerConfigJsonKey]; ok {
			var cfg dockerConfig
			if json.Unmarshal(raw, &cfg) == nil {
				auths = cfg.Auths
			}
		}
	}
	for host, a := range auths {
		ac := authn.AuthConfig{Username: a.Username, Password: a.Password, IdentityToken: a.IdentityToken, RegistryToken: a.RegistryToken}
		if a.Auth != "" && ac.Username == "" {
			if dec, err := base64.StdEncoding.DecodeString(a.Auth); err == nil {
				if u, p, ok := strings.Cut(string(dec), ":"); ok {
					ac.Username, ac.Password = u, p
				}
			}
		}
		out[normalizeRegistry(host)] = ac
	}
	return out
}

// normalizeRegistry maps docker config keys ("https://index.docker.io/v1/",
// "docker.io", "registry.example.com:5000/path") to a registry host.
func normalizeRegistry(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.TrimPrefix(k, "https://")
	k = strings.TrimPrefix(k, "http://")
	if i := strings.Index(k, "/"); i >= 0 {
		k = k[:i]
	}
	switch k {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return name.DefaultRegistry
	}
	return k
}

func (k *secretKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	if k == nil || len(k.entries) == 0 {
		return authn.Anonymous, nil
	}
	host := normalizeRegistry(r.RegistryStr())
	if ac, ok := k.entries[host]; ok {
		return authn.FromConfig(ac), nil
	}
	// Wildcard entries such as "*.azurecr.io" (kubelet supports them).
	for pattern, ac := range k.entries {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) {
			return authn.FromConfig(ac), nil
		}
	}
	return authn.Anonymous, nil
}

// labeledKeychain records which source produced credentials.
type labeledKeychain struct {
	label string
	kc    authn.Keychain
	hit   *authLabel
}

func (l labeledKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	a, err := l.kc.Resolve(r)
	if err != nil {
		return authn.Anonymous, nil // a broken helper must not block the others
	}
	if a != authn.Anonymous && l.hit != nil {
		l.hit.mu.Lock()
		if l.hit.label == "" {
			l.hit.label = l.label
		}
		l.hit.mu.Unlock()
	}
	return a, nil
}

// buildKeychain chains pull secrets, then the local docker config and
// credential helpers, then cloud keychains. The first non-anonymous answer
// wins. method receives the label of the source used.
func buildKeychain(ctx context.Context, secrets *secretKeychain, method *authLabel) authn.Keychain {
	return authn.NewMultiKeychain(
		labeledKeychain{"pull-secret", secrets, method},
		labeledKeychain{"docker-config", authn.DefaultKeychain, method},
		labeledKeychain{"gcloud", google.Keychain, method},
		labeledKeychain{"aws-ecr", ecrKeychain{ctx: ctx}, method},
		labeledKeychain{"azure-acr", acrKeychain{ctx: ctx}, method},
	)
}
