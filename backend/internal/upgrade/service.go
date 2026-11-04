package upgrade

import (
	"context"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	"golang.org/x/sync/singleflight"
)

// inputsTTL is how long collected evidence is reused. Switching the target
// re-scores the same evidence, so it is instant.
const inputsTTL = 2 * time.Minute

// collectTimeout bounds one full collection.
const collectTimeout = 90 * time.Second

// Service computes upgrade readiness reports. It is safe for concurrent use.
type Service struct {
	client k8s.Interface
	helm   HelmSource
	cat    *Catalog

	mu     sync.Mutex
	cache  map[string]*Inputs
	flight singleflight.Group
	// collectFn is replaced in tests.
	collectFn func(ctx context.Context, cluster string) (*Inputs, error)
}

// NewService builds the service. helmSrc may be nil (Helm manifests are then
// reported as unavailable).
func NewService(client k8s.Interface, helmSrc HelmSource) *Service {
	s := &Service{client: client, helm: helmSrc, cat: DefaultCatalog(), cache: map[string]*Inputs{}}
	s.collectFn = func(ctx context.Context, cluster string) (*Inputs, error) {
		return collect(ctx, s.cat, s.client, s.helm, cluster)
	}
	return s
}

// Catalog returns the embedded deprecation table.
func (s *Service) Catalog() *Catalog { return s.cat }

// UpgradeReport returns the readiness of cluster for an upgrade to
// targetVersion ("1.31"; empty means the next minor). refresh forces a new
// collection instead of reusing evidence up to two minutes old.
func (s *Service) UpgradeReport(ctx context.Context, cluster, targetVersion string, refresh bool) (*Report, error) {
	in, err := s.inputs(ctx, cluster, refresh)
	if err != nil {
		return nil, err
	}
	return Analyze(s.cat, cluster, in, targetVersion)
}

// Forget drops cached evidence for a cluster.
func (s *Service) Forget(cluster string) {
	s.mu.Lock()
	delete(s.cache, cluster)
	s.mu.Unlock()
}

func (s *Service) inputs(ctx context.Context, cluster string, refresh bool) (*Inputs, error) {
	if !refresh {
		s.mu.Lock()
		in := s.cache[cluster]
		s.mu.Unlock()
		if in != nil && time.Since(in.CollectedAt) < inputsTTL {
			return in, nil
		}
	}
	v, err, _ := s.flight.Do(cluster, func() (any, error) {
		// Detached from the request so a closed tab does not waste a
		// collection another caller is waiting on.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), collectTimeout)
		defer cancel()
		in, err := s.collectFn(cctx, cluster)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cache[cluster] = in
		s.mu.Unlock()
		return in, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Inputs), nil
}
