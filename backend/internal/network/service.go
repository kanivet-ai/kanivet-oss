package network

import (
	"context"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/network/traffic"
)

// Service is the network feature's API: static path analysis over the
// cluster's NetworkPolicies and Services, a live probe, and observed traffic.
// The REST handler and MCP tools wrap these methods.
type Service struct {
	source  SnapshotSource
	clients ClientSource
	traffic *traffic.Manager
}

// NewService wires the analysis to its snapshot source, the clients the
// probe uses, and the traffic sources (nil for none).
func NewService(source SnapshotSource, clients ClientSource, tm *traffic.Manager) *Service {
	if tm == nil {
		tm = traffic.NewManager()
	}
	return &Service{source: source, clients: clients, traffic: tm}
}

// CheckPath answers "can src reach dst on port?" step by step.
func (s *Service) CheckPath(ctx context.Context, cluster string, req PathRequest) (*PathResult, error) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return snap.CheckPath(req), nil
}

// PolicyCoverage reports which workloads of ns ("" for all) are isolated.
func (s *Service) PolicyCoverage(ctx context.Context, cluster, ns string) (*CoverageReport, error) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return snap.Coverage(ns), nil
}

// ResourceNetwork is what can reach a pod, workload or Service and what it
// can reach.
func (s *Service) ResourceNetwork(ctx context.Context, cluster string, r Ref) (*ResourceNetwork, error) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return snap.ResourceNetwork(r)
}

// PolicyDetail is what a NetworkPolicy selects and allows.
func (s *Service) PolicyDetail(ctx context.Context, cluster, ns, name string) (*PolicyDetail, error) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return snap.PolicyDetail(ns, name)
}

// Targets lists what the path checker can pick.
func (s *Service) Targets(ctx context.Context, cluster string) (*Targets, error) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return snap.Targets(), nil
}

// Flows returns observed flows in ns ("" for all) over window.
func (s *Service) Flows(ctx context.Context, cluster, ns string, window time.Duration) *traffic.Response {
	return s.traffic.Flows(ctx, cluster, ns, window)
}

// Probe runs a live connection test. It creates (and always deletes) a pod,
// so req.Confirm must be set.
func (s *Service) Probe(ctx context.Context, cluster string, req ProbeRequest) (*ProbeResult, error) {
	if !req.Confirm {
		return nil, ErrNotConfirmed
	}
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return nil, err
	}
	cs, err := s.clients.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	return runProbe(ctx, cs, snap, req)
}

// Presence reports, from the cached pods, whether Istio and Hubble run. It
// is the traffic sources' Presence hook.
func (s *Service) Presence(ctx context.Context, cluster string) (istio, hubble bool) {
	snap, err := s.source.Snapshot(ctx, cluster)
	if err != nil {
		return false, false
	}
	for _, p := range snap.Pods {
		switch {
		case strings.HasPrefix(p.Name, "istiod"), strings.HasPrefix(p.Name, "ztunnel"):
			istio = true
		case strings.HasPrefix(p.Name, "hubble-relay"):
			hubble = true
		}
	}
	return
}
