package finops

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Service struct {
	k8s              k8s.Interface
	cache            *cache.Cache
	hybridPricing    *HybridPricingProvider
	mu               sync.RWMutex
	clusterProviders map[string]string
	controlPlaneCost float64
	pricingStats     struct {
		nodesWithPricing  int
		nodesMissingPrice int
	}
}

func NewService(k8sClient k8s.Interface, cacheInstance *cache.Cache) *Service {
	curConfig := &CURConfig{
		DataPath: "", // Will use default ~/.kanivet/cur-data
	}
	return &Service{
		k8s:              k8sClient,
		cache:            cacheInstance,
		hybridPricing:    NewHybridPricingProvider(curConfig),
		clusterProviders: make(map[string]string),
		controlPlaneCost: 0.10,
	}
}

func (s *Service) GetPricingStatus() PricingStatus {
	return s.hybridPricing.GetStatus()
}

func (s *Service) PreloadPricing(ctx context.Context, cluster string) error {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return err
	}
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 10})
	if err != nil {
		return err
	}
	if len(nodes.Items) == 0 {
		return nil
	}
	provider, region := s.detectProviderAndRegion(nodes.Items)
	if strings.Contains(provider, "GCP") || strings.Contains(provider, "GKE") {
		return nil
	}
	s.hybridPricing.SetProvider(provider)
	regions := s.collectUniqueRegions(nodes.Items, region)
	s.ensurePricing(regions)
	return nil
}

func (s *Service) GetDashboard(ctx context.Context, cluster string) (*Dashboard, error) {
	cacheKey := s.cache.BuildKey("finops:dashboard", cluster)
	data, err := s.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		return s.calculateDashboard(ctx, cluster, nil)
	})
	if err != nil {
		return nil, err
	}
	return data.(*Dashboard), nil
}

func (s *Service) StreamDashboard(ctx context.Context, cluster string, send func(string, interface{})) error {
	cacheKey := s.cache.BuildKey("finops:dashboard", cluster)
	var streamed bool
	data, err := s.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		streamed = true
		return s.calculateDashboard(ctx, cluster, send)
	})
	if err != nil {
		return err
	}
	d := data.(*Dashboard)
	if !streamed {
		send("summary", d.Summary)
		send("nodes", d.Nodes)
		send("namespaces", d.Namespaces)
	}
	return nil
}

