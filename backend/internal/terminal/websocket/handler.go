package websocket

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
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
	// input carries the client's keystrokes to the PTY in order. A
	// per-session goroutine writes them, so a child that does not read its
	// stdin cannot stall the connection's read loop.
	input chan []byte
}

const (
	// inputQueueSize is how many input messages may wait for the PTY.
	inputQueueSize = 256
	// inputEnqueueTimeout bounds how long the read loop waits for room in
	// a full input queue before reporting the terminal as busy.
	inputEnqueueTimeout = 2 * time.Second
	// sendRetryBound is how long output waits for a full send buffer to
	// drain before the session is given up.
	sendRetryBound = 30 * time.Second
)

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
	stream := &terminalStream{
		sessionID:  session.ID,
		terminal:   terminal,
		connection: conn,
		cancelFunc: cancel,
		input:      make(chan []byte, inputQueueSize),
	}
	h.activeStreams[session.ID] = stream
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

	// Write input to the PTY in order, off the connection's read loop
	go h.writeInput(streamCtx, stream)

	// Monitor connection close
	go h.monitorConnection(streamCtx, conn, session.ID)

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

	timer := time.NewTimer(inputEnqueueTimeout)
	defer timer.Stop()
	select {
	case stream.input <- []byte(data):
		return nil
	case <-timer.C:
		return h.sendError(conn, "Failed to write: terminal is not accepting input")
	}
}

// writeInput writes queued input to the PTY until the stream ends. A write
// error is reported to the client the way a synchronous one used to be.
func (h *TerminalHandler) writeInput(ctx context.Context, stream *terminalStream) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-stream.input:
			if _, err := stream.terminal.Write(data); err != nil {
				if ctx.Err() != nil {
					return
				}
				if err := h.sendError(stream.connection, fmt.Sprintf("Failed to write: %v", err)); err != nil {
					log.Printf("Failed to report write error for session %s: %v", stream.sessionID, err)
				}
			}
		}
	}
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

// maxOutputMessage bounds how much already-read output one message carries.
const maxOutputMessage = 64 * 1024

// streamOutput streams terminal output to the WebSocket connection. Reads run
// on their own goroutine, so output that arrives while a message is being sent
// goes out together in the next one rather than as one message per read.
func (h *TerminalHandler) streamOutput(ctx context.Context, sessionID string) {
	h.mu.RLock()
	stream, exists := h.activeStreams[sessionID]
	h.mu.RUnlock()

	if !exists {
		return
	}

	defer h.cleanupStream(sessionID)

	chunks := make(chan []byte, 64)
	readErr := make(chan error, 1)
	go func() {
		for {
			data, err := stream.terminal.Read()
			if err != nil {
				readErr <- err
				return
			}
			if len(data) == 0 {
				continue
			}
			select {
			case chunks <- data:
			case <-ctx.Done():
				return
			}
		}
	}()

	var carry utf8Carry
	send := func(data []byte) bool {
		if err := h.sendOutput(ctx, stream.connection, sessionID, data); err != nil {
			log.Printf("Failed to send output for session %s: %v", sessionID, err)
			return false
		}
		return true
	}
	// drain appends the output already read, up to maxOutputMessage. It
	// copies before appending: buf belongs to the terminal that returned it.
	drain := func(buf []byte) []byte {
		owned := false
		for len(buf) < maxOutputMessage {
			select {
			case more := <-chunks:
				if !owned {
					buf = append(make([]byte, 0, len(buf)+len(more)), buf...)
					owned = true
				}
				buf = append(buf, more...)
			default:
				return buf
			}
		}
		return buf
	}

	for {
		select {
		case <-ctx.Done():
			return
		case data := <-chunks:
			if out := carry.next(drain(data)); len(out) > 0 && !send(out) {
				return
			}
		case err := <-readErr:
			// The reader queued everything it read before failing.
			for {
				rest := drain(nil)
				if len(rest) == 0 {
					break
				}
				if out := carry.next(rest); len(out) > 0 && !send(out) {
					return
				}
			}
			if len(carry.pending) > 0 && !send(carry.pending) {
				return
			}
			log.Printf("Terminal read error for session %s: %v", sessionID, err)
			if err := h.sendError(stream.connection, fmt.Sprintf("Terminal closed: %v", err)); err != nil {
				log.Printf("Failed to send error for session %s: %v", sessionID, err)
			}
			return
		}
	}
}

// sendOutput sends output as text when it is valid UTF-8 and as base64
// otherwise, which the terminal decodes back to the original bytes.
func (h *TerminalHandler) sendOutput(ctx context.Context, conn *core.Connection, sessionID string, data []byte) error {
	outputData, encoding := string(data), "utf8"
	if !utf8.Valid(data) {
		outputData, encoding = base64.StdEncoding.EncodeToString(data), "base64"
	}
	msg := core.NewOutgoingMessage("terminal", map[string]interface{}{
		"type":      "output",
		"sessionId": sessionID,
		"data":      outputData,
		"encoding":  encoding,
	})
	msgData, _ := msg.Marshal()
	return sendWithBackoff(ctx, conn, msgData)
}

// sendWithBackoff sends data, waiting while the connection's send buffer is
// full. A full buffer means the client is slow, which the PTY already handles
// by blocking, so it must not end the session. It gives up when the
// connection is closed, ctx ends, or the buffer stays full for sendRetryBound.
func sendWithBackoff(ctx context.Context, conn *core.Connection, data []byte) error {
	deadline := time.Now().Add(sendRetryBound)
	delay := time.Millisecond
	for {
		err := conn.Send(data)
		if !errors.Is(err, core.ErrRateLimitExceeded) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 50*time.Millisecond {
			delay *= 2
		}
	}
}

// utf8Carry holds back a multibyte character that a read cut short until the
// read that completes it. Sent split, both halves were invalid UTF-8, so both
// chunks went out as base64 and every character in them was garbled.
type utf8Carry struct {
	pending []byte
}

// next returns what to send for data, the output read since the last call.
func (c *utf8Carry) next(data []byte) []byte {
	if len(c.pending) > 0 {
		data = append(c.pending, data...)
	}
	out, rest := splitIncompleteRune(data)
	c.pending = append([]byte(nil), rest...)
	return out
}

// splitIncompleteRune splits data before a trailing UTF-8 sequence that is
// cut short. Anything else, invalid bytes included, stays in the first part.
func splitIncompleteRune(data []byte) (complete, rest []byte) {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if data[i] < utf8.RuneSelf {
			break
		}
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				return data[:i], data[i:]
			}
			break
		}
	}
	return data, nil
}

// monitorConnection monitors the WebSocket connection and cleans up when it closes
func (h *TerminalHandler) monitorConnection(ctx context.Context, conn *core.Connection, sessionID string) {
	select {
	case <-conn.Context().Done():
		h.cleanupStream(sessionID)
	case <-ctx.Done():
		// The session ended on its own; nothing left to watch.
	}
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
