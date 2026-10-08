package core

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// IsCloseError checks if error is a websocket close error with specific codes
func IsCloseError(err error, codes ...int) bool {
	return websocket.IsCloseError(err, codes...)
}

type ConnectionID string

type ConnectionState int32

const (
	StateConnecting ConnectionState = iota
	StateConnected
	StateClosing
	StateClosed
)

type Connection struct {
	id           ConnectionID
	conn         *websocket.Conn
	state        atomic.Int32
	ctx          context.Context
	cancel       context.CancelFunc
	writeMu      sync.Mutex
	writeTimeout time.Duration
	metadata     sync.Map
	createdAt    time.Time
	lastActivity atomic.Int64
	sendChan     chan []byte
	// queuedBytes is the payload bytes sitting in sendChan or being written;
	// Send refuses a frame once it would pass config.MaxQueuedBytes.
	queuedBytes atomic.Int64
	maxQueued   int64
	pingTicker  *time.Ticker
	config      *ConnectionConfig
	closeOnce   sync.Once
}

type ConnectionConfig struct {
	MaxMessageSize  int64
	WriteTimeout    time.Duration
	PingInterval    time.Duration
	PongWait        time.Duration
	SendChannelSize int
	// MaxQueuedBytes caps the bytes queued for sending, in addition to the
	// SendChannelSize frame count: a stalled client must not pin
	// SendChannelSize x MaxMessageSize of memory. Zero means
	// DefaultMaxQueuedBytes; it is raised to MaxMessageSize if lower so one
	// maximum-size frame can always be queued.
	MaxQueuedBytes    int64
	EnableCompression bool
}

// DefaultMaxQueuedBytes is the default per-connection send queue byte cap.
const DefaultMaxQueuedBytes = 64 << 20

func DefaultConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		MaxMessageSize:    1024 * 1024, // 1MB
		WriteTimeout:      10 * time.Second,
		PingInterval:      30 * time.Second,
		PongWait:          60 * time.Second,
		SendChannelSize:   256,
		EnableCompression: false,
	}
}