func (s *Service) calculateDashboard(ctx context.Context, cluster string, send func(string, interface{})) (*Dashboard, error) {
	start := time.Now()
	defer func() {
		log.Printf("[FinOps] dashboard %s calculated in %s", cluster, time.Since(start).Round(time.Millisecond))
	}()
	emit := func(t string, d interface{}) {
		if send != nil {
			send(t, d)
		}
	}
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	var nodes *v1.NodeList
	var pods *v1.PodList
	var namespaceList *v1.NamespaceList
	var fetchErr error
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		var err error
		nodes, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			fetchErr = err
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		pods, err = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "status.phase!=Succeeded,status.phase!=Failed",
		})
		if err != nil {
			fetchErr = err
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		namespaceList, err = clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			fetchErr = err
		}
	}()
	wg.Wait()

	if fetchErr != nil {
		return nil, fetchErr
	}

	provider, region := s.detectProviderAndRegion(nodes.Items)
	if strings.Contains(provider, "GCP") || strings.Contains(provider, "GKE") {
		return nil, fmt.Errorf("GCP pricing not yet supported. AWS and Azure clusters are fully supported")
	}

	s.hybridPricing.SetProvider(provider)
	regions := s.collectUniqueRegions(nodes.Items, region)
	s.ensurePricing(regions)

	nodeCosts := s.calculateNodeCosts(ctx, cluster, nodes.Items, pods.Items, provider, region)
	emit("nodes", nodeCosts)
	nodeRates := s.calculateNodeRates(nodes.Items, provider, region)
	podCosts := s.calculatePodCostsFromData(pods.Items, nodeRates)

	var totalCPU, totalMemory, requestedCPU, requestedMemory int64
	var totalHourlyCost, spotSavings float64
	for _, nc := range nodeCosts {
		totalCPU += nc.CPUAllocatable
		totalMemory += nc.MemoryAllocatable
		requestedCPU += nc.CPURequested
		requestedMemory += nc.MemoryRequested
		totalHourlyCost += nc.HourlyCost
		if nc.IsSpot {
			spotSavings += nc.HourlyCost * 0.6
		}
	}

	cpuEfficiency, memoryEfficiency := 0.0, 0.0
	if totalCPU > 0 {
		cpuEfficiency = float64(requestedCPU) / float64(totalCPU) * 100
	}
	if totalMemory > 0 {
		memoryEfficiency = float64(requestedMemory) / float64(totalMemory) * 100
	}
	overallEfficiency := (cpuEfficiency + memoryEfficiency) / 2
	totalHourlyCost += s.controlPlaneCost

	idleCost, idlePercentage := 0.0, 0.0
	if totalCPU > 0 && totalMemory > 0 && totalHourlyCost > 0 {
		idleCPUCost := (1 - float64(requestedCPU)/float64(totalCPU)) * totalHourlyCost * 0.7
		idleMemCost := (1 - float64(requestedMemory)/float64(totalMemory)) * totalHourlyCost * 0.3
		idleCost = idleCPUCost + idleMemCost
		if idleCost < 0 {
			idleCost = 0
		}
		idlePercentage = (idleCost / totalHourlyCost) * 100
	}

	pricingStatus := s.hybridPricing.GetStatus()
	summary := &ClusterCostSummary{
		Cluster: cluster, Provider: provider, Region: region,
		NodeCount: len(nodes.Items), PodCount: len(pods.Items), NamespaceCount: len(namespaceList.Items),
		TotalCPU: totalCPU, TotalMemory: totalMemory, RequestedCPU: requestedCPU, RequestedMemory: requestedMemory,
		HourlyCost: totalHourlyCost, DailyCost: totalHourlyCost * 24, MonthlyCost: totalHourlyCost * 24 * 30,
		ProjectedMonthlyCost: totalHourlyCost * 24 * 30,
		CPUEfficiency: cpuEfficiency, MemoryEfficiency: memoryEfficiency, OverallEfficiency: overallEfficiency,
		IdleCost: idleCost * 24 * 30, IdlePercentage: idlePercentage, SpotSavings: spotSavings * 24 * 30,
		Breakdown: CostBreakdown{
			ComputeCost: totalHourlyCost * 24 * 30, CPUCost: totalHourlyCost * 0.7 * 24 * 30,
			MemoryCost: totalHourlyCost * 0.3 * 24 * 30, ControlPlaneCost: s.controlPlaneCost * 24 * 30,
		},
		PricingInfo: PricingInfo{
			Source: string(pricingStatus.Source), LastUpdated: pricingStatus.LastUpdated,
			InstanceCount: pricingStatus.InstanceCount, IsAvailable: pricingStatus.IsAvailable,
			Error: pricingStatus.Error, NodesWithPricing: len(nodeCosts), NodesMissingPrice: len(nodes.Items) - len(nodeCosts),
		},
		LastUpdated: time.Now(),
	}
	emit("summary", summary)

	nsMap := make(map[string]*NamespaceCost)
	workloadMap := make(map[string]*WorkloadCost)
	for _, pc := range podCosts {
		ns, ok := nsMap[pc.Namespace]
		if !ok {
			ns = &NamespaceCost{Namespace: pc.Namespace}
			nsMap[pc.Namespace] = ns
		}
		ns.PodCount++
		ns.CPURequest += pc.CPURequest
		ns.CPULimit += pc.CPULimit
		ns.MemoryRequest += pc.MemoryRequest
		ns.MemoryLimit += pc.MemoryLimit
		ns.HourlyCost += pc.HourlyCost
		ns.DailyCost += pc.DailyCost
		ns.MonthlyCost += pc.MonthlyCost

		if pc.OwnerKind != "" && pc.OwnerKind != "Node" {
			wKey := fmt.Sprintf("%s/%s/%s", pc.Namespace, pc.OwnerKind, pc.OwnerName)
			wc, ok := workloadMap[wKey]
			if !ok {
				wc = &WorkloadCost{Kind: pc.OwnerKind, Name: pc.OwnerName, Namespace: pc.Namespace}
				workloadMap[wKey] = wc
			}
			wc.Replicas++
			wc.CPURequest += pc.CPURequest
			wc.CPULimit += pc.CPULimit
			wc.MemoryRequest += pc.MemoryRequest
			wc.MemoryLimit += pc.MemoryLimit
			wc.HourlyCost += pc.HourlyCost
			wc.DailyCost += pc.DailyCost
			wc.MonthlyCost += pc.MonthlyCost
			wc.Pods = append(wc.Pods, pc)
		}
	}

	for _, wc := range workloadMap {
		if wc.CPULimit > 0 {
			wc.CPUEfficiency = float64(wc.CPURequest) / float64(wc.CPULimit) * 100
		}
		if wc.MemoryLimit > 0 {
			wc.MemoryEfficiency = float64(wc.MemoryRequest) / float64(wc.MemoryLimit) * 100
		}
		if wc.CPUEfficiency > 0 || wc.MemoryEfficiency > 0 {
			wc.OverallEfficiency = (wc.CPUEfficiency + wc.MemoryEfficiency) / 2
		}
		ns := nsMap[wc.Namespace]
		ns.TopWorkloads = append(ns.TopWorkloads, *wc)
	}

	namespaces := make([]NamespaceCost, 0, len(nsMap))
	for _, ns := range nsMap {
		if ns.CPULimit > 0 {
			ns.CPUEfficiency = float64(ns.CPURequest) / float64(ns.CPULimit) * 100
		}
		if ns.MemoryLimit > 0 {
			ns.MemoryEfficiency = float64(ns.MemoryRequest) / float64(ns.MemoryLimit) * 100
		}
		if ns.CPUEfficiency > 0 || ns.MemoryEfficiency > 0 {
			ns.OverallEfficiency = (ns.CPUEfficiency + ns.MemoryEfficiency) / 2
		}
		sort.Slice(ns.TopWorkloads, func(i, j int) bool {
			return ns.TopWorkloads[i].MonthlyCost > ns.TopWorkloads[j].MonthlyCost
		})
		namespaces = append(namespaces, *ns)
	}
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].MonthlyCost > namespaces[j].MonthlyCost
	})
	emit("namespaces", namespaces)

	return &Dashboard{Summary: summary, Nodes: nodeCosts, Namespaces: namespaces}, nil
}

