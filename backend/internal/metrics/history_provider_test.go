package metrics

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"
)

type historyTestProvider struct {
	Provider
	info    ProviderInfo
	paths   []string
	detects int
}

func (p *historyTestProvider) Detect(string) (*ProviderInfo, error) {
	p.detects++
	return &p.info, nil
}

func (p *historyTestProvider) promGet(_ context.Context, _, path string, _ url.Values) (io.ReadCloser, error) {
	p.paths = append(p.paths, path)
	return io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"42"]}]}}`)), nil
}

func TestHistoryQueriesUseSelectedProvider(t *testing.T) {
	for _, selected := range []string{"", "auto", "prometheus", "mimir"} {
		t.Run(selected, func(t *testing.T) {
			prom := &historyTestProvider{info: ProviderInfo{Type: "prometheus", Found: true}}
			mimir := &historyTestProvider{info: ProviderInfo{Type: "mimir", Found: true}}
			s := &Service{providers: map[string]Provider{"prometheus": prom, "mimir": mimir}}
			ctx := WithHistoryProvider(context.Background(), selected)
			expected, other := prom, mimir
			if selected == "mimir" {
				expected, other = mimir, prom
			}
			info, _ := s.HistorySource(ctx, "cluster")
			if info.Type != expected.info.Type {
				t.Fatalf("source = %s", info.Type)
			}
			at := time.Now()
			if _, err := s.QueryRange(ctx, "cluster", "up", at.Add(-time.Hour), at, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := s.QueryInstant(ctx, "cluster", "up", at); err != nil {
				t.Fatal(err)
			}
			if strings.Join(expected.paths, ",") != "/api/v1/query_range,/api/v1/query" || other.detects != 0 {
				t.Fatalf("selected paths %v, other detected %d times", expected.paths, other.detects)
			}
		})
	}
}

func TestExplicitHistoryProviderDoesNotFallBack(t *testing.T) {
	for _, selected := range []string{"mimir", "metrics-server", "disabled", "custom", "unknown"} {
		t.Run(selected, func(t *testing.T) {
			prom := &historyTestProvider{info: ProviderInfo{Type: "prometheus", Found: true}}
			mimir := &historyTestProvider{info: ProviderInfo{Type: "mimir", Reason: "unavailable"}}
			s := &Service{providers: map[string]Provider{"prometheus": prom, "mimir": mimir}}
			ctx := WithHistoryProvider(context.Background(), selected)
			info, _ := s.HistorySource(ctx, "cluster")
			if info.Found || info.Reason == "" {
				t.Fatalf("source = %+v", info)
			}
			if _, err := s.QueryInstant(ctx, "cluster", "up", time.Now()); err == nil {
				t.Fatal("expected unavailable source")
			}
			if prom.detects != 0 {
				t.Fatal("silently fell back to Prometheus")
			}
		})
	}
}

func TestHistoryAutoFallsBackToMimir(t *testing.T) {
	s := &Service{providers: map[string]Provider{
		"prometheus": &historyTestProvider{info: ProviderInfo{Type: "prometheus"}},
		"mimir":      &historyTestProvider{info: ProviderInfo{Type: "mimir", Found: true}},
	}}
	info, _ := s.HistorySource(context.Background(), "cluster")
	if info.Type != "mimir" || !info.Found {
		t.Fatalf("source = %+v", info)
	}
}
