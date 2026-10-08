package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/kanivet/backend/internal/k8s"
)

const (
	testHost     = "host"
	testVCluster = k8s.VClusterIDPrefix + testHost + ":pink:pink-vc"
)

type fakeHostPods struct {
	k8s.Interface
	pods  map[string]*k8s.VClusterHostPod // by virtual name
	err   error
	calls int
}

func (f *fakeHostPods) ResolveVClusterHostPod(_, _, name string) (*k8s.VClusterHostPod, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if p, ok := f.pods[name]; ok {
		return p, nil
	}
	return nil, k8s.ErrVClusterHostPodNotFound
}

func (f *fakeHostPods) ResolveVClusterHostPods(_, _ string, names []string) (map[string]*k8s.VClusterHostPod, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]*k8s.VClusterHostPod{}
	for _, n := range names {
		if p, ok := f.pods[n]; ok {
			out[n] = p
		}
	}
	return out, nil
}

// storeFake is a provider; hostStoreFake is one that reads the host's series.
type storeFake struct {
	queries     int
	gotCluster  string
	gotQuery    MetricQuery
	gotWorkload WorkloadMetricQuery
	detected    []string
	answer      *WorkloadMetricResponse
}

func (p *storeFake) Detect(cluster string) (*ProviderInfo, error) {
	p.detected = append(p.detected, cluster)
	return &ProviderInfo{Found: true}, nil
}
func (*storeFake) Install(string, string) error { return nil }
func (*storeFake) GetName() string              { return "fake" }
func (p *storeFake) IsInstalled(cluster string) bool {
	p.detected = append(p.detected, cluster)
	return true
}
func (p *storeFake) QueryMetrics(_ context.Context, cluster string, q MetricQuery) (*MetricResponse, error) {
	p.queries++
	p.gotCluster, p.gotQuery = cluster, q
	return &MetricResponse{Values: Samples{1}}, nil
}
func (p *storeFake) QueryWorkloadMetrics(_ context.Context, cluster string, q WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	p.queries++
	p.gotCluster, p.gotWorkload = cluster, q
	return p.answer, nil
}

type hostStoreFake struct{ storeFake }

func (*hostStoreFake) readsHostSeries() {}

func vclusterService(r *fakeHostPods, name string, p Provider) *Service {
	return &Service{k8s: r, providers: map[string]Provider{name: p}}
}

var hostPod = &k8s.VClusterHostPod{Host: testHost, Namespace: "pink", Name: "web-0-x-apps-x-pink-vc"}

func TestStoreCluster(t *testing.T) {
	if got := StoreCluster(testVCluster); got != testHost {
		t.Fatalf("vcluster store is %q, want its host", got)
	}
	if got := StoreCluster("prod"); got != "prod" {
		t.Fatalf("ordinary cluster store is %q", got)
	}
}

func TestVClusterPodChartReadsHostStoreUnderHostNames(t *testing.T) {
	r := &fakeHostPods{pods: map[string]*k8s.VClusterHostPod{"web-0": hostPod}}
	p := &hostStoreFake{}
	s := vclusterService(r, "mimir", p)
	q := MetricQuery{PodName: "web-0", Namespace: "apps", ContainerName: "app", MetricType: "cpu", TimeRange: "15m"}
	for i := 0; i < 2; i++ {
		if _, err := s.QueryMetrics(context.Background(), testVCluster, "mimir", q); err != nil {
			t.Fatal(err)
		}
	}
	if p.gotCluster != testHost {
		t.Fatalf("queried through %q, want the host cluster", p.gotCluster)
	}
	if p.gotQuery.Namespace != "pink" || p.gotQuery.PodName != "web-0-x-apps-x-pink-vc" || p.gotQuery.ContainerName != "app" {
		t.Fatalf("queried %+v", p.gotQuery)
	}
	if r.calls != 1 {
		t.Fatalf("host pod resolved %d times, want 1 (cached)", r.calls)
	}
}

