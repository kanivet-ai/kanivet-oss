package rightsizing

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/kanivet/backend/internal/finops"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/metrics"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// synthWorkload lays synthetic container inputs out as one live workload's
// history, keyed the way fetchHistory keys it, for analyzeWorkload.
func synthWorkload(kind string, ins ...containerInput) (*liveWorkload, *history, grid) {
	n := len(ins[0].mem)
	g := grid{start: t0, end: t0.Add(time.Duration(n-1) * step5m), step: step5m, n: n}
	w := &liveWorkload{
		ref:           finops.WorkloadRef{Namespace: "apps", Kind: kind, Name: "api"},
		hostNamespace: "apps",
		keys:          map[string]map[seriesKey]struct{}{},
		nodes:         []string{"node-a"},
		replicas:      1,
		isJob:         kind == "Job" || kind == "CronJob",
	}
	h := &history{cpu: map[seriesKey]*pooled{}, mem: map[seriesKey][]float64{}, oom: map[seriesKey][]time.Time{}}
	for _, in := range ins {
		k := seriesKey{"apps", "", "api", in.container}
		w.keys[in.container] = map[seriesKey]struct{}{k: {}}
		w.containers = append(w.containers, liveContainer{name: in.container, res: in.current, jvm: in.jvm})
		h.cpu[k], h.mem[k], h.oom[k] = in.cpu, in.mem, in.oomTimes
	}
	return w, h, g
}

// replicaStore serves one Deployment of n interchangeable pods, each at 0.2
// cores, which independently throttle (or burst to their limit) in a share
// of steps. It aggregates across pods the way PromQL would for the query
// shapes fetchHistory sends, so a busiest-replica query shows the busiest.
type replicaStore struct {
	n      int
	kind   string  // throttlePeriods or throttleSeconds
	chance float64 // per pod and step
}

func (replicaStore) HistorySource(context.Context, string) (*metrics.ProviderInfo, bool) {
	return &metrics.ProviderInfo{Type: "prometheus", Found: true}, false
}

func (s replicaStore) QueryInstant(_ context.Context, _, query string, at time.Time) ([]metrics.HistorySeries, error) {
	v := float32(1)
	switch {
	case strings.Contains(query, "kube_pod_start_time"):
		return nil, nil
	case strings.Contains(query, "cfs_periods") || strings.Contains(query, "cfs_throttled_periods"):
		if s.kind != throttlePeriods {
			v = 0
		}
	}
	return []metrics.HistorySeries{{Times: []int64{at.Unix()}, Values: []float32{v}}}, nil
}

func (s replicaStore) QueryRange(_ context.Context, _, query string, start, end time.Time, step time.Duration) ([]metrics.HistorySeries, error) {
	// event says whether pod k throttles or bursts at t, the same for every
	// query that asks.
	event := func(k int, t int64) bool {
		x := uint64(t)*0x9e3779b97f4a7c15 + uint64(k+1)*0xbf58476d1ce4e5b9
		x ^= x >> 31
		x *= 0x94d049bb133111eb
		x ^= x >> 29
		return float64(x%1_000_000)/1e6 < s.chance
	}
	var perPod func(k int, t int64) float64
	switch {
	case strings.Contains(query, "kube_pod_container"):
		return nil, nil
	case strings.Contains(query, "memory"):
		perPod = func(int, int64) float64 { return 300 << 20 }
	case strings.Contains(query, "throttled"):
		perPod = func(k int, t int64) float64 {
			if event(k, t) {
				return 0.2
			}
			return 0
		}
	case strings.Contains(query, "[2m]"):
		perPod = func(k int, t int64) float64 {
			if event(k, t) {
				return 0.95
			}
			return 0.3
		}
	default:
		perPod = func(int, int64) float64 { return 0.2 }
	}
	key := map[string]string{"namespace": "apps", "wk": "api", "container": "main"}
	pod := func(k int) map[string]string {
		l := maps.Clone(key)
		l["pod"] = replicaName(k)
		return l
	}
	var out []metrics.HistorySeries
	if !strings.HasPrefix(query, "max by (namespace, vns, wk, container)") && !strings.HasPrefix(query, "avg by (namespace, vns, wk, container)") {
		for k := range s.n {
			ser := metrics.HistorySeries{Labels: pod(k)}
			for at := start; !at.After(end); at = at.Add(step) {
				ser.Times, ser.Values = append(ser.Times, at.Unix()), append(ser.Values, float32(perPod(k, at.Unix())))
			}
			out = append(out, ser)
		}
		return out, nil
	}
	ser := metrics.HistorySeries{Labels: key}
	for at := start; !at.After(end); at = at.Add(step) {
		var v float64
		for k := range s.n {
			x := perPod(k, at.Unix())
			switch {
			case strings.HasPrefix(query, "max by"):
				v = max(v, x)
			case x > throttleHigh: // avg by (...) (x > bool 0.05)
				v += 1 / float64(s.n)
			}
		}
		ser.Times, ser.Values = append(ser.Times, at.Unix()), append(ser.Values, float32(v))
	}
	return []metrics.HistorySeries{ser}, nil
}

