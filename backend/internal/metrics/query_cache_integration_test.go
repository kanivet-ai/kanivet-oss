package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/db"
	"github.com/kanivet/backend/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestQueryCacheReusesSeriesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	d := &db.DB{}
	if err := d.OpenMetricsCache(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.CloseMetricsCache() })
	c := newQueryCache(d, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	r := &rangeRecorder{}
	cachedRange(t, c, r, "prom", "up", 1000, 1200, 10)
	if err := d.CloseMetricsCache(); err != nil {
		t.Fatal(err)
	}
	if err := d.OpenMetricsCache(path); err != nil {
		t.Fatal(err)
	}
	c = newQueryCache(d, MetricsCacheBytes)
	c.now = func() time.Time { return time.Unix(100000, 0) }
	got := cachedRange(t, c, r, "prom", "up", 1000, 1230, 10)
	if len(r.calls) != 2 || r.calls[1] != [2]int64{1210, 1230} || len(got.Data.Result[0].Values) != 24 {
		t.Fatalf("restart fetches=%v, result=%+v", r.calls, got)
	}
}

// History is read by rightsizing, which caches finished days itself above
// its load limiter; a second cache below the limiter would skew it, so
// history queries must always reach the store.
func TestHistoryBypassesChartCache(t *testing.T) {
	for _, kind := range []string{"prometheus", "mimir"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var calls [][2]int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
				end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
				mu.Lock()
				calls = append(calls, [2]int64{start, end})
				mu.Unlock()
				body, err := (&rangeRecorder{value: "7"}).fetch(r.URL.Query())
				if err != nil {
					t.Error(err)
					return
				}
				defer body.Close()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.Copy(w, body)
			}))
			defer srv.Close()
			info := &ProviderInfo{Type: kind, Found: true, URL: srv.URL, Namespace: "monitoring", Service: kind, Port: 9090}
			metadata := cache.New(time.Hour, 0)
			metadata.Set(metadata.BuildKey(kind+"-info", "cluster"), &detectEntry{info: info, expires: time.Now().Add(time.Hour)}, time.Hour)
			queries := newQueryCache(nil, MetricsCacheBytes)
			pf := &PortForwardInfo{PortForward: &k8s.PortForward{ID: "pf-1", LocalPort: serverPort(t, srv)}, HTTPClient: srv.Client(), LastUsed: time.Now()}
			fc := &forwardingClient{MockClient: &k8s.MockClient{}}
			fc.register(pf.PortForward)
			var provider Provider
			query := MetricQuery{Namespace: "apps", PodName: "a", MetricType: "cpu", TimeRange: "5m", Step: "1m"}
			promQL := chartPromQL(query, time.Minute)
			if kind == "prometheus" {
				p := &PrometheusProvider{k8s: fc, cache: metadata, queries: queries}
				p.portForwardPool.Store(prometheusPoolKey("cluster", info), pf)
				provider = p
			} else {
				p := &MimirProvider{k8s: fc, cache: metadata, queries: queries}
				p.portForwardPool.Store(mimirPoolKey("cluster", info), pf)
				provider = p
			}
			s := &Service{providers: map[string]Provider{kind: provider}, queries: queries}
			chart, err := s.QueryMetrics(context.Background(), "cluster", kind, query)
			if err != nil || len(chart.Values) != 6 {
				t.Fatalf("chart=%+v, err=%v", chart, err)
			}
			mu.Lock()
			span := calls[0]
			mu.Unlock()
			ctx := WithHistoryProvider(context.Background(), kind)
			filtered, err := s.QueryRange(WithSeriesFilter(ctx, func(map[string]string) bool { return false }), "cluster", promQL, time.Unix(span[0], 0), time.Unix(span[1], 0), time.Minute)
			if err != nil || len(filtered) != 0 {
				t.Fatalf("filtered result=%v,%v", filtered, err)
			}
			got, err := s.QueryRange(ctx, "cluster", promQL, time.Unix(span[0], 0), time.Unix(span[1]+60, 0), time.Minute)
			if err != nil || len(got) != 1 || len(got[0].Values) != 7 {
				t.Fatalf("history=%+v, err=%v", got, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 3 || calls[1] != span || calls[2] != [2]int64{span[0], span[1] + 60} {
				t.Fatalf("chart/history queries=%v", calls)
			}
		})
	}
}

func TestMetricsServerSnapshotsCachedBriefly(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetrics",
		"metadata":   map[string]any{"namespace": "apps", "name": "a"},
		"timestamp":  "2026-10-02T09:15:30Z",
		"containers": []any{map[string]any{"name": "main", "usage": map[string]any{"cpu": "100m", "memory": "64Mi"}}},
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "PodMetricsList"})
	if err := dyn.Tracker().Create(gvr, obj, "apps"); err != nil {
		t.Fatal(err)
	}
	k := &k8s.MockClient{DynamicClient: dyn}
	now := time.Now()
	c := newQueryCache(nil, MetricsCacheBytes)
	c.now = func() time.Time { return now }
	p := &MetricsServerProvider{k8s: k, queries: c}
	s := &Service{k8s: k, queries: c}
	query := MetricQuery{Namespace: "apps", PodName: "a", MetricType: "cpu"}
	for i := 0; i < 2; i++ {
		got, err := p.QueryMetrics(context.Background(), "cluster", query)
		if err != nil || len(got.Values) != 1 || got.Values[0] != 100 {
			t.Fatalf("pod=%+v,%v", got, err)
		}
		if want := time.Date(2026, 10, 2, 9, 15, 30, 0, time.UTC).Local().Format("15:04:05"); got.Labels[0] != want {
			t.Fatalf("point labelled %s, want the sample time %s", got.Labels[0], want)
		}
		memoryQuery := query
		memoryQuery.MetricType = "memory"
		memory, err := p.QueryMetrics(context.Background(), "cluster", memoryQuery)
		if err != nil || memory.Values[0] != 64<<20 {
			t.Fatalf("memory=%+v,%v", memory, err)
		}
		list, err := s.ListPodMetrics(context.Background(), "cluster", "apps")
		if err != nil || len(list.Items) != 1 {
			t.Fatalf("list=%+v,%v", list, err)
		}
	}
	if len(dyn.Actions()) != 2 {
		t.Fatalf("current snapshots not cached: %v", dyn.Actions())
	}
	now = now.Add(3 * time.Second)
	obj.SetResourceVersion("2")
	_, _ = dyn.Resource(gvr).Namespace("apps").Update(context.Background(), obj, metav1.UpdateOptions{})
	_, err := p.QueryMetrics(context.Background(), "cluster", query)
	if err != nil {
		t.Fatal(err)
	}
	if len(dyn.Actions()) != 4 {
		t.Fatal("current snapshot did not refresh")
	}
}
