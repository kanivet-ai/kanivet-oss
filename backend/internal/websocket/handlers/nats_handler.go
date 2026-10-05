package handlers

import (
	"context"
	jsonv2 "encoding/json/v2"
	"log"
	"sync"
	"time"

	natspkg "github.com/kanivet/backend/internal/nats"
	"github.com/kanivet/backend/internal/websocket/core"
	natsgo "github.com/nats-io/nats.go"
)

// NatsHandler streams live NATS subject messages (Live Tail) to subscribed
// websocket clients. It never publishes: the only NATS call it makes is a
// plain core subscribe, through the same read-only LivePool everything else
// in the nats package uses.
type NatsHandler struct {
	pool  *natspkg.LivePool
	hub   *core.Hub
	mu    sync.Mutex
	tails map[string]*natsTail
}

type natsTail struct {
	unsubscribe func()
}

type NatsTailMessage struct {
	core.BaseMessage
	Cluster    string              `json:"cluster"`
	Subject    string              `json:"subject"`
	Data       []byte              `json:"data"`
	Headers    map[string][]string `json:"headers,omitempty"`
	ReceivedAt time.Time           `json:"receivedAt"`
}

func (m *NatsTailMessage) Marshal() ([]byte, error) { return jsonv2.Marshal(m) }

type NatsTailErrorMessage struct {
	core.BaseMessage
	Cluster string `json:"cluster"`
	Subject string `json:"subject"`
	Error   string `json:"error"`
}

func (m *NatsTailErrorMessage) Marshal() ([]byte, error) { return jsonv2.Marshal(m) }

func NewNatsHandler(pool *natspkg.LivePool, hub *core.Hub) *NatsHandler {
	return &NatsHandler{pool: pool, hub: hub, tails: make(map[string]*natsTail)}
}

func (h *NatsHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"nats-tail"}
}

func (h *NatsHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload struct {
		Action  string `json:"action"`
		Cluster string `json:"cluster"`
		Subject string `json:"subject"`
	}
	if err := jsonv2.Unmarshal(msg.Payload, &payload); err != nil {
		return err
	}

	switch payload.Action {
	case "subscribe":
		return h.handleSubscribe(ctx, conn, payload.Cluster, payload.Subject)
	case "unsubscribe":
		return h.handleUnsubscribe(conn, payload.Cluster, payload.Subject)
	default:
		return nil
	}
}

func tailKey(cluster, subject string) string {
	return cluster + "|" + subject
}

func (h *NatsHandler) handleSubscribe(ctx context.Context, conn *core.Connection, cluster, subject string) error {
	key := tailKey(cluster, subject)
	topic := "nats-tail:" + key

	if _, err := h.hub.Subscribe(topic, conn); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.tails[key]; exists {
		return nil
	}

	unsubscribe, err := h.pool.Subscribe(ctx, cluster, subject, func(m *natsgo.Msg) {
		tailMsg := &NatsTailMessage{
			BaseMessage: core.BaseMessage{MessageType: "nats-tail", Timestamp: time.Now()},
			Cluster:     cluster,
			Subject:     m.Subject,
			Data:        m.Data,
			Headers:     map[string][]string(m.Header),
			ReceivedAt:  time.Now(),
		}
		if err := h.hub.Broadcast(topic, tailMsg); err != nil {
			log.Printf("[NatsHandler] broadcast failed for %s: %v", topic, err)
		}
	})
	if err != nil {
		errMsg := &NatsTailErrorMessage{
			BaseMessage: core.BaseMessage{MessageType: "nats-tail", Timestamp: time.Now()},
			Cluster:     cluster,
			Subject:     subject,
			Error:       err.Error(),
		}
		if broadcastErr := h.hub.Broadcast(topic, errMsg); broadcastErr != nil {
			log.Printf("[NatsHandler] broadcast failed for %s: %v", topic, broadcastErr)
		}
		return nil
	}

	h.tails[key] = &natsTail{unsubscribe: unsubscribe}
	log.Printf("[NatsHandler] Started tail for cluster=%s subject=%s", cluster, subject)
	return nil
}

func (h *NatsHandler) handleUnsubscribe(conn *core.Connection, cluster, subject string) error {
	key := tailKey(cluster, subject)
	topic := "nats-tail:" + key

	if _, err := h.hub.Unsubscribe(topic, conn); err != nil {
		return err
	}
	if h.hub.HasSubscribers(topic) {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if tail, exists := h.tails[key]; exists {
		tail.unsubscribe()
		delete(h.tails, key)
		log.Printf("[NatsHandler] Stopped tail for cluster=%s subject=%s", cluster, subject)
	}
	return nil
}
