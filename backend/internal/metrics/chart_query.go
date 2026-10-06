package metrics

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// chartQueryTimeout bounds one chart query, cached tail refresh included.
const chartQueryTimeout = 10 * time.Second

// chartPoints is roughly how many points a chart draws. The step is the
// first of chartSteps that keeps a range at or under it, so the newest point
// is never more than one small step old and a burst shorter than the step
// is not averaged away: 1h draws at 15s, 6h at 2m, 24h at 10m.
const chartPoints = 240

var chartSteps = []time.Duration{
	15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
	10 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour,
}

// minRateWindow is the shortest rate() window: it must hold several scrapes
// even at a 15s step.
const minRateWindow = 5 * time.Minute

// chartRange turns a UI time range ("15m", "6h", "3d", "180m") into a
// duration; anything unreadable falls back to 15 minutes.
func chartRange(timeRange string) time.Duration {
	switch timeRange {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	}
	if len(timeRange) > 1 {
		unit := timeRange[len(timeRange)-1]
		if value, err := strconv.Atoi(timeRange[:len(timeRange)-1]); err == nil && value > 0 {
			switch unit {
			case 'm':
				return time.Duration(value) * time.Minute
			case 'h':
				return time.Duration(value) * time.Hour
			case 'd':
				return time.Duration(value) * 24 * time.Hour
			}
		}
	}
	return 15 * time.Minute
}

// chartStep picks the step for a range; past chartSteps it rounds up to
// whole hours.
func chartStep(window time.Duration) time.Duration {
	target := window / chartPoints
	for _, step := range chartSteps {
		if step >= target {
			return step
		}
	}
	return (target + time.Hour - 1) / time.Hour * time.Hour
}

// chartWindow resolves a chart's range and step. stepText, when set, is a Go
// duration or whole seconds.
func chartWindow(timeRange, stepText string) (time.Duration, time.Duration, error) {
	window := chartRange(timeRange)
	if stepText == "" {
		return window, chartStep(window), nil
	}
	step, err := time.ParseDuration(stepText)
	if err != nil {
		secs, e := strconv.ParseInt(stepText, 10, 64)
		if e != nil {
			return 0, 0, fmt.Errorf("invalid metrics step %q", stepText)
		}
		step = time.Duration(secs) * time.Second
	}
	if step < time.Second || step%time.Second != 0 {
		return 0, 0, fmt.Errorf("metrics step must be a positive whole number of seconds")
	}
	return window, step, nil
}

// promQuote writes s as a PromQL string literal, whose escapes are Go's.
func promQuote(s string) string {
	return strconv.Quote(s)
}

func promDuration(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10) + "s"
}

// peakSampled reports whether a chart's memory values are the highest sample
// in each step rather than the one at its instant. Above a minute a point
// sample misses most of what happened between points, including the spike
// that got a pod OOM-killed; rightsizing reads peaks the same way.
func peakSampled(metricType string, step time.Duration) bool {
	return metricType == "memory" && step > time.Minute
}

// chartPromQL builds the query behind a pod, container or node chart. Both
// Prometheus-compatible providers speak the same PromQL, so they share it.
// Every query sums to one series: a pod with several network interfaces, or a
// container that restarted, is one line, not whichever series came first.
func chartPromQL(q MetricQuery, step time.Duration) string {
	var targets []string
	if q.NodeName != "" {
		// Kubernetes service discovery with role=node (the community chart,
		// Kanivet's own install) puts the node name in instance.
		// prometheus-operator scrapes the kubelet through Endpoints: instance
		// is <ip>:10250 there and the name is in node.
		targets = []string{"node=" + promQuote(q.NodeName), "instance=" + promQuote(q.NodeName)}
	} else {
		targets = []string{"namespace=" + promQuote(q.Namespace) + ",pod=" + promQuote(q.PodName)}
	}
	return chartExpr(q.MetricType, targets, q.ContainerName, step, "")
}

// workloadPromQL charts several pods of one namespace in one query, one
// series per pod.
func workloadPromQL(namespace string, pods []string, metricType string, step time.Duration) string {
	alternatives := make([]string, len(pods))
	for i, pod := range pods {
		alternatives[i] = regexp.QuoteMeta(pod)
	}
	target := "namespace=" + promQuote(namespace) + ",pod=~" + promQuote(strings.Join(alternatives, "|"))
	return chartExpr(metricType, []string{target}, "", step, "pod")
}

