package metrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
)

type Provider interface {
	Detect(cluster string) (*ProviderInfo, error)
	Install(cluster string, namespace string) error
	QueryMetrics(ctx context.Context, cluster string, query MetricQuery) (*MetricResponse, error)
	GetName() string
	IsInstalled(cluster string) bool
}

// workloadProvider charts several pods of a workload with one query.
type workloadProvider interface {
	QueryWorkloadMetrics(ctx context.Context, cluster string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error)
}

type Service struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	invalidationBus *cache.InvalidationBus
	providers       map[string]Provider
	lastCacheClear  atomic.Int64
	queries         *queryCache
}

// ProviderInfo is the result of detecting one provider in one cluster. Found
// means the Service has ready pods behind it and the Prometheus-compatible API
// answered a probe (or, for Mimir, answered 401 asking for a tenant); Reason
// says in plain words why it is not found or not usable yet.
type ProviderInfo struct {
	Type      string `json:"type"`
	Found     bool   `json:"found"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Port      int32  `json:"port"`
	// Flavor is the concrete implementation behind a provider type:
	// prometheus | thanos | victoriametrics for "prometheus", mimir | cortex
	// for "mimir".
	Flavor string `json:"flavor,omitempty"`
	// Path is the URL prefix the API lives under ("/prometheus" for Mimir).
	Path string `json:"path,omitempty"`
	// Verified is true when the API answered the detection probe.
	Verified bool `json:"verified,omitempty"`
	// NeedsTenant is set when a multi-tenant gateway answered 401: it is
	// reachable but will not return data until a tenant is configured.
	NeedsTenant bool   `json:"needsTenant,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func NewService(k8sClient k8s.Interface, cacheInstance *cache.Cache, invalidationBus *cache.InvalidationBus) *Service {
	s := &Service{
		k8s:             k8sClient,
		cache:           cacheInstance,
		invalidationBus: invalidationBus,
		providers:       make(map[string]Provider),
		queries:         newQueryCache(nil, MetricsCacheBytes),
	}

	prometheusProvider := NewPrometheusProvider(k8sClient, cacheInstance)
	s.RegisterProvider("prometheus", prometheusProvider)

	mimirProvider := NewMimirProvider(k8sClient, cacheInstance)
	s.RegisterProvider("mimir", mimirProvider)

	metricsServerProvider := NewMetricsServerProvider(k8sClient, cacheInstance)
	s.RegisterProvider("metrics-server", metricsServerProvider)

	if invalidationBus != nil {
		invalidationBus.Subscribe(s)
	}

	return s
}

// SetMimirTenantLookup wires a per-cluster Mimir tenant resolver. Typically
// backed by SQLite — see cmd/main.go where it's hooked to the
// ClusterMetricsSettings table.
func (s *Service) SetMimirTenantLookup(fn func(cluster string) string) {
	if mp, ok := s.providers["mimir"].(*MimirProvider); ok {
		mp.SetTenantLookup(fn)
	}
}

// SetMimirServiceLookup wires a per-cluster resolver for the operator-chosen
// Mimir service, overriding auto-discovery. Backed by SQLite — see cmd/main.go.
func (s *Service) SetMimirServiceLookup(fn func(cluster string) (namespace, service string)) {
	if mp, ok := s.providers["mimir"].(*MimirProvider); ok {
		mp.SetServiceLookup(fn)
	}
}

// MimirProviderForDiscovery returns the Mimir provider so the API layer can
// run tenant-discovery probes against it. Returns nil if Mimir isn't
// registered.
func (s *Service) MimirProviderForDiscovery() *MimirProvider {
	if mp, ok := s.providers["mimir"].(*MimirProvider); ok {
		return mp
	}
	return nil
}

// OnInvalidate implements InvalidationListener interface
func (s *Service) OnInvalidate(pattern string) {
	if pattern != "" && !strings.Contains(pattern, "pods") && !strings.Contains(pattern, "items:") {
		return
	}
	now := time.Now().UnixNano()
	last := s.lastCacheClear.Load()
	if now-last < int64(2*time.Second) || !s.lastCacheClear.CompareAndSwap(last, now) {
		return
	}
	s.cache.DeleteByPrefix("metrics:")
}

func (s *Service) RegisterProvider(name string, provider Provider) {
	s.providers[name] = provider
	switch p := provider.(type) {
	case *PrometheusProvider:
		p.queries = s.queries
	case *MimirProvider:
		p.queries = s.queries
	case *MetricsServerProvider:
		p.queries = s.queries
	}
}

// SetQueryCacheStore is called at startup, before queries are accepted.
func (s *Service) SetQueryCacheStore(store QueryCacheStore) {
	s.queries = newQueryCache(store, MetricsCacheBytes)
	for name, provider := range s.providers {
		s.RegisterProvider(name, provider)
	}
}

