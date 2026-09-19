package metrics

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
// HTTP server: CreatePortForward returns a PortForward whose LocalPort is the
// test server's port, so the probe talks to the fake API exactly as it would
// talk to a pod.
type forwardingClient struct {
	*k8s.MockClient
	localPort int
	forwards  []string
	stopped   []string
}

func (f *forwardingClient) CreatePortForward(cluster, namespace, podName string, remotePort int) (*k8s.PortForward, error) {
	id := fmt.Sprintf("%s/%s:%d", namespace, podName, remotePort)
	f.forwards = append(f.forwards, id)
	return &k8s.PortForward{ID: id, Cluster: cluster, Namespace: namespace, PodName: podName, LocalPort: f.localPort, RemotePort: remotePort, Active: true}, nil
}

func (f *forwardingClient) StopPortForward(id string) error {
	f.stopped = append(f.stopped, id)
	return nil
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
	if len(fc.forwards) != 1 || len(fc.stopped) != 1 {
		t.Errorf("probe should open exactly one port-forward and close it: opened %v, stopped %v", fc.forwards, fc.stopped)
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
