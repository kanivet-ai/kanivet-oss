package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"log"
	"sync"

	"github.com/kanivet/backend/internal/cloud"
	"github.com/kanivet/backend/internal/websocket/core"
)

// clusterDiscoverer is the part of cloud.Service the handler uses.
type clusterDiscoverer interface {
	DiscoverClustersStreaming(ctx context.Context, req cloud.DiscoverRequest, eventCh chan<- cloud.DiscoveryEvent)
}

type CloudDiscoveryHandler struct {
	cloudService  clusterDiscoverer
	activeStreams *sync.Map
}

type discoveryStreamKey struct {
	connectionID string
	key          string
}

type discoverPayload struct {
	Action       string   `json:"action"`
	Key          string   `json:"key"`
	Provider     string   `json:"provider"`
	SSOStartURL  string   `json:"ssoStartUrl,omitempty"`
	Profile      string   `json:"profile,omitempty"`
	AccountIDs   []string `json:"accountIds,omitempty"`
	Region       string   `json:"region,omitempty"`
	AllRegions   bool     `json:"allRegions,omitempty"`
	ProjectID    string   `json:"projectId,omitempty"`
	Subscription string   `json:"subscription,omitempty"`
}

type discoveryStream struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *fencedConn
}

func NewCloudDiscoveryHandler(cloudService *cloud.Service) core.MessageHandler {
	return newCloudDiscoveryHandler(cloudService)
}

func newCloudDiscoveryHandler(d clusterDiscoverer) *CloudDiscoveryHandler {
	return &CloudDiscoveryHandler{
		cloudService:  d,
		activeStreams: &sync.Map{},
	}
}

func (h *CloudDiscoveryHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"cloud.discover"}
}

func (h *CloudDiscoveryHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload discoverPayload

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

func (h *CloudDiscoveryHandler) handleStart(ctx context.Context, conn *core.Connection, payload discoverPayload) error {
	streamKey := discoveryStreamKey{
		connectionID: string(conn.ID()),
		key:          payload.Key,
	}

	// A start for a key that is already running (the client replays it after
	// a reconnect) replaces that discovery. The old one is fenced, so it can
	// send nothing more, and cancelled; the connection's read loop does not
	// wait for it to wind down.
	if existing, exists := h.activeStreams.Load(streamKey); exists {
		if stream, ok := existing.(*discoveryStream); ok {
			stream.conn.fence()
			stream.cancel()
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &discoveryStream{
		cancel: cancel,
		done:   make(chan struct{}),
		conn:   &fencedConn{conn: conn},
	}
	h.activeStreams.Store(streamKey, stream)

	go func() {
		defer close(stream.done)
		// Only the stream's own entry: a newer start may have replaced it.
		defer h.activeStreams.CompareAndDelete(streamKey, stream)
		defer cancel()

		h.runDiscovery(streamCtx, stream.conn, payload)
	}()

	return nil
}

func (h *CloudDiscoveryHandler) runDiscovery(ctx context.Context, conn logSender, payload discoverPayload) {
	req := cloud.DiscoverRequest{
		Provider:     cloud.Provider(payload.Provider),
		SSOStartURL:  payload.SSOStartURL,
		Profile:      payload.Profile,
		AccountIDs:   payload.AccountIDs,
		Region:       payload.Region,
		ProjectID:    payload.ProjectID,
		Subscription: payload.Subscription,
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
			h.sendEvent(conn, payload.Key, event)
		}
	}
}

func (h *CloudDiscoveryHandler) sendEvent(conn logSender, key string, event cloud.DiscoveryEvent) {
	payload := map[string]interface{}{
		"type": string(event.Type),
		"key":  key,
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
		if err := conn.Send(data); err != nil && !stderrors.Is(err, errStreamSuperseded) {
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