func (s *Service) GetClusterCostSummary(ctx context.Context, cluster string) (*ClusterCostSummary, error) {
	cacheKey := s.cache.BuildKey("finops:summary", cluster)
	data, err := s.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		return s.calculateClusterCostSummary(ctx, cluster)
	})
	if err != nil {
		return nil, err
	}
	summary := data.(*ClusterCostSummary)
	if summary.PricingInfo.NodesWithPricing == 0 && summary.NodeCount > 0 {
		log.Printf("No pricing data in cached summary, recalculating...")
		s.cache.Delete(cacheKey)
		return s.calculateClusterCostSummary(ctx, cluster)
	}
	return summary, nil
}

func (s *Service) calculateClusterCostSummary(ctx context.Context, cluster string) (*ClusterCostSummary, error) {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	provider, region := s.detectProviderAndRegion(nodes.Items)
	
	if strings.Contains(provider, "GCP") || strings.Contains(provider, "GKE") {
		return nil, fmt.Errorf("GCP pricing not yet supported. AWS and Azure clusters are fully supported")
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase!=Succeeded,status.phase!=Failed",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	s.hybridPricing.SetProvider(provider)
	log.Printf("=== FINOPS: Cluster %s ===", cluster)
	log.Printf("  Provider: %s, Region: %s, Nodes: %d", provider, region, len(nodes.Items))

	for i, node := range nodes.Items {
		instanceType := s.getInstanceType(node.Labels)
		nodeRegion := s.getNodeRegion(node.Labels, region)
		log.Printf("  Node[%d] %s: instanceType=%s, region=%s", i, node.Name, instanceType, nodeRegion)
	}

	regions := s.collectUniqueRegions(nodes.Items, region)
	log.Printf("  Unique regions to preload: %v", regions)
	s.ensurePricing(regions)

	status := s.hybridPricing.GetStatus()
	log.Printf("  After preload - instanceCount=%d, error=%s", status.InstanceCount, status.Error)

	nodeCosts := s.calculateNodeCosts(ctx, cluster, nodes.Items, pods.Items, provider, region)
	log.Printf("  Nodes with pricing: %d/%d", len(nodeCosts), len(nodes.Items))

	var totalCPU, totalMemory, requestedCPU, requestedMemory int64
	var totalHourlyCost, spotSavings float64

	for _, nc := range nodeCosts {
		totalCPU += nc.CPUAllocatable
		totalMemory += nc.MemoryAllocatable
		requestedCPU += nc.CPURequested
		requestedMemory += nc.MemoryRequested
		totalHourlyCost += nc.HourlyCost
		if nc.IsSpot {
			spotSavings += nc.HourlyCost * 0.6
		}
	}

	cpuEfficiency := 0.0
	if totalCPU > 0 {
		cpuEfficiency = float64(requestedCPU) / float64(totalCPU) * 100
	}
	memoryEfficiency := 0.0
	if totalMemory > 0 {
		memoryEfficiency = float64(requestedMemory) / float64(totalMemory) * 100
	}
	overallEfficiency := (cpuEfficiency + memoryEfficiency) / 2

	totalHourlyCost += s.controlPlaneCost

	idleCost := 0.0
	idlePercentage := 0.0
	if totalCPU > 0 && totalMemory > 0 && totalHourlyCost > 0 {
		idleCPUCost := (1 - float64(requestedCPU)/float64(totalCPU)) * totalHourlyCost * 0.7
		idleMemCost := (1 - float64(requestedMemory)/float64(totalMemory)) * totalHourlyCost * 0.3
		idleCost = idleCPUCost + idleMemCost
		if idleCost < 0 {
			idleCost = 0
		}
		idlePercentage = (idleCost / totalHourlyCost) * 100
	}

	cpuCost := totalHourlyCost * 0.7
	memoryCost := totalHourlyCost * 0.3

	pricingStatus := s.hybridPricing.GetStatus()
	nodesWithPricing := len(nodeCosts)
	nodesMissingPrice := len(nodes.Items) - nodesWithPricing

	summary := &ClusterCostSummary{
		Cluster:         cluster,
		Provider:        provider,
		Region:          region,
		NodeCount:       len(nodes.Items),
		PodCount:        len(pods.Items),
		NamespaceCount:  len(namespaces.Items),
		TotalCPU:        totalCPU,
		TotalMemory:     totalMemory,
		RequestedCPU:    requestedCPU,
		RequestedMemory: requestedMemory,
		HourlyCost:      totalHourlyCost,
		DailyCost:       totalHourlyCost * 24,
		MonthlyCost:     totalHourlyCost * 24 * 30,
		ProjectedMonthlyCost: totalHourlyCost * 24 * 30,
		CPUEfficiency:   cpuEfficiency,
		MemoryEfficiency: memoryEfficiency,
		OverallEfficiency: overallEfficiency,
		IdleCost:        idleCost * 24 * 30,
		IdlePercentage:  idlePercentage,
		SpotSavings:     spotSavings * 24 * 30,
		Breakdown: CostBreakdown{
			ComputeCost:      totalHourlyCost * 24 * 30,
			CPUCost:          cpuCost * 24 * 30,
			MemoryCost:       memoryCost * 24 * 30,
			ControlPlaneCost: s.controlPlaneCost * 24 * 30,
		},
		PricingInfo: PricingInfo{
			Source:            string(pricingStatus.Source),
			LastUpdated:       pricingStatus.LastUpdated,
			InstanceCount:     pricingStatus.InstanceCount,
			IsAvailable:       pricingStatus.IsAvailable,
			Error:             pricingStatus.Error,
			NodesWithPricing:  nodesWithPricing,
			NodesMissingPrice: nodesMissingPrice,
		},
		LastUpdated: time.Now(),
	}

	return summary, nil
}

func (s *Service) GetNodeCosts(ctx context.Context, cluster string) ([]NodeCost, error) {
	cacheKey := s.cache.BuildKey("finops:nodes", cluster)
	data, err := s.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		return s.fetchNodeCosts(ctx, cluster)
	})
	if err != nil {
		return nil, err
	}
	return data.([]NodeCost), nil
}

