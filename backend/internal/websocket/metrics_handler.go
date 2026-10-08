package websocket

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/metrics"
	"github.com/kanivet/backend/internal/websocket/core"
)

const (
	// metricsAttempts bounds how often one refresh asks again after a
	// transient failure (a dropped tunnel, an overloaded store) before it
	// leaves the rest to the next tick.
	metricsAttempts = 3
	// metricsMaxBackoff caps how far a stream whose store keeps failing
	// spaces its refreshes out.
	metricsMaxBackoff = time.Minute
	// metricsReportAfter is how many refreshes in a row may fail transiently
	// before the chart is told; until then it keeps what it shows.
	metricsReportAfter = 3
	// metricsSlowMessage avoids every phrase the frontend reads as "the
	// provider is unavailable": a slow refresh must not grey out every chart
	// of the cluster for a minute.
	metricsSlowMessage = "The metrics store is slow to answer; retrying"
)

// metricsRetryPause is the pause before the first retry within a refresh; a
// var so tests can shrink it.
var metricsRetryPause = 500 * time.Millisecond

// metricsQuerier is what streams read from: metrics.Service.
type metricsQuerier interface {
	QueryMetrics(ctx context.Context, cluster, providerType string, query metrics.MetricQuery) (*metrics.MetricResponse, error)
	QueryWorkloadMetrics(ctx context.Context, cluster, providerType string, query metrics.WorkloadMetricQuery) (*metrics.WorkloadMetricResponse, error)
}

// streamConn is what streams write to: a core.Connection.
type streamConn interface {
	ID() core.ConnectionID
	Send(data []byte) error
}

type MetricsStreamHandler struct {
	metricsService metricsQuerier
	streams        sync.Map // connectionID:topic -> *activeStream
}

type activeStream struct{ cancel context.CancelFunc }

type MetricsStreamRequest struct {
	Action        string   `json:"action"`
	Cluster       string   `json:"cluster"`
	Namespace     string   `json:"namespace"`
	Pod           string   `json:"pod"`
	PodNames      []string `json:"podNames,omitempty"` // For workload batch queries
	NodeName      string   `json:"nodeName,omitempty"`
	Container     string   `json:"container,omitempty"`
	MetricType    string   `json:"metricType"`
	TimeRange     string   `json:"timeRange"`
	Provider      string   `json:"provider,omitempty"`
	StreamingRate int      `json:"streamingRate,omitempty"` // seconds between updates, default 5
}

