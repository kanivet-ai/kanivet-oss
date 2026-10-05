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

// WorkloadMetricResponse contains metrics for multiple pods, all on one step
// grid: each pod's Values has one entry per Timestamps entry.
type WorkloadMetricResponse struct {
	Timestamps []int64                    `json:"timestamps,omitempty"`
	Step       int64                      `json:"step,omitempty"`
	Peak       bool                       `json:"peak,omitempty"`
	Pods       map[string]*MetricResponse `json:"pods"` // podName -> metrics
}

// MetricResponse is one chart series. Timestamps are the instants of the
// query's step grid in unix seconds, and Values has one sample per timestamp,
// NaN (null in JSON) where the store had none, so the series of one query
// line up and a gap is drawn as a gap. Labels are the same instants as clock
// times, kept for older clients.
type MetricResponse struct {
	Labels     []string `json:"labels,omitempty"`
	Timestamps []int64  `json:"timestamps,omitempty"`
	Values     Samples  `json:"values"`
	// Step is the grid spacing in seconds.
	Step int64 `json:"step,omitempty"`
	// Peak is set when each value is the highest sample within its step
	// rather than the sample at its instant.
	Peak bool   `json:"peak,omitempty"`
	Unit string `json:"unit,omitempty"`
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
