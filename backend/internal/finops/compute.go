package finops

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/k8s"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hoursPerMonth = 24 * 30
	bytesPerGiB   = 1024 * 1024 * 1024

	// rightsizeHeadroom is how far above observed usage a request should sit
	// before the excess counts as savings.
	rightsizeHeadroom = 1.3
	// minRecommendationSavings drops recommendations worth less than this per month.
	minRecommendationSavings = 5.0
	maxRecommendations       = 25
)

type podUsage struct {
	cpu int64 // millicores
	mem int64 // bytes
}

// priceFunc resolves an instance type's price; an error means no price yet.
type priceFunc func(instanceType, region string) (*InstancePricing, error)

type vclusterScope struct {
	host, namespace, name string
	// hostMonthlyCost is the whole host's cost, for the share figure.
	hostMonthlyCost float64
}

// computeInput is everything one dashboard is built from. Pods are raw API
// objects: in vcluster scope they are the host pods the vcluster synced, and
// controlPlanePods are the vcluster's own pods in its host namespace.
type computeInput struct {
	cluster          string
	provider         string
	nodes            []v1.Node
	pods             []v1.Pod
	controlPlanePods []v1.Pod
	namespaceCount   int
	price            priceFunc
	// usage is keyed by "<namespace>/<name>" of the pods as listed.
	usage         map[string]podUsage
	hpas          []autoscalingv2.HorizontalPodAutoscaler
	vcluster      *vclusterScope
	pricingStatus PricingStatus
	now           time.Time
}

type nodeRate struct {
	hourly         float64
	cpuRate        float64 // per core-hour
	memRate        float64 // per GiB-hour
	priced         bool
	spotAtOnDemand bool
}

// podView is a pod as FinOps sees it: effective requests, resolved owner and,
// for pods synced from a vcluster, its virtual identity.
type podView struct {
	name, namespace, nodeName string
	ownerKind, ownerName      string
	cpuReq, cpuLim            int64
	memReq, memLim            int64
	phase                     string
	created                   time.Time
	usage                     *podUsage
	// vcluster and vclusterNamespace are set on host pods synced from a
	// vcluster (cluster scope only).
	vcluster, vclusterNamespace string
}

func computeDashboard(in computeInput) *Dashboard {
	rates := priceNodes(in)
	scopeVC := in.vcluster != nil

	views := make([]podView, 0, len(in.pods))
	for i := range in.pods {
		views = append(views, viewPod(&in.pods[i], scopeVC, in.usage))
	}
	cpViews := make([]podView, 0, len(in.controlPlanePods))
	for i := range in.controlPlanePods {
		cpViews = append(cpViews, viewPod(&in.controlPlanePods[i], false, in.usage))
	}

	podCosts := make([]PodCost, len(views))
	for i := range views {
		podCosts[i] = costPod(&views[i], rates[views[i].nodeName])
	}
	cpCosts := make([]PodCost, len(cpViews))
	for i := range cpViews {
		cpCosts[i] = costPod(&cpViews[i], rates[cpViews[i].nodeName])
	}

	nodes := buildNodeCosts(in, rates, podCosts, cpCosts)
	hpas := indexHPAs(in.hpas)
	namespaces := buildNamespaces(views, podCosts, rates, hpas)
	summary := buildSummary(in, rates, nodes, namespaces, podCosts, cpCosts)
	if scopeVC {
		summary.VCluster.ControlPlane = groupWorkloads(cpViews, cpCosts, rates, nil)
	}

	return &Dashboard{
		Summary:         summary,
		Nodes:           nodes,
		Namespaces:      namespaces,
		Recommendations: buildRecommendations(in, nodes, namespaces),
	}
}

