package metrics

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
)

// A 1h chart used to get a 5 minute step (13 points, the newest up to 5
// minutes old) and 6h/24h a 15 minute one, whose rate()[5m] read a third of
// each step.
func TestChartStepTable(t *testing.T) {
	for _, tc := range []struct {
		window   string
		step     time.Duration
		points   int
		interval time.Duration
	}{
		{"5m", 15 * time.Second, 21, 15 * time.Second},
		{"15m", 15 * time.Second, 61, 15 * time.Second},
		{"1h", 15 * time.Second, 241, 15 * time.Second},
		{"6h", 2 * time.Minute, 181, 30 * time.Second},
		{"24h", 10 * time.Minute, 145, 30 * time.Second},
		{"3d", 30 * time.Minute, 145, 30 * time.Second},
		{"30d", 3 * time.Hour, 241, 30 * time.Second},
	} {
		t.Run(tc.window, func(t *testing.T) {
			window, step, err := chartWindow(tc.window, "")
			if err != nil {
				t.Fatal(err)
			}
			grid := newChartGrid(chartQueryParams("up", window, step))
			if step != tc.step || grid.n != tc.points {
				t.Fatalf("step %s with %d points, want %s with %d", step, grid.n, tc.step, tc.points)
			}
			if got := StreamInterval(tc.window, 5); got != tc.interval {
				t.Fatalf("stream interval %s, want %s", got, tc.interval)
			}
		})
	}
}