func (s *Service) fetchNodeCosts(ctx context.Context, cluster string) ([]NodeCost, error) {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase!=Succeeded,status.phase!=Failed",
	})
	if err != nil {
		return nil, err
	}

	provider, region := s.detectProviderAndRegion(nodes.Items)
	regions := s.collectUniqueRegions(nodes.Items, region)
	s.ensurePricing(regions)

	return s.calculateNodeCosts(ctx, cluster, nodes.Items, pods.Items, provider, region), nil
}

func (s *Service) calculateNodeCosts(ctx context.Context, cluster string, nodes []v1.Node, pods []v1.Pod, provider, region string) []NodeCost {
	nodePods := make(map[string][]v1.Pod)
	for _, pod := range pods {
		if pod.Spec.NodeName != "" {
			nodePods[pod.Spec.NodeName] = append(nodePods[pod.Spec.NodeName], pod)
		}
	}

	costs := make([]NodeCost, 0, len(nodes))
	for _, node := range nodes {
		instanceType := s.getInstanceType(node.Labels)
		nodeRegion := s.getNodeRegion(node.Labels, region)
		isSpot := s.isSpotInstance(node.Labels)

		if instanceType == "" {
			log.Printf("Node %s has no instance type label", node.Name)
			continue
		}

		pricing, err := s.getPricing(provider, instanceType, nodeRegion)
		if err != nil {
			log.Printf("Failed to get pricing for %s (%s in %s): %v", node.Name, instanceType, nodeRegion, err)
			continue
		}

		hourlyCost := pricing.OnDemandPrice
		if isSpot && pricing.SpotPrice > 0 {
			hourlyCost = pricing.SpotPrice
		}

		var cpuRequested, memRequested int64
		podsOnNode := nodePods[node.Name]
		for _, pod := range podsOnNode {
			for _, container := range pod.Spec.Containers {
				cpuRequested += container.Resources.Requests.Cpu().MilliValue()
				memRequested += container.Resources.Requests.Memory().Value()
			}
		}

		costs = append(costs, NodeCost{
			NodeName:          node.Name,
			InstanceType:      instanceType,
			Region:            nodeRegion,
			Provider:          provider,
			HourlyCost:        hourlyCost,
			DailyCost:         hourlyCost * 24,
			MonthlyCost:       hourlyCost * 24 * 30,
			CPUCapacity:       node.Status.Capacity.Cpu().MilliValue(),
			MemoryCapacity:    node.Status.Capacity.Memory().Value(),
			CPUAllocatable:    node.Status.Allocatable.Cpu().MilliValue(),
			MemoryAllocatable: node.Status.Allocatable.Memory().Value(),
			CPURequested:      cpuRequested,
			MemoryRequested:   memRequested,
			PodCount:          len(podsOnNode),
			IsSpot:            isSpot,
			Labels:            node.Labels,
		})
	}

	sort.Slice(costs, func(i, j int) bool {
		return costs[i].MonthlyCost > costs[j].MonthlyCost
	})

	return costs
}

func (s *Service) GetPodCosts(ctx context.Context, cluster, namespace string) ([]PodCost, error) {
	cacheKey := s.cache.BuildKey("finops:pods", cluster, namespace)
	data, err := s.cache.GetOrSet(cacheKey, 30*time.Second, func() (interface{}, error) {
		return s.fetchPodCosts(ctx, cluster, namespace)
	})
	if err != nil {
		return nil, err
	}
	return data.([]PodCost), nil
}

