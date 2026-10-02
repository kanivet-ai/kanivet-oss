package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func authCollector(kind, endpoint string) *unstructured.Unstructured {
	version := "monitoring.coreos.com/v1"
	if kind == "PrometheusAgent" {
		version = "monitoring.coreos.com/v1alpha1"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": version, "kind": kind,
		"metadata": map[string]any{"name": "agent", "namespace": "monitoring"},
		"spec": map[string]any{"remoteWrite": []any{map[string]any{
			"url": endpoint,
			"basicAuth": map[string]any{
				"username": map[string]any{"name": "gateway-auth", "key": "user"},
				"password": map[string]any{"name": "gateway-auth", "key": "pass"},
			},
		}}},
	}}
}

func authDynamic(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "monitoring.coreos.com", Version: "v1alpha1", Resource: "prometheusagents"}: "PrometheusAgentList",
		{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheuses"}:           "PrometheusList",
	}, objects...)
}

func authSecret() *v1.Secret {
	return &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gateway-auth", Namespace: "monitoring"},
		Data: map[string][]byte{"user": []byte("reader"), "pass": []byte("secret-value")}}
}

func TestMimirAuthenticatedDiscoveryAndQueries(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	password := "secret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader" || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Mimir"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		paths[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prometheus/api/v1/status/buildinfo":
			fmt.Fprint(w, `{"status":"success","data":{"version":"2.13.0"}}`)
		case "/distributor/all_user_stats":
			fmt.Fprint(w, `[{"userID":"team-a"}]`)
		case "/prometheus/api/v1/labels":
			if r.Header.Get("X-Scope-OrgID") != "team-a" {
				t.Error("tenant discovery did not preserve its candidate header")
			}
			fmt.Fprint(w, `{"status":"success","data":["pod"]}`)
		case "/prometheus/api/v1/query_range":
			if r.Header.Get("X-Scope-OrgID") != "team-a" {
				t.Error("metric query lost its tenant header")
			}
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"pod-a"},"values":[[1234,"7"]]}]}}`)
		}
	}))
	defer srv.Close()

	gateway := svc("mimir", "mimir-nginx", nil, port("http", 80, intstr.FromInt32(8080)))
	clientset := fake.NewSimpleClientset(&gateway, readySlice("mimir", "mimir-nginx", "nginx-0"), authSecret())
	fc := &forwardingClient{MockClient: &k8s.MockClient{TypedClient: clientset,
		DynamicClient: authDynamic(authCollector("PrometheusAgent", "http://mimir-nginx.mimir.svc.cluster.local/api/v1/push"))}, localPort: serverPort(t, srv)}
	p := &MimirProvider{k8s: fc, cache: cache.New(time.Minute, 0)}
	p.SetTenantLookup(func(string) string { return "team-a" })
	info, err := p.Detect("cluster-a")
	if err != nil || !info.Verified || info.NeedsTenant {
		t.Fatalf("detection = %+v, %v", info, err)
	}
	tenants, err := p.DiscoverTenants("cluster-a", nil)
	if err != nil || len(tenants) != 1 || tenants[0] != "team-a" {
		t.Fatalf("tenants = %v, %v", tenants, err)
	}
	result, err := p.QueryMetrics("cluster-a", MetricQuery{Namespace: "app", PodName: "pod-a", MetricType: "cpu", TimeRange: "5m"})
	if err != nil || len(result.Values) != 1 || result.Values[0] != 7 {
		t.Fatalf("query = %+v, %v", result, err)
	}

	// Existing tunnels must pick up a rotated Secret on the first rejected request.
	secret := authSecret()
	secret.Data["pass"] = []byte("rotated")
	if _, err := clientset.CoreV1().Secrets("monitoring").Update(context.Background(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	password = "rotated"
	mu.Unlock()
	workload, err := p.QueryWorkloadMetrics("cluster-a", WorkloadMetricQuery{Namespace: "app", PodNames: []string{"pod-a"}, MetricType: "cpu", TimeRange: "5m"})
	if err != nil || workload.Pods["pod-a"] == nil || workload.Pods["pod-a"].Values[0] != 7 {
		t.Fatalf("workload query after rotation = %+v, %v", workload, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/prometheus/api/v1/status/buildinfo", "/distributor/all_user_stats", "/prometheus/api/v1/labels", "/prometheus/api/v1/query_range"} {
		if paths[path] == 0 {
			t.Errorf("no authenticated request to %s", path)
		}
	}
}

func TestMimirCredentialDiscoveryScopeAndErrors(t *testing.T) {
	info := &ProviderInfo{Namespace: "mimir", Service: "mimir-nginx", URL: "http://mimir-nginx.mimir.svc.cluster.local:80/prometheus"}
	for _, tc := range []struct {
		name, endpoint, problem string
		denySecret              bool
	}{
		{name: "Prometheus reference", endpoint: "http://mimir-nginx.mimir/api/v1/push"},
		{name: "another service", endpoint: "http://other.mimir/api/v1/push", problem: "no collector references"},
		{name: "external hostname", endpoint: "https://mimir.example.com/api/v1/push", problem: "no collector references"},
		{name: "another port", endpoint: "http://mimir-nginx.mimir:8081/api/v1/push", problem: "no collector references"},
		{name: "forbidden Secret", endpoint: "http://mimir-nginx.mimir/api/v1/push", denySecret: true, problem: "cannot read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(authSecret())
			if tc.denySecret {
				clientset.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "gateway-auth", fmt.Errorf("denied"))
				})
			}
			p := &MimirProvider{k8s: &k8s.MockClient{TypedClient: clientset, DynamicClient: authDynamic(authCollector("Prometheus", tc.endpoint))}}
			credentials, err := p.discoverCredentials(context.Background(), "test", info)
			if tc.problem == "" {
				if err != nil || credentials.username != "reader" || credentials.password != "secret-value" {
					t.Fatal("referenced credentials were not resolved")
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.problem) || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("unexpected diagnostic: %v", err)
			}
			if strings.Contains(tc.problem, "no collector") && len(clientset.Actions()) != 0 {
				t.Fatal("read a Secret for an unrelated service")
			}
		})
	}
}

func TestMimirBasicAuthIsNotATenantError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Mimir"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	outcome := probeOnce(srv.Client(), srv.URL, nil)
	if outcome.NeedsTenant || outcome.OK || !strings.Contains(outcome.Detail, "Basic authentication") {
		t.Fatalf("Basic authentication was misclassified: %+v", outcome)
	}
}

func TestMimirCredentialsDoNotFollowRedirects(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	info := &ProviderInfo{Namespace: "mimir", Service: "mimir-nginx"}
	p := &MimirProvider{cache: cache.New(time.Minute, 0)}
	p.cache.Set("mimir-auth:"+mimirPoolKey("test", info), &mimirCredentials{username: "reader", password: "secret"}, time.Minute)
	client := source.Client()
	p.authenticateClient(client, "test", info)
	resp, err := client.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || redirected {
		t.Fatal("authenticated request followed a redirect")
	}
}