func TestChartPromQL(t *testing.T) {
	pod := MetricQuery{Namespace: "apps", PodName: "web-0"}
	node := MetricQuery{NodeName: "ip-10-0-1-5.ec2.internal"}
	with := func(q MetricQuery, metric, container string) MetricQuery {
		q.MetricType, q.ContainerName = metric, container
		return q
	}
	for _, tc := range []struct {
		name string
		q    MetricQuery
		step time.Duration
		want string
	}{
		// The pod's own cgroup (container="") already sums its containers.
		{"pod cpu", with(pod, "cpu", ""), 15 * time.Second,
			`sum (rate(container_cpu_usage_seconds_total{namespace="apps",pod="web-0",container!="",container!="POD"}[300s])) * 1000`},
		{"container cpu", with(pod, "cpu", "app"), 15 * time.Second,
			`sum (rate(container_cpu_usage_seconds_total{namespace="apps",pod="web-0",container="app"}[300s])) * 1000`},
		// Working set, as metrics-server and rightsizing report it.
		{"pod memory", with(pod, "memory", ""), 15 * time.Second,
			`(sum (container_memory_working_set_bytes{namespace="apps",pod="web-0",container!="",container!="POD"}) or sum (container_memory_usage_bytes{namespace="apps",pod="web-0",container!="",container!="POD"}))`},
		// Above a minute a step keeps its peak instead of one point sample.
		{"pod memory 24h", with(pod, "memory", ""), 10 * time.Minute,
			`(sum (max_over_time(container_memory_working_set_bytes{namespace="apps",pod="web-0",container!="",container!="POD"}[600s])) or sum (max_over_time(container_memory_usage_bytes{namespace="apps",pod="web-0",container!="",container!="POD"}[600s])))`},
		// Every interface, summed, and a rate window that covers the step.
		{"pod network 24h", with(pod, "network_rx", ""), 10 * time.Minute,
			`sum (rate(container_network_receive_bytes_total{namespace="apps",pod="web-0"}[600s])) / 1024`},
		{"pod disk", with(pod, "disk_write", ""), 15 * time.Second,
			`sum (rate(container_fs_writes_bytes_total{namespace="apps",pod="web-0",container!="",container!="POD"}[300s])) / 1024`},
		// kube-prometheus-stack names the node in "node"; role=node SD in "instance".
		{"node cpu", with(node, "cpu", ""), 15 * time.Second,
			`(sum (rate(container_cpu_usage_seconds_total{node="ip-10-0-1-5.ec2.internal",container!="",container!="POD"}[300s])) or sum (rate(container_cpu_usage_seconds_total{instance="ip-10-0-1-5.ec2.internal",container!="",container!="POD"}[300s]))) * 1000`},
		{"quoted", MetricQuery{Namespace: `a"b`, PodName: `c\d`, MetricType: "network_tx"}, 15 * time.Second,
			`sum (rate(container_network_transmit_bytes_total{namespace="a\"b",pod="c\\d"}[300s])) / 1024`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := chartPromQL(tc.q, tc.step); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}

	got := workloadPromQL("apps", []string{"web-0", "web.1"}, "disk_read", 2*time.Minute)
	want := `sum by (pod) (rate(container_fs_reads_bytes_total{namespace="apps",pod=~"web-0|web\\.1",container!="",container!="POD"}[300s])) / 1024`
	if got != want {
		t.Fatalf("workload: got  %s\nwant %s", got, want)
	}
}

// promServer answers range queries with the samples answer returns for the
// requested grid, and records the queries it was asked.
func promServer(t *testing.T, answer func(start, end, step int64) string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		queries = append(queries, q.Get("query"))
		mu.Unlock()
		start, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		end, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		step, _ := strconv.ParseInt(q.Get("step"), 10, 64)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[%s]}}`, answer(start, end, step))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}
}

// pooledProvider is a provider of the given kind whose detection and tunnel
// to srv are already in place.
func pooledProvider(t *testing.T, kind string, srv *httptest.Server) Provider {
	t.Helper()
	info := &ProviderInfo{Type: kind, Found: true, URL: srv.URL, Namespace: "monitoring", Service: kind, Port: 9090}
	metadata := cache.New(time.Hour, 0)
	metadata.Set(metadata.BuildKey(kind+"-info", "cluster"), &detectEntry{info: info, expires: time.Now().Add(time.Hour)}, time.Hour)
	fc := &forwardingClient{MockClient: &k8s.MockClient{}}
	pf := &PortForwardInfo{PortForward: &k8s.PortForward{ID: "pf-1", LocalPort: serverPort(t, srv)}, HTTPClient: srv.Client(), LastUsed: time.Now()}
	fc.register(pf.PortForward)
	queries := newQueryCache(nil, MetricsCacheBytes)
	if kind == "mimir" {
		p := &MimirProvider{k8s: fc, cache: metadata, queries: queries}
		p.portForwardPool.Store(mimirPoolKey("cluster", info), pf)
		return p
	}
	p := &PrometheusProvider{k8s: fc, cache: metadata, queries: queries}
	p.portForwardPool.Store(prometheusPoolKey("cluster", info), pf)
	return p
}

// Node charts on Mimir used to ignore the node and match nothing.
func TestMimirNodeChartQueriesTheNode(t *testing.T) {
	srv, asked := promServer(t, func(_, end, _ int64) string {
		return fmt.Sprintf(`{"metric":{},"values":[[%d,"250"]]}`, end)
	})
	p := pooledProvider(t, "mimir", srv)
	chart, err := p.QueryMetrics(context.Background(), "cluster", MetricQuery{NodeName: "node-1", MetricType: "cpu", TimeRange: "15m"})
	if err != nil || chart.Values[len(chart.Values)-1] != 250 {
		t.Fatalf("chart=%+v err=%v", chart, err)
	}
	if q := asked(); len(q) != 1 || !strings.Contains(q[0], `node="node-1"`) || strings.Contains(q[0], `pod=""`) {
		t.Fatalf("queries %q", q)
	}
}

// Missing samples used to be dropped, so a series had no fixed time grid and
// charts drew it shifted. Every step is now present, null where it is empty.
func TestChartSeriesFillTheStepGrid(t *testing.T) {
	srv, _ := promServer(t, func(start, end, step int64) string {
		var samples []string
		for at := start; at <= end; at += step {
			switch {
			case at == start+step: // a scrape gap
			case at == start+2*step:
				samples = append(samples, fmt.Sprintf(`[%d,"NaN"]`, at))
			default:
				samples = append(samples, fmt.Sprintf(`[%d,"%d"]`, at, at-start))
			}
		}
		return `{"metric":{},"values":[` + strings.Join(samples, ",") + `]}`
	})
	for _, kind := range []string{"prometheus", "mimir"} {
		t.Run(kind, func(t *testing.T) {
			chart, err := pooledProvider(t, kind, srv).QueryMetrics(context.Background(), "cluster", MetricQuery{Namespace: "apps", PodName: "a", MetricType: "cpu", TimeRange: "5m"})
			if err != nil {
				t.Fatal(err)
			}
			if len(chart.Timestamps) != 21 || len(chart.Values) != 21 || len(chart.Labels) != 21 || chart.Step != 15 {
				t.Fatalf("timestamps=%d values=%d labels=%d step=%d", len(chart.Timestamps), len(chart.Values), len(chart.Labels), chart.Step)
			}
			if chart.Values[0] != 0 || !math.IsNaN(chart.Values[1]) || !math.IsNaN(chart.Values[2]) || chart.Values[3] != 45 {
				t.Fatalf("values %v", chart.Values[:4])
			}
			b, err := jsonv2.Marshal(chart)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"values":[0,null,null,45,`) {
				t.Fatalf("encoded %s", b)
			}
		})
	}
}

