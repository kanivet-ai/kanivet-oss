package cis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kanivet/backend/internal/clusterinfo"
	"github.com/kanivet/backend/internal/health"
)

// Clients is the k8s client, as the service uses it.
type Clients interface {
	GetClientAndConfig(cluster string) (kubernetes.Interface, *rest.Config, error)
	GetDynamicClient(cluster string) (dynamic.Interface, error)
}

// SnapshotLoader is the health feature's snapshot loader (shared cache).
type SnapshotLoader interface {
	Load(ctx context.Context, cluster string) (*health.Snapshot, error)
}

// Service is the CIS feature's API: what the HTTP handler serves and what
// the MCP tools wrap (cis_benchmark, detect_provider).
type Service struct {
	clients   Clients
	loader    SnapshotLoader
	rbac      rbacGraph
	overrides *OverrideStore
	configz   ConfigzReader // tests replace the node proxy
	ttl       time.Duration
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]*cachedInputs
}

type cachedInputs struct {
	at   time.Time
	in   *Inputs
	wait chan struct{}
	err  error
}

func NewService(clients Clients, loader SnapshotLoader, rb rbacGraph, overrides *OverrideStore) *Service {
	if overrides == nil {
		overrides = NewOverrideStore("")
	}
	return &Service{clients: clients, loader: loader, rbac: rb, overrides: overrides, ttl: 90 * time.Second, now: time.Now, cache: map[string]*cachedInputs{}}
}

var errNoCluster = errors.New("cluster is required")

// inputs returns cached inputs (kubelet reads are not free); concurrent
// callers share one gather.
func (s *Service) inputs(ctx context.Context, cluster string, refresh bool) (*Inputs, error) {
	for {
		s.mu.Lock()
		c := s.cache[cluster]
		if c != nil && c.wait != nil {
			w := c.wait
			s.mu.Unlock()
			select {
			case <-w:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if c != nil && c.in != nil && !refresh && s.now().Sub(c.at) < s.ttl {
			s.mu.Unlock()
			return c.in, nil
		}
		w := make(chan struct{})
		s.cache[cluster] = &cachedInputs{wait: w}
		s.mu.Unlock()

		in, err := s.gather(ctx, cluster)
		s.mu.Lock()
		if err != nil {
			delete(s.cache, cluster)
		} else {
			s.cache[cluster] = &cachedInputs{at: s.now(), in: in}
		}
		s.mu.Unlock()
		close(w)
		return in, err
	}
}

// Options narrow a benchmark run.
type Options struct {
	Cluster string
	// Profile is a profile id; empty uses the override or the detected one.
	Profile string
	Refresh bool
}

// Benchmark runs the CIS profile for the cluster.
func (s *Service) Benchmark(ctx context.Context, o Options) (Report, error) {
	if o.Cluster == "" {
		return Report{}, errNoCluster
	}
	in, err := s.inputs(ctx, o.Cluster, o.Refresh)
	if err != nil {
		return Report{}, err
	}
	ov := s.overrides.Get(o.Cluster)
	cp := *in
	cp.Provider = clusterinfo.ApplyOverride(in.Provider, ov.Provider)
	prof := o.Profile
	if prof == "" {
		prof = ov.Profile
	}
	if prof == "" {
		prof = ProfileFor(cp.Provider.Provider)
	}
	rep := Evaluate(&cp, prof, s.now())
	rep.Cluster = o.Cluster
	return rep, nil
}

// DetectProvider returns the detected (or overridden) provider.
func (s *Service) DetectProvider(ctx context.Context, cluster string, refresh bool) (clusterinfo.Info, error) {
	if cluster == "" {
		return clusterinfo.Info{}, errNoCluster
	}
	in, err := s.inputs(ctx, cluster, refresh)
	if err != nil {
		return clusterinfo.Info{}, err
	}
	return clusterinfo.ApplyOverride(in.Provider, s.overrides.Get(cluster).Provider), nil
}

// Override returns the cluster's manual choices.
func (s *Service) Override(cluster string) Override { return s.overrides.Get(cluster) }

// SetOverride saves (or, with both fields empty, clears) the choices.
func (s *Service) SetOverride(cluster string, o Override) error {
	if cluster == "" {
		return errNoCluster
	}
	if o.Provider != "" && !validProvider(o.Provider) {
		return errors.New("unknown provider " + o.Provider)
	}
	if o.Profile != "" {
		if _, ok := profiles[o.Profile]; !ok {
			return errors.New("unknown profile " + o.Profile)
		}
	}
	return s.overrides.Set(cluster, o)
}

func validProvider(p string) bool {
	for _, x := range clusterinfo.Providers {
		if x == p {
			return true
		}
	}
	return false
}

// Override is a per-cluster manual provider and profile choice.
type Override struct {
	Provider string `json:"provider,omitempty"`
	Profile  string `json:"profile,omitempty"`
}

// OverrideStore keeps overrides in a JSON file under ~/.kanivet.
type OverrideStore struct {
	path string
	mu   sync.Mutex
	m    map[string]Override
}

func NewOverrideStore(path string) *OverrideStore {
	s := &OverrideStore{path: path, m: map[string]Override{}}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &s.m)
		}
	}
	return s
}

func (s *OverrideStore) Get(cluster string) Override {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[cluster]
}

func (s *OverrideStore) Set(cluster string, o Override) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.Provider == "" && o.Profile == "" {
		delete(s.m, cluster)
	} else {
		s.m[cluster] = o
	}
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
