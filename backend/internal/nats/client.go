package nats

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// Client reads a NATS server's monitoring endpoints through the Kubernetes
// API server's service proxy subresource. The monitor port is unauthenticated
// HTTP by NATS convention, so this needs no NATS credentials - only the same
// kubeconfig RBAC that already grants access to the service proxy subresource.
type Client struct {
	config    *rest.Config
	namespace string
	service   string
	port      int32
}

func NewClient(config *rest.Config, namespace, service string, port int32) *Client {
	return &Client{config: config, namespace: namespace, service: service, port: port}
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	rt, err := rest.TransportFor(c.config)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Transport: rt, Timeout: 15 * time.Second}

	host := strings.TrimRight(c.config.Host, "/")
	url := fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%d/proxy%s", host, c.namespace, c.service, c.port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("nats monitor %s failed: %s: %s", path, resp.Status, string(body))
	}
	return body, nil
}

// Varz returns general server info: version, uptime, connection and message
// counts, and cluster/gateway/leafnode membership.
func (c *Client) Varz(ctx context.Context) (*Varz, error) {
	body, err := c.get(ctx, "/varz")
	if err != nil {
		return nil, err
	}
	var v Varz
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode varz: %w", err)
	}
	return &v, nil
}

// Jsz returns JetStream state, with full account/stream/consumer detail so
// the account -> stream -> consumer tree needs no separate NATS client
// connection or credentials.
func (c *Client) Jsz(ctx context.Context) (*Jsz, error) {
	body, err := c.get(ctx, "/jsz?accounts=true&streams=true&consumers=true&config=true")
	if err != nil {
		return nil, err
	}
	var j Jsz
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, fmt.Errorf("decode jsz: %w", err)
	}
	return &j, nil
}

// Connz lists connected clients: address, account, subscription count, and
// per-connection message/byte counters.
func (c *Client) Connz(ctx context.Context) (*Connz, error) {
	body, err := c.get(ctx, "/connz")
	if err != nil {
		return nil, err
	}
	var cz Connz
	if err := json.Unmarshal(body, &cz); err != nil {
		return nil, fmt.Errorf("decode connz: %w", err)
	}
	return &cz, nil
}

// Healthz reports whether the server considers itself healthy.
func (c *Client) Healthz(ctx context.Context) (*Healthz, error) {
	body, err := c.get(ctx, "/healthz")
	if err != nil {
		return &Healthz{Status: "error", Error: err.Error()}, nil
	}
	var h Healthz
	if err := json.Unmarshal(body, &h); err != nil {
		return &Healthz{Status: "ok"}, nil
	}
	if h.Status == "" {
		h.Status = "ok"
	}
	return &h, nil
}
