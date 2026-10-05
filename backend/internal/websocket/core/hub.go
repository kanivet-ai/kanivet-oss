package core

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/faults"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type HubConfig struct {
	ConnectionTimeout time.Duration
	HeartbeatInterval time.Duration
	MaxConnections    int
	MaxMessageSize    int64
	EnableCompression bool
	EnableMetrics     bool
	SendChannelSize   int
	// MaxQueuedBytes caps the bytes queued per connection; zero uses
	// DefaultMaxQueuedBytes.
	MaxQueuedBytes int64
}

func DefaultHubConfig() *HubConfig {
	return &HubConfig{
		ConnectionTimeout: 60 * time.Second,
		HeartbeatInterval: 30 * time.Second,
		MaxConnections:    10000,
		MaxMessageSize:    10 * 1024 * 1024,
		// The backend only listens on loopback, where permessage-deflate
		// saves no transfer time and costs a deflate in the write pump and an
		// inflate in the renderer's network process for every frame.
		EnableCompression: false,
		EnableMetrics:     true,
		SendChannelSize:   2048,
	}
}

type Hub struct {
	Config           *HubConfig
	connections      sync.Map
	router           Router
	subManager       SubscriptionManager
	regMu            sync.RWMutex
	middleware       []Middleware
	metrics          Metrics
	closeHandlers    []ConnectionCloseHandler
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	heartbeatStarted sync.Once
	chain            atomic.Pointer[MessageHandlerFunc]
	// connCount mirrors the entries of connections so the MaxConnections check
	// is O(1) and a reservation closes the check/insert race.
	connCount atomic.Int64
}

// handlerQueueSize bounds the messages a connection may have waiting for its
// handler worker. When it is full the read loop blocks, which is the
// pre-worker behaviour, rather than dropping a subscribe.
const handlerQueueSize = 256

// handlerDrainTimeout bounds how long a disconnect waits for the handler that
// is running when the read loop ends.
const handlerDrainTimeout = 10 * time.Second

func NewHub(config *HubConfig, router Router, subManager SubscriptionManager) *Hub {
	if config == nil {
		config = DefaultHubConfig()
	}

	// Validate configuration
	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 60 * time.Second
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = 10000
	}
	if config.MaxMessageSize <= 0 {
		config.MaxMessageSize = 1024 * 1024
	}
	if config.SendChannelSize <= 0 {
		config.SendChannelSize = 256
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		Config:     config,
		router:     router,
		subManager: subManager,
		ctx:        ctx,
		cancel:     cancel,
	}
	return h
}

func (h *Hub) AddMiddleware(m Middleware) {
	h.regMu.Lock()
	h.middleware = append(h.middleware, m)
	h.chain.Store(nil)
	h.regMu.Unlock()
}

func (h *Hub) AddConnectionCloseHandler(handler ConnectionCloseHandler) {
	h.regMu.Lock()
	h.closeHandlers = append(h.closeHandlers, handler)
	h.regMu.Unlock()
}

func (h *Hub) RegisterConnection(conn *Connection) error {
	if conn.id == "" {
		conn.id = ConnectionID(uuid.New().String())
	}

	// Reserve a slot before inserting so concurrent registrations cannot
	// overshoot the limit.
	if h.connCount.Add(1) > int64(h.Config.MaxConnections) {
		h.connCount.Add(-1)
		return ErrRateLimitExceeded
	}

	if _, replaced := h.connections.Swap(conn.id, conn); replaced {
		h.connCount.Add(-1)
	}

	if h.metrics != nil {
		h.metrics.ConnectionAdded()
	}

	h.wg.Add(1)
	go h.handleConnection(conn)

	return nil
}

func (h *Hub) UnregisterConnection(id ConnectionID) {
	if conn, ok := h.connections.LoadAndDelete(id); ok {
		h.connCount.Add(-1)
		c := conn.(*Connection)

		h.regMu.RLock()
		closeHandlers := append([]ConnectionCloseHandler(nil), h.closeHandlers...)
		h.regMu.RUnlock()
		for _, handler := range closeHandlers {
			handler.OnConnectionClose(c)
		}

		if err := h.subManager.UnsubscribeAll(c); err != nil {
			log.Printf("Failed to unsubscribe all for connection %s: %v", id, err)
		}
		if err := c.Close(); err != nil {
			log.Printf("Failed to close connection %s: %v", id, err)
		}

		h.regMu.RLock()
		middleware := append([]Middleware(nil), h.middleware...)
		h.regMu.RUnlock()
		for _, m := range middleware {
			if cleanup, ok := m.(Cleanup); ok {
				cleanup.Cleanup(id)
			}
		}

		if h.metrics != nil {
			h.metrics.ConnectionRemoved()
		}
	}
}

func (h *Hub) GetConnection(id ConnectionID) (*Connection, bool) {
	if conn, ok := h.connections.Load(id); ok {
		return conn.(*Connection), true
	}
	return nil, false
}

func (h *Hub) CountConnections() int {
	return int(h.connCount.Load())
}

// RangeConnections iterates over all connections, calling fn for each.
// If fn returns false, iteration stops.
func (h *Hub) RangeConnections(fn func(*Connection) bool) {
	h.connections.Range(func(_, value interface{}) bool {
		conn := value.(*Connection)
		return fn(conn)
	})
}