// replicaName is pod k of Deployment api, with a ReplicaSet-style suffix.
func replicaName(k int) string {
	const safe = "bcdfghjklmnpqrstvwxz2456789"
	suffix := []byte("bbbbb")
	for i := 4; i >= 0 && k > 0; i, k = i-1, k/len(safe) {
		suffix[i] = safe[k%len(safe)]
	}
	return "api-bcdfgh-" + string(suffix)
}

type replicaPods struct {
	evidenceFixtures
	n int
}

func (p replicaPods) List(context.Context, string) ([]*v1.Pod, error) {
	controller := true
	var pods []*v1.Pod
	for k := range p.n {
		pods = append(pods, &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "apps", Name: replicaName(k),
				Labels:          map[string]string{"pod-template-hash": "bcdfgh"},
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-bcdfgh", Controller: &controller}},
			},
			Spec: v1.PodSpec{Containers: []v1.Container{{Name: "main", Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")},
				Limits:   v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("1Gi")},
			}}}},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
	}
	return pods, nil
}

// Throttling and bursts against the limit are judged on replica-time, as CPU
// is: pods that each throttle in 2% of steps, or burst to their limit in
// 0.2%, are not censored, however many of them run. The busiest of 30 would
// show one in nearly every step.
func TestThrottleEvidenceIndependentOfReplicaCount(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		kind   string
		chance float64
		want   bool
	}{
		{throttlePeriods, 0.02, false},
		{throttleSeconds, 0.002, false},
		{throttlePeriods, 0.2, true},
		{throttleSeconds, 0.05, true},
	} {
		for _, n := range []int{1, 30} {
			st := replicaStore{n: n, kind: tc.kind, chance: tc.chance}
			s := NewService(&k8s.MockClient{}, st, replicaPods{}, replicaPods{n: n}, nil)
			s.now = func() time.Time { return now }
			rep := s.compute(context.Background(), t.Name(), ProfileBalanced, defaultWindow, nil, func(Progress) {})
			if rep.Status != StatusReady || len(rep.Workloads) != 1 {
				t.Fatalf("report status=%s error=%s workloads=%d", rep.Status, rep.Error, len(rep.Workloads))
			}
			c := rep.Workloads[0].Containers[0]
			if c.CPU.Censored != tc.want {
				t.Errorf("%s, %.1f%% of steps per pod, %d replicas: censored %v, want %v (findings %+v)", tc.kind, tc.chance*100, n, c.CPU.Censored, tc.want, c.Findings)
			}
		}
	}
}

// An HPA on a Resource metric scales on the pod's summed utilisation, so a
// workload gets one target for it, worked out over every container's
// requests, not one per container.
func TestHPAPairedOverThePod(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	app := synth(14, 4, diurnal(rng, 0.3, 0.3), flatMem(rng, 200*mib))
	app.current = Resources{CPURequest: 1, MemRequest: 300 * mib, MemLimit: 300 * mib}
	side := synth(14, 4, diurnal(rng, 0.025, 0.3), flatMem(rng, 50*mib))
	side.container = "sidecar"
	side.current = Resources{CPURequest: 0.5, MemRequest: 128 * mib, MemLimit: 128 * mib}
	w, h, g := synthWorkload("Deployment", app, side)
	hpas := map[string]*hpaTarget{"apps/Deployment/api": {resource: "cpu", utilization: 30, name: "api"}}
	wr := (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, hpas, nil, nil, nil)

	var old, paired float64
	for _, c := range wr.Containers {
		old += c.CPU.Request
		paired += c.CPU.Recommended
	}
	want := int32(math.Round(30 * old / paired))
	if wr.HPA == nil || wr.HPA.SuggestedTarget != want || wr.HPA.PairedRequest != paired || !wr.HPA.Pod || want > maxHPATarget {
		t.Fatalf("workload hpa %+v, want target %d for %v cores (from %v)", wr.HPA, want, paired, old)
	}
	for _, c := range wr.Containers {
		if c.HPA == nil || c.HPA.SuggestedTarget != want || c.HPA.PairedRequest != c.CPU.Recommended {
			t.Errorf("%s: hpa %+v, rec %v: every container must carry the pod's %d%% target", c.Container, c.HPA, c.CPU.Recommended, want)
		}
	}

	// A ContainerResource metric scales on the container it names alone.
	hpas["apps/Deployment/api"].container = "app"
	wr = (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, hpas, nil, nil, nil)
	app0, side0 := wr.Containers[0], wr.Containers[1]
	if side0.HPA != nil || side0.CPU.Verdict == VerdictHPACoupled {
		t.Fatalf("sidecar coupled to an HPA that doesn't count it: %+v", side0.HPA)
	}
	if app0.HPA == nil || app0.HPA.SuggestedTarget != int32(math.Round(30*1/app0.CPU.Recommended)) || app0.HPA.Pod || wr.HPA == nil || *wr.HPA != *app0.HPA {
		t.Fatalf("app hpa %+v, workload %+v", app0.HPA, wr.HPA)
	}
}

