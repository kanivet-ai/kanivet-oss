package metrics

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type mimirCredentials struct {
	username string
	password string
}

type mimirAuthTransport struct {
	base     http.RoundTripper
	provider *MimirProvider
	cluster  string
	info     ProviderInfo
}

func (p *MimirProvider) authenticateClient(client *http.Client, cluster string, info *ProviderInfo) {
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &mimirAuthTransport{base: base, provider: p, cluster: cluster, info: *info}
	// Credentials belong to this port-forward, never to a redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

func requiresBasicAuth(resp *http.Response) bool {
	if resp.StatusCode != http.StatusUnauthorized {
		return false
	}
	for _, challenge := range resp.Header.Values("WWW-Authenticate") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(challenge)), "basic ") {
			return true
		}
	}
	return false
}

const (
	// credentialsTTL is how long discovered credentials are reused before
	// the collector configuration is read again.
	credentialsTTL = 5 * time.Minute
	// missingCredentialsTTL is how long a failed discovery is remembered, so
	// a cluster without readable credentials isn't listed on every request.
	missingCredentialsTTL = time.Minute
	// credentialDiscoveryTimeout bounds one discovery, which runs apart from
	// any caller's request so one cancelled caller can't fail the others
	// waiting on it.
	credentialDiscoveryTimeout = 15 * time.Second
)

// MimirAuthError means the gateway asked for Basic credentials and none could
// be found or the ones found were rejected. It says nothing about the
// port-forward, which stays usable.
type MimirAuthError struct{ Problem string }

func (e *MimirAuthError) Error() string {
	return "Mimir requires Basic authentication; " + e.Problem
}

func (t *mimirAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := "mimir-auth:" + mimirPoolKey(t.cluster, &t.info)
	send := func(credentials *mimirCredentials) (*http.Response, error) {
		copy := req.Clone(req.Context())
		if credentials != nil {
			copy.SetBasicAuth(credentials.username, credentials.password)
		}
		return t.base.RoundTrip(copy)
	}
	var credentials *mimirCredentials
	if cached, ok := t.provider.cache.Get(key); ok {
		credentials = cached.(*mimirCredentials)
	}
	resp, err := send(credentials)
	if err != nil || !requiresBasicAuth(resp) {
		return resp, err
	}
	// Read what is left of the challenge so the connection is reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if credentials != nil {
		// Resolve again after a rejection so a rotated Secret works immediately.
		t.provider.cache.Delete(key)
	}
	credentials, err = t.credentials(req.Context(), key)
	if err != nil {
		return nil, err
	}
	resp, err = send(credentials)
	if err == nil && requiresBasicAuth(resp) {
		t.provider.cache.Delete(key)
		t.provider.cache.Set(key+":missing", &MimirAuthError{Problem: "the collector's credentials were rejected"}, missingCredentialsTTL)
	}
	return resp, err
}

// credentials returns the gateway's credentials, discovering them at most
// once at a time per gateway. The caller stops waiting when its own request
// ends; the discovery carries on for the others.
func (t *mimirAuthTransport) credentials(ctx context.Context, key string) (*mimirCredentials, error) {
	if missing, ok := t.provider.cache.Get(key + ":missing"); ok {
		return nil, missing.(*MimirAuthError)
	}
	type result struct {
		credentials any
		err         error
	}
	done := make(chan result, 1)
	go func() {
		v, err := t.provider.cache.GetOrSet(key, credentialsTTL, func() (any, error) {
			dctx, cancel := context.WithTimeout(context.Background(), credentialDiscoveryTimeout)
			defer cancel()
			credentials, err := t.provider.discoverCredentials(dctx, t.cluster, &t.info)
			if err != nil {
				t.provider.cache.Set(key+":missing", err, missingCredentialsTTL)
				return nil, err
			}
			return credentials, nil
		})
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return r.credentials.(*mimirCredentials), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Read only Secret keys explicitly referenced by collectors writing to this
// Service. A similarly named Secret or another Mimir instance is not evidence
// that credentials belong to the selected gateway.
func (p *MimirProvider) discoverCredentials(ctx context.Context, cluster string, info *ProviderInfo) (*mimirCredentials, *MimirAuthError) {
	dynamicClient, err := p.k8s.GetDynamicClient(cluster)
	if err != nil {
		return nil, &MimirAuthError{Problem: "cannot inspect collector configuration"}
	}
	client, err := p.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, &MimirAuthError{Problem: "cannot read credential Secrets"}
	}
	var problem string
	for _, resource := range []schema.GroupVersionResource{
		{Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "prometheusagents"},
		{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheuses"},
	} {
		collectors, err := dynamicClient.Resource(resource).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			problem = "cannot list Prometheus/PrometheusAgent resources"
			continue
		}
		for _, collector := range collectors.Items {
			writes, _, _ := unstructured.NestedSlice(collector.Object, "spec", "remoteWrite")
			for _, entry := range writes {
				write, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				endpoint, _, _ := unstructured.NestedString(write, "url")
				if !mimirWriteTargetsService(endpoint, collector.GetNamespace(), info) {
					continue
				}
				values := make(map[string]string)
				for _, field := range []string{"username", "password"} {
					name, _, _ := unstructured.NestedString(write, "basicAuth", field, "name")
					key, _, _ := unstructured.NestedString(write, "basicAuth", field, "key")
					if name == "" || key == "" {
						continue
					}
					secret, err := client.CoreV1().Secrets(collector.GetNamespace()).Get(ctx, name, metav1.GetOptions{})
					if err != nil {
						problem = "cannot read the collector's referenced credential Secret"
						continue
					}
					if value, exists := secret.Data[key]; exists {
						values[field] = string(value)
					} else {
						problem = "the collector's credential Secret is missing a referenced key"
					}
				}
				username, hasUsername := values["username"]
				password, hasPassword := values["password"]
				if hasUsername && hasPassword {
					return &mimirCredentials{username: username, password: password}, nil
				}
			}
		}
	}
	if problem == "" {
		problem = "no collector references Basic-auth credentials for the selected Service"
	}
	return nil, &MimirAuthError{Problem: problem}
}

func mimirWriteTargetsService(endpoint, collectorNamespace string, info *ProviderInfo) bool {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	serviceURL, err := url.Parse(info.URL)
	if err != nil || port != serviceURL.Port() {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	service := info.Service + "." + info.Namespace
	return host == service || host == service+".svc" || host == service+".svc.cluster.local" ||
		(host == info.Service && collectorNamespace == info.Namespace)
}
