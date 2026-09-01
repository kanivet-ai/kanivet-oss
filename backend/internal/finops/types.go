package finops

import "time"

type InstancePricing struct {
	InstanceType    string    `json:"instanceType"`
	Region          string    `json:"region"`
	OnDemandPrice   float64   `json:"onDemandPrice"`
	SpotPrice       float64   `json:"spotPrice,omitempty"`
	CPUCores        int       `json:"cpuCores"`
	MemoryGB        float64   `json:"memoryGB"`
	CPUHourlyRate   float64   `json:"cpuHourlyRate"`
	MemoryHourlyRate float64  `json:"memoryHourlyRate"`
	LastUpdated     time.Time `json:"lastUpdated"`
}

type NodeCost struct {
	NodeName        string    `json:"nodeName"`
	InstanceType    string    `json:"instanceType"`
	Region          string    `json:"region"`
	Provider        string    `json:"provider"`
	HourlyCost      float64   `json:"hourlyCost"`
	DailyCost       float64   `json:"dailyCost"`
	MonthlyCost     float64   `json:"monthlyCost"`
	CPUCapacity     int64     `json:"cpuCapacity"`
	MemoryCapacity  int64     `json:"memoryCapacity"`
	CPUAllocatable  int64     `json:"cpuAllocatable"`
	MemoryAllocatable int64   `json:"memoryAllocatable"`
	CPURequested    int64     `json:"cpuRequested"`
	MemoryRequested int64     `json:"memoryRequested"`
	CPUUsed         int64     `json:"cpuUsed,omitempty"`
	MemoryUsed      int64     `json:"memoryUsed,omitempty"`
	PodCount        int       `json:"podCount"`
	IsSpot          bool      `json:"isSpot"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type PodCost struct {
	PodName         string    `json:"podName"`
	Namespace       string    `json:"namespace"`
	NodeName        string    `json:"nodeName"`
	OwnerKind       string    `json:"ownerKind,omitempty"`
	OwnerName       string    `json:"ownerName,omitempty"`
	CPURequest      int64     `json:"cpuRequest"`
	CPULimit        int64     `json:"cpuLimit"`
	MemoryRequest   int64     `json:"memoryRequest"`
	MemoryLimit     int64     `json:"memoryLimit"`
	CPUUsed         int64     `json:"cpuUsed,omitempty"`
	MemoryUsed      int64     `json:"memoryUsed,omitempty"`
	HourlyCost      float64   `json:"hourlyCost"`
	DailyCost       float64   `json:"dailyCost"`
	MonthlyCost     float64   `json:"monthlyCost"`
	CPUEfficiency   float64   `json:"cpuEfficiency"`
	MemoryEfficiency float64  `json:"memoryEfficiency"`
	OverallEfficiency float64 `json:"overallEfficiency"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type NamespaceCost struct {
	Namespace       string    `json:"namespace"`
	PodCount        int       `json:"podCount"`
	CPURequest      int64     `json:"cpuRequest"`
	CPULimit        int64     `json:"cpuLimit"`
	MemoryRequest   int64     `json:"memoryRequest"`
	MemoryLimit     int64     `json:"memoryLimit"`
	HourlyCost      float64   `json:"hourlyCost"`
	DailyCost       float64   `json:"dailyCost"`
	MonthlyCost     float64   `json:"monthlyCost"`
	CPUEfficiency   float64   `json:"cpuEfficiency"`
	MemoryEfficiency float64  `json:"memoryEfficiency"`
	OverallEfficiency float64 `json:"overallEfficiency"`
	TopWorkloads    []WorkloadCost `json:"topWorkloads,omitempty"`
}

type WorkloadCost struct {
	Kind              string         `json:"kind"`
	Name              string         `json:"name"`
	Namespace         string         `json:"namespace"`
	Replicas          int            `json:"replicas"`
	CPURequest        int64          `json:"cpuRequest"`
	CPULimit          int64          `json:"cpuLimit"`
	MemoryRequest     int64          `json:"memoryRequest"`
	MemoryLimit       int64          `json:"memoryLimit"`
	HourlyCost        float64        `json:"hourlyCost"`
	DailyCost         float64        `json:"dailyCost"`
	MonthlyCost       float64        `json:"monthlyCost"`
	CPUEfficiency     float64        `json:"cpuEfficiency"`
	MemoryEfficiency  float64        `json:"memoryEfficiency"`
	OverallEfficiency float64        `json:"overallEfficiency"`
	Pods              []PodCost      `json:"pods,omitempty"`
	HPA               *HPAInfo       `json:"hpa,omitempty"`
	VPA               *VPAInfo       `json:"vpa,omitempty"`
}

type HPAInfo struct {
	MinReplicas     int     `json:"minReplicas"`
	MaxReplicas     int     `json:"maxReplicas"`
	CurrentReplicas int     `json:"currentReplicas"`
	MinMonthlyCost  float64 `json:"minMonthlyCost"`
	MaxMonthlyCost  float64 `json:"maxMonthlyCost"`
}

type VPAInfo struct {
	TargetCPU       int64   `json:"targetCpu,omitempty"`
	TargetMemory    int64   `json:"targetMemory,omitempty"`
	HasRecommendation bool  `json:"hasRecommendation"`
	PotentialSavings float64 `json:"potentialSavings"`
	SavingsPercent   float64 `json:"savingsPercent"`
}

type ClusterCostSummary struct {
	Cluster              string        `json:"cluster"`
	Provider             string        `json:"provider"`
	Region               string        `json:"region"`
	NodeCount            int           `json:"nodeCount"`
	PodCount             int           `json:"podCount"`
	NamespaceCount       int           `json:"namespaceCount"`
	TotalCPU             int64         `json:"totalCpu"`
	TotalMemory          int64         `json:"totalMemory"`
	UsedCPU              int64         `json:"usedCpu"`
	UsedMemory           int64         `json:"usedMemory"`
	RequestedCPU         int64         `json:"requestedCpu"`
	RequestedMemory      int64         `json:"requestedMemory"`
	HourlyCost           float64       `json:"hourlyCost"`
	DailyCost            float64       `json:"dailyCost"`
	MonthlyCost          float64       `json:"monthlyCost"`
	ProjectedMonthlyCost float64       `json:"projectedMonthlyCost"`
	CPUEfficiency        float64       `json:"cpuEfficiency"`
	MemoryEfficiency     float64       `json:"memoryEfficiency"`
	OverallEfficiency    float64       `json:"overallEfficiency"`
	IdleCost             float64       `json:"idleCost"`
	IdlePercentage       float64       `json:"idlePercentage"`
	SpotSavings          float64       `json:"spotSavings,omitempty"`
	Breakdown            CostBreakdown `json:"breakdown"`
	Trend                CostTrend     `json:"trend,omitempty"`
	PricingInfo          PricingInfo   `json:"pricingInfo"`
	LastUpdated          time.Time     `json:"lastUpdated"`
}

type PricingInfo struct {
	Source            string    `json:"source"`
	LastUpdated       time.Time `json:"lastUpdated"`
	InstanceCount     int       `json:"instanceCount"`
	IsAvailable       bool      `json:"isAvailable"`
	Error             string    `json:"error,omitempty"`
	NodesWithPricing  int       `json:"nodesWithPricing"`
	NodesMissingPrice int       `json:"nodesMissingPrice"`
}

type CostBreakdown struct {
	ComputeCost     float64   `json:"computeCost"`
	CPUCost         float64   `json:"cpuCost"`
	MemoryCost      float64   `json:"memoryCost"`
	StorageCost     float64   `json:"storageCost"`
	NetworkCost     float64   `json:"networkCost"`
	ControlPlaneCost float64  `json:"controlPlaneCost"`
}

type CostTrend struct {
	Direction      string    `json:"direction"`
	ChangePercent  float64   `json:"changePercent"`
	Previous       float64   `json:"previous"`
	Current        float64   `json:"current"`
	Datapoints     []CostDatapoint `json:"datapoints,omitempty"`
}

type CostDatapoint struct {
	Timestamp time.Time `json:"timestamp"`
	Cost      float64   `json:"cost"`
}

type CostRecommendation struct {
	Type           string    `json:"type"`
	Resource       string    `json:"resource"`
	Namespace      string    `json:"namespace"`
	CurrentCost    float64   `json:"currentCost"`
	ProjectedSavings float64 `json:"projectedSavings"`
	Recommendation string    `json:"recommendation"`
	Priority       string    `json:"priority"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

type CostFilter struct {
	Namespaces     []string  `json:"namespaces,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	MinCost        float64   `json:"minCost,omitempty"`
	MaxCost        float64   `json:"maxCost,omitempty"`
	MinEfficiency  float64   `json:"minEfficiency,omitempty"`
	MaxEfficiency  float64   `json:"maxEfficiency,omitempty"`
	OwnerKinds     []string  `json:"ownerKinds,omitempty"`
	TimeRange      string    `json:"timeRange,omitempty"`
}

type Dashboard struct {
	Summary    *ClusterCostSummary `json:"summary"`
	Nodes      []NodeCost          `json:"nodes"`
	Namespaces []NamespaceCost     `json:"namespaces"`
}

type AggregationType string

const (
	AggregateByNamespace  AggregationType = "namespace"
	AggregateByDeployment AggregationType = "deployment"
	AggregateByNode       AggregationType = "node"
	AggregateByLabel      AggregationType = "label"
	AggregateByPod        AggregationType = "pod"
)

type CostAggregation struct {
	Type           AggregationType `json:"type"`
	Key            string          `json:"key"`
	HourlyCost     float64         `json:"hourlyCost"`
	DailyCost      float64         `json:"dailyCost"`
	MonthlyCost    float64         `json:"monthlyCost"`
	CPURequest     int64           `json:"cpuRequest"`
	MemoryRequest  int64           `json:"memoryRequest"`
	Efficiency     float64         `json:"efficiency"`
	ItemCount      int             `json:"itemCount"`
}
