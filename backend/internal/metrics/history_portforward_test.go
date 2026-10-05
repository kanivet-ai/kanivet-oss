package metrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
)

// discoveryCounter counts credential discoveries, each of which starts by
// asking for the dynamic client.
type discoveryCounter struct {
	*forwardingClient
	discoveries atomic.Int32
}

func (d *discoveryCounter) GetDynamicClient(cluster string) (dynamic.Interface, error) {
	d.discoveries.Add(1)
	return d.forwardingClient.GetDynamicClient(cluster)
}

// pooledMimir is a Mimir provider whose detection and port-forward are
// already in place, reaching localPort.
func pooledMimir(t *testing.T, client k8s.Interface, localPort int) (*MimirProvider, *ProviderInfo, *PortForwardInfo) {
	t.Helper()
	info := &ProviderInfo{Type: "mimir", Found: true, Namespace: "mimir", Service: "mimir-nginx", URL: "http://mimir-nginx.mimir.svc.cluster.local:80/prometheus", Port: 8080}
	p := &MimirProvider{k8s: client, cache: cache.New(time.Minute, 0)}
	p.cache.Set(p.cache.BuildKey("mimir-info", "cluster"), &detectEntry{info: info, expires: time.Now().Add(time.Hour)}, time.Hour)
	httpClient := &http.Client{Transport: &http.Transport{}}
	p.authenticateClient(httpClient, "cluster", info)
	pf := &PortForwardInfo{PortForward: &k8s.PortForward{ID: "pf-1", LocalPort: localPort}, HTTPClient: httpClient, LastUsed: time.Now()}
	if live, ok := client.(interface{ register(*k8s.PortForward) }); ok {
		live.register(pf.PortForward)
	}
	p.portForwardPool.Store(mimirPoolKey("cluster", info), pf)
	return p, info, pf
}

// A gateway that wants credentials nobody can find is still reachable: the
// tunnel must stay pooled (and so reapable), and the failed discovery must
// not run again on every request.
func TestMimirMissingCredentialsKeepPortForward(t *testing.T) {
	var challenges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		challenges.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="Mimir"`)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "unauthorized")
	}))
	defer srv.Close()
	fc := &discoveryCounter{forwardingClient: &forwardingClient{MockClient: &k8s.MockClient{}}}
	p, info, pf := pooledMimir(t, fc, serverPort(t, srv))

	for range 3 {
		body, err := p.promGet(context.Background(), "cluster", "/api/v1/query", nil)
		if body != nil {
			body.Close()
		}
		var authErr *MimirAuthError
		if !errors.As(err, &authErr) || errors.Is(err, errPortForwardDropped) {
			t.Fatalf("err = %v, want a credentials error", err)
		}
	}
	if got, ok := p.portForwardPool.Load(mimirPoolKey("cluster", info)); !ok || got != pf {
		t.Fatal("a credentials error removed the live port-forward from the pool")
	}
	if len(fc.stopped) != 0 {
		t.Fatalf("stopped %v", fc.stopped)
	}
	if n := fc.discoveries.Load(); n != 1 {
		t.Fatalf("credentials discovered %d times, want once while the failure is remembered", n)
	}
	if n := challenges.Load(); n != 3 {
		t.Fatalf("gateway asked %d times", n)
	}
}

// A dead tunnel is stopped as it leaves the pool, so nothing is left running
// unreaped, and the caller is told so it can retry over a new one.
func TestMimirDeadPortForwardIsStopped(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := serverPort(t, srv)
	srv.Close() // nothing listens any more: connection refused
	fc := &forwardingClient{MockClient: &k8s.MockClient{}}
	p, info, _ := pooledMimir(t, fc, port)

	_, err := p.promGet(context.Background(), "cluster", "/api/v1/query", nil)
	if !errors.Is(err, errPortForwardDropped) {
		t.Fatalf("err = %v, want a dropped port-forward", err)
	}
	if _, ok := p.portForwardPool.Load(mimirPoolKey("cluster", info)); ok {
		t.Fatal("dead port-forward still pooled")
	}
	if len(fc.stopped) != 1 || fc.stopped[0] != "pf-1" {
		t.Fatalf("stopped %v, want the dead port-forward", fc.stopped)
	}
}

// The challenge's body is read before the request is sent again with
// credentials, so both go over one connection.
func TestMimirChallengeIsDrainedBeforeRetry(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "reader" || pass != "secret-value" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Mimir"`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, strings.Repeat("x", 32<<10))
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	client := &k8s.MockClient{
		TypedClient:   fake.NewSimpleClientset(authSecret()),
		DynamicClient: authDynamic(authCollector("PrometheusAgent", "http://mimir-nginx.mimir.svc.cluster.local/api/v1/push")),
	}
	p, _, _ := pooledMimir(t, &forwardingClient{MockClient: client}, serverPort(t, srv))
	body, err := p.promGet(context.Background(), "cluster", "/api/v1/query", nil)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	if n := conns.Load(); n != 1 {
		t.Fatalf("%d connections for a challenged request, want 1", n)
	}
}

