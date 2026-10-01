package finops

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// m5.xlarge: 4 vCPU, 16 GiB at $0.20/h on demand, $0.08/h spot. The rates
// split the price exactly: 4 × 0.035 + 16 × 0.00375 = 0.20.
var testPrices = map[string]*InstancePricing{
	"m5.xlarge": {InstanceType: "m5.xlarge", OnDemandPrice: 0.20, CPUCores: 4, MemoryGB: 16, CPUHourlyRate: 0.035, MemoryHourlyRate: 0.00375},
	"m5.spot":   {InstanceType: "m5.spot", OnDemandPrice: 0.20, SpotPrice: 0.08, CPUCores: 4, MemoryGB: 16, CPUHourlyRate: 0.035, MemoryHourlyRate: 0.00375},
}

func testPrice(instanceType, region string) (*InstancePricing, error) {
	if p, ok := testPrices[instanceType]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("no price for %s", instanceType)
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %.6f, want %.6f", name, got, want)
	}
}

func testNode(name, instanceType string, spot bool) v1.Node {
	labels := map[string]string{
		"node.kubernetes.io/instance-type": instanceType,
		"topology.kubernetes.io/region":    "eu-north-1",
		"eks.amazonaws.com/nodegroup":      "ng",
	}
	if spot {
		labels["karpenter.sh/capacity-type"] = "spot"
	}
	res := v1.ResourceList{v1.ResourceCPU: resource.MustParse("4"), v1.ResourceMemory: resource.MustParse("16Gi")}
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       v1.NodeSpec{ProviderID: "aws:///eu-north-1a/i-" + name},
		Status:     v1.NodeStatus{Capacity: res, Allocatable: res},
	}
}

func testPod(namespace, name, node, cpu, mem string) v1.Pod {
	requests := v1.ResourceList{}
	if cpu != "" {
		requests[v1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		requests[v1.ResourceMemory] = resource.MustParse(mem)
	}
	return v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{}},
		Spec: v1.PodSpec{
			NodeName:   node,
			Containers: []v1.Container{{Name: "app", Resources: v1.ResourceRequirements{Requests: requests}}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
}

func ownedByReplicaSet(p v1.Pod, deployment, hash string) v1.Pod {
	yes := true
	p.Labels["pod-template-hash"] = hash
	p.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: deployment + "-" + hash, Controller: &yes}}
	return p
}

func syncedHostPod(hostNS, vcName, virtNS, virtName, node, cpu, mem string, owner string) v1.Pod {
	p := testPod(hostNS, virtName+"-x-"+virtNS+"-x-"+vcName, node, cpu, mem)
	yes := true
	p.Labels[k8s.VClusterManagedByLabel] = vcName
	p.Annotations = map[string]string{
		k8s.VClusterObjectNameAnnotation:      virtName,
		k8s.VClusterObjectNamespaceAnnotation: virtNS,
	}
	if owner != "" {
		p.Labels["pod-template-hash"] = "abc12"
		p.Annotations[k8s.VClusterOwnerReferencesAnnotation] = `[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"` + owner + `-abc12","uid":"u","controller":true}]`
	}
	// On the host, the syncer makes the vcluster's Service the controller.
	p.OwnerReferences = []metav1.OwnerReference{{Kind: "Service", Name: vcName, Controller: &yes}}
	return p
}

func clusterInput() computeInput {
	return computeInput{
		cluster:  "prod",
		provider: ProviderAWSEKS,
		nodes: []v1.Node{
			testNode("node-a", "m5.xlarge", false),
			testNode("node-b", "m5.spot", true),
			testNode("node-c", "exotic.large", false),
		},
		pods: []v1.Pod{
			ownedByReplicaSet(testPod("shop", "web-1", "node-a", "1", "4Gi"), "web", "7d9f8"),
			ownedByReplicaSet(testPod("shop", "web-2", "node-a", "1", "4Gi"), "web", "7d9f8"),
			testPod("batch", "report", "node-b", "1", "4Gi"),
			testPod("batch", "unpriced", "node-c", "1", "4Gi"),
		},
		namespaceCount: 5,
		price:          testPrice,
		now:            time.Unix(0, 0),
	}
}

