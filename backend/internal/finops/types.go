package finops

import "time"

type InstancePricing struct {
	InstanceType     string    `json:"instanceType"`
	Region           string    `json:"region"`
	OnDemandPrice    float64   `json:"onDemandPrice"`
	SpotPrice        float64   `json:"spotPrice,omitempty"`
	CPUCores         int       `json:"cpuCores"`
	MemoryGB         float64   `json:"memoryGB"`
	CPUHourlyRate    float64   `json:"cpuHourlyRate"`
	MemoryHourlyRate float64   `json:"memoryHourlyRate"`
	LastUpdated      time.Time `json:"lastUpdated"`
}

// Scope says what a dashboard prices: a whole cluster, or one vcluster's share
// of its host.
const (
	ScopeCluster  = "cluster"
	ScopeVCluster = "vcluster"
)

// NodeCost prices one node. In vcluster scope the request, pod and allocated
// figures count only the vcluster's own pods on this (shared) host node.
type NodeCost struct {
	NodeName          string  `json:"nodeName"`
	InstanceType      string  `json:"instanceType"`
	Region            string  `json:"region"`
	Provider          string  `json:"provider"`
	HourlyCost        float64 `json:"hourlyCost"`
	DailyCost         float64 `json:"dailyCost"`
	MonthlyCost       float64 `json:"monthlyCost"`
	PriceMissing      bool    `json:"priceMissing,omitempty"`
	CPUCapacity       int64   `json:"cpuCapacity"`
	MemoryCapacity    int64   `json:"memoryCapacity"`
	CPUAllocatable    int64   `json:"cpuAllocatable"`
	MemoryAllocatable int64   `json:"memoryAllocatable"`
	CPURequested      int64   `json:"cpuRequested"`
	MemoryRequested   int64   `json:"memoryRequested"`
	PodCount          int     `json:"podCount"`
	IsSpot            bool    `json:"isSpot"`
	// SpotAtOnDemand marks a spot node priced at on-demand list price because
	// no spot rate is known: its cost is an upper bound.
	SpotAtOnDemand bool `json:"spotAtOnDemand,omitempty"`
	// AllocatedMonthlyCost is the part of MonthlyCost claimed by pod requests
	// (or usage, when higher). The rest of the node is idle.
	AllocatedMonthlyCost float64 `json:"allocatedMonthlyCost"`
}

// Efficiency fields on pods, workloads and namespaces are usage / request, in
// percent, and only set when HasUsage is true (metrics-server answered).
type PodCost struct {
	PodName           string    `json:"podName"`
	Namespace         string    `json:"namespace"`
	NodeName          string    `json:"nodeName"`
	OwnerKind         string    `json:"ownerKind,omitempty"`
	OwnerName         string    `json:"ownerName,omitempty"`
	CPURequest        int64     `json:"cpuRequest"`
	CPULimit          int64     `json:"cpuLimit"`
	MemoryRequest     int64     `json:"memoryRequest"`
	MemoryLimit       int64     `json:"memoryLimit"`
	CPUUsed           int64     `json:"cpuUsed,omitempty"`
	MemoryUsed        int64     `json:"memoryUsed,omitempty"`
	HasUsage          bool      `json:"hasUsage,omitempty"`
	HourlyCost        float64   `json:"hourlyCost"`
	DailyCost         float64   `json:"dailyCost"`
	MonthlyCost       float64   `json:"monthlyCost"`
	CPUEfficiency     float64   `json:"cpuEfficiency"`
	MemoryEfficiency  float64   `json:"memoryEfficiency"`
	OverallEfficiency float64   `json:"overallEfficiency"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	// VClusterNamespace is set on host pods synced from a vcluster: the
	// namespace the pod lives in inside the vcluster.
	VClusterNamespace string `json:"vclusterNamespace,omitempty"`
}

type NamespaceCost struct {
	Namespace         string         `json:"namespace"`
	PodCount          int            `json:"podCount"`
	CPURequest        int64          `json:"cpuRequest"`
	CPULimit          int64          `json:"cpuLimit"`
	MemoryRequest     int64          `json:"memoryRequest"`
	MemoryLimit       int64          `json:"memoryLimit"`
	CPUUsed           int64          `json:"cpuUsed,omitempty"`
	MemoryUsed        int64          `json:"memoryUsed,omitempty"`
	HasUsage          bool           `json:"hasUsage,omitempty"`
	HourlyCost        float64        `json:"hourlyCost"`
	DailyCost         float64        `json:"dailyCost"`
	MonthlyCost       float64        `json:"monthlyCost"`
	CPUEfficiency     float64        `json:"cpuEfficiency"`
	MemoryEfficiency  float64        `json:"memoryEfficiency"`
	OverallEfficiency float64        `json:"overallEfficiency"`
	TopWorkloads      []WorkloadCost `json:"topWorkloads,omitempty"`
	// VCluster names the vcluster running in this host namespace, if any.
	VCluster string `json:"vcluster,omitempty"`
}

type WorkloadCost struct {
	Kind              string    `json:"kind"`
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	VClusterNamespace string    `json:"vclusterNamespace,omitempty"`
	Replicas          int       `json:"replicas"`
	CPURequest        int64     `json:"cpuRequest"`
	CPULimit          int64     `json:"cpuLimit"`
	MemoryRequest     int64     `json:"memoryRequest"`
	MemoryLimit       int64     `json:"memoryLimit"`
	CPUUsed           int64     `json:"cpuUsed,omitempty"`
	MemoryUsed        int64     `json:"memoryUsed,omitempty"`
	HasUsage          bool      `json:"hasUsage,omitempty"`
	HourlyCost        float64   `json:"hourlyCost"`
	DailyCost         float64   `json:"dailyCost"`
	MonthlyCost       float64   `json:"monthlyCost"`
	CPUEfficiency     float64   `json:"cpuEfficiency"`
	MemoryEfficiency  float64   `json:"memoryEfficiency"`
	OverallEfficiency float64   `json:"overallEfficiency"`
	Pods              []PodCost `json:"pods,omitempty"`
	HPA               *HPAInfo  `json:"hpa,omitempty"`
}

type HPAInfo struct {
	MinReplicas     int     `json:"minReplicas"`
	MaxReplicas     int     `json:"maxReplicas"`
	CurrentReplicas int     `json:"currentReplicas"`
	MinMonthlyCost  float64 `json:"minMonthlyCost"`
	MaxMonthlyCost  float64 `json:"maxMonthlyCost"`
}

// ClusterCostSummary totals a dashboard. In cluster scope CPUEfficiency and
// MemoryEfficiency are allocation: requests / allocatable across all nodes. In
// vcluster scope allocation means nothing (the nodes are shared), so they are
// zero and the VCluster block carries the vcluster's share of the host.
type ClusterCostSummary struct {
	Cluster       string `json:"cluster"`
	Scope         string `json:"scope"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
	NodeCount     int    `json:"nodeCount"`
	SpotNodeCount int    `json:"spotNodeCount"`
	// SpotAtOnDemandCount spot nodes are priced at on-demand list price, and
	// SpotAtOnDemandCost is their monthly total at that price.
	SpotAtOnDemandCount int     `json:"spotAtOnDemandCount"`
	SpotAtOnDemandCost  float64 `json:"spotAtOnDemandCost"`
	PodCount            int     `json:"podCount"`
	NamespaceCount      int     `json:"namespaceCount"`

	TotalCPU        int64 `json:"totalCpu"`
	TotalMemory     int64 `json:"totalMemory"`
	RequestedCPU    int64 `json:"requestedCpu"`
	RequestedMemory int64 `json:"requestedMemory"`
	UsedCPU         int64 `json:"usedCpu"`
	UsedMemory      int64 `json:"usedMemory"`
	// UsageAvailable is true when metrics-server reported usage for the pods;
	// every usage and rightsizing figure is zero otherwise.
	UsageAvailable bool `json:"usageAvailable"`

	HourlyCost  float64 `json:"hourlyCost"`
	DailyCost   float64 `json:"dailyCost"`
	MonthlyCost float64 `json:"monthlyCost"`
	// AllocatedCost is the monthly cost of what pods request (or use, when higher).
	AllocatedCost  float64 `json:"allocatedCost"`
	IdleCost       float64 `json:"idleCost"`
	IdlePercentage float64 `json:"idlePercentage"`

	CPUEfficiency     float64 `json:"cpuEfficiency"`
	MemoryEfficiency  float64 `json:"memoryEfficiency"`
	OverallEfficiency float64 `json:"overallEfficiency"`

	Breakdown   CostBreakdown `json:"breakdown"`
	PricingInfo PricingInfo   `json:"pricingInfo"`
	VCluster    *VClusterCost `json:"vcluster,omitempty"`
	LastUpdated time.Time     `json:"lastUpdated"`
}

