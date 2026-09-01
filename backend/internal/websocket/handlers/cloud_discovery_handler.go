package handlers

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/websocket/core"
)

type CloudDiscoveryHandler struct {
	cloudService  *cloud.Service
	activeStreams *sync.Map
}

type discoveryStreamKey struct {
	connectionID string
	key          string
}

type discoveryStream struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewCloudDiscoveryHandler(cloudService *cloud.Service) core.MessageHandler {
	return &CloudDiscoveryHandler{
		cloudService:  cloudService,
		activeStreams: &sync.Map{},
	}
}

func (h *CloudDiscoveryHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"cloud.discover"}
}

func (h *CloudDiscoveryHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload struct {
		Action      string   `json:"action"`
		Key         string   `json:"key"`
		Provider    string   `json:"provider"`
		SSOStartURL string   `json:"ssoStartUrl,omitempty"`
		Profile     string   `json:"profile,omitempty"`
		AccountIDs  []string `json:"accountIds,omitempty"`
		Region      string   `json:"region,omitempty"`
		AllRegions  bool     `json:"allRegions,omitempty"`
		ProjectID   string   `json:"projectId,omitempty"`
	}

	if err := msg.UnmarshalPayload(&payload); err != nil {
		return fmt.Errorf("failed to unmarshal cloud.discover payload: %w", err)
	}

	switch payload.Action {
	case "start":
		return h.handleStart(ctx, conn, payload)
	case "stop":
		return h.handleStop(conn, payload.Key)
	default:
		return fmt.Errorf("unknown action: %s", payload.Action)
	}
}

func (h *CloudDiscoveryHandler) handleStart(ctx context.Context, conn *core.Connection, payload struct {
	Action      string   `json:"action"`
	Key         string   `json:"key"`
	Provider    string   `json:"provider"`
	SSOStartURL string   `json:"ssoStartUrl,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	AccountIDs  []string `json:"accountIds,omitempty"`
	Region      string   `json:"region,omitempty"`
	AllRegions  bool     `json:"allRegions,omitempty"`
	ProjectID   string   `json:"projectId,omitempty"`
}) error {
	streamKey := discoveryStreamKey{
		connectionID: string(conn.ID()),
		key:          payload.Key,
	}

	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*discoveryStream); ok {
			stream.cancel()
			<-stream.done
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &discoveryStream{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	h.activeStreams.Store(streamKey, stream)

	go func() {
		defer close(stream.done)
		defer h.activeStreams.Delete(streamKey)
		defer cancel()

		h.runDiscovery(streamCtx, conn, payload)
	}()

	return nil
}

func (h *CloudDiscoveryHandler) runDiscovery(ctx context.Context, conn *core.Connection, payload struct {
	Action      string   `json:"action"`
	Key         string   `json:"key"`
	Provider    string   `json:"provider"`
	SSOStartURL string   `json:"ssoStartUrl,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	AccountIDs  []string `json:"accountIds,omitempty"`
	Region      string   `json:"region,omitempty"`
	AllRegions  bool     `json:"allRegions,omitempty"`
	ProjectID   string   `json:"projectId,omitempty"`
}) {
	req := cloud.DiscoverRequest{
		Provider:    cloud.Provider(payload.Provider),
		SSOStartURL: payload.SSOStartURL,
		Profile:     payload.Profile,
		AccountIDs:  payload.AccountIDs,
		Region:      payload.Region,
		ProjectID:   payload.ProjectID,
	}

	eventCh := make(chan cloud.DiscoveryEvent, 100)
	go h.cloudService.DiscoverClustersStreaming(ctx, req, eventCh)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			h.sendEvent(conn, event)
		}
	}
}

func (h *CloudDiscoveryHandler) sendEvent(conn *core.Connection, event cloud.DiscoveryEvent) {
	payload := map[string]interface{}{
		"type": string(event.Type),
	}
	if event.Cluster != nil {
		payload["cluster"] = event.Cluster
	}
	if event.Progress != nil {
		payload["progress"] = event.Progress
	}
	if event.Error != "" {
		payload["error"] = event.Error
	}

	msg := core.NewOutgoingMessage("cloud.discover", payload)
	if data, err := msg.Marshal(); err == nil {
		if err := conn.Send(data); err != nil {
			log.Printf("Failed to send cloud discovery event: %v", err)
		}
	}
}

func (h *CloudDiscoveryHandler) handleStop(conn *core.Connection, key string) error {
	streamKey := discoveryStreamKey{
		connectionID: string(conn.ID()),
		key:          key,
	}

	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*discoveryStream); ok {
			stream.cancel()
		}
	}
	return nil
}

func (h *CloudDiscoveryHandler) OnConnectionClose(conn *core.Connection) {
	h.activeStreams.Range(func(key, value interface{}) bool {
		if streamKey, ok := key.(discoveryStreamKey); ok && streamKey.connectionID == string(conn.ID()) {
			if stream, ok := value.(*discoveryStream); ok {
				stream.cancel()
			}
			h.activeStreams.Delete(key)
		}
		return true
	})
}
