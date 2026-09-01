package argo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultArgoNamespace = "argocd"
	adminSecretName      = "argocd-initial-admin-secret"
	adminUsername        = "admin"
)

type Client struct {
	kube      kubernetes.Interface
	config    *rest.Config
	namespace string
	service   string
	token     string
}

func NewClient(kube kubernetes.Interface, config *rest.Config, namespace, service string) *Client {
	if namespace == "" {
		namespace = defaultArgoNamespace
	}
	if service == "" {
		service = "argocd-server"
	}
	return &Client{kube: kube, config: config, namespace: namespace, service: service}
}

func (c *Client) ensureToken(ctx context.Context) error {
	if c.token != "" {
		return nil
	}
	tok, err := c.fetchAdminTokenFromAPI(ctx)
	if err != nil {
		return err
	}
	c.token = tok
	return nil
}

func (c *Client) fetchAdminTokenFromAPI(ctx context.Context) (string, error) {
	password, err := c.adminPassword(ctx)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"username": adminUsername, "password": password})

	rt, err := rest.TransportFor(c.config)
	if err != nil {
		return "", err
	}
	httpClient := &http.Client{Transport: rt, Timeout: 30 * time.Second}

	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, rerr := c.newProxyRequest(ctx, http.MethodPost, "/api/v1/session", bytes.NewReader(body))
		if rerr != nil {
			return "", rerr
		}
		req.Header.Set("Content-Type", "application/json")
		resp, derr := httpClient.Do(req)
		if derr != nil {
			lastErr = derr
			time.Sleep(time.Duration(400*(attempt+1)) * time.Millisecond)
			continue
		}
		if resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			lastErr = fmt.Errorf("Argo API temporarily unavailable (status %d)", resp.StatusCode)
			time.Sleep(time.Duration(400*(attempt+1)) * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("argo session failed: %s: %s", resp.Status, string(b))
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", err
		}
		if out.Token == "" {
			return "", fmt.Errorf("argo session returned empty token")
		}
		return out.Token, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("session exhausted retries")
	}
	return "", lastErr
}

func (c *Client) adminPassword(ctx context.Context) (string, error) {
	secret, err := c.kube.CoreV1().Secrets(c.namespace).Get(ctx, adminSecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", c.namespace, adminSecretName, err)
	}
	pw, ok := secret.Data["password"]
	if !ok || len(pw) == 0 {
		return "", fmt.Errorf("secret %s missing password", adminSecretName)
	}
	return string(pw), nil
}

func (c *Client) newProxyRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	host := strings.TrimRight(c.config.Host, "/")
	url := fmt.Sprintf("%s/api/v1/namespaces/%s/services/https:%s:https/proxy%s", host, c.namespace, c.service, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	rt, err := rest.TransportFor(c.config)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Transport: rt, Timeout: 30 * time.Second}
	return httpClient.Do(req)
}

// doIdempotent retries on transient 502/503/504 for safe (idempotent) requests.
// Only used for GET. Body is not retried.
func (c *Client) doIdempotent(req *http.Request) (*http.Response, error) {
	rt, err := rest.TransportFor(c.config)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Transport: rt, Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := httpClient.Do(req.Clone(req.Context()))
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(500*(attempt+1)) * time.Millisecond)
			continue
		}
		if resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			lastErr = fmt.Errorf("argo upstream %d", resp.StatusCode)
			time.Sleep(time.Duration(500*(attempt+1)) * time.Millisecond)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("exhausted retries")
	}
	return nil, lastErr
}

type SyncResourceRef struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type SyncRequest struct {
	Prune     bool              `json:"prune,omitempty"`
	DryRun    bool              `json:"dryRun,omitempty"`
	Strategy  string            `json:"strategy,omitempty"`
	Force     bool              `json:"force,omitempty"`
	Replace   bool              `json:"replace,omitempty"`
	Resources []SyncResourceRef `json:"resources,omitempty"`
	Revision  string            `json:"revision,omitempty"`
}

func (c *Client) Sync(ctx context.Context, appName string, req SyncRequest) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	payload := map[string]interface{}{
		"name":   appName,
		"prune":  req.Prune,
		"dryRun": req.DryRun,
	}
	if req.Revision != "" {
		payload["revision"] = req.Revision
	}
	if len(req.Resources) > 0 {
		payload["resources"] = req.Resources
	}
	strategy := strings.ToLower(req.Strategy)
	if strategy == "" {
		strategy = "apply"
	}
	stratPayload := map[string]interface{}{"force": req.Force}
	payload["strategy"] = map[string]interface{}{strategy: stratPayload}
	if req.Replace {
		payload["syncOptions"] = map[string]interface{}{"items": []string{"Replace=true"}}
	}
	body, _ := json.Marshal(payload)
	httpReq, err := c.newProxyRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/applications/%s/sync", appName), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readError(resp, "sync")
}

func (c *Client) Refresh(ctx context.Context, appName string, hard bool) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	kind := "normal"
	if hard {
		kind = "hard"
	}
	httpReq, err := c.newProxyRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/applications/%s?refresh=%s", appName, kind), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readError(resp, "refresh")
}

func (c *Client) Rollback(ctx context.Context, appName string, id int64, prune bool) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]interface{}{"id": id, "prune": prune})
	httpReq, err := c.newProxyRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/applications/%s/rollback", appName), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return readError(resp, "rollback")
}

type ManagedResource struct {
	Group           string `json:"group"`
	Version         string `json:"version"`
	Kind            string `json:"kind"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	TargetState     string `json:"targetState"`
	LiveState       string `json:"liveState"`
	NormalizedLive  string `json:"normalizedLiveState"`
	PredictedLive   string `json:"predictedLiveState"`
	Diff            string `json:"diff,omitempty"`
}

type managedResourcesResponse struct {
	Items []ManagedResource `json:"items"`
}

func (c *Client) ManagedResources(ctx context.Context, appName string) ([]ManagedResource, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	req, err := c.newProxyRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/applications/%s/managed-resources", appName), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doIdempotent(req)
	if err != nil {
		return nil, fmt.Errorf("argo API temporarily unavailable: %w", err)
	}
	defer resp.Body.Close()
	if err := readError(resp, "managed-resources"); err != nil {
		return nil, err
	}
	var out managedResourcesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func readError(resp *http.Response, op string) error {
	if resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if resp.StatusCode == 503 || resp.StatusCode == 502 || resp.StatusCode == 504 {
		return fmt.Errorf("Argo API temporarily unavailable (status %d). Try again in a moment.", resp.StatusCode)
	}
	return fmt.Errorf("argo %s failed: %s: %s", op, resp.Status, body)
}

