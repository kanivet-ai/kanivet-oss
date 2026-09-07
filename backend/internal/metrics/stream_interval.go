package metrics

import "time"

const (
	minStreamInterval = 2 * time.Second
	maxStreamInterval = 30 * time.Second
)

// StreamInterval bounds a metrics refresh cadence by the query step: samples only
// change once per step, so polling faster than that re-fetches identical data.
func StreamInterval(timeRange string, requestedSeconds int) time.Duration {
	if requestedSeconds <= 0 {
		requestedSeconds = 2
	}
	interval := time.Duration(requestedSeconds) * time.Second
	var p PrometheusProvider
	if step, err := time.ParseDuration(p.calculateStep(p.parseTimeRange(timeRange))); err == nil && step > interval {
		interval = step
	}
	return min(max(interval, minStreamInterval), maxStreamInterval)
}
