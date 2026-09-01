package transport

import (
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kanivet/backend/internal/websocket/core"
)

type ServerConfig struct {
	ReadBufferSize    int
	WriteBufferSize   int
	HandshakeTimeout  time.Duration
	EnableCompression bool
	CheckOrigin       func(r *http.Request) bool
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ReadBufferSize:    1024 * 1024,
		WriteBufferSize:   1024 * 1024,
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" || origin == "file://" || origin == "null" {
				return true
			}
			if strings.HasPrefix(origin, "http://localhost:") || origin == "http://localhost:3000" {
				return true
			}
			return false
		},
	}
}

type Server struct {
	hub        *core.Hub
	upgrader   websocket.Upgrader
	connConfig *core.ConnectionConfig
}

func NewServer(hub *core.Hub, config *ServerConfig) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}

	connConfig := &core.ConnectionConfig{
		MaxMessageSize:    hub.Config.MaxMessageSize,
		WriteTimeout:      10 * time.Second,
		PingInterval:      hub.Config.HeartbeatInterval,
		PongWait:          hub.Config.HeartbeatInterval * 2,
		SendChannelSize:   hub.Config.SendChannelSize,
		EnableCompression: hub.Config.EnableCompression,
	}

	return &Server{
		hub:        hub,
		connConfig: connConfig,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    config.ReadBufferSize,
			WriteBufferSize:   config.WriteBufferSize,
			HandshakeTimeout:  config.HandshakeTimeout,
			EnableCompression: config.EnableCompression,
			CheckOrigin:       config.CheckOrigin,
		},
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.ServeHTTPWithMetadata(w, r, nil)
}

// ServeHTTPWithMetadata upgrades the connection and sets metadata on it
func (s *Server) ServeHTTPWithMetadata(w http.ResponseWriter, r *http.Request, metadata map[string]interface{}) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	wsConn := core.NewConnection("", conn, s.connConfig)

	// Set any provided metadata
	for key, value := range metadata {
		wsConn.SetMetadata(key, value)
	}

	if err := s.hub.RegisterConnection(wsConn); err != nil {
		_ = conn.Close()
		return
	}
}