// streamPayload is one update of a stream. Stale marks an answer served from
// the chart cache while the live one is fetched; Transient marks an error the
// stream expects to recover from on its own.
type streamPayload[D any] struct {
	Type      string `json:"type"`
	Topic     string `json:"topic"`
	Timestamp int64  `json:"timestamp"`
	Data      *D     `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
	Stale     bool   `json:"stale,omitempty"`
	Transient bool   `json:"transient,omitempty"`
}

type MetricsStreamResponse = streamPayload[metrics.MetricResponse]

// WorkloadMetricsResponse for batch pod metrics
type WorkloadMetricsResponse = streamPayload[metrics.WorkloadMetricResponse]

func NewMetricsStreamHandler(metricsService *metrics.Service) *MetricsStreamHandler {
	return &MetricsStreamHandler{
		metricsService: metricsService,
	}
}

func (h *MetricsStreamHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"metrics"}
}

func (h *MetricsStreamHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	return h.Handle(ctx, conn, msg)
}

func (h *MetricsStreamHandler) Handle(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var req MetricsStreamRequest
	if err := msg.UnmarshalPayload(&req); err != nil {
		return fmt.Errorf("failed to unmarshal metrics request: %w", err)
	}
	return h.handle(ctx, conn, req)
}

func (h *MetricsStreamHandler) handle(ctx context.Context, conn streamConn, req MetricsStreamRequest) error {
	topic := h.buildTopic(req)
	streamKey := fmt.Sprintf("%s:%s", conn.ID(), topic)

	switch req.Action {
	case "start":
		return h.startStream(ctx, conn, req, streamKey, topic)
	case "stop":
		return h.stopStream(streamKey)
	default:
		return fmt.Errorf("unknown action: %s", req.Action)
	}
}

func (h *MetricsStreamHandler) startStream(ctx context.Context, conn streamConn, req MetricsStreamRequest, streamKey, topic string) error {
	// Stop any existing stream for this connection/topic
	_ = h.stopStream(streamKey)

	interval := metrics.StreamInterval(req.TimeRange, req.StreamingRate)

	// Create a cancellable context for this stream
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &activeStream{cancel: cancel}
	h.streams.Store(streamKey, stream)
	done := func() {
		cancel()
		h.streams.CompareAndDelete(streamKey, stream)
	}

	// Check if this is a workload batch query (has PodNames)
	if len(req.PodNames) > 0 {
		query := metrics.WorkloadMetricQuery{PodNames: req.PodNames, Namespace: req.Namespace, MetricType: req.MetricType, TimeRange: req.TimeRange}
		go runStream(streamCtx, conn, interval, done, "workload_metrics", topic,
			func(ctx context.Context) (*metrics.WorkloadMetricResponse, error) {
				return h.metricsService.QueryWorkloadMetrics(ctx, req.Cluster, req.Provider, query)
			},
			func(d *metrics.WorkloadMetricResponse) bool { return len(d.Pods) > 0 },
			nil)
	} else {
		// Start the streaming goroutine for single pod/node
		query := metrics.MetricQuery{PodName: req.Pod, Namespace: req.Namespace, ContainerName: req.Container, NodeName: req.NodeName, MetricType: req.MetricType, TimeRange: req.TimeRange}
		fetch := func(query metrics.MetricQuery) func(context.Context) (*metrics.MetricResponse, error) {
			return func(ctx context.Context) (*metrics.MetricResponse, error) {
				return h.metricsService.QueryMetrics(ctx, req.Cluster, req.Provider, query)
			}
		}
		// Switching between CPU and memory is the usual next click: once
		// this chart is up, warm the cache for the other so it paints from
		// the cache at once. Bounded to those two, once per stream.
		var siblings []func(context.Context) (*metrics.MetricResponse, error)
		for _, metric := range []string{"cpu", "memory"} {
			if metric != req.MetricType {
				sibling := query
				sibling.MetricType = metric
				siblings = append(siblings, fetch(sibling))
			}
		}
		go runStream(streamCtx, conn, interval, done, "metrics", topic, fetch(query),
			func(d *metrics.MetricResponse) bool { return len(d.Values) > 0 },
			siblings)
	}

	log.Printf("[MetricsStream] Started streaming for %s at %s intervals", streamKey, interval)
	return nil
}

func (h *MetricsStreamHandler) stopStream(streamKey string) error {
	if stream, ok := h.streams.LoadAndDelete(streamKey); ok {
		stream.(*activeStream).cancel()
		log.Printf("[MetricsStream] Stopped streaming for %s", streamKey)
	}
	return nil
}

type fetchResult[D any] struct {
	data *D
	err  error
}

// runStream refreshes one chart until ctx ends or the connection closes.
//
// It first sends whatever the chart cache already holds, marked stale, so a
// chart opened again (or after a restart) paints at once; then the live
// answer. Only one refresh runs at a time: a tick that finds the last one
// still running is skipped rather than piling another query on a slow store.
// A slow or failing store never ends the stream — refreshes back off while
// failures repeat, and the chart keeps what it shows until they have repeated
// a few times. An update identical to the last one sent is not sent again.
func runStream[D any](ctx context.Context, conn streamConn, interval time.Duration, done func(), msgType, topic string,
	fetch func(context.Context) (*D, error), hasData func(*D) bool, warm []func(context.Context) (*D, error)) {
	defer done()

	var last []byte
	// send reports false when the connection is gone. An update dropped
	// because the client is behind is simply superseded by the next one.
	send := func(payload streamPayload[D]) bool {
		payload.Type, payload.Topic = msgType, topic
		key, err := jsonv2.Marshal(payload)
		if err != nil {
			log.Printf("[MetricsStream] Failed to encode %s: %v", topic, err)
			return true
		}
		if bytes.Equal(key, last) {
			return true
		}
		payload.Timestamp = time.Now().Unix()
		b, err := core.NewOutgoingMessage(core.MessageType(msgType), payload).Marshal()
		if err == nil {
			err = conn.Send(b)
		}
		switch {
		case errors.Is(err, core.ErrConnectionClosed):
			return false
		case err != nil:
			log.Printf("[MetricsStream] Failed to send %s: %v", topic, err)
		default:
			last = key
		}
		return true
	}

	// answered is set once the chart has something to show.
	answered := false
	if data, err := fetch(metrics.WithCacheOnly(ctx, true)); err == nil && hasData(data) {
		if !send(streamPayload[D]{Data: data, Stale: true}) {
			return
		}
		answered = true
	}

	results := make(chan fetchResult[D], 1)
	running := false
	refresh := func() {
		running = true
		go func() {
			data, err := fetchWithRetry(ctx, fetch)
			results <- fetchResult[D]{data, err}
		}()
	}
	refresh()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var (
		failures int
		notUntil time.Time
		warmed   bool
	)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[MetricsStream] Context cancelled for topic %s", topic)
			return
		case <-ticker.C:
			if running || time.Now().Before(notUntil) {
				continue
			}
			refresh()
		case r := <-results:
			running = false
			if ctx.Err() != nil {
				return
			}
			var payload streamPayload[D]
			if r.err == nil {
				failures, notUntil = 0, time.Time{}
				payload.Data = r.data
			} else {
				failures++
				notUntil = time.Now().Add(backoff(interval, failures))
				log.Printf("[MetricsStream] Refresh %d of %s failed: %v", failures, topic, r.err)
				if metrics.IsTransient(r.err) {
					if answered && failures < metricsReportAfter {
						continue
					}
					payload.Error, payload.Transient = metricsSlowMessage, true
				} else {
					payload.Error = r.err.Error()
				}
			}
			if !send(payload) {
				return
			}
			answered = true
			if r.err == nil && !warmed {
				warmed = true
				for _, f := range warm {
					go func() { _, _ = f(ctx) }()
				}
			}
		}
	}
}

// fetchWithRetry asks again, after a short jittered pause, when a refresh
// fails for a reason that may pass at once — the tunnel was replaced, the
// store shed load. A bad query or a missing store is answered straight away.
func fetchWithRetry[D any](ctx context.Context, fetch func(context.Context) (*D, error)) (*D, error) {
	pause := metricsRetryPause
	for attempt := 1; ; attempt++ {
		data, err := fetch(ctx)
		if err == nil || attempt == metricsAttempts || !metrics.IsTransient(err) || ctx.Err() != nil {
			return data, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pause/2 + rand.N(pause)):
		}
		pause *= 2
	}
}

// backoff spaces out the refreshes of a stream whose store keeps failing.
func backoff(interval time.Duration, failures int) time.Duration {
	d := interval << min(failures, 6)
	return min(d, metricsMaxBackoff)
}

func (h *MetricsStreamHandler) buildTopic(req MetricsStreamRequest) string {
	// Use workload identifier if PodNames is set: two workloads of one
	// namespace must not share a stream.
	if len(req.PodNames) > 0 {
		pods := slices.Clone(req.PodNames)
		slices.Sort(pods)
		return fmt.Sprintf("workload_metrics:%s:%s:%s:%s:%s",
			req.Cluster, req.Namespace, req.MetricType, req.TimeRange, strings.Join(slices.Compact(pods), ","))
	}
	// Use NodeName if present (for node metrics), otherwise use Pod; the
	// container keeps two container charts of one pod apart.
	identifier := req.Pod
	if req.NodeName != "" {
		identifier = req.NodeName
	}
	if req.Container != "" {
		identifier += "/" + req.Container
	}
	return fmt.Sprintf("metrics:%s:%s:%s:%s:%s",
		req.Cluster, req.Namespace, identifier, req.MetricType, req.TimeRange)
}

func (h *MetricsStreamHandler) OnConnectionClose(conn *core.Connection) {
	var toDelete []string
	h.streams.Range(func(key, value interface{}) bool {
		streamKey := key.(string)
		connIDStr := string(conn.ID())
		if len(streamKey) > len(connIDStr) && streamKey[:len(connIDStr)] == connIDStr {
			toDelete = append(toDelete, streamKey)
		}
		return true
	})

	for _, key := range toDelete {
		_ = h.stopStream(key)
	}
}