func (s *Service) fetchPodCosts(ctx context.Context, cluster, namespace string) ([]PodCost, error) {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}

	listOpts := metav1.ListOptions{
		FieldSelector: "status.phase!=Succeeded,status.phase!=Failed",
	}
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	provider, region := s.detectProviderAndRegion(nodes.Items)
	regions := s.collectUniqueRegions(nodes.Items, region)
	s.ensurePricing(regions)

	nodeRates := s.calculateNodeRates(nodes.Items, provider, region)

	costs := make([]PodCost, 0, len(pods.Items))
	for _, pod := range pods.Items {
		var cpuReq, cpuLim, memReq, memLim int64
		for _, container := range pod.Spec.Containers {
			cpuReq += container.Resources.Requests.Cpu().MilliValue()
			cpuLim += container.Resources.Limits.Cpu().MilliValue()
			memReq += container.Resources.Requests.Memory().Value()
			memLim += container.Resources.Limits.Memory().Value()
		}

		rates := nodeRates[pod.Spec.NodeName]
		cpuCost := float64(cpuReq) / 1000 * rates.cpuRate
		memCost := float64(memReq) / (1024 * 1024 * 1024) * rates.memRate
		hourlyCost := cpuCost + memCost

		ownerKind, ownerName := s.getOwner(pod.OwnerReferences)

		costs = append(costs, PodCost{
			PodName:       pod.Name,
			Namespace:     pod.Namespace,
			NodeName:      pod.Spec.NodeName,
			OwnerKind:     ownerKind,
			OwnerName:     ownerName,
			CPURequest:    cpuReq,
			CPULimit:      cpuLim,
			MemoryRequest: memReq,
			MemoryLimit:   memLim,
			HourlyCost:    hourlyCost,
			DailyCost:     hourlyCost * 24,
			MonthlyCost:   hourlyCost * 24 * 30,
			Status:        string(pod.Status.Phase),
			CreatedAt:     pod.CreationTimestamp.Time,
			Labels:        pod.Labels,
		})
	}

	sort.Slice(costs, func(i, j int) bool {
		return costs[i].MonthlyCost > costs[j].MonthlyCost
	})

	return costs, nil
}

func (s *Service) calculatePodCostsFromData(pods []v1.Pod, nodeRates map[string]nodeRates) []PodCost {
	costs := make([]PodCost, 0, len(pods))
	for _, pod := range pods {
		var cpuReq, cpuLim, memReq, memLim int64
		for _, container := range pod.Spec.Containers {
			cpuReq += container.Resources.Requests.Cpu().MilliValue()
			cpuLim += container.Resources.Limits.Cpu().MilliValue()
			memReq += container.Resources.Requests.Memory().Value()
			memLim += container.Resources.Limits.Memory().Value()
		}
		rates := nodeRates[pod.Spec.NodeName]
		cpuCost := float64(cpuReq) / 1000 * rates.cpuRate
		memCost := float64(memReq) / (1024 * 1024 * 1024) * rates.memRate
		hourlyCost := cpuCost + memCost
		ownerKind, ownerName := s.getOwner(pod.OwnerReferences)
		costs = append(costs, PodCost{
			PodName: pod.Name, Namespace: pod.Namespace, NodeName: pod.Spec.NodeName,
			OwnerKind: ownerKind, OwnerName: ownerName,
			CPURequest: cpuReq, CPULimit: cpuLim, MemoryRequest: memReq, MemoryLimit: memLim,
			HourlyCost: hourlyCost, DailyCost: hourlyCost * 24, MonthlyCost: hourlyCost * 24 * 30,
			Status: string(pod.Status.Phase), CreatedAt: pod.CreationTimestamp.Time, Labels: pod.Labels,
		})
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i].MonthlyCost > costs[j].MonthlyCost })
	return costs
}

func (s *Service) GetNamespaceCosts(ctx context.Context, cluster string) ([]NamespaceCost, error) {
	cacheKey := s.cache.BuildKey("finops:namespaces", cluster)
	data, err := s.cache.GetOrSet(cacheKey, 60*time.Second, func() (interface{}, error) {
		return s.fetchNamespaceCosts(ctx, cluster)
	})
	if err != nil {
		return nil, err
	}
	return data.([]NamespaceCost), nil
}

func (s *Service) fetchNamespaceCosts(ctx context.Context, cluster string) ([]NamespaceCost, error) {
	podCosts, err := s.GetPodCosts(ctx, cluster, "")
	if err != nil {
		return nil, err
	}

	nsMap := make(map[string]*NamespaceCost)
	for _, pc := range podCosts {
		ns, ok := nsMap[pc.Namespace]
		if !ok {
			ns = &NamespaceCost{Namespace: pc.Namespace}
			nsMap[pc.Namespace] = ns
		}
		ns.PodCount++
		ns.CPURequest += pc.CPURequest
		ns.CPULimit += pc.CPULimit
		ns.MemoryRequest += pc.MemoryRequest
		ns.MemoryLimit += pc.MemoryLimit
		ns.HourlyCost += pc.HourlyCost
		ns.DailyCost += pc.DailyCost
		ns.MonthlyCost += pc.MonthlyCost
	}

	costs := make([]NamespaceCost, 0, len(nsMap))
	for _, ns := range nsMap {
		if ns.CPULimit > 0 && ns.CPURequest > 0 {
			ns.CPUEfficiency = float64(ns.CPURequest) / float64(ns.CPULimit) * 100
		}
		if ns.MemoryLimit > 0 && ns.MemoryRequest > 0 {
			ns.MemoryEfficiency = float64(ns.MemoryRequest) / float64(ns.MemoryLimit) * 100
		}
		if ns.CPUEfficiency > 0 || ns.MemoryEfficiency > 0 {
			ns.OverallEfficiency = (ns.CPUEfficiency + ns.MemoryEfficiency) / 2
		}
		costs = append(costs, *ns)
	}

	sort.Slice(costs, func(i, j int) bool {
		return costs[i].MonthlyCost > costs[j].MonthlyCost
	})

	return costs, nil
}

