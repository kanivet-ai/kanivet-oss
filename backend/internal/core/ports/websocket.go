package ports

import (
	"context"

	"github.com/kanivet/backend/internal/core/domain"
)

// WebSocketHub defines the interface for WebSocket hub operations
type WebSocketHub interface {
	// Broadcast sends a message to all connections subscribed to a topic
	Broadcast(ctx context.Context, topic string, message WebSocketMessage) error

	// SendToConnection sends a message to a specific connection
	SendToConnection(ctx context.Context, connectionID string, message WebSocketMessage) error

	// Subscribe subscribes a connection to a topic
	Subscribe(ctx context.Context, connectionID string, topic string) error

	// Unsubscribe unsubscribes a connection from a topic
	Unsubscribe(ctx context.Context, connectionID string, topic string) error

	// UnsubscribeAll unsubscribes a connection from all topics
	UnsubscribeAll(ctx context.Context, connectionID string) error

	// GetSubscribers returns all connections subscribed to a topic
	GetSubscribers(ctx context.Context, topic string) ([]string, error)

	// GetSubscriptions returns all topics a connection is subscribed to
	GetSubscriptions(ctx context.Context, connectionID string) ([]string, error)

	// ConnectionCount returns the number of active connections
	ConnectionCount(ctx context.Context) (int, error)

	// RegisterHandler registers a message handler for a specific message type
	RegisterHandler(messageType string, handler WebSocketHandler) error

	// Stats returns WebSocket hub statistics
	Stats(ctx context.Context) (*WebSocketStats, error)
}

// WebSocketMessage represents a message to be sent via WebSocket
type WebSocketMessage interface {
	// Type returns the message type
	Type() string

	// Payload returns the message payload
	Payload() interface{}

	// Marshal serializes the message
	Marshal() ([]byte, error)
}

// WebSocketHandler handles incoming WebSocket messages
type WebSocketHandler interface {
	// HandleMessage processes an incoming message
	HandleMessage(ctx context.Context, connectionID string, message []byte) error

	// MessageTypes returns the message types this handler supports
	MessageTypes() []string
}

// WebSocketStats represents WebSocket hub statistics
type WebSocketStats struct {
	Connections      int            // Total active connections
	Subscriptions    int            // Total active subscriptions
	MessagesSent     int64          // Total messages sent
	MessagesReceived int64          // Total messages received
	TopicStats       map[string]int // Subscribers per topic
	BytesSent        int64          // Total bytes sent
	BytesReceived    int64          // Total bytes received
}

// ResourceUpdateMessage represents a resource update notification
type ResourceUpdateMessage struct {
	Event       domain.EventType
	ResourceID  domain.ResourceID
	Resource    *domain.Resource
	OldResource *domain.Resource // For update events
}

func (m ResourceUpdateMessage) Type() string {
	return "resource.update"
}

func (m ResourceUpdateMessage) Payload() interface{} {
	return map[string]interface{}{
		"event":       m.Event,
		"resourceId":  m.ResourceID,
		"resource":    m.Resource,
		"oldResource": m.OldResource,
	}
}

func (m ResourceUpdateMessage) Marshal() ([]byte, error) {
	// Implementation would serialize to JSON
	return nil, nil
}

// CountUpdateMessage represents a resource count update
type CountUpdateMessage struct {
	Cluster   string
	Namespace string
	Group     string
	Version   string
	Kind      string
	Count     int
}

func (m CountUpdateMessage) Type() string {
	return "count.update"
}

func (m CountUpdateMessage) Payload() interface{} {
	return map[string]interface{}{
		"cluster":   m.Cluster,
		"namespace": m.Namespace,
		"group":     m.Group,
		"version":   m.Version,
		"kind":      m.Kind,
		"count":     m.Count,
	}
}

func (m CountUpdateMessage) Marshal() ([]byte, error) {
	// Implementation would serialize to JSON
	return nil, nil
}

// StatusUpdateMessage represents a pod status update
type StatusUpdateMessage struct {
	Cluster   string
	Namespace string
	PodName   string
	Status    domain.ResourceStatus
}

func (m StatusUpdateMessage) Type() string {
	return "status.update"
}

func (m StatusUpdateMessage) Payload() interface{} {
	return map[string]interface{}{
		"cluster":   m.Cluster,
		"namespace": m.Namespace,
		"podName":   m.PodName,
		"status":    m.Status,
	}
}

func (m StatusUpdateMessage) Marshal() ([]byte, error) {
	// Implementation would serialize to JSON
	return nil, nil
}

// ErrorMessage represents an error notification
type ErrorMessage struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (m ErrorMessage) Type() string {
	return "error"
}

func (m ErrorMessage) Payload() interface{} {
	return map[string]interface{}{
		"code":    m.Code,
		"message": m.Message,
		"details": m.Details,
	}
}

func (m ErrorMessage) Marshal() ([]byte, error) {
	// Implementation would serialize to JSON
	return nil, nil
}

// WebSocketTopicBuilder helps build consistent WebSocket topics
type WebSocketTopicBuilder interface {
	// ForResource builds a topic for resource updates
	ForResource(cluster, namespace, group, version, kind string) string

	// ForResourceInstance builds a topic for a specific resource instance
	ForResourceInstance(cluster, namespace, group, version, kind, name string) string

	// ForCluster builds a topic for cluster-wide updates
	ForCluster(cluster string) string

	// ForNamespace builds a topic for namespace-specific updates
	ForNamespace(cluster, namespace string) string

	// ForCounts builds a topic for resource count updates
	ForCounts(cluster string) string
}

// DefaultWebSocketTopicBuilder implements WebSocketTopicBuilder
type DefaultWebSocketTopicBuilder struct{}

func (b DefaultWebSocketTopicBuilder) ForResource(cluster, namespace, group, version, kind string) string {
	if namespace == "" {
		return "resource:" + cluster + "/" + group + "/" + version + "/" + kind
	}
	return "resource:" + cluster + "/" + namespace + "/" + group + "/" + version + "/" + kind
}

func (b DefaultWebSocketTopicBuilder) ForResourceInstance(cluster, namespace, group, version, kind, name string) string {
	if namespace == "" {
		return "resource:" + cluster + "/" + group + "/" + version + "/" + kind + "/" + name
	}
	return "resource:" + cluster + "/" + namespace + "/" + group + "/" + version + "/" + kind + "/" + name
}

func (b DefaultWebSocketTopicBuilder) ForCluster(cluster string) string {
	return "cluster:" + cluster
}

func (b DefaultWebSocketTopicBuilder) ForNamespace(cluster, namespace string) string {
	return "namespace:" + cluster + "/" + namespace
}

func (b DefaultWebSocketTopicBuilder) ForCounts(cluster string) string {
	return "counts:" + cluster
}
