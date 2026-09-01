package websocket

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/kanivet/backend/internal/k8s/watcher"
	"github.com/kanivet/backend/internal/topics"
	"github.com/kanivet/backend/internal/websocket/core"
	"github.com/kanivet/backend/internal/websocket/transport"
)

type bucket struct {
	tokens   int
	lastSeen time.Time
}

type simpleRateLimiter struct {
	mu         sync.RWMutex
	buckets    map[core.ConnectionID]*bucket
	maxTokens  int
	refillRate time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
}

func newSimpleRateLimiter(maxTokens int, refillRate time.Duration) *simpleRateLimiter {
	s := &simpleRateLimiter{
		buckets:    make(map[core.ConnectionID]*bucket),
		maxTokens:  maxTokens,
		refillRate: refillRate,
		stopCh:     make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

func (s *simpleRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.removeStale()
		}
	}
}

func (s *simpleRateLimiter) removeStale() {
	s.mu.Lock()
	defer s.mu.Unlock()

	staleThreshold := time.Now().Add(-10 * time.Minute)
	for id, b := range s.buckets {
		if b.lastSeen.Before(staleThreshold) {
			delete(s.buckets, id)
		}
	}
}

func (s *simpleRateLimiter) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *simpleRateLimiter) Allow(connID core.ConnectionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	b, exists := s.buckets[connID]

	if !exists {
		s.buckets[connID] = &bucket{
			tokens:   s.maxTokens - 1,
			lastSeen: now,
		}
		return true
	}

	elapsed := now.Sub(b.lastSeen)
	tokensToAdd := int(elapsed / s.refillRate)

	if tokensToAdd > 0 {
		b.tokens += tokensToAdd
		if b.tokens > s.maxTokens {
			b.tokens = s.maxTokens
		}
		b.lastSeen = now
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

func (s *simpleRateLimiter) Reset(connID core.ConnectionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buckets, connID)
}

type Server struct {
	hub         *core.Hub
	server      *transport.Server
	rateLimiter *simpleRateLimiter
	subManager  *transport.DefaultSubscriptionManager
}

func NewServer() *Server {
	router := transport.NewRouter()
	subManager := transport.NewSubscriptionManager()

	hubConfig := core.DefaultHubConfig()
	hub := core.NewHub(hubConfig, router, subManager)

	rateLimiter := newSimpleRateLimiter(10, 100*time.Millisecond)

	hub.AddMiddleware(core.RecoveryMiddleware())
	hub.AddMiddleware(core.LoggingMiddleware(nil))
	hub.AddMiddleware(core.RateLimitMiddleware(rateLimiter, "subscribe", "unsubscribe", "ping", "pong"))

	hub.StartHeartbeat(10 * time.Second)

	server := transport.NewServer(hub, transport.DefaultServerConfig())

	return &Server{
		hub:         hub,
		server:      server,
		rateLimiter: rateLimiter,
		subManager:  subManager,
	}
}

func (s *Server) RegisterHandler(messageType core.MessageType, handler core.MessageHandler) {
	_ = s.hub.Router().Register(messageType, handler)
	if closeHandler, ok := handler.(core.ConnectionCloseHandler); ok {
		s.hub.AddConnectionCloseHandler(closeHandler)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.server.ServeHTTP(w, r)
}

// ServeHTTPWithMetadata upgrades the connection and sets metadata on it
func (s *Server) ServeHTTPWithMetadata(w http.ResponseWriter, r *http.Request, metadata map[string]interface{}) {
	s.server.ServeHTTPWithMetadata(w, r, metadata)
}

func (s *Server) Hub() *core.Hub {
	return s.hub
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.rateLimiter.Stop()
	return s.hub.Shutdown(ctx)
}

func (s *Server) GetBroadcaster() *WatcherBroadcaster {
	return NewWatcherBroadcaster(s.hub)
}

// SetupDefaultHandlers configures the default message handlers for Kubernetes watching
func (s *Server) SetupDefaultHandlers(watcherService *watcher.Service) {
	// Watch refcounts mirror live (connection, topic) pairs exactly: StartWatch
	// runs only when the subscription manager reports a new pair, and every
	// decrement flows through this single removal callback (explicit
	// unsubscribe and disconnect both end up here).
	s.subManager.SetOnSubscriptionRemoved(func(topic string, _ *core.Connection) {
		if cluster, group, version, kind, namespace, ok := topics.ParseItemsTopic(topic); ok {
			log.Printf("[WS] Subscription removed for %s, releasing watch ref", topic)
			watcherService.StopWatch(cluster, group, version, kind, namespace)
		}
	})

	// Re-push the cached snapshot when a client misses updates (backpressure
	// drop) or re-subscribes to a topic it already had. The pending flag is
	// cleared only after Resync completes and a cooldown keeps a saturated
	// client from turning resyncs into a feedback loop.
	var resyncMu sync.Mutex
	resyncPending := make(map[string]bool)
	lastResync := make(map[string]time.Time)
	requestResync := func(topic string) {
		if _, _, _, _, _, ok := topics.ParseItemsTopic(topic); !ok {
			return
		}
		resyncMu.Lock()
		if resyncPending[topic] || time.Since(lastResync[topic]) < 2*time.Second {
			resyncMu.Unlock()
			return
		}
		resyncPending[topic] = true
		resyncMu.Unlock()
		go func() {
			time.Sleep(250 * time.Millisecond)
			watcherService.Resync(topic)
			resyncMu.Lock()
			lastResync[topic] = time.Now()
			delete(resyncPending, topic)
			resyncMu.Unlock()
		}()
	}
	s.subManager.SetOnBackpressureDrop(func(topic string, _ *core.Connection) {
		requestResync(topic)
	})

	// Handle subscribe messages
	s.RegisterHandler("subscribe", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		var payload map[string]interface{}
		if err := msg.UnmarshalPayload(&payload); err != nil {
			return err
		}

		// Handle unsubscribe within subscribe channel for backward-compatibility
		if action, _ := payload["action"].(string); action == "unsubscribe" {
			if topic, _ := payload["topic"].(string); topic != "" {
				_, err := s.hub.Unsubscribe(topic, conn)
				return err
			}
			return nil
		}

		subscribeItems := func(topic, cluster, group, version, kind, namespace, sortBy, sortOrder string) error {
			if sortBy == "" {
				sortBy = "age"
			}
			if sortOrder == "" {
				sortOrder = "desc"
			}
			added, err := s.hub.Subscribe(topic, conn)
			if err != nil {
				return err
			}
			if !added {
				requestResync(topic)
				return nil
			}
			if err := watcherService.StartWatch(cluster, group, version, kind, namespace, sortBy, sortOrder); err != nil {
				log.Printf("[WS] Failed to start watcher for topic %s: %v", topic, err)
				_, _ = s.hub.Unsubscribe(topic, conn)
				return err
			}
			return nil
		}

		// Subscribe directly to topic or via structured items payload
		if topic, _ := payload["topic"].(string); topic != "" {
			if cluster, group, version, kind, namespace, ok := topics.ParseItemsTopic(topic); ok {
				sortBy, _ := payload["sortBy"].(string)
				sortOrder, _ := payload["sortOrder"].(string)
				return subscribeItems(topic, cluster, group, version, kind, namespace, sortBy, sortOrder)
			}
			_, err := s.hub.Subscribe(topic, conn)
			return err
		}

		channel, _ := payload["channel"].(string)
		if channel == "items" {
			cluster, _ := payload["cluster"].(string)
			group, _ := payload["group"].(string)
			version, _ := payload["version"].(string)
			kind, _ := payload["kind"].(string)
			namespace, _ := payload["namespace"].(string)
			sortBy, _ := payload["sortBy"].(string)
			sortOrder, _ := payload["sortOrder"].(string)
			topic := topics.BuildItemsTopic(cluster, group, version, kind, namespace)
			return subscribeItems(topic, cluster, group, version, kind, namespace, sortBy, sortOrder)
		}

		return nil
	}))

	// Handle explicit unsubscribe messages
	s.RegisterHandler("unsubscribe", core.MessageHandlerFunc(func(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
		var payload map[string]interface{}
		if err := msg.UnmarshalPayload(&payload); err == nil {
			if topic, _ := payload["topic"].(string); topic != "" {
				_, err := s.hub.Unsubscribe(topic, conn)
				return err
			}
		}
		return nil
	}))
}