func TestHistorySourceKeyTellsStoresApart(t *testing.T) {
	key := func(service, tenant string) string {
		info := &ProviderInfo{Type: "mimir", Found: true, Namespace: "mimir", Service: service, URL: "http://" + service + ".mimir:80/prometheus", Port: 8080, Path: "/prometheus"}
		p := &MimirProvider{cache: cache.New(time.Minute, 0)}
		p.cache.Set(p.cache.BuildKey("mimir-info", "cluster"), &detectEntry{info: info, expires: time.Now().Add(time.Hour)}, time.Hour)
		p.SetTenantLookup(func(string) string { return tenant })
		s := &Service{providers: map[string]Provider{"mimir": p}}
		return s.HistorySourceKey(WithHistoryProvider(context.Background(), "mimir"), "cluster")
	}
	a := key("mimir-nginx", "team-a")
	if a == "" || a == key("mimir-nginx", "team-b") || a == key("mimir-gateway", "team-a") {
		t.Fatalf("source keys do not tell tenants or services apart: %q", a)
	}
	if a != key("mimir-nginx", "team-a") {
		t.Fatal("source key is not stable")
	}
}

// promStream generates a range-query answer of n series on the fly, so the
// test itself holds almost none of it in memory: every series shares one
// encoded run of samples.
type promStream struct {
	n, i   int
	values []byte
	header []byte
	queue  [][]byte
	done   bool
}

func newPromStream(n, points int) *promStream {
	var b []byte
	for j := range points {
		if j > 0 {
			b = append(b, ',')
		}
		b = append(b, '[')
		b = strconv.AppendInt(b, int64(1700000000+60*j), 10)
		b = append(b, `,"0.123456"]`...)
	}
	return &promStream{n: n, values: b, queue: [][]byte{[]byte(`{"status":"success","data":{"resultType":"matrix","result":[`)}}
}

func (s *promStream) Read(p []byte) (int, error) {
	for len(s.queue) == 0 {
		switch {
		case s.done:
			return 0, io.EOF
		case s.i < s.n:
			s.header = s.header[:0]
			if s.i > 0 {
				s.header = append(s.header, ',')
			}
			s.header = append(s.header, `{"metric":{"namespace":"apps","pod":"pod-`...)
			s.header = strconv.AppendInt(s.header, int64(s.i), 10)
			s.header = append(s.header, `","container":"main"},"values":[`...)
			s.queue = append(s.queue, s.header, s.values, []byte("]}"))
			s.i++
		default:
			s.queue = append(s.queue, []byte(`]}}`))
			s.done = true
		}
	}
	n := copy(p, s.queue[0])
	if s.queue[0] = s.queue[0][n:]; len(s.queue[0]) == 0 {
		s.queue = s.queue[1:]
	}
	return n, nil
}

// History answers are decoded as they arrive, never read whole: a large
// answer whose series are all filtered out costs a small fraction of its size.
// The chart cache, which buffers answers, must not be in the way.
func TestHistoryQueryStreamsPastChartCache(t *testing.T) {
	const series, points = 2000, 240
	var size atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n, _ := io.Copy(w, newPromStream(series, points))
		size.Store(n)
	}))
	defer srv.Close()
	info := &ProviderInfo{Type: "prometheus", Found: true, URL: srv.URL, Namespace: "monitoring", Service: "prometheus", Port: 9090}
	metadata := cache.New(time.Hour, 0)
	metadata.Set(metadata.BuildKey("prometheus-info", "cluster"), &detectEntry{info: info, expires: time.Now().Add(time.Hour)}, time.Hour)
	fc := &forwardingClient{MockClient: &k8s.MockClient{}}
	pf := &k8s.PortForward{ID: "pf-1", LocalPort: serverPort(t, srv)}
	fc.register(pf)
	p := &PrometheusProvider{k8s: fc, cache: metadata, queries: newQueryCache(nil, MetricsCacheBytes)}
	p.portForwardPool.Store(prometheusPoolKey("cluster", info), &PortForwardInfo{PortForward: pf, HTTPClient: srv.Client(), LastUsed: time.Now()})
	s := &Service{providers: map[string]Provider{"prometheus": p}}

	ctx := WithSeriesFilter(WithHistoryProvider(context.Background(), "prometheus"), func(l map[string]string) bool { return l["pod"] == "pod-7" })
	start := time.Unix(1700000000, 0)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, err := s.QueryRange(ctx, "cluster", "up", start, start.Add(time.Duration(points-1)*time.Minute), time.Minute)
	runtime.ReadMemStats(&after)
	if err != nil || len(res) != 1 || len(res[0].Values) != points {
		t.Fatalf("result=%d series, err=%v", len(res), err)
	}
	if size.Load() < 10<<20 {
		t.Fatalf("answer only %d bytes; the test needs a large one", size.Load())
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(size.Load())/4 {
		t.Fatalf("allocated %d bytes for a %d byte answer: it was buffered", alloc, size.Load())
	}
}