func (s *Service) GetWorkloadCosts(ctx context.Context, cluster, namespace string) ([]WorkloadCost, error) {
	cacheKey := s.cache.BuildKey("finops:workloads", cluster, namespace)
	data, err := s.cache.GetOrSet(cacheKey, 30*time.Second, func() (interface{}, error) {
		return s.fetchWorkloadCosts(ctx, cluster, namespace)
	})
	if err != nil {
		return nil, err
	}
	return data.([]WorkloadCost), nil
}

func (s *Service) fetchWorkloadCosts(ctx context.Context, cluster, namespace string) ([]WorkloadCost, error) {
	podCosts, err := s.GetPodCosts(ctx, cluster, namespace)
	if err != nil {
		return nil, err
	}

	hpaMap := s.fetchHPAs(ctx, cluster, namespace)
	vpaMap := s.fetchVPAs(ctx, cluster, namespace)

	workloadMap := make(map[string]*WorkloadCost)
	for _, pc := range podCosts {
		if pc.OwnerKind == "" || pc.OwnerKind == "Node" {
			continue
		}
		key := fmt.Sprintf("%s/%s/%s", pc.Namespace, pc.OwnerKind, pc.OwnerName)
		wc, ok := workloadMap[key]
		if !ok {
			wc = &WorkloadCost{
				Kind:      pc.OwnerKind,
				Name:      pc.OwnerName,
				Namespace: pc.Namespace,
			}
			workloadMap[key] = wc
		}
		wc.Replicas++
		wc.CPURequest += pc.CPURequest
		wc.CPULimit += pc.CPULimit
		wc.MemoryRequest += pc.MemoryRequest
		wc.MemoryLimit += pc.MemoryLimit
		wc.HourlyCost += pc.HourlyCost
		wc.DailyCost += pc.DailyCost
		wc.MonthlyCost += pc.MonthlyCost
	}

	costs := make([]WorkloadCost, 0, len(workloadMap))
	for _, wc := range workloadMap {
		if wc.CPULimit > 0 && wc.CPURequest > 0 {
			wc.CPUEfficiency = float64(wc.CPURequest) / float64(wc.CPULimit) * 100
		}
		if wc.MemoryLimit > 0 && wc.MemoryRequest > 0 {
			wc.MemoryEfficiency = float64(wc.MemoryRequest) / float64(wc.MemoryLimit) * 100
		}
		if wc.CPUEfficiency > 0 || wc.MemoryEfficiency > 0 {
			wc.OverallEfficiency = (wc.CPUEfficiency + wc.MemoryEfficiency) / 2
		}

		hpaKey := fmt.Sprintf("%s/%s", wc.Namespace, wc.Name)
		if hpa, ok := hpaMap[hpaKey]; ok {
			wc.HPA = s.calculateHPACosts(hpa, wc)
		}

		if vpa, ok := vpaMap[hpaKey]; ok {
			wc.VPA = s.calculateVPASavings(vpa, wc)
		}

		costs = append(costs, *wc)
	}

	sort.Slice(costs, func(i, j int) bool {
		return costs[i].MonthlyCost > costs[j].MonthlyCost
	})

	return costs, nil
}

func (s *Service) fetchHPAs(ctx context.Context, cluster, namespace string) map[string]*autoscalingv2.HorizontalPodAutoscaler {
	hpaMap := make(map[string]*autoscalingv2.HorizontalPodAutoscaler)
	
	clientIface, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return hpaMap
	}

	hpas, err := clientIface.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return hpaMap
	}
	
	for i := range hpas.Items {
		hpa := &hpas.Items[i]
		targetName := hpa.Spec.ScaleTargetRef.Name
		key := fmt.Sprintf("%s/%s", hpa.Namespace, targetName)
		hpaMap[key] = hpa
	}
	
	if len(hpaMap) > 0 {
		log.Printf("[FinOps] Found %d HPAs in namespace %s", len(hpaMap), namespace)
	}
	return hpaMap
}

func (s *Service) fetchVPAs(ctx context.Context, cluster, namespace string) map[string]interface{} {
	vpaMap := make(map[string]interface{})
	return vpaMap
}

func (s *Service) calculateHPACosts(hpa *autoscalingv2.HorizontalPodAutoscaler, wc *WorkloadCost) *HPAInfo {
	if wc.Replicas == 0 {
		return nil
	}
	
	perReplicaCost := wc.MonthlyCost / float64(wc.Replicas)
	minReplicas := int32(1)
	if hpa.Spec.MinReplicas != nil {
		minReplicas = *hpa.Spec.MinReplicas
	}
	
	return &HPAInfo{
		MinReplicas:     int(minReplicas),
		MaxReplicas:     int(hpa.Spec.MaxReplicas),
		CurrentReplicas: wc.Replicas,
		MinMonthlyCost:  perReplicaCost * float64(minReplicas),
		MaxMonthlyCost:  perReplicaCost * float64(hpa.Spec.MaxReplicas),
	}
}

