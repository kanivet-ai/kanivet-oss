package traffic

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type match struct {
	sub string
	out []Sample
}

// fakeQuerier answers with the first match whose substring the query has.
func fakeQuerier(m []match, err error) Querier {
	return func(_ context.Context, _ string, q string) ([]Sample, error) {
		if err != nil {
			return nil, err
		}
		for _, x := range m {
			if strings.Contains(q, x.sub) {
				return x.out, nil
			}
		}
		return nil, nil
	}
}

func TestIstioFlows(t *testing.T) {
	l := map[string]string{"source_workload": "web", "source_workload_namespace": "a", "destination_workload": "api", "destination_workload_namespace": "b", "destination_service_name": "api", "request_protocol": "http"}
	q := fakeQuerier([]match{
		{"count(istio_requests_total)", []Sample{{Value: 3}}},
		{`response_code=~"5.."`, []Sample{{Labels: l, Value: 0.5}}},
		{"histogram_quantile", []Sample{{Labels: l, Value: 42}}},
		{"rate(istio_requests_total", []Sample{{Labels: l, Value: 10}}},
	}, nil)
	m := NewManager(&Istio{Query: q}, &Hubble{Query: q})
	r := m.Flows(context.Background(), "c", "a", 5*time.Minute)
	if !r.Available || r.Source != "istio" || len(r.Flows) != 1 {
		t.Fatalf("%+v", r)
	}
	f := r.Flows[0]
	if f.RPS != 10 || f.ErrorRPS != 0.5 || f.ErrorRate != 0.05 || f.P95Ms != 42 || f.Destination.Service != "api" || f.Protocol != "HTTP" {
		t.Fatalf("flow %+v", f)
	}
	if len(r.Sources) != 2 || r.Sources[1].Status != "unavailable" {
		t.Fatalf("sources %+v", r.Sources)
	}
}

func TestNothingAvailableGivesHints(t *testing.T) {
	q := fakeQuerier(nil, errors.New("no prometheus"))
	presence := func(context.Context, string) (bool, bool) { return false, true }
	r := NewManager(&Istio{Query: q, Presence: presence}, &Hubble{Query: q, Presence: presence}).Flows(context.Background(), "c", "", 0)
	if r.Available || len(r.Flows) != 0 || r.Window != "5m0s" {
		t.Fatalf("%+v", r)
	}
	if r.Sources[0].Status != "unavailable" || r.Sources[1].Status != "detected" || len(r.Sources[1].Hints) == 0 {
		t.Fatalf("%+v", r.Sources)
	}
}

func TestHubbleNeedsContext(t *testing.T) {
	q := fakeQuerier([]match{{"count by (source, destination)", []Sample{{Labels: map[string]string{}, Value: 4}}}}, nil)
	a := (&Hubble{Query: q}).Detect(context.Background(), "c")
	if a.Status != "detected" || !strings.Contains(a.Hints[0], "sourceContext") {
		t.Fatalf("%+v", a)
	}
}

func TestPromLabelEscapes(t *testing.T) {
	if got := promLabel(`a"b\c`); got != `a\"b\\c` {
		t.Fatal(got)
	}
}
