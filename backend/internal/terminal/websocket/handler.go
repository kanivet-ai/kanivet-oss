package websocket

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"
	"unicode/utf8"

	"github.com/kanivet/backend/internal/terminal/domain"
	"github.com/kanivet/backend/internal/terminal/service"
	"github.com/kanivet/backend/internal/websocket/core"
)

// TerminalHandler handles WebSocket connections for terminal sessions
type TerminalHandler struct {
	terminalService *service.TerminalService
	activeStreams   map[string]*terminalStream
	mu              sync.RWMutex
}

type terminalStream struct {
	sessionID  string
	terminal   domain.Terminal
	connection *core.Connection
	cancelFunc context.CancelFunc
}

// NewTerminalHandler creates a new terminal WebSocket handler
func NewTerminalHandler(terminalService *service.TerminalService) *TerminalHandler {
	return &TerminalHandler{
		terminalService: terminalService,
		activeStreams:   make(map[string]*terminalStream),
	}
}

// MessageTypes returns the message types this handler supports
func (h *TerminalHandler) MessageTypes() []core.MessageType {
	return []core.MessageType{"terminal"}
}

// HandleMessage processes terminal WebSocket messages
func (h *TerminalHandler) HandleMessage(ctx context.Context, conn *core.Connection, msg *core.IncomingMessage) error {
	var payload map[string]interface{}
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	action, _ := payload["action"].(string)

	switch action {
	case "create":
		return h.handleCreate(ctx, conn, payload)
	case "resize":
		return h.handleResize(ctx, conn, payload)
	case "input":
		return h.handleInput(ctx, conn, payload)
	case "close":
		return h.handleClose(ctx, conn, payload)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
}

// handleCreate creates a new terminal session
func (h *TerminalHandler) handleCreate(ctx context.Context, conn *core.Connection, payload map[string]interface{}) error {
	shell, _ := payload["shell"].(string)
	cols, _ := payload["cols"].(float64)
	rows, _ := payload["rows"].(float64)

	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	size := domain.TerminalSize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	}

	// Create session
	session, err := h.terminalService.CreateSession(ctx, shell, size)
	if err != nil {
		return h.sendError(conn, fmt.Sprintf("Failed to create session: %v", err))
	}

	// Get terminal
	terminal, err := h.terminalService.GetTerminal(session.ID)
	if err != nil {
		if err := h.terminalService.CloseSession(session.ID); err != nil {
			log.Printf("Failed to close session %s: %v", session.ID, err)
		}
		return h.sendError(conn, fmt.Sprintf("Failed to get terminal: %v", err))
	}

	// Create stream context
	streamCtx, cancel := context.WithCancel(ctx)

	// Store stream
	h.mu.Lock()
	h.activeStreams[session.ID] = &terminalStream{
		sessionID:  session.ID,
		terminal:   terminal,
		connection: conn,
		cancelFunc: cancel,
	}
	h.mu.Unlock()

	// Send success response
	response := map[string]interface{}{
		"type":      "created",
		"sessionId": session.ID,
	}
	msg := core.NewOutgoingMessage("terminal", response)
	msgData, _ := msg.Marshal()
	if err := conn.Send(msgData); err != nil {
		h.cleanupStream(session.ID)
		return fmt.Errorf("failed to send response: %w", err)
	}

	// Start output streaming
	go h.streamOutput(streamCtx, session.ID)

	// Monitor connection close
	go h.monitorConnection(conn, session.ID)

	log.Printf("Created terminal session %s for connection %s", session.ID, conn.ID())
	return nil
}

// handleResize resizes a terminal
func (h *TerminalHandler) handleResize(ctx context.Context, conn *core.Connection, payload map[string]interface{}) error {
	sessionID, _ := payload["sessionId"].(string)
	cols, _ := payload["cols"].(float64)
	rows, _ := payload["rows"].(float64)

	if sessionID == "" {
		return h.sendError(conn, "Session ID required")
	}

	h.mu.RLock()
	stream, exists := h.activeStreams[sessionID]
	h.mu.RUnlock()

	if !exists {
		return h.sendError(conn, "Session not found")
	}

	size := domain.TerminalSize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	}

	if err := stream.terminal.Resize(size); err != nil {
		return h.sendError(conn, fmt.Sprintf("Failed to resize: %v", err))
	}

	return nil
}

