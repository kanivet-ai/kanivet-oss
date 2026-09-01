package metrics

import (
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
	QueryMetrics(cluster string, query MetricQuery) (*MetricResponse, error)
	GetName() string
	IsInstalled(cluster string) bool
}

type Service struct {
	k8s             k8s.Interface
	cache           *cache.Cache
	invalidationBus *cache.InvalidationBus
	providers       map[string]Provider
	lastCacheClear  atomic.Int64
}

type ProviderInfo struct {
	Type      string `json:"type"`
	Found     bool   `json:"found"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Port      int32  `json:"port"`
}

func NewService(k8sClient k8s.Interface, cacheInstance *cache.Cache, invalidationBus *cache.InvalidationBus) *Service {
	s := &Service{
		k8s:             k8sClient,
		cache:           cacheInstance,
		invalidationBus: invalidationBus,
		providers:       make(map[string]Provider),
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
				results[n] = &ProviderInfo{Type: n, Found: false}
			} else {
				results[n] = info
			}
		}(name, provider)
	}
	wg.Wait()

	return results, nil
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

func (s *Service) QueryMetrics(cluster string, providerType string, query MetricQuery) (*MetricResponse, error) {
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

	return provider.QueryMetrics(cluster, query)
}

// QueryWorkloadMetrics queries metrics for multiple pods in a single request (more efficient)
func (s *Service) QueryWorkloadMetrics(cluster string, query WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	if mimirProvider, exists := s.providers["mimir"]; exists {
		if mp, ok := mimirProvider.(*MimirProvider); ok && mp.IsInstalled(cluster) {
			return mp.QueryWorkloadMetrics(cluster, query)
		}
	}
	provider, exists := s.providers["prometheus"]
	if !exists {
		return nil, fmt.Errorf("prometheus provider not registered")
	}
	promProvider, ok := provider.(*PrometheusProvider)
	if !ok {
		return nil, fmt.Errorf("provider is not PrometheusProvider")
	}

	return promProvider.QueryWorkloadMetrics(cluster, query)
}

func (s *Service) GetWorkingProvider(cluster string) (*ProviderInfo, error) {
	providerOrder := []string{"prometheus", "metrics-server"}
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