// chartExpr sums metricType over each target's matchers and takes the first
// target (and, for memory, the first metric) that has data at each step: the
// legs of an "or" all aggregate to the same labels, so PromQL keeps the
// first leg's sample wherever there is one.
//
// CPU, memory and disk count containers only. cAdvisor also reports the pod's
// own cgroup (container="", already the sum of its containers) and, under
// dockershim, the sandbox (container="POD"); summing those as well doubled
// the figure. Network is only reported for the pod's sandbox, so it is never
// filtered by container.
func chartExpr(metricType string, targets []string, container string, step time.Duration, by string) string {
	containers := `container!="",container!="POD"`
	if container != "" {
		containers = "container=" + promQuote(container)
	}
	sum := "sum"
	if by != "" {
		sum = "sum by (" + by + ")"
	}
	window := promDuration(max(minRateWindow, step))
	legs := func(metrics []string, sample func(metric, matchers string) string, filtered bool) string {
		var out []string
		for _, metric := range metrics {
			for _, target := range targets {
				matchers := target
				if filtered {
					matchers += "," + containers
				}
				out = append(out, sum+" ("+sample(metric, matchers)+")")
			}
		}
		if len(out) == 1 {
			return out[0]
		}
		return "(" + strings.Join(out, " or ") + ")"
	}
	rate := func(metric, matchers string) string {
		return "rate(" + metric + "{" + matchers + "}[" + window + "])"
	}
	switch metricType {
	case "cpu":
		return legs([]string{"container_cpu_usage_seconds_total"}, rate, true) + " * 1000"
	case "memory":
		gauge := func(metric, matchers string) string {
			if peakSampled(metricType, step) {
				return "max_over_time(" + metric + "{" + matchers + "}[" + promDuration(step) + "])"
			}
			return metric + "{" + matchers + "}"
		}
		// Working set is what the kubelet evicts on and what metrics-server
		// and rightsizing report; total usage also counts reclaimable page
		// cache and is only the fallback for collectors that drop the former.
		return legs([]string{"container_memory_working_set_bytes", "container_memory_usage_bytes"}, gauge, true)
	case "network_rx":
		return legs([]string{"container_network_receive_bytes_total"}, rate, false) + " / 1024"
	case "network_tx":
		return legs([]string{"container_network_transmit_bytes_total"}, rate, false) + " / 1024"
	case "disk_read":
		return legs([]string{"container_fs_reads_bytes_total"}, rate, true) + " / 1024"
	case "disk_write":
		return legs([]string{"container_fs_writes_bytes_total"}, rate, true) + " / 1024"
	default:
		return legs([]string{"up"}, func(metric, matchers string) string { return metric + "{" + matchers + "}" }, false)
	}
}

func metricUnit(metricType string) string {
	switch metricType {
	case "cpu":
		return "millicores"
	case "memory":
		return "bytes"
	case "network_rx", "network_tx", "disk_read", "disk_write":
		return "KB/s"
	default:
		return ""
	}
}

// Samples are a chart series' values. A step the store had no sample for is
// NaN, sent as null so the chart draws a gap there instead of joining the
// neighbours (JSON has no NaN).
type Samples []float64

func (s Samples) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 2+len(s)*8)
	b = append(b, '[')
	for i, v := range s {
		if i > 0 {
			b = append(b, ',')
		}
		switch {
		case math.IsNaN(v) || math.IsInf(v, 0):
			b = append(b, "null"...)
		case v != 0 && (math.Abs(v) < 1e-6 || math.Abs(v) >= 1e21):
			b = strconv.AppendFloat(b, v, 'e', -1, 64)
		default:
			b = strconv.AppendFloat(b, v, 'f', -1, 64)
		}
	}
	return append(b, ']'), nil
}

func (s *Samples) UnmarshalJSON(b []byte) error {
	var raw []*float64
	if err := jsonv2.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(Samples, len(raw))
	for i, v := range raw {
		if v == nil {
			out[i] = math.NaN()
		} else {
			out[i] = *v
		}
	}
	*s = out
	return nil
}

// chartSource is a provider whose charts come from a Prometheus-compatible
// range query.
type chartSource interface {
	chartGet(ctx context.Context, cluster string, params url.Values) (io.ReadCloser, error)
}

type chartSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"`
}

// chartGrid is the step grid of one range query: every series of the query
// is laid out on it, so they line up with each other and with the time axis.
type chartGrid struct {
	start, step int64
	n           int
}

func newChartGrid(params url.Values) chartGrid {
	start, _ := strconv.ParseInt(params.Get("start"), 10, 64)
	end, _ := strconv.ParseInt(params.Get("end"), 10, 64)
	step, _ := strconv.ParseInt(params.Get("step"), 10, 64)
	if step <= 0 || end < start {
		return chartGrid{start: start, step: max(step, 1)}
	}
	return chartGrid{start: start, step: step, n: int((end-start)/step) + 1}
}