// handleInput sends input to a terminal
func (h *TerminalHandler) handleInput(ctx context.Context, conn *core.Connection, payload map[string]interface{}) error {
	sessionID, _ := payload["sessionId"].(string)
	data, _ := payload["data"].(string)

	if sessionID == "" {
		return h.sendError(conn, "Session ID required")
	}

	h.mu.RLock()
	stream, exists := h.activeStreams[sessionID]
	h.mu.RUnlock()

	if !exists {
		return h.sendError(conn, "Session not found")
	}

	if _, err := stream.terminal.Write([]byte(data)); err != nil {
		return h.sendError(conn, fmt.Sprintf("Failed to write: %v", err))
	}

	return nil
}

// handleClose closes a terminal session
func (h *TerminalHandler) handleClose(ctx context.Context, conn *core.Connection, payload map[string]interface{}) error {
	sessionID, _ := payload["sessionId"].(string)

	if sessionID == "" {
		return h.sendError(conn, "Session ID required")
	}

	h.cleanupStream(sessionID)
	return nil
}

// streamOutput streams terminal output to the WebSocket connection
func (h *TerminalHandler) streamOutput(ctx context.Context, sessionID string) {
	h.mu.RLock()
	stream, exists := h.activeStreams[sessionID]
	h.mu.RUnlock()

	if !exists {
		return
	}

	defer h.cleanupStream(sessionID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			data, err := stream.terminal.Read()
			if err != nil {
				log.Printf("Terminal read error for session %s: %v", sessionID, err)
				if err := h.sendError(stream.connection, fmt.Sprintf("Terminal closed: %v", err)); err != nil {
					log.Printf("Failed to send error for session %s: %v", sessionID, err)
				}
				return
			}

			if len(data) > 0 {
				var outputData string
				var encoding string
				if utf8.Valid(data) {
					outputData = string(data)
					encoding = "utf8"
				} else {
					outputData = base64.StdEncoding.EncodeToString(data)
					encoding = "base64"
				}
				response := map[string]interface{}{
					"type":      "output",
					"sessionId": sessionID,
					"data":      outputData,
					"encoding":  encoding,
				}
				msg := core.NewOutgoingMessage("terminal", response)
				msgData, _ := msg.Marshal()
				if err := stream.connection.Send(msgData); err != nil {
					log.Printf("Failed to send output for session %s: %v", sessionID, err)
					return
				}
			}
		}
	}
}

// monitorConnection monitors the WebSocket connection and cleans up when it closes
func (h *TerminalHandler) monitorConnection(conn *core.Connection, sessionID string) {
	<-conn.Context().Done()
	h.cleanupStream(sessionID)
}

// cleanupStream cleans up a terminal stream
func (h *TerminalHandler) cleanupStream(sessionID string) {
	h.mu.Lock()
	stream, exists := h.activeStreams[sessionID]
	if exists {
		delete(h.activeStreams, sessionID)
	}
	h.mu.Unlock()

	if exists {
		// Cancel stream context
		stream.cancelFunc()

		// Close session
		if err := h.terminalService.CloseSession(sessionID); err != nil {
			log.Printf("Error closing session %s: %v", sessionID, err)
		}

		log.Printf("Cleaned up terminal stream for session %s", sessionID)
	}
}

// sendError sends an error message to the connection
func (h *TerminalHandler) sendError(conn *core.Connection, errMsg string) error {
	response := map[string]interface{}{
		"type":  "error",
		"error": errMsg,
	}
	msg := core.NewOutgoingMessage("terminal", response)
	msgData, _ := msg.Marshal()
	return conn.Send(msgData)
}

// Cleanup cleans up all active streams (called on shutdown)
func (h *TerminalHandler) Cleanup() {
	h.mu.Lock()
	sessionIDs := make([]string, 0, len(h.activeStreams))
	for id := range h.activeStreams {
		sessionIDs = append(sessionIDs, id)
	}
	h.mu.Unlock()

	for _, id := range sessionIDs {
		h.cleanupStream(id)
	}
}
