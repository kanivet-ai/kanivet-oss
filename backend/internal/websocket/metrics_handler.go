package websocket

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/metrics"
	"github.com/kanivet/backend/internal/websocket/core"
)

const metricsFetchTimeout = 10 * time.Second

type MetricsStreamHandler struct {
	metricsService *metrics.Service
	streams        sync.Map // connectionID:topic -> streamCancel
}

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

type MetricsStreamResponse struct {
	Type      string                  `json:"type"`
	Topic     string                  `json:"topic"`
	Timestamp int64                   `json:"timestamp"`
	Data      *metrics.MetricResponse `json:"data,omitempty"`
	Error     string                  `json:"error,omitempty"`
}

// WorkloadMetricsResponse for batch pod metrics
type WorkloadMetricsResponse struct {
	Type      string                          `json:"type"`
	Topic     string                          `json:"topic"`
	Timestamp int64                           `json:"timestamp"`
	Data      *metrics.WorkloadMetricResponse `json:"data,omitempty"`
	Error     string                          `json:"error,omitempty"`
}

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

func (h *MetricsStreamHandler) startStream(ctx context.Context, conn *core.Connection, req MetricsStreamRequest, streamKey, topic string) error {
	// Stop any existing stream for this connection/topic
	_ = h.stopStream(streamKey)

	streamingRate := req.StreamingRate
	if streamingRate <= 0 {
		streamingRate = 2 // Reduce default from 5 seconds to 2 seconds
	}

	// Create a cancellable context for this stream
	streamCtx, cancel := context.WithCancel(ctx)
	h.streams.Store(streamKey, cancel)

	// Check if this is a workload batch query (has PodNames)
	if len(req.PodNames) > 0 {
		go h.streamWorkloadMetrics(streamCtx, conn, req, topic, time.Duration(streamingRate)*time.Second)
	} else {
		// Start the streaming goroutine for single pod/node
		go h.streamMetrics(streamCtx, conn, req, topic, time.Duration(streamingRate)*time.Second)
	}

	log.Printf("[MetricsStream] Started streaming for %s at %ds intervals", streamKey, streamingRate)
	return nil
}

func (h *MetricsStreamHandler) stopStream(streamKey string) error {
	if cancel, ok := h.streams.LoadAndDelete(streamKey); ok {
		cancel.(context.CancelFunc)()
		log.Printf("[MetricsStream] Stopped streaming for %s", streamKey)
	}
	return nil
}

func (h *MetricsStreamHandler) streamMetrics(ctx context.Context, conn *core.Connection, req MetricsStreamRequest, topic string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if !h.fetchAndSendWithTimeout(ctx, conn, req, topic) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[MetricsStream] Context cancelled for topic %s", topic)
			return
		case <-ticker.C:
			if !h.fetchAndSendWithTimeout(ctx, conn, req, topic) {
				return
			}
		}
	}
}

// streamWorkloadMetrics streams metrics for multiple pods using a single Prometheus query
func (h *MetricsStreamHandler) streamWorkloadMetrics(ctx context.Context, conn *core.Connection, req MetricsStreamRequest, topic string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if !h.fetchAndSendWorkloadWithTimeout(ctx, conn, req, topic) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[MetricsStream] Context cancelled for workload topic %s", topic)
			return
		case <-ticker.C:
			if !h.fetchAndSendWorkloadWithTimeout(ctx, conn, req, topic) {
				return
			}
		}
	}
}

func (h *MetricsStreamHandler) fetchAndSendWorkloadWithTimeout(ctx context.Context, conn *core.Connection, req MetricsStreamRequest, topic string) bool {
	responseCh := make(chan WorkloadMetricsResponse, 1)
	go func() {
		responseCh <- h.buildWorkloadMetricsResponse(req, topic)
	}()

	select {
	case <-ctx.Done():
		return false
	case response := <-responseCh:
		if err := h.sendWorkload(conn, response); err != nil {
			log.Printf("[MetricsStream] Failed to send workload metrics: %v", err)
			streamKey := fmt.Sprintf("%s:%s", conn.ID(), topic)
			_ = h.stopStream(streamKey)
			return false
		}
		return true
	case <-time.After(metricsFetchTimeout):
		response := WorkloadMetricsResponse{
			Type:      "workload_metrics",
			Topic:     topic,
			Timestamp: time.Now().Unix(),
			Error:     "metrics provider unavailable: query timed out",
		}
		_ = h.sendWorkload(conn, response)
		streamKey := fmt.Sprintf("%s:%s", conn.ID(), topic)
		_ = h.stopStream(streamKey)
		return false
	}
}

