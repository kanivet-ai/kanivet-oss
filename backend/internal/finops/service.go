package finops

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/cache"
	"github.com/kanivet/backend/internal/k8s"
	"github.com/kanivet/backend/internal/k8s/podcache"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	dashboardTTL = 60 * time.Second
	// usageRetryAfter spaces out retries against a metrics-server that failed.
	usageRetryAfter = 5 * time.Minute
	// computeTimeout bounds one dashboard computation. It runs detached from
	// the request that started it, since other requests may be waiting on it.
	computeTimeout = 2 * time.Minute
	hpaTimeout     = 5 * time.Second
)

var activePodsSelector = "status.phase!=Succeeded,status.phase!=Failed"

// pricingSource is the slice of HybridPricingProvider the service uses.
type pricingSource interface {
	GetStatus(provider string) PricingStatus
	GetInstancePricingCached(provider, instanceType, region string) (*InstancePricing, error)
	EnsureRegions(provider string, regions []string, onLoaded func()) bool
}

type Service struct {
	k8s     k8s.Interface
	cache   *cache.Cache
	pricing pricingSource
	// pods, when set, serves pods from a shared watch instead of listing
	// every pod on each computation.
	pods podcache.Lister
	now  func() time.Time
}

// SetPodLister makes the service read pods from a shared pod cache.
func (s *Service) SetPodLister(l podcache.Lister) { s.pods = l }

// listPods returns the cluster's running and pending pods, optionally in one
// namespace.
func (s *Service) listPods(ctx context.Context, cs kubernetes.Interface, cluster, namespace string) ([]v1.Pod, error) {
	if s.pods == nil {
		l, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{FieldSelector: activePodsSelector})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	}
	cached, err := s.pods.List(ctx, cluster)
	if err != nil {
		return nil, err
	}
	out := make([]v1.Pod, 0, len(cached))
	for _, p := range cached {
		if namespace != "" && p.Namespace != namespace {
			continue
		}
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func NewService(k8sClient k8s.Interface, cacheInstance *cache.Cache) *Service {
	return &Service{
		k8s:     k8sClient,
		cache:   cacheInstance,
		pricing: NewHybridPricingProvider(&CURConfig{}),
		now:     time.Now,
	}
}

func dashboardKey(cluster string) string { return "finops:dashboard:" + cluster }

// GetDashboard returns the cluster's cost dashboard, computing it at most once
// per dashboardTTL. Every other endpoint is a view of it.
func (s *Service) GetDashboard(ctx context.Context, cluster string) (*Dashboard, error) {
	data, err := s.cache.GetOrSet(dashboardKey(cluster), dashboardTTL, func() (interface{}, error) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		return s.calculateDashboard(cctx, cluster)
	})
	if err != nil {
		return nil, err
	}
	return data.(*Dashboard), nil
}

// Invalidate drops a cluster's cached dashboard so the next read recomputes it.
func (s *Service) Invalidate(cluster string) {
	s.cache.Delete(dashboardKey(cluster))
}

func (s *Service) StreamDashboard(ctx context.Context, cluster string, send func(string, interface{})) error {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return err
	}
	send("summary", d.Summary)
	send("nodes", d.Nodes)
	send("namespaces", d.Namespaces)
	send("recommendations", d.Recommendations)
	return nil
}

func (s *Service) calculateDashboard(ctx context.Context, cluster string) (*Dashboard, error) {
	start := time.Now()
	defer func() {
		log.Printf("[FinOps] dashboard %s calculated in %s", cluster, time.Since(start).Round(time.Millisecond))
	}()
	if host, namespace, name, ok := k8s.ParseVClusterID(cluster); ok {
		return s.calculateVClusterDashboard(ctx, cluster, host, namespace, name)
	}

	cs, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	var (
		nodes      []v1.Node
		pods       []v1.Pod
		namespaces int
		hpas       []autoscalingv2.HorizontalPodAutoscaler
		usage      map[string]podUsage
	)
	err = parallel(
		func() error {
			l, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("list nodes: %w", err)
			}
			nodes = l.Items
			return nil
		},
		func() error {
			l, err := s.listPods(ctx, cs, cluster, "")
			if err != nil {
				return fmt.Errorf("list pods: %w", err)
			}
			pods = l
			return nil
		},
		func() error {
			if l, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
				namespaces = len(l.Items)
			}
			return nil
		},
		func() error {
			hpas = listHPAs(ctx, cs)
			return nil
		},
		func() error {
			usage = s.podUsage(ctx, cluster, "")
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	if namespaces == 0 {
		namespaces = distinctNamespaces(pods)
	}

	provider := detectProvider(nodes)
	s.ensurePricing(provider, nodes)
	return computeDashboard(computeInput{
		cluster:        cluster,
		provider:       provider,
		nodes:          nodes,
		pods:           pods,
		namespaceCount: namespaces,
		price:          s.priceFunc(provider),
		usage:          usage,
		hpas:           hpas,
		pricingStatus:  s.pricing.GetStatus(provider),
		now:            s.now(),
	}), nil
}

