package metrics

import (
	"context"
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

func svc(ns, name string, labels map[string]string, ports ...v1.ServicePort) v1.Service {
	return v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec:       v1.ServiceSpec{Ports: ports, Selector: map[string]string{"app": name}},
	}
}

func port(name string, port int32, target intstr.IntOrString) v1.ServicePort {
	return v1.ServicePort{Name: name, Port: port, TargetPort: target}
}

func TestClassifyPrometheusService(t *testing.T) {
	tests := []struct {
		name   string
		svc    v1.Service
		ok     bool
		flavor string
		path   string
	}{
		{
			name:   "plain prometheus service (the orphaned one from the bug report)",
			svc:    svc("kanivet-monitoring", "prometheus", map[string]string{"app": "prometheus"}, port("web", 9090, intstr.FromInt32(9090))),
			ok:     true,
			flavor: "prometheus",
		},
		{
			name:   "prometheus-operated headless service",
			svc:    svc("monitoring", "prometheus-operated", map[string]string{"operated-prometheus": "true"}, port("web", 9090, intstr.FromString("web"))),
			ok:     true,
			flavor: "prometheus",
		},
		{
			name:   "identified only by label",
			svc:    svc("obs", "metrics-store", map[string]string{"app.kubernetes.io/name": "prometheus"}, port("http", 80, intstr.FromString("http-web"))),
			ok:     true,
			flavor: "prometheus",
		},
		{
			name:   "thanos query frontend",
			svc:    svc("monitoring", "thanos-query-frontend", nil, port("http", 9090, intstr.FromString("http"))),
			ok:     true,
			flavor: "thanos",
		},
		{
			name: "thanos store is not queryable",
			svc:  svc("monitoring", "thanos-store", nil, port("http", 10902, intstr.FromInt32(10902))),
			ok:   false,
		},
		{
			name:   "victoriametrics vmselect gets the select prefix",
			svc:    svc("vm", "vmselect-victoria-metrics-cluster", nil, port("http", 8481, intstr.FromInt32(8481))),
			ok:     true,
			flavor: "victoriametrics",
			path:   "/select/0/prometheus",
		},
		{
			name: "victoriametrics vminsert is write-only",
			svc:  svc("vm", "vminsert-victoria-metrics-cluster", nil, port("http", 8480, intstr.FromInt32(8480))),
			ok:   false,
		},
		{
			name: "alertmanager excluded",
			svc:  svc("monitoring", "kube-prometheus-stack-alertmanager", nil, port("web", 9093, intstr.FromInt32(9093))),
			ok:   false,
		},
		{
			name: "node exporter excluded",
			svc:  svc("monitoring", "prometheus-node-exporter", nil, port("metrics", 9100, intstr.FromInt32(9100))),
			ok:   false,
		},
		{
			name: "prometheus in agent mode has no query API",
			svc:  svc("monitoring", "prometheus-agent", nil, port("web", 9090, intstr.FromInt32(9090))),
			ok:   false,
		},
		{
			name: "grafana alloy is a shipper",
			svc:  svc("k8s-monitoring", "k8smon-alloy-singleton", map[string]string{"app.kubernetes.io/name": "alloy"}, port("http", 12345, intstr.FromInt32(12345))),
			ok:   false,
		},
		{
			name: "kube-state-metrics is an exporter",
			svc:  svc("k8s-monitoring", "k8smon-kube-state-metrics", nil, port("http", 8080, intstr.FromInt32(8080))),
			ok:   false,
		},
		{
			name: "mimir belongs to the mimir provider",
			svc:  svc("mimir", "mimir-nginx", nil, port("http-metric", 80, intstr.FromString("http-metric"))),
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := classifyPrometheusService(tt.svc)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if c.flavor != tt.flavor {
				t.Errorf("flavor = %q, want %q", c.flavor, tt.flavor)
			}
			if c.path != tt.path {
				t.Errorf("path = %q, want %q", c.path, tt.path)
			}
		})
	}
}

func TestPrometheusCandidatesRankCanonicalFirst(t *testing.T) {
	services := []v1.Service{
		svc("opencost", "opencost-prometheus-server", nil, port("http", 80, intstr.FromInt32(9090))),
		svc("kanivet-monitoring", "prometheus", nil, port("web", 9090, intstr.FromInt32(9090))),
		svc("monitoring", "prometheus-operated", nil, port("web", 9090, intstr.FromString("web"))),
		svc("monitoring", "thanos-query", nil, port("http", 9090, intstr.FromInt32(10902))),
	}
	got := prometheusCandidates(services)
	var order []string
	for _, c := range got {
		order = append(order, c.svc.Namespace+"/"+c.svc.Name)
	}
	want := "monitoring/prometheus-operated, monitoring/thanos-query, kanivet-monitoring/prometheus, opencost/opencost-prometheus-server"
	if strings.Join(order, ", ") != want {
		t.Fatalf("order = %s\nwant    %s", strings.Join(order, ", "), want)
	}
}

