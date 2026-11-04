package traffic

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Sample is one instant-vector element.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Querier runs an instant PromQL query against the cluster's metrics store.
type Querier func(ctx context.Context, cluster, query string) ([]Sample, error)

// Presence says whether a mesh or CNI is installed, from the pods running
// (istiod, hubble-relay). It lets a source tell "not installed" from
// "installed but its metrics are not scraped".
type Presence func(ctx context.Context, cluster string) (istio, hubble bool)

// detectTTL is how long a detection result is reused.
const detectTTL = time.Minute

type detectCache struct {
	mu sync.Mutex
	m  map[string]cachedDetect
}

type cachedDetect struct {
	at time.Time
	a  Availability
}

func (c *detectCache) get(cluster string, compute func() Availability) Availability {
	c.mu.Lock()
	if d, ok := c.m[cluster]; ok && time.Since(d.at) < detectTTL {
		c.mu.Unlock()
		return d.a
	}
	c.mu.Unlock()
	a := compute()
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]cachedDetect{}
	}
	c.m[cluster] = cachedDetect{at: time.Now(), a: a}
	c.mu.Unlock()
	return a
}

// promLabel escapes a label value for a PromQL string literal.
func promLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func promRange(d time.Duration) string {
	m := int(math.Round(d.Minutes()))
	if m < 1 {
		m = 1
	}
	return fmt.Sprintf("%dm", m)
}

func detectWith(ctx context.Context, q Querier, cluster, metric string) (bool, error) {
	samples, err := q(ctx, cluster, "count("+metric+")")
	if err != nil {
		return false, err
	}
	return len(samples) > 0 && samples[0].Value > 0, nil
}

// --- Istio ---

// Istio reads istio_requests_total (and the request duration histogram) as
// reported by the destination proxies.
type Istio struct {
	Query    Querier
	Presence Presence
	cache    detectCache
}

func (s *Istio) Name() string { return "istio" }

func (s *Istio) Detect(ctx context.Context, cluster string) Availability {
	return s.cache.get(cluster, func() Availability {
		a := Availability{Name: "Istio"}
		ok, err := detectWith(ctx, s.Query, cluster, "istio_requests_total")
		installed := false
		if s.Presence != nil {
			installed, _ = s.Presence(ctx, cluster)
		}
		switch {
		case ok:
			a.Status, a.Detail = "available", "Request metrics from the Istio proxies, via Prometheus."
		case installed && err != nil:
			a.Status, a.Detail = "detected", "Istio is installed, but no Prometheus is configured for this cluster."
			a.Hints = []string{"Configure Prometheus for this cluster in Cluster settings → Metrics.", "Scrape the Envoy sidecars (Istio's Prometheus integration does this)."}
		case installed:
			a.Status, a.Detail = "detected", "Istio is installed, but Prometheus has no istio_requests_total series."
			a.Hints = []string{"Make Prometheus scrape the sidecars' /stats/prometheus endpoint (Istio's prometheus addon or the ServiceMonitor from its docs)."}
		default:
			a.Status, a.Detail = "unavailable", "Istio is not installed."
			a.Hints = []string{"Install Istio (or Istio ambient) and its Prometheus integration to see HTTP request rates and error rates between workloads."}
		}
		return a
	})
}

func (s *Istio) Flows(ctx context.Context, cluster, namespace string, window time.Duration) ([]Flow, error) {
	r := promRange(window)
	by := "source_workload, source_workload_namespace, destination_workload, destination_workload_namespace, destination_service_name, request_protocol"
	sel := func(extra string) string {
		base := `reporter="destination"` + extra
		if namespace == "" {
			return fmt.Sprintf(`sum by (%s) (rate(istio_requests_total{%s}[%s]))`, by, base, r)
		}
		ns := promLabel(namespace)
		return fmt.Sprintf(`sum by (%s) (rate(istio_requests_total{%s, source_workload_namespace="%s"}[%s])) or sum by (%s) (rate(istio_requests_total{%s, destination_workload_namespace="%s"}[%s]))`, by, base, ns, r, by, base, ns, r)
	}
	total, err := s.Query(ctx, cluster, sel(""))
	if err != nil {
		return nil, err
	}
	errs, _ := s.Query(ctx, cluster, sel(`, response_code=~"5.."`))
	p95q := fmt.Sprintf(`histogram_quantile(0.95, sum by (le, source_workload, source_workload_namespace, destination_workload, destination_workload_namespace) (rate(istio_request_duration_milliseconds_bucket{reporter="destination"}[%s])))`, r)
	p95, _ := s.Query(ctx, cluster, p95q)

	key := func(l map[string]string) string {
		return l["source_workload_namespace"] + "/" + l["source_workload"] + ">" + l["destination_workload_namespace"] + "/" + l["destination_workload"] + "|" + l["destination_service_name"] + "|" + l["request_protocol"]
	}
	pair := func(l map[string]string) string {
		return l["source_workload_namespace"] + "/" + l["source_workload"] + ">" + l["destination_workload_namespace"] + "/" + l["destination_workload"]
	}
	errBy := map[string]float64{}
	for _, e := range errs {
		errBy[key(e.Labels)] += e.Value
	}
	p95By := map[string]float64{}
	for _, p := range p95 {
		if !math.IsNaN(p.Value) {
			p95By[pair(p.Labels)] = p.Value
		}
	}
	flows := map[string]*Flow{}
	var order []string
	for _, t := range total {
		k := key(t.Labels)
		f := flows[k]
		if f == nil {
			l := t.Labels
			f = &Flow{
				Source:      Endpoint{Namespace: l["source_workload_namespace"], Workload: unknownIfEmpty(l["source_workload"])},
				Destination: Endpoint{Namespace: l["destination_workload_namespace"], Workload: unknownIfEmpty(l["destination_workload"]), Service: l["destination_service_name"]},
				Protocol:    strings.ToUpper(defaultStr(l["request_protocol"], "http")),
				P95Ms:       p95By[pair(l)],
			}
			flows[k] = f
			order = append(order, k)
		}
		f.RPS += t.Value
		f.ErrorRPS = errBy[k]
	}
	return finishFlows(flows, order), nil
}