// priceNodes resolves each node's hourly price and per-resource rates, looking
// up each instance type once.
func priceNodes(in computeInput) map[string]nodeRate {
	type key struct{ it, region string }
	memo := map[key]*InstancePricing{}
	rates := make(map[string]nodeRate, len(in.nodes))
	fallbackRegion := dominantRegion(in.nodes)
	for i := range in.nodes {
		n := &in.nodes[i]
		it, region := instanceType(n.Labels), nodeRegion(n.Labels)
		if region == "" {
			region = fallbackRegion
		}
		if it == "" || in.price == nil {
			rates[n.Name] = nodeRate{}
			continue
		}
		k := key{it, region}
		p, seen := memo[k]
		if !seen {
			p, _ = in.price(it, region)
			memo[k] = p
		}
		if p == nil || p.OnDemandPrice <= 0 {
			rates[n.Name] = nodeRate{}
			continue
		}
		// Spot rates are not published in the price lists we read, so spot
		// nodes are priced at on-demand list price: an upper bound.
		spot := isSpot(n.Labels)
		hourly := p.OnDemandPrice
		if spot && p.SpotPrice > 0 {
			hourly = p.SpotPrice
		}
		scale := hourly / p.OnDemandPrice
		rates[n.Name] = nodeRate{
			hourly:         hourly,
			cpuRate:        p.CPUHourlyRate * scale,
			memRate:        p.MemoryHourlyRate * scale,
			priced:         true,
			spotAtOnDemand: spot && p.SpotPrice <= 0,
		}
	}
	return rates
}

func viewPod(pod *v1.Pod, scopeVC bool, usage map[string]podUsage) podView {
	cpuReq, cpuLim, memReq, memLim := effectiveResources(&pod.Spec)
	v := podView{
		name:      pod.Name,
		namespace: pod.Namespace,
		nodeName:  pod.Spec.NodeName,
		cpuReq:    cpuReq,
		cpuLim:    cpuLim,
		memReq:    memReq,
		memLim:    memLim,
		phase:     string(pod.Status.Phase),
		created:   pod.CreationTimestamp.Time,
	}
	if u, ok := usage[pod.Namespace+"/"+pod.Name]; ok {
		v.usage = &u
	}

	refs := pod.OwnerReferences
	vcName := pod.Labels[k8s.VClusterManagedByLabel]
	virtualNS := pod.Annotations[k8s.VClusterObjectNamespaceAnnotation]
	if vcName != "" && virtualNS != "" {
		// Synced pods are owned by the vcluster's Service on the host; the
		// real owners travel in an annotation.
		refs = nil
		if raw := pod.Annotations[k8s.VClusterOwnerReferencesAnnotation]; raw != "" {
			_ = json.Unmarshal([]byte(raw), &refs)
		}
		if scopeVC {
			v.namespace = virtualNS
			if name := pod.Annotations[k8s.VClusterObjectNameAnnotation]; name != "" {
				v.name = name
			}
		} else {
			v.vcluster = vcName
			v.vclusterNamespace = virtualNS
		}
	}
	v.ownerKind, v.ownerName = resolveOwner(refs, pod.Labels)
	if v.ownerKind == "" {
		v.ownerKind, v.ownerName = "Pod", v.name
	}
	return v
}

// effectiveResources follows the scheduler: app containers and restartable
// (sidecar) init containers run together, a regular init container runs
// alone alongside the sidecars started before it, and pod overhead adds on top.
func effectiveResources(spec *v1.PodSpec) (cpuReq, cpuLim, memReq, memLim int64) {
	var sideCPU, sideMem, sideCPULim, sideMemLim int64
	var initCPU, initMem, initCPULim, initMemLim int64
	for _, c := range spec.InitContainers {
		r := c.Resources
		if c.RestartPolicy != nil && *c.RestartPolicy == v1.ContainerRestartPolicyAlways {
			sideCPU += r.Requests.Cpu().MilliValue()
			sideMem += r.Requests.Memory().Value()
			sideCPULim += r.Limits.Cpu().MilliValue()
			sideMemLim += r.Limits.Memory().Value()
			continue
		}
		initCPU = max(initCPU, r.Requests.Cpu().MilliValue()+sideCPU)
		initMem = max(initMem, r.Requests.Memory().Value()+sideMem)
		initCPULim = max(initCPULim, r.Limits.Cpu().MilliValue()+sideCPULim)
		initMemLim = max(initMemLim, r.Limits.Memory().Value()+sideMemLim)
	}
	for _, c := range spec.Containers {
		r := c.Resources
		cpuReq += r.Requests.Cpu().MilliValue()
		memReq += r.Requests.Memory().Value()
		cpuLim += r.Limits.Cpu().MilliValue()
		memLim += r.Limits.Memory().Value()
	}
	cpuReq = max(cpuReq+sideCPU, initCPU)
	memReq = max(memReq+sideMem, initMem)
	cpuLim = max(cpuLim+sideCPULim, initCPULim)
	memLim = max(memLim+sideMemLim, initMemLim)
	if spec.Overhead != nil {
		cpuReq += spec.Overhead.Cpu().MilliValue()
		memReq += spec.Overhead.Memory().Value()
	}
	return
}