func TestClassifyMimirService(t *testing.T) {
	tests := []struct {
		name   string
		svc    v1.Service
		ok     bool
		flavor string
		score  int // only the role part (hundreds) is asserted
	}{
		{name: "nginx gateway", svc: svc("mimir", "mimir-nginx", nil, port("http-metric", 80, intstr.FromString("http-metric"))), ok: true, flavor: "mimir", score: 3},
		{name: "gateway by label", svc: svc("metrics", "gw", map[string]string{"app.kubernetes.io/name": "mimir", "app.kubernetes.io/component": "gateway"}, port("http", 8080, intstr.FromInt32(8080))), ok: true, flavor: "mimir", score: 3},
		{name: "query frontend", svc: svc("mimir", "mimir-query-frontend", nil, port("http-metrics", 8080, intstr.FromString("http-metrics"))), ok: true, flavor: "mimir", score: 2},
		{name: "simple scalable read path", svc: svc("mimir", "mimir-read", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: true, flavor: "mimir", score: 2},
		{name: "querier", svc: svc("mimir", "mimir-querier", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: true, flavor: "mimir", score: 1},
		{name: "cortex query frontend", svc: svc("cortex", "cortex-query-frontend", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: true, flavor: "cortex", score: 2},
		{name: "monolithic mimir", svc: svc("mimir", "mimir", map[string]string{"app.kubernetes.io/name": "mimir"}, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: true, flavor: "mimir", score: 0},
		{name: "ingester excluded", svc: svc("mimir", "mimir-ingester", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: false},
		{name: "distributor excluded", svc: svc("mimir", "mimir-distributor", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: false},
		{name: "write path excluded", svc: svc("mimir", "mimir-write", nil, port("http-metrics", 8080, intstr.FromInt32(8080))), ok: false},
		{name: "gossip ring excluded", svc: svc("mimir", "mimir-gossip-ring", nil, port("gossip", 7946, intstr.FromInt32(7946))), ok: false},
		{name: "unrelated", svc: svc("default", "web", nil, port("http", 80, intstr.FromInt32(8080))), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := classifyMimirService(tt.svc)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if d.info.Flavor != tt.flavor {
				t.Errorf("flavor = %q, want %q", d.info.Flavor, tt.flavor)
			}
			if d.score/100 != tt.score {
				t.Errorf("role score = %d, want %d (score %d)", d.score/100, tt.score, d.score)
			}
			if d.info.Path != "/prometheus" {
				t.Errorf("path = %q, want /prometheus", d.info.Path)
			}
		})
	}
}

func TestMimirCandidatesPreferHostOverVclusterCopy(t *testing.T) {
	services := []v1.Service{
		svc("vcluster-team-a", "mimir-nginx-x-mimir-x-team-a", nil, port("http-metric", 80, intstr.FromInt32(8080))),
		svc("mimir", "mimir-nginx", nil, port("http-metric", 80, intstr.FromInt32(8080))),
		svc("mimir", "mimir-query-frontend", nil, port("http-metrics", 8080, intstr.FromInt32(8080))),
	}
	got := mimirCandidatesFromServices(services)
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}
	// The host's own components, gateway then query-frontend, come before a
	// vcluster-synced copy: that copy mirrors a different Mimir with different data.
	if got[0].info.Service != "mimir-nginx" || got[0].info.Namespace != "mimir" {
		t.Errorf("first = %s/%s, want mimir/mimir-nginx", got[0].info.Namespace, got[0].info.Service)
	}
	if got[1].info.Service != "mimir-query-frontend" {
		t.Errorf("second = %s, want the host query-frontend", got[1].info.Service)
	}
	if got[2].info.Service != "mimir-nginx-x-mimir-x-team-a" {
		t.Errorf("third = %s, want the vcluster copy last", got[2].info.Service)
	}
}

func TestListServiceBackends(t *testing.T) {
	ready, notReady := true, false
	service := svc("kanivet-monitoring", "prometheus", nil, port("web", 9090, intstr.FromInt32(9090)))

	t.Run("endpointslice with no endpoints means orphaned service", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&service, &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-abc", Labels: map[string]string{"kubernetes.io/service-name": "prometheus"}},
		})
		got := listServiceBackends(context.Background(), clientset, &service)
		if got.Total != 0 || got.ReadyPod != "" {
			t.Fatalf("got %+v, want no backends", got)
		}
		if got.describe() != "no pods behind the service" {
			t.Errorf("describe = %q", got.describe())
		}
	})

	t.Run("ready endpoint yields a pod to port-forward to", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&service, &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-abc", Labels: map[string]string{"kubernetes.io/service-name": "prometheus"}},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &notReady}, TargetRef: &v1.ObjectReference{Kind: "Pod", Name: "prometheus-0"}},
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &v1.ObjectReference{Kind: "Pod", Name: "prometheus-1"}},
			},
		})
		got := listServiceBackends(context.Background(), clientset, &service)
		if got.Total != 2 || got.Ready != 1 || got.ReadyPod != "prometheus-1" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("no endpoint objects falls back to selector pods", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&service,
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-7", Labels: map[string]string{"app": "prometheus"}},
				Status:     v1.PodStatus{Phase: v1.PodRunning, Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}},
			},
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-8", Labels: map[string]string{"app": "prometheus"}},
				Status:     v1.PodStatus{Phase: v1.PodPending},
			},
		)
		got := listServiceBackends(context.Background(), clientset, &service)
		if got.Total != 2 || got.Ready != 1 || got.ReadyPod != "prometheus-7" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestResolvePodPortUsesNamedContainerPort(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "mimir", Name: "mimir-nginx-0"},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name:  "nginx",
			Ports: []v1.ContainerPort{{Name: "http-metric", ContainerPort: 8080}},
		}}},
	}
	clientset := fake.NewSimpleClientset(pod)
	sp := port("http-metric", 80, intstr.FromString("http-metric"))
	if got := resolvePodPort(context.Background(), clientset, "mimir", "mimir-nginx-0", sp, 9999); got != 8080 {
		t.Fatalf("resolved %d, want 8080 (the container port, not the service port 80)", got)
	}
	if got := resolvePodPort(context.Background(), clientset, "mimir", "missing", sp, 9999); got != 80 {
		t.Fatalf("resolved %d for unknown pod, want the service port 80", got)
	}
}