func (g chartGrid) timestamps() []int64 {
	out := make([]int64, g.n)
	for i := range out {
		out[i] = g.start + int64(i)*g.step
	}
	return out
}

// place lays samples out on the grid; ok is false when none of them is a
// number.
func (g chartGrid) place(values [][]any) (Samples, bool) {
	out := make(Samples, g.n)
	for i := range out {
		out[i] = math.NaN()
	}
	ok := false
	for _, sample := range values {
		if len(sample) < 2 {
			continue
		}
		at, isNum := sample[0].(float64)
		if !isNum {
			continue
		}
		offset := int64(math.Round(at)) - g.start
		if offset < 0 || offset%g.step != 0 || offset/g.step >= int64(g.n) {
			continue
		}
		var v float64
		switch raw := sample[1].(type) {
		case string:
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			v = parsed
		case float64:
			v = raw
		default:
			continue
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out[offset/g.step] = v
		ok = true
	}
	return out, ok
}

// clockLabels are the grid's instants as local clock times, for clients
// that predate Timestamps.
func clockLabels(timestamps []int64) []string {
	out := make([]string, len(timestamps))
	for i, ts := range timestamps {
		out[i] = time.Unix(ts, 0).Format("15:04:05")
	}
	return out
}

// fetchChartSeries runs a chart's range query and returns its series.
func fetchChartSeries(ctx context.Context, src chartSource, store, cluster string, params url.Values) ([]chartSeries, error) {
	ctx, cancel := context.WithTimeout(ctx, chartQueryTimeout)
	defer cancel()
	body, err := src.chartGet(ctx, cluster, params)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	var answer struct {
		Status string `json:"status"`
		Data   struct {
			Result []chartSeries `json:"result"`
		} `json:"data"`
		ErrorType string `json:"errorType,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	if err := jsonv2.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("%s returned an unexpected response: %w", store, err)
	}
	if answer.Status != "success" {
		return nil, &QueryError{Type: answer.ErrorType, Message: answer.Error}
	}
	return answer.Data.Result, nil
}

// emptyChart is a chart with no samples.
func emptyChart(metricType string) *MetricResponse {
	return &MetricResponse{Labels: []string{}, Values: Samples{}, Unit: metricUnit(metricType)}
}

// queryChart answers a pod, container or node chart.
func queryChart(ctx context.Context, src chartSource, store, cluster string, q MetricQuery) (*MetricResponse, error) {
	window, step, err := chartWindow(q.TimeRange, q.Step)
	if err != nil {
		return nil, err
	}
	params := chartQueryParams(chartPromQL(q, step), window, step)
	series, err := fetchChartSeries(ctx, src, store, cluster, params)
	if err != nil {
		return nil, err
	}
	response := emptyChart(q.MetricType)
	if len(series) == 0 {
		return response, nil
	}
	// The query sums to one series.
	grid := newChartGrid(params)
	values, ok := grid.place(series[0].Values)
	if !ok {
		return response, nil
	}
	response.Timestamps = grid.timestamps()
	response.Labels = clockLabels(response.Timestamps)
	response.Values = values
	response.Step = grid.step
	response.Peak = peakSampled(q.MetricType, step)
	return response, nil
}

// queryWorkloadChart answers a chart of several pods with one query.
func queryWorkloadChart(ctx context.Context, src chartSource, store, cluster string, q WorkloadMetricQuery) (*WorkloadMetricResponse, error) {
	response := &WorkloadMetricResponse{Pods: map[string]*MetricResponse{}}
	if len(q.PodNames) == 0 {
		return response, nil
	}
	window, step, err := chartWindow(q.TimeRange, q.Step)
	if err != nil {
		return nil, err
	}
	pods := slices.Clone(q.PodNames)
	slices.Sort(pods)
	params := chartQueryParams(workloadPromQL(q.Namespace, slices.Compact(pods), q.MetricType, step), window, step)
	series, err := fetchChartSeries(ctx, src, store, cluster, params)
	if err != nil {
		return nil, err
	}
	grid := newChartGrid(params)
	unit := metricUnit(q.MetricType)
	for _, s := range series {
		pod := s.Metric["pod"]
		if pod == "" {
			continue
		}
		if values, ok := grid.place(s.Values); ok {
			response.Pods[pod] = &MetricResponse{Values: values, Unit: unit}
		}
	}
	if len(response.Pods) > 0 {
		response.Timestamps = grid.timestamps()
		response.Step = grid.step
		response.Peak = peakSampled(q.MetricType, step)
	}
	return response, nil
}