// resolveOwner names the top-level workload behind a pod's controller
// reference: ReplicaSets fold into their Deployment and scheduled Jobs into
// their CronJob.
func resolveOwner(refs []metav1.OwnerReference, labels map[string]string) (string, string) {
	for _, ref := range refs {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		switch ref.Kind {
		case "ReplicaSet":
			if hash := labels["pod-template-hash"]; hash != "" && strings.HasSuffix(ref.Name, "-"+hash) {
				return "Deployment", strings.TrimSuffix(ref.Name, "-"+hash)
			}
		case "Job":
			// CronJob-created Jobs are named <cronjob>-<scheduled unix minutes>.
			if i := strings.LastIndex(ref.Name, "-"); i > 0 && isDigits(ref.Name[i+1:]) && len(ref.Name)-i-1 >= 8 {
				return "CronJob", ref.Name[:i]
			}
		case "Node":
			// Static pods: mirror pods are "owned" by their node.
			return "", ""
		}
		return ref.Kind, ref.Name
	}
	return "", ""
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// costPod prices a pod on its node's rates. The billed amount of each
// resource is the request, or the usage when a pod runs above its request.
func costPod(v *podView, rate nodeRate) PodCost {
	pc := PodCost{
		PodName:           v.name,
		Namespace:         v.namespace,
		NodeName:          v.nodeName,
		OwnerKind:         v.ownerKind,
		OwnerName:         v.ownerName,
		CPURequest:        v.cpuReq,
		CPULimit:          v.cpuLim,
		MemoryRequest:     v.memReq,
		MemoryLimit:       v.memLim,
		Status:            v.phase,
		CreatedAt:         v.created,
		VClusterNamespace: v.vclusterNamespace,
	}
	cpu, mem := v.cpuReq, v.memReq
	if v.usage != nil {
		pc.HasUsage = true
		pc.CPUUsed, pc.MemoryUsed = v.usage.cpu, v.usage.mem
		cpu, mem = max(cpu, v.usage.cpu), max(mem, v.usage.mem)
		pc.CPUEfficiency, pc.MemoryEfficiency, pc.OverallEfficiency = usageEfficiency(v.usage.cpu, v.cpuReq, v.usage.mem, v.memReq)
	}
	pc.HourlyCost = resourceCost(cpu, mem, rate)
	pc.DailyCost = pc.HourlyCost * 24
	pc.MonthlyCost = pc.HourlyCost * hoursPerMonth
	return pc
}

func resourceCost(milliCPU, memBytes int64, rate nodeRate) float64 {
	return float64(milliCPU)/1000*rate.cpuRate + float64(memBytes)/bytesPerGiB*rate.memRate
}

// usageEfficiency returns usage/request for CPU and memory in percent, and an
// overall figure averaging whichever of the two has a request.
func usageEfficiency(cpuUsed, cpuReq, memUsed, memReq int64) (cpu, mem, overall float64) {
	n := 0
	if cpuReq > 0 {
		cpu = float64(cpuUsed) / float64(cpuReq) * 100
		overall += cpu
		n++
	}
	if memReq > 0 {
		mem = float64(memUsed) / float64(memReq) * 100
		overall += mem
		n++
	}
	if n > 0 {
		overall /= float64(n)
	}
	return
}

// rightsizingSavings is the monthly cost of requests sitting above observed
// usage plus headroom.
func rightsizingSavings(v *podView, rate nodeRate) float64 {
	if v.usage == nil || !rate.priced {
		return 0
	}
	excessCPU := v.cpuReq - int64(float64(v.usage.cpu)*rightsizeHeadroom)
	excessMem := v.memReq - int64(float64(v.usage.mem)*rightsizeHeadroom)
	return resourceCost(max(excessCPU, 0), max(excessMem, 0), rate) * hoursPerMonth
}

func buildNodeCosts(in computeInput, rates map[string]nodeRate, pods, cpPods []PodCost) []NodeCost {
	type agg struct {
		cpu, mem  int64
		count     int
		allocated float64
	}
	perNode := map[string]*agg{}
	add := func(pc []PodCost) {
		for i := range pc {
			if pc[i].NodeName == "" {
				continue
			}
			a := perNode[pc[i].NodeName]
			if a == nil {
				a = &agg{}
				perNode[pc[i].NodeName] = a
			}
			a.cpu += pc[i].CPURequest
			a.mem += pc[i].MemoryRequest
			a.count++
			a.allocated += pc[i].MonthlyCost
		}
	}
	add(pods)
	add(cpPods)

	out := make([]NodeCost, 0, len(in.nodes))
	for i := range in.nodes {
		n := &in.nodes[i]
		a := perNode[n.Name]
		// A vcluster only sees the host nodes its pods run on.
		if in.vcluster != nil && a == nil {
			continue
		}
		if a == nil {
			a = &agg{}
		}
		r := rates[n.Name]
		out = append(out, NodeCost{
			NodeName:             n.Name,
			InstanceType:         instanceType(n.Labels),
			Region:               nodeRegion(n.Labels),
			Provider:             in.provider,
			HourlyCost:           r.hourly,
			DailyCost:            r.hourly * 24,
			MonthlyCost:          r.hourly * hoursPerMonth,
			PriceMissing:         !r.priced,
			CPUCapacity:          n.Status.Capacity.Cpu().MilliValue(),
			MemoryCapacity:       n.Status.Capacity.Memory().Value(),
			CPUAllocatable:       n.Status.Allocatable.Cpu().MilliValue(),
			MemoryAllocatable:    n.Status.Allocatable.Memory().Value(),
			CPURequested:         a.cpu,
			MemoryRequested:      a.mem,
			PodCount:             a.count,
			IsSpot:               isSpot(n.Labels),
			SpotAtOnDemand:       r.spotAtOnDemand,
			AllocatedMonthlyCost: min(a.allocated, r.hourly*hoursPerMonth),
		})
	}
	// A vcluster cares about its share of each node, not the node's price.
	weight := func(n *NodeCost) float64 {
		if in.vcluster != nil {
			return n.AllocatedMonthlyCost
		}
		return n.MonthlyCost
	}
	sort.SliceStable(out, func(i, j int) bool {
		if wi, wj := weight(&out[i]), weight(&out[j]); wi != wj {
			return wi > wj
		}
		return out[i].NodeName < out[j].NodeName
	})
	return out
}

type hpaKey struct{ namespace, kind, name string }

func indexHPAs(hpas []autoscalingv2.HorizontalPodAutoscaler) map[hpaKey]*autoscalingv2.HorizontalPodAutoscaler {
	m := make(map[hpaKey]*autoscalingv2.HorizontalPodAutoscaler, len(hpas))
	for i := range hpas {
		h := &hpas[i]
		m[hpaKey{h.Namespace, h.Spec.ScaleTargetRef.Kind, h.Spec.ScaleTargetRef.Name}] = h
	}
	return m
}

func groupWorkloads(views []podView, costs []PodCost, rates map[string]nodeRate, hpas map[hpaKey]*autoscalingv2.HorizontalPodAutoscaler) []WorkloadCost {
	byKey := map[string]*WorkloadCost{}
	order := []string{}
	for i := range views {
		v := &views[i]
		k := v.namespace + "/" + v.vclusterNamespace + "/" + v.ownerKind + "/" + v.ownerName
		w := byKey[k]
		if w == nil {
			w = &WorkloadCost{Kind: v.ownerKind, Name: v.ownerName, Namespace: v.namespace, VClusterNamespace: v.vclusterNamespace, HasUsage: true}
			byKey[k] = w
			order = append(order, k)
		}
		pc := costs[i]
		w.Replicas++
		w.CPURequest += pc.CPURequest
		w.CPULimit += pc.CPULimit
		w.MemoryRequest += pc.MemoryRequest
		w.MemoryLimit += pc.MemoryLimit
		w.CPUUsed += pc.CPUUsed
		w.MemoryUsed += pc.MemoryUsed
		w.HasUsage = w.HasUsage && pc.HasUsage
		w.HourlyCost += pc.HourlyCost
		w.DailyCost += pc.DailyCost
		w.MonthlyCost += pc.MonthlyCost
		w.RightsizingSavings += rightsizingSavings(v, rates[v.nodeName])
		w.Pods = append(w.Pods, pc)
	}

	out := make([]WorkloadCost, 0, len(order))
	for _, k := range order {
		w := byKey[k]
		if w.HasUsage {
			w.CPUEfficiency, w.MemoryEfficiency, w.OverallEfficiency = usageEfficiency(w.CPUUsed, w.CPURequest, w.MemoryUsed, w.MemoryRequest)
		} else {
			w.CPUUsed, w.MemoryUsed, w.RightsizingSavings = 0, 0, 0
		}
		if h := hpas[hpaKey{w.Namespace, w.Kind, w.Name}]; h != nil && w.VClusterNamespace == "" {
			w.HPA = hpaCosts(h, w)
		}
		sort.Slice(w.Pods, func(i, j int) bool { return w.Pods[i].MonthlyCost > w.Pods[j].MonthlyCost })
		out = append(out, *w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MonthlyCost > out[j].MonthlyCost })
	return out
}

func hpaCosts(h *autoscalingv2.HorizontalPodAutoscaler, w *WorkloadCost) *HPAInfo {
	if w.Replicas == 0 {
		return nil
	}
	perReplica := w.MonthlyCost / float64(w.Replicas)
	minReplicas := int32(1)
	if h.Spec.MinReplicas != nil {
		minReplicas = *h.Spec.MinReplicas
	}
	return &HPAInfo{
		MinReplicas:     int(minReplicas),
		MaxReplicas:     int(h.Spec.MaxReplicas),
		CurrentReplicas: w.Replicas,
		MinMonthlyCost:  perReplica * float64(minReplicas),
		MaxMonthlyCost:  perReplica * float64(h.Spec.MaxReplicas),
	}
}

func buildNamespaces(views []podView, costs []PodCost, rates map[string]nodeRate, hpas map[hpaKey]*autoscalingv2.HorizontalPodAutoscaler) []NamespaceCost {
	idx := map[string][]int{}
	for i := range views {
		idx[views[i].namespace] = append(idx[views[i].namespace], i)
	}
	out := make([]NamespaceCost, 0, len(idx))
	for ns, members := range idx {
		nv := make([]podView, len(members))
		nc := make([]PodCost, len(members))
		for j, i := range members {
			nv[j], nc[j] = views[i], costs[i]
		}
		n := NamespaceCost{Namespace: ns, HasUsage: true}
		for j := range nc {
			pc := &nc[j]
			n.PodCount++
			n.CPURequest += pc.CPURequest
			n.CPULimit += pc.CPULimit
			n.MemoryRequest += pc.MemoryRequest
			n.MemoryLimit += pc.MemoryLimit
			n.CPUUsed += pc.CPUUsed
			n.MemoryUsed += pc.MemoryUsed
			n.HasUsage = n.HasUsage && pc.HasUsage
			n.HourlyCost += pc.HourlyCost
			n.DailyCost += pc.DailyCost
			n.MonthlyCost += pc.MonthlyCost
			if nv[j].vcluster != "" {
				n.VCluster = nv[j].vcluster
			}
		}
		n.TopWorkloads = groupWorkloads(nv, nc, rates, hpas)
		if n.HasUsage {
			n.CPUEfficiency, n.MemoryEfficiency, n.OverallEfficiency = usageEfficiency(n.CPUUsed, n.CPURequest, n.MemoryUsed, n.MemoryRequest)
			for _, w := range n.TopWorkloads {
				n.RightsizingSavings += w.RightsizingSavings
			}
		} else {
			n.CPUUsed, n.MemoryUsed = 0, 0
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MonthlyCost != out[j].MonthlyCost {
			return out[i].MonthlyCost > out[j].MonthlyCost
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func buildSummary(in computeInput, rates map[string]nodeRate, nodes []NodeCost, namespaces []NamespaceCost, pods, cpPods []PodCost) *ClusterCostSummary {
	scopeVC := in.vcluster != nil
	s := &ClusterCostSummary{
		Cluster:        in.cluster,
		Scope:          ScopeCluster,
		Provider:       in.provider,
		Region:         dominantRegion(in.nodes),
		NodeCount:      len(nodes),
		PodCount:       len(pods),
		NamespaceCount: in.namespaceCount,
		UsageAvailable: len(in.usage) > 0,
		LastUpdated:    in.now,
	}
	if scopeVC {
		s.Scope = ScopeVCluster
		s.NamespaceCount = len(namespaces)
	}

	var withPrice, missing int
	var nodeHourly, cpuHourly, memHourly, idleMonthly float64
	for i := range nodes {
		n := &nodes[i]
		if n.IsSpot {
			s.SpotNodeCount++
		}
		if n.SpotAtOnDemand {
			s.SpotAtOnDemandCount++
			if scopeVC {
				s.SpotAtOnDemandCost += n.AllocatedMonthlyCost
			} else {
				s.SpotAtOnDemandCost += n.MonthlyCost
			}
		}
		if n.PriceMissing {
			missing++
		} else {
			withPrice++
			nodeHourly += n.HourlyCost
			r := rates[n.NodeName]
			cpuHourly += float64(n.CPUCapacity) / 1000 * r.cpuRate
			memHourly += float64(n.MemoryCapacity) / bytesPerGiB * r.memRate
			idleMonthly += max(n.MonthlyCost-n.AllocatedMonthlyCost, 0)
		}
		if !scopeVC {
			s.TotalCPU += n.CPUAllocatable
			s.TotalMemory += n.MemoryAllocatable
		}
	}

	var allocatedHourly float64
	for i := range pods {
		s.RequestedCPU += pods[i].CPURequest
		s.RequestedMemory += pods[i].MemoryRequest
		s.UsedCPU += pods[i].CPUUsed
		s.UsedMemory += pods[i].MemoryUsed
		allocatedHourly += pods[i].HourlyCost
	}
	for _, ns := range namespaces {
		s.RightsizingSavings += ns.RightsizingSavings
	}

	if scopeVC {
		var cpHourly float64
		for i := range cpPods {
			cpHourly += cpPods[i].HourlyCost
		}
		s.HourlyCost = allocatedHourly + cpHourly
		s.AllocatedCost = allocatedHourly * hoursPerMonth
		s.Breakdown = CostBreakdown{
			ComputeCost:      allocatedHourly * hoursPerMonth,
			ControlPlaneCost: cpHourly * hoursPerMonth,
		}
		vc := &VClusterCost{
			Host:            in.vcluster.host,
			Namespace:       in.vcluster.namespace,
			Name:            in.vcluster.name,
			HostMonthlyCost: in.vcluster.hostMonthlyCost,
		}
		if vc.HostMonthlyCost > 0 {
			vc.SharePercent = s.HourlyCost * hoursPerMonth / vc.HostMonthlyCost * 100
		}
		s.VCluster = vc
	} else {
		fee := 0.0
		if withPrice > 0 {
			fee = controlPlaneHourly(in.provider)
		}
		s.HourlyCost = nodeHourly + fee
		s.AllocatedCost = allocatedHourly * hoursPerMonth
		s.IdleCost = idleMonthly
		if s.HourlyCost > 0 {
			s.IdlePercentage = idleMonthly / (s.HourlyCost * hoursPerMonth) * 100
		}
		if s.TotalCPU > 0 {
			s.CPUEfficiency = float64(s.RequestedCPU) / float64(s.TotalCPU) * 100
		}
		if s.TotalMemory > 0 {
			s.MemoryEfficiency = float64(s.RequestedMemory) / float64(s.TotalMemory) * 100
		}
		s.OverallEfficiency = (s.CPUEfficiency + s.MemoryEfficiency) / 2
		s.Breakdown = CostBreakdown{
			ComputeCost:      nodeHourly * hoursPerMonth,
			CPUCost:          cpuHourly * hoursPerMonth,
			MemoryCost:       memHourly * hoursPerMonth,
			ControlPlaneCost: fee * hoursPerMonth,
		}
	}
	s.DailyCost = s.HourlyCost * 24
	s.MonthlyCost = s.HourlyCost * hoursPerMonth

	st := in.pricingStatus
	s.PricingInfo = PricingInfo{
		Source:            string(st.Source),
		LastUpdated:       st.LastUpdated,
		InstanceCount:     st.InstanceCount,
		IsAvailable:       st.IsAvailable,
		Error:             st.Error,
		NodesWithPricing:  withPrice,
		NodesMissingPrice: missing,
		Supported:         pricingSupported(in.provider, uniqueRegions(in.nodes)),
	}
	return s
}

func buildRecommendations(in computeInput, nodes []NodeCost, namespaces []NamespaceCost) []CostRecommendation {
	recs := []CostRecommendation{}

	for _, ns := range namespaces {
		for _, w := range ns.TopWorkloads {
			if w.RightsizingSavings >= minRecommendationSavings {
				cpuPct, memPct := w.CPUEfficiency, w.MemoryEfficiency
				recs = append(recs, CostRecommendation{
					Type:              "rightsize",
					Resource:          w.Name,
					Kind:              w.Kind,
					Namespace:         w.Namespace,
					VClusterNamespace: w.VClusterNamespace,
					CurrentCost:       w.MonthlyCost,
					ProjectedSavings:  w.RightsizingSavings,
					Recommendation:    fmt.Sprintf("Uses %.0f%% of its CPU and %.0f%% of its memory requests. Lower the requests to about 1.3× observed usage.", cpuPct, memPct),
					Priority:          savingsPriority(w.RightsizingSavings),
				})
			}
			if w.CPURequest == 0 && w.MemoryRequest == 0 {
				recs = append(recs, CostRecommendation{
					Type:              "no-requests",
					Resource:          w.Name,
					Kind:              w.Kind,
					Namespace:         w.Namespace,
					VClusterNamespace: w.VClusterNamespace,
					CurrentCost:       w.MonthlyCost,
					Recommendation:    "Sets no CPU or memory requests, so the scheduler can't place it well and its cost can't be attributed.",
					Priority:          "medium",
				})
			}
		}
	}

	// Node consolidation only makes sense for the cluster that owns the nodes.
	if in.vcluster == nil {
		for _, n := range nodes {
			if n.PriceMissing || n.CPUAllocatable == 0 || n.MemoryAllocatable == 0 {
				continue
			}
			cpu := float64(n.CPURequested) / float64(n.CPUAllocatable)
			mem := float64(n.MemoryRequested) / float64(n.MemoryAllocatable)
			idle := n.MonthlyCost - n.AllocatedMonthlyCost
			if cpu < 0.3 && mem < 0.3 && idle >= minRecommendationSavings {
				recs = append(recs, CostRecommendation{
					Type:             "underutilized-node",
					Resource:         n.NodeName,
					Kind:             "Node",
					CurrentCost:      n.MonthlyCost,
					ProjectedSavings: idle,
					Recommendation:   fmt.Sprintf("This %s has %.0f%% of its CPU and %.0f%% of its memory requested. Its pods would fit on fewer or smaller nodes.", n.InstanceType, cpu*100, mem*100),
					Priority:         savingsPriority(idle),
				})
			}
		}
	}

	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].ProjectedSavings != recs[j].ProjectedSavings {
			return recs[i].ProjectedSavings > recs[j].ProjectedSavings
		}
		return recs[i].CurrentCost > recs[j].CurrentCost
	})
	if len(recs) > maxRecommendations {
		recs = recs[:maxRecommendations]
	}
	return recs
}

func savingsPriority(monthly float64) string {
	switch {
	case monthly >= 200:
		return "high"
	case monthly >= 50:
		return "medium"
	default:
		return "low"
	}
}