func TestDetectCachedShortLivedNegative(t *testing.T) {
	c := cache.New(time.Minute, time.Minute)
	old := detectNegativeTTL
	detectNegativeTTL = 20 * time.Millisecond
	t.Cleanup(func() { detectNegativeTTL = old })

	calls := 0
	fetch := func() (*ProviderInfo, error) {
		calls++
		return &ProviderInfo{Type: "prometheus", Found: calls > 1}, nil
	}

	first, _ := detectCached(c, "k", fetch)
	second, _ := detectCached(c, "k", fetch)
	if first.Found || second.Found || calls != 1 {
		t.Fatalf("negative result should be served from cache: calls=%d", calls)
	}
	time.Sleep(40 * time.Millisecond)
	third, _ := detectCached(c, "k", fetch)
	if !third.Found || calls != 2 {
		t.Fatalf("negative result should expire quickly: found=%v calls=%d", third.Found, calls)
	}
	fourth, _ := detectCached(c, "k", fetch)
	if !fourth.Found || calls != 2 {
		t.Fatalf("positive result should stay cached: calls=%d", calls)
	}
}

// The bug this rewrite fixes: a Service called "prometheus" with nothing
// behind it must not be reported as found.
func TestPrometheusDetectRejectsOrphanedService(t *testing.T) {
	service := svc("kanivet-monitoring", "prometheus", map[string]string{"app": "prometheus"}, port("web", 9090, intstr.FromInt32(9090)))
	clientset := fake.NewSimpleClientset(&service, &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kanivet-monitoring", Name: "prometheus-abc", Labels: map[string]string{"kubernetes.io/service-name": "prometheus"}},
	})
	p := &PrometheusProvider{k8s: &k8s.MockClient{TypedClient: clientset}, cache: cache.New(time.Minute, time.Minute)}

	info, err := p.Detect("test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Found {
		t.Fatalf("orphaned service reported as found: %+v", info)
	}
	if !strings.Contains(info.Reason, "kanivet-monitoring/prometheus") || !strings.Contains(info.Reason, "no pods behind the service") {
		t.Fatalf("reason = %q", info.Reason)
	}
}

func TestPrometheusDetectReportsProbeFailure(t *testing.T) {
	ready := true
	service := svc("monitoring", "prometheus-operated", nil, port("web", 9090, intstr.FromInt32(9090)))
	clientset := fake.NewSimpleClientset(&service, &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "prometheus-operated-x", Labels: map[string]string{"kubernetes.io/service-name": "prometheus-operated"}},
		Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &v1.ObjectReference{Kind: "Pod", Name: "prometheus-k8s-0"}}},
	})
	// The mock cannot port-forward, which stands in for a pod that does not answer.
	p := &PrometheusProvider{k8s: &k8s.MockClient{TypedClient: clientset}, cache: cache.New(time.Minute, time.Minute)}

	info, err := p.Detect("test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Found {
		t.Fatalf("unverified service reported as found: %+v", info)
	}
	if !strings.Contains(info.Reason, "port-forward to pod prometheus-k8s-0:9090 failed") {
		t.Fatalf("reason = %q", info.Reason)
	}
}

func TestMimirDetectHonoursChosenServiceOnly(t *testing.T) {
	service := svc("mimir", "mimir-nginx", nil, port("http-metric", 80, intstr.FromInt32(8080)))
	other := svc("other", "mimir-gateway", nil, port("http", 80, intstr.FromInt32(8080)))
	clientset := fake.NewSimpleClientset(&service, &other)
	p := &MimirProvider{k8s: &k8s.MockClient{TypedClient: clientset}, cache: cache.New(time.Minute, time.Minute)}
	p.SetServiceLookup(func(string) (string, string) { return "mimir", "gone" })

	info, err := p.Detect("test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Found || !strings.Contains(info.Reason, "mimir/gone no longer exists") {
		t.Fatalf("got %+v", info)
	}
}