func TestHPATargetsKeepTheContainerTheyScaleOn(t *testing.T) {
	util := int32(60)
	got := hpaTargets([]autoscalingv2.HorizontalPodAutoscaler{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "api"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "api"},
			Metrics: []autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ContainerResourceMetricSourceType,
				ContainerResource: &autoscalingv2.ContainerResourceMetricSource{Name: v1.ResourceCPU, Container: "app",
					Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: &util}},
			}},
		},
	}})
	w := &liveWorkload{ref: finops.WorkloadRef{Namespace: "apps", Kind: "Deployment", Name: "api"},
		containers: []liveContainer{{name: "app", res: Resources{CPURequest: 1}}, {name: "istio-proxy", res: Resources{CPURequest: 0.1}}}}
	if h := hpaFor(got, w, "app"); h == nil || h.container != "app" || h.pod {
		t.Fatalf("app: %+v", h)
	}
	if h := hpaFor(got, w, "istio-proxy"); h != nil {
		t.Fatalf("istio-proxy: %+v", h)
	}
}

func vpaObject(namespace, name, target string, spec, status map[string]any) *unstructured.Unstructured {
	spec["targetRef"] = map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": target}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling.k8s.io/v1", "kind": "VerticalPodAutoscaler",
		"metadata": map[string]any{"namespace": namespace, "name": name},
		"spec":     spec, "status": status,
	}}
}

// A VerticalPodAutoscaler that isn't Off sets requests as pods are created:
// which containers and resources it controls, and what it recommends.
func TestVPATargets(t *testing.T) {
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{vpaResource: "VerticalPodAutoscalerList"},
		vpaObject("apps", "api-vpa", "api", map[string]any{
			"resourcePolicy": map[string]any{"containerPolicies": []any{
				map[string]any{"containerName": "istio-proxy", "mode": "Off"},
				map[string]any{"containerName": "*", "controlledResources": []any{"memory"}},
			}},
		}, map[string]any{"recommendation": map[string]any{"containerRecommendations": []any{
			map[string]any{"containerName": "app", "target": map[string]any{"cpu": "250m", "memory": "512Mi"}},
		}}}),
		vpaObject("apps", "web-vpa", "web", map[string]any{"updatePolicy": map[string]any{"updateMode": "Off"}}, nil),
	)
	vpas := listVPAs(context.Background(), &k8s.MockClient{DynamicClient: dc}, "c")
	api := &liveWorkload{ref: finops.WorkloadRef{Namespace: "apps", Kind: "Deployment", Name: "api"}}
	v := vpaFor(vpas, api, "app")
	if v == nil || v.name != "api-vpa" || v.mode != "Auto" || v.sets("cpu") || !v.sets("memory") || v.target.MemRequest != 512*mib || v.target.CPURequest != 0.25 {
		t.Fatalf("app: %+v", v)
	}
	if v := vpaFor(vpas, api, "istio-proxy"); v != nil {
		t.Fatalf("istio-proxy, which the VPA leaves alone: %+v", v)
	}
	web := &liveWorkload{ref: finops.WorkloadRef{Namespace: "apps", Kind: "Deployment", Name: "web"}}
	if v := vpaFor(vpas, web, "app"); v != nil {
		t.Fatalf("a VPA that only recommends doesn't set requests: %+v", v)
	}
	if listVPAs(context.Background(), &k8s.MockClient{}, "c") != nil {
		t.Fatal("no dynamic client, no VPAs")
	}
}

