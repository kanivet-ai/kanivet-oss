package finops

import (
	"context"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

type fakePricing struct{ ensured []string }

func (f *fakePricing) GetStatus(provider string) PricingStatus {
	return PricingStatus{Source: PricingSourceAWSAPI, IsAvailable: true}
}

func (f *fakePricing) GetInstancePricingCached(provider, instanceType, region string) (*InstancePricing, error) {
	return testPrice(instanceType, region)
}

func (f *fakePricing) EnsureRegions(provider string, regions []string, onLoaded func()) bool {
	f.ensured = append(f.ensured, provider)
	return true
}

func newTestService(objects ...runtime.Object) *Service {
	return &Service{
		k8s:     &k8s.MockClient{TypedClient: fake.NewSimpleClientset(objects...)},
		cache:   cache.New(time.Minute, time.Minute),
		pricing: &fakePricing{},
		now:     func() time.Time { return time.Unix(0, 0) },
	}
}

func TestService_VClusterDashboardIsPricedFromHost(t *testing.T) {
	nodeA := testNode("node-a", "m5.xlarge", false)
	cp := testPod("beige", "beige-vcluster-0", "node-a", "500m", "2Gi")
	synced := syncedHostPod("beige", "beige-vcluster", "applications", "api-abc12-x1", "node-a", "1", "4Gi", "api")
	unrelated := testPod("shop", "web", "node-a", "1", "4Gi")
	svc := newTestService(&nodeA, &cp, &synced, &unrelated)

	d, err := svc.GetDashboard(context.Background(), "vcluster:prod:beige:beige-vcluster")
	if err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}
	s := d.Summary
	if s.Scope != ScopeVCluster || s.VCluster.Host != "prod" || s.VCluster.Name != "beige-vcluster" {
		t.Fatalf("summary = %+v", s)
	}
	if s.PodCount != 1 || len(d.Namespaces) != 1 || d.Namespaces[0].Namespace != "applications" {
		t.Fatalf("vcluster pods = %d, namespaces = %+v", s.PodCount, d.Namespaces)
	}
	// Host total: one m5.xlarge plus the EKS fee.
	approx(t, "host monthly", s.VCluster.HostMonthlyCost, 0.30*hoursPerMonth)
	approx(t, "control plane", s.Breakdown.ControlPlaneCost, (0.5*0.035+2*0.00375)*hoursPerMonth)
}

func TestService_EndpointsShareOneComputation(t *testing.T) {
	nodeA := testNode("node-a", "m5.xlarge", false)
	web := ownedByReplicaSet(testPod("shop", "web-1", "node-a", "1", "4Gi"), "web", "7d9f8")
	svc := newTestService(&nodeA, &web)
	ctx := context.Background()

	workloads, err := svc.GetWorkloadCosts(ctx, "prod", "shop")
	if err != nil || len(workloads) != 1 || workloads[0].Name != "web" {
		t.Fatalf("workloads = %+v, %v", workloads, err)
	}
	pods, _ := svc.GetPodCosts(ctx, "prod", "")
	if len(pods) != 1 || pods[0].PodName != "web-1" {
		t.Fatalf("pods = %+v", pods)
	}
	d, _ := svc.GetDashboard(ctx, "prod")
	if got := findResourceCost(d, "Deployment", "shop", "web"); got == nil {
		t.Errorf("Deployment lookup failed")
	}
	if got := findResourceCost(d, "Pod", "shop", "web-1"); got == nil {
		t.Errorf("Pod lookup failed")
	}
	if got := findResourceCost(d, "Node", "", "node-a"); got == nil {
		t.Errorf("Node lookup failed")
	}
}

func TestService_MissingMetricsServerIsNotFatal(t *testing.T) {
	nodeA := testNode("node-a", "m5.xlarge", false)
	pod := testPod("shop", "web-1", "node-a", "1", "4Gi")
	svc := newTestService(&nodeA, &pod)

	// MockClient has no dynamic client, so usage is unavailable.
	d, err := svc.GetDashboard(context.Background(), "prod")
	if err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}
	if d.Summary.UsageAvailable {
		t.Errorf("usage should be unavailable: %+v", d.Summary)
	}
}
