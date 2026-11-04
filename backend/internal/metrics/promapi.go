package metrics

import (
	"context"
	"fmt"
	"io"
	"net/url"
)

// promReadPaths are the read-only Prometheus HTTP API endpoints PromGet
// may call (metric metadata, labels and rules), besides queries.
var promReadPaths = map[string]bool{
	"/api/v1/metadata":              true,
	"/api/v1/labels":                true,
	"/api/v1/label/__name__/values": true,
	"/api/v1/rules":                 true,
	"/api/v1/alerts":                true,
	"/api/v1/series":                true,
}

// PromGet calls a read-only Prometheus API endpoint on the cluster's history
// store (the same Prometheus/Thanos/VictoriaMetrics/Mimir discovery chart
// queries use) and returns at most maxBytes of the raw JSON body.
func (s *Service) PromGet(ctx context.Context, cluster, path string, params url.Values, maxBytes int64) ([]byte, bool, error) {
	if !promReadPaths[path] {
		return nil, false, fmt.Errorf("prometheus path %q not allowed", path)
	}
	api, err := s.historyProvider(ctx, cluster)
	if err != nil {
		return nil, false, err
	}
	body, err := api.promGet(ctx, cluster, path, params)
	if err != nil {
		return nil, false, err
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	truncated := int64(len(b)) > maxBytes
	if truncated {
		b = b[:maxBytes]
	}
	return b, truncated, nil
}