func (s *Service) calculateVPASavings(vpa interface{}, wc *WorkloadCost) *VPAInfo {
	return &VPAInfo{HasRecommendation: false}
}

func (s *Service) GetCostRecommendations(ctx context.Context, cluster string) ([]CostRecommendation, error) {
	cacheKey := s.cache.BuildKey("finops:recommendations", cluster)
	data, err := s.cache.GetOrSet(cacheKey, 5*time.Minute, func() (interface{}, error) {
		return s.generateRecommendations(ctx, cluster)
	})
	if err != nil {
		return nil, err
	}
	return data.([]CostRecommendation), nil
}

func (s *Service) generateRecommendations(ctx context.Context, cluster string) ([]CostRecommendation, error) {
	recommendations := make([]CostRecommendation, 0)

	nodeCosts, err := s.GetNodeCosts(ctx, cluster)
	if err == nil {
		for _, nc := range nodeCosts {
			if nc.CPURequested > 0 && nc.CPUAllocatable > 0 {
				utilization := float64(nc.CPURequested) / float64(nc.CPUAllocatable)
				if utilization < 0.3 {
					recommendations = append(recommendations, CostRecommendation{
						Type:             "underutilized-node",
						Resource:         nc.NodeName,
						CurrentCost:      nc.MonthlyCost,
						ProjectedSavings: nc.MonthlyCost * 0.5,
						Recommendation:   fmt.Sprintf("Node %s has only %.0f%% CPU utilization. Consider consolidating workloads.", nc.NodeName, utilization*100),
						Priority:         "medium",
					})
				}
			}
			if !nc.IsSpot {
				recommendations = append(recommendations, CostRecommendation{
					Type:             "spot-candidate",
					Resource:         nc.NodeName,
					CurrentCost:      nc.MonthlyCost,
					ProjectedSavings: nc.MonthlyCost * 0.6,
					Recommendation:   fmt.Sprintf("Node %s could use Spot instances to save up to 60%%.", nc.NodeName),
					Priority:         "low",
				})
			}
		}
	}

	podCosts, err := s.GetPodCosts(ctx, cluster, "")
	if err == nil {
		for _, pc := range podCosts {
			if pc.CPURequest == 0 && pc.MemoryRequest == 0 {
				recommendations = append(recommendations, CostRecommendation{
					Type:           "no-resource-requests",
					Resource:       fmt.Sprintf("%s/%s", pc.Namespace, pc.PodName),
					Namespace:      pc.Namespace,
					Recommendation: "Pod has no resource requests defined. Define requests for better scheduling and cost allocation.",
					Priority:       "high",
				})
			}
		}
	}

	return recommendations, nil
}

type nodeRates struct {
	cpuRate float64
	memRate float64
}

func (s *Service) calculateNodeRates(nodes []v1.Node, provider, region string) map[string]nodeRates {
	rates := make(map[string]nodeRates)
	for _, node := range nodes {
		instanceType := s.getInstanceType(node.Labels)
		nodeRegion := s.getNodeRegion(node.Labels, region)
		pricing, err := s.getPricing(provider, instanceType, nodeRegion)
		if err != nil {
			log.Printf("No pricing for node %s (%s): %v", node.Name, instanceType, err)
			continue
		}
		rates[node.Name] = nodeRates{
			cpuRate: pricing.CPUHourlyRate,
			memRate: pricing.MemoryHourlyRate,
		}
	}
	return rates
}

func (s *Service) getPricing(provider, instanceType, region string) (*InstancePricing, error) {
	if strings.Contains(provider, "GCP") || strings.Contains(provider, "GKE") {
		return nil, fmt.Errorf("GCP pricing not supported")
	}
	return s.hybridPricing.GetInstancePricingCached(instanceType, region)
}

// ensurePricing never blocks a request on a cold offer-file download: costs
// render as missing and the finops cache is invalidated once pricing lands so
// the next poll fills them in.
func (s *Service) ensurePricing(regions []string) {
	s.hybridPricing.EnsureRegions(regions, func() {
		s.cache.Invalidate("finops:*")
	})
}

func (s *Service) detectProviderAndRegion(nodes []v1.Node) (string, string) {
	if len(nodes) == 0 {
		return "Unknown", "us-east-1"
	}

	node := nodes[0]
	labels := node.Labels

	if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
		region := labels["topology.kubernetes.io/region"]
		if region == "" {
			region = labels["failure-domain.beta.kubernetes.io/region"]
		}
		if region == "" {
			region = "us-east-1"
		}
		return "AWS EKS", region
	}

	if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
		region := labels["topology.kubernetes.io/region"]
		if region == "" {
			region = "us-central1"
		}
		return "GCP GKE", region
	}

	if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
		region := labels["topology.kubernetes.io/region"]
		if region == "" {
			region = "eastus"
		}
		return "Azure AKS", region
	}

	if instanceType, ok := labels["node.kubernetes.io/instance-type"]; ok {
		if strings.HasPrefix(instanceType, "t2.") || strings.HasPrefix(instanceType, "t3.") ||
			strings.HasPrefix(instanceType, "m5.") || strings.HasPrefix(instanceType, "m6") ||
			strings.HasPrefix(instanceType, "c5.") || strings.HasPrefix(instanceType, "r5.") {
			region := labels["topology.kubernetes.io/region"]
			if region == "" {
				region = "us-east-1"
			}
			return "AWS EC2", region
		}
	}

	return "Unknown", "us-east-1"
}