func findNamespace(t *testing.T, d *Dashboard, name string) NamespaceCost {
	t.Helper()
	for _, ns := range d.Namespaces {
		if ns.Namespace == name {
			return ns
		}
	}
	t.Fatalf("namespace %s not in dashboard", name)
	return NamespaceCost{}
}

func TestComputeDashboard_ClusterTotalsReconcile(t *testing.T) {
	d := computeDashboard(clusterInput())
	s := d.Summary

	// Nodes 0.20 + spot 0.08 + EKS fee 0.10; the unpriced node adds nothing.
	approx(t, "hourly", s.HourlyCost, 0.38)
	approx(t, "monthly", s.MonthlyCost, 0.38*hoursPerMonth)
	approx(t, "control plane", s.Breakdown.ControlPlaneCost, 0.10*hoursPerMonth)

	// web: 2 × (1 × 0.035 + 4 × 0.00375) = 0.10/h; report on spot: 0.05 × 0.4.
	shop, batch := findNamespace(t, d, "shop"), findNamespace(t, d, "batch")
	approx(t, "shop", shop.MonthlyCost, 0.10*hoursPerMonth)
	approx(t, "batch", batch.MonthlyCost, 0.02*hoursPerMonth)

	// Idle is each priced node's cost minus what pods on it claim.
	approx(t, "idle", s.IdleCost, (0.10+0.06)*hoursPerMonth)
	approx(t, "namespaces + idle + control plane", shop.MonthlyCost+batch.MonthlyCost+s.IdleCost+s.Breakdown.ControlPlaneCost, s.MonthlyCost)

	// Allocation counts every node, priced or not: 4 cores requested of 12.
	approx(t, "cpu allocation", s.CPUEfficiency, 4000.0/12000*100)
	if s.PricingInfo.NodesWithPricing != 2 || s.PricingInfo.NodesMissingPrice != 1 {
		t.Errorf("pricing counts = %d/%d, want 2/1", s.PricingInfo.NodesWithPricing, s.PricingInfo.NodesMissingPrice)
	}
	if s.SpotNodeCount != 1 || s.SpotAtOnDemandCount != 0 || s.Scope != ScopeCluster || s.UsageAvailable {
		t.Errorf("summary flags wrong: %+v", s)
	}
}

func TestComputeDashboard_SpotWithoutSpotRateIsFlagged(t *testing.T) {
	in := clusterInput()
	in.nodes = append(in.nodes, testNode("node-d", "m5.xlarge", true))
	d := computeDashboard(in)
	// node-b has a spot rate; node-d's type does not, so it bills on demand.
	if d.Summary.SpotNodeCount != 2 || d.Summary.SpotAtOnDemandCount != 1 {
		t.Fatalf("spot counts = %d/%d", d.Summary.SpotNodeCount, d.Summary.SpotAtOnDemandCount)
	}
	approx(t, "spot at on-demand", d.Summary.SpotAtOnDemandCost, 0.20*hoursPerMonth)
}

func TestComputeDashboard_UnpricedNodesStayVisible(t *testing.T) {
	d := computeDashboard(clusterInput())
	if len(d.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(d.Nodes))
	}
	last := d.Nodes[2]
	if last.NodeName != "node-c" || !last.PriceMissing || last.PodCount != 1 || last.CPURequested != 1000 {
		t.Errorf("unpriced node = %+v", last)
	}
}

func TestComputeDashboard_WorkloadsFoldReplicaSetsIntoDeployments(t *testing.T) {
	d := computeDashboard(clusterInput())
	shop := findNamespace(t, d, "shop")
	if len(shop.TopWorkloads) != 1 {
		t.Fatalf("got %d workloads, want 1", len(shop.TopWorkloads))
	}
	w := shop.TopWorkloads[0]
	if w.Kind != "Deployment" || w.Name != "web" || w.Replicas != 2 || len(w.Pods) != 2 {
		t.Errorf("workload = %s %s ×%d", w.Kind, w.Name, w.Replicas)
	}
	// A bare pod is its own workload, so namespace drill-down still reaches it.
	batch := findNamespace(t, d, "batch")
	if batch.TopWorkloads[0].Kind != "Pod" {
		t.Errorf("bare pod workload kind = %s", batch.TopWorkloads[0].Kind)
	}
}