// VClusterCost puts a vcluster's spend in the context of its host cluster.
type VClusterCost struct {
	Host      string `json:"host"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// HostMonthlyCost is the whole host cluster's monthly cost; SharePercent
	// is this vcluster's MonthlyCost as a share of it.
	HostMonthlyCost float64 `json:"hostMonthlyCost"`
	SharePercent    float64 `json:"sharePercent"`
	// ControlPlane is the vcluster's own syncer/API server and etcd pods,
	// which run on the host outside any virtual namespace.
	ControlPlane []WorkloadCost `json:"controlPlane,omitempty"`
}

type PricingInfo struct {
	Source            string    `json:"source"`
	LastUpdated       time.Time `json:"lastUpdated"`
	InstanceCount     int       `json:"instanceCount"`
	IsAvailable       bool      `json:"isAvailable"`
	Error             string    `json:"error,omitempty"`
	NodesWithPricing  int       `json:"nodesWithPricing"`
	NodesMissingPrice int       `json:"nodesMissingPrice"`
	// Supported is false when the provider has no price source at all (GKE,
	// on-prem, kind): costs stay at zero rather than filling in later.
	Supported bool `json:"supported"`
}

type CostBreakdown struct {
	ComputeCost      float64 `json:"computeCost"`
	CPUCost          float64 `json:"cpuCost"`
	MemoryCost       float64 `json:"memoryCost"`
	ControlPlaneCost float64 `json:"controlPlaneCost"`
}

type CostRecommendation struct {
	Type      string `json:"type"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// VClusterNamespace is set when a host-side recommendation targets a
	// workload running inside a vcluster.
	VClusterNamespace string  `json:"vclusterNamespace,omitempty"`
	CurrentCost       float64 `json:"currentCost"`
	ProjectedSavings  float64 `json:"projectedSavings"`
	Recommendation    string  `json:"recommendation"`
	Priority          string  `json:"priority"`
}

type Dashboard struct {
	Summary         *ClusterCostSummary  `json:"summary"`
	Nodes           []NodeCost           `json:"nodes"`
	Namespaces      []NamespaceCost      `json:"namespaces"`
	Recommendations []CostRecommendation `json:"recommendations"`

	rates map[string]nodeRate
}

// Rates is what one node charges per core-hour and per GiB-hour.
type Rates struct {
	CPU    float64
	Memory float64
	Priced bool
}

// WorkloadRef is the workload a pod belongs to, as FinOps groups pods.
type WorkloadRef struct {
	Namespace, Kind, Name       string
	VCluster, VClusterNamespace string
}
