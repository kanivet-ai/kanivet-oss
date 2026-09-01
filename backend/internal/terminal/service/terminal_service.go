package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kanivet/backend/internal/terminal/domain"
)

// TerminalService manages terminal sessions
type TerminalService struct {
	factory     domain.TerminalFactory
	sessions    map[string]*sessionEntry
	mu          sync.RWMutex
	maxSessions int
}

type sessionEntry struct {
	session  *domain.Session
	terminal domain.Terminal
}

// NewTerminalService creates a new terminal service
func NewTerminalService(factory domain.TerminalFactory) *TerminalService {
	return &TerminalService{
		factory:     factory,
		sessions:    make(map[string]*sessionEntry),
		maxSessions: 10, // Configurable limit
	}
}

// CreateSession creates a new terminal session
func (s *TerminalService) CreateSession(ctx context.Context, shell string, size domain.TerminalSize) (*domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check session limit
	if len(s.sessions) >= s.maxSessions {
		return nil, fmt.Errorf("maximum number of sessions (%d) reached", s.maxSessions)
	}

	// Create terminal
	terminal, err := s.factory.CreateTerminal(shell, size)
	if err != nil {
		return nil, fmt.Errorf("failed to create terminal: %w", err)
	}

	// Start terminal
	if err := terminal.Start(); err != nil {
		if closeErr := terminal.Close(); closeErr != nil {
			log.Printf("Failed to close terminal after start error: %v", closeErr)
		}
		return nil, fmt.Errorf("failed to start terminal: %w", err)
	}

	// Create session
	session := &domain.Session{
		ID:        uuid.New().String(),
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		Shell:     shell,
	}

	// Store session
	s.sessions[session.ID] = &sessionEntry{
		session:  session,
		terminal: terminal,
	}

	// Start cleanup goroutine for this session
	go s.monitorSession(session.ID)

	log.Printf("Created terminal session %s with shell %s", session.ID, shell)
	return session, nil
}

// GetSession retrieves an existing session
func (s *TerminalService) GetSession(sessionID string) (*domain.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Update last used time
	entry.session.LastUsed = time.Now()
	return entry.session, nil
}

// GetTerminal retrieves the terminal for a session
func (s *TerminalService) GetTerminal(sessionID string) (domain.Terminal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Update last used time
	entry.session.LastUsed = time.Now()
	return entry.terminal, nil
}

// CloseSession terminates a session
func (s *TerminalService) CloseSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Close terminal
	if err := entry.terminal.Close(); err != nil {
		log.Printf("Error closing terminal for session %s: %v", sessionID, err)
	}

	// Remove session
	delete(s.sessions, sessionID)
	log.Printf("Closed terminal session %s", sessionID)
	return nil
}

// ListSessions returns all active sessions
func (s *TerminalService) ListSessions() ([]*domain.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]*domain.Session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		sessions = append(sessions, entry.session)
	}
	return sessions, nil
}

// monitorSession monitors a session and cleans it up when the terminal exits
func (s *TerminalService) monitorSession(sessionID string) {
	// Get terminal
	s.mu.RLock()
	entry, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if !exists {
		return
	}

	// Wait for terminal to exit
	if err := entry.terminal.Wait(); err != nil {
		log.Printf("Terminal process for session %s exited with error: %v", sessionID, err)
	} else {
		log.Printf("Terminal process for session %s exited normally", sessionID)
	}

	// Clean up session
	if err := s.CloseSession(sessionID); err != nil {
		log.Printf("Failed to close session %s: %v", sessionID, err)
	}
}

// StartCleanupWorker starts a background worker that monitors sessions
// We no longer clean up based on idle time - sessions persist until explicitly closed
func (s *TerminalService) StartCleanupWorker(ctx context.Context) {
	// This worker now only exists to handle context cancellation
	// Sessions will only be cleaned up when explicitly closed or when the terminal process exits
	<-ctx.Done()
	log.Println("Terminal cleanup worker shutting down")
}