func TestComputeDashboard_UsageDrivesEfficiency(t *testing.T) {
	in := clusterInput()
	in.usage = map[string]podUsage{
		"shop/web-1":     {cpu: 100, mem: 1 << 30},
		"shop/web-2":     {cpu: 100, mem: 1 << 30},
		"batch/report":   {cpu: 1000, mem: 4 << 30},
		"batch/unpriced": {cpu: 1000, mem: 4 << 30},
	}
	d := computeDashboard(in)
	w := findNamespace(t, d, "shop").TopWorkloads[0]

	approx(t, "cpu efficiency", w.CPUEfficiency, 10)
	approx(t, "memory efficiency", w.MemoryEfficiency, 25)

	// Request sizing belongs to the rightsizing engine, which has history; a
	// single usage snapshot must not produce rightsizing advice here.
	for _, r := range d.Recommendations {
		if r.Type == "rightsize" {
			t.Fatalf("snapshot rightsizing recommendation: %+v", r)
		}
	}
}

func TestComputeDashboard_PodsAboveRequestPayForUsage(t *testing.T) {
	in := clusterInput()
	in.pods = []v1.Pod{testPod("shop", "greedy", "node-a", "", "")}
	in.usage = map[string]podUsage{"shop/greedy": {cpu: 500, mem: 1 << 30}}
	d := computeDashboard(in)
	approx(t, "greedy", findNamespace(t, d, "shop").MonthlyCost, (0.5*0.035+0.00375)*hoursPerMonth)

	found := false
	for _, r := range d.Recommendations {
		found = found || (r.Type == "no-requests" && r.Resource == "greedy")
	}
	if !found {
		t.Errorf("no no-requests recommendation for greedy: %+v", d.Recommendations)
	}
}

func TestComputeDashboard_UnderutilizedNodeRecommendation(t *testing.T) {
	in := clusterInput()
	in.pods = []v1.Pod{testPod("shop", "tiny", "node-a", "100m", "256Mi")}
	d := computeDashboard(in)
	var rec *CostRecommendation
	for i := range d.Recommendations {
		if d.Recommendations[i].Type == "underutilized-node" && d.Recommendations[i].Resource == "node-a" {
			rec = &d.Recommendations[i]
		}
	}
	if rec == nil {
		t.Fatalf("no underutilized-node recommendation: %+v", d.Recommendations)
	}
	if rec.ProjectedSavings <= 0 || rec.ProjectedSavings >= 0.20*hoursPerMonth {
		t.Errorf("savings = %.2f", rec.ProjectedSavings)
	}
}

func TestComputeDashboard_HPAMatchesKindAndName(t *testing.T) {
	in := clusterInput()
	minR := int32(1)
	in.hpas = []autoscalingv2.HorizontalPodAutoscaler{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "web"},
				MinReplicas:    &minR,
				MaxReplicas:    10,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "shop"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "StatefulSet", Name: "web"},
				MaxReplicas:    3,
			},
		},
	}
	w := findNamespace(t, computeDashboard(in), "shop").TopWorkloads[0]
	if w.HPA == nil || w.HPA.MaxReplicas != 10 {
		t.Fatalf("hpa = %+v", w.HPA)
	}
	approx(t, "hpa max", w.HPA.MaxMonthlyCost, 0.05*10*hoursPerMonth)
}

