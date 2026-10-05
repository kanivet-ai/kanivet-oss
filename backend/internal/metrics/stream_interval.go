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
	interval := max(time.Duration(requestedSeconds)*time.Second, chartStep(chartRange(timeRange)))
	return min(max(interval, minStreamInterval), maxStreamInterval)
}