func (s *Service) DetectAllProviders(cluster string) (map[string]*ProviderInfo, error) {
	results := make(map[string]*ProviderInfo)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, provider := range s.providers {
		wg.Add(1)
		go func(n string, p Provider) {
			defer wg.Done()
			info, err := p.Detect(cluster)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[n] = &ProviderInfo{Type: n, Found: false, Reason: "detection failed: " + trimErr(err)}
			} else {
				results[n] = info
			}
		}(name, provider)
	}
	wg.Wait()

	return results, nil
}

// InvalidateDetection forgets every cached detection result for a cluster so
// the next DetectAllProviders call probes again. Used by the UI's "Detect
// again" and after an install.
func (s *Service) InvalidateDetection(cluster string) {
	for _, name := range []string{"prometheus-info", "mimir-info", "mimir-candidates", "metrics-server-info"} {
		s.cache.Delete(s.cache.BuildKey(name, cluster))
	}
}

func (s *Service) DetectProvider(cluster string, providerType string) (*ProviderInfo, error) {
	provider, exists := s.providers[providerType]
	if !exists {
		return nil, fmt.Errorf("provider %s not registered", providerType)
	}

	return provider.Detect(cluster)
}

func (s *Service) InstallProvider(cluster string, providerType string, namespace string) error {
	provider, exists := s.providers[providerType]
	if !exists {
		return fmt.Errorf("provider %s not registered", providerType)
	}

	if namespace == "" {
		namespace = "kanivet-monitoring"
	}

	return provider.Install(cluster, namespace)
}

// chartProvider is the provider a chart reads: the one asked for, or in auto
// mode the first one installed of Prometheus, Mimir and metrics-server.
func (s *Service) chartProvider(cluster, providerType string) (Provider, error) {
	if providerType == "" {
		providerOrder := []string{"prometheus", "mimir", "metrics-server"}
		for _, name := range providerOrder {
			if provider, exists := s.providers[name]; exists && provider.IsInstalled(cluster) {
				providerType = name
				break
			}
		}
	}

	if providerType == "" {
		return nil, fmt.Errorf("no metrics provider found in cluster")
	}

	provider, exists := s.providers[providerType]
	if !exists {
		return nil, fmt.Errorf("provider %s not registered", providerType)
	}
	return provider, nil
}

func (s *Service) QueryMetrics(ctx context.Context, cluster string, providerType string, query MetricQuery) (*MetricResponse, error) {
	provider, err := s.chartProvider(cluster, providerType)
	if err != nil {
		return nil, err
	}
	return provider.QueryMetrics(ctx, cluster, query)
}

// QueryWorkloadMetrics charts several pods at once, from the same provider
// QueryMetrics would pick. Prometheus-compatible stores answer with one
// query; metrics-server, which only knows current values, pod by pod.
func (s *Service) QueryWorkloadMetrics(ctx context.Context, cluster string, providerType string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	provider, err := s.chartProvider(cluster, providerType)
	if err != nil {
		return nil, err
	}
	if wp, ok := provider.(workloadProvider); ok {
		return wp.QueryWorkloadMetrics(ctx, cluster, query)
	}
	response := &WorkloadMetricResponse{Pods: map[string]*MetricResponse{}}
	var firstErr error
	for _, pod := range query.PodNames {
		data, err := provider.QueryMetrics(ctx, cluster, MetricQuery{PodName: pod, Namespace: query.Namespace, MetricType: query.MetricType, TimeRange: query.TimeRange, Step: query.Step})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(data.Values) == 0 {
			continue
		}
		// One current value per pod, read at about the same moment.
		if len(data.Timestamps) > 0 && (len(response.Timestamps) == 0 || data.Timestamps[0] > response.Timestamps[0]) {
			response.Timestamps = data.Timestamps[:1]
		}
		response.Pods[pod] = &MetricResponse{Values: data.Values[:1], Unit: data.Unit}
	}
	if len(response.Pods) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return response, nil
}

func (s *Service) GetWorkingProvider(cluster string) (*ProviderInfo, error) {
	providerOrder := []string{"prometheus", "mimir", "metrics-server"}
	for _, name := range providerOrder {
		if provider, exists := s.providers[name]; exists {
			info, err := provider.Detect(cluster)
			if err == nil && info.Found {
				return info, nil
			}
		}
	}
	return nil, fmt.Errorf("no working metrics provider found")
}

func (s *Service) GetAvailableProviders() []string {
	providers := make([]string, 0, len(s.providers))
	for name := range s.providers {
		providers = append(providers, name)
	}
	return providers
}