// --- Hubble ---

// Hubble reads hubble_flows_processed_total, which carries workload labels
// when Hubble metrics are enabled with a source/destination labelsContext.
type Hubble struct {
	Query    Querier
	Presence Presence
	cache    detectCache
}

func (s *Hubble) Name() string { return "hubble" }

const hubbleContextHint = `Enable Hubble flow metrics with workload context, e.g. Helm values hubble.metrics.enabled={"flow:sourceContext=workload-name|reserved-identity;destinationContext=workload-name|reserved-identity;labelsContext=source_namespace,destination_namespace","drop:labelsContext=source_namespace,destination_namespace"} and let Prometheus scrape them (hubble.metrics.serviceMonitor.enabled=true).`

func (s *Hubble) Detect(ctx context.Context, cluster string) Availability {
	return s.cache.get(cluster, func() Availability {
		a := Availability{Name: "Hubble (Cilium)"}
		installed := false
		if s.Presence != nil {
			_, installed = s.Presence(ctx, cluster)
		}
		samples, err := s.Query(ctx, cluster, `count by (source, destination) (hubble_flows_processed_total)`)
		hasSeries := err == nil && len(samples) > 0
		withContext := false
		for _, smp := range samples {
			if smp.Labels["source"] != "" || smp.Labels["destination"] != "" {
				withContext = true
				break
			}
		}
		switch {
		case withContext:
			a.Status, a.Detail = "available", "Flow metrics from Hubble, via Prometheus."
		case hasSeries:
			a.Status, a.Detail = "detected", "Hubble flow metrics are scraped, but without source and destination context."
			a.Hints = []string{hubbleContextHint}
		case installed && err != nil:
			a.Status, a.Detail = "detected", "Hubble Relay is running, but no Prometheus is configured for this cluster."
			a.Hints = []string{"Configure Prometheus for this cluster in Cluster settings → Metrics.", hubbleContextHint}
		case installed:
			a.Status, a.Detail = "detected", "Hubble Relay is running, but its flow metrics are not in Prometheus."
			a.Hints = []string{hubbleContextHint}
		default:
			a.Status, a.Detail = "unavailable", "Cilium Hubble is not running."
			a.Hints = []string{"With Cilium as the CNI, enable Hubble (cilium hubble enable) and its flow metrics to see L3/L4 flows and drops between workloads."}
		}
		return a
	})
}

func (s *Hubble) Flows(ctx context.Context, cluster, namespace string, window time.Duration) ([]Flow, error) {
	r := promRange(window)
	by := "source, source_namespace, destination, destination_namespace, protocol"
	q := func(extra string) string {
		if namespace == "" {
			return fmt.Sprintf(`sum by (%s) (rate(hubble_flows_processed_total{%s}[%s]))`, by, strings.TrimPrefix(extra, ", "), r)
		}
		ns := promLabel(namespace)
		return fmt.Sprintf(`sum by (%s) (rate(hubble_flows_processed_total{source_namespace="%s"%s}[%s])) or sum by (%s) (rate(hubble_flows_processed_total{destination_namespace="%s"%s}[%s]))`, by, ns, extra, r, by, ns, extra, r)
	}
	total, err := s.Query(ctx, cluster, q(""))
	if err != nil {
		return nil, err
	}
	drops, _ := s.Query(ctx, cluster, q(`, verdict="DROPPED"`))
	key := func(l map[string]string) string {
		return l["source_namespace"] + "/" + l["source"] + ">" + l["destination_namespace"] + "/" + l["destination"] + "|" + l["protocol"]
	}
	dropBy := map[string]float64{}
	for _, d := range drops {
		dropBy[key(d.Labels)] += d.Value
	}
	flows := map[string]*Flow{}
	var order []string
	for _, t := range total {
		l := t.Labels
		if l["source"] == "" && l["destination"] == "" {
			continue
		}
		k := key(l)
		f := flows[k]
		if f == nil {
			f = &Flow{
				Source:      Endpoint{Namespace: l["source_namespace"], Workload: unknownIfEmpty(l["source"])},
				Destination: Endpoint{Namespace: l["destination_namespace"], Workload: unknownIfEmpty(l["destination"])},
				Protocol:    strings.ToUpper(defaultStr(l["protocol"], "ip")),
			}
			flows[k] = f
			order = append(order, k)
		}
		f.RPS += t.Value
		f.ErrorRPS = dropBy[k]
	}
	return finishFlows(flows, order), nil
}

func finishFlows(flows map[string]*Flow, order []string) []Flow {
	out := make([]Flow, 0, len(order))
	for _, k := range order {
		f := flows[k]
		if f.RPS <= 0 {
			continue
		}
		f.ErrorRate = f.ErrorRPS / f.RPS
		f.RPS, f.ErrorRPS = round3(f.RPS), round3(f.ErrorRPS)
		f.ErrorRate = round3(f.ErrorRate)
		f.P95Ms = round3(f.P95Ms)
		out = append(out, *f)
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func unknownIfEmpty(s string) string {
	if s == "" || s == "unknown" {
		return "unknown"
	}
	return s
}

func defaultStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