func (h *MetricsStreamHandler) buildWorkloadMetricsResponse(req MetricsStreamRequest, topic string) WorkloadMetricsResponse {
	query := metrics.WorkloadMetricQuery{
		PodNames:   req.PodNames,
		Namespace:  req.Namespace,
		MetricType: req.MetricType,
		TimeRange:  req.TimeRange,
	}

	data, err := h.metricsService.QueryWorkloadMetrics(req.Cluster, query)

	response := WorkloadMetricsResponse{
		Type:      "workload_metrics",
		Topic:     topic,
		Timestamp: time.Now().Unix(),
	}

	if err != nil {
		response.Error = err.Error()
		log.Printf("[MetricsStream] Error fetching workload metrics: %v", err)
	} else {
		response.Data = data
		log.Printf("[MetricsStream] Successfully fetched workload metrics for %d pods", len(data.Pods))
	}

	return response
}

func (h *MetricsStreamHandler) sendWorkload(conn *core.Connection, payload WorkloadMetricsResponse) error {
	msg := core.NewOutgoingMessage("workload_metrics", payload)
	b, err := msg.Marshal()
	if err != nil {
		return err
	}
	return conn.Send(b)
}

func (h *MetricsStreamHandler) fetchAndSendWithTimeout(ctx context.Context, conn *core.Connection, req MetricsStreamRequest, topic string) bool {
	responseCh := make(chan MetricsStreamResponse, 1)
	go func() {
		responseCh <- h.buildMetricsResponse(req, topic)
	}()

	select {
	case <-ctx.Done():
		return false
	case response := <-responseCh:
		if err := h.send(conn, response); err != nil {
			log.Printf("[MetricsStream] Failed to send metrics: %v", err)
			streamKey := fmt.Sprintf("%s:%s", conn.ID(), topic)
			_ = h.stopStream(streamKey)
			return false
		}
		return true
	case <-time.After(metricsFetchTimeout):
		response := MetricsStreamResponse{
			Type:      "metrics",
			Topic:     topic,
			Timestamp: time.Now().Unix(),
			Error:     "metrics provider unavailable: query timed out",
		}
		_ = h.send(conn, response)
		streamKey := fmt.Sprintf("%s:%s", conn.ID(), topic)
		_ = h.stopStream(streamKey)
		return false
	}
}

func (h *MetricsStreamHandler) buildMetricsResponse(req MetricsStreamRequest, topic string) MetricsStreamResponse {
	query := metrics.MetricQuery{
		PodName:       req.Pod,
		Namespace:     req.Namespace,
		ContainerName: req.Container,
		NodeName:      req.NodeName,
		MetricType:    req.MetricType,
		TimeRange:     req.TimeRange,
	}

	// Retry logic for Prometheus errors
	var data *metrics.MetricResponse
	var err error
	maxRetries := 3
	retryDelay := time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		data, err = h.metricsService.QueryMetrics(req.Cluster, req.Provider, query)

		if err == nil {
			break // Success
		}

		log.Printf("[MetricsStream] Attempt %d/%d failed for %s: %v", attempt, maxRetries, topic, err)

		if attempt < maxRetries {
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}
	}

	response := MetricsStreamResponse{
		Type:      "metrics",
		Topic:     topic,
		Timestamp: time.Now().Unix(),
	}

	if err != nil {
		response.Error = fmt.Sprintf("Failed after %d attempts: %v", maxRetries, err)
		log.Printf("[MetricsStream] Final error after retries for %s: %v", topic, err)
	} else {
		response.Data = data
		log.Printf("[MetricsStream] Successfully fetched metrics for %s", topic)
	}

	return response
}

func (h *MetricsStreamHandler) send(conn *core.Connection, payload MetricsStreamResponse) error {
	msg := core.NewOutgoingMessage("metrics", payload)
	b, err := msg.Marshal()
	if err != nil {
		return err
	}
	return conn.Send(b)
}

func (h *MetricsStreamHandler) buildTopic(req MetricsStreamRequest) string {
	// Use workload identifier if PodNames is set
	if len(req.PodNames) > 0 {
		return fmt.Sprintf("workload_metrics:%s:%s:%s:%s",
			req.Cluster, req.Namespace, req.MetricType, req.TimeRange)
	}
	// Use NodeName if present (for node metrics), otherwise use Pod
	identifier := req.Pod
	if req.NodeName != "" {
		identifier = req.NodeName
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