func NewConnection(id ConnectionID, conn *websocket.Conn, config *ConnectionConfig) *Connection {
	if config == nil {
		config = DefaultConnectionConfig()
	}

	// Validate configuration
	if config.MaxMessageSize <= 0 {
		config.MaxMessageSize = 1024 * 1024
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 10 * time.Second
	}
	if config.PingInterval <= 0 {
		config.PingInterval = 30 * time.Second
	}
	if config.PongWait <= 0 {
		config.PongWait = 60 * time.Second
	}
	if config.SendChannelSize <= 0 {
		config.SendChannelSize = 256
	}

	// Held per connection rather than written back: the config is shared by
	// every connection of a server.
	maxQueued := config.MaxQueuedBytes
	if maxQueued <= 0 {
		maxQueued = DefaultMaxQueuedBytes
	}
	if maxQueued < config.MaxMessageSize {
		maxQueued = config.MaxMessageSize
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Connection{
		id:           id,
		conn:         conn,
		ctx:          ctx,
		cancel:       cancel,
		writeTimeout: config.WriteTimeout,
		createdAt:    time.Now(),
		sendChan:     make(chan []byte, config.SendChannelSize),
		maxQueued:    maxQueued,
		pingTicker:   time.NewTicker(config.PingInterval),
		config:       config,
	}

	// Configure connection
	conn.SetReadLimit(config.MaxMessageSize)
	if err := conn.SetReadDeadline(time.Now().Add(config.PongWait)); err != nil {
		log.Printf("Failed to set initial read deadline: %v", err)
	}
	conn.SetPongHandler(func(string) error {
		if err := conn.SetReadDeadline(time.Now().Add(config.PongWait)); err != nil {
			log.Printf("Failed to set read deadline in pong handler: %v", err)
		}
		c.UpdateActivity()
		return nil
	})

	if config.EnableCompression {
		conn.EnableWriteCompression(true)
	}

	c.state.Store(int32(StateConnected))
	c.UpdateActivity()

	// Start write pump
	go c.writePump()

	return c
}

func (c *Connection) ID() ConnectionID {
	return c.id
}

func (c *Connection) State() ConnectionState {
	return ConnectionState(c.state.Load())
}

func (c *Connection) Context() context.Context {
	return c.ctx
}

func (c *Connection) SetMetadata(key string, value interface{}) {
	c.metadata.Store(key, value)
}

func (c *Connection) GetMetadata(key string) (interface{}, bool) {
	return c.metadata.Load(key)
}

func (c *Connection) UpdateActivity() {
	c.lastActivity.Store(time.Now().UnixNano())
}

func (c *Connection) LastActivity() time.Time {
	return time.Unix(0, c.lastActivity.Load())
}

func (c *Connection) Send(data []byte) error {
	if c.State() != StateConnected {
		return ErrConnectionClosed
	}

	if int64(len(data)) > c.config.MaxMessageSize {
		return ErrInvalidMessage
	}

	// Reserve the bytes first so concurrent senders cannot jointly overshoot
	// the cap. A refusal is the same backpressure as a full queue.
	n := int64(len(data))
	if c.queuedBytes.Add(n) > c.maxQueued {
		c.queuedBytes.Add(-n)
		return ErrRateLimitExceeded
	}

	select {
	case c.sendChan <- data:
		return nil
	case <-c.ctx.Done():
		c.queuedBytes.Add(-n)
		return ErrConnectionClosed
	default:
		// Channel is full - backpressure
		c.queuedBytes.Add(-n)
		return ErrRateLimitExceeded
	}
}

// QueuedBytes reports the bytes currently queued for sending.
func (c *Connection) QueuedBytes() int64 {
	return c.queuedBytes.Load()
}

func (c *Connection) SendBinary(data []byte) error {
	if c.State() != StateConnected {
		return ErrConnectionClosed
	}

	if int64(len(data)) > c.config.MaxMessageSize {
		return ErrInvalidMessage
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		log.Printf("Failed to set write deadline: %v", err)
		return err
	}
	err := c.conn.WriteMessage(websocket.BinaryMessage, data)
	if err == nil {
		c.UpdateActivity()
	}
	return err
}

func (c *Connection) writePump() {
	defer c.cleanup()

	for {
		select {
		case message := <-c.sendChan:
			c.writeMu.Lock()
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
				log.Printf("Failed to set write deadline: %v", err)
				c.writeMu.Unlock()
				return
			}
			err := c.conn.WriteMessage(websocket.TextMessage, message)
			c.writeMu.Unlock()
			c.queuedBytes.Add(-int64(len(message)))

			if err != nil {
				return
			}
			c.UpdateActivity()

		case <-c.pingTicker.C:
			c.writeMu.Lock()
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
				c.writeMu.Unlock()
				return
			}
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.writeMu.Unlock()
			if err != nil {
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}

// closeGrace is how long cleanup waits for an in-flight write before it gives
// up on the close frame and breaks the write.
const closeGrace = 250 * time.Millisecond

func (c *Connection) cleanup() {
	c.closeOnce.Do(func() {
		c.pingTicker.Stop()
		// writePump may be mid-write to a client that stopped reading, which
		// holds writeMu for up to the write timeout. Give it a moment, then
		// break the write by expiring the socket's deadline (net.Conn
		// deadlines are safe to set concurrently) and skip the close frame.
		sendClose := true
		if !c.writeMu.TryLock() {
			locked := make(chan struct{})
			go func() {
				c.writeMu.Lock()
				close(locked)
			}()
			timer := time.NewTimer(closeGrace)
			select {
			case <-locked:
				timer.Stop()
			case <-timer.C:
				sendClose = false
				_ = c.conn.UnderlyingConn().SetWriteDeadline(time.Now())
				<-locked
			}
		}
		if sendClose {
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err == nil {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			}
		}
		c.writeMu.Unlock()
		if err := c.conn.Close(); err != nil {
			log.Printf("Failed to close connection: %v", err)
		}
	})
}

func (c *Connection) Close() error {
	if !c.state.CompareAndSwap(int32(StateConnected), int32(StateClosing)) {
		return nil
	}
	c.cancel()
	c.cleanup()
	c.state.Store(int32(StateClosed))
	return nil
}

func (c *Connection) Read() (messageType int, data []byte, err error) {
	messageType, data, err = c.conn.ReadMessage()
	if err == nil {
		c.UpdateActivity()
		if setErr := c.conn.SetReadDeadline(time.Now().Add(c.config.PongWait)); setErr != nil {
			log.Printf("Failed to set read deadline after read: %v", setErr)
		}
	}
	return
}
