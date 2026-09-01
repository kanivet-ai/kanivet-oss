package domain

import (
	"context"
	"io"
	"time"
)

// Session represents a terminal session
type Session struct {
	ID        string
	CreatedAt time.Time
	LastUsed  time.Time
	Shell     string
	Pid       int
}

// TerminalSize represents terminal dimensions
type TerminalSize struct {
	Rows uint16
	Cols uint16
}

// SessionManager defines the interface for managing terminal sessions
type SessionManager interface {
	// CreateSession creates a new terminal session
	CreateSession(ctx context.Context, shell string, size TerminalSize) (*Session, error)

	// GetSession retrieves an existing session
	GetSession(sessionID string) (*Session, error)

	// CloseSession terminates a session
	CloseSession(sessionID string) error

	// ListSessions returns all active sessions
	ListSessions() ([]*Session, error)

	// CleanupStaleSessions removes sessions that haven't been used recently
	CleanupStaleSessions(maxAge time.Duration) error
}

// Terminal defines the interface for terminal operations
type Terminal interface {
	// Start starts the terminal process
	Start() error

	// Resize changes terminal size
	Resize(size TerminalSize) error

	// Write sends input to the terminal
	Write(data []byte) (int, error)

	// Read reads output from the terminal
	Read() ([]byte, error)

	// Close terminates the terminal
	Close() error

	// Wait waits for the terminal process to exit
	Wait() error
}

// TerminalFactory creates terminal instances
type TerminalFactory interface {
	// CreateTerminal creates a new terminal instance
	CreateTerminal(shell string, size TerminalSize) (Terminal, error)
}

// StreamOptions defines options for streaming terminal I/O
type StreamOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}