func (s *Service) getInstanceType(labels map[string]string) string {
	if it, ok := labels["node.kubernetes.io/instance-type"]; ok {
		return it
	}
	if it, ok := labels["beta.kubernetes.io/instance-type"]; ok {
		return it
	}
	return ""
}

func (s *Service) getNodeRegion(labels map[string]string, defaultRegion string) string {
	if region, ok := labels["topology.kubernetes.io/region"]; ok {
		return region
	}
	if region, ok := labels["failure-domain.beta.kubernetes.io/region"]; ok {
		return region
	}
	return defaultRegion
}

func (s *Service) isSpotInstance(labels map[string]string) bool {
	if lifecycle, ok := labels["node.kubernetes.io/lifecycle"]; ok {
		return lifecycle == "spot"
	}
	if capacityType, ok := labels["eks.amazonaws.com/capacityType"]; ok {
		return capacityType == "SPOT"
	}
	if capacityType, ok := labels["karpenter.sh/capacity-type"]; ok {
		return capacityType == "spot"
	}
	if _, ok := labels["cloud.google.com/gke-preemptible"]; ok {
		return true
	}
	if scalesetPriority, ok := labels["kubernetes.azure.com/scalesetpriority"]; ok {
		return scalesetPriority == "spot"
	}
	return false
}

func (s *Service) getOwner(refs []metav1.OwnerReference) (string, string) {
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller {
			kind := ref.Kind
			if kind == "ReplicaSet" {
				parts := strings.Split(ref.Name, "-")
				if len(parts) > 1 {
					return "Deployment", strings.Join(parts[:len(parts)-1], "-")
				}
			}
			return kind, ref.Name
		}
	}
	return "", ""
}

func (s *Service) collectUniqueRegions(nodes []v1.Node, defaultRegion string) []string {
	regionSet := make(map[string]struct{})
	for _, node := range nodes {
		region := s.getNodeRegion(node.Labels, defaultRegion)
		regionSet[region] = struct{}{}
	}
	regions := make([]string, 0, len(regionSet))
	for r := range regionSet {
		regions = append(regions, r)
	}
	return regions
}

type PricingDebugInfo struct {
	Provider      string            `json:"provider"`
	Region        string            `json:"region"`
	Nodes         []NodeDebugInfo   `json:"nodes"`
	PricingStatus PricingStatus     `json:"pricingStatus"`
}

type NodeDebugInfo struct {
	Name         string `json:"name"`
	InstanceType string `json:"instanceType"`
	Region       string `json:"region"`
	HasPricing   bool   `json:"hasPricing"`
	Error        string `json:"error,omitempty"`
}

func (s *Service) GetPricingDebug(ctx context.Context, cluster string) (*PricingDebugInfo, error) {
	clientset, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	provider, region := s.detectProviderAndRegion(nodes.Items)
	log.Printf("=== PRICING DEBUG for cluster %s ===", cluster)
	log.Printf("Detected provider: %s, region: %s", provider, region)

	regions := s.collectUniqueRegions(nodes.Items, region)
	log.Printf("Unique regions: %v", regions)
	s.ensurePricing(regions)

	status := s.hybridPricing.GetStatus()
	log.Printf("Pricing status: source=%s, instanceCount=%d, isAvailable=%v, error=%s",
		status.Source, status.InstanceCount, status.IsAvailable, status.Error)

	nodeInfos := make([]NodeDebugInfo, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		instanceType := s.getInstanceType(node.Labels)
		nodeRegion := s.getNodeRegion(node.Labels, region)

		info := NodeDebugInfo{
			Name:         node.Name,
			InstanceType: instanceType,
			Region:       nodeRegion,
		}

		if instanceType == "" {
			info.Error = "no instance type label found"
			log.Printf("  Node %s: NO INSTANCE TYPE LABEL", node.Name)
		} else {
			pricing, err := s.getPricing(provider, instanceType, nodeRegion)
			if err != nil {
				info.Error = err.Error()
				log.Printf("  Node %s: instanceType=%s, region=%s, ERROR: %v", node.Name, instanceType, nodeRegion, err)
			} else {
				info.HasPricing = pricing != nil
				log.Printf("  Node %s: instanceType=%s, region=%s, price=$%.4f/hr", node.Name, instanceType, nodeRegion, pricing.OnDemandPrice)
			}
		}

		nodeInfos = append(nodeInfos, info)
	}
	log.Printf("=== END PRICING DEBUG ===")

	return &PricingDebugInfo{
		Provider:      provider,
		Region:        region,
		Nodes:         nodeInfos,
		PricingStatus: s.hybridPricing.GetStatus(),
	}, nil
}
