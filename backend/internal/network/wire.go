package network

import (
	"context"
	"time"

	"github.com/kanivet/backend/internal/network/traffic"
)

// QuerierFunc adapts an instant-query function (the metrics service's) to
// traffic.Querier, evaluating at "now" with a bounded timeout.
func QuerierFunc(f func(ctx context.Context, cluster, query string, at time.Time) ([]traffic.Sample, error)) traffic.Querier {
	return func(ctx context.Context, cluster, query string) ([]traffic.Sample, error) {
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return f(qctx, cluster, query, time.Now())
	}
}

// Wire builds the service with the default sources: pods from the shared pod
// cache, the other kinds listed with a short TTL, and Istio then Hubble for
// traffic, both read through query.
func Wire(clients ClientSource, pods PodLister, query traffic.Querier) (*Service, *Handler) {
	src := NewLiveSource(clients, pods)
	svc := NewService(src, clients, nil)
	if query != nil {
		svc.traffic = traffic.NewManager(
			&traffic.Istio{Query: query, Presence: svc.Presence},
			&traffic.Hubble{Query: query, Presence: svc.Presence},
		)
	}
	return svc, NewHandler(svc)
}