func TestComputeDashboard_VClusterScope(t *testing.T) {
	in := clusterInput()
	cp := testPod("beige", "beige-vcluster-0", "node-a", "500m", "2Gi")
	in.pods = []v1.Pod{
		syncedHostPod("beige", "beige-vcluster", "applications", "api-abc12-x1", "node-a", "1", "4Gi", "api"),
		syncedHostPod("beige", "beige-vcluster", "applications", "api-abc12-x2", "node-b", "1", "4Gi", "api"),
		syncedHostPod("beige", "beige-vcluster", "kube-system", "coredns-0", "node-a", "100m", "128Mi", ""),
	}
	in.controlPlanePods = []v1.Pod{cp}
	in.usage = map[string]podUsage{
		"beige/api-abc12-x1-x-applications-x-beige-vcluster": {cpu: 200, mem: 1 << 30},
	}
	in.vcluster = &vclusterScope{host: "prod", namespace: "beige", name: "beige-vcluster", hostMonthlyCost: 1000}
	d := computeDashboard(in)
	s := d.Summary

	if s.Scope != ScopeVCluster || s.VCluster == nil {
		t.Fatalf("scope = %s", s.Scope)
	}
	// Namespaces are the virtual ones, and pods carry their virtual names.
	apps := findNamespace(t, d, "applications")
	findNamespace(t, d, "kube-system")
	if len(apps.TopWorkloads) != 1 || apps.TopWorkloads[0].Kind != "Deployment" || apps.TopWorkloads[0].Name != "api" {
		t.Fatalf("applications workloads = %+v", apps.TopWorkloads)
	}
	if apps.TopWorkloads[0].Pods[0].PodName != "api-abc12-x1" && apps.TopWorkloads[0].Pods[1].PodName != "api-abc12-x1" {
		t.Errorf("pods not shown by virtual name: %+v", apps.TopWorkloads[0].Pods)
	}
	// Usage keyed by host pod name still reaches the virtual pod.
	if !apps.TopWorkloads[0].Pods[0].HasUsage && !apps.TopWorkloads[0].Pods[1].HasUsage {
		t.Errorf("usage not mapped through the host pod name")
	}

	// Cost is the vcluster's pods plus its control plane, not the nodes.
	workloads := 0.05 + 0.02 + (0.1*0.035 + 0.125*0.00375)
	control := 0.5*0.035 + 2*0.00375
	approx(t, "control plane", s.Breakdown.ControlPlaneCost, control*hoursPerMonth)
	approx(t, "monthly", s.MonthlyCost, (workloads+control)*hoursPerMonth)
	approx(t, "share", s.VCluster.SharePercent, (workloads+control)*hoursPerMonth/1000*100)
	if s.IdleCost != 0 || s.CPUEfficiency != 0 {
		t.Errorf("idle/allocation should not apply to a vcluster: %+v", s)
	}
	if len(s.VCluster.ControlPlane) != 1 {
		t.Errorf("control plane workloads = %+v", s.VCluster.ControlPlane)
	}

	// Only the host nodes the vcluster runs on are listed.
	if len(d.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(d.Nodes))
	}
	for _, r := range d.Recommendations {
		if r.Type == "underutilized-node" {
			t.Errorf("vcluster got a node recommendation: %+v", r)
		}
	}
}

func TestComputeDashboard_HostSeesVClusterWorkloads(t *testing.T) {
	in := clusterInput()
	in.pods = []v1.Pod{
		testPod("beige", "beige-vcluster-0", "node-a", "500m", "2Gi"),
		syncedHostPod("beige", "beige-vcluster", "applications", "api-abc12-x1", "node-a", "1", "4Gi", "api"),
		syncedHostPod("beige", "beige-vcluster", "tools", "api-abc12-x1", "node-a", "1", "4Gi", "api"),
	}
	d := computeDashboard(in)
	beige := findNamespace(t, d, "beige")
	if beige.VCluster != "beige-vcluster" {
		t.Errorf("namespace vcluster = %q", beige.VCluster)
	}
	// Same Deployment name in two virtual namespaces stays two workloads.
	seen := map[string]bool{}
	for _, w := range beige.TopWorkloads {
		if w.Kind == "Deployment" && w.Name == "api" {
			seen[w.VClusterNamespace] = true
		}
	}
	if !seen["applications"] || !seen["tools"] {
		t.Errorf("synced workloads = %+v", beige.TopWorkloads)
	}
}

func TestSplitVClusterPods(t *testing.T) {
	pods := []v1.Pod{
		testPod("beige", "beige-vcluster-0", "n", "", ""),
		syncedHostPod("beige", "beige-vcluster", "apps", "a", "n", "", "", ""),
		syncedHostPod("beige", "other-vcluster", "apps", "b", "n", "", "", ""),
	}
	workloads, cp := splitVClusterPods(pods, "beige-vcluster")
	if len(workloads) != 1 || workloads[0].Annotations[k8s.VClusterObjectNameAnnotation] != "a" {
		t.Errorf("workloads = %d", len(workloads))
	}
	if len(cp) != 1 || cp[0].Name != "beige-vcluster-0" {
		t.Errorf("control plane = %d", len(cp))
	}
}