func (h *Hub) handleConnection(conn *Connection) {
	defer h.wg.Done()
	defer h.UnregisterConnection(conn.ID())

	ctx := conn.Context()

	// Handlers run on a per-connection worker so a slow one (a subscribe that
	// waits on a cluster) cannot stall the read loop: gorilla answers pings
	// and processes pongs inside Read, and a read that does not come back
	// within PongWait drops the client. One worker keeps the connection's
	// messages strictly ordered.
	queue := make(chan *IncomingMessage, handlerQueueSize)
	stop := make(chan struct{})
	done := make(chan struct{})
	go h.runHandlers(conn, queue, stop, done)
	defer func() {
		// Drop what is still queued, let the running handler finish so a late
		// subscribe cannot land after UnsubscribeAll, then unregister.
		close(stop)
		select {
		case <-done:
		case <-time.After(handlerDrainTimeout):
			log.Printf("[WebSocket] handler for connection %s still running %v after disconnect", conn.ID(), handlerDrainTimeout)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("[WebSocket] FATAL: panic in connection %s: %v\n%s", conn.ID(), r, stack)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in handleConnection: %v", r),
				map[string]any{"connectionId": conn.ID(), "panic": r, "stack": stack},
			)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.ctx.Done():
			return
		default:
			_, data, err := conn.Read()
			if err != nil {
				if IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return
				}
				if h.metrics != nil {
					h.metrics.MessageError("read", err)
				}
				return
			}

			msg := new(IncomingMessage)
			if err := jsonv2.Unmarshal(data, msg); err != nil {
				if errData := mustSafe(NewErrorMessage(ErrInvalidMessage, "INVALID_FORMAT").Marshal()); errData != nil {
					_ = conn.Send(errData)
				}
				continue
			}

			msg.Timestamp = time.Now()

			select {
			case queue <- msg:
			case <-ctx.Done():
				return
			case <-h.ctx.Done():
				return
			}
		}
	}
}

// runHandlers is the connection's ordered handler worker. It exits when stop
// is closed, discarding messages still queued.
func (h *Hub) runHandlers(conn *Connection, queue <-chan *IncomingMessage, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ctx := conn.Context()
	for {
		select {
		case <-stop:
			return
		default:
		}
		select {
		case <-stop:
			return
		case msg := <-queue:
			h.handleMessage(ctx, conn, msg)
		}
	}
}

func (h *Hub) handleMessage(ctx context.Context, conn *Connection, msg *IncomingMessage) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			log.Printf("[WebSocket] FATAL: panic in handler for connection %s: %v\n%s", conn.ID(), r, stack)
			faults.CaptureExceptionWithContext(
				fmt.Errorf("panic in message handler: %v", r),
				map[string]any{"connectionId": conn.ID(), "panic": r, "stack": stack},
			)
			// A panic used to end the connection through handleConnection's
			// recover; keep that outcome.
			_ = conn.Close()
		}
	}()
	handler := h.buildHandlerChain()
	if err := handler(ctx, conn, msg); err != nil {
		if h.metrics != nil {
			h.metrics.MessageError("handle", err)
		}
	}
}

func (h *Hub) buildHandlerChain() MessageHandlerFunc {
	if c := h.chain.Load(); c != nil {
		return *c
	}
	handler := MessageHandlerFunc(h.router.Route)
	h.regMu.RLock()
	for i := len(h.middleware) - 1; i >= 0; i-- {
		handler = h.middleware[i].Wrap(handler)
	}
	h.regMu.RUnlock()
	h.chain.Store(&handler)
	return handler
}

// MaxMessageSize is the largest frame a connection accepts for sending; the
// event batcher sizes its frames to stay under it.
func (h *Hub) MaxMessageSize() int64 {
	return h.Config.MaxMessageSize
}

func (h *Hub) HasSubscribers(topic string) bool {
	return h.subManager.HasSubscribers(topic)
}

func (h *Hub) Broadcast(topic string, msg Message) error {
	return h.subManager.Broadcast(topic, msg)
}

func (h *Hub) BroadcastAll(msg Message) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	h.connections.Range(func(_, value interface{}) bool {
		conn := value.(*Connection)
		_ = conn.Send(data)
		return true
	})
	return nil
}

// Subscribe subscribes a connection to a topic, reporting whether the
// (connection, topic) pair is new.
func (h *Hub) Subscribe(topic string, conn *Connection) (bool, error) {
	return h.subManager.Subscribe(topic, conn)
}

// Unsubscribe unsubscribes a connection from a topic, reporting whether the
// pair actually existed.
func (h *Hub) Unsubscribe(topic string, conn *Connection) (bool, error) {
	return h.subManager.Unsubscribe(topic, conn)
}

// Subscribers returns all connections subscribed to a topic
func (h *Hub) Subscribers(topic string) []*Connection {
	return h.subManager.GetSubscribers(topic)
}

func (h *Hub) StartHeartbeat(interval time.Duration) {
	h.heartbeatStarted.Do(func() {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-h.ctx.Done():
					return
				case t := <-ticker.C:
					msg := &OutgoingMessage{
						BaseMessage: BaseMessage{
							MessageType: "heartbeat",
							Timestamp:   t,
						},
					}
					_ = h.BroadcastAll(msg)
				}
			}
		}()
	})
}

func (h *Hub) Router() Router {
	return h.router
}

func (h *Hub) Shutdown(ctx context.Context) error {
	h.cancel()

	// Close concurrently: each close may wait on a client that stopped
	// reading, and serial closes would add those waits up.
	var closing sync.WaitGroup
	h.connections.Range(func(key, value interface{}) bool {
		conn := value.(*Connection)
		closing.Add(1)
		go func() {
			defer closing.Done()
			_ = conn.Close()
		}()
		return true
	})

	done := make(chan struct{})
	go func() {
		closing.Wait()
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// mustSafe returns data if err is nil, otherwise returns nil and logs the error.
// Unlike must(), this does not panic - a connection error is not worth crashing the server.
func mustSafe(data []byte, err error) []byte {
	if err != nil {
		log.Printf("[WebSocket] Error marshaling message: %v", err)
		return nil
	}
	return data
}
