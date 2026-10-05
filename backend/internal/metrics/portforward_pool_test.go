package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

// prometheusCluster is a cluster with one Prometheus whose pod answers on
// srv, detected and with its tunnel pooled.
func prometheusCluster(t *testing.T) (*PrometheusProvider, *forwardingClient) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status/buildinfo" {
			fmt.Fprint(w, `{"status":"success","data":{"version":"2.53.1"}}`)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[%s,"1"]]}]}}`, r.URL.Query().Get("end"))
	}))
	t.Cleanup(srv.Close)
	live := svc("monitoring", "prometheus-operated", nil, port("web", 9090, intstr.FromInt32(9090)))
	clientset := fake.NewSimpleClientset(&live, readySlice("monitoring", "prometheus-operated", "prometheus-0"))
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset}, localPort: serverPort(t, srv)}
	p := &PrometheusProvider{k8s: fc, cache: cache.New(time.Hour, 0), queries: newQueryCache(nil, MetricsCacheBytes)}
	if _, err := p.Detect("c"); err != nil {
		t.Fatal(err)
	}
	return p, fc
}

func (p *PrometheusProvider) pooledIDs() []string {
	var ids []string
	p.portForwardPool.Range(func(_, v any) bool {
		ids = append(ids, v.(*PortForwardInfo).PortForward.ID)
		return true
	})
	return ids
}

func chartOnce(t *testing.T, p Provider) {
	t.Helper()
	if _, err := p.QueryMetrics(context.Background(), "c", MetricQuery{Namespace: "apps", PodName: "web-0", MetricType: "cpu", TimeRange: "5m"}); err != nil {
		t.Fatal(err)
	}
}

// Re-detection (every 30 minutes, inline in whatever query came next) used
// to probe through the pool's own tunnel ID and stop it, cutting every chart
// and history read in flight on it.
func TestRedetectionLeavesThePoolTunnelRunning(t *testing.T) {
	p, fc := prometheusCluster(t)
	chartOnce(t, p)
	pooled := p.pooledIDs()
	if len(pooled) != 1 {
		t.Fatalf("pool %v", pooled)
	}

	// The positive detection lapses; the next query serves it stale and
	// detects again in the background.
	key := p.cache.BuildKey("prometheus-info", "c")
	v, _ := p.cache.Get(key)
	v.(*detectEntry).expires = time.Now().Add(-time.Second)
	chartOnce(t, p)
	waitFor(t, func() bool { return len(fc.opened()) == 2 && len(fc.closed()) == 1 })

	if slices.Contains(fc.closed(), pooled[0]) {
		t.Fatalf("re-detection stopped the pool's tunnel %s", pooled[0])
	}
	if got := p.pooledIDs(); !slices.Equal(got, pooled) {
		t.Fatalf("pool %v, want %v kept", got, pooled)
	}
	chartOnce(t, p)
}

// A tunnel whose forward exited (pod restart) used to stay pooled while the
// 30s recreate throttle ran, failing every chart until it lapsed.
func TestDeadPooledTunnelIsReplacedAtOnce(t *testing.T) {
	p, fc := prometheusCluster(t)
	p.queries = nil // every chart goes upstream
	chartOnce(t, p)
	dead := p.pooledIDs()[0]
	if !p.shouldRecreate("c") {
		t.Fatal("throttle")
	}
	fc.exit(dead)

	chartOnce(t, p)
	if got := p.pooledIDs(); len(got) != 1 || got[0] == dead {
		t.Fatalf("pool %v still holds the dead tunnel %s", got, dead)
	}
}

// Tunnels are private, so one left out of the pool would run until Kanivet
// exits: of two opened for the same Service at once, one is kept.
func TestPoolKeepsOneTunnelPerService(t *testing.T) {
	fc := &forwardingClient{MockClient: &k8s.MockClient{}}
	var pool sync.Map
	first, _ := fc.CreatePrivatePortForward("c", "monitoring", "prometheus-0", 9090)
	second, _ := fc.CreatePrivatePortForward("c", "monitoring", "prometheus-0", 9090)
	a := poolPortForward(&pool, fc, "k", newPortForwardInfo(first, ""))
	b := poolPortForward(&pool, fc, "k", newPortForwardInfo(second, ""))
	if a != b || a.PortForward != first || !slices.Equal(fc.closed(), []string{second.ID}) {
		t.Fatalf("kept %s and %s, stopped %v", a.PortForward.ID, b.PortForward.ID, fc.closed())
	}

	// A dead tunnel in the pool gives way to a live one.
	fc.exit(first.ID)
	third, _ := fc.CreatePrivatePortForward("c", "monitoring", "prometheus-0", 9090)
	if c := poolPortForward(&pool, fc, "k", newPortForwardInfo(third, "")); c.PortForward != third {
		t.Fatalf("kept the dead tunnel %s", c.PortForward.ID)
	}
}
