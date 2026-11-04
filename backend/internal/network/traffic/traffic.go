// Package traffic reports observed service-to-service flows from whatever
// telemetry the cluster has: Istio's request metrics or Hubble's flow
// metrics, both read from the cluster's Prometheus. Each integration is a
// Source; the Manager asks them in order and uses the first that has data.
package traffic

import (
	"context"
	"sort"
	"time"
)

// Endpoint is one end of a flow.
type Endpoint struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	// Service is the destination Service, when the source knows it.
	Service string `json:"service,omitempty"`
}

// Flow is the traffic between two workloads over the window.
type Flow struct {
	Source      Endpoint `json:"source"`
	Destination Endpoint `json:"destination"`
	Protocol    string   `json:"protocol"`
	// RPS is requests (or, for L3/L4 sources, flows) per second.
	RPS float64 `json:"rps"`
	// ErrorRPS counts failed requests (5xx) or dropped flows per second.
	ErrorRPS float64 `json:"errorRps"`
	// ErrorRate is ErrorRPS / RPS.
	ErrorRate float64 `json:"errorRate"`
	// P95Ms is the 95th percentile latency when the source reports it.
	P95Ms float64 `json:"p95Ms,omitempty"`
}

// Availability is what a source found.
type Availability struct {
	Name string `json:"name"`
	// Status: available | detected (installed but no usable metrics) |
	// unavailable.
	Status string   `json:"status"`
	Detail string   `json:"detail"`
	Hints  []string `json:"hints,omitempty"`
}

// Source is one telemetry integration.
type Source interface {
	Name() string
	Detect(ctx context.Context, cluster string) Availability
	Flows(ctx context.Context, cluster, namespace string, window time.Duration) ([]Flow, error)
}

// Response is what Flows returns.
type Response struct {
	Available bool           `json:"available"`
	Source    string         `json:"source,omitempty"`
	Window    string         `json:"window"`
	Flows     []Flow         `json:"flows"`
	Sources   []Availability `json:"sources"`
	Error     string         `json:"error,omitempty"`
}

// Manager picks a source and asks it for flows.
type Manager struct {
	sources []Source
}

func NewManager(sources ...Source) *Manager { return &Manager{sources: sources} }

// Flows returns the flows of namespace ("" for all) over window, from the
// first available source. With none, Sources say what was found and how to
// set one up.
func (m *Manager) Flows(ctx context.Context, cluster, namespace string, window time.Duration) *Response {
	if window <= 0 {
		window = 5 * time.Minute
	}
	resp := &Response{Window: window.String(), Flows: []Flow{}, Sources: []Availability{}}
	for _, s := range m.sources {
		a := s.Detect(ctx, cluster)
		resp.Sources = append(resp.Sources, a)
		if a.Status != "available" || resp.Available {
			continue
		}
		flows, err := s.Flows(ctx, cluster, namespace, window)
		if err != nil {
			resp.Error = err.Error()
			continue
		}
		resp.Available, resp.Source = true, s.Name()
		resp.Flows = flows
	}
	sort.Slice(resp.Flows, func(i, j int) bool { return resp.Flows[i].RPS > resp.Flows[j].RPS })
	return resp
}