func TestVClusterAutoProviderIsDetectedOnHost(t *testing.T) {
	r := &fakeHostPods{pods: map[string]*k8s.VClusterHostPod{"web-0": hostPod}}
	p := &hostStoreFake{}
	s := vclusterService(r, "mimir", p)
	if _, err := s.QueryMetrics(context.Background(), testVCluster, "", MetricQuery{PodName: "web-0", Namespace: "apps", MetricType: "cpu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DetectAllProviders(testVCluster); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DetectProvider(testVCluster, "mimir"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWorkingProvider(testVCluster); err != nil {
		t.Fatal(err)
	}
	if len(p.detected) != 4 {
		t.Fatalf("detected %d times, want 4", len(p.detected))
	}
	for _, c := range p.detected {
		if c != testHost {
			t.Fatalf("detected in %q, want the host cluster", c)
		}
	}
}

func TestVClusterNodeChartReadsHostStore(t *testing.T) {
	r := &fakeHostPods{}
	p := &hostStoreFake{}
	s := vclusterService(r, "mimir", p)
	_, _ = s.QueryMetrics(context.Background(), testVCluster, "mimir", MetricQuery{NodeName: "node-1", MetricType: "cpu"})
	if p.gotCluster != testHost || p.gotQuery.NodeName != "node-1" || r.calls != 0 {
		t.Fatalf("cluster %q query %+v lookups %d", p.gotCluster, p.gotQuery, r.calls)
	}
}

func TestVClusterPodWithoutHostCopyChartsNothing(t *testing.T) {
	p := &hostStoreFake{}
	r := &fakeHostPods{}
	s := vclusterService(r, "mimir", p)
	for i := 0; i < 2; i++ {
		got, err := s.QueryMetrics(context.Background(), testVCluster, "mimir", MetricQuery{PodName: "new", Namespace: "apps", MetricType: "cpu"})
		if err != nil || len(got.Values) != 0 {
			t.Fatalf("got %+v, %v", got, err)
		}
	}
	if p.queries != 0 {
		t.Fatal("the host store was asked about virtual names, which could name another pod there")
	}
	if r.calls != 1 {
		t.Fatalf("%d lookups for a pod with no host copy, want 1 (remembered)", r.calls)
	}
	// The workload chart of the same pod reuses what the pod chart learned.
	if _, err := s.QueryWorkloadMetrics(context.Background(), testVCluster, "mimir", WorkloadMetricQuery{PodNames: []string{"new"}, Namespace: "apps"}); err != nil || r.calls != 1 {
		t.Fatalf("workload chart looked the pod up again: %d lookups, %v", r.calls, err)
	}
}

func TestVClusterHostLookupFailureIsReported(t *testing.T) {
	p := &hostStoreFake{}
	s := vclusterService(&fakeHostPods{err: errors.New("host unreachable")}, "mimir", p)
	if _, err := s.QueryMetrics(context.Background(), testVCluster, "mimir", MetricQuery{PodName: "web-0", Namespace: "apps"}); err == nil {
		t.Fatal("want the lookup error")
	}
	if _, err := s.QueryWorkloadMetrics(context.Background(), testVCluster, "mimir", WorkloadMetricQuery{PodNames: []string{"web-0"}, Namespace: "apps"}); err == nil {
		t.Fatal("want the lookup error")
	}
}

func TestOrdinaryClusterAndInClusterProviderAreUntouched(t *testing.T) {
	r := &fakeHostPods{pods: map[string]*k8s.VClusterHostPod{"web-0": hostPod}}
	q := MetricQuery{PodName: "web-0", Namespace: "apps", MetricType: "cpu"}

	host := &hostStoreFake{}
	_, _ = vclusterService(r, "mimir", host).QueryMetrics(context.Background(), "prod", "mimir", q)
	if host.gotCluster != "prod" || host.gotQuery.PodName != "web-0" {
		t.Fatalf("ordinary cluster rewritten: %q %+v", host.gotCluster, host.gotQuery)
	}

	// metrics-server answers inside the vcluster, under the virtual names.
	inside := &storeFake{}
	_, _ = vclusterService(r, "metrics-server", inside).QueryMetrics(context.Background(), testVCluster, "metrics-server", q)
	if inside.gotCluster != testVCluster || inside.gotQuery.PodName != "web-0" || inside.gotQuery.Namespace != "apps" {
		t.Fatalf("in-cluster provider rewritten: %q %+v", inside.gotCluster, inside.gotQuery)
	}
	if r.calls != 0 {
		t.Fatalf("%d host lookups, want none", r.calls)
	}
}

func TestVClusterWorkloadChartTranslatesThereAndBack(t *testing.T) {
	r := &fakeHostPods{pods: map[string]*k8s.VClusterHostPod{
		"web-0": hostPod,
		"web-1": {Host: testHost, Namespace: "pink", Name: "web-1-x-apps-x-pink-vc"},
	}}
	p := &hostStoreFake{storeFake{answer: &WorkloadMetricResponse{Pods: map[string]*MetricResponse{
		"web-0-x-apps-x-pink-vc": {Unit: "m"},
		"web-1-x-apps-x-pink-vc": {Unit: "m"},
	}}}}
	s := vclusterService(r, "mimir", p)
	got, err := s.QueryWorkloadMetrics(context.Background(), testVCluster, "mimir",
		WorkloadMetricQuery{PodNames: []string{"web-0", "web-1", "pending"}, Namespace: "apps", MetricType: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if p.gotCluster != testHost || p.gotWorkload.Namespace != "pink" || len(p.gotWorkload.PodNames) != 2 {
		t.Fatalf("queried %q %+v", p.gotCluster, p.gotWorkload)
	}
	if len(got.Pods) != 2 || got.Pods["web-0"] == nil || got.Pods["web-1"] == nil {
		t.Fatalf("answer keyed %v, want the virtual names", got.Pods)
	}

	none := &hostStoreFake{}
	empty, err := vclusterService(&fakeHostPods{}, "mimir", none).QueryWorkloadMetrics(context.Background(), testVCluster, "mimir",
		WorkloadMetricQuery{PodNames: []string{"pending"}, Namespace: "apps"})
	if err != nil || len(empty.Pods) != 0 || none.queries != 0 {
		t.Fatalf("got %+v, %v, %d queries", empty, err, none.queries)
	}
}