// Requests a VPA sets are its own: the report says so, and a template change
// it would undo saves nothing.
func TestVPAManagedSavingsLeftOut(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	in := synth(14, 3, diurnal(rng, 0.15, 0.5), flatMem(rng, 300*mib))
	in.current = Resources{CPURequest: 1, MemRequest: 2 * gib, MemLimit: 2 * gib}
	w, h, g := synthWorkload("Deployment", in)
	vpas := map[string]*vpaTarget{"apps/Deployment/api": {name: "api-vpa", mode: "Recreate", policies: map[string][]string{}, recommended: map[string]Resources{}}}
	wr := (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, nil, vpas, testRates, nil)
	c := wr.Containers[0]
	if !hasFinding(c, "vpa-managed") || c.MonthlySavings != 0 || wr.MonthlySavings != 0 || wr.SavingsHigh != 0 {
		t.Fatalf("savings %v (workload %v, up to %v), findings %+v", c.MonthlySavings, wr.MonthlySavings, wr.SavingsHigh, c.Findings)
	}
	if c.Verdict != VerdictOver || c.CPU.Recommended >= 1 {
		t.Fatalf("the analysis itself still shows: verdict %s, cpu %v", c.Verdict, c.CPU.Recommended)
	}
	// The UI's bulk patches leave what the VPA sets alone: it says which.
	if b, _ := json.Marshal(c.VPA); string(b) != `{"name":"api-vpa","mode":"Recreate","resources":["cpu","memory"]}` {
		t.Fatalf("vpa %s", b)
	}
	// Memory only: CPU savings are still Kanivet's to make.
	vpas["apps/Deployment/api"].policies["*"] = []string{"memory"}
	wr = (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, nil, vpas, testRates, nil)
	if c := wr.Containers[0]; math.Abs(c.MonthlySavings-(1-c.CPU.Recommended)*c.CPUMonthly) > 1e-9 {
		t.Fatalf("savings %v, want the CPU change's %v", c.MonthlySavings, (1-c.CPU.Recommended)*c.CPUMonthly)
	}
	if c := wr.Containers[0]; c.VPA == nil || len(c.VPA.Resources) != 1 || c.VPA.Resources[0] != "memory" {
		t.Fatalf("vpa %+v", c.VPA)
	}
	// Without a VPA there is none to report.
	wr = (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, nil, nil, testRates, nil)
	if c := wr.Containers[0]; c.VPA != nil || hasFinding(c, "vpa-managed") {
		t.Fatalf("vpa %+v", c.VPA)
	}
}

var testRates = map[string]finops.Rates{"node-a": {CPU: 0.04, Memory: 0.005, Priced: true}}

// A CronJob that runs an hour every two days reserves its request for that
// hour only: it is priced for the time it runs, not the whole month.
func TestJobPricedForTheTimeItRuns(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 11))
	in := synth(14, 1, diurnal(rng, 0.5, 0.1), flatMem(rng, 300*mib))
	drop(&in, func(i int) bool { return (i/288)%2 != 0 || i%288 >= 12 })
	in.current = Resources{CPURequest: 2, MemRequest: 2 * gib, MemLimit: 2 * gib}
	w, h, g := synthWorkload("CronJob", in)
	wr := (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, nil, nil, testRates, nil)
	cr := wr.Containers[0]
	duty := 7 * 12.0 / (14 * 288)
	if math.Abs(cr.Data.DutyCycle-duty) > 1e-9 {
		t.Fatalf("duty cycle %v, want %v", cr.Data.DutyCycle, duty)
	}
	if want := 0.04 * hoursPerMonth * duty; math.Abs(cr.CPUMonthly-want) > 1e-9 {
		t.Fatalf("a core costs %v a month, want %v for the hours it runs", cr.CPUMonthly, want)
	}
	if wr.MonthlySavings <= 0 || wr.MonthlySavings > 2*0.04*hoursPerMonth*duty+2*0.005*hoursPerMonth*duty {
		t.Fatalf("savings %v exceed what its request costs while it runs", wr.MonthlySavings)
	}

	// A Deployment's gaps are lost scrapes, not idle time: priced in full.
	w.ref.Kind, w.isJob = "Deployment", false
	if cr := (&Service{}).analyzeWorkload("c", w, h, g, probe{}, ProfileBalanced, nil, nil, testRates, nil).Containers[0]; cr.Data.DutyCycle != 0 || math.Abs(cr.CPUMonthly-0.04*hoursPerMonth) > 1e-9 {
		t.Fatalf("deployment: duty %v, a core costs %v", cr.Data.DutyCycle, cr.CPUMonthly)
	}
}
