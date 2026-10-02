package rightsizing

import (
	"context"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/metrics"
)

type providerHistory struct{ countingStore }

func (s *providerHistory) QueryRange(ctx context.Context, cluster, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	if metrics.HistoryProvider(ctx) == "prometheus" {
		return nil, nil
	}
	return s.countingStore.QueryRange(ctx, cluster, query, start, end, step)
}

func TestProviderSwitchDoesNotReuseEmptyHistory(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	q := &providerHistory{}
	c := newChunker(q, &memChunks{data: map[string][]byte{}})
	c.now = func() time.Time { return now }
	g := newGrid(now.Add(-2*day), day, time.Hour)
	prom := metrics.WithHistoryProvider(context.Background(), "prometheus")
	mimir := metrics.WithHistoryProvider(context.Background(), "mimir")
	if _, err := c.rangeQuery(prom, "cluster", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	result, err := c.rangeQuery(mimir, "cluster", "q", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	samples := 0
	result.each(func(s *metrics.HistorySeries) { samples += len(s.Times) })
	if samples == 0 {
		t.Fatal("Mimir reused Prometheus's empty cached days")
	}
	calls := q.calls
	if _, err := c.rangeQuery(mimir, "cluster", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	if q.calls != calls {
		t.Fatal("same-provider history was not cached")
	}
}

type selectedSource struct {
	metricsSource
	selected chan string
}

func (s *selectedSource) HistorySource(ctx context.Context, _ string) (*metrics.ProviderInfo, bool) {
	selected := metrics.HistoryProvider(ctx)
	s.selected <- selected
	return &metrics.ProviderInfo{Type: selected, Reason: "unavailable"}, false
}

func TestReportSelectionIsolatedFromExistingReport(t *testing.T) {
	source := &selectedSource{selected: make(chan string, 1)}
	s := NewService(nil, source, nil, nil, nil)
	now := time.Now()
	s.runs[runKey("cluster", ProfileBalanced, defaultWindow, "prometheus")] = &run{
		ready:   &Report{Status: StatusReady, ComputedAt: now, Source: SourceInfo{Type: "prometheus"}},
		readyAt: now, loaded: true,
	}
	rep := s.GetReport("cluster", ProfileBalanced, defaultWindow, false, "", "mimir")
	if rep.Status != StatusComputing {
		t.Fatalf("reused prior report: %+v", rep)
	}
	select {
	case selected := <-source.selected:
		if selected != "mimir" {
			t.Fatalf("background computation chose %q", selected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("report did not compute")
	}
	if cached := s.Cached("cluster", ProfileBalanced, defaultWindow, "prometheus"); cached == nil || cached.Source.Type != "prometheus" {
		t.Fatal("provider switch replaced the other provider's report")
	}
}

func TestProbeCacheIsolatedByProvider(t *testing.T) {
	cluster := t.Name()
	s := NewService(nil, nil, nil, nil, nil)
	q := &probeSource{}
	s.metrics = q
	s.cachedProbe(metrics.WithHistoryProvider(context.Background(), "prometheus"), cluster, time.Now())
	s.cachedProbe(metrics.WithHistoryProvider(context.Background(), "mimir"), cluster, time.Now())
	if !q.seen["prometheus"] || !q.seen["mimir"] {
		t.Fatalf("probes queried %v", q.seen)
	}
}

type probeSource struct {
	metricsSource
	seen map[string]bool
}

func (s *probeSource) QueryInstant(ctx context.Context, _, _ string, _ time.Time) ([]metrics.HistorySeries, error) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[metrics.HistoryProvider(ctx)] = true
	return nil, nil
}

// Two stores of the same provider type, such as two Mimir tenants, must not
// share cached days.
func TestChunksKeyedByHistorySource(t *testing.T) {
	now := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC)
	q := &countingStore{}
	c := newChunker(q, &memChunks{data: map[string][]byte{}})
	c.now = func() time.Time { return now }
	g := newGrid(now.Add(-2*day), day, time.Hour)
	ctx := metrics.WithHistoryProvider(context.Background(), "mimir")
	teamA := withHistorySource(ctx, "mimir|http://mimir-nginx.mimir:80|mimir|mimir-nginx|80|/prometheus|team-a")
	teamB := withHistorySource(ctx, "mimir|http://mimir-nginx.mimir:80|mimir|mimir-nginx|80|/prometheus|team-b")
	if _, err := c.rangeQuery(teamA, "cluster", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	calls := q.calls
	if _, err := c.rangeQuery(teamB, "cluster", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	if q.calls == calls {
		t.Fatal("tenant B was served tenant A's cached days")
	}
	calls = q.calls
	if _, err := c.rangeQuery(teamA, "cluster", "q", g, nil); err != nil {
		t.Fatal(err)
	}
	if q.calls != calls {
		t.Fatal("same-source history was not cached")
	}
}
