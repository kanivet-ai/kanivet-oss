package metrics

import "time"

type MetricQuery struct {
	PodName       string `json:"podName"`
	Namespace     string `json:"namespace"`
	ContainerName string `json:"containerName,omitempty"`
	NodeName      string `json:"nodeName,omitempty"`
	MetricType    string `json:"metricType"`
	TimeRange     string `json:"timeRange"`
	Step          string `json:"step,omitempty"`
}

// WorkloadMetricQuery is for batch querying metrics for multiple pods at once
type WorkloadMetricQuery struct {
	PodNames   []string `json:"podNames"`
	Namespace  string   `json:"namespace"`
	MetricType string   `json:"metricType"`
	TimeRange  string   `json:"timeRange"`
	Step       string   `json:"step,omitempty"`
}

// WorkloadMetricResponse contains metrics for multiple pods
type WorkloadMetricResponse struct {
	Pods map[string]*MetricResponse `json:"pods"` // podName -> metrics
}

type MetricResponse struct {
	Labels []string  `json:"labels"`
	Values []float64 `json:"values"`
	Unit   string    `json:"unit,omitempty"`
}

type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type ContainerMetrics struct {
	ContainerName string                     `json:"containerName"`
	Metrics       map[string]*MetricResponse `json:"metrics"`
}

type PodMetrics struct {
	PodName    string                     `json:"podName"`
	Namespace  string                     `json:"namespace"`
	Containers []ContainerMetrics         `json:"containers"`
	PodLevel   map[string]*MetricResponse `json:"podLevel"`
}

type MetricTypes struct {
	CPU       string
	Memory    string
	NetworkRx string
	NetworkTx string
	DiskRead  string
	DiskWrite string
}

var SupportedMetricTypes = MetricTypes{
	CPU:       "cpu",
	Memory:    "memory",
	NetworkRx: "network_rx",
	NetworkTx: "network_tx",
	DiskRead:  "disk_read",
	DiskWrite: "disk_write",
}

var TimeRanges = []string{"5m", "15m", "1h", "6h", "24h"}