// One query for the whole workload, every pod on the same grid: a pod that
// started late is null before it existed instead of being drawn from the
// start of the chart.
func TestWorkloadChartAlignsPods(t *testing.T) {
	srv, asked := promServer(t, func(start, end, step int64) string {
		var early, late []string
		for at := start; at <= end; at += step {
			early = append(early, fmt.Sprintf(`[%d,"1"]`, at))
			if at > end-3*step {
				late = append(late, fmt.Sprintf(`[%d,"2"]`, at))
			}
		}
		return `{"metric":{"pod":"web-0"},"values":[` + strings.Join(early, ",") + `]},{"metric":{"pod":"web-1"},"values":[` + strings.Join(late, ",") + `]}`
	})
	p := pooledProvider(t, "prometheus", srv)
	s := &Service{providers: map[string]Provider{"prometheus": p}}
	got, err := s.QueryWorkloadMetrics(context.Background(), "cluster", "prometheus", WorkloadMetricQuery{Namespace: "apps", PodNames: []string{"web-1", "web-0"}, MetricType: "cpu", TimeRange: "15m"})
	if err != nil {
		t.Fatal(err)
	}
	n := len(got.Timestamps)
	early, late := got.Pods["web-0"], got.Pods["web-1"]
	if n != 61 || early == nil || late == nil || len(early.Values) != n || len(late.Values) != n {
		t.Fatalf("grid %d, pods %+v", n, got.Pods)
	}
	if !math.IsNaN(late.Values[0]) || late.Values[n-1] != 2 || !math.IsNaN(late.Values[n-4]) || late.Values[n-3] != 2 {
		t.Fatalf("late pod not aligned to the end: %v", late.Values[n-5:])
	}
	if q := asked(); len(q) != 1 || !strings.Contains(q[0], `pod=~"web-0|web-1"`) {
		t.Fatalf("queries %q", q)
	}
}

// The workload chart used to prefer Mimir whenever it was installed, whatever
// the user picked; it now follows the same choice as every other chart.
func TestWorkloadChartFollowsTheProviderChoice(t *testing.T) {
	prom := &workloadTestProvider{name: "prometheus"}
	mimir := &workloadTestProvider{name: "mimir"}
	s := &Service{providers: map[string]Provider{"prometheus": prom, "mimir": mimir}}
	q := WorkloadMetricQuery{Namespace: "apps", PodNames: []string{"a"}, MetricType: "cpu", TimeRange: "15m"}
	for provider, want := range map[string]*workloadTestProvider{"": prom, "prometheus": prom, "mimir": mimir} {
		prom.calls, mimir.calls = 0, 0
		if _, err := s.QueryWorkloadMetrics(context.Background(), "cluster", provider, q); err != nil {
			t.Fatal(err)
		}
		if want.calls != 1 || prom.calls+mimir.calls != 1 {
			t.Fatalf("provider %q: prometheus=%d mimir=%d", provider, prom.calls, mimir.calls)
		}
	}
}

type workloadTestProvider struct {
	Provider
	name  string
	calls int
}

func (p *workloadTestProvider) IsInstalled(string) bool { return true }

func (p *workloadTestProvider) QueryWorkloadMetrics(context.Context, string, WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	p.calls++
	return &WorkloadMetricResponse{Pods: map[string]*MetricResponse{}}, nil
}

func TestSamplesJSON(t *testing.T) {
	b, err := jsonv2.Marshal(Samples{1.5, math.NaN(), 0, 1e-9, 2e21})
	if err != nil || string(b) != `[1.5,null,0,1e-09,2e+21]` {
		t.Fatalf("%s %v", b, err)
	}
	var back Samples
	if err := jsonv2.Unmarshal(b, &back); err != nil || len(back) != 5 || !math.IsNaN(back[1]) || back[0] != 1.5 {
		t.Fatalf("%v %v", back, err)
	}
	if b, _ := jsonv2.Marshal(Samples(nil)); string(b) != `[]` {
		t.Fatalf("nil encodes as %s", b)
	}
}
