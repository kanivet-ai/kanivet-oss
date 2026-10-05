package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResponseInfo is what a store tells about one query beyond its answer, for
// whoever is controlling the load on it. A caller puts one in the context of a
// query (WithResponseInfo); the HTTP layer fills it from the response headers.
//
// Mimir answers with a Server-Timing header: response_time is the time the
// store itself took, queueing included, and bytes_processed the work it did.
// Neither includes the network, which on a port-forward is as large as the
// store's own time and changes with the tunnel, not with the store's load.
// Date is the store's clock, which every client of the store can read.
type ResponseInfo struct {
	mu sync.Mutex
	n  int

	// ServerTime is the store's own time for the query; zero when the store
	// does not report it.
	ServerTime time.Duration
	// Bytes is the work the store reports for the query; zero when unknown.
	Bytes int64
	// StoreDate is the response's Date header (one second resolution); zero
	// when absent. ReceivedAt is when the headers reached us.
	StoreDate  time.Time
	ReceivedAt time.Time
}

type responseInfoKey struct{}

// WithResponseInfo asks the HTTP layer to record the next response's headers
// in info.
func WithResponseInfo(ctx context.Context, info *ResponseInfo) context.Context {
	return context.WithValue(ctx, responseInfoKey{}, info)
}

// Snapshot returns what was recorded, and whether it describes exactly one
// response: a query that took several round trips has no single store time.
func (r *ResponseInfo) Snapshot() (serverTime time.Duration, bytes int64, storeDate, receivedAt time.Time, single bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ServerTime, r.Bytes, r.StoreDate, r.ReceivedAt, r.n == 1
}

// RecordResponse notes a response's timing headers for the caller that asked.
func RecordResponse(ctx context.Context, resp *http.Response) {
	info, ok := ctx.Value(responseInfoKey{}).(*ResponseInfo)
	if !ok || info == nil || resp == nil {
		return
	}
	serverTime, bytes := parseServerTiming(resp.Header.Get("Server-Timing"))
	var date time.Time
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		date = d
	}
	info.mu.Lock()
	info.n++
	info.ServerTime, info.Bytes, info.StoreDate, info.ReceivedAt = serverTime, bytes, date, time.Now()
	info.mu.Unlock()
}

// parseServerTiming reads response_time (milliseconds) and bytes_processed
// from a Server-Timing header such as
//
//	querier_wall_time;dur=10.4, response_time;dur=24.6, bytes_processed;val=42177
//
// Zero means the metric is absent or unreadable.
func parseServerTiming(header string) (serverTime time.Duration, bytes int64) {
	for _, metric := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(metric), ";")
		name := strings.TrimSpace(parts[0])
		if name != "response_time" && name != "bytes_processed" {
			continue
		}
		for _, p := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(p), "=")
			if !ok {
				continue
			}
			f, err := strconv.ParseFloat(strings.Trim(value, `"`), 64)
			if err != nil || f < 0 {
				continue
			}
			switch {
			case name == "response_time" && key == "dur":
				serverTime = time.Duration(f * float64(time.Millisecond))
			case name == "bytes_processed" && key == "val":
				bytes = int64(f)
			}
		}
	}
	return serverTime, bytes
}