// calculateVClusterDashboard prices a vcluster from its host: every pod the
// vcluster runs is a real pod in its host namespace, scheduled on a real,
// priced host node. The vcluster's own control plane runs there too.
func (s *Service) calculateVClusterDashboard(ctx context.Context, cluster, host, namespace, name string) (*Dashboard, error) {
	hostCS, err := s.k8s.GetClientForCluster(host)
	if err != nil {
		return nil, fmt.Errorf("failed to get host client: %w", err)
	}

	var (
		nodes    []v1.Node
		hostPods []v1.Pod
		hpas     []autoscalingv2.HorizontalPodAutoscaler
		usage    map[string]podUsage
	)
	err = parallel(
		func() error {
			l, err := hostCS.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("list host nodes: %w", err)
			}
			nodes = l.Items
			return nil
		},
		func() error {
			l, err := s.listPods(ctx, hostCS, host, namespace)
			if err != nil {
				return fmt.Errorf("list host pods in %s: %w", namespace, err)
			}
			hostPods = l
			return nil
		},
		func() error {
			hpas = s.vclusterHPAs(ctx, cluster)
			return nil
		},
		func() error {
			usage = s.podUsage(ctx, host, namespace)
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	workloadPods, controlPlanePods := splitVClusterPods(hostPods, name)
	provider := detectProvider(nodes)
	s.ensurePricing(provider, nodes)
	price := s.priceFunc(provider)

	hostRates := priceNodes(computeInput{nodes: nodes, price: price})
	hostHourly := 0.0
	anyPriced := false
	for _, r := range hostRates {
		hostHourly += r.hourly
		anyPriced = anyPriced || r.priced
	}
	if anyPriced {
		hostHourly += controlPlaneHourly(provider)
	}

	return computeDashboard(computeInput{
		cluster:          cluster,
		provider:         provider,
		nodes:            nodes,
		pods:             workloadPods,
		controlPlanePods: controlPlanePods,
		price:            price,
		usage:            usage,
		hpas:             hpas,
		vcluster: &vclusterScope{
			host:            host,
			namespace:       namespace,
			name:            name,
			hostMonthlyCost: hostHourly * hoursPerMonth,
		},
		pricingStatus: s.pricing.GetStatus(provider),
		now:           s.now(),
	}), nil
}

// splitVClusterPods separates the pods a vcluster synced to its host namespace
// from the vcluster's own control plane pods. Pods synced by another vcluster
// sharing the namespace belong to neither.
func splitVClusterPods(hostPods []v1.Pod, vcluster string) (workloads, controlPlane []v1.Pod) {
	for i := range hostPods {
		p := hostPods[i]
		managedBy, synced := p.Labels[k8s.VClusterManagedByLabel]
		switch {
		case synced && managedBy == vcluster:
			workloads = append(workloads, p)
		case !synced:
			controlPlane = append(controlPlane, p)
		}
	}
	return workloads, controlPlane
}

func (s *Service) priceFunc(provider string) priceFunc {
	return func(instanceType, region string) (*InstancePricing, error) {
		return s.pricing.GetInstancePricingCached(provider, instanceType, region)
	}
}

// ensurePricing never blocks a request on a cold offer-file download: costs
// render as missing and the finops cache is invalidated once pricing lands so
// the next poll fills them in.
func (s *Service) ensurePricing(provider string, nodes []v1.Node) {
	s.pricing.EnsureRegions(provider, uniqueRegions(nodes), func() {
		s.cache.Invalidate("finops:*")
	})
}

// podUsage returns metrics-server pod usage, or nil when it is unavailable.
// Failures are remembered so a broken metrics API is not retried every poll.
func (s *Service) podUsage(ctx context.Context, cluster, namespace string) map[string]podUsage {
	failKey := "finops:usage-unavailable:" + cluster
	if _, failed := s.cache.Get(failKey); failed {
		return nil
	}
	dyn, err := s.k8s.GetDynamicClient(cluster)
	if err != nil {
		return nil
	}
	usage, err := fetchPodUsage(ctx, dyn, namespace)
	if err != nil {
		log.Printf("[FinOps] pod usage unavailable for %s: %v", cluster, err)
		s.cache.Set(failKey, true, usageRetryAfter)
		return nil
	}
	return usage
}

// vclusterHPAs asks the vcluster itself, since HPAs are not synced to the
// host. Reaching the vcluster can mean waiting on its port-forward, so the
// whole lookup is bounded and skipped when slow.
func (s *Service) vclusterHPAs(ctx context.Context, cluster string) []autoscalingv2.HorizontalPodAutoscaler {
	ctx, cancel := context.WithTimeout(ctx, hpaTimeout)
	defer cancel()
	result := make(chan []autoscalingv2.HorizontalPodAutoscaler, 1)
	go func() {
		vcs, err := s.k8s.GetClientForCluster(cluster)
		if err != nil {
			result <- nil
			return
		}
		result <- listHPAs(ctx, vcs)
	}()
	select {
	case h := <-result:
		return h
	case <-ctx.Done():
		return nil
	}
}

func listHPAs(ctx context.Context, cs kubernetes.Interface) []autoscalingv2.HorizontalPodAutoscaler {
	ctx, cancel := context.WithTimeout(ctx, hpaTimeout)
	defer cancel()
	l, err := cs.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return l.Items
}

func distinctNamespaces(pods []v1.Pod) int {
	set := map[string]struct{}{}
	for i := range pods {
		set[pods[i].Namespace] = struct{}{}
	}
	return len(set)
}

// parallel runs fns concurrently and returns the first error.
func parallel(fns ...func() error) error {
	var wg sync.WaitGroup
	errs := make([]error, len(fns))
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn()
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetClusterCostSummary(ctx context.Context, cluster string) (*ClusterCostSummary, error) {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return d.Summary, nil
}

func (s *Service) GetNodeCosts(ctx context.Context, cluster string) ([]NodeCost, error) {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return d.Nodes, nil
}

func (s *Service) GetNamespaceCosts(ctx context.Context, cluster string) ([]NamespaceCost, error) {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return d.Namespaces, nil
}

// GetWorkloadCosts lists workloads, optionally in one namespace.
func (s *Service) GetWorkloadCosts(ctx context.Context, cluster, namespace string) ([]WorkloadCost, error) {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return nil, err
	}
	out := []WorkloadCost{}
	for _, ns := range d.Namespaces {
		if namespace == "" || ns.Namespace == namespace {
			out = append(out, ns.TopWorkloads...)
		}
	}
	return out, nil
}

// GetPodCosts lists pods, optionally in one namespace.
func (s *Service) GetPodCosts(ctx context.Context, cluster, namespace string) ([]PodCost, error) {
	workloads, err := s.GetWorkloadCosts(ctx, cluster, namespace)
	if err != nil {
		return nil, err
	}
	out := []PodCost{}
	for _, w := range workloads {
		out = append(out, w.Pods...)
	}
	return out, nil
}

func (s *Service) GetCostRecommendations(ctx context.Context, cluster string) ([]CostRecommendation, error) {
	d, err := s.GetDashboard(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return d.Recommendations, nil
}

// GetPricingStatus reports the price source for a cluster's cloud, or for AWS
// when no cluster is given.
func (s *Service) GetPricingStatus(ctx context.Context, cluster string) PricingStatus {
	provider := ProviderAWSEKS
	if cluster != "" {
		if nodes, err := s.pricingNodes(ctx, cluster); err == nil {
			provider = detectProvider(nodes)
		}
	}
	return s.pricing.GetStatus(provider)
}

// PreloadPricing starts loading prices for a cluster's regions so opening the
// dashboard is fast.
func (s *Service) PreloadPricing(ctx context.Context, cluster string) error {
	nodes, err := s.pricingNodes(ctx, cluster)
	if err != nil || len(nodes) == 0 {
		return err
	}
	s.ensurePricing(detectProvider(nodes), nodes)
	return nil
}

// pricingNodes lists the nodes whose prices a cluster's dashboard needs: the
// host's nodes for a vcluster.
func (s *Service) pricingNodes(ctx context.Context, cluster string) ([]v1.Node, error) {
	if host, _, _, ok := k8s.ParseVClusterID(cluster); ok {
		cluster = host
	}
	cs, err := s.k8s.GetClientForCluster(cluster)
	if err != nil {
		return nil, err
	}
	l, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

type PricingDebugInfo struct {
	Provider      string          `json:"provider"`
	Region        string          `json:"region"`
	Nodes         []NodeDebugInfo `json:"nodes"`
	PricingStatus PricingStatus   `json:"pricingStatus"`
}

type NodeDebugInfo struct {
	Name         string `json:"name"`
	InstanceType string `json:"instanceType"`
	Region       string `json:"region"`
	HasPricing   bool   `json:"hasPricing"`
	Error        string `json:"error,omitempty"`
}

func (s *Service) GetPricingDebug(ctx context.Context, cluster string) (*PricingDebugInfo, error) {
	nodes, err := s.pricingNodes(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	provider := detectProvider(nodes)
	s.ensurePricing(provider, nodes)
	fallback := dominantRegion(nodes)

	infos := make([]NodeDebugInfo, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		it, region := instanceType(n.Labels), nodeRegion(n.Labels)
		if region == "" {
			region = fallback
		}
		info := NodeDebugInfo{Name: n.Name, InstanceType: it, Region: region}
		if it == "" {
			info.Error = "no instance type label found"
		} else if _, err := s.pricing.GetInstancePricingCached(provider, it, region); err != nil {
			info.Error = err.Error()
		} else {
			info.HasPricing = true
		}
		infos = append(infos, info)
	}
	return &PricingDebugInfo{
		Provider:      provider,
		Region:        fallback,
		Nodes:         infos,
		PricingStatus: s.pricing.GetStatus(provider),
	}, nil
}
