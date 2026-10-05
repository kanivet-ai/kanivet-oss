package metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	v1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

// forwardingClient stands in for a cluster whose port-forwards land on a local
// HTTP server: CreatePrivatePortForward returns a PortForward whose LocalPort
// is the test server's port, so the probe talks to the fake API exactly as it
// would talk to a pod. Like the real manager it knows which forwards are
// running until they are stopped.
type forwardingClient struct {
	*k8s.MockClient
	localPort int
	mu        sync.Mutex
	forwards  []string
	stopped   []string
	live      map[string]*k8s.PortForward
}

func (f *forwardingClient) CreatePrivatePortForward(cluster, namespace, podName string, remotePort int) (*k8s.PortForward, error) {
	f.mu.Lock()
	id := fmt.Sprintf("%s/%s:%d#%d", namespace, podName, remotePort, len(f.forwards)+1)
	f.forwards = append(f.forwards, id)
	f.mu.Unlock()
	pf := &k8s.PortForward{ID: id, Cluster: cluster, Namespace: namespace, PodName: podName, LocalPort: f.localPort, RemotePort: remotePort, Active: true}
	f.register(pf)
	return pf, nil
}

func (f *forwardingClient) StopPortForward(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	delete(f.live, id)
	return nil
}

func (f *forwardingClient) GetPortForward(id string) (*k8s.PortForward, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pf, ok := f.live[id]
	return pf, ok
}

// register marks a forward as running, as the manager does once it is up.
func (f *forwardingClient) register(pf *k8s.PortForward) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		f.live = map[string]*k8s.PortForward{}
	}
	f.live[pf.ID] = pf
}

// exit makes a forward die on its own, as when its pod restarts.
func (f *forwardingClient) exit(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, id)
}

func (f *forwardingClient) opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forwards)
}

func (f *forwardingClient) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stopped)
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)
	return port
}

func readySlice(ns, service, pod string) *discoveryv1.EndpointSlice {
	ready := true
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: service + "-slice", Labels: map[string]string{"kubernetes.io/service-name": service}},
		Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &v1.ObjectReference{Kind: "Pod", Name: pod}}},
	}
}

func TestPrometheusDetectVerifiesAgainstTheAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status/buildinfo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"version":"2.53.1"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Two candidates: the better-ranked one is orphaned, the other answers.
	orphan := svc("monitoring", "prometheus-operated", nil, port("web", 9090, intstr.FromInt32(9090)))
	live := svc("kanivet-monitoring", "prometheus", map[string]string{"app": "prometheus"}, port("web", 9090, intstr.FromString("web")))
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-0"},
		Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "prometheus", Ports: []v1.ContainerPort{{Name: "web", ContainerPort: 9090}}}}},
	}
	clientset := fake.NewSimpleClientset(&orphan, &live, pod,
		&discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "prometheus-operated-slice", Labels: map[string]string{"kubernetes.io/service-name": "prometheus-operated"}}},
		readySlice("kanivet-monitoring", "prometheus", "prometheus-0"),
	)
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset}, localPort: serverPort(t, srv)}
	p := &PrometheusProvider{k8s: fc, cache: cache.New(time.Minute, time.Minute)}

	info, err := p.Detect("test")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Found || !info.Verified {
		t.Fatalf("expected the live service to be found and verified, got %+v", info)
	}
	if info.Namespace != "kanivet-monitoring" || info.Service != "prometheus" {
		t.Errorf("picked %s/%s, want the service that answered", info.Namespace, info.Service)
	}
	if info.Version != "2.53.1" || info.Flavor != "prometheus" || info.Port != 9090 {
		t.Errorf("version/flavor/port = %q/%q/%d", info.Version, info.Flavor, info.Port)
	}
	if len(fc.opened()) != 1 || len(fc.closed()) != 0 {
		t.Errorf("probe should open exactly one port-forward and keep it: opened %v, stopped %v", fc.opened(), fc.closed())
	}
}

