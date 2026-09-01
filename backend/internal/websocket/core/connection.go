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
	pingTicker   *time.Ticker
	config       *ConnectionConfig
	closeOnce    sync.Once
}

type ConnectionConfig struct {
	MaxMessageSize    int64
	WriteTimeout      time.Duration
	PingInterval      time.Duration
	PongWait          time.Duration
	SendChannelSize   int
	EnableCompression bool
}

func DefaultConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		MaxMessageSize:    1024 * 1024, // 1MB
		WriteTimeout:      10 * time.Second,
		PingInterval:      30 * time.Second,
		PongWait:          60 * time.Second,
		SendChannelSize:   256,
		EnableCompression: true,
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

	ctx, cancel := context.WithCancel(context.Background())
	c := &Connection{
		id:           id,
		conn:         conn,
		ctx:          ctx,
		cancel:       cancel,
		writeTimeout: config.WriteTimeout,
		createdAt:    time.Now(),
		sendChan:     make(chan []byte, config.SendChannelSize),
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

	select {
	case c.sendChan <- data:
		return nil
	case <-c.ctx.Done():
		return ErrConnectionClosed
	default:
		// Channel is full - backpressure
		return ErrRateLimitExceeded
	}
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

func (c *Connection) cleanup() {
	c.closeOnce.Do(func() {
		c.pingTicker.Stop()
		c.writeMu.Lock()
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err == nil {
			_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
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