func TestEffectiveResources(t *testing.T) {
	always := v1.ContainerRestartPolicyAlways
	req := func(cpu, mem string) v1.ResourceRequirements {
		return v1.ResourceRequirements{Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse(cpu), v1.ResourceMemory: resource.MustParse(mem)}}
	}
	spec := v1.PodSpec{
		InitContainers: []v1.Container{
			{Name: "sidecar", RestartPolicy: &always, Resources: req("100m", "64Mi")},
			{Name: "migrate", Resources: req("2", "128Mi")},
		},
		Containers: []v1.Container{
			{Name: "a", Resources: req("250m", "256Mi")},
			{Name: "b", Resources: req("250m", "256Mi")},
		},
		Overhead: v1.ResourceList{v1.ResourceCPU: resource.MustParse("10m")},
	}
	cpu, _, mem, _ := effectiveResources(&spec)
	// CPU: the init step (2 + sidecar 0.1) outweighs the app phase (0.5 + 0.1).
	if cpu != 2110 {
		t.Errorf("cpu = %d, want 2110", cpu)
	}
	// Memory: the app phase (512Mi + 64Mi) outweighs the init step (128Mi + 64Mi).
	if mem != (512+64)<<20 {
		t.Errorf("mem = %d, want %d", mem, (512+64)<<20)
	}
}

func TestResolveOwner(t *testing.T) {
	yes := true
	ref := func(kind, name string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes}}
	}
	cases := []struct {
		refs           []metav1.OwnerReference
		labels         map[string]string
		kind, name     string
		wantKind, want string
	}{
		{refs: ref("ReplicaSet", "my-app-5d8f7c"), labels: map[string]string{"pod-template-hash": "5d8f7c"}, wantKind: "Deployment", want: "my-app"},
		{refs: ref("ReplicaSet", "standalone"), wantKind: "ReplicaSet", want: "standalone"},
		{refs: ref("Job", "nightly-report-29012345"), wantKind: "CronJob", want: "nightly-report"},
		{refs: ref("Job", "migrate-v2"), wantKind: "Job", want: "migrate-v2"},
		{refs: ref("StatefulSet", "db"), wantKind: "StatefulSet", want: "db"},
		{refs: ref("Node", "ip-10-0-0-1"), wantKind: "", want: ""},
		{refs: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "x"}}, wantKind: "", want: ""},
	}
	for _, c := range cases {
		kind, name := resolveOwner(c.refs, c.labels)
		if kind != c.wantKind || name != c.want {
			t.Errorf("resolveOwner(%v) = %s/%s, want %s/%s", c.refs[0].Name, kind, name, c.wantKind, c.want)
		}
	}
}

func TestDetectProvider(t *testing.T) {
	karpenter := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"karpenter.sh/capacity-type": "spot", "karpenter.k8s.aws/instance-family": "c5a"}},
		Spec:       v1.NodeSpec{ProviderID: "aws:///eu-north-1b/i-0abc"},
	}
	ec2 := v1.Node{Spec: v1.NodeSpec{ProviderID: "aws:///us-east-1a/i-0abc"}}
	aks := v1.Node{Spec: v1.NodeSpec{ProviderID: "azure:///subscriptions/x/resourceGroups/y"}}
	gke := v1.Node{Spec: v1.NodeSpec{ProviderID: "gce://proj/us-central1-a/n"}}
	kind := v1.Node{Spec: v1.NodeSpec{ProviderID: "kind://docker/kind/kind-control-plane"}}

	cases := map[string][]v1.Node{
		ProviderAWSEKS:   {karpenter},
		ProviderAWSEC2:   {ec2},
		ProviderAzureAKS: {aks},
		ProviderGCPGKE:   {gke},
		ProviderUnknown:  {kind},
	}
	for want, nodes := range cases {
		if got := detectProvider(nodes); got != want {
			t.Errorf("detectProvider = %s, want %s", got, want)
		}
	}
	if pricingSupported(ProviderGCPGKE, nil) || !pricingSupported(ProviderUnknown, []string{"us-east-1"}) || pricingSupported(ProviderUnknown, []string{""}) {
		t.Errorf("pricingSupported misjudged a provider")
	}
}