// The first chart after detection used to look the pod up again and open a
// second tunnel to it. It now runs over the probe's tunnel.
func TestFirstChartReusesTheProbeTunnel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/status/buildinfo" {
			_, _ = w.Write([]byte(`{"status":"success","data":{"version":"2.53.1"}}`))
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[%s,"42"]]}]}}`, r.URL.Query().Get("end"))
	}))
	defer srv.Close()
	live := svc("monitoring", "prometheus-operated", nil, port("web", 9090, intstr.FromInt32(9090)))
	clientset := fake.NewSimpleClientset(&live, readySlice("monitoring", "prometheus-operated", "prometheus-0"))
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset}, localPort: serverPort(t, srv)}
	p := &PrometheusProvider{k8s: fc, cache: cache.New(time.Minute, time.Minute)}

	if _, err := p.Detect("test"); err != nil {
		t.Fatal(err)
	}
	apiCalls := len(clientset.Actions())
	chart, err := p.QueryMetrics(context.Background(), "test", MetricQuery{Namespace: "apps", PodName: "web-0", MetricType: "cpu", TimeRange: "15m"})
	if err != nil || chart.Values[len(chart.Values)-1] != 42 {
		t.Fatalf("chart=%+v err=%v", chart, err)
	}
	if n := len(clientset.Actions()) - apiCalls; n != 0 {
		t.Errorf("first chart made %d more API calls to find the pod again", n)
	}
	if len(fc.opened()) != 1 || len(fc.closed()) != 0 {
		t.Errorf("opened %v, stopped %v: want the probe's tunnel reused", fc.opened(), fc.closed())
	}
}

func TestMimirDetectReportsTenantRequirement(t *testing.T) {
	var sawTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/prometheus/api/v1/") {
			http.NotFound(w, r)
			return
		}
		sawTenant = r.Header.Get("X-Scope-OrgID")
		if sawTenant == "" {
			http.Error(w, "no org id", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"version":"2.13.0"}}`))
	}))
	defer srv.Close()

	gateway := svc("mimir", "mimir-nginx", nil, port("http-metric", 80, intstr.FromString("http-metric")))
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "mimir", Name: "mimir-nginx-0"},
		Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "nginx", Ports: []v1.ContainerPort{{Name: "http-metric", ContainerPort: 8080}}}}},
	}
	clientset := fake.NewSimpleClientset(&gateway, pod, readySlice("mimir", "mimir-nginx", "mimir-nginx-0"))
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset}, localPort: serverPort(t, srv)}

	t.Run("without a tenant the gateway is found but flagged", func(t *testing.T) {
		p := &MimirProvider{k8s: fc, cache: cache.New(time.Minute, time.Minute)}
		info, err := p.Detect("test")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Found || !info.NeedsTenant || info.Verified {
			t.Fatalf("got %+v, want found + needsTenant", info)
		}
		if info.Port != 8080 {
			t.Errorf("port = %d, want the container port 8080 resolved from the named targetPort", info.Port)
		}
	})

	t.Run("with a tenant it verifies", func(t *testing.T) {
		p := &MimirProvider{k8s: fc, cache: cache.New(time.Minute, time.Minute)}
		p.SetTenantLookup(func(string) string { return "team-a" })
		info, err := p.Detect("test")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Found || !info.Verified || info.NeedsTenant {
			t.Fatalf("got %+v, want found + verified", info)
		}
		if sawTenant != "team-a" || info.Version != "2.13.0" || info.Flavor != "mimir" {
			t.Errorf("tenant/version/flavor = %q/%q/%q", sawTenant, info.Version, info.Flavor)
		}
	})
}

func TestPrometheusDetectRejectsNonPrometheusAnswer(t *testing.T) {
	// A plain web server answering 200 with HTML on every path must not pass.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	service := svc("monitoring", "prometheus", nil, port("web", 9090, intstr.FromInt32(9090)))
	clientset := fake.NewSimpleClientset(&service, readySlice("monitoring", "prometheus", "prometheus-0"))
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset}, localPort: serverPort(t, srv)}
	p := &PrometheusProvider{k8s: fc, cache: cache.New(time.Minute, time.Minute)}

	info, err := p.Detect("test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Found {
		t.Fatalf("an HTML answer was accepted as Prometheus: %+v", info)
	}
	if !strings.Contains(info.Reason, "not with the Prometheus API") {
		t.Errorf("reason = %q", info.Reason)
	}
}
